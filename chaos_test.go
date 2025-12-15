package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Test constants for chaos testing edge cases
const (
	// maxNestedSubmodules defines the nesting depth for testing extremely nested module sources.
	// This tests the tool's ability to handle deeply nested Terraform module paths like
	// "module//sub//sub//sub...". 50 levels is chosen as an extreme but realistic edge case.
	maxNestedSubmodules = 50

	// stressTestIgnorePatterns defines the number of ignore patterns for stress testing.
	// 10,000 patterns is chosen to test performance with a very large number of patterns,
	// representative of enterprise-scale configurations where users might have hundreds
	// of modules to ignore across multiple environments.
	stressTestIgnorePatterns = 10000
)

// TestNullBytesInFile tests handling of files with null bytes
func TestNullBytesInFile(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	// Create file with null byte in the middle
	content := "module \"vpc\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\x00\n  version = \"3.0.0\"\n}"
	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Parsing should fail with null bytes in file
	_, err = updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", nil, nil, nil, false, false, false, "text")

	// Assert that null bytes cause an error
	if err == nil {
		t.Fatal("Expected error when parsing file with null bytes, but got none")
	}
	t.Logf("Null bytes correctly caused parse error: %v", err)
}

// TestBinaryFileContent tests what happens with binary content
func TestBinaryFileContent(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	// Create file with binary content that looks like it might have HCL
	binaryContent := []byte{0xFF, 0xFE, 0x00, 0x00, 'm', 'o', 'd', 'u', 'l', 'e'}
	err := os.WriteFile(testFile, binaryContent, 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Should fail to parse as HCL
	_, err = updateModuleVersion(testFile, "test", "1.0.0", nil, nil, nil, false, false, false, "text")

	if err == nil {
		t.Fatal("Expected error when parsing binary content as HCL")
	}

	// Verify error message contains expected keywords
	errMsg := err.Error()
	if strings.Contains(errMsg, "parse") || strings.Contains(errMsg, "invalid") {
		t.Logf("Error message contains expected keyword: %v", errMsg)
	} else {
		t.Fatalf("Unexpected error message (expected 'parse' or 'invalid'): %v", errMsg)
	}
}

// TestUTF8BOM tests handling of UTF-8 BOM at file start
func TestUTF8BOM(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	// Create file with UTF-8 BOM
	content := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}`
	// Prepend UTF-8 BOM to content
	fullContent := []byte{0xEF, 0xBB, 0xBF}
	fullContent = append(fullContent, []byte(content)...)
	err := os.WriteFile(testFile, fullContent, 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// HCL parser should handle BOM
	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", nil, nil, nil, false, false, false, "text")
	if err != nil {
		t.Fatalf("Expected BOM to be handled, but got error: %v", err)
	}

	// Verify the update worked
	if !updated {
		t.Fatal("Expected module to be updated, but updated=false")
	}

	resultContent, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read result: %v", err)
	}
	if !strings.Contains(string(resultContent), `version = "5.0.0"`) {
		t.Error("Module was marked updated but version not changed")
	}
}

// TestMixedLineEndings tests files with mixed CRLF and LF
func TestMixedLineEndings(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	// Mix of CRLF and LF
	content := "module \"vpc\" {\r\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.0.0\"\r\n}"
	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", nil, nil, nil, false, false, false, "text")
	if err != nil {
		t.Fatalf("Failed to process file with mixed line endings: %v", err)
	}

	if !updated {
		t.Error("File with mixed line endings should be updated")
	}
}

// TestSymbolicLinks tests handling of symbolic links
func TestSymbolicLinks(t *testing.T) {
	tmpDir := t.TempDir()
	realFile := filepath.Join(tmpDir, "real.tf")
	linkFile := filepath.Join(tmpDir, "link.tf")

	content := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}`
	err := os.WriteFile(realFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create real file: %v", err)
	}

	// Create symbolic link
	err = os.Symlink(realFile, linkFile)
	if err != nil {
		t.Skipf("Cannot create symlink (may not be supported): %v", err)
	}

	// Update via symlink
	updated, err := updateModuleVersion(linkFile, "terraform-aws-modules/vpc/aws", "5.0.0", nil, nil, nil, false, false, false, "text")
	if err != nil {
		t.Fatalf("Failed to process symlink: %v", err)
	}

	if !updated {
		t.Error("Symlink file should be updated")
	}

	// Verify the real file was updated
	resultContent, err := os.ReadFile(realFile)
	if err != nil {
		t.Fatalf("Failed to read real file: %v", err)
	}

	if !strings.Contains(string(resultContent), `version = "5.0.0"`) {
		t.Error("Real file should be updated when processing via symlink")
	}
}

// TestReadOnlyFile tests handling of read-only files
func TestReadOnlyFile(t *testing.T) {
	// Skip if running as root (root can write to read-only files)
	if os.Geteuid() == 0 {
		t.Skip("Skipping read-only file test when running as root")
	}

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "readonly.tf")

	content := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}`
	err := os.WriteFile(testFile, []byte(content), 0444) // Read-only
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Should fail to write
	_, err = updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", nil, nil, nil, false, false, false, "text")

	if err == nil {
		t.Error("Expected error when trying to write to read-only file")
		return
	}

	if !strings.Contains(err.Error(), "failed to write") {
		t.Errorf("Expected error about write failure, got: %v", err)
		return
	}

	t.Logf("Got expected error: %v", err)
}

// TestExtremelyNestedModuleSource tests deeply nested module sources
func TestExtremelyNestedModuleSource(t *testing.T) {
	// Module source with many subpaths
	deepSource := "terraform-aws-modules/iam/aws"
	for i := 0; i < maxNestedSubmodules; i++ {
		deepSource += "//modules/submodule"
	}

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	content := `module "test" {
  source  = "` + deepSource + `"
  version = "3.0.0"
}`
	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	updated, err := updateModuleVersion(testFile, deepSource, "5.0.0", nil, nil, nil, false, false, false, "text")
	if err != nil {
		t.Fatalf("Failed to process deeply nested source: %v", err)
	}

	if !updated {
		t.Error("File should be updated regardless of source depth")
	}
}

// TestModuleSourceWithQueryParams tests module sources with query parameters
func TestModuleSourceWithQueryParams(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{"git with ref", "git::https://github.com/example/module.git?ref=v1.0.0"},
		{"git with depth", "git::https://github.com/example/module.git?depth=1"},
		{"http with params", "https://example.com/module.zip?token=abc123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			testFile := filepath.Join(tmpDir, "test.tf")

			content := `module "test" {
  source  = "` + tt.source + `"
  version = "3.0.0"
}`
			err := os.WriteFile(testFile, []byte(content), 0644)
			if err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			updated, err := updateModuleVersion(testFile, tt.source, "5.0.0", nil, nil, nil, false, false, false, "text")
			if err != nil {
				t.Fatalf("Failed to process source with query params: %v", err)
			}

			if !updated {
				t.Error("File should be updated for source with query params")
			}
		})
	}
}

// TestInvalidVersionFormats tests various invalid version strings
func TestInvalidVersionFormats(t *testing.T) {
	// Test various "invalid" version strings - tool doesn't validate, it just sets them
	tests := []struct {
		name    string
		version string
	}{
		{"empty string", ""},
		{"whitespace only", "   "},
		{"non-semantic version", "not-a-version"},
		{"path traversal attempt", "../../../etc/passwd"},
		{"multiple lines", "1.0.0\n2.0.0"},
		{"command injection attempt", "1.0.0; rm -rf /"},
		{"extremely long version", strings.Repeat("1.0.0-", 10000) + "final"}, // ~60KB string
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

			// Tool doesn't validate version format, it will just set whatever is given.
			// Note: Some edge cases (e.g., multiline strings) may cause HCL parse errors during
			// file write/format operations, as HCL doesn't support newlines in attribute values.
			updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", tt.version, nil, nil, nil, false, false, false, "text")

			// Verify behavior - if no error, file should contain the version string
			if err == nil && updated {
				resultContent, err := os.ReadFile(testFile)
				if err != nil {
					t.Fatalf("Failed to read result file: %v", err)
				}
				// Verify file contains the expected version string
				// Note: HCL formatter may escape special characters (e.g., \n becomes \\n)
				contentStr := string(resultContent)
				if !strings.Contains(contentStr, tt.version) && !strings.Contains(contentStr, strings.ReplaceAll(tt.version, "\n", "\\n")) {
					t.Errorf("Expected file to contain version %q (or escaped form), but it doesn't", tt.version)
				}
			} else if err != nil {
				// Expected for cases like multiline strings which HCL rejects
				t.Logf("Version %q caused error: %v", tt.version, err)
			}
		})
	}
}

// TestExtremeWhitespace tests files with unusual whitespace
func TestExtremeWhitespace(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name: "many spaces",
			content: `module     "vpc"     {
  source                =                "terraform-aws-modules/vpc/aws"
  version               =                "3.0.0"
}`,
		},
		{
			name: "tabs everywhere",
			content: "module\t\"vpc\"\t{\n\tsource\t=\t\"terraform-aws-modules/vpc/aws\"\n\tversion\t=\t\"3.0.0\"\n}",
		},
		{
			name: "mixed tabs and spaces",
			content: "module  \t  \"vpc\"  \t  {\n  \t  source \t = \t \"terraform-aws-modules/vpc/aws\"\n\t  version  \t=  \t\"3.0.0\"\n}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			testFile := filepath.Join(tmpDir, "test.tf")

			err := os.WriteFile(testFile, []byte(tt.content), 0644)
			if err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", nil, nil, nil, false, false, false, "text")
			if err != nil {
				t.Fatalf("Failed to process file with extreme whitespace: %v", err)
			}

			if !updated {
				t.Error("File with extreme whitespace should be updated")
			}
		})
	}
}

// TestModuleDuplicateNames tests handling of duplicate module names
func TestModuleDuplicateNames(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	// Terraform technically allows duplicate names (though it's a bad practice)
	content := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "4.0.0"
}`

	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", nil, nil, nil, false, false, false, "text")
	if err != nil {
		t.Fatalf("Failed to process file with duplicate names: %v", err)
	}

	if !updated {
		t.Error("File should be updated")
	}

	// Both modules should be updated
	resultContent, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read result: %v", err)
	}

	count := strings.Count(string(resultContent), `version = "5.0.0"`)
	if count != 2 {
		t.Errorf("Expected both duplicate modules to be updated, got %d updates", count)
	}
}

// TestVeryLargeIgnoreList tests behavior with many ignore patterns
func TestVeryLargeIgnoreList(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	content := `module "vpc-prod" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}`
	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create a very large ignore list
	var ignorePatterns []string
	for i := 0; i < stressTestIgnorePatterns; i++ {
		ignorePatterns = append(ignorePatterns, fmt.Sprintf("pattern-%d", i))
	}

	// Measure performance with large ignore list
	start := time.Now()
	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", nil, nil, ignorePatterns, false, false, false, "text")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Failed with large ignore list: %v", err)
	}

	if !updated {
		t.Error("File should be updated (vpc-prod doesn't match any pattern)")
	}

	// Log performance characteristics
	t.Logf("Processing with %d ignore patterns took %v", stressTestIgnorePatterns, elapsed)

	// Set a stricter performance threshold (should complete within 1 second)
	if elapsed > 1*time.Second {
		t.Errorf("Performance degraded: took %v (threshold: 1s)", elapsed)
	}
}

// TestComplexPatternMatching tests edge cases for the custom matchPattern function.
// Note: This tests the custom matchPattern implementation (not filepath.Match).
// matchPattern is a simple wildcard matcher that only recognizes * as special;
// all other characters including [], (), etc. are treated as literals.
func TestComplexPatternMatching(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		pattern  string
		expected bool
	}{
		{"pattern with dots", "vpc.prod.v1", "vpc.*.v1", true},
		// The pattern "vpc[*]" treats "[" and "]" as literal characters, with * matching any characters between them.
		{"star wildcard with literal brackets", "vpc[0]", "vpc[*]", true},
		// The pattern "vpc(*)" treats "(" and ")" as literal characters, with * matching any characters between them.
		{"star wildcard with literal parens", "vpc(prod)", "vpc(*)", true},
		{"many wildcards in row", "test", "***test***", true},
		{"wildcard with empty parts", "test", "*test*", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchPattern(tt.input, tt.pattern)
			if result != tt.expected {
				t.Errorf("matchPattern(%q, %q) = %v, want %v", tt.input, tt.pattern, result, tt.expected)
			}
		})
	}
}

// TestConfigWithEmptyModulesList tests config with empty modules list
func TestConfigWithEmptyModulesList(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yml")

	// Config with empty modules list
	content := `modules: []`
	err := os.WriteFile(configFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}

	updates, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("Failed to load empty config: %v", err)
	}

	if len(updates) != 0 {
		t.Errorf("Expected 0 updates, got %d", len(updates))
	}
}

// TestConfigWithNullValues tests YAML with null values
func TestConfigWithNullValues(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yml")

	// Config with null values
	content := `modules:
  - source: null
    version: "1.0.0"`
	err := os.WriteFile(configFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}

	_, err = loadConfig(configFile)
	// Should error because source is required
	if err == nil {
		t.Error("Expected error for null source value")
	} else {
		// Check that the error message is about missing or null source
		errMsg := err.Error()
		if !strings.Contains(errMsg, "source") && !strings.Contains(errMsg, "missing") {
			t.Errorf("Error message does not mention 'source' or 'missing': %v", err)
		}
	}
}

// TestFileWithOnlyComments tests file containing only comments
func TestFileWithOnlyComments(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	content := `# This is a comment
# Another comment
// Yet another comment
/* Multi-line
   comment */`
	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	updated, err := updateModuleVersion(testFile, "test", "1.0.0", nil, nil, nil, false, false, false, "text")
	if err != nil {
		t.Fatalf("Failed to process comments-only file: %v", err)
	}

	if updated {
		t.Error("Comments-only file should not be marked as updated")
	}
}

// TestNestedQuotesInAttributes tests module attributes with nested quotes
func TestNestedQuotesInAttributes(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	// HCL with nested quotes (escaped)
	content := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
  tags = {
    Name = "VPC with \"quotes\""
  }
}`
	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", nil, nil, nil, false, false, false, "text")
	if err != nil {
		t.Fatalf("Failed to process file with nested quotes: %v", err)
	}

	if !updated {
		t.Error("File should be updated")
	}

	// Verify the version was updated (main goal)
	resultContent, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read result: %v", err)
	}

	if !strings.Contains(string(resultContent), `version = "5.0.0"`) {
		t.Error("Version should be updated")
	}

	// Verify tags block is still present (HCL formatter may change exact quote escaping)
	if !strings.Contains(string(resultContent), "tags") {
		t.Error("Tags block should be preserved")
	}

	// Document that the file was processable with nested quotes
	t.Logf("Successfully processed file with nested quotes")
}

// TestTrailingWhitespace tests files with trailing whitespace
func TestTrailingWhitespace(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	// File with trailing spaces and tabs
	content := "module \"vpc\" {  \t  \n  source  = \"terraform-aws-modules/vpc/aws\"  \n  version = \"3.0.0\"  \t\n}  \t  "
	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", nil, nil, nil, false, false, false, "text")
	if err != nil {
		t.Fatalf("Failed to process file with trailing whitespace: %v", err)
	}

	if !updated {
		t.Error("File with trailing whitespace should be updated")
	}
}

// TestFromVersionWithSpecialChars tests from filter with special characters
func TestFromVersionWithSpecialChars(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	// Version with special characters
	content := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0-rc.1+build.123"
}`
	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Exact match should work
	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", []string{"3.0.0-rc.1+build.123"}, nil, nil, false, false, false, "text")
	if err != nil {
		t.Fatalf("Failed to process: %v", err)
	}

	if !updated {
		t.Error("Should update when from version exactly matches")
	}
}
