package main

import (
	"fmt"
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
		_, err := updateModuleVersion(tf1File, update.Source, update.Version, update.From, nil, false, false)
		if err != nil {
			t.Errorf("Failed to update %s: %v", tf1File, err)
		}
		_, err = updateModuleVersion(tf2File, update.Source, update.Version, update.From, nil, false, false)
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

// TestLoadConfigWithComments tests config file parsing with YAML comments
func TestLoadConfigWithComments(t *testing.T) {
	configYAML := `# Configuration file for module updates
modules:
  # Update VPC module
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
  # Update S3 bucket module
  - source: "terraform-aws-modules/s3-bucket/aws"
    version: "4.0.0"
`
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yml")

	err := os.WriteFile(configFile, []byte(configYAML), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp config file: %v", err)
	}

	updates, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(updates) != 2 {
		t.Errorf("Got %d modules, want 2", len(updates))
	}
}

// TestLoadConfigWithGitSources tests config with Git-based module sources
func TestLoadConfigWithGitSources(t *testing.T) {
	configYAML := `modules:
  - source: "git::https://github.com/example/terraform-module.git"
    version: "v1.0.0"
  - source: "git::ssh://git@github.com/example/private-module.git"
    version: "v2.0.0"
`
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yml")

	err := os.WriteFile(configFile, []byte(configYAML), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp config file: %v", err)
	}

	updates, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(updates) != 2 {
		t.Errorf("Got %d modules, want 2", len(updates))
	}

	if updates[0].Source != "git::https://github.com/example/terraform-module.git" {
		t.Errorf("First module source = %q, want %q", updates[0].Source, "git::https://github.com/example/terraform-module.git")
	}
	if updates[0].Version != "v1.0.0" {
		t.Errorf("First module version = %q, want %q", updates[0].Version, "v1.0.0")
	}
}

// TestLoadConfigWithLocalModules tests config file parsing with local module sources.
// Note: This tests the config parsing capability only. In actual Terraform usage,
// local modules (./path or ../path) typically don't use version attributes since
// they reference local filesystem paths. However, this tool allows version tracking
// for local modules for use cases like internal versioning or documentation purposes.
func TestLoadConfigWithLocalModules(t *testing.T) {
	configYAML := `modules:
  - source: "./modules/vpc"
    version: "1.0.0"
  - source: "../shared-modules/s3"
    version: "2.0.0"
`
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yml")

	err := os.WriteFile(configFile, []byte(configYAML), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp config file: %v", err)
	}

	updates, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(updates) != 2 {
		t.Errorf("Got %d modules, want 2", len(updates))
	}

	// Verify local paths are parsed correctly
	if updates[0].Source != "./modules/vpc" {
		t.Errorf("First module source = %q, want %q", updates[0].Source, "./modules/vpc")
	}
	if updates[1].Source != "../shared-modules/s3" {
		t.Errorf("Second module source = %q, want %q", updates[1].Source, "../shared-modules/s3")
	}
}

// TestLoadConfigMultipleMissingFields tests various field validation errors
func TestLoadConfigMultipleMissingFields(t *testing.T) {
	tests := []struct {
		name        string
		configYAML  string
		errorSubstr string
	}{
		{
			name: "first module missing source",
			configYAML: `modules:
  - version: "5.0.0"
  - source: "terraform-aws-modules/s3-bucket/aws"
    version: "4.0.0"
`,
			errorSubstr: "module at index 0 is missing 'source' field",
		},
		{
			name: "second module missing version",
			configYAML: `modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
  - source: "terraform-aws-modules/s3-bucket/aws"
`,
			errorSubstr: "module at index 1 is missing 'version' field",
		},
		{
			name: "middle module missing source",
			configYAML: `modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
  - version: "4.0.0"
  - source: "terraform-aws-modules/rds/aws"
    version: "3.0.0"
`,
			errorSubstr: "module at index 1 is missing 'source' field",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configFile := filepath.Join(tmpDir, "config.yml")

			err := os.WriteFile(configFile, []byte(tt.configYAML), 0644)
			if err != nil {
				t.Fatalf("Failed to create temp config file: %v", err)
			}

			_, err = loadConfig(configFile)
			if err == nil {
				t.Error("Expected error but got none")
				return
			}

			if !strings.Contains(err.Error(), tt.errorSubstr) {
				t.Errorf("Expected error containing %q, got: %v", tt.errorSubstr, err)
			}
		})
	}
}

// TestLoadConfigLargeFile tests handling of a config with many modules
func TestLoadConfigLargeFile(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("modules:\n")

	// Generate 50 module entries
	for i := 0; i < 50; i++ {
		sb.WriteString(fmt.Sprintf(`  - source: "terraform-aws-modules/module-%d/aws"
    version: "1.0.%d"
`, i, i))
	}

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yml")

	err := os.WriteFile(configFile, []byte(sb.String()), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp config file: %v", err)
	}

	updates, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(updates) != 50 {
		t.Errorf("Got %d modules, want 50", len(updates))
	}

	// Verify first and last entries
	if updates[0].Source != "terraform-aws-modules/module-0/aws" {
		t.Errorf("First module source incorrect")
	}
	if updates[49].Source != "terraform-aws-modules/module-49/aws" {
		t.Errorf("Last module source incorrect")
	}
}

// TestLoadConfigWithFromField tests config parsing with optional 'from' field
func TestLoadConfigWithFromField(t *testing.T) {
	tests := []struct {
		name        string
		configYAML  string
		expectError bool
		expectCount int
		validate    func(*testing.T, []ModuleUpdate)
	}{
		{
			name: "config with from field specified",
			configYAML: `modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
    from: "3.14.0"
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
				if updates[0].From != "3.14.0" {
					t.Errorf("Module from = %q, want %q", updates[0].From, "3.14.0")
				}
			},
		},
		{
			name: "config with mixed from fields",
			configYAML: `modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
    from: "3.14.0"
  - source: "terraform-aws-modules/s3-bucket/aws"
    version: "4.0.0"
`,
			expectError: false,
			expectCount: 2,
			validate: func(t *testing.T, updates []ModuleUpdate) {
				if updates[0].From != "3.14.0" {
					t.Errorf("First module from = %q, want %q", updates[0].From, "3.14.0")
				}
				if updates[1].From != "" {
					t.Errorf("Second module from = %q, want empty string", updates[1].From)
				}
			},
		},
		{
			name: "config with all modules having from field",
			configYAML: `modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
    from: "3.14.0"
  - source: "terraform-aws-modules/s3-bucket/aws"
    version: "4.0.0"
    from: "3.0.0"
  - source: "terraform-aws-modules/iam/aws"
    version: "5.2.0"
    from: "5.1.0"
`,
			expectError: false,
			expectCount: 3,
			validate: func(t *testing.T, updates []ModuleUpdate) {
				if updates[0].From != "3.14.0" {
					t.Errorf("First module from = %q, want %q", updates[0].From, "3.14.0")
				}
				if updates[1].From != "3.0.0" {
					t.Errorf("Second module from = %q, want %q", updates[1].From, "3.0.0")
				}
				if updates[2].From != "5.1.0" {
					t.Errorf("Third module from = %q, want %q", updates[2].From, "5.1.0")
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
				t.Fatalf("Failed to create temp config file: %v", err)
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

			if len(updates) != tt.expectCount {
				t.Errorf("Got %d modules, want %d", len(updates), tt.expectCount)
			}

			if tt.validate != nil {
				tt.validate(t, updates)
			}
		})
	}
}

// TestConfigFileIntegrationWithFromField tests end-to-end config file usage with from field
func TestConfigFileIntegrationWithFromField(t *testing.T) {
	// Create test Terraform files
	tmpDir := t.TempDir()

	tfContent := `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"
}

module "s3" {
  source  = "terraform-aws-modules/s3-bucket/aws"
  version = "3.0.0"
}

module "iam" {
  source  = "terraform-aws-modules/iam/aws"
  version = "5.1.0"
}
`

	tfFile := filepath.Join(tmpDir, "test.tf")
	err := os.WriteFile(tfFile, []byte(tfContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp Terraform file: %v", err)
	}

	// Create config with from field
	configYAML := `modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
    from: "3.14.0"
  - source: "terraform-aws-modules/s3-bucket/aws"
    version: "4.0.0"
    from: "2.0.0"
  - source: "terraform-aws-modules/iam/aws"
    version: "5.2.0"
`

	configFile := filepath.Join(tmpDir, "config.yml")
	err = os.WriteFile(configFile, []byte(configYAML), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp config file: %v", err)
	}

	// Load config
	updates, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Apply updates
	for _, update := range updates {
		_, err := updateModuleVersion(tfFile, update.Source, update.Version, update.From, nil, false, false)
		if err != nil {
			t.Fatalf("Failed to update module: %v", err)
		}
	}

	// Verify results
	content, err := os.ReadFile(tfFile)
	if err != nil {
		t.Fatalf("Failed to read updated file: %v", err)
	}

	contentStr := string(content)

	// VPC should be updated (from 3.14.0 matches)
	if !strings.Contains(contentStr, `version = "5.0.0"`) {
		t.Error("VPC module should be updated to 5.0.0")
	}

	// S3 should NOT be updated (from 2.0.0 doesn't match current 3.0.0)
	if !strings.Contains(contentStr, `version = "3.0.0"`) {
		t.Error("S3 module should remain at 3.0.0")
	}

	// IAM should be updated (no from filter)
	if !strings.Contains(contentStr, `version = "5.2.0"`) {
		t.Error("IAM module should be updated to 5.2.0")
	}
}

// TestLoadConfigEmptyFile tests loading an empty config file
func TestLoadConfigEmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "empty.yml")

	err := os.WriteFile(configFile, []byte(""), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp config file: %v", err)
	}

	updates, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(updates) != 0 {
		t.Errorf("Expected 0 updates from empty file, got %d", len(updates))
	}
}

// TestLoadConfigOnlyComments tests loading a config file with only comments
func TestLoadConfigOnlyComments(t *testing.T) {
	configYAML := `# This is a comment
# Another comment
# No actual modules here
`
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "comments.yml")

	err := os.WriteFile(configFile, []byte(configYAML), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp config file: %v", err)
	}

	updates, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(updates) != 0 {
		t.Errorf("Expected 0 updates from comment-only file, got %d", len(updates))
	}
}

// TestLoadConfigSpecialCharacters tests modules with special characters in source
func TestLoadConfigSpecialCharacters(t *testing.T) {
	configYAML := `modules:
  - source: "terraform-aws-modules/vpc_test-123/aws"
    version: "1.0.0"
  - source: "registry.example.com/org/module-name/provider"
    version: "2.0.0"
`
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yml")

	err := os.WriteFile(configFile, []byte(configYAML), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp config file: %v", err)
	}

	updates, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(updates) != 2 {
		t.Errorf("Expected 2 updates, got %d", len(updates))
	}

	if updates[0].Source != "terraform-aws-modules/vpc_test-123/aws" {
		t.Errorf("First module source incorrect: %s", updates[0].Source)
	}

	if updates[1].Source != "registry.example.com/org/module-name/provider" {
		t.Errorf("Second module source incorrect: %s", updates[1].Source)
	}
}

// TestLoadConfigDuplicateModules tests config with duplicate module sources
func TestLoadConfigDuplicateModules(t *testing.T) {
	configYAML := `modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.1.0"
`
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yml")

	err := os.WriteFile(configFile, []byte(configYAML), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp config file: %v", err)
	}

	updates, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should parse both entries even if they're duplicates
	if len(updates) != 2 {
		t.Errorf("Expected 2 updates, got %d", len(updates))
	}

	if updates[0].Version != "5.0.0" {
		t.Errorf("First module version = %q, want %q", updates[0].Version, "5.0.0")
	}

	if updates[1].Version != "5.1.0" {
		t.Errorf("Second module version = %q, want %q", updates[1].Version, "5.1.0")
	}
}

// TestLoadConfigMixedQuotes tests config with various YAML quoting styles
func TestLoadConfigMixedQuotes(t *testing.T) {
	configYAML := `modules:
  - source: 'terraform-aws-modules/vpc/aws'
    version: '5.0.0'
  - source: "terraform-aws-modules/s3-bucket/aws"
    version: "4.0.0"
  - source: terraform-aws-modules/iam/aws
    version: 3.0.0
`
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yml")

	err := os.WriteFile(configFile, []byte(configYAML), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp config file: %v", err)
	}

	updates, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(updates) != 3 {
		t.Errorf("Expected 3 updates, got %d", len(updates))
	}

	// All should be parsed correctly regardless of quote style
	expectedSources := []string{
		"terraform-aws-modules/vpc/aws",
		"terraform-aws-modules/s3-bucket/aws",
		"terraform-aws-modules/iam/aws",
	}

	for i, expected := range expectedSources {
		if updates[i].Source != expected {
			t.Errorf("Module %d source = %q, want %q", i, updates[i].Source, expected)
		}
	}
}

// TestLoadConfigVeryLongVersionString tests handling of very long version strings in config
func TestLoadConfigVeryLongVersionString(t *testing.T) {
	longVersion := "5.0.0-alpha.1.2.3.4.5.6.7.8.9.10+build.metadata.with.lots.of.segments.2024.01.15.abc123def456.and.more.segments"
	configYAML := fmt.Sprintf(`modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "%s"
`, longVersion)

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yml")

	err := os.WriteFile(configFile, []byte(configYAML), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp config file: %v", err)
	}

	updates, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(updates) != 1 {
		t.Errorf("Expected 1 update, got %d", len(updates))
	}

	if updates[0].Version != longVersion {
		t.Errorf("Version = %q, want %q", updates[0].Version, longVersion)
	}
}

// TestLoadConfigWhitespaceInValues tests handling of extra whitespace in YAML values
func TestLoadConfigWhitespaceInValues(t *testing.T) {
	configYAML := `modules:
  - source:  "  terraform-aws-modules/vpc/aws  "
    version:  "  5.0.0  "
    from:  "  4.0.0  "
`
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yml")

	err := os.WriteFile(configFile, []byte(configYAML), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp config file: %v", err)
	}

	updates, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(updates) != 1 {
		t.Errorf("Expected 1 update, got %d", len(updates))
	}

	// Source and Version should have whitespace trimmed by loadConfig
	if updates[0].Source != "terraform-aws-modules/vpc/aws" {
		t.Errorf("Source = %q, want %q (whitespace should be trimmed)", updates[0].Source, "terraform-aws-modules/vpc/aws")
	}
	if updates[0].Version != "5.0.0" {
		t.Errorf("Version = %q, want %q (whitespace should be trimmed)", updates[0].Version, "5.0.0")
	}
	if updates[0].From != "4.0.0" {
		t.Errorf("From = %q, want %q (whitespace should be trimmed)", updates[0].From, "4.0.0")
	}
}

// TestLoadConfigVersionConstraintsWithWhitespace tests version constraints with whitespace
func TestLoadConfigVersionConstraintsWithWhitespace(t *testing.T) {
	tests := []struct {
		name            string
		versionInput    string
		expectedVersion string
		fromInput       string
		expectedFrom    string
	}{
		{
			name:            "pessimistic constraint with trailing space",
			versionInput:    "~> 1.0 ",
			expectedVersion: "~> 1.0",
			fromInput:       "~> 0.9 ",
			expectedFrom:    "~> 0.9",
		},
		{
			name:            "greater than or equal with leading space",
			versionInput:    " >= 2.0.0",
			expectedVersion: ">= 2.0.0",
			fromInput:       " >= 1.0.0",
			expectedFrom:    ">= 1.0.0",
		},
		{
			name:            "exact version with surrounding spaces",
			versionInput:    "  = 3.5.0  ",
			expectedVersion: "= 3.5.0",
			fromInput:       "  = 3.4.0  ",
			expectedFrom:    "= 3.4.0",
		},
		{
			name:            "range constraint with spaces",
			versionInput:    " >= 1.0, < 2.0 ",
			expectedVersion: ">= 1.0, < 2.0",
			fromInput:       " >= 0.9, < 1.0 ",
			expectedFrom:    ">= 0.9, < 1.0",
		},
		{
			name:            "less than constraint with whitespace",
			versionInput:    "< 5.0.0  ",
			expectedVersion: "< 5.0.0",
			fromInput:       "< 4.0.0  ",
			expectedFrom:    "< 4.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configYAML := fmt.Sprintf(`modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "%s"
    from: "%s"
`, tt.versionInput, tt.fromInput)

			tmpDir := t.TempDir()
			configFile := filepath.Join(tmpDir, "config.yml")

			err := os.WriteFile(configFile, []byte(configYAML), 0644)
			if err != nil {
				t.Fatalf("Failed to create temp config file: %v", err)
			}

			updates, err := loadConfig(configFile)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if len(updates) != 1 {
				t.Errorf("Expected 1 update, got %d", len(updates))
			}

			// Version constraints should have whitespace trimmed while preserving the constraint operators
			if updates[0].Version != tt.expectedVersion {
				t.Errorf("Version = %q, want %q (whitespace should be trimmed, constraint preserved)", updates[0].Version, tt.expectedVersion)
			}

			if updates[0].From != tt.expectedFrom {
				t.Errorf("From = %q, want %q (whitespace should be trimmed, constraint preserved)", updates[0].From, tt.expectedFrom)
			}
		})
	}
}


func TestLoadConfigWithIgnoreField(t *testing.T) {
	configYAML := `modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
    ignore:
      - "legacy-vpc"
      - "test-*"
  - source: "terraform-aws-modules/s3-bucket/aws"
    version: "4.0.0"
    from: "3.0.0"
    ignore:
      - "*-deprecated"
`

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yml")

	err := os.WriteFile(configFile, []byte(configYAML), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp config file: %v", err)
	}

	updates, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(updates) != 2 {
		t.Errorf("Expected 2 updates, got %d", len(updates))
	}

	// Check first module
	if updates[0].Source != "terraform-aws-modules/vpc/aws" {
		t.Errorf("First module source = %q, want %q", updates[0].Source, "terraform-aws-modules/vpc/aws")
	}
	if updates[0].Version != "5.0.0" {
		t.Errorf("First module version = %q, want %q", updates[0].Version, "5.0.0")
	}
	if len(updates[0].Ignore) != 2 {
		t.Fatalf("First module ignore patterns count = %d, want 2", len(updates[0].Ignore))
	}
	if updates[0].Ignore[0] != "legacy-vpc" {
		t.Errorf("First module ignore[0] = %q, want %q", updates[0].Ignore[0], "legacy-vpc")
	}
	if updates[0].Ignore[1] != "test-*" {
		t.Errorf("First module ignore[1] = %q, want %q", updates[0].Ignore[1], "test-*")
	}

	// Check second module
	if updates[1].Source != "terraform-aws-modules/s3-bucket/aws" {
		t.Errorf("Second module source = %q, want %q", updates[1].Source, "terraform-aws-modules/s3-bucket/aws")
	}
	if updates[1].Version != "4.0.0" {
		t.Errorf("Second module version = %q, want %q", updates[1].Version, "4.0.0")
	}
	if updates[1].From != "3.0.0" {
		t.Errorf("Second module from = %q, want %q", updates[1].From, "3.0.0")
	}
	if len(updates[1].Ignore) != 1 {
		t.Fatalf("Second module ignore patterns count = %d, want 1", len(updates[1].Ignore))
	}
	if updates[1].Ignore[0] != "*-deprecated" {
		t.Errorf("Second module ignore[0] = %q, want %q", updates[1].Ignore[0], "*-deprecated")
	}
}

func TestLoadConfigWithIgnoreFieldWhitespace(t *testing.T) {
	configYAML := `modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
    ignore:
      - "  legacy-vpc  "
      - "  test-*  "
`

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yml")

	err := os.WriteFile(configFile, []byte(configYAML), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp config file: %v", err)
	}

	updates, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(updates) != 1 {
		t.Errorf("Expected 1 update, got %d", len(updates))
	}

	// Whitespace should be trimmed from ignore patterns
	if len(updates[0].Ignore) != 2 {
		t.Fatalf("Ignore patterns count = %d, want 2", len(updates[0].Ignore))
	}
	if updates[0].Ignore[0] != "legacy-vpc" {
		t.Errorf("Ignore[0] = %q, want %q (whitespace should be trimmed)", updates[0].Ignore[0], "legacy-vpc")
	}
	if updates[0].Ignore[1] != "test-*" {
		t.Errorf("Ignore[1] = %q, want %q (whitespace should be trimmed)", updates[0].Ignore[1], "test-*")
	}
}

func TestLoadConfigWithEmptyIgnoreField(t *testing.T) {
	configYAML := `modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
    ignore: []
`

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yml")

	err := os.WriteFile(configFile, []byte(configYAML), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp config file: %v", err)
	}

	updates, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(updates) != 1 {
		t.Errorf("Expected 1 update, got %d", len(updates))
	}

	// Empty ignore array should be allowed
	if len(updates[0].Ignore) != 0 {
		t.Errorf("Ignore patterns count = %d, want 0", len(updates[0].Ignore))
	}
}

func TestLoadConfigWithoutIgnoreField(t *testing.T) {
	configYAML := `modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
`

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yml")

	err := os.WriteFile(configFile, []byte(configYAML), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp config file: %v", err)
	}

	updates, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(updates) != 1 {
		t.Errorf("Expected 1 update, got %d", len(updates))
	}

	// Modules without ignore field should have nil/empty slice
	if updates[0].Ignore == nil || len(updates[0].Ignore) == 0 {
		// This is expected behavior
	} else {
		t.Errorf("Ignore patterns should be nil or empty, got %v", updates[0].Ignore)
	}
}
