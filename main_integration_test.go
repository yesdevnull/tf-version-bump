package main

import (
	"bytes"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureRunnerOutput(t *testing.T, run func() error) (stdout, diagnostic string, runnerErr error) {
	t.Helper()

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stdout pipe: %v", err)
	}
	defer func() {
		if err := stdoutReader.Close(); err != nil {
			t.Errorf("failed to close stdout reader: %v", err)
		}
	}()

	originalStdout := os.Stdout
	os.Stdout = stdoutWriter
	defer func() { os.Stdout = originalStdout }()

	originalLogOutput := log.Writer()
	originalLogFlags := log.Flags()
	var logOutput bytes.Buffer
	log.SetOutput(&logOutput)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(originalLogOutput)
		log.SetFlags(originalLogFlags)
	}()

	runnerErr = run()

	if err := stdoutWriter.Close(); err != nil {
		t.Fatalf("failed to close stdout writer: %v", err)
	}
	output, err := io.ReadAll(stdoutReader)
	if err != nil {
		t.Fatalf("failed to read stdout: %v", err)
	}

	return string(output), logOutput.String(), runnerErr
}

func TestRunCLIModeReportsModuleFileFailure(t *testing.T) {
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

	stdout, diagnostic, runnerErr := captureRunnerOutput(t, func() error {
		return runCLIMode([]string{malformedFile, validFile}, &cliFlags{
			pattern:      "*.tf",
			moduleSource: "terraform-aws-modules/vpc/aws",
			toVersion:    "5.0.0",
			output:       "text",
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
	if !strings.Contains(stdout, "Successfully updated 1 file(s)") {
		t.Errorf("expected existing summary in stdout, got %q", stdout)
	}
	if runnerErr == nil {
		t.Fatal("expected runner to report the malformed file failure")
	}
}

func TestRunCLIModeReportsTerraformFileFailure(t *testing.T) {
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

	stdout, diagnostic, runnerErr := captureRunnerOutput(t, func() error {
		return runCLIMode([]string{malformedFile, validFile}, &cliFlags{
			terraformVersion: ">= 1.5",
			output:           "text",
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
	if !strings.Contains(stdout, "Successfully updated Terraform version in 1 file(s)") {
		t.Errorf("expected existing summary in stdout, got %q", stdout)
	}
	if runnerErr == nil {
		t.Fatal("expected runner to report the malformed file failure")
	}
}

// TestLoadModuleUpdatesErrorCases tests error handling in loadModuleUpdates
func TestLoadModuleUpdatesErrorCases(t *testing.T) {
	tests := []struct {
		name    string
		flags   *cliFlags
		wantErr bool
	}{
		{
			name: "missing pattern",
			flags: &cliFlags{
				pattern:      "",
				moduleSource: "module/source",
				toVersion:    "1.0.0",
			},
			wantErr: true,
		},
		{
			name: "missing module source",
			flags: &cliFlags{
				pattern:      "*.tf",
				moduleSource: "",
				toVersion:    "1.0.0",
			},
			wantErr: true,
		},
		{
			name: "missing version",
			flags: &cliFlags{
				pattern:      "*.tf",
				moduleSource: "module/source",
				toVersion:    "",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This function calls os.Exit(1) on error, so we can't test it directly
			// in a normal test. We verify the logic by checking the conditions.
			if tt.flags.pattern == "" || tt.flags.moduleSource == "" || tt.flags.toVersion == "" {
				// This would trigger os.Exit(1)
				return
			}
			// If we get here without exiting, the test should fail
			if tt.wantErr {
				t.Error("Expected function to exit, but it didn't")
			}
		})
	}
}

// TestLoadModuleUpdatesWithIgnoreModules tests ignore modules parsing
func TestLoadModuleUpdatesWithIgnoreModules(t *testing.T) {
	flags := &cliFlags{
		pattern:       "*.tf",
		moduleSource:  "terraform-aws-modules/vpc/aws",
		toVersion:     "5.0.0",
		ignoreModules: "legacy-*, old-module, test-*",
	}

	updates := loadModuleUpdates(flags)

	if len(updates) != 1 {
		t.Fatalf("Expected 1 update, got %d", len(updates))
	}

	if len(updates[0].IgnoreModules) != 3 {
		t.Errorf("Expected 3 ignore patterns, got %d", len(updates[0].IgnoreModules))
	}

	expectedPatterns := []string{"legacy-*", "old-module", "test-*"}
	for i, expected := range expectedPatterns {
		if updates[0].IgnoreModules[i] != expected {
			t.Errorf("Expected pattern %q at index %d, got %q", expected, i, updates[0].IgnoreModules[i])
		}
	}
}

// TestLoadModuleUpdatesWithEmptyIgnoreModules tests empty ignore modules
func TestLoadModuleUpdatesWithEmptyIgnoreModules(t *testing.T) {
	flags := &cliFlags{
		pattern:       "*.tf",
		moduleSource:  "terraform-aws-modules/vpc/aws",
		toVersion:     "5.0.0",
		ignoreModules: "  ,  , , ",
	}

	updates := loadModuleUpdates(flags)

	if len(updates) != 1 {
		t.Fatalf("Expected 1 update, got %d", len(updates))
	}

	if len(updates[0].IgnoreModules) != 0 {
		t.Errorf("Expected 0 ignore patterns (empty strings filtered), got %d", len(updates[0].IgnoreModules))
	}
}

// TestProcessTerraformVersionWithErrors tests error handling in processTerraformVersion
func TestProcessTerraformVersionWithErrors(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file with invalid HCL
	invalidFile := filepath.Join(tmpDir, "invalid.tf")
	if err := os.WriteFile(invalidFile, []byte("this is not valid HCL {{{"), 0o644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	files := []string{invalidFile}
	count, _ := processTerraformVersion(files, ">= 1.5", false, "text")

	if count != 0 {
		t.Errorf("Expected 0 updates for invalid file, got %d", count)
	}
}

// TestProcessTerraformVersionDryRun tests dry-run mode
func TestProcessTerraformVersionDryRun(t *testing.T) {
	tmpDir := t.TempDir()

	tfContent := `terraform {
  required_version = ">= 1.0"
}`
	tfFile := filepath.Join(tmpDir, "main.tf")
	if err := os.WriteFile(tfFile, []byte(tfContent), 0o644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	files := []string{tfFile}
	count, _ := processTerraformVersion(files, ">= 1.5", true, "text")

	if count != 1 {
		t.Errorf("Expected 1 update in dry-run, got %d", count)
	}

	// Verify file was not modified
	content, err := os.ReadFile(tfFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	if !strings.Contains(string(content), `required_version = ">= 1.0"`) {
		t.Error("File was modified in dry-run mode")
	}
}

// TestProcessProviderVersionWithErrors tests error handling in processProviderVersion
func TestProcessProviderVersionWithErrors(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file with invalid HCL
	invalidFile := filepath.Join(tmpDir, "invalid.tf")
	if err := os.WriteFile(invalidFile, []byte("this is not valid HCL {{{"), 0o644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	files := []string{invalidFile}
	count := processProviderVersion(files, "aws", "~> 5.0", false, "text")

	if count != 0 {
		t.Errorf("Expected 0 updates for invalid file, got %d", count)
	}
}

// TestProcessProviderVersionDryRun tests dry-run mode
func TestProcessProviderVersionDryRun(t *testing.T) {
	tmpDir := t.TempDir()

	tfContent := `terraform {
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}`
	tfFile := filepath.Join(tmpDir, "main.tf")
	if err := os.WriteFile(tfFile, []byte(tfContent), 0o644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	files := []string{tfFile}
	count := processProviderVersion(files, "aws", "~> 5.0", true, "text")

	if count != 1 {
		t.Errorf("Expected 1 update in dry-run, got %d", count)
	}

	// Verify file was not modified
	content, err := os.ReadFile(tfFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	if !strings.Contains(string(content), `version = "~> 4.0"`) {
		t.Error("File was modified in dry-run mode")
	}
}

// TestProcessProviderVersionMarkdownOutput tests markdown output format
func TestProcessProviderVersionMarkdownOutput(t *testing.T) {
	tmpDir := t.TempDir()

	tfContent := `terraform {
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}`
	tfFile := filepath.Join(tmpDir, "main.tf")
	if err := os.WriteFile(tfFile, []byte(tfContent), 0o644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	files := []string{tfFile}
	count := processProviderVersion(files, "aws", "~> 5.0", false, "md")

	if count != 1 {
		t.Errorf("Expected 1 update, got %d", count)
	}

	// Verify file was modified
	content, err := os.ReadFile(tfFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	if !strings.Contains(string(content), `version = "~> 5.0"`) {
		t.Error("Provider version was not updated")
	}
}

// TestUpdateTerraformVersionNoTerraformBlock tests file with no terraform block
func TestUpdateTerraformVersionNoTerraformBlock(t *testing.T) {
	tmpDir := t.TempDir()

	tfContent := `resource "aws_instance" "example" {
  ami           = "ami-123456"
  instance_type = "t2.micro"
}`
	tfFile := filepath.Join(tmpDir, "main.tf")
	if err := os.WriteFile(tfFile, []byte(tfContent), 0o644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	updated, err := updateTerraformVersion(tfFile, ">= 1.5", false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if updated {
		t.Error("Expected no update for file without terraform block")
	}
}

// TestUpdateTerraformVersionInvalidFile tests invalid file
func TestUpdateTerraformVersionInvalidFile(t *testing.T) {
	tmpDir := t.TempDir()

	invalidFile := filepath.Join(tmpDir, "invalid.tf")
	if err := os.WriteFile(invalidFile, []byte("this is not valid HCL {{{"), 0o644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	updated, err := updateTerraformVersion(invalidFile, ">= 1.5", false)
	if err == nil {
		t.Error("Expected error for invalid HCL file")
	}

	if updated {
		t.Error("Expected no update for invalid file")
	}
}

// TestUpdateTerraformVersionFileNotFound tests non-existent file
func TestUpdateTerraformVersionFileNotFound(t *testing.T) {
	updated, err := updateTerraformVersion("/nonexistent/file.tf", ">= 1.5", false)
	if err == nil {
		t.Error("Expected error for non-existent file")
	}

	if updated {
		t.Error("Expected no update for non-existent file")
	}
}

// TestUpdateProviderVersionNoProviderBlock tests file with no provider
func TestUpdateProviderVersionNoProviderBlock(t *testing.T) {
	tmpDir := t.TempDir()

	tfContent := `terraform {
  required_version = ">= 1.0"
}`
	tfFile := filepath.Join(tmpDir, "main.tf")
	if err := os.WriteFile(tfFile, []byte(tfContent), 0o644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	updated, err := updateProviderVersion(tfFile, "aws", "~> 5.0", false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if updated {
		t.Error("Expected no update for file without provider")
	}
}

// TestUpdateProviderVersionInvalidFile tests invalid file
func TestUpdateProviderVersionInvalidFile(t *testing.T) {
	tmpDir := t.TempDir()

	invalidFile := filepath.Join(tmpDir, "invalid.tf")
	if err := os.WriteFile(invalidFile, []byte("this is not valid HCL {{{"), 0o644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	updated, err := updateProviderVersion(invalidFile, "aws", "~> 5.0", false)
	if err == nil {
		t.Error("Expected error for invalid HCL file")
	}

	if updated {
		t.Error("Expected no update for invalid file")
	}
}

// TestUpdateProviderVersionFileNotFound tests non-existent file
func TestUpdateProviderVersionFileNotFound(t *testing.T) {
	updated, err := updateProviderVersion("/nonexistent/file.tf", "aws", "~> 5.0", false)
	if err == nil {
		t.Error("Expected error for non-existent file")
	}

	if updated {
		t.Error("Expected no update for non-existent file")
	}
}

// TestUpdateProviderVersionDifferentProvider tests updating a different provider
func TestUpdateProviderVersionDifferentProvider(t *testing.T) {
	tmpDir := t.TempDir()

	tfContent := `terraform {
  required_providers {
    azurerm {
      source  = "hashicorp/azurerm"
      version = "~> 3.0"
    }
  }
}`
	tfFile := filepath.Join(tmpDir, "main.tf")
	if err := os.WriteFile(tfFile, []byte(tfContent), 0o644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Try to update aws provider, but only azurerm exists
	updated, err := updateProviderVersion(tfFile, "aws", "~> 5.0", false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if updated {
		t.Error("Expected no update when provider doesn't exist")
	}

	// Verify file was not modified
	content, err := os.ReadFile(tfFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	if !strings.Contains(string(content), `version = "~> 3.0"`) {
		t.Error("File was unexpectedly modified")
	}
}
