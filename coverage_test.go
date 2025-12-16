package main

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseFlagsOutputFormat tests the output format validation in parseFlags
func TestParseFlagsOutputFormat(t *testing.T) {
	// Save original os.Args and restore after test
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	tests := []struct {
		name         string
		args         []string
		expectOutput string
	}{
		{
			name:         "default output format",
			args:         []string{"cmd", "-pattern", "*.tf", "-module", "test", "-to", "1.0.0"},
			expectOutput: "text",
		},
		{
			name:         "text output format",
			args:         []string{"cmd", "-pattern", "*.tf", "-module", "test", "-to", "1.0.0", "-output", "text"},
			expectOutput: "text",
		},
		{
			name:         "markdown output format",
			args:         []string{"cmd", "-pattern", "*.tf", "-module", "test", "-to", "1.0.0", "-output", "md"},
			expectOutput: "md",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset flag.CommandLine for each test
			resetFlags()
			os.Args = tt.args

			flags := parseFlags()

			if flags.output != tt.expectOutput {
				t.Errorf("parseFlags().output = %q, want %q", flags.output, tt.expectOutput)
			}
		})
	}
}

// TestParseFlagsBasicFlags tests that basic flags are parsed correctly
func TestParseFlagsBasicFlags(t *testing.T) {
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	tests := []struct {
		name          string
		args          []string
		expectPattern string
		expectModule  string
		expectTo      string
		expectForce   bool
		expectDry     bool
		expectVerbose bool
		expectConfig  string
	}{
		{
			name:          "basic flags",
			args:          []string{"cmd", "-pattern", "*.tf", "-module", "test/module", "-to", "2.0.0"},
			expectPattern: "*.tf",
			expectModule:  "test/module",
			expectTo:      "2.0.0",
		},
		{
			name:          "with force-add flag",
			args:          []string{"cmd", "-pattern", "*.tf", "-module", "test/module", "-to", "2.0.0", "-force-add"},
			expectPattern: "*.tf",
			expectModule:  "test/module",
			expectTo:      "2.0.0",
			expectForce:   true,
		},
		{
			name:          "with dry-run flag",
			args:          []string{"cmd", "-pattern", "*.tf", "-module", "test/module", "-to", "2.0.0", "-dry-run"},
			expectPattern: "*.tf",
			expectModule:  "test/module",
			expectTo:      "2.0.0",
			expectDry:     true,
		},
		{
			name:          "with verbose flag",
			args:          []string{"cmd", "-pattern", "*.tf", "-module", "test/module", "-to", "2.0.0", "-verbose"},
			expectPattern: "*.tf",
			expectModule:  "test/module",
			expectTo:      "2.0.0",
			expectVerbose: true,
		},
		{
			name:          "with config file flag",
			args:          []string{"cmd", "-pattern", "*.tf", "-config", "config.yml"},
			expectPattern: "*.tf",
			expectConfig:  "config.yml",
		},
		{
			name:          "all optional flags",
			args:          []string{"cmd", "-pattern", "*.tf", "-module", "test/module", "-to", "2.0.0", "-force-add", "-dry-run", "-verbose"},
			expectPattern: "*.tf",
			expectModule:  "test/module",
			expectTo:      "2.0.0",
			expectForce:   true,
			expectDry:     true,
			expectVerbose: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetFlags()
			os.Args = tt.args

			flags := parseFlags()

			if flags.pattern != tt.expectPattern {
				t.Errorf("pattern = %q, want %q", flags.pattern, tt.expectPattern)
			}
			if flags.moduleSource != tt.expectModule {
				t.Errorf("moduleSource = %q, want %q", flags.moduleSource, tt.expectModule)
			}
			if flags.toVersion != tt.expectTo {
				t.Errorf("toVersion = %q, want %q", flags.toVersion, tt.expectTo)
			}
			if flags.forceAdd != tt.expectForce {
				t.Errorf("forceAdd = %v, want %v", flags.forceAdd, tt.expectForce)
			}
			if flags.dryRun != tt.expectDry {
				t.Errorf("dryRun = %v, want %v", flags.dryRun, tt.expectDry)
			}
			if flags.verbose != tt.expectVerbose {
				t.Errorf("verbose = %v, want %v", flags.verbose, tt.expectVerbose)
			}
			if flags.configFile != tt.expectConfig {
				t.Errorf("configFile = %q, want %q", flags.configFile, tt.expectConfig)
			}
		})
	}
}

// TestParseFlagsFromAndIgnoreVersions tests multi-value flags
func TestParseFlagsFromAndIgnoreVersions(t *testing.T) {
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	tests := []struct {
		name                 string
		args                 []string
		expectFromVersions   []string
		expectIgnoreVersions []string
		expectIgnoreModules  string
	}{
		{
			name:               "single from version",
			args:               []string{"cmd", "-pattern", "*.tf", "-module", "test", "-to", "2.0.0", "-from", "1.0.0"},
			expectFromVersions: []string{"1.0.0"},
		},
		{
			name:               "multiple from versions",
			args:               []string{"cmd", "-pattern", "*.tf", "-module", "test", "-to", "2.0.0", "-from", "1.0.0", "-from", "1.5.0"},
			expectFromVersions: []string{"1.0.0", "1.5.0"},
		},
		{
			name:                 "single ignore version",
			args:                 []string{"cmd", "-pattern", "*.tf", "-module", "test", "-to", "2.0.0", "-ignore-version", "3.0.0"},
			expectIgnoreVersions: []string{"3.0.0"},
		},
		{
			name:                 "multiple ignore versions",
			args:                 []string{"cmd", "-pattern", "*.tf", "-module", "test", "-to", "2.0.0", "-ignore-version", "3.0.0", "-ignore-version", "~> 3.0"},
			expectIgnoreVersions: []string{"3.0.0", "~> 3.0"},
		},
		{
			name:                "ignore modules flag",
			args:                []string{"cmd", "-pattern", "*.tf", "-module", "test", "-to", "2.0.0", "-ignore-modules", "vpc,legacy-*"},
			expectIgnoreModules: "vpc,legacy-*",
		},
		{
			name:                 "combined from and ignore versions",
			args:                 []string{"cmd", "-pattern", "*.tf", "-module", "test", "-to", "2.0.0", "-from", "1.0.0", "-from", "1.5.0", "-ignore-version", "3.0.0"},
			expectFromVersions:   []string{"1.0.0", "1.5.0"},
			expectIgnoreVersions: []string{"3.0.0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetFlags()
			os.Args = tt.args

			flags := parseFlags()

			// Check from versions
			if len(tt.expectFromVersions) > 0 {
				if len(flags.fromVersions) != len(tt.expectFromVersions) {
					t.Errorf("fromVersions length = %d, want %d", len(flags.fromVersions), len(tt.expectFromVersions))
				}
				for i, v := range tt.expectFromVersions {
					if i < len(flags.fromVersions) && flags.fromVersions[i] != v {
						t.Errorf("fromVersions[%d] = %q, want %q", i, flags.fromVersions[i], v)
					}
				}
			}

			// Check ignore versions
			if len(tt.expectIgnoreVersions) > 0 {
				if len(flags.ignoreVersions) != len(tt.expectIgnoreVersions) {
					t.Errorf("ignoreVersions length = %d, want %d", len(flags.ignoreVersions), len(tt.expectIgnoreVersions))
				}
				for i, v := range tt.expectIgnoreVersions {
					if i < len(flags.ignoreVersions) && flags.ignoreVersions[i] != v {
						t.Errorf("ignoreVersions[%d] = %q, want %q", i, flags.ignoreVersions[i], v)
					}
				}
			}

			// Check ignore modules
			if flags.ignoreModules != tt.expectIgnoreModules {
				t.Errorf("ignoreModules = %q, want %q", flags.ignoreModules, tt.expectIgnoreModules)
			}
		})
	}
}

// TestParseFlagsVersionFlag tests the version flag
func TestParseFlagsVersionFlag(t *testing.T) {
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	resetFlags()
	os.Args = []string{"cmd", "-version"}

	flags := parseFlags()

	if !flags.showVersion {
		t.Error("showVersion should be true when -version flag is set")
	}
}

// TestLoadModuleUpdatesSingleModule tests loading module updates in single module mode
func TestLoadModuleUpdatesSingleModule(t *testing.T) {
	tests := []struct {
		name                 string
		flags                *cliFlags
		expectSource         string
		expectVersion        string
		expectFromVersions   int
		expectIgnoreVersions int
		expectIgnoreModules  int
	}{
		{
			name: "basic single module",
			flags: &cliFlags{
				pattern:      "*.tf",
				moduleSource: "terraform-aws-modules/vpc/aws",
				toVersion:    "5.0.0",
			},
			expectSource:  "terraform-aws-modules/vpc/aws",
			expectVersion: "5.0.0",
		},
		{
			name: "single module with from version",
			flags: &cliFlags{
				pattern:      "*.tf",
				moduleSource: "terraform-aws-modules/vpc/aws",
				toVersion:    "5.0.0",
				fromVersions: stringSliceFlag{"4.0.0"},
			},
			expectSource:       "terraform-aws-modules/vpc/aws",
			expectVersion:      "5.0.0",
			expectFromVersions: 1,
		},
		{
			name: "single module with ignore versions",
			flags: &cliFlags{
				pattern:        "*.tf",
				moduleSource:   "terraform-aws-modules/vpc/aws",
				toVersion:      "5.0.0",
				ignoreVersions: stringSliceFlag{"3.0.0", "~> 3.0"},
			},
			expectSource:         "terraform-aws-modules/vpc/aws",
			expectVersion:        "5.0.0",
			expectIgnoreVersions: 2,
		},
		{
			name: "single module with ignore modules",
			flags: &cliFlags{
				pattern:       "*.tf",
				moduleSource:  "terraform-aws-modules/vpc/aws",
				toVersion:     "5.0.0",
				ignoreModules: "vpc,legacy-*,*-test",
			},
			expectSource:        "terraform-aws-modules/vpc/aws",
			expectVersion:       "5.0.0",
			expectIgnoreModules: 3,
		},
		{
			name: "single module with all options",
			flags: &cliFlags{
				pattern:        "*.tf",
				moduleSource:   "terraform-aws-modules/vpc/aws",
				toVersion:      "5.0.0",
				fromVersions:   stringSliceFlag{"4.0.0", "~> 4.0"},
				ignoreVersions: stringSliceFlag{"3.0.0"},
				ignoreModules:  "legacy-vpc",
			},
			expectSource:         "terraform-aws-modules/vpc/aws",
			expectVersion:        "5.0.0",
			expectFromVersions:   2,
			expectIgnoreVersions: 1,
			expectIgnoreModules:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updates := loadModuleUpdates(tt.flags)

			if len(updates) != 1 {
				t.Fatalf("expected 1 update, got %d", len(updates))
			}

			update := updates[0]
			if update.Source != tt.expectSource {
				t.Errorf("Source = %q, want %q", update.Source, tt.expectSource)
			}
			if update.Version != tt.expectVersion {
				t.Errorf("Version = %q, want %q", update.Version, tt.expectVersion)
			}
			if len(update.From) != tt.expectFromVersions {
				t.Errorf("From length = %d, want %d", len(update.From), tt.expectFromVersions)
			}
			if len(update.IgnoreVersions) != tt.expectIgnoreVersions {
				t.Errorf("IgnoreVersions length = %d, want %d", len(update.IgnoreVersions), tt.expectIgnoreVersions)
			}
			if len(update.IgnoreModules) != tt.expectIgnoreModules {
				t.Errorf("IgnoreModules length = %d, want %d", len(update.IgnoreModules), tt.expectIgnoreModules)
			}
		})
	}
}

// TestLoadModuleUpdatesIgnoreModulesParsing tests parsing of comma-separated ignore modules
func TestLoadModuleUpdatesIgnoreModulesParsing(t *testing.T) {
	tests := []struct {
		name           string
		ignoreModules  string
		expectPatterns []string
	}{
		{
			name:           "single pattern",
			ignoreModules:  "vpc",
			expectPatterns: []string{"vpc"},
		},
		{
			name:           "multiple patterns",
			ignoreModules:  "vpc,s3,rds",
			expectPatterns: []string{"vpc", "s3", "rds"},
		},
		{
			name:           "patterns with wildcards",
			ignoreModules:  "legacy-*,*-test,*-dev-*",
			expectPatterns: []string{"legacy-*", "*-test", "*-dev-*"},
		},
		{
			name:           "patterns with spaces",
			ignoreModules:  "vpc, s3 , rds",
			expectPatterns: []string{"vpc", "s3", "rds"},
		},
		{
			name:           "empty patterns filtered",
			ignoreModules:  "vpc,,s3,",
			expectPatterns: []string{"vpc", "s3"},
		},
		{
			name:           "empty string",
			ignoreModules:  "",
			expectPatterns: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags := &cliFlags{
				pattern:       "*.tf",
				moduleSource:  "test/module",
				toVersion:     "1.0.0",
				ignoreModules: tt.ignoreModules,
			}

			updates := loadModuleUpdates(flags)

			if len(updates) != 1 {
				t.Fatalf("expected 1 update, got %d", len(updates))
			}

			patterns := updates[0].IgnoreModules
			if len(patterns) != len(tt.expectPatterns) {
				t.Errorf("IgnoreModules length = %d, want %d", len(patterns), len(tt.expectPatterns))
			}

			for i, expected := range tt.expectPatterns {
				if i < len(patterns) && patterns[i] != expected {
					t.Errorf("IgnoreModules[%d] = %q, want %q", i, patterns[i], expected)
				}
			}
		})
	}
}

// TestLoadModuleUpdatesConfigFile tests loading module updates from config file
func TestLoadModuleUpdatesConfigFile(t *testing.T) {
	// Create a temp directory
	tmpDir := t.TempDir()

	// Create a valid config file
	configContent := `modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
  - source: "terraform-aws-modules/s3-bucket/aws"
    version: "4.0.0"
`
	configFile := filepath.Join(tmpDir, "config.yml")
	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	flags := &cliFlags{
		pattern:    "*.tf",
		configFile: configFile,
	}

	updates := loadModuleUpdates(flags)

	if len(updates) != 2 {
		t.Fatalf("expected 2 updates, got %d", len(updates))
	}

	if updates[0].Source != "terraform-aws-modules/vpc/aws" {
		t.Errorf("updates[0].Source = %q, want %q", updates[0].Source, "terraform-aws-modules/vpc/aws")
	}
	if updates[0].Version != "5.0.0" {
		t.Errorf("updates[0].Version = %q, want %q", updates[0].Version, "5.0.0")
	}
	if updates[1].Source != "terraform-aws-modules/s3-bucket/aws" {
		t.Errorf("updates[1].Source = %q, want %q", updates[1].Source, "terraform-aws-modules/s3-bucket/aws")
	}
	if updates[1].Version != "4.0.0" {
		t.Errorf("updates[1].Version = %q, want %q", updates[1].Version, "4.0.0")
	}
}

// TestProcessFiles tests the processFiles function
func TestProcessFiles(t *testing.T) {
	// Create a temp directory
	tmpDir := t.TempDir()

	// Create a Terraform file with a module
	tfContent := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "4.0.0"
}

module "s3" {
  source  = "terraform-aws-modules/s3-bucket/aws"
  version = "3.0.0"
}
`
	tfFile := filepath.Join(tmpDir, "main.tf")
	if err := os.WriteFile(tfFile, []byte(tfContent), 0644); err != nil {
		t.Fatalf("failed to write tf file: %v", err)
	}

	tests := []struct {
		name          string
		updates       []ModuleUpdate
		dryRun        bool
		expectUpdates int
	}{
		{
			name: "update single module",
			updates: []ModuleUpdate{
				{Source: "terraform-aws-modules/vpc/aws", Version: "5.0.0"},
			},
			expectUpdates: 1,
		},
		{
			name: "update multiple modules",
			updates: []ModuleUpdate{
				{Source: "terraform-aws-modules/vpc/aws", Version: "5.0.0"},
				{Source: "terraform-aws-modules/s3-bucket/aws", Version: "4.0.0"},
			},
			expectUpdates: 2,
		},
		{
			name: "update non-matching module",
			updates: []ModuleUpdate{
				{Source: "terraform-aws-modules/rds/aws", Version: "5.0.0"},
			},
			expectUpdates: 0,
		},
		{
			name: "dry run mode",
			updates: []ModuleUpdate{
				{Source: "terraform-aws-modules/vpc/aws", Version: "5.0.0"},
			},
			dryRun:        true,
			expectUpdates: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset the file for each test
			if err := os.WriteFile(tfFile, []byte(tfContent), 0644); err != nil {
				t.Fatalf("failed to reset tf file: %v", err)
			}

			flags := &cliFlags{
				dryRun: tt.dryRun,
				output: "text",
			}

			// Capture stdout
			oldStdout := os.Stdout
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("os.Pipe failed: %v", err)
			}
			os.Stdout = w

			totalUpdates := processFiles([]string{tfFile}, tt.updates, flags)

			if err := w.Close(); err != nil {
				t.Fatalf("failed to close pipe writer: %v", err)
			}
			os.Stdout = oldStdout
			if _, err := io.ReadAll(r); err != nil {
				t.Fatalf("failed to read from pipe: %v", err)
			}

			if totalUpdates != tt.expectUpdates {
				t.Errorf("totalUpdates = %d, want %d", totalUpdates, tt.expectUpdates)
			}
		})
	}
}

// TestProcessFilesWithFromVersionFilter tests processFiles with from version filtering
func TestProcessFilesWithFromVersionFilter(t *testing.T) {
	tmpDir := t.TempDir()

	tfContent := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "4.0.0"
}
`
	tfFile := filepath.Join(tmpDir, "main.tf")
	if err := os.WriteFile(tfFile, []byte(tfContent), 0644); err != nil {
		t.Fatalf("failed to write tf file: %v", err)
	}

	tests := []struct {
		name          string
		fromVersions  FromVersions
		expectUpdates int
	}{
		{
			name:          "from version matches",
			fromVersions:  FromVersions{"4.0.0"},
			expectUpdates: 1,
		},
		{
			name:          "from version does not match",
			fromVersions:  FromVersions{"3.0.0"},
			expectUpdates: 0,
		},
		{
			name:          "multiple from versions one matches",
			fromVersions:  FromVersions{"3.0.0", "4.0.0"},
			expectUpdates: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset the file
			if err := os.WriteFile(tfFile, []byte(tfContent), 0644); err != nil {
				t.Fatalf("failed to reset tf file: %v", err)
			}

			flags := &cliFlags{
				dryRun: true,
				output: "text",
			}

			updates := []ModuleUpdate{
				{
					Source:  "terraform-aws-modules/vpc/aws",
					Version: "5.0.0",
					From:    tt.fromVersions,
				},
			}

			oldStdout := os.Stdout
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("failed to create os.Pipe: %v", err)
			}
			os.Stdout = w

			totalUpdates := processFiles([]string{tfFile}, updates, flags)

			if err := w.Close(); err != nil {
				t.Fatalf("failed to close pipe writer: %v", err)
			}
			os.Stdout = oldStdout
			if _, err := io.ReadAll(r); err != nil {
				t.Fatalf("failed to read from pipe: %v", err)
			}

			if totalUpdates != tt.expectUpdates {
				t.Errorf("totalUpdates = %d, want %d", totalUpdates, tt.expectUpdates)
			}
		})
	}
}

// TestProcessFilesMultipleFiles tests processFiles with multiple files
func TestProcessFilesMultipleFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create multiple Terraform files
	tfContent1 := `module "vpc1" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "4.0.0"
}
`
	tfContent2 := `module "vpc2" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "4.0.0"
}
`
	tfFile1 := filepath.Join(tmpDir, "main1.tf")
	tfFile2 := filepath.Join(tmpDir, "main2.tf")

	if err := os.WriteFile(tfFile1, []byte(tfContent1), 0644); err != nil {
		t.Fatalf("failed to write tf file 1: %v", err)
	}
	if err := os.WriteFile(tfFile2, []byte(tfContent2), 0644); err != nil {
		t.Fatalf("failed to write tf file 2: %v", err)
	}

	flags := &cliFlags{
		dryRun: true,
		output: "text",
	}

	updates := []ModuleUpdate{
		{Source: "terraform-aws-modules/vpc/aws", Version: "5.0.0"},
	}

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create os.Pipe: %v", err)
	}
	os.Stdout = w

	totalUpdates := processFiles([]string{tfFile1, tfFile2}, updates, flags)

	if err := w.Close(); err != nil {
		t.Fatalf("failed to close pipe writer: %v", err)
	}
	os.Stdout = oldStdout
	if _, err := io.ReadAll(r); err != nil {
		t.Fatalf("failed to read from pipe: %v", err)
	}

	if totalUpdates != 2 {
		t.Errorf("totalUpdates = %d, want 2", totalUpdates)
	}
}

// TestPrintSummary tests the printSummary function
func TestPrintSummary(t *testing.T) {
	tests := []struct {
		name         string
		totalUpdates int
		updatesCount int
		dryRun       bool
		expectSubstr string
	}{
		{
			name:         "single update dry run",
			totalUpdates: 1,
			updatesCount: 1,
			dryRun:       true,
			expectSubstr: "Dry run: would update 1 file(s)",
		},
		{
			name:         "multiple updates dry run",
			totalUpdates: 3,
			updatesCount: 2,
			dryRun:       true,
			expectSubstr: "Dry run: would apply 3 update(s)",
		},
		{
			name:         "single update real run",
			totalUpdates: 1,
			updatesCount: 1,
			dryRun:       false,
			expectSubstr: "Successfully updated 1 file(s)",
		},
		{
			name:         "multiple updates real run",
			totalUpdates: 5,
			updatesCount: 3,
			dryRun:       false,
			expectSubstr: "Successfully applied 5 update(s)",
		},
		{
			name:         "zero updates dry run",
			totalUpdates: 0,
			updatesCount: 1,
			dryRun:       true,
			expectSubstr: "Dry run: would update 0 file(s)",
		},
		{
			name:         "zero updates real run",
			totalUpdates: 0,
			updatesCount: 1,
			dryRun:       false,
			expectSubstr: "Successfully updated 0 file(s)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Capture stdout
			oldStdout := os.Stdout
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("failed to create os.Pipe: %v", err)
			}
			os.Stdout = w

			printSummary(tt.totalUpdates, tt.updatesCount, tt.dryRun)

			if err := w.Close(); err != nil {
				t.Fatalf("failed to close pipe writer: %v", err)
			}
			os.Stdout = oldStdout
			output, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("failed to read from pipe: %v", err)
			}

			if !strings.Contains(string(output), tt.expectSubstr) {
				t.Errorf("output = %q, expected to contain %q", string(output), tt.expectSubstr)
			}
		})
	}
}

// TestProcessFilesWithVerbose tests processFiles with verbose output
func TestProcessFilesWithVerbose(t *testing.T) {
	tmpDir := t.TempDir()

	tfContent := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "4.0.0"
}
`
	tfFile := filepath.Join(tmpDir, "main.tf")
	if err := os.WriteFile(tfFile, []byte(tfContent), 0644); err != nil {
		t.Fatalf("failed to write tf file: %v", err)
	}

	flags := &cliFlags{
		verbose: true,
		dryRun:  true,
		output:  "text",
	}

	// Test that verbose output is generated when from filter doesn't match
	updates := []ModuleUpdate{
		{
			Source:  "terraform-aws-modules/vpc/aws",
			Version: "5.0.0",
			From:    FromVersions{"3.0.0"}, // Doesn't match 4.0.0
		},
	}

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create os.Pipe: %v", err)
	}
	os.Stdout = w

	totalUpdates := processFiles([]string{tfFile}, updates, flags)

	if err := w.Close(); err != nil {
		t.Fatalf("failed to close pipe writer: %v", err)
	}
	os.Stdout = oldStdout
	output, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("failed to read from pipe: %v", err)
	}

	if totalUpdates != 0 {
		t.Errorf("totalUpdates = %d, want 0 (from filter should prevent update)", totalUpdates)
	}

	// Verbose output should mention the skip
	if !strings.Contains(string(output), "Skipped") {
		t.Errorf("verbose output should contain 'Skipped', got: %s", string(output))
	}
}

// TestProcessFilesMarkdownOutput tests processFiles with markdown output format
func TestProcessFilesMarkdownOutput(t *testing.T) {
	tmpDir := t.TempDir()

	tfContent := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "4.0.0"
}
`
	tfFile := filepath.Join(tmpDir, "main.tf")
	if err := os.WriteFile(tfFile, []byte(tfContent), 0644); err != nil {
		t.Fatalf("failed to write tf file: %v", err)
	}

	flags := &cliFlags{
		dryRun: true,
		output: "md",
	}

	updates := []ModuleUpdate{
		{Source: "terraform-aws-modules/vpc/aws", Version: "5.0.0"},
	}

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create os.Pipe: %v", err)
	}
	os.Stdout = w

	totalUpdates := processFiles([]string{tfFile}, updates, flags)

	if err := w.Close(); err != nil {
		t.Fatalf("failed to close pipe writer: %v", err)
	}
	os.Stdout = oldStdout
	output, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("failed to read from pipe: %v", err)
	}

	if totalUpdates != 1 {
		t.Errorf("totalUpdates = %d, want 1", totalUpdates)
	}

	// Markdown output should use backticks
	if !strings.Contains(string(output), "`") {
		t.Errorf("markdown output should contain backticks, got: %s", string(output))
	}
}

// TestProcessFilesOutputMessages tests the different output messages
func TestProcessFilesOutputMessages(t *testing.T) {
	tmpDir := t.TempDir()

	tfContent := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "4.0.0"
}
`
	tfFile := filepath.Join(tmpDir, "main.tf")

	tests := []struct {
		name        string
		dryRun      bool
		from        FromVersions
		expectInOut string
	}{
		{
			name:        "dry run prefix",
			dryRun:      true,
			expectInOut: "Would update",
		},
		{
			name:        "actual update prefix",
			dryRun:      false,
			expectInOut: "Updated",
		},
		{
			name:        "with from version in message",
			dryRun:      true,
			from:        FromVersions{"4.0.0"},
			expectInOut: "from version(s)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset the file
			if err := os.WriteFile(tfFile, []byte(tfContent), 0644); err != nil {
				t.Fatalf("failed to reset tf file: %v", err)
			}

			flags := &cliFlags{
				dryRun: tt.dryRun,
				output: "text",
			}

			updates := []ModuleUpdate{
				{
					Source:  "terraform-aws-modules/vpc/aws",
					Version: "5.0.0",
					From:    tt.from,
				},
			}

			oldStdout := os.Stdout
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("failed to create os.Pipe: %v", err)
			}
			os.Stdout = w

			processFiles([]string{tfFile}, updates, flags)

			if err := w.Close(); err != nil {
				t.Fatalf("failed to close pipe writer: %v", err)
			}
			os.Stdout = oldStdout
			output, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("failed to read from pipe: %v", err)
			}

			if !strings.Contains(string(output), tt.expectInOut) {
				t.Errorf("output should contain %q, got: %s", tt.expectInOut, string(output))
			}
		})
	}
}

// TestProcessFilesError tests processFiles error handling
func TestProcessFilesError(t *testing.T) {
	// Test with non-existent file
	flags := &cliFlags{
		dryRun: true,
		output: "text",
	}

	updates := []ModuleUpdate{
		{Source: "test/module", Version: "1.0.0"},
	}

	totalUpdates := processFiles([]string{"/nonexistent/path/file.tf"}, updates, flags)

	// Should return 0 updates but handle the error gracefully
	// Note: The actual error is logged via log.Printf which uses the default logger
	// and cannot be easily captured in tests without redirecting the logger output
	if totalUpdates != 0 {
		t.Errorf("totalUpdates = %d, want 0 for non-existent file", totalUpdates)
	}
}

// TestLoadConfigYAMLEdgeCases tests additional edge cases for loadConfig's YAML parsing
func TestLoadConfigYAMLEdgeCases(t *testing.T) {
	tests := []struct {
		name          string
		yamlContent   string
		expectError   bool
		validateField string // "From", "IgnoreVersions", or empty for no validation
		expectLen     int
	}{
		{
			name: "from as mapping should fail",
			yamlContent: `modules:
  - source: "test"
    version: "1.0.0"
    from:
      key: value`,
			expectError: true,
		},
		{
			name: "from as null should work",
			yamlContent: `modules:
  - source: "test"
    version: "1.0.0"
    from: ~`,
			expectError:   false,
			validateField: "From",
			expectLen:     0,
		},
		{
			name: "ignore_versions as single string",
			yamlContent: `modules:
  - source: "test"
    version: "1.0.0"
    ignore_versions: "3.0.0"`,
			expectError:   false,
			validateField: "IgnoreVersions",
			expectLen:     1,
		},
		{
			name: "from with array containing empty strings",
			yamlContent: `modules:
  - source: "test"
    version: "1.0.0"
    from:
      - "1.0.0"
      - ""
      - "2.0.0"`,
			expectError:   false,
			validateField: "From",
			expectLen:     2, // Empty strings should be filtered out
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configFile := filepath.Join(tmpDir, "config.yml")
			if err := os.WriteFile(configFile, []byte(tt.yamlContent), 0644); err != nil {
				t.Fatalf("failed to write config file: %v", err)
			}

			modules, err := loadConfig(configFile)

			if tt.expectError {
				if err == nil {
					t.Error("expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				// Validate field length when specified
				if tt.validateField != "" {
					if len(modules) == 0 {
						t.Error("expected at least one module for field validation")
					} else {
						switch tt.validateField {
						case "From":
							if len(modules[0].From) != tt.expectLen {
								t.Errorf("From length = %d, want %d", len(modules[0].From), tt.expectLen)
							}
						case "IgnoreVersions":
							if len(modules[0].IgnoreVersions) != tt.expectLen {
								t.Errorf("IgnoreVersions length = %d, want %d", len(modules[0].IgnoreVersions), tt.expectLen)
							}
						default:
							t.Errorf("unknown validateField: %s", tt.validateField)
						}
					}
				}
			}
		})
	}
}

// TestProcessFilesIgnoreModulesFilter tests processFiles with ignore modules filter
func TestProcessFilesIgnoreModulesFilter(t *testing.T) {
	tmpDir := t.TempDir()

	tfContent := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "4.0.0"
}

module "legacy-vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "4.0.0"
}
`
	tfFile := filepath.Join(tmpDir, "main.tf")
	if err := os.WriteFile(tfFile, []byte(tfContent), 0644); err != nil {
		t.Fatalf("failed to write tf file: %v", err)
	}

	tests := []struct {
		name          string
		ignoreModules []string
		expectUpdates int
	}{
		{
			// processFiles returns 1 per file that had at least one module updated
			// Both modules in one file = 1 file update
			name:          "no ignore modules",
			ignoreModules: nil,
			expectUpdates: 1,
		},
		{
			// Ignoring "vpc" leaves only "legacy-vpc" to update
			name:          "ignore one module by name",
			ignoreModules: []string{"vpc"},
			expectUpdates: 1,
		},
		{
			// Ignoring "legacy-*" leaves only "vpc" to update
			name:          "ignore one module by pattern",
			ignoreModules: []string{"legacy-*"},
			expectUpdates: 1,
		},
		{
			// Ignoring all modules means no updates
			name:          "ignore all modules",
			ignoreModules: []string{"*"},
			expectUpdates: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset the file
			if err := os.WriteFile(tfFile, []byte(tfContent), 0644); err != nil {
				t.Fatalf("failed to reset tf file: %v", err)
			}

			flags := &cliFlags{
				dryRun: true,
				output: "text",
			}

			updates := []ModuleUpdate{
				{
					Source:        "terraform-aws-modules/vpc/aws",
					Version:       "5.0.0",
					IgnoreModules: tt.ignoreModules,
				},
			}

			oldStdout := os.Stdout
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("failed to create os.Pipe: %v", err)
			}
			os.Stdout = w

			totalUpdates := processFiles([]string{tfFile}, updates, flags)

			if err := w.Close(); err != nil {
				t.Fatalf("failed to close pipe writer: %v", err)
			}
			os.Stdout = oldStdout
			if _, err := io.ReadAll(r); err != nil {
				t.Fatalf("failed to read from pipe: %v", err)
			}

			if totalUpdates != tt.expectUpdates {
				t.Errorf("totalUpdates = %d, want %d", totalUpdates, tt.expectUpdates)
			}
		})
	}
}

// resetFlags resets the flag.CommandLine to allow re-parsing
// Note: parseFlags only validates the output format and calls log.Fatalf for that specific error.
// Most validation errors for flag combinations (e.g., conflicting -config with -module flags)
// are handled in loadModuleUpdates, not parseFlags. These program-terminating error handlers
// are intentionally not tested and would require code refactoring to test properly.
func resetFlags() {
	// Create a new FlagSet to clear all flags
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
}
