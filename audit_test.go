package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func auditValue(value string) *string {
	return &value
}

// auditJSON renders audit values the way -audit-file writes them, so failures show readable
// documents instead of pointer addresses.
func auditJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal audit value: %v", err)
	}
	return string(data)
}

func TestBuildAudit_RecordsTerraformRequiredVersions(t *testing.T) {
	versions := writeTestFile(t, t.TempDir(), "versions.tf", `terraform {
  required_version = ">= 1.10"
}

terraform {
  required_version = "~>1.10"
}

terraform {
  required_version = var.minimum_terraform
}

terraform {
  backend "local" {}
}
`)

	audit, err := buildAudit([]string{versions}, &Config{TerraformVersion: ">= 1.10"})
	if err != nil {
		t.Fatalf("buildAudit: %v", err)
	}

	want := []terraformAuditEntry{
		{File: versions, Actual: auditValue(">= 1.10"), Expected: ">= 1.10", Matches: true},
		{File: versions, Actual: auditValue("~>1.10"), Expected: ">= 1.10", Matches: false},
		{File: versions, Actual: auditValue("var.minimum_terraform"), Expected: ">= 1.10", Matches: false},
		{File: versions, Actual: nil, Expected: ">= 1.10", Matches: false},
	}
	if got, wantJSON := auditJSON(t, audit.Terraform), auditJSON(t, want); got != wantJSON {
		t.Fatalf("terraform = %s, want %s", got, wantJSON)
	}
}

func TestBuildAudit_OmitsTerraformVersionsTheConfigDoesNotSet(t *testing.T) {
	versions := writeTestFile(t, t.TempDir(), "versions.tf", "terraform {\n  required_version = \">= 1.10\"\n}\n")

	audit, err := buildAudit([]string{versions}, &Config{})
	if err != nil {
		t.Fatalf("buildAudit: %v", err)
	}

	if got := auditJSON(t, audit); got != `{"schema_version":1,"terraform":[],"providers":[],"modules":[]}` {
		t.Fatalf("audit = %s, want empty sections", got)
	}
}

func TestBuildAudit_RecordsProvidersInEitherSyntax(t *testing.T) {
	versions := writeTestFile(t, t.TempDir(), "versions.tf", `terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    google = {
      source = "hashicorp/google"
    }
    random = {
      source  = "hashicorp/random"
      version = "3.6.0"
    }
  }
}

terraform {
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
    google {
      source = "hashicorp/google"
    }
  }
}
`)
	config := &Config{Providers: []ProviderUpdate{
		{Name: "aws", Version: "~> 6.0"},
		{Name: "google", Version: "~> 5.0"},
		{Name: "azurerm", Version: "~> 4.0"},
	}}

	audit, err := buildAudit([]string{versions}, config)
	if err != nil {
		t.Fatalf("buildAudit: %v", err)
	}

	want := []providerAuditEntry{
		{File: versions, Name: "aws", Actual: auditValue("~> 5.0"), Expected: "~> 6.0", Matches: false},
		{File: versions, Name: "google", Actual: nil, Expected: "~> 5.0", Matches: false},
		{File: versions, Name: "aws", Actual: auditValue("~> 6.0"), Expected: "~> 6.0", Matches: true},
		{File: versions, Name: "google", Actual: nil, Expected: "~> 5.0", Matches: false},
	}
	if got, wantJSON := auditJSON(t, audit.Providers), auditJSON(t, want); got != wantJSON {
		t.Fatalf("providers = %s, want %s", got, wantJSON)
	}
}

func TestBuildAudit_ReportsAnUnreadableFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.tf")

	audit, err := buildAudit([]string{missing}, &Config{})

	if audit != nil || err == nil || !strings.HasPrefix(err.Error(), "Error auditing "+missing+": failed to read file: ") {
		t.Fatalf("audit = %v, err = %v; want no audit and a read error naming %s", audit, err, missing)
	}
}

const auditModuleFixture = `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "4.2.0"
}

module "legacy_vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.19.0"
}

module "pinned" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "4.9.0"
}

module "old" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "4.0.0"
}

module "current" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.0.0"
}

module "unversioned" {
  source = "terraform-aws-modules/vpc/aws"
}

module "network" {
  source = "./modules/network"
}

module "bucket" {
  source  = "terraform-aws-modules/s3-bucket/aws"
  version = "4.0.0"
}
`

var auditVPCUpdate = ModuleUpdate{
	Source:         "terraform-aws-modules/vpc/aws",
	Version:        "5.0.0",
	From:           FromVersions{"4.2.0"},
	IgnoreVersions: FromVersions{"4.9.0"},
	IgnoreModules:  []string{"legacy_*"},
}

func TestBuildAudit_RecordsModulesInTheUpdatersFilterPrecedence(t *testing.T) {
	moduleFile := writeTestFile(t, t.TempDir(), "main.tf", auditModuleFixture)
	config := &Config{Modules: []ModuleUpdate{auditVPCUpdate, {Source: "./modules/network", Version: "1.0.0"}}}

	audit, err := buildAudit([]string{moduleFile}, config)
	if err != nil {
		t.Fatalf("buildAudit: %v", err)
	}

	vpc := "terraform-aws-modules/vpc/aws"
	from := &moduleAuditSkip{Filter: "from", Values: []string{"4.2.0"}}
	want := []moduleAuditEntry{
		{File: moduleFile, Name: "vpc", Source: vpc, Actual: auditValue("4.2.0"), Expected: "5.0.0"},
		{File: moduleFile, Name: "legacy_vpc", Source: vpc, Actual: auditValue("3.19.0"), Expected: "5.0.0", Skip: &moduleAuditSkip{Filter: "ignore_modules", Values: []string{"legacy_*"}}},
		{File: moduleFile, Name: "pinned", Source: vpc, Actual: auditValue("4.9.0"), Expected: "5.0.0", Skip: &moduleAuditSkip{Filter: "ignore_versions", Values: []string{"4.9.0"}}},
		{File: moduleFile, Name: "old", Source: vpc, Actual: auditValue("4.0.0"), Expected: "5.0.0", Skip: from},
		{File: moduleFile, Name: "current", Source: vpc, Actual: auditValue("5.0.0"), Expected: "5.0.0", Matches: true, Skip: from},
		{File: moduleFile, Name: "unversioned", Source: vpc, Actual: nil, Expected: "5.0.0"},
		{File: moduleFile, Name: "network", Source: "./modules/network", Actual: nil, Expected: "1.0.0", Skip: &moduleAuditSkip{Filter: "local_source", Values: []string{}}},
	}
	if got, wantJSON := auditJSON(t, audit.Modules), auditJSON(t, want); got != wantJSON {
		t.Fatalf("modules = %s, want %s", got, wantJSON)
	}
}

func TestBuildAudit_RecordsABlockOncePerMatchingConfigEntry(t *testing.T) {
	moduleFile := writeTestFile(t, t.TempDir(), "main.tf", "module \"vpc\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"4.2.0\"\n}\n")
	config := &Config{Modules: []ModuleUpdate{
		{Source: "terraform-aws-modules/vpc/aws", Version: "4.9.0", From: FromVersions{"4.2.0"}},
		{Source: "terraform-aws-modules/vpc/aws", Version: "5.0.0", From: FromVersions{"4.9.0"}},
	}}

	audit, err := buildAudit([]string{moduleFile}, config)
	if err != nil {
		t.Fatalf("buildAudit: %v", err)
	}

	vpc := "terraform-aws-modules/vpc/aws"
	want := []moduleAuditEntry{
		{File: moduleFile, Name: "vpc", Source: vpc, Actual: auditValue("4.2.0"), Expected: "4.9.0"},
		{File: moduleFile, Name: "vpc", Source: vpc, Actual: auditValue("4.2.0"), Expected: "5.0.0", Skip: &moduleAuditSkip{Filter: "from", Values: []string{"4.9.0"}}},
	}
	if got, wantJSON := auditJSON(t, audit.Modules), auditJSON(t, want); got != wantJSON {
		t.Fatalf("modules = %s, want %s", got, wantJSON)
	}
}

// The audit and the updater must agree on which versioned blocks an update would change: an
// entry that neither matches nor is skipped is exactly a block the updater rewrites.
func TestBuildAudit_AgreesWithTheUpdaterOnModulesItWouldChange(t *testing.T) {
	moduleFile := writeTestFile(t, t.TempDir(), "main.tf", auditModuleFixture)

	audit, err := buildAudit([]string{moduleFile}, &Config{Modules: []ModuleUpdate{auditVPCUpdate}})
	if err != nil {
		t.Fatalf("buildAudit: %v", err)
	}
	var auditChanges []string
	for _, entry := range audit.Modules {
		if entry.Actual != nil && !entry.Matches && entry.Skip == nil {
			auditChanges = append(auditChanges, entry.Name)
		}
	}

	var changedBlocks []int
	warnings := captureStderr(t, func() {
		_, changedBlocks, err = updateModuleVersionWithCount(moduleFile, auditVPCUpdate.Source, auditVPCUpdate.Version,
			auditVPCUpdate.From, auditVPCUpdate.IgnoreVersions, auditVPCUpdate.IgnoreModules, false, true, false, "text")
	})
	if err != nil {
		t.Fatalf("updateModuleVersionWithCount: %v", err)
	}
	wantWarning := "Warning: Module 'unversioned' in " + moduleFile + " (source: 'terraform-aws-modules/vpc/aws') has no version attribute, skipping\n"
	if warnings != wantWarning {
		t.Fatalf("updater warnings = %q, want %q", warnings, wantWarning)
	}

	if len(auditChanges) != 1 || auditChanges[0] != "vpc" || len(changedBlocks) != 1 || changedBlocks[0] != 0 {
		t.Fatalf("audit changes %v and updater changed blocks %v, want only the first block, vpc", auditChanges, changedBlocks)
	}
}
