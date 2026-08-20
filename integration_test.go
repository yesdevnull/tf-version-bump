package main

import (
	"strings"
	"testing"
)

func TestRunCLIModeContinuesAfterFileFailure(t *testing.T) {
	tests := []struct{ name, bad, valid, output, errText, wantHCL string }{
		{"terraform", `terraform {`, "terraform {\n  required_version = \">= 1.0\"\n}\n", "✓ Updated Terraform required_version to '>= 1.5' in ", "1 Terraform version update error(s)", "terraform {\n  required_version = \">= 1.5\"\n}\n"},
		{"provider", `terraform {`, "terraform {\n  required_providers {\n    aws = {\n      source = \"hashicorp/aws\"\n      version = \"~> 4.0\"\n    }\n  }\n}\n", "✓ Updated provider 'aws' to version '~> 5.0' in ", "1 provider update error(s)", "terraform {\n  required_providers {\n    aws = {\n      source  = \"hashicorp/aws\"\n      version = \"~> 5.0\"\n    }\n  }\n}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			bad := writeTestFile(t, dir, "01.tf", tt.bad)
			good := writeTestFile(t, dir, "02.tf", tt.valid)
			flags := &cliFlags{output: "text", pattern: dir + "/*.tf"}
			if tt.name == "terraform" {
				flags.terraformVersion = ">= 1.5"
			} else {
				flags.providerName = "aws"
				flags.toVersion = "~> 5.0"
			}
			stdout, diag, err := captureRunnerOutput(t, func() error { return runCLIMode([]string{bad, good}, flags) })
			parser := "failed to parse HCL: " + bad + ":1,11-12: Unclosed configuration block; There is no closing brace for this block before the end of the file. This may be caused by incorrect brace nesting elsewhere in this file."
			wantDiag := "Error processing " + bad + ": " + parser + "\n"
			wantOut := tt.output + good + "\n\nSuccessfully updated 1 file(s)\n"
			if tt.name == "terraform" {
				wantOut = tt.output + good + "\n\nSuccessfully updated Terraform version in 1 file(s)\n"
			}
			if tt.name == "provider" {
				wantOut = tt.output + good + "\n\nSuccessfully updated 'aws' provider version in 1 file(s)\n"
			}
			if stdout != wantOut || diag != wantDiag || err == nil || err.Error() != tt.errText || readTestFile(t, good) != tt.wantHCL {
				t.Fatalf("stdoutOK=%v diagOK=%v errOK=%v hclOK=%v stdout=%q diag=%q err=%v content=%q", stdout == wantOut, diag == wantDiag, err != nil && err.Error() == tt.errText, readTestFile(t, good) == tt.wantHCL, stdout, diag, err, readTestFile(t, good))
			}
		})
	}
}

func TestRunConfigFileModeAppliesCombinedUpdates(t *testing.T) {
	dir := t.TempDir()
	file := writeTestFile(t, dir, "main.tf", `terraform {
  required_version = ">= 1.0"
  required_providers {
    aws = {
      source = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}
module "x" {
  source = "example/module"
  version = "1.0.0"
}
`)
	cfg := writeTestFile(t, dir, "updates.yml", `terraform_version: ">= 1.5"
providers:
  - name: aws
    version: "~> 5.0"
modules:
  - source: example/module
    version: 2.0.0
`)
	stdout, diag, err := captureRunnerOutput(t, func() error { return runConfigFileMode([]string{file}, &cliFlags{configFile: cfg, output: "text"}) })
	wantPrefix := "✓ Updated Terraform required_version to '>= 1.5' in " + file + "\n✓ Updated provider 'aws' to version '~> 5.0' in " + file + "\n✓ Updated module source 'example/module' to version '2.0.0' in " + file + "\n\n==================================================\nConfig File Update Summary\n==================================================\nTerraform version: 1 file(s) updated\nProviders: 1 update(s) applied\nModules: 1 file(s) updated\n"
	if err != nil || diag != "" || stdout != wantPrefix {
		t.Fatalf("stdout=%q diag=%q err=%v", stdout, diag, err)
	}
	wantHCL := `terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}
module "x" {
  source  = "example/module"
  version = "2.0.0"
}
`
	if readTestFile(t, file) != wantHCL {
		t.Fatalf("final HCL=%q want=%q", readTestFile(t, file), wantHCL)
	}
}

func TestRunConfigFileModeAggregatesMixedFailures(t *testing.T) {
	dir := t.TempDir()
	bad1 := writeTestFile(t, dir, "01.tf", `module "broken" {`)
	bad2 := writeTestFile(t, dir, "02.tf", `module "broken" {`)
	good := writeTestFile(t, dir, "03.tf", `terraform {
  required_version = ">= 1.0"
  required_providers {
    aws = {
      source = "hashicorp/aws"
      version = "~> 4.0"
    }
    azurerm = {
      source = "hashicorp/azurerm"
      version = "~> 3.0"
    }
  }
}
module "vpc" {
  source = "terraform-aws-modules/vpc/aws"
  version = "3.0.0"
}
module "ec2" {
  source = "terraform-aws-modules/ec2-instance/aws"
  version = "4.0.0"
}
`)
	cfg := writeTestFile(t, dir, "updates.yml", `terraform_version: ">= 1.6"
providers:
  - name: aws
    version: "~> 5.0"
  - name: azurerm
    version: "~> 4.0"
modules:
  - source: terraform-aws-modules/vpc/aws
    version: 5.0.0
  - source: terraform-aws-modules/ec2-instance/aws
    version: 6.0.0
`)
	stdout, diag, err := captureRunnerOutput(t, func() error {
		return runConfigFileMode([]string{bad1, bad2, good}, &cliFlags{configFile: cfg, output: "text"})
	})
	wantPrefix := "✓ Updated Terraform required_version to '>= 1.6' in " + good + "\n✓ Updated provider 'aws' to version '~> 5.0' in " + good + "\n✓ Updated provider 'azurerm' to version '~> 4.0' in " + good + "\n✓ Updated module source 'terraform-aws-modules/vpc/aws' to version '5.0.0' in " + good + "\n✓ Updated module source 'terraform-aws-modules/ec2-instance/aws' to version '6.0.0' in " + good + "\n\n==================================================\nConfig File Update Summary\n==================================================\nTerraform version: 1 file(s) updated\nProviders: 2 update(s) applied\nModules: 2 file(s) updated\n"
	if err == nil || err.Error() != "10 update error(s)" || stdout != wantPrefix {
		t.Fatalf("stdout=%q diag=%q err=%v content=%q", stdout, diag, err, readTestFile(t, good))
	}
	parser := "failed to parse HCL: " + bad1 + ":1,17-18: Unclosed configuration block; There is no closing brace for this block before the end of the file. This may be caused by incorrect brace nesting elsewhere in this file.\n"
	wantDiag := "Error processing " + bad1 + ": " + parser + "Error processing " + bad2 + ": " + strings.Replace(parser, bad1, bad2, 1) + "Error processing " + bad1 + ": " + parser + "Error processing " + bad2 + ": " + strings.Replace(parser, bad1, bad2, 1) + "Error processing " + bad1 + ": " + parser + "Error processing " + bad2 + ": " + strings.Replace(parser, bad1, bad2, 1) + "Error processing " + bad1 + ": " + parser + "Error processing " + bad1 + ": " + parser + "Error processing " + bad2 + ": " + strings.Replace(parser, bad1, bad2, 1) + "Error processing " + bad2 + ": " + strings.Replace(parser, bad1, bad2, 1)
	if diag != wantDiag {
		t.Fatalf("diagnostics=%q want=%q", diag, wantDiag)
	}
	wantHCL := `terraform {
  required_version = ">= 1.6"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 4.0"
    }
  }
}
module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.0.0"
}
module "ec2" {
  source  = "terraform-aws-modules/ec2-instance/aws"
  version = "6.0.0"
}
`
	if readTestFile(t, good) != wantHCL {
		t.Fatalf("final HCL=%q want=%q", readTestFile(t, good), wantHCL)
	}
}
