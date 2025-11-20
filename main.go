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
)

func main() {
	// Define CLI flags
	pattern := flag.String("pattern", "", "Glob pattern for Terraform files (e.g., '*.tf' or 'modules/**/*.tf')")
	moduleSource := flag.String("module", "", "Source of the module to update (e.g., 'terraform-aws-modules/vpc/aws')")
	toVersion := flag.String("to", "", "Desired version number")
	from := flag.String("from", "", "Optional: only update modules with this current version (e.g., '4.0.0')")
	configFile := flag.String("config", "", "Path to YAML config file with multiple module updates")
	forceAdd := flag.Bool("force-add", false, "Add version attribute to modules that don't have one (default: skip with warning)")
	flag.Parse()

	// Determine operation mode
	var updates []ModuleUpdate
	var err error

	if *configFile != "" {
		// Config file mode
		if *moduleSource != "" || *toVersion != "" || *from != "" {
			log.Fatal("Error: Cannot use -config with -module, -to, or -from flags")
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
		if *pattern == "" || *moduleSource == "" || *toVersion == "" {
			fmt.Println("Usage:")
			fmt.Println("  Single module:  tf-version-bump -pattern <glob> -module <source> -to <version> [-from <version>]")
			fmt.Println("  Config file:    tf-version-bump -pattern <glob> -config <config-file>")
			flag.PrintDefaults()
			os.Exit(1)
		}
		updates = []ModuleUpdate{
			{Source: *moduleSource, Version: *toVersion, From: *from},
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
			updated, err := updateModuleVersion(file, update.Source, update.Version, update.From, *forceAdd)
			if err != nil {
				log.Printf("Error processing %s: %v", file, err)
				continue
			}
			if updated {
				if update.From != "" {
					fmt.Printf("✓ Updated module source '%s' from version '%s' to '%s' in %s\n", update.Source, update.From, update.Version, file)
				} else {
					fmt.Printf("✓ Updated module source '%s' to version '%s' in %s\n", update.Source, update.Version, file)
				}
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

// updateModuleVersion parses a Terraform file, finds modules with the specified source,
// updates their version attribute, and writes the modified content back to the file.
//
// The function preserves all formatting, comments, and other HCL structures in the file.
// If a matching module doesn't have a version attribute:
//   - When forceAdd is false (default): a warning is printed and the module is skipped
//   - When forceAdd is true: a version attribute is added to the module
//
// All modules with the same source attribute will be updated to the same version.
// If fromVersion is specified, only modules with that current version will be updated.
//
// Parameters:
//   - filename: Path to the Terraform file to process
//   - moduleSource: The module source to match (e.g., "terraform-aws-modules/vpc/aws")
//   - version: The target version to set (e.g., "5.0.0")
//   - fromVersion: Optional: only update if current version matches this (e.g., "4.0.0")
//   - forceAdd: If true, add version attribute to modules that don't have one
//
// Returns:
//   - bool: true if at least one module was updated, false otherwise
//   - error: Any error encountered during file reading, parsing, or writing
func updateModuleVersion(filename, moduleSource, version, fromVersion string, forceAdd bool) (bool, error) {
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

				// Skip local modules - they don't have versions
				if isLocalModule(sourceValue) && sourceValue == moduleSource {
					moduleName := ""
					if len(block.Labels()) > 0 {
						moduleName = block.Labels()[0]
					}
					fmt.Fprintf(os.Stderr, "Warning: Module %q in %s (source: %q) is a local module and cannot be version-bumped, skipping\n",
						moduleName, filename, moduleSource)
					continue
				}

				// Check if this module's source matches
				if sourceValue == moduleSource {
					// Check if the module has a version attribute
					versionAttr := block.Body().GetAttribute("version")
					if versionAttr == nil {
						if !forceAdd {
							// Module doesn't have a version attribute - print warning and skip
							moduleName := ""
							if len(block.Labels()) > 0 {
								moduleName = block.Labels()[0]
							}
							fmt.Fprintf(os.Stderr, "Warning: Module %q in %s (source: %q) has no version attribute, skipping\n",
								moduleName, filename, moduleSource)
							continue
						}
						// forceAdd is true, so we'll add the version attribute below
					} else if fromVersion != "" {
						// If fromVersion is specified, check if current version matches
						versionTokens := versionAttr.Expr().BuildTokens(nil)
						currentVersion := string(versionTokens.Bytes())
						currentVersion = trimQuotes(strings.TrimSpace(currentVersion))

						if currentVersion != fromVersion {
							// Current version doesn't match fromVersion, skip this module
							continue
						}
					}

					// Update or add the version attribute
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

// isLocalModule checks if a module source is a local path.
// Local modules use relative or absolute paths instead of registry sources.
//
// Parameters:
//   - source: The module source to check
//
// Returns:
//   - bool: true if the source is a local path, false otherwise
//
// Examples:
//   - `./modules/vpc` returns true
//   - `../shared/modules` returns true
//   - `/absolute/path/module` returns true
//   - `terraform-aws-modules/vpc/aws` returns false
func isLocalModule(source string) bool {
	return strings.HasPrefix(source, "./") ||
		strings.HasPrefix(source, "../") ||
		strings.HasPrefix(source, "/")
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
