package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// FromVersions is a custom type that can unmarshal both string and []string from YAML
type FromVersions []string

// UnmarshalYAML implements custom unmarshaling to handle both string and array formats
func (f *FromVersions) UnmarshalYAML(value *yaml.Node) error {
	// Check if it's a sequence (array)
	if value.Kind == yaml.SequenceNode {
		var slice []string
		if err := value.Decode(&slice); err != nil {
			return fmt.Errorf("from field array contains non-string values: %w", err)
		}
		*f = FromVersions(slice)
		return nil
	}

	// Check if it's a scalar (single value)
	if value.Kind == yaml.ScalarNode {
		// Only accept string scalars, reject numbers and booleans
		if value.Tag != "!!str" {
			return fmt.Errorf("from field must be a string or array of strings, got %s", value.Tag)
		}

		var str string
		if err := value.Decode(&str); err != nil {
			return fmt.Errorf("failed to decode from field as string: %w", err)
		}

		if str == "" {
			*f = FromVersions{}
		} else {
			*f = FromVersions{str}
		}
		return nil
	}

	return fmt.Errorf("from field must be either a string or an array of strings, got node kind %v", value.Kind)
}

// ModuleUpdate represents a single module source and its target version.
// It is used both for single module updates via CLI flags and for batch
// updates from YAML configuration files.
type ModuleUpdate struct {
	Source         string       `yaml:"source"`          // Module source (e.g., "terraform-aws-modules/vpc/aws")
	Version        string       `yaml:"version"`         // Target version (e.g., "5.0.0")
	From           FromVersions `yaml:"from"`            // Optional: only update if current version matches any in this list (e.g., ["4.0.0", "~> 3.0"])
	IgnoreVersions FromVersions `yaml:"ignore_versions"` // Optional: skip update if current version matches any in this list (e.g., ["4.0.0", "~> 3.0"])
	IgnoreModules  []string     `yaml:"ignore_modules"`  // Optional: list of module names or patterns to ignore (e.g., ["vpc", "legacy-*"])
}

// ProviderUpdate represents a provider version update in required_providers blocks
type ProviderUpdate struct {
	Name    string `yaml:"name"`    // Provider name (e.g., "aws", "azurerm")
	Version string `yaml:"version"` // Target version (e.g., "~> 5.0")
}

// Config represents the structure of a YAML configuration file for batch updates.
// The YAML file can contain:
// - A "modules" key with a list of module updates
// - A "terraform_version" key to update Terraform required_version
// - A "providers" key with a list of provider updates
//
// Example YAML:
//
//	terraform_version: ">= 1.5"
//
//	providers:
//	  - name: "aws"
//	    version: "~> 5.0"
//	  - name: "azurerm"
//	    version: "~> 3.5"
//
//	modules:
//	  - source: "terraform-aws-modules/vpc/aws"
//	    version: "5.0.0"
//	    from: "4.0.0"          # Optional: only update if current version is 4.0.0
//	    ignore_versions:       # Optional: versions to skip
//	      - "3.0.0"
//	      - "~> 3.0"
//	    ignore_modules:        # Optional: module names or patterns to ignore
//	      - "legacy-vpc"
//	      - "test-*"
//	  - source: "terraform-aws-modules/s3-bucket/aws"
//	    version: "4.0.0"
type Config struct {
	TerraformVersion string           `yaml:"terraform_version"` // Optional: Terraform required_version to set
	Providers        []ProviderUpdate `yaml:"providers"`         // Optional: List of provider updates
	Modules          []ModuleUpdate   `yaml:"modules"`           // Optional: List of module updates
}

// loadConfig reads and parses a YAML configuration file containing module, terraform version,
// and provider updates. It validates that all required fields are present.
//
// Parameters:
//   - filename: Path to the YAML configuration file
//
// Returns:
//   - *Config: Configuration structure with all updates
//   - error: Any error encountered during reading, parsing, or validation
func loadConfig(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true) // Strict mode: error on unknown fields
	if err := decoder.Decode(&config); err != nil {
		// EOF indicates an empty file or a file with only comments, which is valid
		if err == io.EOF {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Trim and validate terraform_version
	config.TerraformVersion = strings.TrimSpace(config.TerraformVersion)

	// Validate and sanitize provider updates
	for i, provider := range config.Providers {
		config.Providers[i].Name = strings.TrimSpace(provider.Name)
		config.Providers[i].Version = strings.TrimSpace(provider.Version)

		if config.Providers[i].Name == "" {
			return nil, fmt.Errorf("provider at index %d is missing 'name' field", i)
		}
		if config.Providers[i].Version == "" {
			return nil, fmt.Errorf("provider at index %d is missing 'version' field", i)
		}
	}

	// Validate and sanitize module updates
	for i, module := range config.Modules {
		// Trim whitespace from source and version fields
		config.Modules[i].Source = strings.TrimSpace(module.Source)
		config.Modules[i].Version = strings.TrimSpace(module.Version)

		// Trim whitespace from from versions and filter out empty ones
		filteredFrom := make([]string, 0, len(module.From))
		for _, fromVer := range module.From {
			if trimmed := strings.TrimSpace(fromVer); trimmed != "" {
				filteredFrom = append(filteredFrom, trimmed)
			}
		}
		config.Modules[i].From = filteredFrom

		// Trim whitespace from ignore versions and filter out empty ones
		filteredIgnoreVersions := make([]string, 0, len(module.IgnoreVersions))
		for _, ignoreVer := range module.IgnoreVersions {
			if trimmed := strings.TrimSpace(ignoreVer); trimmed != "" {
				filteredIgnoreVersions = append(filteredIgnoreVersions, trimmed)
			}
		}
		config.Modules[i].IgnoreVersions = filteredIgnoreVersions

		// Trim whitespace from ignore patterns and filter out empty ones
		filteredIgnore := make([]string, 0, len(module.IgnoreModules))
		for _, pattern := range module.IgnoreModules {
			if trimmed := strings.TrimSpace(pattern); trimmed != "" {
				filteredIgnore = append(filteredIgnore, trimmed)
			}
		}
		config.Modules[i].IgnoreModules = filteredIgnore

		if config.Modules[i].Source == "" {
			return nil, fmt.Errorf("module at index %d is missing 'source' field", i)
		}
		if config.Modules[i].Version == "" {
			return nil, fmt.Errorf("module at index %d is missing 'version' field", i)
		}
	}

	return &config, nil
}
