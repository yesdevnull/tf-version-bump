package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func main() {
	// Define CLI flags
	pattern := flag.String("pattern", "", "Glob pattern for Terraform files (e.g., '*.tf' or 'modules/**/*.tf')")
	moduleName := flag.String("module", "", "Name of the module to update")
	version := flag.String("version", "", "Desired version number")
	flag.Parse()

	// Validate inputs
	if *pattern == "" || *moduleName == "" || *version == "" {
		fmt.Println("Usage: tf-version-bump -pattern <glob> -module <name> -version <version>")
		flag.PrintDefaults()
		os.Exit(1)
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

	// Process each file
	updatedCount := 0
	for _, file := range files {
		updated, err := updateModuleVersion(file, *moduleName, *version)
		if err != nil {
			log.Printf("Error processing %s: %v", file, err)
			continue
		}
		if updated {
			fmt.Printf("✓ Updated module '%s' to version '%s' in %s\n", *moduleName, *version, file)
			updatedCount++
		}
	}

	fmt.Printf("\nSuccessfully updated %d file(s)\n", updatedCount)
}

// updateModuleVersion parses a Terraform file, finds the specified module, updates its version, and writes it back
func updateModuleVersion(filename, moduleName, version string) (bool, error) {
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
		// Look for module blocks with matching labels
		if block.Type() == "module" {
			labels := block.Labels()
			if len(labels) > 0 && labels[0] == moduleName {
				// Update or set the version attribute
				block.Body().SetAttributeValue("version", cty.StringVal(version))
				updated = true
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
