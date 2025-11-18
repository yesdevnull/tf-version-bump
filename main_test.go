package main

import (
	"fmt"
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
			expectUpdate: false,
			expectError:  false,
			checkContent: func(content string) bool {
				// Should NOT add version attribute
				return !strings.Contains(content, `version`)
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

			// Run the update (default behavior: don't force-add)
			updated, err := updateModuleVersion(tmpFile, tt.moduleSource, tt.version, false)

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

	updated, err := updateModuleVersion(tmpFile, "terraform-aws-modules/vpc/aws", "5.0.0", false)
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
	updated, err := updateModuleVersion("/nonexistent/file.tf", "test", "1.0.0", false)

	if err == nil {
		t.Error("Expected error for non-existent file")
	}

	if updated {
		t.Error("Should not report file as updated when it doesn't exist")
	}
}

// TestUpdateModuleVersionEmptyFile tests handling of an empty Terraform file
func TestUpdateModuleVersionEmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "empty.tf")

	err := os.WriteFile(tmpFile, []byte(""), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	updated, err := updateModuleVersion(tmpFile, "terraform-aws-modules/vpc/aws", "5.0.0", false)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if updated {
		t.Error("Empty file should not be reported as updated")
	}
}

// TestUpdateModuleVersionMultipleVersionFormats tests different version formats
func TestUpdateModuleVersionMultipleVersionFormats(t *testing.T) {
	tests := []struct {
		name    string
		version string
	}{
		{"semantic version", "5.0.0"},
		{"version with v prefix", "v5.0.0"},
		{"version with patch", "5.0.1"},
		{"version with prerelease", "5.0.0-beta.1"},
		{"git tag", "v1.2.3"},
		{"commit hash", "abc123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "1.0.0"
}`

			tmpDir := t.TempDir()
			tmpFile := filepath.Join(tmpDir, "test.tf")

			err := os.WriteFile(tmpFile, []byte(input), 0644)
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}

			updated, err := updateModuleVersion(tmpFile, "terraform-aws-modules/vpc/aws", tt.version, false)
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

			expectedVersion := fmt.Sprintf(`version = "%s"`, tt.version)
			if !strings.Contains(string(content), expectedVersion) {
				t.Errorf("Expected version %q not found in content:\n%s", expectedVersion, string(content))
			}
		})
	}
}

// TestUpdateModuleVersionGitSource tests modules with Git sources
func TestUpdateModuleVersionGitSource(t *testing.T) {
	input := `module "example" {
  source  = "git::https://github.com/example/terraform-module.git"
  version = "v1.0.0"
}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.tf")

	err := os.WriteFile(tmpFile, []byte(input), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	updated, err := updateModuleVersion(tmpFile, "git::https://github.com/example/terraform-module.git", "v2.0.0", false)
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

	if !strings.Contains(string(content), `version = "v2.0.0"`) {
		t.Error("Git module version was not updated correctly")
	}
}

// TestUpdateModuleVersionNoModuleBlocks tests files without module blocks
func TestUpdateModuleVersionNoModuleBlocks(t *testing.T) {
	input := `resource "aws_instance" "example" {
  ami           = "ami-12345"
  instance_type = "t2.micro"
}

variable "region" {
  type    = string
  default = "us-east-1"
}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.tf")

	err := os.WriteFile(tmpFile, []byte(input), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	updated, err := updateModuleVersion(tmpFile, "terraform-aws-modules/vpc/aws", "5.0.0", false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if updated {
		t.Error("File without module blocks should not be reported as updated")
	}
}

// TestUpdateModuleVersionWithoutVersionWarning tests that warnings are printed for modules without version attributes
func TestUpdateModuleVersionWithoutVersionWarning(t *testing.T) {
	input := `module "local_module" {
  source = "./modules/my-module"
  name   = "test"
}

module "registry_module" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
  name    = "vpc"
}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.tf")

	err := os.WriteFile(tmpFile, []byte(input), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	// Try to update local module (which has no version)
	updated, err := updateModuleVersion(tmpFile, "./modules/my-module", "1.0.0", false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if updated {
		t.Error("Module without version should not be reported as updated")
	}

	// Verify file wasn't modified
	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	if strings.Contains(string(content), `version = "1.0.0"`) {
		t.Error("Version should not have been added to module without version attribute")
	}

	// Verify the registry module still has its version
	if !strings.Contains(string(content), `version = "3.0.0"`) {
		t.Error("Existing module version should be preserved")
	}
}

// TestUpdateModuleVersionMixedWithAndWithoutVersions tests updating when some modules have versions and some don't
func TestUpdateModuleVersionMixedWithAndWithoutVersions(t *testing.T) {
	input := `module "vpc1" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
  name    = "vpc1"
}

module "vpc2" {
  source = "terraform-aws-modules/vpc/aws"
  name   = "vpc2"
}

module "vpc3" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
  name    = "vpc3"
}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.tf")

	err := os.WriteFile(tmpFile, []byte(input), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	updated, err := updateModuleVersion(tmpFile, "terraform-aws-modules/vpc/aws", "5.0.0", false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if !updated {
		t.Error("Expected file to be updated")
	}

	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	contentStr := string(content)

	// vpc1 and vpc3 should be updated to 5.0.0
	versionCount := strings.Count(contentStr, `version = "5.0.0"`)
	if versionCount != 2 {
		t.Errorf("Expected 2 modules updated to 5.0.0, got %d", versionCount)
	}

	// vpc2 should not have a version attribute added
	// Count total version attributes - should be 2 (vpc1 and vpc3)
	totalVersions := strings.Count(contentStr, `version =`)
	if totalVersions != 2 {
		t.Errorf("Expected 2 total version attributes, got %d", totalVersions)
	}

	// Verify module names are preserved
	if !strings.Contains(contentStr, `name    = "vpc1"`) {
		t.Error("vpc1 name not preserved")
	}
	if !strings.Contains(contentStr, `name   = "vpc2"`) {
		t.Error("vpc2 name not preserved")
	}
	if !strings.Contains(contentStr, `name    = "vpc3"`) {
		t.Error("vpc3 name not preserved")
	}
}

// TestUpdateModuleVersionForceAdd tests the force-add flag behavior
func TestUpdateModuleVersionForceAdd(t *testing.T) {
	input := `module "local_module" {
  source = "./modules/my-module"
  name   = "test"
}

module "registry_module" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
  name    = "vpc"
}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.tf")

	err := os.WriteFile(tmpFile, []byte(input), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	// Try to update local module with force-add=true
	updated, err := updateModuleVersion(tmpFile, "./modules/my-module", "1.0.0", true)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if !updated {
		t.Error("Module should be reported as updated with force-add")
	}

	// Verify file was modified and version was added
	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	contentStr := string(content)

	// Local module should now have version attribute
	if !strings.Contains(contentStr, `version = "1.0.0"`) {
		t.Error("Version should have been added to module with force-add=true")
	}

	// Verify both modules are in the file
	if !strings.Contains(contentStr, `"./modules/my-module"`) {
		t.Error("Local module source should be preserved")
	}
	if !strings.Contains(contentStr, `"3.0.0"`) {
		t.Error("Registry module version should be preserved")
	}
	if !strings.Contains(contentStr, `module "registry_module"`) {
		t.Error("Registry module should be preserved")
	}
}

// TestUpdateModuleVersionComplexFile tests a file with multiple resource types
func TestUpdateModuleVersionComplexFile(t *testing.T) {
	input := `terraform {
  required_version = ">= 1.0"
}

variable "vpc_cidr" {
  type = string
}

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
  cidr    = var.vpc_cidr
}

resource "aws_instance" "app" {
  ami           = "ami-12345"
  instance_type = "t2.micro"
}

module "security_group" {
  source  = "terraform-aws-modules/security-group/aws"
  version = "4.0.0"
  vpc_id  = module.vpc.vpc_id
}

output "vpc_id" {
  value = module.vpc.vpc_id
}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.tf")

	err := os.WriteFile(tmpFile, []byte(input), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	// Update only VPC module
	updated, err := updateModuleVersion(tmpFile, "terraform-aws-modules/vpc/aws", "5.0.0", false)
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

	// VPC module should be updated
	if !strings.Contains(contentStr, `version = "5.0.0"`) {
		t.Error("VPC module was not updated")
	}

	// Security group module should remain unchanged
	if !strings.Contains(contentStr, `version = "4.0.0"`) {
		t.Error("Security group module should not have been changed")
	}

	// Other blocks should be preserved
	if !strings.Contains(contentStr, `terraform {`) {
		t.Error("Terraform block was not preserved")
	}
	if !strings.Contains(contentStr, `variable "vpc_cidr"`) {
		t.Error("Variable block was not preserved")
	}
	if !strings.Contains(contentStr, `resource "aws_instance" "app"`) {
		t.Error("Resource block was not preserved")
	}
	if !strings.Contains(contentStr, `output "vpc_id"`) {
		t.Error("Output block was not preserved")
	}
}
