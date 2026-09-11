# State-branch Version Report Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a read-only `-audit-file` CLI flag, then a copyable GitHub Actions workflow that uses it to report every state branch's versions against its policy config twice: in an existing report's exact legacy format and as an improved report.

**Architecture:** Part 1 adds `audit.go`, which parses each selected file once with `hclwrite` and records every configured Terraform, provider and module version value beside its expected value, reusing the updater's comparison and filter helpers; `main.go` gains the flag and shares its temporary-file JSON writer between `-report-file` and `-audit-file`. Part 2 adds `report-state-branches.sh` (`collect`, `report`, `legacy`), which audits each discovered branch in a detached worktree and formats one `records.json` into both reports with jq, and a standalone workflow that runs it once per policy. A release sits between the parts.

**Tech Stack:** Go (flat `main` package, `hclwrite`/`hclsyntax`), Bash, jq 1.6+, Git worktrees, GitHub Actions, yq (harness only).

**Spec:** `docs/superpowers/specs/2026-09-11-state-branch-version-report-design.md`

## Global Constraints

- Go floor from `go.mod` (1.25); CI pins 1.26.8. golangci-lint v2.12 with `.golangci.yml`: `gocyclo` min-complexity 15, `gocritic` diagnostic/style/performance, `unparam`, `misspell`, and test files linted.
- Coverage stays at or above 90% (`go test -race -coverprofile=coverage.out -covermode=atomic ./...`).
- All Go code stays in the flat `main` package. Never string-manipulate HCL; read values through `hclwrite`/`hclsyntax` helpers that already exist (`attributeStringValue`, `attributeHasStringValue`, `expressionHasStringValue`, `providerAttributeObject`, `providerObjectItemKey`, `moduleSourceValue`, `moduleBlockName`, `isLocalModule`, `shouldIgnoreModule`, `containsVersion`, `trimQuotes`).
- Capitalised user-facing `fmt.Errorf` strings carry `//nolint:staticcheck // User-facing CLI diagnostic.`, as the existing ones do.
- Audit schema version is `1`; `-report-file` stays schema version `2` with byte-identical output and diagnostics.
- shellcheck pinned at 0.11.0 (`make shellcheck`); workflows pass `make actionlint`.
- Australian/British spelling in prose and comments. Comments say what and why, never history.
- TDD for every behaviour: write the test, watch it fail for the stated reason, implement, watch it pass. Test output must be pristine; expected diagnostics are captured and asserted.
- Git: use `/Users/dan/.claude/bin/claude-git` for every Git command. Write each commit message to a file in the session scratchpad (written `<scratchpad>` below; it is the scratchpad directory named in the executing session's system prompt) and commit with `-F`. Run `git status --short` before staging; never `git add -A`. Never skip hooks. Every commit message ends with these two lines, written `<trailer lines>` in the messages below:

  ```text
  Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01NoVKYoELFsYPGWNWaj6CJz
  ```

- Never push, open a PR, merge, tag or release without Dan's explicit go-ahead in the moment. PR bodies go through `--body-file`, one line per paragraph, ending with the Claude Code attribution lines.
- Each part ends with a separate test-cleanup pass (not by the implementer) and an independent code review.

---

# Part 1 — `-audit-file` (PR 1)

Work on branch `state-branch-version-report`, which already holds the spec and this plan.

### Task 1: Audit Terraform and provider versions

**Files:**
- Create: `audit.go`
- Create: `audit_test.go`

**Interfaces:**
- Consumes: `Config`, `ProviderUpdate` (`config.go`); `attributeStringValue`, `attributeHasStringValue`, `expressionHasStringValue`, `providerAttributeObject`, `providerObjectItemKey`, `trimQuotes` (`main.go`); `writeTestFile` (`test_helpers_test.go`).
- Produces: `type auditReport struct { SchemaVersion int; Terraform []terraformAuditEntry; Providers []providerAuditEntry }`; `type terraformAuditEntry struct { File string; Actual *string; Expected string; Matches bool }`; `type providerAuditEntry struct { File, Name string; Actual *string; Expected string; Matches bool }`; `func buildAudit(files []string, config *Config) (*auditReport, error)`; `func parseAuditedFile(filename string) (*hclwrite.File, error)`; `func auditedAttribute(attr *hclwrite.Attribute, expected string) (actual *string, matches bool)`; test helpers `auditValue(string) *string` and `auditJSON(*testing.T, any) string`.

- [ ] **Step 1: Write the failing tests**

Create `audit_test.go`:

```go
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

	if got := auditJSON(t, audit); got != `{"schema_version":1,"terraform":[],"providers":[]}` {
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

func TestBuildAudit_StopsAtTheFirstUnparseableFile(t *testing.T) {
	dir := t.TempDir()
	bad := writeTestFile(t, dir, "bad.tf", "terraform {\n")
	good := writeTestFile(t, dir, "good.tf", "terraform {\n  required_version = \">= 1.10\"\n}\n")

	audit, err := buildAudit([]string{bad, good}, &Config{TerraformVersion: ">= 1.10"})

	if audit != nil || err == nil || !strings.HasPrefix(err.Error(), "Error auditing "+bad+": failed to parse HCL: ") {
		t.Fatalf("audit = %v, err = %v; want no audit and a parse error naming %s", audit, err, bad)
	}
}

func TestBuildAudit_ReportsAnUnreadableFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.tf")

	audit, err := buildAudit([]string{missing}, &Config{})

	if audit != nil || err == nil || !strings.HasPrefix(err.Error(), "Error auditing "+missing+": failed to read file: ") {
		t.Fatalf("audit = %v, err = %v; want no audit and a read error naming %s", audit, err, missing)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestBuildAudit' ./...`
Expected: FAIL to build with `undefined: buildAudit`, `undefined: terraformAuditEntry` and `undefined: providerAuditEntry`.

- [ ] **Step 3: Write the implementation**

Create `audit.go`:

```go
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// auditReport is the -audit-file document: each configured version value the selected files
// declare, beside the value the config expects.
type auditReport struct {
	SchemaVersion int                   `json:"schema_version"`
	Terraform     []terraformAuditEntry `json:"terraform"`
	Providers     []providerAuditEntry  `json:"providers"`
}

type terraformAuditEntry struct {
	File     string  `json:"file"`
	Actual   *string `json:"actual"`
	Expected string  `json:"expected"`
	Matches  bool    `json:"matches"`
}

type providerAuditEntry struct {
	File     string  `json:"file"`
	Name     string  `json:"name"`
	Actual   *string `json:"actual"`
	Expected string  `json:"expected"`
	Matches  bool    `json:"matches"`
}

// buildAudit reads each selected file once and records every version value the config targets
// in it. It never writes files and stops at the first file it cannot read or parse.
func buildAudit(files []string, config *Config) (*auditReport, error) {
	audit := &auditReport{SchemaVersion: 1, Terraform: []terraformAuditEntry{}, Providers: []providerAuditEntry{}}
	for _, filename := range files {
		file, err := parseAuditedFile(filename)
		if err != nil {
			return nil, err
		}
		for _, block := range file.Body().Blocks() {
			if block.Type() == "terraform" {
				audit.recordTerraformBlock(filename, block, config)
			}
		}
	}
	return audit, nil
}

func parseAuditedFile(filename string) (*hclwrite.File, error) {
	src, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("Error auditing %s: failed to read file: %w", filename, err) //nolint:staticcheck // User-facing CLI diagnostic.
	}
	file, diags := hclwrite.ParseConfig(src, filename, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, fmt.Errorf("Error auditing %s: failed to parse HCL: %s", filename, diags.Error()) //nolint:staticcheck // User-facing CLI diagnostic.
	}
	return file, nil
}

// recordTerraformBlock records the block's required_version when the config sets one, and each
// required_providers declaration of a configured provider.
func (audit *auditReport) recordTerraformBlock(filename string, block *hclwrite.Block, config *Config) {
	if config.TerraformVersion != "" {
		actual, matches := auditedAttribute(block.Body().GetAttribute("required_version"), config.TerraformVersion)
		audit.Terraform = append(audit.Terraform, terraformAuditEntry{File: filename, Actual: actual, Expected: config.TerraformVersion, Matches: matches})
	}
	for _, nestedBlock := range block.Body().Blocks() {
		if nestedBlock.Type() != "required_providers" {
			continue
		}
		for _, provider := range config.Providers {
			audit.recordRequiredProvider(filename, nestedBlock, provider)
		}
	}
}

// recordRequiredProvider records a provider declared in block syntax, object syntax or both,
// because the updater reads either.
func (audit *auditReport) recordRequiredProvider(filename string, requiredProviders *hclwrite.Block, provider ProviderUpdate) {
	for _, providerBlock := range requiredProviders.Body().Blocks() {
		if providerBlock.Type() == provider.Name {
			actual, matches := auditedAttribute(providerBlock.Body().GetAttribute("version"), provider.Version)
			audit.Providers = append(audit.Providers, providerAuditEntry{File: filename, Name: provider.Name, Actual: actual, Expected: provider.Version, Matches: matches})
		}
	}
	if objExpr, expression, ok := providerAttributeObject(requiredProviders, provider.Name); ok {
		actual, matches := auditedObjectVersion(objExpr, expression, provider.Version)
		audit.Providers = append(audit.Providers, providerAuditEntry{File: filename, Name: provider.Name, Actual: actual, Expected: provider.Version, Matches: matches})
	}
}

// auditedAttribute returns an attribute's value as written, without its quotes, and whether it
// already evaluates to expected, which is the updater's no-op test. A missing attribute has no
// value.
func auditedAttribute(attr *hclwrite.Attribute, expected string) (actual *string, matches bool) {
	if attr == nil {
		return nil, false
	}
	value := attributeStringValue(attr)
	return &value, attributeHasStringValue(attr, expected)
}

// auditedObjectVersion reads the version item of an object-syntax provider declaration.
func auditedObjectVersion(objExpr *hclsyntax.ObjectConsExpr, expression []byte, expected string) (actual *string, matches bool) {
	for _, item := range objExpr.Items {
		keyName, ok := providerObjectItemKey(item)
		if !ok || keyName != "version" {
			continue
		}
		valueRange := item.ValueExpr.Range()
		if valueRange.Start.Byte < 0 || valueRange.End.Byte > len(expression) || valueRange.Start.Byte > valueRange.End.Byte {
			continue
		}
		valueExpression := expression[valueRange.Start.Byte:valueRange.End.Byte]
		value := trimQuotes(strings.TrimSpace(string(valueExpression)))
		return &value, expressionHasStringValue(valueExpression, expected)
	}
	return nil, false
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -count=1 -run 'TestBuildAudit' ./...`
Expected: PASS, no other output.

Then run `go test -count=1 ./...` and `golangci-lint run --timeout=5m`. Expected: PASS and no lint findings.

- [ ] **Step 5: Commit**

```bash
/Users/dan/.claude/bin/claude-git -C /Users/dan/Code/tf-version-bump status --short
/Users/dan/.claude/bin/claude-git -C /Users/dan/Code/tf-version-bump add audit.go audit_test.go
/Users/dan/.claude/bin/claude-git -C /Users/dan/Code/tf-version-bump commit -F <scratchpad>/commit-task-1.md
```

Message:

```text
feat: audit Terraform and provider versions against a config

buildAudit reads each selected file once and records every configured
required_version and provider constraint beside the value the config
expects, using the updater's own comparison so a match always means an
update would be a no-op.

<trailer lines from Global Constraints>
```

### Task 2: Audit module versions in the updater's filter precedence

**Files:**
- Modify: `audit.go` (add module entries; `buildAudit` loop)
- Modify: `main.go:1328-1344` (`shouldSkipModuleVersion` uses a new shared `moduleVersionFilter`)
- Test: `audit_test.go`

**Interfaces:**
- Consumes: Task 1's `auditReport`, `auditedAttribute`, `parseAuditedFile`, `auditValue`, `auditJSON`; `ModuleUpdate`, `FromVersions` (`config.go`); `moduleSourceValue`, `moduleBlockName`, `isLocalModule`, `shouldIgnoreModule`, `containsVersion`, `updateModuleVersionWithCount`, `captureStderr`.
- Produces: `auditReport.Modules []moduleAuditEntry` (JSON key `modules`); `type moduleAuditEntry struct { File, Name, Source string; Actual *string; Expected string; Matches bool; Skip *moduleAuditSkip }`; `type moduleAuditSkip struct { Filter string; Values []string }`; `func moduleVersionFilter(currentVersion string, ignoreVersions, fromVersions []string) string` returning `"ignore_versions"`, `"from"` or `""`.

- [ ] **Step 1: Share the updater's version-filter precedence (refactor under the existing tests)**

Do this before writing Task 2's tests: once they are appended, the package's tests do not compile until Step 4, so this refactor could not be verified on its own.

In `main.go`, replace `shouldSkipModuleVersion` (currently lines 1328-1344) with:

```go
// moduleVersionFilter names the config filter that stops an update from currentVersion:
// "ignore_versions" when it lists the version, "from" when it is set and does not, or "" when
// neither applies. ignore_versions takes precedence over from.
func moduleVersionFilter(currentVersion string, ignoreVersions, fromVersions []string) string {
	if len(ignoreVersions) > 0 && containsVersion(ignoreVersions, currentVersion) {
		return "ignore_versions"
	}
	if len(fromVersions) > 0 && !containsVersion(fromVersions, currentVersion) {
		return "from"
	}
	return ""
}

func shouldSkipModuleVersion(moduleName, currentVersion string, opts *moduleUpdateOptions) bool {
	switch moduleVersionFilter(currentVersion, opts.ignoreVersions, opts.fromVersions) {
	case "ignore_versions":
		if opts.verbose {
			fmt.Printf("  ⊗ Skipped module %s in %s (current version %s matches 'ignore-version' filter %v)\n", quote(moduleName, opts.outputFormat), opts.filename, quote(currentVersion, opts.outputFormat), opts.ignoreVersions)
		}
		return true
	case "from":
		if opts.verbose {
			fmt.Printf("  ⊗ Skipped module %s in %s (current version %s does not match any 'from' filter %v)\n", quote(moduleName, opts.outputFormat), opts.filename, quote(currentVersion, opts.outputFormat), opts.fromVersions)
		}
		return true
	}
	return false
}
```

Run: `go test -count=1 ./...` and `golangci-lint run --timeout=5m`
Expected: PASS and no findings; the refactor changes no behaviour, and the existing module-filter and `-verbose` tests pin it.

Commit it on its own after `git status --short`:

```text
refactor: name the module version filter that skips an update

moduleVersionFilter reports which of ignore_versions or from stops an
update, in the updater's precedence, so the coming audit can record the
same filter the updater applies instead of re-deriving it.

<trailer lines>
```

- [ ] **Step 2: Write the failing tests**

Append to `audit_test.go`:

```go
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
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestBuildAudit' ./...`
Expected: FAIL to build with `undefined: moduleAuditEntry`, `undefined: moduleAuditSkip` and `audit.Modules undefined`.

- [ ] **Step 4: Record module entries**

In `audit.go`, add the `Modules` field to `auditReport`:

```go
type auditReport struct {
	SchemaVersion int                   `json:"schema_version"`
	Terraform     []terraformAuditEntry `json:"terraform"`
	Providers     []providerAuditEntry  `json:"providers"`
	Modules       []moduleAuditEntry    `json:"modules"`
}
```

Add the types and recorder after `providerAuditEntry`:

```go
type moduleAuditEntry struct {
	File     string           `json:"file"`
	Name     string           `json:"name"`
	Source   string           `json:"source"`
	Actual   *string          `json:"actual"`
	Expected string           `json:"expected"`
	Matches  bool             `json:"matches"`
	Skip     *moduleAuditSkip `json:"skip"`
}

// moduleAuditSkip names the first config filter that stops the updater changing a block.
type moduleAuditSkip struct {
	Filter string   `json:"filter"`
	Values []string `json:"values"`
}

// recordModuleBlock records the block once for each config entry with an equal source.
func (audit *auditReport) recordModuleBlock(filename string, block *hclwrite.Block, updates []ModuleUpdate) {
	source, ok := moduleSourceValue(block)
	if !ok {
		return
	}
	name := moduleBlockName(block)
	versionAttribute := block.Body().GetAttribute("version")
	// Index rather than copy: gocritic's hugeParam rejects passing a ModuleUpdate by value.
	for i := range updates {
		update := &updates[i]
		if update.Source != source {
			continue
		}
		actual, matches := auditedAttribute(versionAttribute, update.Version)
		audit.Modules = append(audit.Modules, moduleAuditEntry{
			File: filename, Name: name, Source: source, Actual: actual, Expected: update.Version, Matches: matches,
			Skip: moduleAuditSkipFor(name, source, actual, update),
		})
	}
}

// moduleAuditSkipFor applies the updater's precedence: a local source, then ignore_modules, then
// the version filters, which a block without a version never meets.
func moduleAuditSkipFor(name, source string, actual *string, update *ModuleUpdate) *moduleAuditSkip {
	switch {
	case isLocalModule(source):
		return &moduleAuditSkip{Filter: "local_source", Values: []string{}}
	case shouldIgnoreModule(name, update.IgnoreModules):
		return &moduleAuditSkip{Filter: "ignore_modules", Values: update.IgnoreModules}
	case actual == nil:
		return nil
	}
	switch moduleVersionFilter(*actual, update.IgnoreVersions, update.From) {
	case "ignore_versions":
		return &moduleAuditSkip{Filter: "ignore_versions", Values: update.IgnoreVersions}
	case "from":
		return &moduleAuditSkip{Filter: "from", Values: update.From}
	}
	return nil
}
```

In `buildAudit`, initialise `Modules: []moduleAuditEntry{}` alongside the other sections and replace the block loop with:

```go
		for _, block := range file.Body().Blocks() {
			switch block.Type() {
			case "terraform":
				audit.recordTerraformBlock(filename, block, config)
			case "module":
				audit.recordModuleBlock(filename, block, config.Modules)
			}
		}
```

In `TestBuildAudit_OmitsTerraformVersionsTheConfigDoesNotSet`, the expected document gains the new section: `{"schema_version":1,"terraform":[],"providers":[],"modules":[]}`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -count=1 -run 'TestBuildAudit' ./...`
Expected: PASS.

Then `go test -count=1 ./...` and `golangci-lint run --timeout=5m`. Expected: PASS, no lint findings.

- [ ] **Step 6: Commit**

Stage `audit.go` and `audit_test.go` after `git status --short` (Step 1 already committed `main.go`), then commit:

```text
feat: audit module versions in the updater's filter precedence

Each module block is recorded once per config entry with an equal source,
with the first filter that would stop an update: a local source,
ignore_modules, ignore_versions, then from. The audit uses the updater's
moduleVersionFilter, so the two cannot disagree about which versions a
filter excludes.

<trailer lines>
```

### Task 3: Wire `-audit-file` into the command

**Files:**
- Modify: `main.go` (imports; `cliFlags`; `parseFlags`; `main`; report-file helpers at lines 421-516; `validateOperationModes`; `configValidationHasConflicts`)
- Modify: `audit.go` (add `runAuditMode`)
- Test: `command_test.go`

**Interfaces:**
- Consumes: `buildAudit` (Tasks 1-2); `loadConfig`; `quote`; `runMainCommand`, `writeTestFile`, `readTestFile` (`test_helpers_test.go`).
- Produces: `cliFlags.auditFile string`; `type jsonOutput struct { fileLabel, documentLabel, tempPattern string }`; `updateReportOutput`, `auditOutput jsonOutput`; `func prepareJSONOutput(output jsonOutput, destination string, inputFiles []string) (*preparedReportFile, error)` (replaces `prepareUpdateReport`); `func validateOutputDoesNotOverwriteInput(output jsonOutput, destination string, inputFiles []string) error` (replaces `validateReportFileDoesNotOverwriteInput`); `func (prepared *preparedReportFile) publish(document any) error`; `func runUpdateMode(files []string, flags *cliFlags) (int, error)` (the update path moved out of `main`); `func validateAuditMode(flags *cliFlags)`; `func runAuditMode(files []string, flags *cliFlags) error`.

- [ ] **Step 1: Generalise the JSON writer (refactor under the existing report tests)**

In `main.go`, add `"bytes"` to the imports. Replace `prepareUpdateReport`, `publish` and `validateReportFileDoesNotOverwriteInput` with:

```go
// jsonOutput describes a JSON document the command writes through a temporary file once its
// work succeeds, and how diagnostics name it.
type jsonOutput struct {
	fileLabel     string // the destination, as in "report file must not overwrite input file"
	documentLabel string // the document, as in "Error preparing update report"
	tempPattern   string
}

var (
	updateReportOutput = jsonOutput{fileLabel: "report file", documentLabel: "update report", tempPattern: ".tf-version-bump-report-*"}
	auditOutput        = jsonOutput{fileLabel: "audit file", documentLabel: "audit", tempPattern: ".tf-version-bump-audit-*"}
)

func prepareJSONOutput(output jsonOutput, destination string, inputFiles []string) (*preparedReportFile, error) {
	if destination == "" {
		return nil, nil
	}
	if err := validateOutputDoesNotOverwriteInput(output, destination, inputFiles); err != nil {
		return nil, err
	}

	file, err := os.CreateTemp(filepath.Dir(destination), output.tempPattern)
	if err != nil {
		return nil, fmt.Errorf("Error preparing %s: %w", output.documentLabel, err) //nolint:staticcheck // User-facing CLI diagnostic.
	}
	return &preparedReportFile{destination: destination, file: file}, nil
}

func (prepared *preparedReportFile) publish(document any) error {
	var data bytes.Buffer
	encoder := json.NewEncoder(&data)
	// encoding/json writes <, > and & as Unicode escapes by default, which would make
	// constraints such as ">= 1.5" unreadable in the audit.
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		_ = prepared.discard()
		return fmt.Errorf("create report: %w", err)
	}
	if _, err := prepared.file.Write(data.Bytes()); err != nil {
		_ = prepared.discard()
		return err
	}
	if err := prepared.file.Sync(); err != nil {
		_ = prepared.discard()
		return err
	}
	temporaryName := prepared.file.Name()
	if err := prepared.file.Close(); err != nil {
		_ = os.Remove(temporaryName)
		return err
	}
	prepared.file = nil
	if err := os.Rename(temporaryName, prepared.destination); err != nil {
		_ = os.Remove(temporaryName)
		return err
	}
	return nil
}

func validateOutputDoesNotOverwriteInput(output jsonOutput, destination string, inputFiles []string) error {
	if destination == "" {
		return nil
	}

	destinationPath, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("Error resolving %s: %w", output.fileLabel, err) //nolint:staticcheck // User-facing CLI diagnostic.
	}
	destinationInfo, destinationStatErr := os.Stat(destinationPath)
	if destinationStatErr != nil && !os.IsNotExist(destinationStatErr) {
		return fmt.Errorf("Error inspecting %s: %w", output.fileLabel, destinationStatErr) //nolint:staticcheck // User-facing CLI diagnostic.
	}
	if destinationStatErr == nil && destinationInfo.IsDir() {
		return fmt.Errorf("Error preparing %s: destination is a directory: %s", output.documentLabel, destination) //nolint:staticcheck // User-facing CLI diagnostic.
	}

	for _, inputFile := range inputFiles {
		inputPath, absErr := filepath.Abs(inputFile)
		if absErr != nil {
			return fmt.Errorf("Error resolving input file %s: %w", inputFile, absErr) //nolint:staticcheck // User-facing CLI diagnostic.
		}
		if filepath.Clean(destinationPath) == filepath.Clean(inputPath) {
			return fmt.Errorf("Error: %s must not overwrite input file: %s", output.fileLabel, destination) //nolint:staticcheck // User-facing CLI diagnostic.
		}
		if destinationStatErr != nil {
			continue
		}
		inputInfo, inputStatErr := os.Stat(inputPath)
		if inputStatErr != nil {
			return fmt.Errorf("Error inspecting input file %s: %w", inputFile, inputStatErr) //nolint:staticcheck // User-facing CLI diagnostic.
		}
		if os.SameFile(destinationInfo, inputInfo) {
			return fmt.Errorf("Error: %s must not overwrite input file: %s", output.fileLabel, destination) //nolint:staticcheck // User-facing CLI diagnostic.
		}
	}

	return nil
}
```

`main` already sits at the `gocyclo` limit of 15, so the audit branch cannot be added to it directly. Move the update path, unchanged, into a helper below `main`:

```go
// runUpdateMode applies the selected updates and publishes any requested update report,
// returning the update-operation total that check mode turns into its exit status.
func runUpdateMode(files []string, flags *cliFlags) (int, error) {
	inputFiles := files
	if flags.configFile != "" {
		inputFiles = append(append([]string(nil), files...), flags.configFile)
	}
	preparedReport, err := prepareJSONOutput(updateReportOutput, flags.reportFile, inputFiles)
	if err != nil {
		return 0, err
	}

	var totalUpdates int
	if flags.configFile != "" {
		totalUpdates, err = runConfigFileMode(files, flags)
	} else {
		totalUpdates, err = runCLIMode(files, flags)
	}
	if err != nil {
		if preparedReport != nil {
			if discardErr := preparedReport.discard(); discardErr != nil {
				err = fmt.Errorf("%w; failed to discard prepared report: %v", err, discardErr)
			}
		}
		return totalUpdates, err
	}
	if preparedReport != nil {
		flags.report.SchemaVersion = 2
		if publishErr := preparedReport.publish(&flags.report); publishErr != nil {
			return totalUpdates, fmt.Errorf("Error writing update report: %v", publishErr) //nolint:staticcheck // User-facing CLI diagnostic.
		}
	}
	return totalUpdates, nil
}
```

and replace everything in `main` after `validateRequiredOperationFlags(flags)` with:

```go
	totalUpdates, err := runUpdateMode(files, flags)
	if err != nil {
		fatalf("%v", err)
	}
	if flags.check && totalUpdates > 0 {
		exitFunc(2)
	}
```

The diagnostics are unchanged: each error that `main` previously passed to `fatalf` is now returned with the same text and logged by the same `fatalf("%v", err)`.

Run: `go test -count=1 ./...` and `golangci-lint run --timeout=5m`
Expected: PASS and no findings, including every `TestCommand*Report*` and `TestCommandCheck*` test, which pin the report's bytes, diagnostics and the check exit statuses.

Commit this refactor on its own (`refactor: share the JSON output writer and isolate the update path`), with a body explaining that the audit will reuse the writer, that `runUpdateMode` keeps `main` within the complexity limit, and that the report's bytes, diagnostics and exit statuses are unchanged.

- [ ] **Step 2: Write the failing command tests**

In `command_test.go`:

1. In `TestParseFlagsContract`, append `"-audit-file", "audit.json"` to `args` and `auditFile: "audit.json"` to `want`.
2. In `TestCommandConfigValidationRejectsUpdateAndReportFlags`, add the row `{name: "audit", args: []string{"-audit-file", "audit.json"}},`.
3. Add `"path/filepath"` to the imports and append:

```go
func TestCommandWritesConfigAudit(t *testing.T) {
	dir := t.TempDir()
	input := `terraform {
  required_version = ">= 1.9"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "4.2.0"
}
`
	file := writeTestFile(t, dir, "main.tf", input)
	config := writeTestFile(t, dir, "versions.yml", `terraform_version: ">= 1.10"
providers:
  - name: aws
    version: "~> 6.0"
modules:
  - source: terraform-aws-modules/vpc/aws
    version: 5.0.0
`)
	audit := filepath.Join(dir, "audit.json")

	result := runMainCommand(t, []string{"tf-version-bump", "-pattern", file, "-config", config, "-audit-file", audit})

	wantStdout := "Found 1 file(s) matching pattern '" + file + "'\n✓ Wrote audit to '" + audit + "'\n"
	if result.stdout != wantStdout || result.diagnostics != "" || result.exitCode != -1 {
		t.Fatalf("result = %#v, want stdout %q and a normal return", result, wantStdout)
	}
	wantAudit := `{
  "schema_version": 1,
  "terraform": [
    {
      "file": "` + file + `",
      "actual": ">= 1.9",
      "expected": ">= 1.10",
      "matches": false
    }
  ],
  "providers": [
    {
      "file": "` + file + `",
      "name": "aws",
      "actual": "~> 6.0",
      "expected": "~> 6.0",
      "matches": true
    }
  ],
  "modules": [
    {
      "file": "` + file + `",
      "name": "vpc",
      "source": "terraform-aws-modules/vpc/aws",
      "actual": "4.2.0",
      "expected": "5.0.0",
      "matches": false,
      "skip": null
    }
  ]
}
`
	if got := readTestFile(t, audit); got != wantAudit {
		t.Errorf("audit = %q, want %q", got, wantAudit)
	}
	if got := readTestFile(t, file); got != input {
		t.Errorf("audit changed Terraform content to %q", got)
	}
}

func TestCommandAuditRejectsConflictingFlags(t *testing.T) {
	dir := t.TempDir()
	file := writeTestFile(t, dir, "main.tf", "module \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n")
	config := writeTestFile(t, dir, "versions.yml", "modules:\n  - source: example/module\n    version: 2.0.0\n")
	audit := filepath.Join(dir, "audit.json")
	conflict := "Error: Cannot use -audit-file with -dry-run, -check, -report-file or -force-add\n"
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "without config", args: []string{"-module", "example/module", "-to", "2.0.0"}, want: "Error: -audit-file requires -config\n"},
		{name: "dry run", args: []string{"-config", config, "-dry-run"}, want: conflict},
		{name: "check", args: []string{"-config", config, "-check"}, want: conflict},
		{name: "report", args: []string{"-config", config, "-report-file", filepath.Join(dir, "report.json")}, want: conflict},
		{name: "force add", args: []string{"-config", config, "-force-add"}, want: conflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"tf-version-bump", "-pattern", file, "-audit-file", audit}, tt.args...)
			result := runMainCommand(t, args)
			if result.stdout != "" || result.diagnostics != tt.want || result.exitCode != 1 {
				t.Fatalf("result = %#v, want diagnostic %q and exit 1", result, tt.want)
			}
			if _, err := os.Stat(audit); !os.IsNotExist(err) {
				t.Fatalf("audit stat error = %v, want no audit", err)
			}
		})
	}
}

func TestCommandAuditRejectsInputCollision(t *testing.T) {
	dir := t.TempDir()
	input := "module \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n"
	file := writeTestFile(t, dir, "main.tf", input)
	configContent := "modules:\n  - source: example/module\n    version: 2.0.0\n"
	config := writeTestFile(t, dir, "versions.yml", configContent)

	for _, destination := range []string{file, config} {
		result := runMainCommand(t, []string{"tf-version-bump", "-pattern", file, "-config", config, "-audit-file", destination})

		wantDiagnostic := "Error: audit file must not overwrite input file: " + destination + "\n"
		if result.exitCode != 1 || result.diagnostics != wantDiagnostic {
			t.Errorf("result = %#v, want diagnostic %q", result, wantDiagnostic)
		}
	}
	if readTestFile(t, file) != input || readTestFile(t, config) != configContent {
		t.Error("a rejected audit changed an input file")
	}
}

func TestCommandAuditRejectsADirectoryDestination(t *testing.T) {
	dir := t.TempDir()
	file := writeTestFile(t, dir, "main.tf", "module \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n")
	config := writeTestFile(t, dir, "versions.yml", "modules:\n  - source: example/module\n    version: 2.0.0\n")

	result := runMainCommand(t, []string{"tf-version-bump", "-pattern", file, "-config", config, "-audit-file", dir})

	if result.exitCode != 1 || result.diagnostics != "Error preparing audit: destination is a directory: "+dir+"\n" {
		t.Errorf("result = %#v, want the audit preparation failure", result)
	}
}

func TestCommandAuditWritesNothingWhenAFileCannotBeParsed(t *testing.T) {
	dir := t.TempDir()
	bad := writeTestFile(t, dir, "bad.tf", "terraform {\n")
	writeTestFile(t, dir, "good.tf", "terraform {\n  required_version = \">= 1.10\"\n}\n")
	config := writeTestFile(t, dir, "versions.yml", "terraform_version: \">= 1.10\"\n")
	previous := "previous audit\n"
	audit := writeTestFile(t, dir, "audit.json", previous)

	result := runMainCommand(t, []string{"tf-version-bump", "-pattern", filepath.Join(dir, "*.tf"), "-config", config, "-audit-file", audit})

	if result.exitCode != 1 || !strings.HasPrefix(result.diagnostics, "Error auditing "+bad+": failed to parse HCL: ") {
		t.Errorf("result = %#v, want a parse failure naming %s", result, bad)
	}
	if got := readTestFile(t, audit); got != previous {
		t.Errorf("audit = %q, want the previous audit kept", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read directory: %v", err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if strings.Join(names, ",") != "audit.json,bad.tf,good.tf,versions.yml" {
		t.Errorf("directory entries = %v, want no temporary audit left behind", names)
	}
}

func TestCommandAuditReportsAnInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	file := writeTestFile(t, dir, "main.tf", "terraform {}\n")
	config := writeTestFile(t, dir, "versions.yml", "modules:\n  - version: 1.0.0\n")

	result := runMainCommand(t, []string{"tf-version-bump", "-pattern", file, "-config", config, "-audit-file", filepath.Join(dir, "audit.json")})

	want := "Error loading config file: module at index 0 is missing 'source' field\n"
	if result.exitCode != 1 || result.diagnostics != want {
		t.Errorf("result = %#v, want diagnostic %q", result, want)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestParseFlagsContract|TestCommand(WritesConfigAudit|Audit|ConfigValidationRejectsUpdateAndReportFlags)' ./...`
Expected: FAIL to build with `unknown field auditFile in struct literal of type cliFlags`.

- [ ] **Step 4: Implement the flag**

In `main.go`:

1. Add `auditFile string` to `cliFlags` after `reportFile`.
2. In `parseFlags`, after the `report-file` registration:

```go
	flagSet.StringVar(&flags.auditFile, "audit-file", "", "Write each configured version value's current and expected version as JSON without changing files (config mode)")
```

3. In `configValidationHasConflicts`, append `|| flags.auditFile != ""` to the returned expression.
4. In `validateOperationModes`, directly after the `-validate-config` block's closing brace, add:

```go
	if flags.auditFile != "" {
		validateAuditMode(flags)
	}
```

5. Add below `validateConfigUpdateMode`:

```go
// validateAuditMode keeps -audit-file read-only: it compares files with a config and never
// combines with flags that preview, check, report or add versions.
func validateAuditMode(flags *cliFlags) {
	if flags.configFile == "" {
		fatalf("Error: -audit-file requires -config")
	}
	if flags.dryRun || flags.check || flags.reportFile != "" || flags.forceAdd {
		fatalf("Error: Cannot use -audit-file with -dry-run, -check, -report-file or -force-add")
	}
}
```

6. In `main`, between `validateRequiredOperationFlags(flags)` and the `runUpdateMode` call from Step 1:

```go
	if flags.auditFile != "" {
		if err := runAuditMode(files, flags); err != nil {
			fatalf("%v", err)
		}
		return
	}
```

In `audit.go`, append:

```go
// runAuditMode writes the audit of files against the config. It validates the destination
// before reading Terraform files, never writes them, and replaces an existing audit only once
// every file has been audited.
func runAuditMode(files []string, flags *cliFlags) error {
	config, err := loadConfig(flags.configFile)
	if err != nil {
		return fmt.Errorf("Error loading config file: %w", err) //nolint:staticcheck // User-facing CLI diagnostic.
	}
	inputFiles := append(append([]string(nil), files...), flags.configFile)
	prepared, err := prepareJSONOutput(auditOutput, flags.auditFile, inputFiles)
	if err != nil {
		return err
	}
	audit, err := buildAudit(files, config)
	if err != nil {
		if discardErr := prepared.discard(); discardErr != nil {
			err = fmt.Errorf("%w; failed to discard prepared audit: %v", err, discardErr)
		}
		return err
	}
	if err := prepared.publish(audit); err != nil {
		return fmt.Errorf("Error writing audit: %w", err) //nolint:staticcheck // User-facing CLI diagnostic.
	}
	fmt.Printf("✓ Wrote audit to %s\n", quote(flags.auditFile, flags.output))
	return nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -count=1 -run 'TestParseFlagsContract|TestCommand(WritesConfigAudit|Audit|ConfigValidationRejectsUpdateAndReportFlags)' ./...`
Expected: PASS.

Then `go test -count=1 -race ./...` and `golangci-lint run --timeout=5m`. Expected: PASS, no findings. If `gocyclo` reports `validateOperationModes` above 15, move the `-check` conflict checks into a `validateCheckMode(flags)` helper beside `validateAuditMode` rather than raising the limit.

- [ ] **Step 6: Commit**

```text
feat: add -audit-file to compare files with a config read-only

In config mode, -audit-file writes schema version 1 JSON listing every
configured Terraform, provider and module version the selected files
declare, beside the expected value, whether it already matches, and the
first filter that would skip a module. It never writes Terraform files,
validates its destination like -report-file, and leaves an existing audit
untouched when any file cannot be parsed.

<trailer lines>
```

### Task 4: Document `-audit-file`

**Files:**
- Modify: `docs/USAGE.md` (after line 22; flag table at line 46; new section after line 82; config-mode paragraph at line 258; output bullets at lines 296-304)
- Modify: `README.md` (after line 161)
- Modify: `CLAUDE.md` (Layout block, "Update flow", Testing list, "Errors and output")
- Modify: `AGENTS.md` (repository tree and file table)

**Interfaces:**
- Consumes: the behaviour from Tasks 1-3.
- Produces: the anchor `docs/USAGE.md#machine-readable-version-audit`, which the README links to.

- [ ] **Step 1: Update `docs/USAGE.md`**

After the paragraph ending "…cannot be combined with update or report flags." (line 22), add:

```markdown
`-audit-file` is a config-mode option rather than another mode: it compares the selected files with
the config and writes the result without changing any Terraform file.
```

In the flag table, after the `-report-file` row:

```markdown
| `-audit-file <path>` | Config mode | Write every configured version value's current and expected version as JSON, without changing files. |
```

After the "Machine-readable update report" section (before `## Module updates`), add:

~~~~markdown
### Machine-readable version audit

Use `-audit-file` with a config to record how far the selected files are from it, without changing
them:

```bash
tf-version-bump \
  -pattern "**/*.tf" \
  -config versions.yml \
  -audit-file audit.json
```

The audit lists every value the config targets that the files declare:

```json
{
  "schema_version": 1,
  "terraform": [
    {"file": "versions.tf", "actual": ">= 1.10", "expected": ">= 1.10", "matches": true}
  ],
  "providers": [
    {"file": "versions.tf", "name": "aws", "actual": "~> 5.0", "expected": "~> 6.0", "matches": false}
  ],
  "modules": [
    {"file": "main.tf", "name": "vpc", "source": "terraform-aws-modules/vpc/aws",
     "actual": "4.2.0", "expected": "5.0.0", "matches": false, "skip": null},
    {"file": "main.tf", "name": "legacy_vpc", "source": "terraform-aws-modules/vpc/aws",
     "actual": "3.19.0", "expected": "5.0.0", "matches": false,
     "skip": {"filter": "ignore_modules", "values": ["legacy_*"]}}
  ]
}
```

- `terraform` has one entry per `terraform` block when the config sets `terraform_version`.
- `providers` has one entry per `required_providers` declaration, in either syntax, whose local name
  the config lists.
- `modules` has one entry per module block and config entry with an equal `source`, so a block that
  two entries target appears twice.
- `actual` is the value as written, without its quotes, or `null` when the declaration has no
  version. A non-literal expression appears as its source text.
- `matches` is true when the value already evaluates to the expected string, so an update would never
  change it. A value can also stay unchanged without matching: a skipped module, or an object-syntax
  provider without `version`, which updates do not add.
- `skip` names the first filter that would stop an update, in the order the updater applies them:
  `local_source`, `ignore_modules`, `ignore_versions`, then `from`. A module without a `version` is
  never skipped by a version filter.

Config entries that no selected file uses produce no entries. The audit is written only after every
selected file parses; otherwise the command exits 1 and leaves any existing audit untouched. A
written audit exits 0 whatever it contains. The destination is validated like `-report-file`'s, and
`-audit-file` cannot be combined with `-dry-run`, `-check`, `-report-file` or `-force-add`.
~~~~

In "Config mode", after "Use `-force-add`, `-dry-run`, `-check`, `-verbose`, or `-output md` with config mode when required.", add the sentence: "Add `-audit-file` to compare the files with the config instead of updating them."

In "Output and error behaviour", after the `-check` bullet, add:

```markdown
- `-audit-file` writes its audit and exits 0 whatever the audit contains; a selected file that cannot
  be read or parsed exits 1 without writing it.
```

- [ ] **Step 2: Update `README.md`**

After the paragraph ending "…for a complete workflow." (line 161), add:

```markdown
To compare files with a config without changing them, pass `-audit-file audit.json` in config mode
instead. The audit lists every configured value's current and expected version; the
[usage reference](docs/USAGE.md#machine-readable-version-audit) describes it.
```

- [ ] **Step 3: Update `CLAUDE.md`**

In the Layout block, after the `config.go` line:

```text
audit.go                 # -audit-file: read-only comparison of files with a config
```

In the Testing list, after the `provider_update_test.go` item:

```markdown
- `audit_test.go` — audit entries, filter precedence, and agreement with the updater.
```

In "Errors and output", after the `-report-file` paragraph:

```markdown
`-audit-file` is the read-only comparison contract. In config mode it writes schema version 1 JSON
listing each configured Terraform, provider and module version value the selected files declare,
its current and expected values, whether they already match and, for modules, the first filter that
would skip an update. The audit and the updater share `moduleVersionFilter` and the
`attributeHasStringValue` comparison; keep the audit in step with any change to update filtering.
```

In "Update flow", replace the first paragraph with:

```markdown
`main()` → `validateOperationModes` → either standalone config validation or
`findMatchingFiles` → `runAuditMode` (`-audit-file`: `buildAudit`, never writes Terraform files) or
`runUpdateMode` → `runConfigFileMode` (YAML) / `runCLIMode` (one direct operation).
Each update mode dispatches to one of three update paths:
`updateModuleVersionWithCount`, `updateTerraformVersion`, or `updateProviderVersionWithCount`.
```

In `AGENTS.md`, add `audit.go` beside the core files. In the repository tree, after the `config.go` line (line 32), add:

```text
├── audit.go                 # -audit-file comparison
```

In the file table, after the `config.go` row (line 45), add:

```markdown
| `audit.go` | Read-only `-audit-file` comparison of files with a config |
```

- [ ] **Step 4: Verify the documentation**

Run: `make -C /Users/dan/Code/tf-version-bump docs-check`
Expected: PASS, including `TestDocumentationLocalLinksResolve` (the README anchor resolves).

- [ ] **Step 5: Commit**

```text
docs: document -audit-file

<trailer lines>
```

### Task 5: Verify, clean up and open PR 1

**Files:** none new.

- [ ] **Step 1: Run the full validation**

```bash
go mod download && go mod verify
go test -count=1 -v -race -coverprofile=coverage.out -covermode=atomic ./...
go tool cover -func=coverage.out | tail -n 1
golangci-lint run --timeout=5m
make actionlint && make shellcheck && make docs-check
go build -o tf-version-bump .
```

Expected: every command succeeds; total coverage at least 90.0%. Remove `coverage.out` and the `tf-version-bump` binary afterwards if `make clean` does not.

- [ ] **Step 2: Test-cleanup pass**

Dispatch a separate subagent with the `test-cleanup` skill over `audit_test.go` and the new `command_test.go` tests. It must keep one strongest owner per contract (the command test owns bytes, exit status and diagnostics; `audit_test.go` owns entry semantics). Re-run `go test -count=1 ./...` after its changes, then commit them (`test: …`).

- [ ] **Step 3: Independent review**

Dispatch `pr-review-toolkit:code-reviewer` over `git diff main...HEAD`. Fix confirmed findings with TDD, one commit each.

- [ ] **Step 4: Ask Dan, then push and open PR 1**

Ask Dan before pushing. With his go-ahead: push `state-branch-version-report` with `claude-git`, then `claude-gh pr create --base main --title "feat: add -audit-file for read-only config comparison" --body-file <scratchpad>/pr-1-body.md`. The body covers the flag, the schema, the refactors (`moduleVersionFilter`, shared JSON writer, unescaped HTML in JSON), test evidence, and that the spec and plan for part 2 ride along.

---

## Gate: release

Part 2 depends on a release that contains `-audit-file`:

1. Dan merges PR 1.
2. Dan cuts the release from `main` following `docs/RELEASING.md` ("Create a release"), and verifies the Linux x86-64 archive's checksum against `tf-version-bump-v<version>.checksums.txt`.
3. Dan provides the tag and the verified Linux x86-64 SHA-256.

Tasks 6-11 need only PR 1 merged. Task 12 needs the tag and digest.

---

# Part 2 — Report workflow (PR 2)

Start from `main` after PR 1 merges: `claude-git -C /Users/dan/Code/tf-version-bump checkout main`, `claude-git … pull --rebase`, then `claude-git … checkout -b state-branch-version-report-workflow`.

Until Task 12 the report workflow pins the example's current release (`v1.0.0-rc.11`), matching the update callers. That pin cannot run `-audit-file`, which is why the branch is not pushed as a PR before Task 12.

### Task 6: Report harness and `collect`

**Files:**
- Create: `examples/github-actions/.github/scripts/report-state-branches.sh` (mode 755)
- Create: `examples/github-actions/report-test.sh` (mode 755)
- Modify: `examples/github-actions/test.sh` (line 8 area: add `REPORT_TEST`; line 1718: run it)

**Interfaces:**
- Consumes: `-audit-file` (Part 1); discovery's JSON shape `{include: [{branch, base_oid, …}]}`.
- Produces: `report-state-branches.sh collect` reading `REPORT_POLICY_ID`, `REPORT_CONTROL_CHECKOUT`, `REPORT_CONFIG_PATH`, `REPORT_TERRAFORM_ROOTS`, `REPORT_BRANCHES`, `REPORT_OUTPUT_DIR`, `REPORT_TF_VERSION_BUMP_VERSION`, `REPORT_TF_VERSION_BUMP_ARCHIVE_SHA256`, `RUNNER_TEMP`, and writing `$REPORT_OUTPUT_DIR/records.json` with the spec's shape. Harness helpers: `setup_report_fixture`, `add_state_branch <branch> <writer>` (sets `FIXTURE_LAST_OID`), `run_collect`, `assert_silent_success`, `fail`; fixture globals `FIXTURE_ROOT`, `FIXTURE_CONTROL`, `FIXTURE_OUTPUT`, `FIXTURE_BRANCH_ENTRIES`.

- [ ] **Step 1: Write the harness and the first failing tests**

Create `examples/github-actions/report-test.sh`:

```bash
#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPOSITORY_ROOT=$(cd "$SCRIPT_DIR/../.." && pwd)
REPORT_SCRIPT="$SCRIPT_DIR/.github/scripts/report-state-branches.sh"
REPORT_WORKFLOW="$SCRIPT_DIR/.github/workflows/tf-version-bump-report.yml"
TEST_GIT=${TEST_GIT-git}
TEST_ROOT=$(mktemp -d)
TEST_ROOT=$(realpath "$TEST_ROOT")
# The report needs -audit-file, so the harness builds tf-version-bump from this checkout and a
# curl shim serves it at the release URL the script derives from this version.
TEST_VERSION=v9.9.9-report
TEST_ARCHIVE="$TEST_ROOT/tf-version-bump.tar.gz"
TEST_ARCHIVE_SHA256=""

FIXTURE_ROOT=""
FIXTURE_REMOTE=""
FIXTURE_SEED=""
FIXTURE_CONTROL=""
FIXTURE_RUNNER_TEMP=""
FIXTURE_OUTPUT=""
FIXTURE_BRANCH_ENTRIES='[]'
FIXTURE_BRANCH_COUNT=0
FIXTURE_LAST_OID=""

fail() {
    echo "FAIL: $*" >&2
    exit 1
}


cleanup() {
    chmod -R u+w "$TEST_ROOT" 2>/dev/null || true
    rm -rf -- "$TEST_ROOT"
}
trap cleanup EXIT


assert_silent_success() {
    local description=$1 stdout_file=$2 stderr_file=$3
    shift 3
    if ! "$@" >"$stdout_file" 2>"$stderr_file"; then
        fail "$description failed: $(<"$stderr_file")"
    fi
    [[ ! -s "$stdout_file" && ! -s "$stderr_file" ]] \
        || fail "$description emitted unexpected output: stdout=$(<"$stdout_file") stderr=$(<"$stderr_file")"
}


build_release_archive() {
    mkdir -p "$TEST_ROOT/release" "$TEST_ROOT/bin"
    go build -C "$REPOSITORY_ROOT" -ldflags "-X main.version=${TEST_VERSION#v}" \
        -o "$TEST_ROOT/release/tf-version-bump" .
    tar -czf "$TEST_ARCHIVE" -C "$TEST_ROOT/release" tf-version-bump
    TEST_ARCHIVE_SHA256=$(sha256sum "$TEST_ARCHIVE")
    TEST_ARCHIVE_SHA256=${TEST_ARCHIVE_SHA256%% *}
    cat >"$TEST_ROOT/bin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
output='' url=''
while [[ $# -gt 0 ]]; do
    case "$1" in
        --output) output=$2; shift 2 ;;
        -*) shift ;;
        *) url=$1; shift ;;
    esac
done
[[ "$url" == "${REPORT_TEST_EXPECTED_URL:?}" ]] || { echo "unexpected download: $url" >&2; exit 22; }
cp -- "${REPORT_TEST_ARCHIVE:?}" "$output"
EOF
    chmod +x "$TEST_ROOT/bin/curl"
}


fixture_commit() {
    local checkout=$1 message=$2
    shift 2
    "$TEST_GIT" -C "$checkout" \
        -c user.name='Report Test' \
        -c user.email='report-test@example.invalid' \
        commit "$@" -m "$message" >/dev/null
}


# An origin whose default branch holds the control config, a shallow control clone of it (as
# actions/checkout makes), and a seed repository that pushes state branches.
setup_report_fixture() {
    FIXTURE_ROOT=$(mktemp -d "$TEST_ROOT/fixture.XXXXXX")
    FIXTURE_REMOTE="$FIXTURE_ROOT/origin.git"
    FIXTURE_SEED="$FIXTURE_ROOT/seed"
    FIXTURE_CONTROL="$FIXTURE_ROOT/control"
    FIXTURE_RUNNER_TEMP="$FIXTURE_ROOT/runner-temp"
    FIXTURE_OUTPUT="$FIXTURE_RUNNER_TEMP/version-report"
    FIXTURE_BRANCH_ENTRIES='[]'
    mkdir -p "$FIXTURE_RUNNER_TEMP" "$FIXTURE_SEED/.github/tf-version-bump"
    "$TEST_GIT" init --quiet --bare --initial-branch=main "$FIXTURE_REMOTE"
    "$TEST_GIT" init --quiet --initial-branch=main "$FIXTURE_SEED"
    cat >"$FIXTURE_SEED/.github/tf-version-bump/test.yml" <<'EOF'
terraform_version: ">= 1.10"
providers:
  - name: aws
    version: "~> 6.0"
modules:
  - source: terraform-aws-modules/vpc/aws
    version: "5.0.0"
    ignore_modules:
      - "legacy_*"
EOF
    "$TEST_GIT" -C "$FIXTURE_SEED" add -- .github/tf-version-bump/test.yml
    fixture_commit "$FIXTURE_SEED" 'test: create report control fixture'
    "$TEST_GIT" -C "$FIXTURE_SEED" remote add origin "$FIXTURE_REMOTE"
    "$TEST_GIT" -C "$FIXTURE_SEED" push --quiet origin main
    "$TEST_GIT" clone --quiet --depth=1 "file://$FIXTURE_REMOTE" "$FIXTURE_CONTROL"
}


# Pushes a state branch whose tree is exactly what the writer function creates, and records it
# for collection as discovery would.
add_state_branch() {
    local branch=$1 writer=$2
    FIXTURE_BRANCH_COUNT=$((FIXTURE_BRANCH_COUNT + 1))
    "$TEST_GIT" -C "$FIXTURE_SEED" checkout --quiet --orphan "fixture-$FIXTURE_BRANCH_COUNT"
    "$TEST_GIT" -C "$FIXTURE_SEED" read-tree --empty
    find "$FIXTURE_SEED" -mindepth 1 -maxdepth 1 ! -name .git -exec rm -rf -- {} +
    "$writer" "$FIXTURE_SEED"
    "$TEST_GIT" -C "$FIXTURE_SEED" add --all
    fixture_commit "$FIXTURE_SEED" "test: create $branch"
    FIXTURE_LAST_OID=$("$TEST_GIT" -C "$FIXTURE_SEED" rev-parse HEAD)
    "$TEST_GIT" -C "$FIXTURE_SEED" push --quiet origin "HEAD:refs/heads/$branch"
    FIXTURE_BRANCH_ENTRIES=$(jq -c --arg branch "$branch" --arg oid "$FIXTURE_LAST_OID" \
        '. + [{branch: $branch, base_oid: $oid}]' <<<"$FIXTURE_BRANCH_ENTRIES")
}


run_collect() {
    jq -n --argjson include "$FIXTURE_BRANCH_ENTRIES" '{include: $include}' \
        >"$FIXTURE_RUNNER_TEMP/branches.json"
    env PATH="$TEST_ROOT/bin:$PATH" \
        REPORT_TEST_EXPECTED_URL="https://github.com/yesdevnull/tf-version-bump/releases/download/$TEST_VERSION/tf-version-bump_${TEST_VERSION#v}_linux_x86_64.tar.gz" \
        REPORT_TEST_ARCHIVE="$TEST_ARCHIVE" \
        REPORT_POLICY_ID="${REPORT_POLICY_ID-nonproduction}" \
        REPORT_CONTROL_CHECKOUT="${REPORT_CONTROL_CHECKOUT-$FIXTURE_CONTROL}" \
        REPORT_CONFIG_PATH="${REPORT_CONFIG_PATH-.github/tf-version-bump/test.yml}" \
        REPORT_TERRAFORM_ROOTS="${REPORT_TERRAFORM_ROOTS-.}" \
        REPORT_BRANCHES="$FIXTURE_RUNNER_TEMP/branches.json" \
        REPORT_OUTPUT_DIR="${REPORT_OUTPUT_DIR-$FIXTURE_OUTPUT}" \
        REPORT_TF_VERSION_BUMP_VERSION="${REPORT_TF_VERSION_BUMP_VERSION-$TEST_VERSION}" \
        REPORT_TF_VERSION_BUMP_ARCHIVE_SHA256="${REPORT_TF_VERSION_BUMP_ARCHIVE_SHA256-$TEST_ARCHIVE_SHA256}" \
        RUNNER_TEMP="$FIXTURE_RUNNER_TEMP" \
        "$REPORT_SCRIPT" collect
}


write_alpha_branch() {
    cat >"$1/main.tf" <<'EOF'
module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "4.2.0"
}

module "legacy_vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.19.0"
}
EOF
    cat >"$1/versions.tf" <<'EOF'
terraform {
  required_version = ">= 1.10"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}
EOF
    printf 'provider "aws" {}\n' >"$1/providers.tf"
}


write_beta_branch() {
    cat >"$1/main.tf" <<'EOF'
module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.0.0"
}
EOF
}


write_documented_beta_branch() {
    write_beta_branch "$1"
    mkdir "$1/docs"
    printf '# Notes\n' >"$1/docs/README.md"
}


test_collect_records_each_branch_root_and_audit() {
    setup_report_fixture
    add_state_branch state/nonproduction/alpha write_alpha_branch
    local alpha=$FIXTURE_LAST_OID
    add_state_branch state/staging/beta write_beta_branch
    local beta=$FIXTURE_LAST_OID

    assert_silent_success 'collecting two branches' "$FIXTURE_ROOT/stdout" "$FIXTURE_ROOT/stderr" run_collect

    jq -e --arg alpha "$alpha" --arg beta "$beta" '. == {
        policy: "nonproduction",
        roots: ["."],
        branches: [
          {branch: "state/nonproduction/alpha", commit: $alpha, error: null, roots: [
            {root: ".", exists: true, files: {"main.tf": true, "providers.tf": true}, audit: {
              schema_version: 1,
              terraform: [{file: "versions.tf", actual: ">= 1.10", expected: ">= 1.10", matches: true}],
              providers: [{file: "versions.tf", name: "aws", actual: "~> 5.0", expected: "~> 6.0", matches: false}],
              modules: [
                {file: "main.tf", name: "vpc", source: "terraform-aws-modules/vpc/aws",
                 actual: "4.2.0", expected: "5.0.0", matches: false, skip: null},
                {file: "main.tf", name: "legacy_vpc", source: "terraform-aws-modules/vpc/aws",
                 actual: "3.19.0", expected: "5.0.0", matches: false,
                 skip: {filter: "ignore_modules", values: ["legacy_*"]}}]}}]},
          {branch: "state/staging/beta", commit: $beta, error: null, roots: [
            {root: ".", exists: true, files: {"main.tf": true, "providers.tf": false}, audit: {
              schema_version: 1, terraform: [], providers: [],
              modules: [{file: "main.tf", name: "vpc", source: "terraform-aws-modules/vpc/aws",
                         actual: "5.0.0", expected: "5.0.0", matches: true, skip: null}]}}]}]}' \
        "$FIXTURE_OUTPUT/records.json" >/dev/null \
        || fail "collect did not record both branches: $(<"$FIXTURE_OUTPUT/records.json")"
    [[ "$("$TEST_GIT" -C "$FIXTURE_CONTROL" worktree list | wc -l)" -eq 1 ]] \
        || fail 'collect left a branch worktree behind'
}


test_collect_records_missing_and_empty_roots() {
    setup_report_fixture
    add_state_branch state/staging/beta write_documented_beta_branch

    REPORT_TERRAFORM_ROOTS=$'.\ninfra\ndocs' assert_silent_success 'collecting three roots' \
        "$FIXTURE_ROOT/stdout" "$FIXTURE_ROOT/stderr" run_collect

    jq -e '.roots == [".", "infra", "docs"] and (.branches[0].roots | map(del(.audit.modules)) == [
        {root: ".", exists: true, files: {"main.tf": true, "providers.tf": false},
         audit: {schema_version: 1, terraform: [], providers: []}},
        {root: "infra", exists: false, files: {"main.tf": false, "providers.tf": false}, audit: null},
        {root: "docs", exists: true, files: {"main.tf": false, "providers.tf": false},
         audit: {schema_version: 1, terraform: [], providers: []}}])
        and .branches[0].roots[2].audit.modules == []' \
        "$FIXTURE_OUTPUT/records.json" >/dev/null \
        || fail "collect did not record missing and empty roots: $(<"$FIXTURE_OUTPUT/records.json")"
}


build_release_archive

if [[ $# -eq 0 ]]; then
    tests=(test_collect_records_each_branch_root_and_audit test_collect_records_missing_and_empty_roots)
else tests=("$@"); fi
for test_name in "${tests[@]}"; do
    "$test_name"
    printf 'PASS: %s\n' "$test_name"
done
```

Note: `del(.audit.modules)` on a record whose `audit` is `null` leaves it `null`, so the `infra` comparison holds.

Create `examples/github-actions/.github/scripts/report-state-branches.sh` containing only the usage and dispatch, so the tests fail on behaviour rather than a missing file:

```bash
#!/usr/bin/env bash

set -euo pipefail
set +x
export LC_ALL=C

usage() {
    cat <<'EOF'
Usage: report-state-branches.sh collect
       report-state-branches.sh --help
EOF
}

case "${1-}" in
    --help) usage ;;
    *) usage >&2; exit 2 ;;
esac
```

`chmod 755` both files. In `test.sh`, add `REPORT_TEST="$SCRIPT_DIR/report-test.sh"` after the `RECONCILE_TEST` line and `"$REPORT_TEST"` after `"$RECONCILE_TEST"` in the no-argument branch.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `examples/github-actions/report-test.sh`
Expected: `FAIL: collecting two branches failed: Usage: report-state-branches.sh collect …` (the dispatcher rejects `collect`).

- [ ] **Step 3: Implement `collect`**

Replace the script body after `export LC_ALL=C` with:

```bash
usage() {
    cat <<'EOF'
Usage: report-state-branches.sh collect
       report-state-branches.sh --help

Compare each discovered state branch with its policy's control configuration, without
changing anything.

collect reads REPORT_BRANCHES (the discovery script's JSON), fetches each branch's
discovered commit from REPORT_CONTROL_CHECKOUT's origin into a temporary worktree and,
for every root in REPORT_TERRAFORM_ROOTS (newline separated, relative, no glob
characters), runs tf-version-bump -audit-file against REPORT_CONFIG_PATH (relative to
the control checkout) and records whether main.tf and providers.tf exist. It installs
REPORT_TF_VERSION_BUMP_VERSION after checking REPORT_TF_VERSION_BUMP_ARCHIVE_SHA256 and
writes records.json for REPORT_POLICY_ID into REPORT_OUTPUT_DIR, which must be absent.
A branch it cannot read is recorded with an error; collect still exits 0. Temporary
files live below RUNNER_TEMP.
EOF
}

WORK_ROOT=""
CONTROL_CHECKOUT=""
TOOL=""

report_error() { echo "report error: $*" >&2; exit 1; }

# shellcheck disable=SC2329 # Called by the EXIT trap.
cleanup() {
    [[ -n "$WORK_ROOT" ]] || return 0
    rm -rf -- "$WORK_ROOT"
    [[ -z "$CONTROL_CHECKOUT" ]] || git -C "$CONTROL_CHECKOUT" worktree prune
}
trap cleanup EXIT

path_is_within() {
    [[ "$1" == "$2" || "$1" == "$2/"* ]]
}

install_tool() {
    [[ "$REPORT_TF_VERSION_BUMP_VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] \
        || report_error 'tf-version-bump version must be a v-prefixed semantic version'
    [[ "$REPORT_TF_VERSION_BUMP_ARCHIVE_SHA256" =~ ^[0-9a-f]{64}$ ]] \
        || report_error 'tf-version-bump archive SHA-256 must be 64 lowercase hexadecimal characters'
    local version=${REPORT_TF_VERSION_BUMP_VERSION#v} archive="$WORK_ROOT/tf-version-bump.tar.gz"
    curl --fail --silent --show-error --location --output "$archive" \
        "https://github.com/yesdevnull/tf-version-bump/releases/download/$REPORT_TF_VERSION_BUMP_VERSION/tf-version-bump_${version}_linux_x86_64.tar.gz" \
        || report_error 'could not download the tf-version-bump release archive'
    # Compare the digest directly: the harness runs this on macOS too, whose sha256sum lacks
    # GNU's --check --status.
    local digest
    digest=$(sha256sum "$archive")
    [[ "${digest%% *}" == "$REPORT_TF_VERSION_BUMP_ARCHIVE_SHA256" ]] \
        || report_error 'tf-version-bump release archive checksum mismatch'
    tar -xzf "$archive" -C "$WORK_ROOT" tf-version-bump
    TOOL="$WORK_ROOT/tf-version-bump"
    [[ "$("$TOOL" -version | head -n 1)" == "tf-version-bump $version" ]] \
        || report_error 'tf-version-bump reported an unexpected version'
}

# Prints the JSON record of one root, or one error line on stderr and returns 1. It runs
# inside collect_branch's if condition, where errexit does not apply, so every step checks
# its own status.
collect_root() {
    local worktree=$1 root=$2 config=$3 canonical_roots=$4
    local path="$worktree/$root" canonical audit="$WORK_ROOT/audit.json"
    if [[ ! -e "$path" && ! -L "$path" ]]; then
        jq -cn --arg root "$root" \
            '{root: $root, exists: false, files: {"main.tf": false, "providers.tf": false}, audit: null}'
        return
    fi
    canonical=$(realpath "$path") || return 1
    path_is_within "$canonical" "$worktree" \
        || { echo "root $root resolves outside the checkout" >&2; return 1; }
    [[ -d "$canonical" ]] || { echo "root $root is not a directory" >&2; return 1; }
    ! grep -qxF -- "$canonical" "$canonical_roots" \
        || { echo "root $root duplicates another root" >&2; return 1; }
    printf '%s\n' "$canonical" >>"$canonical_roots"
    [[ -z "$(find "$canonical" -maxdepth 1 -name '*.tf' -type l -print -quit)" ]] \
        || { echo "root $root contains a symlinked Terraform file" >&2; return 1; }
    rm -f -- "$audit"
    if [[ -z "$(find "$canonical" -maxdepth 1 -name '*.tf' -type f -print -quit)" ]]; then
        printf '%s\n' '{"schema_version": 1, "terraform": [], "providers": [], "modules": []}' >"$audit"
    elif ! (cd "$worktree" && "$TOOL" -pattern "$root/*.tf" -config "$config" -audit-file "$audit") \
        >"$WORK_ROOT/audit.log" 2>&1; then
        local failure
        failure=$(tail -n 1 "$WORK_ROOT/audit.log")
        # The CLI logs through Go's log package, which starts each line with the date and time;
        # dropping it keeps the recorded error stable between runs.
        failure=${failure#[0-9][0-9][0-9][0-9]/[0-9][0-9]/[0-9][0-9] [0-9][0-9]:[0-9][0-9]:[0-9][0-9] }
        echo "could not audit root $root: $failure" >&2
        return 1
    fi
    local main_found=false providers_found=false
    [[ ! -f "$canonical/main.tf" ]] || main_found=true
    [[ ! -f "$canonical/providers.tf" ]] || providers_found=true
    jq -c --arg root "$root" --argjson main "$main_found" --argjson providers "$providers_found" \
        '{root: $root, exists: true, files: {"main.tf": $main, "providers.tf": $providers}, audit: .}' "$audit"
}

# Prints a JSON array of one branch's root records, or one error line on stderr and returns 1.
collect_branch() {
    local commit=$1 config=$2
    shift 2
    local worktree="$WORK_ROOT/worktree" root
    local roots_file="$WORK_ROOT/roots.jsonl" canonical_roots="$WORK_ROOT/canonical-roots"
    : >"$roots_file"
    : >"$canonical_roots"
    git -C "$CONTROL_CHECKOUT" fetch --quiet --no-tags --depth=1 origin "$commit" 2>"$WORK_ROOT/git.log" \
        || { echo "could not fetch commit $commit: $(tail -n 1 "$WORK_ROOT/git.log")" >&2; return 1; }
    git -C "$CONTROL_CHECKOUT" worktree add --quiet --detach "$worktree" "$commit" 2>"$WORK_ROOT/git.log" \
        || { echo "could not check out commit $commit: $(tail -n 1 "$WORK_ROOT/git.log")" >&2; return 1; }
    worktree=$(realpath "$worktree") || return 1
    for root in "$@"; do
        collect_root "$worktree" "$root" "$config" "$canonical_roots" >>"$roots_file" || return 1
    done
    jq -s . "$roots_file"
}

collect() {
    local name
    for name in POLICY_ID CONTROL_CHECKOUT CONFIG_PATH TERRAFORM_ROOTS BRANCHES OUTPUT_DIR \
        TF_VERSION_BUMP_VERSION TF_VERSION_BUMP_ARCHIVE_SHA256; do
        name="REPORT_$name"
        [[ -n "${!name-}" ]] || report_error "$name must be set"
    done
    [[ -n "${RUNNER_TEMP-}" && -d "$RUNNER_TEMP" ]] || report_error 'RUNNER_TEMP must be an existing directory'
    [[ "$REPORT_POLICY_ID" =~ ^[a-z0-9][a-z0-9-]{0,31}$ ]] || report_error 'policy ID is invalid'
    [[ "$REPORT_CONTROL_CHECKOUT" == /* && -d "$REPORT_CONTROL_CHECKOUT" ]] \
        || report_error 'control checkout must be an absolute existing directory'
    CONTROL_CHECKOUT=$(realpath "$REPORT_CONTROL_CHECKOUT")
    [[ "$REPORT_CONFIG_PATH" != /* && "/$REPORT_CONFIG_PATH/" != *"/../"* ]] \
        || report_error 'config path must be relative and must not contain ..'
    local config
    if ! config=$(realpath "$CONTROL_CHECKOUT/$REPORT_CONFIG_PATH" 2>/dev/null) \
        || [[ ! -f "$config" ]] || ! path_is_within "$config" "$CONTROL_CHECKOUT"; then
        report_error 'config path must name a file inside the control checkout'
    fi
    # Each root becomes part of a -pattern, so a glob character would widen the selection.
    local root glob_characters='[][*?{}\]'
    local -a roots=()
    readarray -t roots < <(printf '%s' "$REPORT_TERRAFORM_ROOTS")
    for root in "${roots[@]}"; do
        [[ -n "$root" && "$root" != /* && "/$root/" != *"/../"* ]] \
            || report_error 'Terraform roots must be non-empty relative paths without ..'
        [[ ! "$root" =~ $glob_characters ]] || report_error "Terraform root $root contains a glob character"
    done
    jq -e '.include | type == "array" and all(.[]; (.branch | type == "string") and (.base_oid | test("^[0-9a-f]{40}$")))' \
        "$REPORT_BRANCHES" >/dev/null 2>&1 || report_error 'branches file is not discovery output'
    [[ "$REPORT_OUTPUT_DIR" == /* && ! -e "$REPORT_OUTPUT_DIR" && ! -L "$REPORT_OUTPUT_DIR" ]] \
        || report_error 'output directory must be absolute and absent'

    umask 077
    WORK_ROOT=$(mktemp -d "$RUNNER_TEMP/tf-version-bump-report.XXXXXX")
    install_tool
    mkdir -p -- "$REPORT_OUTPUT_DIR"
    local branch commit error records="$WORK_ROOT/branches.jsonl"
    : >"$records"
    while IFS=$'\t' read -r branch commit; do
        if collect_branch "$commit" "$config" "${roots[@]}" >"$WORK_ROOT/branch.json" 2>"$WORK_ROOT/branch.error"; then
            jq -c --arg branch "$branch" --arg commit "$commit" \
                '{branch: $branch, commit: $commit, error: null, roots: .}' "$WORK_ROOT/branch.json" >>"$records"
        else
            error=$(tail -n 1 "$WORK_ROOT/branch.error")
            jq -cn --arg branch "$branch" --arg commit "$commit" --arg error "${error:-could not read the branch}" \
                '{branch: $branch, commit: $commit, error: $error, roots: []}' >>"$records"
        fi
        [[ ! -e "$WORK_ROOT/worktree" ]] || git -C "$CONTROL_CHECKOUT" worktree remove --force "$WORK_ROOT/worktree"
    done < <(jq -r '.include[] | [.branch, .base_oid] | @tsv' "$REPORT_BRANCHES")
    jq -s --arg policy "$REPORT_POLICY_ID" '{policy: $policy, roots: $ARGS.positional, branches: .}' \
        --args "${roots[@]}" <"$records" >"$REPORT_OUTPUT_DIR/records.json"
}

if [[ $# -ne 1 ]]; then
    usage >&2
    exit 2
fi
case "$1" in
    --help) usage ;;
    collect) collect ;;
    *) usage >&2; exit 2 ;;
esac
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `examples/github-actions/report-test.sh`
Expected: `PASS: test_collect_records_each_branch_root_and_audit` and `PASS: test_collect_records_missing_and_empty_roots`, nothing else.

Then run the pinned shellcheck (0.11.0) directly on the new and changed scripts, because `make shellcheck` lints only tracked files and these are not committed until Step 5: `shellcheck examples/github-actions/.github/scripts/report-state-branches.sh examples/github-actions/report-test.sh examples/github-actions/test.sh`. Expected: clean. If shellcheck reports SC2329 for writer functions that are only passed by name (for example `write_alpha_branch`), add `# shellcheck disable=SC2329 # Called by name through add_state_branch.` above each one.

- [ ] **Step 5: Commit**

Stage the two new files and `test.sh`, then commit:

```text
feat: collect state-branch versions for the version report

report-state-branches.sh collect audits every configured root of each
discovered branch at its discovered commit, in a temporary worktree of the
control checkout, and records whether main.tf and providers.tf exist. Its
harness builds tf-version-bump from this checkout because no release has
-audit-file yet, and serves it through a curl shim at the release URL.

<trailer lines>
```

### Task 7: Unreadable branches and invalid inputs

**Files:**
- Modify: `examples/github-actions/report-test.sh`
- Modify (only if a test exposes a defect): `examples/github-actions/.github/scripts/report-state-branches.sh`

**Interfaces:**
- Consumes: Task 6's harness and `collect`.
- Produces: tests `test_collect_records_unreadable_branches_and_continues`, `test_collect_rejects_invalid_inputs`.

- [ ] **Step 1: Write the tests**

Add before `build_release_archive`:

```bash
write_broken_branch() {
    printf 'module {\n' >"$1/main.tf"
}


write_symlinked_branch() {
    write_beta_branch "$1"
    mv "$1/main.tf" "$1/real.tf"
    ln -s real.tf "$1/main.tf"
}


write_escaping_root_branch() {
    write_beta_branch "$1"
    ln -s .. "$1/outside"
}


test_collect_records_unreadable_branches_and_continues() {
    setup_report_fixture
    add_state_branch state/staging/broken write_broken_branch
    add_state_branch state/staging/linked write_symlinked_branch
    add_state_branch state/staging/escaping write_escaping_root_branch
    local missing=0123456789abcdef0123456789abcdef01234567
    FIXTURE_BRANCH_ENTRIES=$(jq -c --arg oid "$missing" '. + [{branch: "state/staging/missing", base_oid: $oid}]' \
        <<<"$FIXTURE_BRANCH_ENTRIES")
    add_state_branch state/staging/beta write_beta_branch

    REPORT_TERRAFORM_ROOTS=$'.\noutside' assert_silent_success 'collecting unreadable branches' \
        "$FIXTURE_ROOT/stdout" "$FIXTURE_ROOT/stderr" run_collect

    # The parse error must follow the root directly: collect strips the date and time Go's log
    # package puts before each CLI diagnostic, so the recorded error is stable between runs.
    jq -e --arg missing "$missing" '
        [.branches[].branch] == ["state/staging/broken", "state/staging/linked", "state/staging/escaping",
                                 "state/staging/missing", "state/staging/beta"]
        and (.branches[0].error | startswith("could not audit root .: Error auditing main.tf: failed to parse HCL"))
        and .branches[1].error == "root . contains a symlinked Terraform file"
        and .branches[2].error == "root outside resolves outside the checkout"
        and (.branches[3].error | startswith("could not fetch commit " + $missing))
        and all(.branches[0:4][]; .roots == [])
        and .branches[4].error == null
        and [.branches[4].roots[] | .exists] == [true, false]' \
        "$FIXTURE_OUTPUT/records.json" >/dev/null \
        || fail "collect did not record the unreadable branches and continue: $(<"$FIXTURE_OUTPUT/records.json")"
}


test_collect_rejects_invalid_inputs() {
    setup_report_fixture
    add_state_branch state/staging/beta write_beta_branch
    local wrong_digest
    wrong_digest=$(printf 'a%.0s' {1..64})
    local -a cases=(
        "REPORT_TERRAFORM_ROOTS=../outside|Terraform roots must be non-empty relative paths without .."
        "REPORT_TERRAFORM_ROOTS=env-[12]|Terraform root env-[12] contains a glob character"
        "REPORT_CONFIG_PATH=/etc/hosts|config path must be relative and must not contain .."
        "REPORT_POLICY_ID=Bad|policy ID is invalid"
        "REPORT_TF_VERSION_BUMP_ARCHIVE_SHA256=$wrong_digest|tf-version-bump release archive checksum mismatch"
        "REPORT_OUTPUT_DIR=$FIXTURE_RUNNER_TEMP|output directory must be absolute and absent"
    )
    local entry assignment expected
    for entry in "${cases[@]}"; do
        assignment=${entry%%|*}
        expected=${entry#*|}
        if (export "${assignment?}"; run_collect) >"$FIXTURE_ROOT/stdout" 2>"$FIXTURE_ROOT/stderr"; then
            fail "collect accepted $assignment"
        fi
        [[ ! -s "$FIXTURE_ROOT/stdout" ]] || fail "collect printed output for $assignment"
        [[ "$(<"$FIXTURE_ROOT/stderr")" == "report error: $expected" ]] \
            || fail "collect did not report '$expected' for $assignment: $(<"$FIXTURE_ROOT/stderr")"
        [[ ! -e "$FIXTURE_OUTPUT/records.json" ]] || fail "collect wrote records for $assignment"
    done
}


test_collect_records_duplicate_roots_as_a_branch_error() {
    setup_report_fixture
    add_state_branch state/staging/beta write_beta_branch

    REPORT_TERRAFORM_ROOTS=$'.\n./' assert_silent_success 'collecting duplicate roots' \
        "$FIXTURE_ROOT/stdout" "$FIXTURE_ROOT/stderr" run_collect

    jq -e '.branches == [{branch: "state/staging/beta", commit: .branches[0].commit,
        error: "root ./ duplicates another root", roots: []}]' "$FIXTURE_OUTPUT/records.json" >/dev/null \
        || fail "collect did not record duplicate roots as a branch error: $(<"$FIXTURE_OUTPUT/records.json")"
}
```

Add the three names to the `tests=(…)` list.

- [ ] **Step 2: Run the tests**

Run: `examples/github-actions/report-test.sh test_collect_records_unreadable_branches_and_continues test_collect_rejects_invalid_inputs test_collect_records_duplicate_roots_as_a_branch_error`
Expected: all three PASS against Task 6's implementation. Because these tests describe behaviour Task 6 already implemented, prove each one can fail: temporarily delete the symlink check in `collect_root`, re-run, confirm `test_collect_records_unreadable_branches_and_continues` fails, then restore it; temporarily delete the glob-character check, confirm `test_collect_rejects_invalid_inputs` fails, then restore it; temporarily delete the duplicate-root check, confirm `test_collect_records_duplicate_roots_as_a_branch_error` fails, then restore it. Do the mutation in a throwaway copy or restore with `git checkout -- <script>`, and confirm `git status --short` shows only `report-test.sh` changed afterwards.

- [ ] **Step 3: Commit**

```text
test: cover unreadable branches and invalid report inputs

<trailer lines>
```

### Task 8: The improved report

**Files:**
- Modify: `examples/github-actions/.github/scripts/report-state-branches.sh`
- Modify: `examples/github-actions/report-test.sh`

**Interfaces:**
- Consumes: `records.json` (Tasks 6-7).
- Produces: `report-state-branches.sh report` reading `REPORT_OUTPUT_DIR` and `GITHUB_STEP_SUMMARY`, writing `version-report.csv` and appending the summary; jq definitions `REPORT_JQ_DEFINITIONS` (`cell`, `table_row`) and `VERSION_ROWS_JQ` (`branch_rows`) that Task 9 reuses; harness helpers `setup_records_fixture`, `write_report_records`, `run_report_subcommand`.

- [ ] **Step 1: Write the failing tests**

Add to `report-test.sh` before `build_release_archive`:

```bash
setup_records_fixture() {
    FIXTURE_ROOT=$(mktemp -d "$TEST_ROOT/records.XXXXXX")
    FIXTURE_OUTPUT="$FIXTURE_ROOT/version-report"
    mkdir "$FIXTURE_OUTPUT"
    : >"$FIXTURE_ROOT/summary.md"
}


# Four branches covering every improved-report status: alpha has a provider and module
# mismatch, a skipped module and a missing version; beta lacks providers.tf; gamma passes
# everything; delta could not be read.
write_report_records() {
    cat >"$FIXTURE_OUTPUT/records.json" <<'EOF'
{
  "policy": "nonproduction",
  "roots": ["."],
  "branches": [
    {"branch": "state/nonproduction/alpha", "commit": "1111111111111111111111111111111111111111", "error": null, "roots": [
      {"root": ".", "exists": true, "files": {"main.tf": true, "providers.tf": true}, "audit": {"schema_version": 1,
        "terraform": [{"file": "versions.tf", "actual": ">= 1.10", "expected": ">= 1.10", "matches": true}],
        "providers": [{"file": "versions.tf", "name": "aws", "actual": "~> 5.0", "expected": "~> 6.0", "matches": false}],
        "modules": [
          {"file": "main.tf", "name": "vpc", "source": "terraform-aws-modules/vpc/aws", "actual": "4.2.0", "expected": "5.0.0", "matches": false, "skip": null},
          {"file": "main.tf", "name": "legacy_vpc", "source": "terraform-aws-modules/vpc/aws", "actual": "3.19.0", "expected": "5.0.0", "matches": false, "skip": {"filter": "ignore_modules", "values": ["legacy_*"]}},
          {"file": "storage.tf", "name": "logs", "source": "terraform-aws-modules/s3-bucket/aws", "actual": ">= 4.0, < 5.0", "expected": ">= 4.0, < 5.0", "matches": true, "skip": null},
          {"file": "storage.tf", "name": "assets", "source": "terraform-aws-modules/s3-bucket/aws", "actual": null, "expected": ">= 4.0, < 5.0", "matches": false, "skip": null}
        ]}}]},
    {"branch": "state/staging/beta", "commit": "2222222222222222222222222222222222222222", "error": null, "roots": [
      {"root": ".", "exists": true, "files": {"main.tf": true, "providers.tf": false}, "audit": {"schema_version": 1, "terraform": [], "providers": [],
        "modules": [{"file": "main.tf", "name": "vpc", "source": "terraform-aws-modules/vpc/aws", "actual": "5.0.0", "expected": "5.0.0", "matches": true, "skip": null}]}}]},
    {"branch": "state/staging/gamma", "commit": "3333333333333333333333333333333333333333", "error": null, "roots": [
      {"root": ".", "exists": true, "files": {"main.tf": true, "providers.tf": true}, "audit": {"schema_version": 1, "terraform": [], "providers": [],
        "modules": [{"file": "main.tf", "name": "vpc", "source": "terraform-aws-modules/vpc/aws", "actual": "5.0.0", "expected": "5.0.0", "matches": true, "skip": null}]}}]},
    {"branch": "state/staging/delta", "commit": "4444444444444444444444444444444444444444", "error": "could not fetch commit 4444444444444444444444444444444444444444", "roots": []}
  ]
}
EOF
}


run_report_subcommand() {
    env REPORT_OUTPUT_DIR="$FIXTURE_OUTPUT" GITHUB_STEP_SUMMARY="$FIXTURE_ROOT/summary.md" \
        "$REPORT_SCRIPT" "$1"
}


assert_file_content() {
    local description=$1 actual=$2 expected=$3
    diff -u "$expected" "$actual" >&2 || fail "$description differs from the expected content"
}


test_report_writes_the_improved_csv_and_summary() {
    setup_records_fixture
    write_report_records

    if run_report_subcommand report >"$FIXTURE_ROOT/stdout" 2>"$FIXTURE_ROOT/stderr"; then
        fail 'the report passed although a branch could not be read'
    fi
    [[ ! -s "$FIXTURE_ROOT/stdout" && "$(<"$FIXTURE_ROOT/stderr")" \
        == 'report error: 1 branch(es) could not be read; the version report lists them' ]] \
        || fail "the report did not name the unreadable branch count: $(<"$FIXTURE_ROOT/stderr")"

    cat >"$FIXTURE_ROOT/expected.csv" <<'EOF'
"status","branch","kind","subject","block","file","actual","expected","detail"
"PASS","state/nonproduction/alpha","root",".","","","","","found"
"PASS","state/nonproduction/alpha","file","main.tf","","main.tf","","","found"
"PASS","state/nonproduction/alpha","file","providers.tf","","providers.tf","","","found"
"PASS","state/nonproduction/alpha","terraform","required_version","","versions.tf",">= 1.10",">= 1.10",""
"FAIL","state/nonproduction/alpha","provider","aws","","versions.tf","~> 5.0","~> 6.0","version differs"
"FAIL","state/nonproduction/alpha","module","terraform-aws-modules/vpc/aws","vpc","main.tf","4.2.0","5.0.0","version differs"
"SKIP","state/nonproduction/alpha","module","terraform-aws-modules/vpc/aws","legacy_vpc","main.tf","3.19.0","5.0.0","skipped by ignore_modules (legacy_*)"
"PASS","state/nonproduction/alpha","module","terraform-aws-modules/s3-bucket/aws","logs","storage.tf",">= 4.0, < 5.0",">= 4.0, < 5.0",""
"FAIL","state/nonproduction/alpha","module","terraform-aws-modules/s3-bucket/aws","assets","storage.tf","",">= 4.0, < 5.0","no version attribute"
"PASS","state/staging/beta","root",".","","","","","found"
"PASS","state/staging/beta","file","main.tf","","main.tf","","","found"
"FAIL","state/staging/beta","file","providers.tf","","providers.tf","","","not found"
"PASS","state/staging/beta","module","terraform-aws-modules/vpc/aws","vpc","main.tf","5.0.0","5.0.0",""
"PASS","state/staging/gamma","root",".","","","","","found"
"PASS","state/staging/gamma","file","main.tf","","main.tf","","","found"
"PASS","state/staging/gamma","file","providers.tf","","providers.tf","","","found"
"PASS","state/staging/gamma","module","terraform-aws-modules/vpc/aws","vpc","main.tf","5.0.0","5.0.0",""
"ERROR","state/staging/delta","branch","","","","","","could not fetch commit 4444444444444444444444444444444444444444"
EOF
    assert_file_content 'the improved CSV' "$FIXTURE_OUTPUT/version-report.csv" "$FIXTURE_ROOT/expected.csv"

    cat >"$FIXTURE_ROOT/expected.md" <<'EOF'
## Version report (nonproduction)

Checked 4 branch(es): 4 FAIL, 12 PASS, 1 SKIP, 1 ERROR

### state/nonproduction/alpha: 3 FAIL, 5 PASS, 1 SKIP, 0 ERROR

| Status | Kind | Subject | Block | File | Actual | Expected | Detail |
| --- | --- | --- | --- | --- | --- | --- | --- |
| FAIL | provider | aws |  | versions.tf | &#126;&gt; 5.0 | &#126;&gt; 6.0 | version differs |
| FAIL | module | terraform-aws-modules/vpc/aws | vpc | main.tf | 4.2.0 | 5.0.0 | version differs |
| SKIP | module | terraform-aws-modules/vpc/aws | legacy&#95;vpc | main.tf | 3.19.0 | 5.0.0 | skipped by ignore&#95;modules (legacy&#95;&#42;) |
| FAIL | module | terraform-aws-modules/s3-bucket/aws | assets | storage.tf |  | &gt;= 4.0, &lt; 5.0 | no version attribute |

### state/staging/beta: 1 FAIL, 3 PASS, 0 SKIP, 0 ERROR

| Status | Kind | Subject | Block | File | Actual | Expected | Detail |
| --- | --- | --- | --- | --- | --- | --- | --- |
| FAIL | file | providers.tf |  | providers.tf |  |  | not found |

### state/staging/delta: 0 FAIL, 0 PASS, 0 SKIP, 1 ERROR

| Status | Kind | Subject | Block | File | Actual | Expected | Detail |
| --- | --- | --- | --- | --- | --- | --- | --- |
| ERROR | branch |  |  |  |  |  | could not fetch commit 4444444444444444444444444444444444444444 |

Branches where every check passed:

- state/staging/gamma (4 checks)
EOF
    assert_file_content 'the improved summary' "$FIXTURE_ROOT/summary.md" "$FIXTURE_ROOT/expected.md"
}


test_report_succeeds_when_every_branch_was_read() {
    setup_records_fixture
    write_report_records
    jq '.branches |= map(select(.error == null))' "$FIXTURE_OUTPUT/records.json" >"$FIXTURE_ROOT/readable.json"
    mv "$FIXTURE_ROOT/readable.json" "$FIXTURE_OUTPUT/records.json"

    assert_silent_success 'reporting readable branches' "$FIXTURE_ROOT/stdout" "$FIXTURE_ROOT/stderr" \
        run_report_subcommand report
    grep -qxF 'Checked 3 branch(es): 4 FAIL, 12 PASS, 1 SKIP, 0 ERROR' "$FIXTURE_ROOT/summary.md" \
        || fail "the summary did not count the readable branches: $(<"$FIXTURE_ROOT/summary.md")"
}


# The audit records a non-literal expression as its source text, which can span lines.
test_report_keeps_multi_line_values_on_one_table_row() {
    setup_records_fixture
    write_report_records
    jq '.branches = [.branches[0] | .roots[0].audit.modules[0].actual = "try(\n  var.vpc_version,\n  \"5.0.0\"\n)"]' \
        "$FIXTURE_OUTPUT/records.json" >"$FIXTURE_ROOT/multi-line.json"
    mv "$FIXTURE_ROOT/multi-line.json" "$FIXTURE_OUTPUT/records.json"

    assert_silent_success 'reporting a multi-line value' "$FIXTURE_ROOT/stdout" "$FIXTURE_ROOT/stderr" \
        run_report_subcommand report
    grep -qxF '| FAIL | module | terraform-aws-modules/vpc/aws | vpc | main.tf | try(<br>  var.vpc&#95;version,<br>  &quot;5.0.0&quot;<br>) | 5.0.0 | version differs |' \
        "$FIXTURE_ROOT/summary.md" || fail "the multi-line value broke its table row: $(<"$FIXTURE_ROOT/summary.md")"
}
```

Add the three names to `tests=(…)`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `examples/github-actions/report-test.sh test_report_writes_the_improved_csv_and_summary test_report_succeeds_when_every_branch_was_read test_report_keeps_multi_line_values_on_one_table_row`
Expected: `FAIL: the report did not name the unreadable branch count: Usage: …` for the first (the dispatcher rejects `report`).

- [ ] **Step 3: Implement `report`**

Add below `report_error`:

```bash
# Escapes a value for a Markdown table cell: HTML special characters, then the characters
# Markdown would read as structure or emphasis, as numeric entities, then newlines as <br> so a
# multi-line expression stays on its row.
REPORT_JQ_DEFINITIONS='
def cell: tostring | @html | gsub("(?<c>[|*_`~\\[\\]\\\\])"; "&#\(.c | explode[0]);") | gsub("\r?\n"; "<br>");
def table_row: "| " + (map(cell) | join(" | ")) + " |";
'

# The improved report's rows for one branch record: each root's own check, its file checks,
# then every audited value. A matching value passes even when a filter would skip it.
# shellcheck disable=SC2016 # jq, not the shell, expands these.
VERSION_ROWS_JQ='
def root_file($root; $name): if $root == "." then $name else "\($root)/\($name)" end;
def found_row($kind; $subject; $file; $found):
  {status: (if $found then "PASS" else "FAIL" end), kind: $kind, subject: $subject, block: "",
   file: $file, actual: "", expected: "", detail: (if $found then "found" else "not found" end)};
def value_row($kind; $subject; $block; $missing):
  {kind: $kind, subject: $subject, block: $block, file: .file, actual: (.actual // ""), expected: .expected}
  + (if .matches then {status: "PASS", detail: ""}
     elif .skip != null and .skip.filter == "local_source" then {status: "SKIP", detail: "skipped: local module source"}
     elif .skip != null then {status: "SKIP", detail: "skipped by \(.skip.filter) (\(.skip.values | join(", ")))"}
     elif .actual == null then {status: "FAIL", detail: $missing}
     else {status: "FAIL", detail: "version differs"} end);
def branch_rows:
  .branch as $branch
  | (if .error != null then
       {status: "ERROR", kind: "branch", subject: "", block: "", file: "", actual: "", expected: "", detail: .error}
     else
       .roots[] as $root
       | found_row("root"; $root.root; ""; $root.exists),
         (("main.tf", "providers.tf") as $name
          | found_row("file"; $name; root_file($root.root; $name); $root.files[$name])),
         (($root.audit // {terraform: [], providers: [], modules: []})
          | (.terraform[] | value_row("terraform"; "required_version"; ""; "no required_version")),
            (.providers[] | value_row("provider"; .name; ""; "no version attribute")),
            (.modules[] | value_row("module"; .source; .name; "no version attribute")))
     end)
  | {status: .status, branch: $branch, kind: .kind, subject: .subject, block: .block,
     file: .file, actual: .actual, expected: .expected, detail: .detail};
'

# shellcheck disable=SC2016 # jq, not the shell, expands these.
VERSION_SUMMARY_JQ='
def counts:
  "\(map(select(.status == "FAIL")) | length) FAIL, \(map(select(.status == "PASS")) | length) PASS, "
  + "\(map(select(.status == "SKIP")) | length) SKIP, \(map(select(.status == "ERROR")) | length) ERROR";
[.branches[] | {branch: .branch, rows: [branch_rows]}] as $branches
| "## Version report (\(.policy | cell))\n\nChecked \($branches | length) branch(es): \([$branches[].rows[]] | counts)\n"
  + ($branches
     | map(select(any(.rows[]; .status != "PASS"))
           | "\n### \(.branch | cell): \(.rows | counts)\n\n"
             + "| Status | Kind | Subject | Block | File | Actual | Expected | Detail |\n"
             + "| --- | --- | --- | --- | --- | --- | --- | --- |\n"
             + (.rows | map(select(.status != "PASS")
                            | [.status, .kind, .subject, .block, .file, .actual, .expected, .detail]
                            | table_row + "\n") | join("")))
     | join(""))
  + ([$branches[] | select(all(.rows[]; .status == "PASS"))]
     | if . == [] then ""
       else "\nBranches where every check passed:\n\n" + (map("- \(.branch | cell) (\(.rows | length) checks)\n") | join(""))
       end)
'

records_file() {
    : "${REPORT_OUTPUT_DIR:?REPORT_OUTPUT_DIR must be set}"
    : "${GITHUB_STEP_SUMMARY:?GITHUB_STEP_SUMMARY must be set}"
    [[ -f "$REPORT_OUTPUT_DIR/records.json" ]] || report_error 'collect wrote no records.json'
    printf '%s\n' "$REPORT_OUTPUT_DIR/records.json"
}

report() {
    local records unreadable
    records=$(records_file)
    jq -r "$REPORT_JQ_DEFINITIONS$VERSION_ROWS_JQ"'
        (["status", "branch", "kind", "subject", "block", "file", "actual", "expected", "detail"] | @csv),
        (.branches[] | branch_rows | [.status, .branch, .kind, .subject, .block, .file, .actual, .expected, .detail] | @csv)
    ' "$records" >"$REPORT_OUTPUT_DIR/version-report.csv"
    jq -j "$REPORT_JQ_DEFINITIONS$VERSION_ROWS_JQ$VERSION_SUMMARY_JQ" "$records" >>"$GITHUB_STEP_SUMMARY"
    unreadable=$(jq '[.branches[] | select(.error != null)] | length' "$records")
    [[ "$unreadable" -eq 0 ]] \
        || report_error "$unreadable branch(es) could not be read; the version report lists them"
}
```

Add `report) report ;;` to the dispatch `case`, and extend `usage` with:

```text
       report-state-branches.sh report

report appends the version report to GITHUB_STEP_SUMMARY and writes version-report.csv
into REPORT_OUTPUT_DIR from its records.json. It exits 1 after writing both when any
branch could not be read; version mismatches alone never fail it.
```

(The `Usage:` line list gains `report-state-branches.sh report`; the paragraph goes after the collect paragraph.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `examples/github-actions/report-test.sh`
Expected: every test PASSES. Then `make shellcheck`: clean.

- [ ] **Step 5: Commit**

```text
feat: write the improved version report

report turns records.json into version-report.csv and a job summary with
separate kind, subject, block, file, actual, expected and detail columns,
per-branch counts, SKIP rows that name the filter, and ERROR rows for
branches that could not be read, which then fail the step after writing.

<trailer lines>
```

### Task 9: The legacy report

**Files:**
- Modify: `examples/github-actions/.github/scripts/report-state-branches.sh`
- Modify: `examples/github-actions/report-test.sh`

**Interfaces:**
- Consumes: `REPORT_JQ_DEFINITIONS`, `records_file` (Task 8); `setup_records_fixture`, `write_report_records`, `run_report_subcommand`, `assert_file_content`.
- Produces: `report-state-branches.sh legacy` writing `legacy-report.csv` and appending the legacy summary.

- [ ] **Step 1: Write the failing tests**

Add to `report-test.sh`:

```bash
test_legacy_writes_the_existing_report_format() {
    setup_records_fixture
    write_report_records

    assert_silent_success 'writing the legacy report' "$FIXTURE_ROOT/stdout" "$FIXTURE_ROOT/stderr" \
        run_report_subcommand legacy

    cat >"$FIXTURE_ROOT/expected.csv" <<'EOF'
"Result","Test","Comment","State Branch"
"PASS","main.tf","found: main.tf","state/nonproduction/alpha"
"PASS","providers.tf","found: providers.tf","state/nonproduction/alpha"
"FAIL","terraform-aws-modules/vpc/aws","version mismatch: act. 4.2.0 exp. 5.0.0","state/nonproduction/alpha"
"FAIL","terraform-aws-modules/vpc/aws","version mismatch: act. 3.19.0 exp. 5.0.0","state/nonproduction/alpha"
"PASS","terraform-aws-modules/s3-bucket/aws","version matched: act. >= 4.0, < 5.0 exp. >= 4.0, < 5.0","state/nonproduction/alpha"
"FAIL","terraform-aws-modules/s3-bucket/aws","version mismatch: act. none exp. >= 4.0, < 5.0","state/nonproduction/alpha"
"PASS","main.tf","found: main.tf","state/staging/beta"
"FAIL","providers.tf","not found: providers.tf","state/staging/beta"
"PASS","terraform-aws-modules/vpc/aws","version matched: act. 5.0.0 exp. 5.0.0","state/staging/beta"
"PASS","main.tf","found: main.tf","state/staging/gamma"
"PASS","providers.tf","found: providers.tf","state/staging/gamma"
"PASS","terraform-aws-modules/vpc/aws","version matched: act. 5.0.0 exp. 5.0.0","state/staging/gamma"
EOF
    assert_file_content 'the legacy CSV' "$FIXTURE_OUTPUT/legacy-report.csv" "$FIXTURE_ROOT/expected.csv"

    cat >"$FIXTURE_ROOT/expected.md" <<'EOF'
## Legacy version report (nonproduction)

| Result | Test | Comment | State Branch |
| --- | --- | --- | --- |
| FAIL | terraform-aws-modules/vpc/aws | version mismatch: act. 4.2.0 exp. 5.0.0 | state/nonproduction/alpha |
| FAIL | terraform-aws-modules/vpc/aws | version mismatch: act. 3.19.0 exp. 5.0.0 | state/nonproduction/alpha |
| FAIL | terraform-aws-modules/s3-bucket/aws | version mismatch: act. none exp. &gt;= 4.0, &lt; 5.0 | state/nonproduction/alpha |
| FAIL | providers.tf | not found: providers.tf | state/staging/beta |
EOF
    assert_file_content 'the legacy summary' "$FIXTURE_ROOT/summary.md" "$FIXTURE_ROOT/expected.md"
}


test_legacy_reports_all_passed_and_missing_roots() {
    setup_records_fixture
    write_report_records
    jq '.branches |= map(select(.branch == "state/staging/gamma"))' "$FIXTURE_OUTPUT/records.json" \
        >"$FIXTURE_ROOT/gamma.json"
    mv "$FIXTURE_ROOT/gamma.json" "$FIXTURE_OUTPUT/records.json"

    assert_silent_success 'writing a passing legacy report' "$FIXTURE_ROOT/stdout" "$FIXTURE_ROOT/stderr" \
        run_report_subcommand legacy
    [[ "$(<"$FIXTURE_ROOT/summary.md")" == $'## Legacy version report (nonproduction)\n\nAll 3 checks passed.' ]] \
        || fail "the passing legacy summary is wrong: $(<"$FIXTURE_ROOT/summary.md")"

    : >"$FIXTURE_ROOT/summary.md"
    jq '.branches[0].roots[0] = {root: ".", exists: false, files: {"main.tf": false, "providers.tf": false}, audit: null}' \
        "$FIXTURE_OUTPUT/records.json" >"$FIXTURE_ROOT/missing.json"
    mv "$FIXTURE_ROOT/missing.json" "$FIXTURE_OUTPUT/records.json"
    assert_silent_success 'writing a legacy report for a missing root' "$FIXTURE_ROOT/stdout" "$FIXTURE_ROOT/stderr" \
        run_report_subcommand legacy
    [[ "$(<"$FIXTURE_OUTPUT/legacy-report.csv")" == '"Result","Test","Comment","State Branch"
"FAIL","main.tf","not found: main.tf","state/staging/gamma"
"FAIL","providers.tf","not found: providers.tf","state/staging/gamma"' ]] \
        || fail "the missing-root legacy CSV is wrong: $(<"$FIXTURE_OUTPUT/legacy-report.csv")"
}


test_legacy_rejects_several_roots() {
    setup_records_fixture
    write_report_records
    jq '.roots = [".", "infra"]' "$FIXTURE_OUTPUT/records.json" >"$FIXTURE_ROOT/roots.json"
    mv "$FIXTURE_ROOT/roots.json" "$FIXTURE_OUTPUT/records.json"

    if run_report_subcommand legacy >"$FIXTURE_ROOT/stdout" 2>"$FIXTURE_ROOT/stderr"; then
        fail 'the legacy report accepted several roots'
    fi
    [[ ! -s "$FIXTURE_ROOT/stdout" \
        && "$(<"$FIXTURE_ROOT/stderr")" == 'report error: legacy report supports one Terraform root only' ]] \
        || fail "the legacy report did not reject several roots: $(<"$FIXTURE_ROOT/stderr")"
    [[ ! -e "$FIXTURE_OUTPUT/legacy-report.csv" && ! -s "$FIXTURE_ROOT/summary.md" ]] \
        || fail 'the legacy report wrote output for several roots'
}
```

Add the three names to `tests=(…)`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `examples/github-actions/report-test.sh test_legacy_writes_the_existing_report_format test_legacy_reports_all_passed_and_missing_roots test_legacy_rejects_several_roots`
Expected: `FAIL: writing the legacy report failed: Usage: …`.

- [ ] **Step 3: Implement `legacy`**

Add below `VERSION_SUMMARY_JQ`:

```bash
# The legacy report reproduces an existing report exactly, defects included: modules only,
# the module source as its Test, filters ignored and one row per block.
# shellcheck disable=SC2016 # jq, not the shell, expands these.
LEGACY_ROWS_JQ='
def legacy_rows:
  .branch as $branch
  | .roots[0] as $root
  | ((("main.tf", "providers.tf") as $name
      | if $root.files[$name] then ["PASS", $name, "found: \($name)"] else ["FAIL", $name, "not found: \($name)"] end),
     (($root.audit.modules // [])[]
      | [(if .matches then "PASS" else "FAIL" end), .source,
         "version \(if .matches then "matched" else "mismatch" end): act. \(.actual // "none") exp. \(.expected)"]))
  | . + [$branch];
'

legacy() {
    local records
    records=$(records_file)
    [[ "$(jq '.roots | length' "$records")" -eq 1 ]] \
        || report_error 'legacy report supports one Terraform root only'
    jq -r "$REPORT_JQ_DEFINITIONS$LEGACY_ROWS_JQ"'
        (["Result", "Test", "Comment", "State Branch"] | @csv),
        (.branches[] | select(.error == null) | legacy_rows | @csv)
    ' "$records" >"$REPORT_OUTPUT_DIR/legacy-report.csv"
    jq -j "$REPORT_JQ_DEFINITIONS$LEGACY_ROWS_JQ"'
        [.branches[] | select(.error == null) | legacy_rows] as $rows
        | ($rows | map(select(.[0] == "FAIL"))) as $failures
        | "## Legacy version report (\(.policy | cell))\n\n"
          + (if $failures == [] then "All \($rows | length) checks passed.\n"
             else "| Result | Test | Comment | State Branch |\n| --- | --- | --- | --- |\n"
                  + ($failures | map(table_row + "\n") | join(""))
             end)
    ' "$records" >>"$GITHUB_STEP_SUMMARY"
}
```

Add `legacy) legacy ;;` to the dispatch and to `usage`:

```text
       report-state-branches.sh legacy

legacy appends the legacy report (FAIL rows only) to GITHUB_STEP_SUMMARY and writes
legacy-report.csv with every PASS and FAIL row. It supports one Terraform root only and
omits branches that could not be read.
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `examples/github-actions/report-test.sh`
Expected: every test PASSES. `make shellcheck`: clean.

- [ ] **Step 5: Commit**

```text
feat: write the legacy version report

legacy reproduces an existing report's format exactly for readers that
still depend on it: modules only, the module source as its Test, filters
ignored, a FAIL-only summary table and a CSV of every PASS and FAIL row.
It refuses several Terraform roots because the format cannot show them.

<trailer lines>
```

### Task 10: The report workflow and its release pin

**Files:**
- Create: `examples/github-actions/.github/workflows/tf-version-bump-report.yml`
- Modify: `examples/github-actions/report-test.sh`
- Modify: `scripts/update-actions-release-pin.sh`
- Modify: `release_workflow_test.go:576-585` (`actionsReleasePinFiles`)

**Interfaces:**
- Consumes: `report-state-branches.sh` (Tasks 6-9); the update callers' `with:` blocks; `discover-state-branches.sh`.
- Produces: the workflow; the pin updater maintaining `REPORT_TF_VERSION_BUMP_VERSION` and `REPORT_TF_VERSION_BUMP_ARCHIVE_SHA256` in it.

- [ ] **Step 1: Write the failing wiring test**

Add to `report-test.sh`:

```bash
test_workflow_reports_each_policy_read_only() {
    [[ -f "$REPORT_WORKFLOW" ]] || fail 'the report workflow does not exist'
    local callers
    callers=$(for policy in nonproduction production; do
        yq -o=json '.jobs.automation.with' "$SCRIPT_DIR/.github/workflows/tf-version-bump-$policy.yml"
    done | jq -s .)
    yq -o=json '.' "$REPORT_WORKFLOW" | jq -e --argjson callers "$callers" '
        .jobs.report as $job
        | ($job.steps | map({key: (.name // "checkout"), value: .}) | from_entries) as $steps
        | .permissions == {contents: "read"} and (.jobs | keys) == ["report"]
          and $job.permissions == {contents: "read"}
          and $job.if == "${{ github.ref == format('"'"'refs/heads/{0}'"'"', github.event.repository.default_branch) }}"
          and $job.strategy["fail-fast"] == false
          and ([$job.strategy.matrix.include[] | {policy, config_path, branch_prefixes, terraform_directories}]
               == [$callers[] | {policy: .automation_policy_id, config_path,
                                 branch_prefixes: .allowed_branch_prefixes, terraform_directories}])
          and [$job.steps[] | .name // "checkout"] == ["checkout", "Discover state branches", "Collect versions",
                                                      "Write version report", "Write legacy version report",
                                                      "Upload version reports"]
          and $steps["Discover state branches"].env.DISCOVERY_ALLOWED_PREFIXES == "${{ matrix.branch_prefixes }}"
          and $steps["Discover state branches"].env.DISCOVERY_POLICY_ID == "${{ matrix.policy }}"
          and $steps["Discover state branches"].env.DISCOVERY_MANUAL_PREFIX == ""
          and ($steps["Discover state branches"].run | contains(">\"$RUNNER_TEMP/branches.json\""))
          and $steps["Collect versions"].id == "collect"
          and $steps["Collect versions"].env.REPORT_BRANCHES == "${{ runner.temp }}/branches.json"
          and $steps["Collect versions"].env.REPORT_POLICY_ID == "${{ matrix.policy }}"
          and $steps["Collect versions"].env.REPORT_CONFIG_PATH == "${{ matrix.config_path }}"
          and $steps["Collect versions"].env.REPORT_TERRAFORM_ROOTS == "${{ matrix.terraform_directories }}"
          and $steps["Collect versions"].env.REPORT_TF_VERSION_BUMP_VERSION == $callers[0].tf_version_bump_version
          and $steps["Collect versions"].env.REPORT_TF_VERSION_BUMP_ARCHIVE_SHA256 == $callers[0].tf_version_bump_archive_sha256
          and ([$steps["Collect versions", "Write version report", "Write legacy version report"].env.REPORT_OUTPUT_DIR]
               | unique == [$steps["Upload version reports"].with.path])
          and $steps["Write version report"].if == null
          and $steps["Write legacy version report"].if == "${{ !cancelled() && steps.collect.outcome == '"'"'success'"'"' }}"
          and $steps["Upload version reports"].if == $steps["Write legacy version report"].if
          and $steps["Upload version reports"].with["retention-days"] == 7
          and $steps["Upload version reports"].with["if-no-files-found"] == "error"
          and ([.. | strings | select(test("secrets\\."))] == [])
    ' >/dev/null || fail 'the report workflow does not wire discovery, collection and both reports read-only'
}
```

Add it to `tests=(…)`. Run `examples/github-actions/report-test.sh test_workflow_reports_each_policy_read_only`.
Expected: `FAIL: the report workflow does not exist`.

- [ ] **Step 2: Create the workflow**

Create `examples/github-actions/.github/workflows/tf-version-bump-report.yml`:

```yaml
name: Terraform version report

on:
  workflow_dispatch:
  schedule:
    - cron: "17 6 * * 1"
      timezone: Australia/Melbourne

permissions:
  contents: read

jobs:
  report:
    name: report (${{ matrix.policy }})
    if: ${{ github.ref == format('refs/heads/{0}', github.event.repository.default_branch) }}
    strategy:
      fail-fast: false
      matrix:
        include:
          - policy: nonproduction
            config_path: .github/tf-version-bump/nonproduction.yml
            branch_prefixes: |
              state/nonproduction/
              state/staging/
              aws-state/nonproduction/
              aws-state/staging/
            terraform_directories: .
          - policy: production
            config_path: .github/tf-version-bump/production.yml
            branch_prefixes: |
              state/production/
              aws-state/production/
            terraform_directories: .
    runs-on: ubuntu-latest
    timeout-minutes: 30
    permissions:
      contents: read
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: true
          ref: ${{ github.sha }}
          path: control
      - name: Discover state branches
        env:
          CONTROL_CHECKOUT: ${{ github.workspace }}/control
          DISCOVERY_ALLOWED_PREFIXES: ${{ matrix.branch_prefixes }}
          DISCOVERY_MANUAL_PREFIX: ""
          DISCOVERY_POLICY_ID: ${{ matrix.policy }}
          DISCOVERY_RUN_ID: ${{ github.run_id }}
          DISCOVERY_RUN_ATTEMPT: ${{ github.run_attempt }}
          DISCOVERY_CONTROL_OID: ${{ github.sha }}
          DISCOVERY_CALLER_REF: ${{ github.ref }}
          DISCOVERY_DEFAULT_BRANCH: ${{ github.event.repository.default_branch }}
        run: '"$CONTROL_CHECKOUT/.github/scripts/discover-state-branches.sh" >"$RUNNER_TEMP/branches.json"'
      - name: Collect versions
        id: collect
        env:
          CONTROL_CHECKOUT: ${{ github.workspace }}/control
          REPORT_POLICY_ID: ${{ matrix.policy }}
          REPORT_CONTROL_CHECKOUT: ${{ github.workspace }}/control
          REPORT_CONFIG_PATH: ${{ matrix.config_path }}
          REPORT_TERRAFORM_ROOTS: ${{ matrix.terraform_directories }}
          REPORT_BRANCHES: ${{ runner.temp }}/branches.json
          REPORT_OUTPUT_DIR: ${{ runner.temp }}/version-report
          REPORT_TF_VERSION_BUMP_VERSION: v1.0.0-rc.11
          REPORT_TF_VERSION_BUMP_ARCHIVE_SHA256: 5560b45e220650e8b18d5836eff05d471f602a6ac970aeeb9628781797f54c85
        run: '"$CONTROL_CHECKOUT/.github/scripts/report-state-branches.sh" collect'
      - name: Write version report
        env:
          CONTROL_CHECKOUT: ${{ github.workspace }}/control
          REPORT_OUTPUT_DIR: ${{ runner.temp }}/version-report
        run: '"$CONTROL_CHECKOUT/.github/scripts/report-state-branches.sh" report'
      - name: Write legacy version report
        if: ${{ !cancelled() && steps.collect.outcome == 'success' }}
        env:
          CONTROL_CHECKOUT: ${{ github.workspace }}/control
          REPORT_OUTPUT_DIR: ${{ runner.temp }}/version-report
        run: '"$CONTROL_CHECKOUT/.github/scripts/report-state-branches.sh" legacy'
      - name: Upload version reports
        if: ${{ !cancelled() && steps.collect.outcome == 'success' }}
        uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7.0.1
        with:
          name: version-report-${{ matrix.policy }}-${{ github.run_id }}-${{ github.run_attempt }}
          path: ${{ runner.temp }}/version-report
          if-no-files-found: error
          retention-days: 7
```

Run: `examples/github-actions/report-test.sh test_workflow_reports_each_policy_read_only` and `make actionlint`.
Expected: PASS; actionlint clean.

- [ ] **Step 3: Write the failing pin-updater test**

In `release_workflow_test.go`, add `"examples/github-actions/.github/workflows/tf-version-bump-report.yml",` to `actionsReleasePinFiles()` after the nonproduction caller.

Run: `go test -count=1 -run 'TestUpdateActionsReleasePin' ./...`
Expected: `TestUpdateActionsReleasePinUpdatesMaintainedFiles` FAILS with `examples/github-actions/.github/workflows/tf-version-bump-report.yml retains the previous release pin`.

- [ ] **Step 4: Maintain the report pin in the updater**

In `scripts/update-actions-release-pin.sh`:

1. After `nonproduction_file=…`, add `report_file="examples/github-actions/.github/workflows/tf-version-bump-report.yml"`, and add `"$report_file"` to `files` after `"$nonproduction_file"`.
2. In each of the four parallel arrays, insert after the two nonproduction entries:
   - `pin_files`: `"$report_file" "$report_file"`
   - `pin_markers`: `"          REPORT_TF_VERSION_BUMP_VERSION:" "          REPORT_TF_VERSION_BUMP_ARCHIVE_SHA256:"` (ten spaces)
   - `current_values`: `"REPORT_TF_VERSION_BUMP_VERSION: $current_version" "REPORT_TF_VERSION_BUMP_ARCHIVE_SHA256: $current_digest"`
   - `new_values`: `"REPORT_TF_VERSION_BUMP_VERSION: $version" "REPORT_TF_VERSION_BUMP_ARCHIVE_SHA256: $digest"`

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -count=1 -run 'TestUpdateActionsReleasePin' ./...`, then `go test -count=1 ./...`, `examples/github-actions/report-test.sh`, `make actionlint`, `make shellcheck`.
Expected: all PASS and clean. `TestUpdateActionsReleasePinIsIdempotent` still passes because the report workflow pins the current release.

- [ ] **Step 6: Commit**

Stage the workflow, `report-test.sh`, `scripts/update-actions-release-pin.sh` and `release_workflow_test.go`:

```text
feat: add the state-branch version report workflow

A read-only workflow reports every discovered state branch against its
policy config once per policy, weekly and on demand, writing the improved
and legacy reports to the job summary and both CSVs to an artefact. A
harness test keeps its policy prefixes, config paths and release pin in
step with the update callers, and the release-pin updater now maintains
it with the rest of the example.

<trailer lines>
```

### Task 11: Document the report

**Files:**
- Modify: `examples/github-actions/README.md` (new section before `## Run and inspect`; install list at line 28-33)

**Interfaces:**
- Consumes: Tasks 6-10.
- Produces: nothing code depends on. The section must not repeat the release version or digest: `scripts/update-actions-release-pin.sh` requires exactly one occurrence of each in this README.

- [ ] **Step 1: Write the section**

In "Install", after the production caller bullet, add:

```markdown
The same copy adds `tf-version-bump-report.yml`, a read-only version report described
[below](#version-report).
```

Before `## Run and inspect`, add:

```markdown
## Version report

`tf-version-bump-report.yml` compares every state branch with its policy's control configuration
without changing anything. It runs on Mondays at 06:17 `Australia/Melbourne`, after both scheduled
update runs, and can be started manually from the default branch. It has read-only repository
access, runs no Terraform and receives no secrets.

One job per policy discovers the policy's branches exactly as the update workflow does, then audits
each branch's discovered commit with `tf-version-bump -audit-file`. Its matrix repeats each caller's
branch prefixes, config path and Terraform directories; the example's harness fails if they differ,
so change both together.

Each job writes two reports to its summary and uploads both as CSV files, with the collected
`records.json`, in an artefact retained for seven days:

- **Version report** (`version-report.csv`): one row per check with `status`, `branch`, `kind`,
  `subject`, `block`, `file`, `actual`, `expected` and `detail` columns. It checks each root's
  presence, whether `main.tf` and `providers.tf` exist, `required_version`, providers and modules.
  A value that already matches passes; a module the config's `ignore_modules`, `ignore_versions` or
  `from` excludes is `SKIP`, naming the filter; a branch that cannot be fetched or parsed is
  `ERROR`. The summary counts every status and lists each branch's non-passing rows.
- **Legacy version report** (`legacy-report.csv`): the columns `Result`, `Test`, `Comment` and
  `State Branch` in an existing report's format, with only FAIL rows in the summary. It checks
  modules and the two files only, ignores the config's filters, names each module by its source
  (so blocks sharing a source produce identical rows), writes `none` for a missing version, and
  supports a single Terraform root.

Version mismatches never fail the job. A branch that cannot be read fails it after both reports are
written, so the gap is visible. Discovery runs exactly as in the update workflow, so a policy whose
prefixes match no branch, or more than 256, fails its report job before any report is written, as it
fails the update run.

Both CSVs keep values exactly as written. A value beginning with `=`, `+`, `-` or `@`, such as the
valid Terraform pin `= 5.0.0`, may be evaluated as a formula by a spreadsheet that opens the file
directly, so import the CSVs as text instead.
```

- [ ] **Step 2: Verify**

Run: `make docs-check` and `go test -count=1 -run 'TestUpdateActionsReleasePin' ./...`.
Expected: PASS (the `#version-report` anchor resolves; the README still holds one version and one digest occurrence).

- [ ] **Step 3: Commit**

```text
docs: describe the state-branch version report

<trailer lines>
```

### Task 12: Pin the new release, verify and open PR 2 (gated)

Start only once Dan has provided the release tag `v<version>` and its verified Linux x86-64 SHA-256 (see the Gate).

**Files:**
- Modify (by the updater): both callers, the config-validation workflow, the report workflow, `examples/github-actions/test.sh`, `examples/github-actions/README.md`, `docs/ADVANCED-USAGE.md`
- Modify: `release_workflow_test.go` (current-pin literals)

- [ ] **Step 1: Move every example pin**

```bash
scripts/update-actions-release-pin.sh v<version> <linux-x86-64-sha256>
```

Expected output: `Updated GitHub Actions example pin to v<version>`.

- [ ] **Step 2: Update the pin tests' current-release literals**

`release_workflow_test.go` hard-codes the current pin (`v1.0.0-rc.11` and `5560b45e…` at lines 339, 361, 395, 461 and 486) and uses `v1.0.0-rc.12` as a hypothetical next release. Follow the precedent in commit `72c5323` (`claude-git show 72c5323 -- release_workflow_test.go`): replace the current-pin literals with the new version and digest, and if the new release is itself `v1.0.0-rc.12`, move the hypothetical next release to `v1.0.0-rc.13`.

Run: `go test -count=1 -run 'TestUpdateActionsReleasePin' ./...`
Expected: PASS.

- [ ] **Step 3: Full verification**

```bash
go test -count=1 -race ./...
golangci-lint run --timeout=5m
make docs-check && make actionlint && make shellcheck
make test-github-actions
```

Expected: all PASS; `make test-github-actions` runs the processing tests against the new release and finishes with the reconcile and report harnesses' `PASS:` lines.

- [ ] **Step 4: Commit**

```text
build: pin the GitHub Actions example to v<version>

Move every example workflow, the harness and the guides to the release
that contains -audit-file, which the version report needs.

<trailer lines>
```

- [ ] **Step 5: Test-cleanup and review**

Dispatch a separate `test-cleanup` subagent over `report-test.sh` and the `release_workflow_test.go` changes, then `pr-review-toolkit:code-reviewer` over `git diff main...HEAD`. Fix confirmed findings with TDD and re-run Step 3.

- [ ] **Step 6: Ask Dan, then push and open PR 2**

With Dan's go-ahead, push `state-branch-version-report-workflow` and open the PR with `claude-gh pr create --base main --body-file <scratchpad>/pr-2-body.md`, titled `feat: add a state-branch version report workflow`. The body names both reports, the pin move (including the update callers), and the test evidence.
