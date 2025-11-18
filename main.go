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

// ModuleUpdate represents a module source and its target version
type ModuleUpdate struct {
	Source  string `yaml:"source"`
	Version string `yaml:"version"`
}

// Config represents the configuration file structure
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

// loadConfig reads and parses the YAML configuration file
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

// updateModuleVersion parses a Terraform file, finds modules with the specified source, updates their version, and writes it back
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

// trimQuotes removes surrounding quotes from a string
func trimQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
