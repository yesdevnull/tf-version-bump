// Package main provides a CLI tool for updating Terraform module versions, Terraform versions,
// and provider versions across multiple files.
//
// The tool supports four modes of operation:
//  1. Single Module Mode: Update one module at a time via command-line flags
//  2. Config File Mode: Update multiple modules using a YAML configuration file
//  3. Terraform Version Mode: Update Terraform required_version in terraform blocks
//  4. Provider Version Mode: Update provider versions in terraform required_providers blocks
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

// stringSliceFlag is a custom flag type that allows a flag to be specified multiple times
type stringSliceFlag []string

// String returns the string representation of the flag
func (s *stringSliceFlag) String() string {
	return strings.Join(*s, ",")
}

// Set appends a value to the slice
func (s *stringSliceFlag) Set(value string) error {
	*s = append(*s, value)
	return nil
}

// quote formats a string with appropriate quoting based on the output format.
// For "text" output, uses single quotes. For "md" (Markdown) output, uses backticks.
//
// Parameters:
//   - s: The string to quote
//   - format: Output format ("text" or "md")
//
// Returns:
//   - string: The quoted string
//
// Examples:
//   - quote("vpc", "text") returns "'vpc'"
//   - quote("vpc", "md") returns "`vpc`"
func quote(s, format string) string {
	if format == "md" {
		return "`" + s + "`"
	}
	return "'" + s + "'"
}

// cliFlags holds all command-line flags
type cliFlags struct {
	pattern            string
	moduleSource       string
	toVersion          string
	fromVersions       stringSliceFlag
	ignoreVersions     stringSliceFlag
	ignoreModules      string
	configFile         string
	forceAdd           bool
	dryRun             bool
	verbose            bool
	showVersion        bool
	output             string
	terraformVersion   string
	providerName       string
}

// parseFlags parses and validates command-line flags
func parseFlags() *cliFlags {
	flags := &cliFlags{}
	
	flag.StringVar(&flags.pattern, "pattern", "", "Glob pattern for Terraform files (e.g., '*.tf' or 'modules/**/*.tf')")
	flag.StringVar(&flags.moduleSource, "module", "", "Source of the module to update (e.g., 'terraform-aws-modules/vpc/aws')")
	flag.StringVar(&flags.toVersion, "to", "", "Desired version number")
	flag.Var(&flags.fromVersions, "from", "Optional: version to update from (can be specified multiple times, e.g., -from 3.0.0 -from '~> 3.0')")
	flag.Var(&flags.ignoreVersions, "ignore-version", "Optional: version(s) to skip (can be specified multiple times, e.g., -ignore-version 3.0.0 -ignore-version '~> 3.0')")
	flag.StringVar(&flags.ignoreModules, "ignore-modules", "", "Optional: comma-separated list of module names or patterns to ignore (e.g., 'vpc,legacy-*')")
	flag.StringVar(&flags.configFile, "config", "", "Path to YAML config file with multiple module updates")
	flag.BoolVar(&flags.forceAdd, "force-add", false, "Add version attribute to modules that don't have one (default: skip with warning)")
	flag.BoolVar(&flags.dryRun, "dry-run", false, "Show what changes would be made without actually modifying files")
	flag.BoolVar(&flags.verbose, "verbose", false, "Show verbose output including skipped modules")
	flag.BoolVar(&flags.showVersion, "version", false, "Print version information and exit")
	flag.StringVar(&flags.output, "output", "text", "Output format: 'text' (default) or 'md' (Markdown)")
	flag.StringVar(&flags.terraformVersion, "terraform-version", "", "Update Terraform required_version in terraform blocks")
	flag.StringVar(&flags.providerName, "provider", "", "Provider name to update (e.g., 'aws', 'azurerm')")
	flag.Parse()

	// Validate output format
	if flags.output != "text" && flags.output != "md" {
		log.Fatalf("Error: Invalid output format '%s'. Must be 'text' or 'md'", flags.output)
	}

	return flags
}

// loadModuleUpdates loads module updates for single module CLI mode
func loadModuleUpdates(flags *cliFlags) []ModuleUpdate {
	// Single module mode - validate required flags
	if flags.pattern == "" || flags.moduleSource == "" || flags.toVersion == "" {
		fmt.Println("Usage:")
		fmt.Println("  Single module:  tf-version-bump -pattern <glob> -module <source> -to <version> [-from <version>]... [-ignore-version <version>]... [-ignore-modules <patterns>]")
		fmt.Println("  Config file:    tf-version-bump -pattern <glob> -config <config-file>")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Parse ignore patterns from comma-separated list
	var ignorePatterns []string
	if flags.ignoreModules != "" {
		for _, p := range strings.Split(flags.ignoreModules, ",") {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				ignorePatterns = append(ignorePatterns, trimmed)
			}
		}
	}

	return []ModuleUpdate{
		{Source: flags.moduleSource, Version: flags.toVersion, From: FromVersions(flags.fromVersions), IgnoreVersions: FromVersions(flags.ignoreVersions), IgnoreModules: ignorePatterns},
	}
}

// processFiles processes all matching files and applies module updates
func processFiles(files []string, updates []ModuleUpdate, flags *cliFlags) int {
	totalUpdates := 0
	for _, file := range files {
		for _, update := range updates {
			updated, err := updateModuleVersion(file, update.Source, update.Version, update.From, update.IgnoreVersions, update.IgnoreModules, flags.forceAdd, flags.dryRun, flags.verbose, flags.output)
			if err != nil {
				log.Printf("Error processing %s: %v", file, err)
				continue
			}
			if updated {
				prefix := "✓"
				action := "Updated"
				if flags.dryRun {
					prefix = "→"
					action = "Would update"
				}
				if len(update.From) > 0 {
					fmt.Printf("%s %s module source %s from version(s) %v to %s in %s\n", prefix, action, quote(update.Source, flags.output), update.From, quote(update.Version, flags.output), file)
				} else {
					fmt.Printf("%s %s module source %s to version %s in %s\n", prefix, action, quote(update.Source, flags.output), quote(update.Version, flags.output), file)
				}
				totalUpdates++
			}
		}
	}
	return totalUpdates
}

// printSummary prints the final summary of updates
func printSummary(totalUpdates int, updatesCount int, dryRun bool) {
	if dryRun {
		if updatesCount > 1 {
			fmt.Printf("\nDry run: would apply %d update(s) across all files\n", totalUpdates)
		} else {
			fmt.Printf("\nDry run: would update %d file(s)\n", totalUpdates)
		}
	} else {
		if updatesCount > 1 {
			fmt.Printf("\nSuccessfully applied %d update(s) across all files\n", totalUpdates)
		} else {
			fmt.Printf("\nSuccessfully updated %d file(s)\n", totalUpdates)
		}
	}
}

func main() {
	flags := parseFlags()

	// Handle version flag
	if flags.showVersion {
		fmt.Printf("tf-version-bump %s\n", version)
		fmt.Printf("  commit: %s\n", commit)
		fmt.Printf("  built:  %s\n", date)
		os.Exit(0)
	}

	// Validate operation modes
	validateOperationModes(flags)

	// Find and validate matching files
	files := findMatchingFiles(flags)

	// Run the appropriate operation mode
	if flags.configFile != "" {
		runConfigFileMode(files, flags)
	} else {
		runCLIMode(files, flags)
	}
}

// validateOperationModes validates that the CLI flags are properly set
func validateOperationModes(flags *cliFlags) {
	// Config file mode is exclusive with all other CLI flags
	if flags.configFile != "" {
		if flags.moduleSource != "" || flags.terraformVersion != "" || flags.providerName != "" ||
			len(flags.fromVersions) > 0 || len(flags.ignoreVersions) > 0 || flags.ignoreModules != "" {
			log.Fatal("Error: Cannot use -config with other operation flags (-module, -terraform-version, -provider, -from, -ignore-version, -ignore-modules)")
		}
		return
	}

	// CLI mode - validate that at least one operation is specified
	modesSet := 0
	if flags.moduleSource != "" {
		modesSet++
	}
	if flags.terraformVersion != "" {
		modesSet++
	}
	if flags.providerName != "" {
		modesSet++
	}

	if modesSet == 0 {
		fmt.Println("Usage:")
		fmt.Println("  Module update:     tf-version-bump -pattern <glob> -module <source> -to <version>")
		fmt.Println("  Config file:       tf-version-bump -pattern <glob> -config <config-file>")
		fmt.Println("  Terraform version: tf-version-bump -pattern <glob> -terraform-version <version>")
		fmt.Println("  Provider version:  tf-version-bump -pattern <glob> -provider <name> -to <version>")
		flag.PrintDefaults()
		os.Exit(1)
	}

	if modesSet > 1 {
		log.Fatal("Error: Cannot use -module, -terraform-version, and -provider flags together. Choose one operation mode or use a config file.")
	}
}

// findMatchingFiles finds all files matching the pattern
func findMatchingFiles(flags *cliFlags) []string {
	if flags.pattern == "" {
		log.Fatal("Error: -pattern flag is required")
	}

	files, err := filepath.Glob(flags.pattern)
	if err != nil {
		log.Fatalf("Error matching pattern: %v", err)
	}

	if len(files) == 0 {
		log.Fatalf("No files matched pattern: %s", flags.pattern)
	}

	fmt.Printf("Found %d file(s) matching pattern %s\n", len(files), quote(flags.pattern, flags.output))

	if flags.dryRun {
		fmt.Println("Running in dry-run mode - no files will be modified")
	}

	return files
}

// runConfigFileMode handles config file mode operations
func runConfigFileMode(files []string, flags *cliFlags) {
	config, err := loadConfig(flags.configFile)
	if err != nil {
		log.Fatalf("Error loading config file: %v", err)
	}

	var terraformUpdates, providerUpdates, moduleUpdates int

	// Process terraform version if specified
	if config.TerraformVersion != "" {
		terraformUpdates = processTerraformVersion(files, config.TerraformVersion, flags.dryRun, flags.output)
	}

	// Process provider updates if specified
	for _, provider := range config.Providers {
		count := processProviderVersion(files, provider.Name, provider.Version, flags.dryRun, flags.output)
		providerUpdates += count
	}

	// Process module updates if specified
	if len(config.Modules) > 0 {
		moduleUpdates = processFiles(files, config.Modules, flags)
	}

	// Print summary
	printConfigSummary(terraformUpdates, providerUpdates, moduleUpdates)
}

// runCLIMode handles CLI mode operations
func runCLIMode(files []string, flags *cliFlags) {
	var totalUpdates int
	var updates []ModuleUpdate

	switch {
	case flags.terraformVersion != "":
		totalUpdates = processTerraformVersion(files, flags.terraformVersion, flags.dryRun, flags.output)
		printTerraformSummary(totalUpdates, flags.dryRun)
	case flags.providerName != "":
		if flags.toVersion == "" {
			log.Fatal("Error: -to flag is required when using -provider")
		}
		totalUpdates = processProviderVersion(files, flags.providerName, flags.toVersion, flags.dryRun, flags.output)
		printProviderSummary(flags.providerName, totalUpdates, flags.dryRun, flags.output)
	default:
		updates = loadModuleUpdates(flags)
		totalUpdates = processFiles(files, updates, flags)
		printSummary(totalUpdates, len(updates), flags.dryRun)
	}
}

// printConfigSummary prints the summary for config file mode
func printConfigSummary(terraformUpdates, providerUpdates, moduleUpdates int) {
	if terraformUpdates > 0 || providerUpdates > 0 || moduleUpdates > 0 {
		fmt.Println("\n" + strings.Repeat("=", 50))
		fmt.Println("Config File Update Summary")
		fmt.Println(strings.Repeat("=", 50))
		if terraformUpdates > 0 {
			fmt.Printf("Terraform version: %d file(s) updated\n", terraformUpdates)
		}
		if providerUpdates > 0 {
			fmt.Printf("Providers: %d update(s) applied\n", providerUpdates)
		}
		if moduleUpdates > 0 {
			fmt.Printf("Modules: %d file(s) updated\n", moduleUpdates)
		}
	} else {
		fmt.Println("\nNo updates were performed. Config file may be empty or contain no matching items.")
	}
}

// printTerraformSummary prints the summary for terraform version updates
func printTerraformSummary(totalUpdates int, dryRun bool) {
	if dryRun {
		fmt.Printf("\nDry run: would update Terraform version in %d file(s)\n", totalUpdates)
	} else {
		fmt.Printf("\nSuccessfully updated Terraform version in %d file(s)\n", totalUpdates)
	}
}

// printProviderSummary prints the summary for provider version updates
func printProviderSummary(providerName string, totalUpdates int, dryRun bool, outputFormat string) {
	if dryRun {
		fmt.Printf("\nDry run: would update %s provider version in %d file(s)\n", quote(providerName, outputFormat), totalUpdates)
	} else {
		fmt.Printf("\nSuccessfully updated %s provider version in %d file(s)\n", quote(providerName, outputFormat), totalUpdates)
	}
}

// containsVersion checks if a version string is present in a slice of versions.
// This helper function reduces code duplication when checking version filters.
//
// Parameters:
//   - versions: List of version strings to search through
//   - version: The version string to search for
//
// Returns:
//   - bool: true if the version is found in the list, false otherwise
func containsVersion(versions []string, version string) bool {
	for _, v := range versions {
		if v == version {
			return true
		}
	}
	return false
}

// processTerraformVersion updates the required_version in terraform blocks across all files
//
// Parameters:
//   - files: List of file paths to process
//   - version: Target Terraform version to set
//   - dryRun: If true, show what would be changed without modifying files
//   - outputFormat: Output format ("text" or "md")
//
// Returns:
//   - int: Number of files that were updated (or would be updated in dry-run mode)
func processTerraformVersion(files []string, version string, dryRun bool, outputFormat string) int {
	totalUpdates := 0
	for _, file := range files {
		updated, err := updateTerraformVersion(file, version, dryRun)
		if err != nil {
			log.Printf("Error processing %s: %v", file, err)
			continue
		}
		if updated {
			prefix := "✓"
			action := "Updated"
			if dryRun {
				prefix = "→"
				action = "Would update"
			}
			fmt.Printf("%s %s Terraform required_version to %s in %s\n", prefix, action, quote(version, outputFormat), file)
			totalUpdates++
		}
	}
	return totalUpdates
}

// processProviderVersion updates provider versions in terraform required_providers blocks across all files
//
// Parameters:
//   - files: List of file paths to process
//   - providerName: Name of the provider to update (e.g., "aws", "azurerm")
//   - version: Target provider version to set
//   - dryRun: If true, show what would be changed without modifying files
//   - outputFormat: Output format ("text" or "md")
//
// Returns:
//   - int: Number of files that were updated (or would be updated in dry-run mode)
func processProviderVersion(files []string, providerName string, version string, dryRun bool, outputFormat string) int {
	totalUpdates := 0
	for _, file := range files {
		updated, err := updateProviderVersion(file, providerName, version, dryRun)
		if err != nil {
			log.Printf("Error processing %s: %v", file, err)
			continue
		}
		if updated {
			prefix := "✓"
			action := "Updated"
			if dryRun {
				prefix = "→"
				action = "Would update"
			}
			fmt.Printf("%s %s provider %s to version %s in %s\n", prefix, action, quote(providerName, outputFormat), quote(version, outputFormat), file)
			totalUpdates++
		}
	}
	return totalUpdates
}

// updateTerraformVersion updates the required_version attribute in terraform blocks
//
// Parameters:
//   - filename: Path to the Terraform file to process
//   - version: Target Terraform version to set
//   - dryRun: If true, show what would be changed without modifying files
//
// Returns:
//   - bool: true if a terraform block was updated (or would be updated in dry-run mode)
//   - error: Any error encountered during file reading, parsing, or writing
func updateTerraformVersion(filename, version string, dryRun bool) (bool, error) {
	// Get original file permissions to preserve them when writing
	fileInfo, err := os.Stat(filename)
	if err != nil {
		return false, fmt.Errorf("failed to stat file: %w", err)
	}
	originalMode := fileInfo.Mode()

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
		// Look for terraform blocks
		if block.Type() == "terraform" {
			// Update or add the required_version attribute
			block.Body().SetAttributeValue("required_version", cty.StringVal(version))
			updated = true
		}
	}

	// If we made changes, write the file back (unless in dry-run mode)
	if updated && !dryRun {
		output := hclwrite.Format(file.Bytes())
		// Preserve original file permissions
		if err := os.WriteFile(filename, output, originalMode.Perm()); err != nil {
			return false, fmt.Errorf("failed to write file: %w", err)
		}
	}

	return updated, nil
}

// updateProviderVersion updates the version attribute for a specific provider in terraform required_providers blocks
//
// Current implementation supports the block-based provider syntax:
//   required_providers { aws { source = "..." version = "..." } }
//
// Known Limitation: The attribute-based syntax is not yet supported:
//   required_providers { aws = { source = "..." version = "..." } }
// This limitation is documented in the README. Supporting attribute-based syntax would require
// parsing and modifying object expressions, which is more complex with the hclwrite library.
//
// Parameters:
//   - filename: Path to the Terraform file to process
//   - providerName: Name of the provider to update (e.g., "aws", "azurerm")
//   - version: Target provider version to set
//   - dryRun: If true, show what would be changed without modifying files
//
// Returns:
//   - bool: true if a provider was updated (or would be updated in dry-run mode)
//   - error: Any error encountered during file reading, parsing, or writing
func updateProviderVersion(filename, providerName, version string, dryRun bool) (bool, error) {
	// Get original file permissions to preserve them when writing
	fileInfo, err := os.Stat(filename)
	if err != nil {
		return false, fmt.Errorf("failed to stat file: %w", err)
	}
	originalMode := fileInfo.Mode()

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
		// Look for terraform blocks
		if block.Type() == "terraform" {
			// Look for required_providers block (it's a nested block type)
			for _, nestedBlock := range block.Body().Blocks() {
				if nestedBlock.Type() == "required_providers" {
					// Iterate through provider blocks in required_providers
					// e.g., required_providers { aws { source = "..." version = "..." } }
					for _, providerBlock := range nestedBlock.Body().Blocks() {
						if providerBlock.Type() == providerName {
							// Update the version attribute within the provider block
							providerBlock.Body().SetAttributeValue("version", cty.StringVal(version))
							updated = true
						}
					}
				}
			}
		}
	}

	// If we made changes, write the file back (unless in dry-run mode)
	if updated && !dryRun {
		output := hclwrite.Format(file.Bytes())
		// Preserve original file permissions
		if err := os.WriteFile(filename, output, originalMode.Perm()); err != nil {
			return false, fmt.Errorf("failed to write file: %w", err)
		}
	}

	return updated, nil
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
// If fromVersions is specified, only modules with current version matching any in the list will be updated.
// If ignoreVersions is specified, modules with current version matching any in the list will be skipped.
// If ignorePatterns is specified, modules with names matching any pattern will be skipped.
//
// Parameters:
//   - filename: Path to the Terraform file to process
//   - moduleSource: The module source to match (e.g., "terraform-aws-modules/vpc/aws")
//   - version: The target version to set (e.g., "5.0.0")
//   - fromVersions: Optional: only update if current version matches any in this list (e.g., ["4.0.0", "~> 3.0"])
//   - ignoreVersions: Optional: skip update if current version matches any in this list (e.g., ["4.0.0", "~> 3.0"])
//   - ignorePatterns: Optional: list of module names or patterns to ignore (e.g., ["vpc", "legacy-*"])
//   - forceAdd: If true, add version attribute to modules that don't have one
//   - dryRun: If true, show what would be changed without modifying files
//   - verbose: If true, print informational messages about skipped modules
//   - outputFormat: Output format ("text" or "md")
//
// Returns:
//   - bool: true if at least one module was updated (or would be updated in dry-run mode), false otherwise
//   - error: Any error encountered during file reading, parsing, or writing
func updateModuleVersion(filename, moduleSource, version string, fromVersions []string, ignoreVersions []string, ignorePatterns []string, forceAdd bool, dryRun bool, verbose bool, outputFormat string) (bool, error) {
	// Get original file permissions to preserve them when writing
	fileInfo, err := os.Stat(filename)
	if err != nil {
		return false, fmt.Errorf("failed to stat file: %w", err)
	}
	originalMode := fileInfo.Mode()

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
					fmt.Fprintf(os.Stderr, "Warning: Module %s in %s (source: %s) is a local module and cannot be version-bumped, skipping\n",
						quote(moduleName, outputFormat), filename, quote(moduleSource, outputFormat))
					continue
				}

				// Check if this module's source matches
				if sourceValue == moduleSource {
					// Check if module name matches any ignore pattern
					if shouldIgnoreModule(moduleName, ignorePatterns) {
						if verbose {
							fmt.Printf("  ⊗ Skipped module %s in %s (matches ignore pattern)\n", quote(moduleName, outputFormat), filename)
						}
						continue
					}

					// Check if the module has a version attribute
					versionAttr := block.Body().GetAttribute("version")
					if versionAttr == nil {
						if !forceAdd {
							// Module doesn't have a version attribute - print warning and skip
							fmt.Fprintf(os.Stderr, "Warning: Module %s in %s (source: %s) has no version attribute, skipping\n",
								quote(moduleName, outputFormat), filename, quote(moduleSource, outputFormat))
							continue
						}
						// forceAdd is true, so we'll add the version attribute below
					} else {
						// Get the current version for filtering
						versionTokens := versionAttr.Expr().BuildTokens(nil)
						currentVersion := string(versionTokens.Bytes())
						currentVersion = trimQuotes(strings.TrimSpace(currentVersion))

						// Check if current version matches any ignore-version filter
						if len(ignoreVersions) > 0 && containsVersion(ignoreVersions, currentVersion) {
							// Current version matches an ignore-version filter, skip this module
							if verbose {
								fmt.Printf("  ⊗ Skipped module %s in %s (current version %s matches 'ignore-version' filter %v)\n", quote(moduleName, outputFormat), filename, quote(currentVersion, outputFormat), ignoreVersions)
							}
							continue
						}

						// If fromVersions is specified, check if current version matches any in the list
						if len(fromVersions) > 0 && !containsVersion(fromVersions, currentVersion) {
							// Current version doesn't match any fromVersion, skip this module
							if verbose {
								fmt.Printf("  ⊗ Skipped module %s in %s (current version %s does not match any 'from' filter %v)\n", quote(moduleName, outputFormat), filename, quote(currentVersion, outputFormat), fromVersions)
							}
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
		// Preserve original file permissions
		if err := os.WriteFile(filename, output, originalMode.Perm()); err != nil {
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
	// Defensive: According to HCL/Terraform syntax, module blocks must have labels ("module" "name"),
	// so moduleName should never be empty in practice. This check handles malformed HCL or unexpected
	// parsing results. If moduleName is empty, do not ignore the module.
	if moduleName == "" {
		return false
	}

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
// Matching behavior:
//   - Uses greedy matching for middle parts (finds first occurrence of each part in order)
//   - Consecutive wildcards (**, ***, etc.) are treated as a single wildcard
//   - For patterns with multiple wildcards and repeated literal parts (e.g., "a*c*c"),
//     the algorithm ensures all parts fit without overlapping by checking that middle
//     parts don't extend past where the suffix begins
//
// Parameters:
//   - name: The string to match
//   - pattern: The pattern to match against (may contain '*' wildcards)
//
// Returns:
//   - bool: true if the name matches the pattern, false otherwise
//
// Examples:
//   - matchPattern("vpc", "vpc") returns true (exact match)
//   - matchPattern("legacy-vpc", "legacy-*") returns true (wildcard suffix)
//   - matchPattern("vpc-test", "*-test") returns true (wildcard prefix)
//   - matchPattern("prod-vpc-test", "*-vpc-*") returns true (wildcard both sides)
//   - matchPattern("abc", "a**c") returns true (consecutive wildcards)
//   - matchPattern("acc", "a*c*c") returns true (repeated parts, wildcards match zero chars)
//   - matchPattern("vpc", "s3") returns false (no match)
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

	// Ensure there's enough length for both prefix and suffix when both are present
	if parts[0] != "" && parts[len(parts)-1] != "" {
		minLength := len(parts[0]) + len(parts[len(parts)-1])
		if len(name) < minLength {
			return false
		}
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
		// Skip the last part check (already done above)
		if i == len(parts)-1 {
			break
		}
		// Find the part in the remaining string
		idx := strings.Index(name[pos:], part)
		if idx == -1 {
			return false
		}
		pos += idx + len(part)
	}

	// Ensure middle parts don't overlap with the suffix
	// The suffix must start at or after the current position
	if parts[len(parts)-1] != "" {
		suffixStart := len(name) - len(parts[len(parts)-1])
		if pos > suffixStart {
			return false
		}
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
