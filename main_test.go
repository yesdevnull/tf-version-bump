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

			// Run the update (default behavior: don't force-add, no from version filter)
			updated, err := updateModuleVersion(tmpFile, tt.moduleSource, tt.version, "", nil, false, false)

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

	updated, err := updateModuleVersion(tmpFile, "terraform-aws-modules/vpc/aws", "5.0.0", "", nil, false, false)
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
	updated, err := updateModuleVersion("/nonexistent/file.tf", "test", "1.0.0", "", nil, false, false)

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

	updated, err := updateModuleVersion(tmpFile, "terraform-aws-modules/vpc/aws", "5.0.0", "", nil, false, false)

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

			updated, err := updateModuleVersion(tmpFile, "terraform-aws-modules/vpc/aws", tt.version, "", nil, false, false)
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

	updated, err := updateModuleVersion(tmpFile, "git::https://github.com/example/terraform-module.git", "v2.0.0", "", nil, false, false)
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

	updated, err := updateModuleVersion(tmpFile, "terraform-aws-modules/vpc/aws", "5.0.0", "", nil, false, false)
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
	updated, err := updateModuleVersion(tmpFile, "./modules/my-module", "1.0.0", "", nil, false, false)
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

	updated, err := updateModuleVersion(tmpFile, "terraform-aws-modules/vpc/aws", "5.0.0", "", nil, false, false)
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

	// Try to update local module with force-add=true (should be skipped)
	updated, err := updateModuleVersion(tmpFile, "./modules/my-module", "1.0.0", "", nil, true, false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if updated {
		t.Error("Local module should not be updated even with force-add")
	}

	// Verify file was not modified
	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	contentStr := string(content)

	// Local module should NOT have version attribute added
	if strings.Contains(contentStr, `version = "1.0.0"`) {
		t.Error("Version should not have been added to local module")
	}

	// Verify local module is still in the file unchanged
	if !strings.Contains(contentStr, `"./modules/my-module"`) {
		t.Error("Local module source should be preserved")
	}

	// Test force-add with registry module without version
	input2 := `module "s3" {
  source = "terraform-aws-modules/s3-bucket/aws"
  name   = "bucket"
}`

	tmpFile2 := filepath.Join(tmpDir, "test2.tf")
	err = os.WriteFile(tmpFile2, []byte(input2), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	updated, err = updateModuleVersion(tmpFile2, "terraform-aws-modules/s3-bucket/aws", "4.0.0", "", nil, true, false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if !updated {
		t.Error("Registry module should be updated with force-add")
	}

	content, err = os.ReadFile(tmpFile2)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	if !strings.Contains(string(content), `version = "4.0.0"`) {
		t.Error("Version should have been added to registry module with force-add=true")
	}
}

// TestUpdateModuleVersionLocalModulesSkipped tests that local modules are always skipped
func TestUpdateModuleVersionLocalModulesSkipped(t *testing.T) {
	tests := []struct {
		name         string
		inputContent string
		moduleSource string
		version      string
		forceAdd     bool
	}{
		{
			name: "local module with relative path ./ and version",
			inputContent: `module "local" {
  source  = "./modules/vpc"
  version = "1.0.0"
}`,
			moduleSource: "./modules/vpc",
			version:      "2.0.0",
			forceAdd:     false,
		},
		{
			name: "local module with parent path ../",
			inputContent: `module "shared" {
  source  = "../shared-modules/s3"
  version = "1.0.0"
}`,
			moduleSource: "../shared-modules/s3",
			version:      "2.0.0",
			forceAdd:     false,
		},
		{
			name: "local module without version and force-add",
			inputContent: `module "local" {
  source = "./modules/vpc"
  name   = "test"
}`,
			moduleSource: "./modules/vpc",
			version:      "1.0.0",
			forceAdd:     true,
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

			// Try to update local module
			updated, err := updateModuleVersion(tmpFile, tt.moduleSource, tt.version, "", nil, tt.forceAdd, false)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if updated {
				t.Error("Local module should never be updated")
			}

			// Verify file content is unchanged
			content, err := os.ReadFile(tmpFile)
			if err != nil {
				t.Fatalf("Failed to read file: %v", err)
			}

			contentStr := string(content)
			if contentStr != tt.inputContent {
				t.Errorf("File content should not have changed. Got:\n%s\nWant:\n%s", contentStr, tt.inputContent)
			}
		})
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
	updated, err := updateModuleVersion(tmpFile, "terraform-aws-modules/vpc/aws", "5.0.0", "", nil, false, false)
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

func TestUpdateModuleVersionWithFromFilter(t *testing.T) {
	tests := []struct {
		name         string
		inputContent string
		moduleSource string
		version      string
		fromVersion  string
		expectUpdate bool
		checkContent func(string) bool
	}{
		{
			name: "update module matching from version",
			inputContent: `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"

  name = "my-vpc"
}`,
			moduleSource: "terraform-aws-modules/vpc/aws",
			version:      "5.0.0",
			fromVersion:  "3.14.0",
			expectUpdate: true,
			checkContent: func(content string) bool {
				return strings.Contains(content, `version = "5.0.0"`)
			},
		},
		{
			name: "skip module not matching from version",
			inputContent: `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "4.0.0"

  name = "my-vpc"
}`,
			moduleSource: "terraform-aws-modules/vpc/aws",
			version:      "5.0.0",
			fromVersion:  "3.14.0",
			expectUpdate: false,
			checkContent: func(content string) bool {
				// Should keep original version
				return strings.Contains(content, `version = "4.0.0"`)
			},
		},
		{
			name: "update only modules matching from version in multi-module file",
			inputContent: `module "vpc1" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"
}

module "vpc2" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "4.0.0"
}

module "vpc3" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"
}`,
			moduleSource: "terraform-aws-modules/vpc/aws",
			version:      "5.0.0",
			fromVersion:  "3.14.0",
			expectUpdate: true,
			checkContent: func(content string) bool {
				// Should have 2 modules at 5.0.0 and 1 at 4.0.0
				count5 := strings.Count(content, `version = "5.0.0"`)
				count4 := strings.Count(content, `version = "4.0.0"`)
				return count5 == 2 && count4 == 1
			},
		},
		{
			name: "empty from version updates all matching modules",
			inputContent: `module "vpc1" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"
}

module "vpc2" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "4.0.0"
}`,
			moduleSource: "terraform-aws-modules/vpc/aws",
			version:      "5.0.0",
			fromVersion:  "",
			expectUpdate: true,
			checkContent: func(content string) bool {
				// Both should be updated to 5.0.0
				count := strings.Count(content, `version = "5.0.0"`)
				return count == 2
			},
		},
		{
			name: "from version with different module sources",
			inputContent: `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"
}

module "s3" {
  source  = "terraform-aws-modules/s3-bucket/aws"
  version = "3.14.0"
}`,
			moduleSource: "terraform-aws-modules/vpc/aws",
			version:      "5.0.0",
			fromVersion:  "3.14.0",
			expectUpdate: true,
			checkContent: func(content string) bool {
				// Only VPC should be updated, S3 should remain at 3.14.0
				hasVpcUpdate := strings.Contains(content, `version = "5.0.0"`)
				countOldVersion := strings.Count(content, `version = "3.14.0"`)
				return hasVpcUpdate && countOldVersion == 1
			},
		},
		{
			name: "from version filter with semantic versioning variations",
			inputContent: `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"
}`,
			moduleSource: "terraform-aws-modules/vpc/aws",
			version:      "5.0.0",
			fromVersion:  "3.14",
			expectUpdate: false,
			checkContent: func(content string) bool {
				// Should NOT update because "3.14.0" != "3.14"
				return strings.Contains(content, `version = "3.14.0"`)
			},
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

			// Run the update with from version filter
			updated, err := updateModuleVersion(tmpFile, tt.moduleSource, tt.version, tt.fromVersion, nil, false, false)

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			// Check update expectation
			if updated != tt.expectUpdate {
				t.Errorf("Expected updated=%v, got %v", tt.expectUpdate, updated)
			}

			// Check file content
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

func TestUpdateModuleVersionDryRun(t *testing.T) {
	inputContent := `
module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"

  name = "my-vpc"
}
`

	// Create temporary file
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.tf")

	err := os.WriteFile(tmpFile, []byte(inputContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	// Run the update in dry-run mode
	updated, err := updateModuleVersion(tmpFile, "terraform-aws-modules/vpc/aws", "5.0.0", "", nil, false, true)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should report that it would update
	if !updated {
		t.Error("Expected updated=true in dry-run mode, got false")
	}

	// File should NOT be modified
	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	if !strings.Contains(string(content), `version = "3.0.0"`) {
		t.Error("File was modified in dry-run mode, but should not have been")
	}

	if strings.Contains(string(content), `version = "5.0.0"`) {
		t.Error("File contains new version in dry-run mode, but should not")
	}
}

// TestIsLocalModule tests the isLocalModule function with various path formats
func TestIsLocalModule(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		expected bool
	}{
		{
			name:     "relative path with ./",
			source:   "./modules/vpc",
			expected: true,
		},
		{
			name:     "relative path with ../",
			source:   "../shared/modules",
			expected: true,
		},
		{
			name:     "absolute path",
			source:   "/absolute/path/to/module",
			expected: true,
		},
		{
			name:     "registry module",
			source:   "terraform-aws-modules/vpc/aws",
			expected: false,
		},
		{
			name:     "git source",
			source:   "git::https://github.com/example/module.git",
			expected: false,
		},
		{
			name:     "empty string",
			source:   "",
			expected: false,
		},
		{
			name:     "just a dot",
			source:   ".",
			expected: false,
		},
		{
			name:     "module name starting with dot but not path",
			source:   ".module-name",
			expected: false,
		},
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

// TestUpdateModuleVersionModuleWithoutSource tests handling of modules without source attribute
func TestUpdateModuleVersionModuleWithoutSource(t *testing.T) {
	input := `module "broken" {
  version = "1.0.0"
  name    = "test"
}

module "valid" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.tf")

	err := os.WriteFile(tmpFile, []byte(input), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	// Try to update - should skip the module without source and update the valid one
	updated, err := updateModuleVersion(tmpFile, "terraform-aws-modules/vpc/aws", "5.0.0", "", nil, false, false)
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
	if !strings.Contains(contentStr, `version = "5.0.0"`) {
		t.Error("Valid module was not updated")
	}

	// The broken module should still be present
	if !strings.Contains(contentStr, `module "broken"`) {
		t.Error("Module without source should still be in file")
	}
}

// TestUpdateModuleVersionSpecialCharactersInSource tests modules with special characters
func TestUpdateModuleVersionSpecialCharactersInSource(t *testing.T) {
	tests := []struct {
		name         string
		moduleSource string
		version      string
	}{
		{
			name:         "module with underscores",
			moduleSource: "terraform-aws-modules/vpc_example/aws",
			version:      "1.0.0",
		},
		{
			name:         "module with hyphens",
			moduleSource: "terraform-aws-modules/vpc-example-test/aws",
			version:      "2.0.0",
		},
		{
			name:         "module with numbers",
			moduleSource: "terraform-aws-modules/s3-bucket-v2/aws",
			version:      "3.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := fmt.Sprintf(`module "test" {
  source  = "%s"
  version = "0.1.0"
}`, tt.moduleSource)

			tmpDir := t.TempDir()
			tmpFile := filepath.Join(tmpDir, "test.tf")

			err := os.WriteFile(tmpFile, []byte(input), 0644)
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}

			updated, err := updateModuleVersion(tmpFile, tt.moduleSource, tt.version, "", nil, false, false)
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

			expectedVersion := fmt.Sprintf(`version = "%s"`, tt.version)
			if !strings.Contains(string(content), expectedVersion) {
				t.Errorf("Expected version %q not found in content", expectedVersion)
			}
		})
	}
}

// TestUpdateModuleVersionLongVersionString tests handling of very long version strings
func TestUpdateModuleVersionLongVersionString(t *testing.T) {
	input := `module "test" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "1.0.0"
}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.tf")

	err := os.WriteFile(tmpFile, []byte(input), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	// Very long version string (e.g., commit SHA or complex prerelease version)
	longVersion := "5.0.0-alpha.1.2.3.4.5.6.7.8.9.10+build.metadata.with.lots.of.segments.2024.01.15.abc123def456"

	updated, err := updateModuleVersion(tmpFile, "terraform-aws-modules/vpc/aws", longVersion, "", nil, false, false)
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

	expectedVersion := fmt.Sprintf(`version = "%s"`, longVersion)
	if !strings.Contains(string(content), expectedVersion) {
		t.Error("Long version string was not set correctly")
	}
}

// TestUpdateModuleVersionAbsolutePathLocalModule tests local modules with absolute paths
func TestUpdateModuleVersionAbsolutePathLocalModule(t *testing.T) {
	input := `module "local" {
  source  = "/absolute/path/to/module"
  version = "1.0.0"
}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.tf")

	err := os.WriteFile(tmpFile, []byte(input), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	// Should be skipped as it's a local module (absolute path)
	updated, err := updateModuleVersion(tmpFile, "/absolute/path/to/module", "2.0.0", "", nil, false, false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if updated {
		t.Error("Local module with absolute path should not be updated")
	}

	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	// Original version should remain
	if !strings.Contains(string(content), `version = "1.0.0"`) {
		t.Error("Local module version should not have been changed")
	}
}

// TestUpdateModuleVersionModuleWithNoLabels tests module blocks without labels
func TestUpdateModuleVersionModuleWithNoLabels(t *testing.T) {
	// This is technically invalid HCL but we should handle it gracefully
	input := `module {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.tf")

	err := os.WriteFile(tmpFile, []byte(input), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	// Should still update even without labels (though this is unusual)
	updated, err := updateModuleVersion(tmpFile, "terraform-aws-modules/vpc/aws", "5.0.0", "", nil, false, false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if !updated {
		t.Error("Module without labels should still be updated")
	}

	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	if !strings.Contains(string(content), `version = "5.0.0"`) {
		t.Error("Module without labels was not updated")
	}
}

// TestUpdateModuleVersionWhitespaceVariations tests modules with various whitespace
func TestUpdateModuleVersionWhitespaceVariations(t *testing.T) {
	tests := []struct {
		name         string
		inputContent string
		expectUpdate bool
	}{
		{
			name: "extra spaces in source",
			inputContent: `module "test" {
  source  =    "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}`,
			expectUpdate: true,
		},
		{
			name: "tabs in source",
			inputContent: `module "test" {
  source	=	"terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}`,
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

			updated, err := updateModuleVersion(tmpFile, "terraform-aws-modules/vpc/aws", "5.0.0", "", nil, false, false)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if updated != tt.expectUpdate {
				t.Errorf("Expected updated=%v, got %v", tt.expectUpdate, updated)
			}

			if tt.expectUpdate {
				content, err := os.ReadFile(tmpFile)
				if err != nil {
					t.Fatalf("Failed to read file: %v", err)
				}

				if !strings.Contains(string(content), `version = "5.0.0"`) {
					t.Error("Version was not updated correctly")
				}
			}
		})
	}
}

// TestUpdateModuleVersionRegistryModuleWithoutVersionNoForceAdd tests behavior when registry module lacks version
func TestUpdateModuleVersionRegistryModuleWithoutVersionNoForceAdd(t *testing.T) {
	input := `module "s3" {
  source = "terraform-aws-modules/s3-bucket/aws"
  bucket = "my-bucket"
}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.tf")

	err := os.WriteFile(tmpFile, []byte(input), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	// With forceAdd=false, should skip and print warning
	updated, err := updateModuleVersion(tmpFile, "terraform-aws-modules/s3-bucket/aws", "4.0.0", "", nil, false, false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if updated {
		t.Error("Module without version should not be updated when forceAdd=false")
	}

	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	// Should NOT have version added
	if strings.Contains(string(content), "version") {
		t.Error("Version attribute should not have been added")
	}
}

// TestTrimQuotesEdgeCases tests additional edge cases for trimQuotes
func TestTrimQuotesEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "only opening double quote",
			input:    `"test`,
			expected: `"test`,
		},
		{
			name:     "only closing double quote",
			input:    `test"`,
			expected: `test"`,
		},
		{
			name:     "quoted string with internal quotes",
			input:    `"test "quoted" value"`,
			expected: `test "quoted" value`,
		},
		{
			name:     "single quoted string with internal quotes",
			input:    `'test 'quoted' value'`,
			expected: `test 'quoted' value`,
		},
		{
			name:     "two character string with quotes",
			input:    `""`,
			expected: ``,
		},
		{
			name:     "two character string with single quotes",
			input:    `''`,
			expected: ``,
		},
		{
			name:     "string with only double quote",
			input:    `"`,
			expected: `"`,
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

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		pattern  string
		expected bool
	}{
		{
			name:     "exact match",
			input:    "vpc",
			pattern:  "vpc",
			expected: true,
		},
		{
			name:     "no match",
			input:    "vpc",
			pattern:  "s3",
			expected: false,
		},
		{
			name:     "wildcard prefix",
			input:    "legacy-vpc",
			pattern:  "legacy-*",
			expected: true,
		},
		{
			name:     "wildcard suffix",
			input:    "vpc-test",
			pattern:  "*-test",
			expected: true,
		},
		{
			name:     "wildcard both sides",
			input:    "prod-vpc-test",
			pattern:  "*-vpc-*",
			expected: true,
		},
		{
			name:     "wildcard only",
			input:    "anything",
			pattern:  "*",
			expected: true,
		},
		{
			name:     "multiple wildcards",
			input:    "prod-vpc-test-1",
			pattern:  "prod-*-test-*",
			expected: true,
		},
		{
			name:     "wildcard prefix no match",
			input:    "vpc",
			pattern:  "legacy-*",
			expected: false,
		},
		{
			name:     "wildcard suffix no match",
			input:    "vpc",
			pattern:  "*-test",
			expected: false,
		},
		{
			name:     "empty string with wildcard",
			input:    "",
			pattern:  "*",
			expected: true,
		},
		{
			name:     "empty string no wildcard",
			input:    "",
			pattern:  "vpc",
			expected: false,
		},
		{
			name:     "wildcard in middle",
			input:    "module-vpc-test",
			pattern:  "module-*-test",
			expected: true,
		},
		{
			name:     "complex pattern match",
			input:    "aws-prod-vpc-us-east-1",
			pattern:  "aws-*-vpc-*",
			expected: true,
		},
		{
			name:     "complex pattern no match",
			input:    "aws-prod-s3-us-east-1",
			pattern:  "aws-*-vpc-*",
			expected: false,
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

func TestShouldIgnoreModule(t *testing.T) {
	tests := []struct {
		name       string
		moduleName string
		patterns   []string
		expected   bool
	}{
		{
			name:       "no patterns",
			moduleName: "vpc",
			patterns:   []string{},
			expected:   false,
		},
		{
			name:       "exact match",
			moduleName: "vpc",
			patterns:   []string{"vpc"},
			expected:   true,
		},
		{
			name:       "no match",
			moduleName: "vpc",
			patterns:   []string{"s3"},
			expected:   false,
		},
		{
			name:       "match with wildcard prefix",
			moduleName: "legacy-vpc",
			patterns:   []string{"legacy-*"},
			expected:   true,
		},
		{
			name:       "match with wildcard suffix",
			moduleName: "vpc-test",
			patterns:   []string{"*-test"},
			expected:   true,
		},
		{
			name:       "multiple patterns, first matches",
			moduleName: "vpc",
			patterns:   []string{"vpc", "s3", "rds"},
			expected:   true,
		},
		{
			name:       "multiple patterns, second matches",
			moduleName: "s3",
			patterns:   []string{"vpc", "s3", "rds"},
			expected:   true,
		},
		{
			name:       "multiple patterns, none match",
			moduleName: "ec2",
			patterns:   []string{"vpc", "s3", "rds"},
			expected:   false,
		},
		{
			name:       "multiple patterns with wildcards",
			moduleName: "legacy-vpc-old",
			patterns:   []string{"legacy-*", "*-deprecated"},
			expected:   true,
		},
		{
			name:       "complex pattern match",
			moduleName: "prod-vpc-test",
			patterns:   []string{"*-vpc-*", "staging-*"},
			expected:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldIgnoreModule(tt.moduleName, tt.patterns)
			if result != tt.expected {
				t.Errorf("shouldIgnoreModule(%q, %v) = %v, want %v", tt.moduleName, tt.patterns, result, tt.expected)
			}
		})
	}
}

// Helper functions for TestUpdateModuleVersionWithIgnore

// checkModuleVersions checks if specific modules have been updated to the expected version
func checkModuleVersions(content string, modules map[string]bool) bool {
	lines := strings.Split(content, "\n")
	moduleStates := make(map[string]bool)
	currentModule := ""

	for _, line := range lines {
		// Detect which module we're in
		for moduleName := range modules {
			if strings.Contains(line, fmt.Sprintf(`module "%s"`, moduleName)) {
				currentModule = moduleName
				break
			}
		}

		// Check version lines
		if currentModule != "" && strings.Contains(line, "version") && strings.Contains(line, "5.0.0") {
			moduleStates[currentModule] = true
		}
	}

	// Verify expectations match reality
	for moduleName, shouldBeUpdated := range modules {
		wasUpdated := moduleStates[moduleName]
		if shouldBeUpdated != wasUpdated {
			return false
		}
	}
	return true
}

// checkTwoModuleUpdate checks if one module was updated and another was not
func checkTwoModuleUpdate(content, updatedModule, notUpdatedModule string) bool {
	lines := strings.Split(content, "\n")
	updatedFound := false
	notUpdatedFound := false
	inUpdated := false
	inNotUpdated := false

	for _, line := range lines {
		if strings.Contains(line, fmt.Sprintf(`module "%s"`, updatedModule)) {
			inUpdated = true
			inNotUpdated = false
		} else if strings.Contains(line, fmt.Sprintf(`module "%s"`, notUpdatedModule)) {
			inNotUpdated = true
			inUpdated = false
		}

		if strings.Contains(line, "version") {
			if inUpdated && strings.Contains(line, "5.0.0") {
				updatedFound = true
			}
			if inNotUpdated && strings.Contains(line, "5.0.0") {
				notUpdatedFound = true
			}
		}
	}

	return updatedFound && !notUpdatedFound
}

// checkNoVersion checks that a specific version string does not appear in content
func checkNoVersion(content, version string) bool {
	return !strings.Contains(content, fmt.Sprintf(`version = "%s"`, version))
}

// checkVersionCount counts occurrences of a specific version
func checkVersionCount(content, version string, expectedCount int) bool {
	count := strings.Count(content, fmt.Sprintf(`version = "%s"`, version))
	return count == expectedCount
}

func TestUpdateModuleVersionWithIgnore(t *testing.T) {
	tests := []struct {
		name           string
		inputContent   string
		moduleSource   string
		version        string
		ignorePatterns []string
		expectUpdate   bool
		expectError    bool
		checkContent   func(string) bool
	}{
		{
			name: "ignore exact module name",
			inputContent: `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"
}

module "vpc-prod" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"
}`,
			moduleSource:   "terraform-aws-modules/vpc/aws",
			version:        "5.0.0",
			ignorePatterns: []string{"vpc"},
			expectUpdate:   true,
			expectError:    false,
			checkContent: func(content string) bool {
				return checkTwoModuleUpdate(content, "vpc-prod", "vpc")
			},
		},
		{
			name: "ignore with wildcard prefix",
			inputContent: `module "legacy-vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"
}

module "vpc-prod" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"
}`,
			moduleSource:   "terraform-aws-modules/vpc/aws",
			version:        "5.0.0",
			ignorePatterns: []string{"legacy-*"},
			expectUpdate:   true,
			expectError:    false,
			checkContent: func(content string) bool {
				return checkTwoModuleUpdate(content, "vpc-prod", "legacy-vpc")
			},
		},
		{
			name: "ignore with wildcard suffix",
			inputContent: `module "vpc-test" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"
}

module "vpc-prod" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"
}`,
			moduleSource:   "terraform-aws-modules/vpc/aws",
			version:        "5.0.0",
			ignorePatterns: []string{"*-test"},
			expectUpdate:   true,
			expectError:    false,
			checkContent: func(content string) bool {
				return checkTwoModuleUpdate(content, "vpc-prod", "vpc-test")
			},
		},
		{
			name: "ignore all with wildcard",
			inputContent: `module "vpc1" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"
}

module "vpc2" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"
}`,
			moduleSource:   "terraform-aws-modules/vpc/aws",
			version:        "5.0.0",
			ignorePatterns: []string{"*"},
			expectUpdate:   false,
			expectError:    false,
			checkContent: func(content string) bool {
				return checkNoVersion(content, "5.0.0")
			},
		},
		{
			name: "multiple ignore patterns",
			inputContent: `module "legacy-vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"
}

module "vpc-test" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"
}

module "vpc-prod" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"
}`,
			moduleSource:   "terraform-aws-modules/vpc/aws",
			version:        "5.0.0",
			ignorePatterns: []string{"legacy-*", "*-test"},
			expectUpdate:   true,
			expectError:    false,
			checkContent: func(content string) bool {
				// Only vpc-prod should be updated
				return checkModuleVersions(content, map[string]bool{
					"legacy-vpc": false,
					"vpc-test":   false,
					"vpc-prod":   true,
				})
			},
		},
		{
			name: "no ignore patterns",
			inputContent: `module "vpc1" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"
}

module "vpc2" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"
}`,
			moduleSource:   "terraform-aws-modules/vpc/aws",
			version:        "5.0.0",
			ignorePatterns: []string{},
			expectUpdate:   true,
			expectError:    false,
			checkContent: func(content string) bool {
				return checkVersionCount(content, "5.0.0", 2)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp file
			tmpfile, err := os.CreateTemp("", "test-*.tf")
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer func() {
				if err := os.Remove(tmpfile.Name()); err != nil {
					t.Logf("Warning: failed to remove temp file: %v", err)
				}
			}()

			if _, err := tmpfile.WriteString(tt.inputContent); err != nil {
				t.Fatalf("Failed to write to temp file: %v", err)
			}
			if err := tmpfile.Close(); err != nil {
				t.Fatalf("Failed to close temp file: %v", err)
			}

			// Run updateModuleVersion
			updated, err := updateModuleVersion(tmpfile.Name(), tt.moduleSource, tt.version, "", tt.ignorePatterns, false, false)

			if (err != nil) != tt.expectError {
				t.Errorf("updateModuleVersion() error = %v, expectError %v", err, tt.expectError)
				return
			}

			if updated != tt.expectUpdate {
				t.Errorf("updateModuleVersion() updated = %v, expectUpdate %v", updated, tt.expectUpdate)
			}

			// Check file content
			content, err := os.ReadFile(tmpfile.Name())
			if err != nil {
				t.Fatalf("Failed to read temp file: %v", err)
			}

			if tt.checkContent != nil && !tt.checkContent(string(content)) {
				t.Errorf("Content check failed. Content:\n%s", string(content))
			}
		})
	}
}
