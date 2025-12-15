package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUnicodeModuleNames tests handling of Unicode characters in module names
func TestUnicodeModuleNames(t *testing.T) {
	tests := []struct {
		name         string
		inputContent string
		moduleSource string
		version      string
		expectUpdate bool
	}{
		{
			name: "module name with unicode characters",
			inputContent: `module "vpc-主要" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}`,
			moduleSource: "terraform-aws-modules/vpc/aws",
			version:      "5.0.0",
			expectUpdate: true,
		},
		{
			name: "module name with emojis",
			inputContent: `module "vpc-🚀-prod" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}`,
			moduleSource: "terraform-aws-modules/vpc/aws",
			version:      "5.0.0",
			expectUpdate: true,
		},
		{
			name: "module source with unicode (though unusual)",
			inputContent: `module "test" {
  source  = "registry.example.com/组织/module/aws"
  version = "1.0.0"
}`,
			moduleSource: "registry.example.com/组织/module/aws",
			version:      "2.0.0",
			expectUpdate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tmpFile := filepath.Join(tmpDir, "test.tf")

			err := os.WriteFile(tmpFile, []byte(tt.inputContent), 0644)
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}

			updated, err := updateModuleVersion(tmpFile, tt.moduleSource, tt.version, nil, nil, nil, false, false, false, "text")
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if updated != tt.expectUpdate {
				t.Errorf("Expected updated=%v, got %v", tt.expectUpdate, updated)
			}

			if tt.expectUpdate {
				content, err := os.ReadFile(tmpFile)
				if err != nil {
					t.Fatalf("Failed to read updated file: %v", err)
				}

				expectedVersion := `version = "` + tt.version + `"`
				if !strings.Contains(string(content), expectedVersion) {
					t.Errorf("Expected version %q not found in content", expectedVersion)
				}
			}
		})
	}
}

// TestPatternMatchingWithUnicode tests wildcard matching with Unicode characters
func TestPatternMatchingWithUnicode(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		pattern  string
		expected bool
	}{
		{
			name:     "unicode exact match",
			input:    "vpc-主要",
			pattern:  "vpc-主要",
			expected: true,
		},
		{
			name:     "unicode with wildcard prefix",
			input:    "測試-vpc",
			pattern:  "測試-*",
			expected: true,
		},
		{
			name:     "unicode with wildcard suffix",
			input:    "vpc-テスト",
			pattern:  "*-テスト",
			expected: true,
		},
		{
			name:     "emoji in pattern",
			input:    "vpc-🚀-prod",
			pattern:  "vpc-🚀-*",
			expected: true,
		},
		{
			name:     "mixed unicode and ascii",
			input:    "prod-主要-vpc-test",
			pattern:  "*-主要-*",
			expected: true,
		},
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

// TestIgnorePatternWithUnicode tests ignore patterns with Unicode characters
func TestIgnorePatternWithUnicode(t *testing.T) {
	inputContent := `module "vpc-主要" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}

module "vpc-prod" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.tf")

	err := os.WriteFile(tmpFile, []byte(inputContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	// Should ignore the Unicode-named module
	ignorePatterns := []string{"vpc-主要"}
	updated, err := updateModuleVersion(tmpFile, "terraform-aws-modules/vpc/aws", "5.0.0", nil, nil, ignorePatterns, false, false, false, "text")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if !updated {
		t.Error("Expected file to be updated (vpc-prod should be updated)")
	}

	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read updated file: %v", err)
	}

	// Count version occurrences - should be 1 old, 1 new
	old := strings.Count(string(content), `version = "3.0.0"`)
	new := strings.Count(string(content), `version = "5.0.0"`)

	if old != 1 || new != 1 {
		t.Errorf("Expected 1 old version and 1 new version, got %d old and %d new", old, new)
	}
}

// TestVeryLongModuleName tests handling of extremely long module names
func TestVeryLongModuleName(t *testing.T) {
	longName := strings.Repeat("very-long-module-name-", 100) // ~2200 characters
	inputContent := `module "` + longName + `" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.tf")

	err := os.WriteFile(tmpFile, []byte(inputContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	updated, err := updateModuleVersion(tmpFile, "terraform-aws-modules/vpc/aws", "5.0.0", nil, nil, nil, false, false, false, "text")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if !updated {
		t.Error("Expected file to be updated")
	}
}

// TestVeryLongPattern tests pattern matching with extremely long strings
func TestVeryLongPattern(t *testing.T) {
	// Test with very long prefix
	longPrefix := strings.Repeat("prefix-", 1000)
	input := longPrefix + "suffix"
	pattern := longPrefix + "*"

	result := matchPattern(input, pattern)
	if !result {
		t.Error("Expected match for very long prefix pattern")
	}

	// Test with very long middle part
	middle := strings.Repeat("middle-", 1000)
	input = "start-" + middle + "-end"
	pattern = "start-*-end"

	result = matchPattern(input, pattern)
	if !result {
		t.Error("Expected match for pattern with very long middle section")
	}
}

// TestSpecialCharactersInModuleName tests various special characters
func TestSpecialCharactersInModuleName(t *testing.T) {
	tests := []struct {
		name       string
		moduleName string
	}{
		{"dots in name", "vpc.prod.v1"},
		{"multiple dashes", "vpc---prod"},
		{"underscores and dashes", "vpc_prod-v1_test"},
		{"brackets", "vpc[0]"},
		{"parentheses", "vpc(prod)"},
		{"plus signs", "vpc+prod+v1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputContent := `module "` + tt.moduleName + `" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}`

			tmpDir := t.TempDir()
			tmpFile := filepath.Join(tmpDir, "test.tf")

			err := os.WriteFile(tmpFile, []byte(inputContent), 0644)
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}

			updated, err := updateModuleVersion(tmpFile, "terraform-aws-modules/vpc/aws", "5.0.0", nil, nil, nil, false, false, false, "text")
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if !updated {
				t.Error("Expected file to be updated")
			}
		})
	}
}

// TestFilePermissionPreservation tests that file permissions are preserved
func TestFilePermissionPreservation(t *testing.T) {
	inputContent := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.tf")

	// Create file with specific permissions (read-only for owner, others have no access)
	err := os.WriteFile(tmpFile, []byte(inputContent), 0600)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	// Get original permissions
	originalInfo, err := os.Stat(tmpFile)
	if err != nil {
		t.Fatalf("Failed to stat file: %v", err)
	}
	originalMode := originalInfo.Mode()

	// Update the file
	_, err = updateModuleVersion(tmpFile, "terraform-aws-modules/vpc/aws", "5.0.0", nil, nil, nil, false, false, false, "text")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Check permissions are preserved
	newInfo, err := os.Stat(tmpFile)
	if err != nil {
		t.Fatalf("Failed to stat updated file: %v", err)
	}
	newMode := newInfo.Mode()

	if originalMode.Perm() != newMode.Perm() {
		t.Errorf("File permissions changed: original=%v, new=%v", originalMode.Perm(), newMode.Perm())
	}
}

// TestEmptyModuleName tests behavior with empty module name (malformed HCL)
func TestEmptyModuleName(t *testing.T) {
	// This tests the defensive check in shouldIgnoreModule
	result := shouldIgnoreModule("", []string{"*"})
	if result {
		t.Error("Empty module name should not be ignored (defensive check)")
	}

	result = shouldIgnoreModule("", []string{"test"})
	if result {
		t.Error("Empty module name should not match any pattern")
	}
}

// TestPatternMatchingEdgeCases tests additional edge cases in pattern matching
func TestPatternMatchingEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		pattern  string
		expected bool
	}{
		// Tab and newline characters
		{"pattern with actual tab", "test\tvalue", "test\t*", true},
		{"pattern with space vs tab", "test value", "test\tvalue", false},

		// Multiple consecutive same characters
		{"multiple same chars with wildcard", "aaabbbccc", "a*c", true},
		{"pattern is subset", "abcd", "abc", false},
		{"input is subset", "abc", "abcd", false},

		// Wildcard matching zero characters at various positions
		{"wildcard zero chars at start", "test", "*test", true},
		{"wildcard zero chars at end", "test", "test*", true},
		{"wildcard zero chars in middle", "testvalue", "test*value", true},

		// Complex nesting scenarios
		{"nested pattern parts", "a-b-c-d-e-f", "a-*-c-*-e-*", true},
		{"many wildcards", "abcdef", "a*b*c*d*e*f", true},
		{"pattern longer than input", "ab", "a*b*c", false},
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

// TestConfigLoadingEdgeCases tests edge cases in YAML config loading
func TestConfigLoadingEdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		configYAML  string
		expectError bool
		validate    func(*testing.T, []ModuleUpdate)
	}{
		{
			name: "config with unicode in source",
			configYAML: `modules:
  - source: "registry.example.com/組織/module/aws"
    version: "1.0.0"
`,
			expectError: false,
			validate: func(t *testing.T, updates []ModuleUpdate) {
				if updates[0].Source != "registry.example.com/組織/module/aws" {
					t.Errorf("Unicode source not preserved: %s", updates[0].Source)
				}
			},
		},
		{
			name: "config with very long source",
			configYAML: `modules:
  - source: "` + strings.Repeat("very-long-path/", 100) + `module"
    version: "1.0.0"
`,
			expectError: false,
			validate: func(t *testing.T, updates []ModuleUpdate) {
				if len(updates[0].Source) < 1000 {
					t.Error("Long source was truncated")
				}
			},
		},
		{
			name: "config with special characters in ignore patterns",
			configYAML: `modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
    ignore:
      - "vpc-主要"
      - "test-🚀-*"
      - "vpc[prod]"
`,
			expectError: false,
			validate: func(t *testing.T, updates []ModuleUpdate) {
				if len(updates[0].Ignore) != 3 {
					t.Errorf("Expected 3 ignore patterns, got %d", len(updates[0].Ignore))
				}
				if updates[0].Ignore[0] != "vpc-主要" {
					t.Errorf("Unicode ignore pattern not preserved")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configFile := filepath.Join(tmpDir, "config.yml")

			err := os.WriteFile(configFile, []byte(tt.configYAML), 0644)
			if err != nil {
				t.Fatalf("Failed to create config file: %v", err)
			}

			updates, err := loadConfig(configFile)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if tt.validate != nil {
				tt.validate(t, updates)
			}
		})
	}
}

// TestTrimQuotesWithEscapedQuotes tests handling of escaped quotes (edge case)
func TestTrimQuotesWithEscapedQuotes(t *testing.T) {
	// Note: In practice, HCL tokens won't have escaped quotes in this context,
	// but we test the function's behavior anyway
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "escaped quote at start",
			input:    `\"test"`,
			expected: `\"test"`, // No quotes to trim
		},
		{
			name:     "escaped quote at end",
			input:    `"test\"`,
			expected: `test\`, // Outer quotes are trimmed, even though there's an escape
		},
		{
			name:     "properly quoted with internal escaped quotes",
			input:    `"test \"quoted\" value"`,
			expected: `test \"quoted\" value`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := trimQuotes(tt.input)
			if result != tt.expected {
				t.Errorf("trimQuotes(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestMultipleFilesSimultaneously tests that the tool can handle multiple files
// This is a basic test - true concurrent access would require external processes
func TestMultipleFilesSimultaneously(t *testing.T) {
	tmpDir := t.TempDir()

	// Create multiple test files
	for i := 0; i < 10; i++ {
		content := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}`
		filename := filepath.Join(tmpDir, fmt.Sprintf("test%d.tf", i))
		err := os.WriteFile(filename, []byte(content), 0644)
		if err != nil {
			t.Fatalf("Failed to create file %s: %v", filename, err)
		}
	}

	// Process all files
	files, err := filepath.Glob(filepath.Join(tmpDir, "*.tf"))
	if err != nil {
		t.Fatalf("Failed to glob files: %v", err)
	}

	if len(files) != 10 {
		t.Fatalf("Expected 10 files, got %d", len(files))
	}

	// Update all files
	for _, file := range files {
		updated, err := updateModuleVersion(file, "terraform-aws-modules/vpc/aws", "5.0.0", nil, nil, nil, false, false, false, "text")
		if err != nil {
			t.Errorf("Failed to update %s: %v", file, err)
		}
		if !updated {
			t.Errorf("File %s was not updated", file)
		}
	}

	// Verify all files were updated
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Errorf("Failed to read %s: %v", file, err)
			continue
		}
		if !strings.Contains(string(content), `version = "5.0.0"`) {
			t.Errorf("File %s was not updated correctly", file)
		}
	}
}

// TestWindowsPathSeparators tests handling of Windows-style paths (if applicable)
func TestWindowsPathSeparators(t *testing.T) {
	// Test that backslashes in local module paths are correctly identified
	// Note: On Windows, Go's filepath handles this automatically
	tests := []struct {
		name     string
		source   string
		expected bool
	}{
		{
			name:     "forward slash local",
			source:   "./modules/vpc",
			expected: true,
		},
		{
			name:     "parent with forward slash",
			source:   "../shared/modules",
			expected: true,
		},
		// Note: Backslashes are less common in Terraform module sources
		// as Terraform uses forward slashes even on Windows
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isLocalModule(tt.source)
			if result != tt.expected {
				t.Errorf("isLocalModule(%q) = %v, want %v", tt.source, result, tt.expected)
			}
		})
	}
}
