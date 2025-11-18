package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name        string
		configYAML  string
		expectError bool
		expectCount int
		validate    func(*testing.T, []ModuleUpdate)
	}{
		{
			name: "valid config with multiple modules",
			configYAML: `modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
  - source: "terraform-aws-modules/s3-bucket/aws"
    version: "4.0.0"
`,
			expectError: false,
			expectCount: 2,
			validate: func(t *testing.T, updates []ModuleUpdate) {
				if updates[0].Source != "terraform-aws-modules/vpc/aws" {
					t.Errorf("First module source = %q, want %q", updates[0].Source, "terraform-aws-modules/vpc/aws")
				}
				if updates[0].Version != "5.0.0" {
					t.Errorf("First module version = %q, want %q", updates[0].Version, "5.0.0")
				}
				if updates[1].Source != "terraform-aws-modules/s3-bucket/aws" {
					t.Errorf("Second module source = %q, want %q", updates[1].Source, "terraform-aws-modules/s3-bucket/aws")
				}
				if updates[1].Version != "4.0.0" {
					t.Errorf("Second module version = %q, want %q", updates[1].Version, "4.0.0")
				}
			},
		},
		{
			name: "valid config with single module",
			configYAML: `modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
`,
			expectError: false,
			expectCount: 1,
			validate: func(t *testing.T, updates []ModuleUpdate) {
				if updates[0].Source != "terraform-aws-modules/vpc/aws" {
					t.Errorf("Module source = %q, want %q", updates[0].Source, "terraform-aws-modules/vpc/aws")
				}
				if updates[0].Version != "5.0.0" {
					t.Errorf("Module version = %q, want %q", updates[0].Version, "5.0.0")
				}
			},
		},
		{
			name: "config with module with subpath",
			configYAML: `modules:
  - source: "terraform-aws-modules/iam/aws//modules/iam-user"
    version: "5.2.0"
`,
			expectError: false,
			expectCount: 1,
			validate: func(t *testing.T, updates []ModuleUpdate) {
				if updates[0].Source != "terraform-aws-modules/iam/aws//modules/iam-user" {
					t.Errorf("Module source = %q, want %q", updates[0].Source, "terraform-aws-modules/iam/aws//modules/iam-user")
				}
			},
		},
		{
			name: "config missing source field",
			configYAML: `modules:
  - version: "5.0.0"
`,
			expectError: true,
		},
		{
			name: "config missing version field",
			configYAML: `modules:
  - source: "terraform-aws-modules/vpc/aws"
`,
			expectError: true,
		},
		{
			name: "empty modules list",
			configYAML: `modules: []
`,
			expectError: false,
			expectCount: 0,
		},
		{
			name:        "invalid YAML",
			configYAML:  `modules:\n  - source: "test\n    invalid`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary config file
			tmpDir := t.TempDir()
			configFile := filepath.Join(tmpDir, "config.yml")

			err := os.WriteFile(configFile, []byte(tt.configYAML), 0644)
			if err != nil {
				t.Fatalf("Failed to create temp config file: %v", err)
			}

			// Load config
			updates, err := loadConfig(configFile)

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

			// Check count
			if len(updates) != tt.expectCount {
				t.Errorf("Got %d modules, want %d", len(updates), tt.expectCount)
			}

			// Run custom validation if provided
			if tt.validate != nil {
				tt.validate(t, updates)
			}
		})
	}
}

func TestLoadConfigFileNotFound(t *testing.T) {
	_, err := loadConfig("/nonexistent/config.yml")
	if err == nil {
		t.Error("Expected error for non-existent file")
	}
	if !strings.Contains(err.Error(), "failed to read config file") {
		t.Errorf("Expected 'failed to read config file' error, got: %v", err)
	}
}

func TestConfigFileIntegration(t *testing.T) {
	// Create temporary directory with test files
	tmpDir := t.TempDir()

	// Create test Terraform files
	tf1Content := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
  name    = "test-vpc"
}

module "s3" {
  source  = "terraform-aws-modules/s3-bucket/aws"
  version = "3.0.0"
  bucket  = "test-bucket"
}
`

	tf2Content := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
  name    = "another-vpc"
}
`

	tf1File := filepath.Join(tmpDir, "test1.tf")
	tf2File := filepath.Join(tmpDir, "test2.tf")

	if err := os.WriteFile(tf1File, []byte(tf1Content), 0644); err != nil {
		t.Fatalf("Failed to create test1.tf: %v", err)
	}
	if err := os.WriteFile(tf2File, []byte(tf2Content), 0644); err != nil {
		t.Fatalf("Failed to create test2.tf: %v", err)
	}

	// Create config file
	configYAML := `modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
  - source: "terraform-aws-modules/s3-bucket/aws"
    version: "4.0.0"
`
	configFile := filepath.Join(tmpDir, "config.yml")
	if err := os.WriteFile(configFile, []byte(configYAML), 0644); err != nil {
		t.Fatalf("Failed to create config file: %v", err)
	}

	// Load config
	updates, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Apply updates
	for _, update := range updates {
		_, err := updateModuleVersion(tf1File, update.Source, update.Version)
		if err != nil {
			t.Errorf("Failed to update %s: %v", tf1File, err)
		}
		_, err = updateModuleVersion(tf2File, update.Source, update.Version)
		if err != nil {
			t.Errorf("Failed to update %s: %v", tf2File, err)
		}
	}

	// Verify updates in test1.tf
	content1, err := os.ReadFile(tf1File)
	if err != nil {
		t.Fatalf("Failed to read test1.tf: %v", err)
	}
	content1Str := string(content1)
	if !strings.Contains(content1Str, `version = "5.0.0"`) {
		t.Error("VPC module in test1.tf was not updated to 5.0.0")
	}
	if !strings.Contains(content1Str, `version = "4.0.0"`) {
		t.Error("S3 module in test1.tf was not updated to 4.0.0")
	}

	// Verify updates in test2.tf
	content2, err := os.ReadFile(tf2File)
	if err != nil {
		t.Fatalf("Failed to read test2.tf: %v", err)
	}
	content2Str := string(content2)
	if !strings.Contains(content2Str, `version = "5.0.0"`) {
		t.Error("VPC module in test2.tf was not updated to 5.0.0")
	}
}
