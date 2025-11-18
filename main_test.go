package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrimQuotes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "double quotes",
			input:    `"terraform-aws-modules/vpc/aws"`,
			expected: "terraform-aws-modules/vpc/aws",
		},
		{
			name:     "single quotes",
			input:    "'terraform-aws-modules/vpc/aws'",
			expected: "terraform-aws-modules/vpc/aws",
		},
		{
			name:     "no quotes",
			input:    "terraform-aws-modules/vpc/aws",
			expected: "terraform-aws-modules/vpc/aws",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "single character",
			input:    "a",
			expected: "a",
		},
		{
			name:     "mismatched quotes",
			input:    `"test'`,
			expected: `"test'`,
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

func TestUpdateModuleVersion(t *testing.T) {
	tests := []struct {
		name         string
		inputContent string
		moduleSource string
		version      string
		expectUpdate bool
		expectError  bool
		checkContent func(string) bool
	}{
		{
			name: "update single module",
			inputContent: `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"

  name = "my-vpc"
}`,
			moduleSource: "terraform-aws-modules/vpc/aws",
			version:      "5.0.0",
			expectUpdate: true,
			expectError:  false,
			checkContent: func(content string) bool {
				return strings.Contains(content, `version = "5.0.0"`)
			},
		},
		{
			name: "update multiple modules with same source",
			inputContent: `module "vpc1" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"
}

module "vpc2" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"
}`,
			moduleSource: "terraform-aws-modules/vpc/aws",
			version:      "5.0.0",
			expectUpdate: true,
			expectError:  false,
			checkContent: func(content string) bool {
				// Both should be updated
				count := strings.Count(content, `version = "5.0.0"`)
				return count == 2
			},
		},
		{
			name: "no matching module",
			inputContent: `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"
}`,
			moduleSource: "terraform-aws-modules/s3-bucket/aws",
			version:      "4.0.0",
			expectUpdate: false,
			expectError:  false,
			checkContent: func(content string) bool {
				return strings.Contains(content, `version = "3.14.0"`)
			},
		},
		{
			name: "module without version attribute",
			inputContent: `module "local_module" {
  source = "./modules/my-module"

  name = "test"
}`,
			moduleSource: "./modules/my-module",
			version:      "1.0.0",
			expectUpdate: true,
			expectError:  false,
			checkContent: func(content string) bool {
				return strings.Contains(content, `version = "1.0.0"`)
			},
		},
		{
			name: "mixed modules - update only matching",
			inputContent: `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"
}

module "s3" {
  source  = "terraform-aws-modules/s3-bucket/aws"
  version = "3.0.0"
}`,
			moduleSource: "terraform-aws-modules/vpc/aws",
			version:      "5.0.0",
			expectUpdate: true,
			expectError:  false,
			checkContent: func(content string) bool {
				hasVpcUpdate := strings.Contains(content, `version = "5.0.0"`)
				hasS3Original := strings.Contains(content, `version = "3.0.0"`)
				return hasVpcUpdate && hasS3Original
			},
		},
		{
			name: "module with subpath in source",
			inputContent: `module "iam_user" {
  source  = "terraform-aws-modules/iam/aws//modules/iam-user"
  version = "5.1.0"
}`,
			moduleSource: "terraform-aws-modules/iam/aws//modules/iam-user",
			version:      "5.2.0",
			expectUpdate: true,
			expectError:  false,
			checkContent: func(content string) bool {
				return strings.Contains(content, `version = "5.2.0"`)
			},
		},
		{
			name:         "invalid HCL",
			inputContent: `module "vpc" {`,
			moduleSource: "terraform-aws-modules/vpc/aws",
			version:      "5.0.0",
			expectUpdate: false,
			expectError:  true,
			checkContent: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary file
			tmpDir := t.TempDir()
			tmpFile := filepath.Join(tmpDir, "test.tf")

			err := os.WriteFile(tmpFile, []byte(tt.inputContent), 0644)
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}

			// Run the update
			updated, err := updateModuleVersion(tmpFile, tt.moduleSource, tt.version)

			// Check error expectation
			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			// Check update expectation
			if updated != tt.expectUpdate {
				t.Errorf("Expected updated=%v, got %v", tt.expectUpdate, updated)
			}

			// Check file content if needed
			if tt.checkContent != nil {
				content, err := os.ReadFile(tmpFile)
				if err != nil {
					t.Fatalf("Failed to read updated file: %v", err)
				}

				if !tt.checkContent(string(content)) {
					t.Errorf("Content check failed. File contents:\n%s", string(content))
				}
			}
		})
	}
}

func TestUpdateModuleVersionPreservesFormatting(t *testing.T) {
	input := `# This is a comment
module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"

  # Another comment
  name = "my-vpc"
  cidr = "10.0.0.0/16"
}

resource "aws_instance" "example" {
  ami           = "ami-12345"
  instance_type = "t2.micro"
}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.tf")

	err := os.WriteFile(tmpFile, []byte(input), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	updated, err := updateModuleVersion(tmpFile, "terraform-aws-modules/vpc/aws", "5.0.0")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if !updated {
		t.Error("Expected file to be updated")
	}

	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read updated file: %v", err)
	}

	contentStr := string(content)

	// Check that comments are preserved
	if !strings.Contains(contentStr, "# This is a comment") {
		t.Error("Top comment was not preserved")
	}
	if !strings.Contains(contentStr, "# Another comment") {
		t.Error("Inline comment was not preserved")
	}

	// Check that resource block is preserved
	if !strings.Contains(contentStr, "resource \"aws_instance\" \"example\"") {
		t.Error("Resource block was not preserved")
	}

	// Check that version was updated
	if !strings.Contains(contentStr, `version = "5.0.0"`) {
		t.Error("Version was not updated correctly")
	}

	// Check that old version is gone
	if strings.Contains(contentStr, `version = "3.14.0"`) {
		t.Error("Old version still present")
	}
}

func TestUpdateModuleVersionFileNotFound(t *testing.T) {
	updated, err := updateModuleVersion("/nonexistent/file.tf", "test", "1.0.0")

	if err == nil {
		t.Error("Expected error for non-existent file")
	}

	if updated {
		t.Error("Should not report file as updated when it doesn't exist")
	}
}
