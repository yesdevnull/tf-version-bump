// Package main provides a CLI tool for updating Terraform module versions across multiple files.
//
// The tool supports two modes of operation:
//  1. Single Module Mode: Update one module at a time via command-line flags
//  2. Config File Mode: Update multiple modules using a YAML configuration file
//
// It uses the official HashiCorp HCL library to safely parse and modify Terraform files
// while preserving formatting and comments.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
	"gopkg.in/yaml.v3"
)

// ModuleUpdate represents a single module source and its target version.
// It is used both for single module updates via CLI flags and for batch
// updates from YAML configuration files.
type ModuleUpdate struct {
	Source  string `yaml:"source"`  // Module source (e.g., "terraform-aws-modules/vpc/aws")
	Version string `yaml:"version"` // Target version (e.g., "5.0.0")
}

// Config represents the structure of a YAML configuration file for batch updates.
// The YAML file should contain a top-level "modules" key with a list of module updates.
//
// Example YAML:
//
//	modules:
//	  - source: "terraform-aws-modules/vpc/aws"
//	    version: "5.0.0"
//	  - source: "terraform-aws-modules/s3-bucket/aws"
//	    version: "4.0.0"
type Config struct {
	Modules []ModuleUpdate `yaml:"modules"`
}

func main() {
	// Define CLI flags
	pattern := flag.String("pattern", "", "Glob pattern for Terraform files (e.g., '*.tf' or 'modules/**/*.tf')")
	moduleSource := flag.String("module", "", "Source of the module to update (e.g., 'terraform-aws-modules/vpc/aws')")
	version := flag.String("version", "", "Desired version number")
	configFile := flag.String("config", "", "Path to YAML config file with multiple module updates")
	flag.Parse()

	// Determine operation mode
	var updates []ModuleUpdate
	var err error

	if *configFile != "" {
		// Config file mode
		if *moduleSource != "" || *version != "" {
			log.Fatal("Error: Cannot use -config with -module or -version flags")
		}
		if *pattern == "" {
			log.Fatal("Error: -pattern flag is required")
		}
		updates, err = loadConfig(*configFile)
		if err != nil {
			log.Fatalf("Error loading config file: %v", err)
		}
		if len(updates) == 0 {
			log.Fatal("Error: Config file contains no module updates")
		}
	} else {
		// Single module mode
		if *pattern == "" || *moduleSource == "" || *version == "" {
			fmt.Println("Usage:")
			fmt.Println("  Single module:  tf-version-bump -pattern <glob> -module <source> -version <version>")
			fmt.Println("  Config file:    tf-version-bump -pattern <glob> -config <config-file>")
			flag.PrintDefaults()
			os.Exit(1)
		}
		updates = []ModuleUpdate{
			{Source: *moduleSource, Version: *version},
		}
	}

	// Find matching files
	files, err := filepath.Glob(*pattern)
	if err != nil {
		log.Fatalf("Error matching pattern: %v", err)
	}

	if len(files) == 0 {
		log.Fatalf("No files matched pattern: %s", *pattern)
	}

	fmt.Printf("Found %d file(s) matching pattern '%s'\n", len(files), *pattern)

	// Process each file with all module updates
	totalUpdates := 0
	for _, file := range files {
		fileUpdates := 0
		for _, update := range updates {
			updated, err := updateModuleVersion(file, update.Source, update.Version)
			if err != nil {
				log.Printf("Error processing %s: %v", file, err)
				continue
			}
			if updated {
				fmt.Printf("✓ Updated module source '%s' to version '%s' in %s\n", update.Source, update.Version, file)
				fileUpdates++
				totalUpdates++
			}
		}
	}

	if len(updates) > 1 {
		fmt.Printf("\nSuccessfully applied %d update(s) across all files\n", totalUpdates)
	} else {
		fmt.Printf("\nSuccessfully updated %d file(s)\n", totalUpdates)
	}
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

	// Validate config
	for i, module := range config.Modules {
		if module.Source == "" {
			return nil, fmt.Errorf("module at index %d is missing 'source' field", i)
		}
		if module.Version == "" {
			return nil, fmt.Errorf("module at index %d is missing 'version' field", i)
		}
	}

	return config.Modules, nil
}

// updateModuleVersion parses a Terraform file, finds modules with the specified source,
// updates their version attribute, and writes the modified content back to the file.
//
// The function preserves all formatting, comments, and other HCL structures in the file.
// If a matching module doesn't have a version attribute, one will be added.
// All modules with the same source attribute will be updated to the same version.
//
// Parameters:
//   - filename: Path to the Terraform file to process
//   - moduleSource: The module source to match (e.g., "terraform-aws-modules/vpc/aws")
//   - version: The target version to set (e.g., "5.0.0")
//
// Returns:
//   - bool: true if at least one module was updated, false otherwise
//   - error: Any error encountered during file reading, parsing, or writing
func updateModuleVersion(filename, moduleSource, version string) (bool, error) {
	// Read the file
	src, err := os.ReadFile(filename)
	if err != nil {
		return false, fmt.Errorf("failed to read file: %w", err)
	}

	// Parse the file with hclwrite
	file, diags := hclwrite.ParseConfig(src, filename, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return false, fmt.Errorf("failed to parse HCL: %s", diags.Error())
	}

	// Track if we made any changes
	updated := false

	// Iterate through all blocks in the file
	for _, block := range file.Body().Blocks() {
		// Look for module blocks with matching source
		if block.Type() == "module" {
			// Get the source attribute
			sourceAttr := block.Body().GetAttribute("source")
			if sourceAttr != nil {
				// Extract the source value
				sourceTokens := sourceAttr.Expr().BuildTokens(nil)
				sourceValue := string(sourceTokens.Bytes())

				// Remove whitespace and quotes from the source value
				sourceValue = trimQuotes(strings.TrimSpace(sourceValue))

				// Check if this module's source matches
				if sourceValue == moduleSource {
					// Update or set the version attribute
					block.Body().SetAttributeValue("version", cty.StringVal(version))
					updated = true
				}
			}
		}
	}

	// If we made changes, write the file back
	if updated {
		output := hclwrite.Format(file.Bytes())
		if err := os.WriteFile(filename, output, 0644); err != nil {
			return false, fmt.Errorf("failed to write file: %w", err)
		}
	}

	return updated, nil
}

// trimQuotes removes surrounding single or double quotes from a string.
// If the string doesn't have matching quotes on both ends, it returns the original string.
//
// Parameters:
//   - s: The string to trim quotes from
//
// Returns:
//   - string: The string with quotes removed, or the original string if no matching quotes found
//
// Examples:
//   - `"hello"` returns `hello`
//   - `'hello'` returns `hello`
//   - `hello` returns `hello`
//   - `"hello'` returns `"hello'` (mismatched quotes)
func trimQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
