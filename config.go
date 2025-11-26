package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// ModuleUpdate represents a single module source and its target version.
// It is used both for single module updates via CLI flags and for batch
// updates from YAML configuration files.
type ModuleUpdate struct {
	Source  string `yaml:"source"`  // Module source (e.g., "terraform-aws-modules/vpc/aws")
	Version string `yaml:"version"` // Target version (e.g., "5.0.0")
	From    string `yaml:"from"`    // Optional: only update if current version matches this (e.g., "4.0.0")
}

// Config represents the structure of a YAML configuration file for batch updates.
// The YAML file should contain a top-level "modules" key with a list of module updates.
//
// Example YAML:
//
//	modules:
//	  - source: "terraform-aws-modules/vpc/aws"
//	    version: "5.0.0"
//	    from: "4.0.0"  # Optional: only update if current version is 4.0.0
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
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Validate and sanitize config
	for i, module := range config.Modules {
		// Trim whitespace from source and version fields
		config.Modules[i].Source = strings.TrimSpace(module.Source)
		config.Modules[i].Version = strings.TrimSpace(module.Version)
		config.Modules[i].From = strings.TrimSpace(module.From)

		if config.Modules[i].Source == "" {
			return nil, fmt.Errorf("module at index %d is missing 'source' field", i)
		}
		if config.Modules[i].Version == "" {
			return nil, fmt.Errorf("module at index %d is missing 'version' field", i)
		}
	}

	return config.Modules, nil
}
