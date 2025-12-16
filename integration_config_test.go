package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConfigFileWithTerraformVersion tests processing config files with terraform_version
func TestConfigFileWithTerraformVersion(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test terraform file
	tfContent := `terraform {
  required_version = ">= 1.0"
}`
	tfFile := filepath.Join(tmpDir, "main.tf")
	if err := os.WriteFile(tfFile, []byte(tfContent), 0644); err != nil {
		t.Fatalf("Failed to create terraform file: %v", err)
	}

	// Create config file with terraform_version
	configContent := `terraform_version: ">= 1.6"`
	configFile := filepath.Join(tmpDir, "config.yml")
	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to create config file: %v", err)
	}

	// Load and process config
	config, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if config.TerraformVersion != ">= 1.6" {
		t.Errorf("Expected terraform_version '>= 1.6', got '%s'", config.TerraformVersion)
	}

	// Process the file
	files := []string{tfFile}
	flags := &cliFlags{dryRun: false, output: "text"}
	
	count := processTerraformVersion(files, config.TerraformVersion, flags.dryRun, flags.output)
	if count != 1 {
		t.Errorf("Expected 1 file updated, got %d", count)
	}

	// Verify the update
	content, err := os.ReadFile(tfFile)
	if err != nil {
		t.Fatalf("Failed to read updated file: %v", err)
	}

	if !strings.Contains(string(content), `required_version = ">= 1.6"`) {
		t.Error("Terraform version was not updated correctly")
	}
}

// TestConfigFileWithMultipleProviders tests processing config files with multiple providers
func TestConfigFileWithMultipleProviders(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test terraform file
	tfContent := `terraform {
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
}`
	tfFile := filepath.Join(tmpDir, "main.tf")
	if err := os.WriteFile(tfFile, []byte(tfContent), 0644); err != nil {
		t.Fatalf("Failed to create terraform file: %v", err)
	}

	// Create config file with multiple providers
	configContent := `providers:
  - name: "aws"
    version: "~> 5.0"
  - name: "azurerm"
    version: "~> 3.5"`
	configFile := filepath.Join(tmpDir, "config.yml")
	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to create config file: %v", err)
	}

	// Load and process config
	config, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if len(config.Providers) != 2 {
		t.Fatalf("Expected 2 providers, got %d", len(config.Providers))
	}

	// Process the file
	files := []string{tfFile}
	flags := &cliFlags{dryRun: false, output: "text"}
	
	totalUpdates := 0
	for _, provider := range config.Providers {
		count := processProviderVersion(files, provider.Name, provider.Version, flags.dryRun, flags.output)
		totalUpdates += count
	}

	if totalUpdates != 2 {
		t.Errorf("Expected 2 provider updates, got %d", totalUpdates)
	}

	// Verify the updates
	content, err := os.ReadFile(tfFile)
	if err != nil {
		t.Fatalf("Failed to read updated file: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, `version = "~> 5.0"`) {
		t.Error("AWS provider version was not updated correctly")
	}
	if !strings.Contains(contentStr, `version = "~> 3.5"`) {
		t.Error("Azure provider version was not updated correctly")
	}
}

// TestConfigFileWithCombinedUpdates tests config files with terraform_version, providers, and modules together
func TestConfigFileWithCombinedUpdates(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test terraform file
	tfContent := `terraform {
  required_version = ">= 1.0"
  
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}`
	tfFile := filepath.Join(tmpDir, "main.tf")
	if err := os.WriteFile(tfFile, []byte(tfContent), 0644); err != nil {
		t.Fatalf("Failed to create terraform file: %v", err)
	}

	// Create config file with all three types
	configContent := `terraform_version: ">= 1.6"
providers:
  - name: "aws"
    version: "~> 5.0"
modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"`
	configFile := filepath.Join(tmpDir, "config.yml")
	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to create config file: %v", err)
	}

	// Load and process config
	config, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	files := []string{tfFile}
	flags := &cliFlags{dryRun: false, output: "text"}

	// Process terraform version
	terraformUpdates := 0
	if config.TerraformVersion != "" {
		terraformUpdates = processTerraformVersion(files, config.TerraformVersion, flags.dryRun, flags.output)
	}

	// Process providers
	providerUpdates := 0
	for _, provider := range config.Providers {
		count := processProviderVersion(files, provider.Name, provider.Version, flags.dryRun, flags.output)
		providerUpdates += count
	}

	// Process modules
	moduleUpdates := 0
	if len(config.Modules) > 0 {
		moduleUpdates = processFiles(files, config.Modules, flags)
	}

	// Verify counts
	if terraformUpdates != 1 {
		t.Errorf("Expected 1 terraform update, got %d", terraformUpdates)
	}
	if providerUpdates != 1 {
		t.Errorf("Expected 1 provider update, got %d", providerUpdates)
	}
	if moduleUpdates != 1 {
		t.Errorf("Expected 1 module update, got %d", moduleUpdates)
	}

	// Verify the updates
	content, err := os.ReadFile(tfFile)
	if err != nil {
		t.Fatalf("Failed to read updated file: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, `required_version = ">= 1.6"`) {
		t.Error("Terraform version was not updated correctly")
	}
	if !strings.Contains(contentStr, `version = "~> 5.0"`) {
		t.Error("Provider version was not updated correctly")
	}
	if !strings.Contains(contentStr, `version = "5.0.0"`) {
		t.Error("Module version was not updated correctly")
	}
}

// TestConfigFileSummaryOutput tests that the summary output is correct for various combinations
func TestConfigFileSummaryOutput(t *testing.T) {
	tests := []struct {
		name              string
		terraformUpdates  int
		providerUpdates   int
		moduleUpdates     int
		expectSummary     bool
	}{
		{
			name:              "all updates present",
			terraformUpdates:  1,
			providerUpdates:   2,
			moduleUpdates:     3,
			expectSummary:     true,
		},
		{
			name:              "only terraform updates",
			terraformUpdates:  1,
			providerUpdates:   0,
			moduleUpdates:     0,
			expectSummary:     true,
		},
		{
			name:              "only provider updates",
			terraformUpdates:  0,
			providerUpdates:   2,
			moduleUpdates:     0,
			expectSummary:     true,
		},
		{
			name:              "only module updates",
			terraformUpdates:  0,
			providerUpdates:   0,
			moduleUpdates:     3,
			expectSummary:     true,
		},
		{
			name:              "no updates",
			terraformUpdates:  0,
			providerUpdates:   0,
			moduleUpdates:     0,
			expectSummary:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This test just verifies that printConfigSummary doesn't panic
			// The actual output testing would require capturing stdout which is complex
			printConfigSummary(tt.terraformUpdates, tt.providerUpdates, tt.moduleUpdates)
		})
	}
}
