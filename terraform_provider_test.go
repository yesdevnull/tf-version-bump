package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUpdateTerraformVersion tests updating Terraform required_version
func TestUpdateTerraformVersion(t *testing.T) {
	tests := []struct {
		name         string
		inputContent string
		version      string
		expectUpdate bool
		expectError  bool
		checkContent func(string) bool
	}{
		{
			name: "update terraform required_version",
			inputContent: `terraform {
  required_version = ">= 1.0"
}

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.0.0"
}`,
			version:      ">= 1.5",
			expectUpdate: true,
			expectError:  false,
			checkContent: func(content string) bool {
				return strings.Contains(content, `required_version = ">= 1.5"`)
			},
		},
		{
			name: "update terraform version with providers",
			inputContent: `terraform {
  required_version = ">= 1.0"
  
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}`,
			version:      ">= 1.6",
			expectUpdate: true,
			expectError:  false,
			checkContent: func(content string) bool {
				hasNewVersion := strings.Contains(content, `required_version = ">= 1.6"`)
				// Provider version should not change
				hasProviderVersion := strings.Contains(content, `version = "~> 4.0"`)
				return hasNewVersion && hasProviderVersion
			},
		},
		{
			name: "no terraform block",
			inputContent: `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.0.0"
}`,
			version:      ">= 1.5",
			expectUpdate: false,
			expectError:  false,
			checkContent: nil,
		},
		{
			name: "multiple terraform blocks (unusual but valid)",
			inputContent: `terraform {
  required_version = ">= 1.0"
}

terraform {
  required_providers {
    aws {
      source = "hashicorp/aws"
    }
  }
}`,
			version:      ">= 1.5",
			expectUpdate: true,
			expectError:  false,
			checkContent: func(content string) bool {
				// Both terraform blocks should be updated
				return strings.Count(content, `required_version = ">= 1.5"`) == 2
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

			// Run the update
			updated, err := updateTerraformVersion(tmpFile, tt.version, false)

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

// TestUpdateTerraformVersionDryRun tests dry-run mode for terraform version updates
func TestUpdateTerraformVersionDryRun(t *testing.T) {
	inputContent := `terraform {
  required_version = ">= 1.0"
}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.tf")

	err := os.WriteFile(tmpFile, []byte(inputContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	// Run in dry-run mode
	updated, err := updateTerraformVersion(tmpFile, ">= 1.5", true)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if !updated {
		t.Error("Expected updated=true in dry-run mode")
	}

	// File should NOT be modified
	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	if !strings.Contains(string(content), `required_version = ">= 1.0"`) {
		t.Error("File was modified in dry-run mode")
	}

	if strings.Contains(string(content), `required_version = ">= 1.5"`) {
		t.Error("File contains new version in dry-run mode")
	}
}

// TestUpdateProviderVersion tests updating provider versions in required_providers blocks
func TestUpdateProviderVersion(t *testing.T) {
	tests := []struct {
		name         string
		inputContent string
		providerName string
		version      string
		expectUpdate bool
		expectError  bool
		checkContent func(string) bool
	}{
		{
			name: "update aws provider version",
			inputContent: `terraform {
  required_version = ">= 1.0"
  
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}`,
			providerName: "aws",
			version:      "~> 5.0",
			expectUpdate: true,
			expectError:  false,
			checkContent: func(content string) bool {
				// AWS version should be updated
				hasNewVersion := strings.Contains(content, `version = "~> 5.0"`)
				// Terraform version should not change
				hasTerraformVersion := strings.Contains(content, `required_version = ">= 1.0"`)
				return hasNewVersion && hasTerraformVersion
			},
		},
		{
			name: "update one of multiple providers",
			inputContent: `terraform {
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
    azurerm {
      source  = "hashicorp/azurerm"
      version = "~> 3.0"
    }
  }
}`,
			providerName: "azurerm",
			version:      "~> 3.5",
			expectUpdate: true,
			expectError:  false,
			checkContent: func(content string) bool {
				// Azure version should be updated
				hasAzureUpdate := strings.Contains(content, `version = "~> 3.5"`)
				// AWS version should not change
				hasAwsVersion := strings.Contains(content, `version = "~> 4.0"`)
				return hasAzureUpdate && hasAwsVersion
			},
		},
		{
			name: "provider not found",
			inputContent: `terraform {
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}`,
			providerName: "google",
			version:      "~> 4.0",
			expectUpdate: false,
			expectError:  false,
			checkContent: nil,
		},
		{
			name: "no required_providers block",
			inputContent: `terraform {
  required_version = ">= 1.0"
}`,
			providerName: "aws",
			version:      "~> 5.0",
			expectUpdate: false,
			expectError:  false,
			checkContent: nil,
		},
		{
			name: "no terraform block",
			inputContent: `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.0.0"
}`,
			providerName: "aws",
			version:      "~> 5.0",
			expectUpdate: false,
			expectError:  false,
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
			updated, err := updateProviderVersion(tmpFile, tt.providerName, tt.version, false)

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

// TestUpdateProviderVersionDryRun tests dry-run mode for provider version updates
func TestUpdateProviderVersionDryRun(t *testing.T) {
	inputContent := `terraform {
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.tf")

	err := os.WriteFile(tmpFile, []byte(inputContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	// Run in dry-run mode
	updated, err := updateProviderVersion(tmpFile, "aws", "~> 5.0", true)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if !updated {
		t.Error("Expected updated=true in dry-run mode")
	}

	// File should NOT be modified
	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	if !strings.Contains(string(content), `version = "~> 4.0"`) {
		t.Error("File was modified in dry-run mode")
	}

	if strings.Contains(string(content), `version = "~> 5.0"`) {
		t.Error("File contains new version in dry-run mode")
	}
}

// TestProcessTerraformVersion tests the high-level function for processing multiple files
func TestProcessTerraformVersion(t *testing.T) {
	tmpDir := t.TempDir()

	// Create multiple test files
	file1Content := `terraform {
  required_version = ">= 1.0"
}`
	file2Content := `terraform {
  required_version = ">= 1.1"
}

module "vpc" {
  source = "terraform-aws-modules/vpc/aws"
  version = "5.0.0"
}`
	file3Content := `module "s3" {
  source = "terraform-aws-modules/s3-bucket/aws"
  version = "3.0.0"
}`

	file1 := filepath.Join(tmpDir, "main.tf")
	file2 := filepath.Join(tmpDir, "vpc.tf")
	file3 := filepath.Join(tmpDir, "s3.tf")

	if err := os.WriteFile(file1, []byte(file1Content), 0644); err != nil {
		t.Fatalf("Failed to create file1: %v", err)
	}
	if err := os.WriteFile(file2, []byte(file2Content), 0644); err != nil {
		t.Fatalf("Failed to create file2: %v", err)
	}
	if err := os.WriteFile(file3, []byte(file3Content), 0644); err != nil {
		t.Fatalf("Failed to create file3: %v", err)
	}

	files := []string{file1, file2, file3}

	// Process all files
	count := processTerraformVersion(files, ">= 1.5", false, "text")

	// Should update 2 files (file1 and file2, not file3 which has no terraform block)
	if count != 2 {
		t.Errorf("Expected 2 files updated, got %d", count)
	}

	// Verify updates
	content1, _ := os.ReadFile(file1)
	content2, _ := os.ReadFile(file2)
	content3, _ := os.ReadFile(file3)

	if !strings.Contains(string(content1), `required_version = ">= 1.5"`) {
		t.Error("file1 was not updated correctly")
	}
	if !strings.Contains(string(content2), `required_version = ">= 1.5"`) {
		t.Error("file2 was not updated correctly")
	}
	if string(content3) != file3Content {
		t.Error("file3 should not have been modified")
	}
}

// TestProcessProviderVersion tests the high-level function for processing multiple files
func TestProcessProviderVersion(t *testing.T) {
	tmpDir := t.TempDir()

	// Create multiple test files
	file1Content := `terraform {
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}`
	file2Content := `terraform {
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 4.5"
    }
  }
}`
	file3Content := `module "s3" {
  source = "terraform-aws-modules/s3-bucket/aws"
  version = "3.0.0"
}`

	file1 := filepath.Join(tmpDir, "main.tf")
	file2 := filepath.Join(tmpDir, "vpc.tf")
	file3 := filepath.Join(tmpDir, "s3.tf")

	if err := os.WriteFile(file1, []byte(file1Content), 0644); err != nil {
		t.Fatalf("Failed to create file1: %v", err)
	}
	if err := os.WriteFile(file2, []byte(file2Content), 0644); err != nil {
		t.Fatalf("Failed to create file2: %v", err)
	}
	if err := os.WriteFile(file3, []byte(file3Content), 0644); err != nil {
		t.Fatalf("Failed to create file3: %v", err)
	}

	files := []string{file1, file2, file3}

	// Process all files
	count := processProviderVersion(files, "aws", "~> 5.0", false, "text")

	// Should update 2 files (file1 and file2, not file3 which has no provider)
	if count != 2 {
		t.Errorf("Expected 2 files updated, got %d", count)
	}

	// Verify updates
	content1, _ := os.ReadFile(file1)
	content2, _ := os.ReadFile(file2)
	content3, _ := os.ReadFile(file3)

	if !strings.Contains(string(content1), `version = "~> 5.0"`) {
		t.Error("file1 was not updated correctly")
	}
	if !strings.Contains(string(content2), `version = "~> 5.0"`) {
		t.Error("file2 was not updated correctly")
	}
	if string(content3) != file3Content {
		t.Error("file3 should not have been modified")
	}
}
