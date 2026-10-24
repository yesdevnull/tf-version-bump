package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"go.yaml.in/yaml/v3"
)

// FromVersions is a version filter that YAML may give as one string or a list of strings.
type FromVersions []string

// UnmarshalYAML accepts a string scalar or a sequence of string scalars, rejecting numbers and
// booleans so a bare 1.5 cannot silently become "1.5".
func (f *FromVersions) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.SequenceNode:
		for _, item := range value.Content {
			if item.ShortTag() != "!!str" {
				return errors.New("version filter array contains non-string values")
			}
		}
		var versions []string
		if err := value.Decode(&versions); err != nil {
			return fmt.Errorf("version filter array contains non-string values: %w", err)
		}
		*f = versions
		return nil
	case yaml.ScalarNode:
		if value.Tag != "!!str" {
			return fmt.Errorf("version filter must be a string or array of strings, got %s", value.Tag)
		}
		if value.Value == "" {
			*f = FromVersions{}
		} else {
			*f = FromVersions{value.Value}
		}
		return nil
	default:
		return fmt.Errorf("version filter must be either a string or an array of strings, got node kind %v", value.Kind)
	}
}

// ModuleUpdate is one module source and its target version, from -module flags or a config entry.
type ModuleUpdate struct {
	Source         string       `yaml:"source"`          // required
	Version        string       `yaml:"version"`         // required
	From           FromVersions `yaml:"from"`            // update only from these versions
	IgnoreVersions FromVersions `yaml:"ignore_versions"` // never update from these versions
	IgnoreModules  []string     `yaml:"ignore_modules"`  // name patterns, optionally '<branch>/<name>', as written

	// resolvedIgnoreModules holds the IgnoreModules patterns that apply to the supplied branch.
	// Filtering reads this; diagnostics report IgnoreModules as written.
	resolvedIgnoreModules []string
}

// ProviderUpdate is one provider and its target version for required_providers.
type ProviderUpdate struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
}

// Config is a YAML configuration file; every key is optional, but see declaresNoUpdates.
// docs/CONFIGURATION.md documents the format and examples/ holds sample files.
type Config struct {
	TerraformVersion string           `yaml:"terraform_version"`
	Providers        []ProviderUpdate `yaml:"providers"`
	Modules          []ModuleUpdate   `yaml:"modules"`
}

// loadConfig reads a config file strictly, rejecting unknown keys and a second YAML document, and
// trims and validates every entry. An empty file, or one holding only comments, is an empty config.
func loadConfig(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		if errors.Is(err, io.EOF) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}
	var additionalDocument yaml.Node
	if err := decoder.Decode(&additionalDocument); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("failed to parse YAML: %w", err)
		}
		return nil, errors.New("multiple YAML documents are not supported")
	}

	config.TerraformVersion = strings.TrimSpace(config.TerraformVersion)
	if err := sanitizeProviderUpdates(config.Providers); err != nil {
		return nil, err
	}
	if err := sanitizeModuleUpdates(config.Modules); err != nil {
		return nil, err
	}

	return &config, nil
}

// declaresNoUpdates reports whether the config asks for nothing. -validate-config and an update
// run both refuse such a config with errConfigDeclaresNoUpdates; audit mode does not, because an
// empty audit is a true answer.
func (c *Config) declaresNoUpdates() bool {
	return c.TerraformVersion == "" && len(c.Providers) == 0 && len(c.Modules) == 0
}

var errConfigDeclaresNoUpdates = errors.New("config contains no updates")

// validateConfigFile is the -validate-config check: the config loads and asks for something.
func validateConfigFile(filename string) error {
	config, err := loadConfig(filename)
	if err != nil {
		return err
	}
	if config.declaresNoUpdates() {
		return errConfigDeclaresNoUpdates
	}
	return nil
}

// sanitizeProviderUpdates trims and validates provider entries. A provider may appear only once:
// an entry has no filters, so a repeat can at best restate the first and otherwise contradicts
// it, leaving a check that never passes and an audit that never matches.
func sanitizeProviderUpdates(providers []ProviderUpdate) error {
	firstIndex := make(map[string]int, len(providers))
	for i := range providers {
		providers[i].Name = strings.TrimSpace(providers[i].Name)
		providers[i].Version = strings.TrimSpace(providers[i].Version)

		if providers[i].Name == "" {
			return fmt.Errorf("provider at index %d is missing 'name' field", i)
		}
		if providers[i].Version == "" {
			return fmt.Errorf("provider at index %d is missing 'version' field", i)
		}
		if first, repeated := firstIndex[providers[i].Name]; repeated {
			return fmt.Errorf("provider at index %d repeats name '%s' from index %d", i, providers[i].Name, first)
		}
		firstIndex[providers[i].Name] = i
	}

	return nil
}

// sanitizeModuleUpdates trims and validates module entries. Sources may repeat, because entries
// for one source chain in order.
func sanitizeModuleUpdates(modules []ModuleUpdate) error {
	for i := range modules {
		module := &modules[i]
		module.Source = strings.TrimSpace(module.Source)
		module.Version = strings.TrimSpace(module.Version)
		module.From = trimNonEmptyStrings(module.From)
		module.IgnoreVersions = trimNonEmptyStrings(module.IgnoreVersions)
		module.IgnoreModules = trimNonEmptyStrings(module.IgnoreModules)

		if module.Source == "" {
			return fmt.Errorf("module at index %d is missing 'source' field", i)
		}
		if module.Version == "" {
			return fmt.Errorf("module at index %d is missing 'version' field", i)
		}
		for _, entry := range module.IgnoreModules {
			if _, _, err := splitIgnoreModuleEntry(entry); err != nil {
				return fmt.Errorf("module at index %d has an invalid 'ignore_modules' entry: %w", i, err)
			}
		}
	}

	return nil
}

// splitIgnoreModuleEntry divides a branch-scoped ignore entry at its last '/' into branch and
// module patterns; Terraform module names cannot contain '/'. An entry without a separator
// applies to every branch and yields an empty branch pattern.
func splitIgnoreModuleEntry(entry string) (branchPattern, modulePattern string, err error) {
	segments := strings.Split(entry, "/")
	if len(segments) == 1 {
		return "", entry, nil
	}

	for index, segment := range segments {
		segments[index] = strings.TrimSpace(segment)
		if segments[index] == "" {
			return "", "", fmt.Errorf("'%s' must be '<branch>/<module>' with no empty '/'-separated part", entry)
		}
	}

	return strings.Join(segments[:len(segments)-1], "/"), segments[len(segments)-1], nil
}

// trimNonEmptyStrings trims each value and drops the empty ones.
func trimNonEmptyStrings(values []string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	return filtered
}
