package main

import (
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

// Config represents the structure of a YAML configuration file for batch updates.
// The YAML file should contain a top-level "modules" key with a list of module updates.
//
// Example YAML:
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
	Modules []ModuleUpdate `yaml:"modules"`
}

// loadConfig reads and parses a YAML configuration file containing module updates.
// It validates that all required fields (source and version) are present for each module.
//
// Parameters:
//   - filename: Path to the YAML configuration file
//
// Returns:
//   - []ModuleUpdate: List of module updates parsed from the file
//   - error: Any error encountered during reading, parsing, or validation
func loadConfig(filename string) ([]ModuleUpdate, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true) // Strict mode: error on unknown fields
	if err := decoder.Decode(&config); err != nil {
		// EOF indicates an empty file or a file with only comments, which is valid
		if err == io.EOF {
			return []ModuleUpdate{}, nil
		}
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Validate and sanitize config
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

	return config.Modules, nil
}
