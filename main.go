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

// Build information set by ldflags
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	// Define CLI flags
	pattern := flag.String("pattern", "", "Glob pattern for Terraform files (e.g., '*.tf' or 'modules/**/*.tf')")
	moduleSource := flag.String("module", "", "Source of the module to update (e.g., 'terraform-aws-modules/vpc/aws')")
	toVersion := flag.String("to", "", "Desired version number")
	from := flag.String("from", "", "Optional: only update modules with this current version (e.g., '4.0.0')")
	ignore := flag.String("ignore", "", "Optional: comma-separated list of module names or patterns to ignore (e.g., 'vpc,legacy-*')")
	configFile := flag.String("config", "", "Path to YAML config file with multiple module updates")
	forceAdd := flag.Bool("force-add", false, "Add version attribute to modules that don't have one (default: skip with warning)")
	dryRun := flag.Bool("dry-run", false, "Show what changes would be made without actually modifying files")
	showVersion := flag.Bool("version", false, "Print version information and exit")
	flag.Parse()

	// Handle version flag
	if *showVersion {
		fmt.Printf("tf-version-bump %s\n", version)
		fmt.Printf("  commit: %s\n", commit)
		fmt.Printf("  built:  %s\n", date)
		os.Exit(0)
	}

	// Determine operation mode
	var updates []ModuleUpdate
	var err error

	if *configFile != "" {
		// Config file mode
		if *moduleSource != "" || *toVersion != "" || *from != "" || *ignore != "" {
			log.Fatal("Error: Cannot use -config with -module, -to, -from, or -ignore flags")
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
			fmt.Println("  Single module:  tf-version-bump -pattern <glob> -module <source> -to <version> [-from <version>] [-ignore <patterns>]")
			fmt.Println("  Config file:    tf-version-bump -pattern <glob> -config <config-file>")
			flag.PrintDefaults()
			os.Exit(1)
		}
		// Parse ignore patterns from comma-separated list
		var ignorePatterns []string
		if *ignore != "" {
			for _, p := range strings.Split(*ignore, ",") {
				if trimmed := strings.TrimSpace(p); trimmed != "" {
					ignorePatterns = append(ignorePatterns, trimmed)
				}
			}
		}
		updates = []ModuleUpdate{
			{Source: *moduleSource, Version: *toVersion, From: *from, Ignore: ignorePatterns},
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

	if *dryRun {
		fmt.Println("Running in dry-run mode - no files will be modified")
	}

	// Process each file with all module updates
	totalUpdates := 0
	for _, file := range files {
		fileUpdates := 0
		for _, update := range updates {
			updated, err := updateModuleVersion(file, update.Source, update.Version, update.From, update.Ignore, *forceAdd, *dryRun)
			if err != nil {
				log.Printf("Error processing %s: %v", file, err)
				continue
			}
			if updated {
				prefix := "✓"
				action := "Updated"
				if *dryRun {
					prefix = "→"
					action = "Would update"
				}
				if update.From != "" {
					fmt.Printf("%s %s module source '%s' from version '%s' to '%s' in %s\n", prefix, action, update.Source, update.From, update.Version, file)
				} else {
					fmt.Printf("%s %s module source '%s' to version '%s' in %s\n", prefix, action, update.Source, update.Version, file)
				}
				fileUpdates++
				totalUpdates++
			}
		}
	}

	if *dryRun {
		if len(updates) > 1 {
			fmt.Printf("\nDry run: would apply %d update(s) across all files\n", totalUpdates)
		} else {
			fmt.Printf("\nDry run: would update %d file(s)\n", totalUpdates)
		}
	} else {
		if len(updates) > 1 {
			fmt.Printf("\nSuccessfully applied %d update(s) across all files\n", totalUpdates)
		} else {
			fmt.Printf("\nSuccessfully updated %d file(s)\n", totalUpdates)
		}
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
// If ignorePatterns is specified, modules with names matching any pattern will be skipped.
//
// Parameters:
//   - filename: Path to the Terraform file to process
//   - moduleSource: The module source to match (e.g., "terraform-aws-modules/vpc/aws")
//   - version: The target version to set (e.g., "5.0.0")
//   - fromVersion: Optional: only update if current version matches this (e.g., "4.0.0")
//   - ignorePatterns: Optional: list of module names or patterns to ignore (e.g., ["vpc", "legacy-*"])
//   - forceAdd: If true, add version attribute to modules that don't have one
//   - dryRun: If true, show what would be changed without modifying files
//
// Returns:
//   - bool: true if at least one module was updated (or would be updated in dry-run mode), false otherwise
//   - error: Any error encountered during file reading, parsing, or writing
func updateModuleVersion(filename, moduleSource, version, fromVersion string, ignorePatterns []string, forceAdd bool, dryRun bool) (bool, error) {
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
			// Get the module name from block labels
			moduleName := ""
			if len(block.Labels()) > 0 {
				moduleName = block.Labels()[0]
			}

			// Check if module name matches any ignore pattern
			if shouldIgnoreModule(moduleName, ignorePatterns) {
				continue
			}

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

	// If we made changes, write the file back (unless in dry-run mode)
	if updated && !dryRun {
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

// shouldIgnoreModule checks if a module name matches any of the ignore patterns.
// Patterns support wildcard matching using '*' for zero or more characters.
//
// Parameters:
//   - moduleName: The name of the module to check
//   - patterns: List of patterns to match against (e.g., ["vpc", "legacy-*", "*-test"])
//
// Returns:
//   - bool: true if the module name matches any pattern, false otherwise
//
// Examples:
//   - shouldIgnoreModule("vpc", ["vpc"]) returns true (exact match)
//   - shouldIgnoreModule("legacy-vpc", ["legacy-*"]) returns true (wildcard prefix)
//   - shouldIgnoreModule("vpc-test", ["*-test"]) returns true (wildcard suffix)
//   - shouldIgnoreModule("prod-vpc-test", ["*-vpc-*"]) returns true (wildcard both sides)
//   - shouldIgnoreModule("vpc", ["s3"]) returns false (no match)
func shouldIgnoreModule(moduleName string, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}

	for _, pattern := range patterns {
		if matchPattern(moduleName, pattern) {
			return true
		}
	}
	return false
}

// matchPattern performs wildcard pattern matching.
// Supports '*' as a wildcard that matches zero or more characters.
//
// Parameters:
//   - name: The string to match
//   - pattern: The pattern to match against (may contain '*' wildcards)
//
// Returns:
//   - bool: true if the name matches the pattern, false otherwise
//
// Examples:
//   - matchPattern("vpc", "vpc") returns true
//   - matchPattern("legacy-vpc", "legacy-*") returns true
//   - matchPattern("vpc-test", "*-test") returns true
//   - matchPattern("prod-vpc-test", "*-vpc-*") returns true
//   - matchPattern("vpc", "s3") returns false
func matchPattern(name, pattern string) bool {
	// If pattern has no wildcards, do exact match
	if !strings.Contains(pattern, "*") {
		return name == pattern
	}

	// Split pattern by '*' to get the literal parts
	parts := strings.Split(pattern, "*")

	// Check if name starts with first part (if not empty)
	if parts[0] != "" && !strings.HasPrefix(name, parts[0]) {
		return false
	}

	// Check if name ends with last part (if not empty)
	if parts[len(parts)-1] != "" && !strings.HasSuffix(name, parts[len(parts)-1]) {
		return false
	}

	// For middle parts, check they appear in order
	pos := 0
	for i, part := range parts {
		if part == "" {
			continue
		}
		// Skip the first part check (already done above)
		if i == 0 {
			pos = len(part)
			continue
		}
		// Find the part in the remaining string
		idx := strings.Index(name[pos:], part)
		if idx == -1 {
			return false
		}
		pos += idx + len(part)
	}

	return true
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
