package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func auditValue(value string) *string {
	return &value
}

// auditConfig builds a config the way the command does, resolving branch-scoped ignore_modules
// before anything reads them, so tests cannot pass through a path production never takes.
func auditConfig(t *testing.T, branch string, modules ...ModuleUpdate) *Config {
	t.Helper()
	config := &Config{Modules: modules}
	if err := resolveBranchIgnoreModules(config.Modules, branch); err != nil {
		t.Fatalf("resolveBranchIgnoreModules: %v", err)
	}
	return config
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
	config := auditConfig(t, "", auditVPCUpdate, ModuleUpdate{Source: "./modules/network", Version: "1.0.0"})

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

// A run applies a source's entries in YAML order, so each entry meets the value the entries before
// it leave, and a skipped entry leaves that value unchanged.
func TestBuildAudit_JudgesEachEntryAgainstTheValueEarlierEntriesLeave(t *testing.T) {
	moduleFile := writeTestFile(t, t.TempDir(), "main.tf", "module \"vpc\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"4.2.0\"\n}\n")
	vpc := "terraform-aws-modules/vpc/aws"
	config := auditConfig(t, "",
		ModuleUpdate{Source: vpc, Version: "9.9.9", IgnoreModules: []string{"vpc"}},
		ModuleUpdate{Source: vpc, Version: "4.9.0", From: FromVersions{"4.2.0"}},
		ModuleUpdate{Source: vpc, Version: "5.0.0", From: FromVersions{"4.9.0"}},
		ModuleUpdate{Source: vpc, Version: "5.0.0"},
	)

	audit, err := buildAudit([]string{moduleFile}, config)
	if err != nil {
		t.Fatalf("buildAudit: %v", err)
	}

	want := []moduleAuditEntry{
		{File: moduleFile, Name: "vpc", Source: vpc, Actual: auditValue("4.2.0"), Expected: "9.9.9", Skip: &moduleAuditSkip{Filter: "ignore_modules", Values: []string{"vpc"}}},
		{File: moduleFile, Name: "vpc", Source: vpc, Actual: auditValue("4.2.0"), Expected: "4.9.0"},
		{File: moduleFile, Name: "vpc", Source: vpc, Actual: auditValue("4.9.0"), Expected: "5.0.0"},
		{File: moduleFile, Name: "vpc", Source: vpc, Actual: auditValue("5.0.0"), Expected: "5.0.0", Matches: true},
	}
	if got, wantJSON := auditJSON(t, audit.Modules), auditJSON(t, want); got != wantJSON {
		t.Fatalf("modules = %s, want %s", got, wantJSON)
	}
}

// The updater rewrites a block once per entry that reaches it, so the audit must agree about every
// entry of a chained config, not only the first.
func TestBuildAudit_AgreesWithTheUpdaterOnChainedEntries(t *testing.T) {
	moduleFile := writeTestFile(t, t.TempDir(), "main.tf", "module \"vpc\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"~> 3.0\"\n}\n")
	vpc := "terraform-aws-modules/vpc/aws"
	config := auditConfig(t, "",
		ModuleUpdate{Source: vpc, Version: "~> 4.0", From: FromVersions{"~> 3.0"}},
		ModuleUpdate{Source: vpc, Version: "~> 5.0", From: FromVersions{"~> 4.0"}},
		ModuleUpdate{Source: vpc, Version: "~> 6.0", From: FromVersions{"~> 3.0"}},
	)

	audit, err := buildAudit([]string{moduleFile}, config)
	if err != nil {
		t.Fatalf("buildAudit: %v", err)
	}
	var auditChanges []int
	for index, entry := range audit.Modules {
		if entry.Actual != nil && !entry.Matches && entry.Skip == nil {
			auditChanges = append(auditChanges, index)
		}
	}

	var updaterChanges []int
	for index := range config.Modules {
		update := &config.Modules[index]
		_, changedBlocks, err := updateModuleVersionWithCount(moduleFile, update.Source, update.Version,
			update.From, update.IgnoreVersions, update.resolvedIgnoreModules, false, false, false, "text")
		if err != nil {
			t.Fatalf("updateModuleVersionWithCount: %v", err)
		}
		if len(changedBlocks) > 0 {
			updaterChanges = append(updaterChanges, index)
		}
	}

	if !slices.Equal(auditChanges, updaterChanges) || !slices.Equal(updaterChanges, []int{0, 1}) {
		t.Fatalf("audit changes entries %v and updater changes entries %v, want both to be [0 1]", auditChanges, updaterChanges)
	}
}

// The audit and the updater must agree on which versioned blocks an update would change: an
// entry that neither matches nor is skipped is exactly a block the updater rewrites.
func TestBuildAudit_AgreesWithTheUpdaterOnModulesItWouldChange(t *testing.T) {
	moduleFile := writeTestFile(t, t.TempDir(), "main.tf", auditModuleFixture)

	config := auditConfig(t, "", auditVPCUpdate)
	audit, err := buildAudit([]string{moduleFile}, config)
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
	update := &config.Modules[0]
	warnings := captureStderr(t, func() {
		_, changedBlocks, err = updateModuleVersionWithCount(moduleFile, update.Source, update.Version,
			update.From, update.IgnoreVersions, update.resolvedIgnoreModules, false, true, false, "text")
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

// A branch-scoped entry must reach both paths identically. If it did not, the audit would report
// a deliberately excluded module as out of date on the very branch that excludes it.
func TestBuildAudit_AgreesWithTheUpdaterOnBranchScopedIgnoreModules(t *testing.T) {
	const fixture = "module \"shared_vpc\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"4.2.0\"\n}\n"
	const entry = "state/staging/example-thing/shared_vpc"

	tests := []struct {
		name, branch string
		wantSkip     *moduleAuditSkip
		wantChanged  []int
	}{
		{name: "scoped branch skips the module", branch: "state/staging/example-thing", wantSkip: &moduleAuditSkip{Filter: "ignore_modules", Values: []string{entry}}},
		{name: "other branch updates the module", branch: "state/nonproduction/example-thing", wantChanged: []int{0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			moduleFile := writeTestFile(t, t.TempDir(), "main.tf", fixture)
			config := auditConfig(t, tt.branch, ModuleUpdate{Source: "terraform-aws-modules/vpc/aws", Version: "5.0.0", IgnoreModules: []string{entry}})

			audit, err := buildAudit([]string{moduleFile}, config)
			if err != nil {
				t.Fatalf("buildAudit: %v", err)
			}
			want := []moduleAuditEntry{{
				File: moduleFile, Name: "shared_vpc", Source: "terraform-aws-modules/vpc/aws",
				Actual: auditValue("4.2.0"), Expected: "5.0.0", Skip: tt.wantSkip,
			}}
			if got, wantJSON := auditJSON(t, audit.Modules), auditJSON(t, want); got != wantJSON {
				t.Fatalf("modules = %s, want %s", got, wantJSON)
			}

			update := &config.Modules[0]
			_, changedBlocks, err := updateModuleVersionWithCount(moduleFile, update.Source, update.Version,
				update.From, update.IgnoreVersions, update.resolvedIgnoreModules, false, true, false, "text")
			if err != nil {
				t.Fatalf("updateModuleVersionWithCount: %v", err)
			}
			if !slices.Equal(changedBlocks, tt.wantChanged) {
				t.Fatalf("updater changed blocks %v, want %v", changedBlocks, tt.wantChanged)
			}
		})
	}
}

func TestCommandAuditRequiresBranchForScopedIgnoreModules(t *testing.T) {
	dir := t.TempDir()
	moduleFile := writeTestFile(t, dir, "main.tf", "module \"shared_vpc\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n")
	configFile := writeTestFile(t, dir, "versions.yml", "modules:\n  - source: example/module\n    version: 2.0.0\n    ignore_modules:\n      - state/staging/example-thing/shared_vpc\n")
	auditFile := filepath.Join(dir, "audit.json")

	result := runMainCommand(t, []string{"tf-version-bump", "-pattern", moduleFile, "-config", configFile, "-audit-file", auditFile})

	want := "Error: ignore pattern 'state/staging/example-thing/shared_vpc' is scoped to a branch, but -branch is missing or empty; a detached checkout has no current branch\n"
	if result.exitCode != 1 || result.diagnostics != want {
		t.Fatalf("result = %#v, want diagnostic %q and exit 1", result, want)
	}
	if _, err := os.Stat(auditFile); !os.IsNotExist(err) {
		t.Fatalf("audit file stat error = %v, want the audit not to be written", err)
	}
}

// The audit must resolve scoped entries against the -branch it was given, exactly as an update
// does, or the version report would mark a deliberately excluded module as out of date.
func TestCommandAuditResolvesScopedIgnoreModulesAgainstBranch(t *testing.T) {
	const entry = "state/staging/example-thing/shared_vpc"
	dir := t.TempDir()
	moduleFile := writeTestFile(t, dir, "main.tf", "module \"shared_vpc\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n")
	configFile := writeTestFile(t, dir, "versions.yml", "modules:\n  - source: example/module\n    version: 2.0.0\n    ignore_modules:\n      - "+entry+"\n")
	auditFile := filepath.Join(dir, "audit.json")

	result := runMainCommand(t, []string{"tf-version-bump", "-pattern", moduleFile, "-config", configFile, "-branch", "state/staging/example-thing", "-audit-file", auditFile})

	if result.diagnostics != "" || result.exitCode != -1 {
		t.Fatalf("result = %#v, want a successful audit", result)
	}
	var report auditReport
	if err := json.Unmarshal([]byte(readTestFile(t, auditFile)), &report); err != nil {
		t.Fatalf("parse audit file: %v", err)
	}
	want := []moduleAuditEntry{{
		File: moduleFile, Name: "shared_vpc", Source: "example/module",
		Actual: auditValue("1.0.0"), Expected: "2.0.0", Skip: &moduleAuditSkip{Filter: "ignore_modules", Values: []string{entry}},
	}}
	if got, wantJSON := auditJSON(t, report.Modules), auditJSON(t, want); got != wantJSON {
		t.Fatalf("modules = %s, want %s", got, wantJSON)
	}
}
