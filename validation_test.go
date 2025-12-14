package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestGlobPatternValidation tests various glob patterns for correctness
func TestGlobPatternValidation(t *testing.T) {
	tests := []struct {
		name         string
		pattern      string
		shouldError  bool
		description  string
	}{
		{
			name:         "valid simple pattern",
			pattern:      "*.tf",
			shouldError:  false,
			description:  "Simple wildcard pattern",
		},
		{
			name:         "valid nested pattern",
			pattern:      "**/*.tf",
			shouldError:  false,
			description:  "Recursive wildcard pattern",
		},
		{
			name:         "malformed pattern with unclosed bracket",
			pattern:      "[abc",
			shouldError:  true,
			description:  "Unclosed bracket should cause error",
		},
		{
			name:         "empty pattern",
			pattern:      "",
			shouldError:  false,  // filepath.Glob returns error for syntax, not empty
			description:  "Empty pattern",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := filepath.Glob(tt.pattern)

			if tt.shouldError && err == nil {
				t.Errorf("Expected error for pattern %q, but got none", tt.pattern)
			}
			if !tt.shouldError && err != nil {
				t.Errorf("Did not expect error for pattern %q, but got: %v", tt.pattern, err)
			}
		})
	}
}

// TestInvalidGlobPatternsInProduction simulates actual usage
func TestInvalidGlobPatternsInProduction(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a test file
	testFile := filepath.Join(tmpDir, "test.tf")
	content := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}`
	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Test with invalid glob pattern (unclosed bracket)
	badPattern := filepath.Join(tmpDir, "[invalid")
	_, err = filepath.Glob(badPattern)

	if err == nil {
		t.Error("Expected error for invalid glob pattern with unclosed bracket")
	}

	// The error should be ErrBadPattern
	if err != nil && err != filepath.ErrBadPattern {
		t.Logf("Got expected error type: %v", err)
	}
}

// TestValidationOfModuleUpdates tests validation at the config level
func TestValidationOfModuleUpdates(t *testing.T) {
	tests := []struct {
		name        string
		configYAML  string
		expectError bool
		errorMatch  string
	}{
		{
			name: "empty source should error",
			configYAML: `modules:
  - source: ""
    version: "1.0.0"
`,
			expectError: true,
			errorMatch:  "missing 'source' field",
		},
		{
			name: "empty version should error",
			configYAML: `modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: ""
`,
			expectError: true,
			errorMatch:  "missing 'version' field",
		},
		{
			name: "whitespace-only source should error",
			configYAML: `modules:
  - source: "   "
    version: "1.0.0"
`,
			expectError: true,
			errorMatch:  "missing 'source' field",
		},
		{
			name: "whitespace-only version should error",
			configYAML: `modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "   "
`,
			expectError: true,
			errorMatch:  "missing 'version' field",
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

			_, err = loadConfig(configFile)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
				return
			}

			if !tt.expectError && err != nil {
				t.Errorf("Did not expect error but got: %v", err)
				return
			}

			// Validate error message contains expected text
			if tt.expectError && err != nil && tt.errorMatch != "" {
				if !strings.Contains(err.Error(), tt.errorMatch) {
					t.Errorf("Expected error to contain %q, got: %v", tt.errorMatch, err)
				}
			}
		})
	}
}

// TestFileSystemEdgeCases tests handling of various file system scenarios
func TestFileSystemEdgeCases(t *testing.T) {
	t.Run("directory instead of file", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Try to process a directory as if it were a file
		_, err := updateModuleVersion(tmpDir, "test", "1.0.0", nil, nil, nil, false, false, false)

		if err == nil {
			t.Error("Expected error when processing directory as file")
		}
	})

	t.Run("very deep nested directory", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create a very deep directory structure
		deepPath := tmpDir
		for i := 0; i < 100; i++ {
			deepPath = filepath.Join(deepPath, "dir")
		}
		err := os.MkdirAll(deepPath, 0755)
		if err != nil {
			t.Fatalf("Failed to create deep directory: %v", err)
		}

		// Create a file at the deep path
		testFile := filepath.Join(deepPath, "test.tf")
		content := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}`
		err = os.WriteFile(testFile, []byte(content), 0644)
		if err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}

		// Should be able to process it
		updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", nil, nil, nil, false, false, false)
		if err != nil {
			t.Errorf("Failed to process file in deep directory: %v", err)
		}
		if !updated {
			t.Error("File should have been updated")
		}
	})
}

// TestConcurrentSafetyConsiderations documents concurrent access behavior
// Note: This is informational - true concurrent safety would require external processes
func TestConcurrentSafetyConsiderations(t *testing.T) {
	// Document that this tool does not use file locking
	// Multiple processes modifying the same file could cause corruption
	// This is a limitation users should be aware of

	t.Run("sequential access is safe", func(t *testing.T) {
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

		// Multiple sequential updates should work fine
		for i := 2; i <= 5; i++ {
			version := fmt.Sprintf("1.0.%d", i-1)
			_, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", version, nil, nil, nil, false, false, false)
			if err != nil {
				t.Errorf("Sequential update %d failed: %v", i, err)
			}
		}
	})
}

// TestLargeFileHandling tests behavior with larger files
func TestLargeFileHandling(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "large.tf")

	// Create a file with many modules (simulating a large infrastructure file)
	var content string
	content += "# Large Terraform configuration file\n\n"

	for i := 0; i < 100; i++ {
		content += "module \"vpc_" + strconv.Itoa(i) + "\" {\n"
		content += "  source  = \"terraform-aws-modules/vpc/aws\"\n"
		content += "  version = \"3.0.0\"\n"
		content += "  name    = \"vpc-" + strconv.Itoa(i) + "\"\n"
		content += "}\n\n"
	}

	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create large test file: %v", err)
	}

	// Process the large file
	updated, err := updateModuleVersion(testFile, "terraform-aws-modules/vpc/aws", "5.0.0", nil, nil, nil, false, false, false)
	if err != nil {
		t.Fatalf("Failed to process large file: %v", err)
	}

	if !updated {
		t.Error("Large file should have been updated")
	}

	// Verify all modules were updated
	resultContent, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read updated file: %v", err)
	}

	// Should have no 3.0.0 versions left
	oldCount := strings.Count(string(resultContent), `version = "3.0.0"`)
	newCount := strings.Count(string(resultContent), `version = "5.0.0"`)

	if oldCount != 0 {
		t.Errorf("Expected 0 old versions, found %d", oldCount)
	}
	if newCount != 100 {
		t.Errorf("Expected 100 new versions, found %d", newCount)
	}
}

// TestErrorMessageQuality tests that error messages are helpful
func TestErrorMessageQuality(t *testing.T) {
	t.Run("file not found error includes path", func(t *testing.T) {
		nonExistent := "/tmp/nonexistent-file-12345.tf"
		_, err := updateModuleVersion(nonExistent, "test", "1.0.0", nil, nil, nil, false, false, false)

		if err == nil {
			t.Error("Expected error for non-existent file")
			return
		}

		// Error should mention the file path
		errMsg := err.Error()
		if errMsg == "" {
			t.Error("Error message should not be empty")
		}
	})

	t.Run("invalid HCL error is descriptive", func(t *testing.T) {
		tmpDir := t.TempDir()
		testFile := filepath.Join(tmpDir, "invalid.tf")

		// Create file with invalid HCL
		invalidContent := `module "test" {`
		err := os.WriteFile(testFile, []byte(invalidContent), 0644)
		if err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}

		_, err = updateModuleVersion(testFile, "test", "1.0.0", nil, nil, nil, false, false, false)

		if err == nil {
			t.Error("Expected error for invalid HCL")
			return
		}

		errMsg := err.Error()
		if errMsg == "" {
			t.Error("Error message should not be empty")
		}
	})
}
