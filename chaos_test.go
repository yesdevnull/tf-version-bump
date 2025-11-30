package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestChaosNullBytesInFile tests handling of files with null bytes
func TestChaosNullBytesInFile(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	// Create file with null byte in the middle
	content := "module \"vpc\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\x00\n  version = \"3.0.0\"\n}"
	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// The HCL parser should handle this - let's see what happens
	_, err = updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", "", nil, false, false, false)

	// Document the behavior - null bytes might cause parsing errors
	if err != nil {
		t.Logf("Null bytes in file cause expected error: %v", err)
	}
}

// TestChaosBinaryFileContent tests what happens with binary content
func TestChaosBinaryFileContent(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	// Create file with binary content that looks like it might have HCL
	binaryContent := []byte{0xFF, 0xFE, 0x00, 0x00, 'm', 'o', 'd', 'u', 'l', 'e'}
	err := os.WriteFile(testFile, binaryContent, 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Should fail to parse as HCL
	_, err = updateModuleVersion(testFile, "test", "1.0.0", "", nil, false, false, false)

	if err == nil {
		t.Error("Expected error when parsing binary content as HCL")
	}
}

// TestChaosUTF8BOM tests handling of UTF-8 BOM at file start
func TestChaosUTF8BOM(t *testing.T) {
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
	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", "", nil, false, false, false)
	if err != nil {
		t.Logf("UTF-8 BOM handling: %v", err)
	}

	// If it succeeds, verify the update worked
	if updated {
		resultContent, err := os.ReadFile(testFile)
		if err != nil {
			t.Fatalf("Failed to read result: %v", err)
		}
		if !strings.Contains(string(resultContent), `version = "5.0.0"`) {
			t.Error("BOM file was marked updated but version not changed")
		}
	}
}

// TestChaosMixedLineEndings tests files with mixed CRLF and LF
func TestChaosMixedLineEndings(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	// Mix of CRLF and LF
	content := "module \"vpc\" {\r\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.0.0\"\r\n}"
	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", "", nil, false, false, false)
	if err != nil {
		t.Fatalf("Failed to process file with mixed line endings: %v", err)
	}

	if !updated {
		t.Error("File with mixed line endings should be updated")
	}
}

// TestChaosSymbolicLinks tests handling of symbolic links
func TestChaosSymbolicLinks(t *testing.T) {
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
	updated, err := updateModuleVersion(linkFile, "terraform-aws-modules/vpc/aws", "5.0.0", "", nil, false, false, false)
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

// TestChaosReadOnlyFile tests handling of read-only files
func TestChaosReadOnlyFile(t *testing.T) {
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
	_, err = updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", "", nil, false, false, false)

	if err == nil {
		t.Error("Expected error when trying to write to read-only file")
	}

	// Verify error message is informative
	if err != nil && !strings.Contains(err.Error(), "failed to write") {
		t.Logf("Error message: %v", err)
	}
}

// TestChaosExtremelyNestedModuleSource tests deeply nested module sources
func TestChaosExtremelyNestedModuleSource(t *testing.T) {
	// Module source with many subpaths
	deepSource := "terraform-aws-modules/iam/aws"
	for i := 0; i < 50; i++ {
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

	updated, err := updateModuleVersion(testFile, deepSource, "5.0.0", "", nil, false, false, false)
	if err != nil {
		t.Fatalf("Failed to process deeply nested source: %v", err)
	}

	if !updated {
		t.Error("File should be updated regardless of source depth")
	}
}

// TestChaosModuleSourceWithQueryParams tests module sources with query parameters
func TestChaosModuleSourceWithQueryParams(t *testing.T) {
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

			updated, err := updateModuleVersion(testFile, tt.source, "5.0.0", "", nil, false, false, false)
			if err != nil {
				t.Fatalf("Failed to process source with query params: %v", err)
			}

			if !updated {
				t.Error("File should be updated for source with query params")
			}
		})
	}
}

// TestChaosInvalidVersionFormats tests various invalid version strings
func TestChaosInvalidVersionFormats(t *testing.T) {
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
		{"extremely long version", strings.Repeat("1.", 1000) + "0"},
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

			// Tool doesn't validate version format, it will just set whatever is given
			_, err = updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", tt.version, "", nil, false, false, false)

			// Document behavior - no validation means any string is accepted
			if err != nil {
				t.Logf("Version %q caused error: %v", tt.version, err)
			}
		})
	}
}

// TestChaosExtremeWhitespace tests files with unusual whitespace
func TestChaosExtremeWhitespace(t *testing.T) {
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

			updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", "", nil, false, false, false)
			if err != nil {
				t.Fatalf("Failed to process file with extreme whitespace: %v", err)
			}

			if !updated {
				t.Error("File with extreme whitespace should be updated")
			}
		})
	}
}

// TestChaosModuleDuplicateNames tests handling of duplicate module names
func TestChaosModuleDuplicateNames(t *testing.T) {
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

	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", "", nil, false, false, false)
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

// TestChaosVeryLargeIgnoreList tests behavior with many ignore patterns
func TestChaosVeryLargeIgnoreList(t *testing.T) {
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
	for i := 0; i < 10000; i++ {
		ignorePatterns = append(ignorePatterns, fmt.Sprintf("pattern-%d", i))
	}

	// Should still work, just slower
	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", "", ignorePatterns, false, false, false)
	if err != nil {
		t.Fatalf("Failed with large ignore list: %v", err)
	}

	if !updated {
		t.Error("File should be updated (vpc-prod doesn't match any pattern)")
	}
}

// TestChaosComplexPatternMatching tests pattern matching edge cases
func TestChaosComplexPatternMatching(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		pattern  string
		expected bool
	}{
		{"pattern with dots", "vpc.prod.v1", "vpc.*.v1", true},
		{"pattern with brackets", "vpc[0]", "vpc[*]", true},
		{"pattern with parens", "vpc(prod)", "vpc(*)", true},
		{"many wildcards in row", "test", "***test***", true},
		{"wildcard with empty parts", "test", "*test*", true},
		{"unicode wildcard", "测试-vpc-测试", "测试-*-测试", true},
		{"emoji wildcard", "🚀-prod-🚀", "🚀-*-🚀", true},
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

// TestChaosConfigWithEmptyModulesList tests config with empty modules list
func TestChaosConfigWithEmptyModulesList(t *testing.T) {
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

// TestChaosConfigWithNullValues tests YAML with null values
func TestChaosConfigWithNullValues(t *testing.T) {
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
	}
}

// TestChaosFileWithOnlyComments tests file containing only comments
func TestChaosFileWithOnlyComments(t *testing.T) {
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

	updated, err := updateModuleVersion(testFile, "test", "1.0.0", "", nil, false, false, false)
	if err != nil {
		t.Fatalf("Failed to process comments-only file: %v", err)
	}

	if updated {
		t.Error("Comments-only file should not be marked as updated")
	}
}

// TestChaosNestedQuotesInAttributes tests module attributes with nested quotes
func TestChaosNestedQuotesInAttributes(t *testing.T) {
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

	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", "", nil, false, false, false)
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

// TestChaosTrailingWhitespace tests files with trailing whitespace
func TestChaosTrailingWhitespace(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tf")

	// File with trailing spaces and tabs
	content := "module \"vpc\" {  \t  \n  source  = \"terraform-aws-modules/vpc/aws\"  \n  version = \"3.0.0\"  \t\n}  \t  "
	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", "", nil, false, false, false)
	if err != nil {
		t.Fatalf("Failed to process file with trailing whitespace: %v", err)
	}

	if !updated {
		t.Error("File with trailing whitespace should be updated")
	}
}

// TestChaosFromVersionWithSpecialChars tests from filter with special characters
func TestChaosFromVersionWithSpecialChars(t *testing.T) {
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
	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", "3.0.0-rc.1+build.123", nil, false, false, false)
	if err != nil {
		t.Fatalf("Failed to process: %v", err)
	}

	if !updated {
		t.Error("Should update when from version exactly matches")
	}
}
