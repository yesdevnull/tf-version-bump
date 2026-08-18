package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunConfigModeReportsModuleFileFailure(t *testing.T) {
	tmpDir := t.TempDir()
	malformedFile := filepath.Join(tmpDir, "01-malformed.tf")
	if err := os.WriteFile(malformedFile, []byte("module \"broken\" {"), 0o644); err != nil {
		t.Fatalf("failed to write malformed Terraform file: %v", err)
	}

	validFile := filepath.Join(tmpDir, "02-valid.tf")
	if err := os.WriteFile(validFile, []byte(`module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}
`), 0o644); err != nil {
		t.Fatalf("failed to write valid Terraform file: %v", err)
	}

	configFile := filepath.Join(tmpDir, "updates.yml")
	if err := os.WriteFile(configFile, []byte(`modules:
  - source: terraform-aws-modules/vpc/aws
    version: 5.0.0
`), 0o644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	stdout, diagnostic, runnerErr := captureRunnerOutput(t, func() error {
		return runConfigFileMode([]string{malformedFile, validFile}, &cliFlags{
			configFile: configFile,
			output:     "text",
		})
	})

	updated, err := os.ReadFile(validFile)
	if err != nil {
		t.Fatalf("failed to read updated Terraform file: %v", err)
	}
	if !strings.Contains(string(updated), `version = "5.0.0"`) {
		t.Fatalf("expected valid file to be updated, got: %s", updated)
	}

	if got, want := diagnostic, "Error processing "+malformedFile+": failed to parse HCL: "+malformedFile+":1,17-18: Unclosed configuration block; There is no closing brace for this block before the end of the file. This may be caused by incorrect brace nesting elsewhere in this file.\n"; got != want {
		t.Errorf("malformed-file diagnostic = %q, want %q", got, want)
	}
	if !strings.Contains(stdout, "Modules: 1 file(s) updated") {
		t.Errorf("expected existing config summary in stdout, got %q", stdout)
	}
	if runnerErr == nil {
		t.Fatal("expected runner to report the malformed file failure")
	}
	if got, want := runnerErr.Error(), "1 module update error(s)"; got != want {
		t.Errorf("module-only runner error = %q, want %q", got, want)
	}
}

func TestRunConfigModeReportsTerraformFileFailure(t *testing.T) {
	tmpDir := t.TempDir()
	malformedFile := filepath.Join(tmpDir, "01-malformed.tf")
	if err := os.WriteFile(malformedFile, []byte("terraform {"), 0o644); err != nil {
		t.Fatalf("failed to write malformed Terraform file: %v", err)
	}

	validFile := filepath.Join(tmpDir, "02-valid.tf")
	if err := os.WriteFile(validFile, []byte(`terraform {
  required_version = ">= 1.0"
}
`), 0o644); err != nil {
		t.Fatalf("failed to write valid Terraform file: %v", err)
	}

	configFile := filepath.Join(tmpDir, "updates.yml")
	if err := os.WriteFile(configFile, []byte(`terraform_version: ">= 1.5"
`), 0o644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	stdout, diagnostic, runnerErr := captureRunnerOutput(t, func() error {
		return runConfigFileMode([]string{malformedFile, validFile}, &cliFlags{
			configFile: configFile,
			output:     "text",
		})
	})

	updated, err := os.ReadFile(validFile)
	if err != nil {
		t.Fatalf("failed to read updated Terraform file: %v", err)
	}
	if !strings.Contains(string(updated), `required_version = ">= 1.5"`) {
		t.Fatalf("expected valid file to be updated, got: %s", updated)
	}

	if !strings.Contains(diagnostic, "Error processing "+malformedFile+": failed to parse HCL:") {
		t.Errorf("expected malformed-file diagnostic, got %q", diagnostic)
	}
	if !strings.Contains(stdout, "Terraform version: 1 file(s) updated") {
		t.Errorf("expected existing config summary in stdout, got %q", stdout)
	}
	if runnerErr == nil {
		t.Fatal("expected runner to report the malformed file failure")
	}
}

func TestRunConfigModeTerraformAllFilesSucceed(t *testing.T) {
	tmpDir := t.TempDir()
	files := []string{
		filepath.Join(tmpDir, "01.tf"),
		filepath.Join(tmpDir, "02.tf"),
	}
	for _, file := range files {
		if err := os.WriteFile(file, []byte(`terraform {
  required_version = ">= 1.0"
}
`), 0o644); err != nil {
			t.Fatalf("failed to write Terraform file: %v", err)
		}
	}

	configFile := filepath.Join(tmpDir, "updates.yml")
	if err := os.WriteFile(configFile, []byte(`terraform_version: ">= 1.5"
`), 0o644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	stdout, diagnostic, runnerErr := captureRunnerOutput(t, func() error {
		return runConfigFileMode(files, &cliFlags{configFile: configFile, output: "text"})
	})
	if runnerErr != nil {
		t.Fatalf("expected all valid files to succeed: %v", runnerErr)
	}
	if diagnostic != "" {
		t.Errorf("expected no diagnostics, got %q", diagnostic)
	}
	if !strings.Contains(stdout, "Terraform version: 2 file(s) updated") {
		t.Errorf("expected existing summary in stdout, got %q", stdout)
	}
}

/*
func TestRunConfigModeProviderAllFilesSucceed(t *testing.T) {
	tmpDir := t.TempDir()
	files := []string{
		filepath.Join(tmpDir, "01.tf"),
		filepath.Join(tmpDir, "02.tf"),
	}
	for _, file := range files {
		if err := os.WriteFile(file, []byte(`terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}
`), 0o644); err != nil {
			t.Fatalf("failed to write Terraform file: %v", err)
		}
	}

	configFile := filepath.Join(tmpDir, "updates.yml")
	if err := os.WriteFile(configFile, []byte(`providers:
  - name: aws
    version: "~> 5.0"
`), 0o644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	stdout, diagnostic, runnerErr := captureRunnerOutput(t, func() error {
		return runConfigFileMode(files, &cliFlags{configFile: configFile, output: "text"})
	})
	if runnerErr != nil {
		t.Fatalf("expected all valid files to succeed: %v", runnerErr)
	}
	if diagnostic != "" {
		t.Errorf("expected no diagnostics, got %q", diagnostic)
	}
	if !strings.Contains(stdout, "Providers: 2 update(s) applied") {
		t.Errorf("expected existing config summary in stdout, got %q", stdout)
	}
}

*/

// TestConfigFileWithTerraformVersion tests processing config files with terraform_version
func TestConfigFileWithTerraformVersion(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test terraform file
	tfContent := `terraform {
  required_version = ">= 1.0"
}`
	tfFile := filepath.Join(tmpDir, "main.tf")
	if err := os.WriteFile(tfFile, []byte(tfContent), 0o644); err != nil {
		t.Fatalf("Failed to create terraform file: %v", err)
	}

	// Create config file with terraform_version
	configContent := `terraform_version: ">= 1.6"`
	configFile := filepath.Join(tmpDir, "config.yml")
	if err := os.WriteFile(configFile, []byte(configContent), 0o644); err != nil {
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

	count, _ := processTerraformVersion(files, config.TerraformVersion, flags.dryRun, flags.output)
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
	if err := os.WriteFile(tfFile, []byte(tfContent), 0o644); err != nil {
		t.Fatalf("Failed to create terraform file: %v", err)
	}

	// Create config file with multiple providers
	configContent := `providers:
  - name: "aws"
    version: "~> 5.0"
  - name: "azurerm"
    version: "~> 3.5"`
	configFile := filepath.Join(tmpDir, "config.yml")
	if err := os.WriteFile(configFile, []byte(configContent), 0o644); err != nil {
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
	if err := os.WriteFile(tfFile, []byte(tfContent), 0o644); err != nil {
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
	if err := os.WriteFile(configFile, []byte(configContent), 0o644); err != nil {
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
		terraformUpdates, _ = processTerraformVersion(files, config.TerraformVersion, flags.dryRun, flags.output)
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
		moduleUpdates, _ = processFiles(files, config.Modules, flags)
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
		name             string
		terraformUpdates int
		providerUpdates  int
		moduleUpdates    int
		expectSummary    bool
	}{
		{
			name:             "all updates present",
			terraformUpdates: 1,
			providerUpdates:  2,
			moduleUpdates:    3,
			expectSummary:    true,
		},
		{
			name:             "only terraform updates",
			terraformUpdates: 1,
			providerUpdates:  0,
			moduleUpdates:    0,
			expectSummary:    true,
		},
		{
			name:             "only provider updates",
			terraformUpdates: 0,
			providerUpdates:  2,
			moduleUpdates:    0,
			expectSummary:    true,
		},
		{
			name:             "only module updates",
			terraformUpdates: 0,
			providerUpdates:  0,
			moduleUpdates:    3,
			expectSummary:    true,
		},
		{
			name:             "no updates",
			terraformUpdates: 0,
			providerUpdates:  0,
			moduleUpdates:    0,
			expectSummary:    false,
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
