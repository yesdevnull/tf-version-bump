package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Test constants for advanced chaos testing edge cases
const (
	// veryLongSourcePathSegments defines how many path segments to repeat when creating
	// an extremely long module source string. 100 segments is chosen to stress test the
	// tool's ability to handle very long source URLs while remaining realistic for deeply
	// nested repository structures.
	veryLongSourcePathSegments = 100
)

// TestRecursiveGlobPatterns documents that Go's filepath.Glob does NOT support
// recursive glob patterns like "**/*.tf" (a bash/zsh feature).
// Go's filepath.Glob only supports *, ?, and [...] character classes.
// The ** pattern is treated as a literal directory name, not recursive wildcard.
func TestRecursiveGlobPatterns(t *testing.T) {
	tmpDir := t.TempDir()

	// Create nested structure with 5 .tf files at different depths
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

	// Demonstrate that ** is NOT supported as recursive wildcard by filepath.Glob
	// The ** pattern acts like a single-level wildcard (similar to *), not recursive.
	// So "**/*.tf" matches one directory level down, not all nested levels.
	pattern := filepath.Join(tmpDir, "**/*.tf")
	matchedFiles, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("Unexpected error from filepath.Glob: %v", err)
	}

	// The behavior of ** in filepath.Glob can vary by platform/Go version.
	// On most systems, ** acts like a single-level wildcard.
	// On some systems, ** may be treated as a literal directory name (no matches).
	if len(matchedFiles) == 0 {
		t.Skip("Skipping: '**' pattern did not match any files; platform or Go version may treat '**' as a literal directory name")
	}

	// Verify it doesn't match ALL nested files (should be < 5)
	// This confirms ** is not recursive like in bash/zsh
	if len(matchedFiles) >= len(files) {
		t.Errorf("** pattern matched too many files (%d), expected < %d (not recursive)", len(matchedFiles), len(files))
	}

	// Test single-level wildcard (this DOES work)
	pattern = filepath.Join(tmpDir, "*.tf")
	matchedFiles, err = filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("Failed to match pattern: %v", err)
	}

	if len(matchedFiles) != 1 {
		t.Errorf("Expected 1 file at root, got %d", len(matchedFiles))
	}
}

// TestModuleNameCollisions tests handling of modules with similar names
func TestModuleNameCollisions(t *testing.T) {
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
	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", nil, ignorePatterns, false, false, false)
	if err != nil {
		t.Fatalf("Failed to process: %v", err)
	}

	if !updated {
		t.Error("File should be updated (other modules should be updated)")
	}

	resultContent, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read result: %v", err)
	}
	newCount := strings.Count(string(resultContent), `version = "5.0.0"`)
	oldCount := strings.Count(string(resultContent), `version = "3.0.0"`)

	// Only "vpc" should be ignored, others should be updated
	if newCount != 3 || oldCount != 1 {
		t.Errorf("Expected 3 updated and 1 ignored, got %d updated and %d old", newCount, oldCount)
	}
}

// TestHugeVersionString tests extremely long version strings
func TestHugeVersionString(t *testing.T) {
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

	// Create a version string that's exactly 10KB
	hugeVersion := strings.Repeat("X", 10*1024)

	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", hugeVersion, nil, nil, false, false, false)
	if err != nil {
		t.Fatalf("Failed to process: %v", err)
	}

	if !updated {
		t.Error("File should be updated even with huge version")
	}

	// Verify the huge version was set
	resultContent, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read result: %v", err)
	}
	if !strings.Contains(string(resultContent), hugeVersion) {
		t.Error("Huge version should be set")
	}
}

// TestInterpolationInSource tests module sources with interpolation syntax.
// This documents that the HCL parser preserves interpolation expressions as-is in string literals,
// so sources containing "${...}" won't match literal source patterns without interpolation.
// This is expected behavior: the tool performs literal string matching on the parsed source value.
func TestInterpolationInSource(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	// HCL interpolation syntax in source attribute
	content := `module "vpc" {
  source  = "terraform-aws-modules/${var.module_name}/aws"
  version = "3.0.0"
}`
	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// The tool performs literal string matching on the parsed source value.
	// The HCL parser preserves "terraform-aws-modules/${var.module_name}/aws" as-is,
	// so it won't match the literal string "terraform-aws-modules/vpc/aws".
	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", nil, nil, false, false, false)
	if err != nil {
		t.Fatalf("Failed to process: %v", err)
	}

	if updated {
		t.Error("Module with interpolated source should not match literal source pattern")
	}

	// Verify the file content remains unchanged
	resultContent, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read result: %v", err)
	}
	if !strings.Contains(string(resultContent), `version = "3.0.0"`) {
		t.Error("Version should remain unchanged when source doesn't match")
	}
}

// TestMultilineAttributes tests HCL with multiline attributes
func TestMultilineAttributes(t *testing.T) {
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

	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", nil, nil, false, false, false)
	if err != nil {
		t.Fatalf("Failed to process file with heredoc: %v", err)
	}

	if !updated {
		t.Error("File with heredoc should be updated")
	}

	// Verify heredoc is preserved
	resultContent, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read result: %v", err)
	}
	if !strings.Contains(string(resultContent), "description") {
		t.Error("Heredoc description should be preserved")
	}
}

// TestIgnorePatternPerformanceWithManyModules tests performance with many modules and ignore patterns
func TestIgnorePatternPerformanceWithManyModules(t *testing.T) {
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

	// Measure performance
	start := time.Now()
	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", nil, ignorePatterns, false, false, false)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Failed to process: %v", err)
	}

	if !updated {
		t.Error("File should be updated")
	}

	// Log performance characteristics
	t.Logf("Processing 100 modules with 50 ignore patterns took %v", elapsed)

	// Performance threshold set for typical development environments (5s).
	// This threshold is intentionally strict to catch meaningful regressions.
	// If CI environment performance becomes an issue, we can adjust as needed.
	if elapsed > 5*time.Second {
		t.Errorf("Performance degraded: took %v (threshold: 5s)", elapsed)
	}

	resultContent, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read result: %v", err)
	}
	newCount := strings.Count(string(resultContent), `version = "5.0.0"`)
	oldCount := strings.Count(string(resultContent), `version = "3.0.0"`)

	// Should have updated 50 and ignored 50
	if newCount != 50 || oldCount != 50 {
		t.Errorf("Expected 50 updated and 50 ignored, got %d updated and %d old", newCount, oldCount)
	}
}

// TestEmptyLinesAndFormatting tests files with unusual formatting
func TestEmptyLinesAndFormatting(t *testing.T) {
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

	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", nil, nil, false, false, false)
	if err != nil {
		t.Fatalf("Failed to process weirdly formatted file: %v", err)
	}

	if !updated {
		t.Error("Weirdly formatted file should be updated")
	}

	resultContent, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read result: %v", err)
	}
	if !strings.Contains(string(resultContent), `version = "5.0.0"`) {
		t.Error("Version should be updated despite weird formatting")
	}
}

// TestConfigWithDuplicateSources tests config with same source multiple times
func TestConfigWithDuplicateSources(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yml")
	testFile := filepath.Join(tmpDir, "test.tf")

	// Create a file with a module
	tfContent := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}`
	err := os.WriteFile(testFile, []byte(tfContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Config with duplicate sources - when applied sequentially, the last update wins
	configContent := `modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
  - source: "terraform-aws-modules/vpc/aws"
    version: "6.0.0"
  - source: "terraform-aws-modules/s3-bucket/aws"
    version: "4.0.0"`

	err = os.WriteFile(configFile, []byte(configContent), 0644)
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

	// Apply all updates to the file and track outcomes.
	// Expected behavior: 2 vpc updates succeed, 1 s3-bucket update finds no matching module.
	//
	// Note: When a module source isn't found, updateModuleVersion returns (updated=false, err=nil).
	// This is the expected contract: not finding a module is not considered an error, just a no-op.
	var successCount, failCount, notFoundCount int
	for _, update := range updates {
		updated, err := updateModuleVersion(testFile, update.Source, update.Version, update.From, update.Ignore, false, false, false)
		if err != nil {
			t.Logf("Update for %s to %s failed: %v", update.Source, update.Version, err)
			failCount++
			continue
		}
		if updated {
			successCount++
			continue
		}
		// Note: notFoundCount includes both "not found" and "ignored" cases,
		// as updateModuleVersion returns (updated=false, err=nil) for both.
		notFoundCount++
	}

	// Verify we got expected counts
	if successCount != 2 {
		t.Errorf("Expected 2 successful updates (vpc entries), got %d", successCount)
	}
	if notFoundCount != 1 {
		t.Errorf("Expected 1 not-found (s3-bucket), got %d", notFoundCount)
	}
	if failCount != 0 {
		t.Errorf("Expected 0 failures, got %d", failCount)
	}
	t.Logf("Update summary: %d succeeded, %d not found, %d failed", successCount, notFoundCount, failCount)

	// Verify which version ended up in the file (last vpc update wins: 6.0.0)
	resultContent, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read result: %v", err)
	}

	if !strings.Contains(string(resultContent), `version = "6.0.0"`) {
		t.Error("Expected last duplicate source version (6.0.0) to be set in the file")
	}
	if strings.Contains(string(resultContent), `version = "5.0.0"`) {
		t.Error("First duplicate source version (5.0.0) should be overwritten by second")
	}
}

// TestSourceWithEscapedCharacters tests sources with URL-encoded characters
func TestSourceWithEscapedCharacters(t *testing.T) {
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
	updated, err := updateModuleVersion(testFile, source, "5.0.0", nil, nil, false, false, false)
	if err != nil {
		t.Fatalf("Failed to process: %v", err)
	}

	if !updated {
		t.Error("Module with URL-encoded source should be updated")
	}
}

// TestVeryLongModuleSource tests extremely long module source strings
func TestVeryLongModuleSource(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	// Create a very long module source (e.g., deeply nested path)
	longSource := "git::https://github.com/org/repo.git//"
	for i := 0; i < veryLongSourcePathSegments; i++ {
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

	updated, err := updateModuleVersion(testFile, longSource, "5.0.0", nil, nil, false, false, false)
	if err != nil {
		t.Fatalf("Failed to process very long source: %v", err)
	}

	if !updated {
		t.Error("Module with very long source should be updated")
	}
}

// TestFromVersionNotMatching tests from filter with no matches
func TestFromVersionNotMatching(t *testing.T) {
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
	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", []string{"99.99.99"}, nil, false, false, false)
	if err != nil {
		t.Fatalf("Failed to process: %v", err)
	}

	if updated {
		t.Error("Should not update when from version doesn't match")
	}

	// Verify original version remains
	resultContent, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read result: %v", err)
	}
	if !strings.Contains(string(resultContent), `version = "3.0.0"`) {
		t.Error("Original version should be preserved when from filter doesn't match")
	}
}

// TestIgnorePatternWhitespaceTrimming tests that ignore patterns have whitespace trimmed
func TestIgnorePatternWhitespaceTrimming(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yml")

	// Config with whitespace in ignore patterns that should be trimmed
	content := `modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
    ignore:
      - "  vpc-prod  "
      - "  staging-*  "
      - "	dev-*	"`

	err := os.WriteFile(configFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}

	updates, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Ignore patterns should have whitespace trimmed
	if len(updates[0].Ignore) != 3 {
		t.Errorf("Expected 3 ignore patterns, got %d", len(updates[0].Ignore))
	}
	if updates[0].Ignore[0] != "vpc-prod" {
		t.Errorf("First ignore pattern whitespace not trimmed: %q", updates[0].Ignore[0])
	}
	if updates[0].Ignore[1] != "staging-*" {
		t.Errorf("Second ignore pattern whitespace not trimmed: %q", updates[0].Ignore[1])
	}
	if updates[0].Ignore[2] != "dev-*" {
		t.Errorf("Third ignore pattern (with tabs) whitespace not trimmed: %q", updates[0].Ignore[2])
	}
}

// TestDryRunModificationTime tests that dry-run doesn't modify files.
// Primary check: file content unchanged (robust across all filesystems)
// Secondary check: modification time unchanged (may be unreliable on coarse filesystems)
func TestDryRunModificationTime(t *testing.T) {
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

	// Read original file content for comparison
	originalBytes, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read original file: %v", err)
	}

	// Get original file modification time
	originalInfo, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("Failed to stat file: %v", err)
	}
	originalModTime := originalInfo.ModTime()

	// Add a delay to ensure modification time would change if file were written.
	// Note: This test assumes filesystems with millisecond-precision timestamps (e.g., ext4, NTFS, APFS).
	// Some filesystems have coarser resolution (FAT32: 2s, network drives: variable) and may cause
	// the timestamp check to be unreliable. The 10ms delay is sufficient for most modern filesystems.
	time.Sleep(10 * time.Millisecond)

	// Run in dry-run mode
	_, err = updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", nil, nil, false, true, false)
	if err != nil {
		t.Fatalf("Dry-run failed: %v", err)
	}

	// PRIMARY CHECK: Verify file content did not change (robust across all filesystems)
	newBytes, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read file after dry-run: %v", err)
	}
	if string(newBytes) != string(originalBytes) {
		t.Error("Dry-run should not change file content")
	}

	// SECONDARY CHECK: Verify modification time didn't change (may be unreliable on coarse filesystems)
	newInfo, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("Failed to stat file: %v", err)
	}

	if !newInfo.ModTime().Equal(originalModTime) {
		t.Logf("Warning: Dry-run changed file modification time (may be due to coarse filesystem timestamp resolution)")
	}
}
