package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestChaosRecursiveGlobPatterns tests various recursive glob patterns
func TestChaosRecursiveGlobPatterns(t *testing.T) {
	tmpDir := t.TempDir()

	// Create nested structure
	dirs := []string{
		"modules",
		"modules/vpc",
		"modules/vpc/prod",
		"environments",
		"environments/staging",
	}

	for _, dir := range dirs {
		err := os.MkdirAll(filepath.Join(tmpDir, dir), 0755)
		if err != nil {
			t.Fatalf("Failed to create directory %s: %v", dir, err)
		}
	}

	// Create files at different depths
	content := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}`

	files := []string{
		"main.tf",
		"modules/main.tf",
		"modules/vpc/main.tf",
		"modules/vpc/prod/main.tf",
		"environments/staging/main.tf",
	}

	for _, file := range files {
		fullPath := filepath.Join(tmpDir, file)
		err := os.WriteFile(fullPath, []byte(content), 0644)
		if err != nil {
			t.Fatalf("Failed to create file %s: %v", file, err)
		}
	}

	// Test recursive glob pattern
	pattern := filepath.Join(tmpDir, "**/*.tf")
	matchedFiles, err := filepath.Glob(pattern)
	if err != nil {
		t.Logf("Recursive glob not supported on this platform: %v", err)
	} else {
		t.Logf("Matched %d files with recursive pattern", len(matchedFiles))
	}

	// Test single-level wildcard
	pattern = filepath.Join(tmpDir, "*.tf")
	matchedFiles, err = filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("Failed to match pattern: %v", err)
	}

	if len(matchedFiles) != 1 {
		t.Errorf("Expected 1 file at root, got %d", len(matchedFiles))
	}
}

// TestChaosModuleNameCollisions tests handling of modules with similar names
func TestChaosModuleNameCollisions(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	// Modules with similar names that could confuse pattern matching
	content := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}

module "vpc-prod" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}

module "prod-vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}

module "vpc-prod-legacy" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}`

	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Test ignoring only exact "vpc"
	ignorePatterns := []string{"vpc"}
	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", "", ignorePatterns, false, false, false)
	if err != nil {
		t.Fatalf("Failed to process: %v", err)
	}

	if !updated {
		t.Error("File should be updated (other modules should be updated)")
	}

	resultContent, _ := os.ReadFile(testFile)
	newCount := strings.Count(string(resultContent), `version = "5.0.0"`)
	oldCount := strings.Count(string(resultContent), `version = "3.0.0"`)

	// Only "vpc" should be ignored, others should be updated
	if newCount != 3 || oldCount != 1 {
		t.Errorf("Expected 3 updated and 1 ignored, got %d updated and %d old", newCount, oldCount)
	}
}

// TestChaosHugeVersionString tests extremely long version strings
func TestChaosHugeVersionString(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	content := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "1.0.0"
}`
	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create a version string that's 10KB
	hugeVersion := strings.Repeat("1.0.0-", 1000) + "final"

	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", hugeVersion, "", nil, false, false, false)
	if err != nil {
		t.Fatalf("Failed to process: %v", err)
	}

	if !updated {
		t.Error("File should be updated even with huge version")
	}

	// Verify the huge version was set
	resultContent, _ := os.ReadFile(testFile)
	if !strings.Contains(string(resultContent), hugeVersion) {
		t.Error("Huge version should be set")
	}
}

// TestChaosInterpolationInSource tests module sources with interpolation syntax
func TestChaosInterpolationInSource(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	// While interpolation in source is unusual, HCL might allow it
	content := `module "vpc" {
  source  = "terraform-aws-modules/${var.module_name}/aws"
  version = "3.0.0"
}`
	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// This won't match because the source has interpolation
	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", "", nil, false, false, false)
	if err != nil {
		t.Fatalf("Failed to process: %v", err)
	}

	if updated {
		t.Error("Module with interpolated source should not match literal source")
	}
}

// TestChaosMultilineAttributes tests HCL with multiline attributes
func TestChaosMultilineAttributes(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	// HCL with multiline strings (heredoc)
	content := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"

  description = <<-EOT
    This is a multiline
    description for the VPC module
  EOT
}`
	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", "", nil, false, false, false)
	if err != nil {
		t.Fatalf("Failed to process file with heredoc: %v", err)
	}

	if !updated {
		t.Error("File with heredoc should be updated")
	}

	// Verify heredoc is preserved
	resultContent, _ := os.ReadFile(testFile)
	if !strings.Contains(string(resultContent), "description") {
		t.Error("Heredoc description should be preserved")
	}
}

// TestChaosIgnorePatternPerformance tests performance with many modules and ignore patterns
func TestChaosIgnorePatternPerformance(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	// Create file with many modules
	var contentBuilder strings.Builder
	for i := 0; i < 100; i++ {
		contentBuilder.WriteString(fmt.Sprintf(`module "vpc-%d" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}

`, i))
	}

	err := os.WriteFile(testFile, []byte(contentBuilder.String()), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create ignore patterns that match half of them
	var ignorePatterns []string
	for i := 0; i < 50; i++ {
		ignorePatterns = append(ignorePatterns, fmt.Sprintf("vpc-%d", i))
	}

	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", "", ignorePatterns, false, false, false)
	if err != nil {
		t.Fatalf("Failed to process: %v", err)
	}

	if !updated {
		t.Error("File should be updated")
	}

	resultContent, _ := os.ReadFile(testFile)
	newCount := strings.Count(string(resultContent), `version = "5.0.0"`)
	oldCount := strings.Count(string(resultContent), `version = "3.0.0"`)

	// Should have updated 50 and ignored 50
	if newCount != 50 || oldCount != 50 {
		t.Errorf("Expected 50 updated and 50 ignored, got %d updated and %d old", newCount, oldCount)
	}
}

// TestChaosEmptyLinesAndFormatting tests files with unusual formatting
func TestChaosEmptyLinesAndFormatting(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	// File with lots of empty lines and strange formatting
	content := `


module       "vpc"        {


  source       =       "terraform-aws-modules/vpc/aws"


  version      =       "3.0.0"


}


`
	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", "", nil, false, false, false)
	if err != nil {
		t.Fatalf("Failed to process weirdly formatted file: %v", err)
	}

	if !updated {
		t.Error("Weirdly formatted file should be updated")
	}

	resultContent, _ := os.ReadFile(testFile)
	if !strings.Contains(string(resultContent), `version = "5.0.0"`) {
		t.Error("Version should be updated despite weird formatting")
	}
}

// TestChaosConfigWithDuplicateSources tests config with same source multiple times
func TestChaosConfigWithDuplicateSources(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yml")

	// Config with duplicate sources (different versions)
	content := `modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
  - source: "terraform-aws-modules/vpc/aws"
    version: "6.0.0"
  - source: "terraform-aws-modules/s3-bucket/aws"
    version: "4.0.0"`

	err := os.WriteFile(configFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}

	updates, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Should have all 3 entries (duplicates allowed)
	if len(updates) != 3 {
		t.Errorf("Expected 3 module updates, got %d", len(updates))
	}

	// Document that both vpc updates will be processed
	t.Logf("Config with duplicate sources has %d updates", len(updates))
}

// TestChaosSourceWithEscapedCharacters tests sources with URL-encoded characters
func TestChaosSourceWithEscapedCharacters(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	// Source with URL encoding
	source := "git::https://github.com/example/module.git?ref=v1.0.0%2Bbuild.1"
	content := `module "test" {
  source  = "` + source + `"
  version = "3.0.0"
}`
	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Exact match should work
	updated, err := updateModuleVersion(testFile, source, "5.0.0", "", nil, false, false, false)
	if err != nil {
		t.Fatalf("Failed to process: %v", err)
	}

	if !updated {
		t.Error("Module with URL-encoded source should be updated")
	}
}

// TestChaosVeryLongModuleSource tests extremely long module source strings
func TestChaosVeryLongModuleSource(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	// Create a very long module source (e.g., deeply nested path)
	longSource := "git::https://github.com/org/repo.git//"
	for i := 0; i < 100; i++ {
		longSource += "very/long/path/to/module/"
	}
	longSource += "final"

	content := `module "test" {
  source  = "` + longSource + `"
  version = "3.0.0"
}`
	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	updated, err := updateModuleVersion(testFile, longSource, "5.0.0", "", nil, false, false, false)
	if err != nil {
		t.Fatalf("Failed to process very long source: %v", err)
	}

	if !updated {
		t.Error("Module with very long source should be updated")
	}
}

// TestChaosFromVersionNotMatching tests from filter with no matches
func TestChaosFromVersionNotMatching(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	content := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}`
	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// From filter that doesn't match any modules
	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", "99.99.99", nil, false, false, false)
	if err != nil {
		t.Fatalf("Failed to process: %v", err)
	}

	if updated {
		t.Error("Should not update when from version doesn't match")
	}

	// Verify original version remains
	resultContent, _ := os.ReadFile(testFile)
	if !strings.Contains(string(resultContent), `version = "3.0.0"`) {
		t.Error("Original version should be preserved when from filter doesn't match")
	}
}

// TestChaosConfigWithWhitespaceInValues tests config with extra whitespace
func TestChaosConfigWithWhitespaceInValues(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yml")

	// Config with whitespace that should be trimmed
	content := `modules:
  - source: "  terraform-aws-modules/vpc/aws  "
    version: "  5.0.0  "
    from: "  3.0.0  "
    ignore:
      - "  vpc  "
      - "  test-*  "`

	err := os.WriteFile(configFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}

	updates, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Whitespace should be trimmed
	if updates[0].Source != "terraform-aws-modules/vpc/aws" {
		t.Errorf("Source whitespace not trimmed: %q", updates[0].Source)
	}
	if updates[0].Version != "5.0.0" {
		t.Errorf("Version whitespace not trimmed: %q", updates[0].Version)
	}
	if updates[0].From != "3.0.0" {
		t.Errorf("From whitespace not trimmed: %q", updates[0].From)
	}
	if len(updates[0].Ignore) != 2 {
		t.Errorf("Expected 2 ignore patterns, got %d", len(updates[0].Ignore))
	}
	if updates[0].Ignore[0] != "vpc" {
		t.Errorf("Ignore pattern whitespace not trimmed: %q", updates[0].Ignore[0])
	}
}

// TestChaosDryRunDoesNotModify tests that dry-run truly doesn't modify files
func TestChaosDryRunDoesNotModify(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	originalContent := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}`
	err := os.WriteFile(testFile, []byte(originalContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Get original file info
	originalInfo, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("Failed to stat file: %v", err)
	}
	originalModTime := originalInfo.ModTime()

	// Run in dry-run mode
	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", "", nil, false, true, false)
	if err != nil {
		t.Fatalf("Dry-run failed: %v", err)
	}

	if !updated {
		t.Error("Dry-run should report what would be updated")
	}

	// Verify file was NOT modified
	resultContent, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	if string(resultContent) != originalContent {
		t.Error("Dry-run should not modify file content")
	}

	// Check modification time didn't change
	newInfo, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("Failed to stat file: %v", err)
	}

	if !newInfo.ModTime().Equal(originalModTime) {
		t.Error("Dry-run should not change file modification time")
	}
}
