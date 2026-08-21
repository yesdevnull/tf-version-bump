# Update Report Review Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make machine-readable update counts unique per physical HCL entry, prevent report destinations from overwriting inputs, fail before Terraform mutation when a report cannot be prepared, and correct contributor documentation.

**Architecture:** Keep the existing update order and accepted configuration shape. Record changed block identities as canonical file path plus deterministic HCL block indexes, and derive the public counters from identity sets. Prepare a temporary report in the destination directory before updates, reject aliases of selected inputs, and rename the completed temporary report into place only after successful processing.

**Tech Stack:** Go 1.26, standard library filesystem APIs, HashiCorp HCL, existing command-level test harness.

**Spec:** `/tmp/tf-version-bump-par-findings.md`

## Global Constraints

- Preserve all existing CLI and configuration behaviour except the reviewed defects.
- Do not reject duplicate or sequential configuration entries.
- Use real temporary files and directories; do not mock filesystem behaviour.
- Follow red-green-refactor separately for counting and destination safety.
- Keep all Codex-created commits signed with the repository's Codex git wrapper.

---

### Task 1: Count unique changed blocks

**Files:**
- Modify: `command_test.go`
- Modify: `main.go`

**Interfaces:**
- Consumes: the existing `updateReport` JSON fields and config-mode update order.
- Produces: internal report methods that record canonical file/block identities and keep `ModuleBlocksUpdated` and `ProviderBlocksUpdated` equal to the number of unique identities.

- [ ] **Step 1: Write the failing duplicate-target command test**

Add a command-level test with one module block and one provider entry. Configure two sequential updates for each target:

```yaml
providers:
  - name: aws
    version: "~> 5.0"
  - name: aws
    version: "~> 6.0"
modules:
  - source: example/module
    version: 2.0.0
  - source: example/module
    version: 3.0.0
```

Assert the final versions are `~> 6.0` and `3.0.0`, while the report is exactly:

```json
{
  "schema_version": 1,
  "module_blocks_updated": 1,
  "provider_blocks_updated": 1
}
```

- [ ] **Step 2: Verify the test fails for inflated counts**

Run:

```bash
go test -run TestCommandReportCountsEachBlockOnce -v
```

Expected: FAIL because both counters are `2`.

- [ ] **Step 3: Implement unique identity recording**

Retain the exported JSON counter fields and add unexported identity sets to `updateReport`. Change counted update helpers to return deterministic identities:

```go
type changedBlock struct {
    kind  string
    index string
}
```

Use the canonical selected-file path plus top-level and nested block indexes as the identity. Record each identity through methods on `updateReport`; only the first insertion increments its public counter. Preserve the existing human-readable `updated` booleans and sequential update behaviour.

- [ ] **Step 4: Verify the focused test and existing report tests pass**

Run:

```bash
go test -run 'TestCommandReport(CountsEachBlockOnce|PreservesSameTargetHumanOutput)|TestCommandWritesExactUpdatedBlockCounts' -v
```

Expected: PASS with pristine output.

- [ ] **Step 5: Commit the counting fix**

Stage `command_test.go` and `main.go`, then create a signed commit named `fix: count unique updated blocks`.

### Task 2: Reserve a safe report destination before updates

**Files:**
- Modify: `command_test.go`
- Modify: `main.go`

**Interfaces:**
- Consumes: `flags.reportFile`, selected Terraform paths, and optional `flags.configFile`.
- Produces: a prepared report destination that owns a same-directory temporary file, rejects input aliases, cleans up on ordinary failure, and publishes JSON by rename after successful updates.

- [ ] **Step 1: Write failing collision tests**

Add command-level subtests for a report path equal to the selected Terraform path and equal to the YAML config path. Assert exit code `1`, a collision diagnostic, and byte-for-byte unchanged input files.

- [ ] **Step 2: Verify the collision tests fail destructively**

Run:

```bash
go test -run TestCommandRejectsReportInputCollision -v
```

Expected: FAIL because the current command processes and overwrites the colliding destination.

- [ ] **Step 3: Implement collision validation before updates**

Resolve absolute paths, compare clean paths, and use `os.SameFile` when both paths exist so symlink and hard-link aliases are rejected. Validate against every selected Terraform path and the config path before running either update mode.

- [ ] **Step 4: Verify collision tests pass**

Run:

```bash
go test -run TestCommandRejectsReportInputCollision -v
```

Expected: PASS with unchanged fixtures.

- [ ] **Step 5: Write the failing unusable-destination test**

Run a real module update with `-report-file` under a nonexistent parent directory. Assert exit code `1`, a preparation diagnostic, and unchanged Terraform content.

- [ ] **Step 6: Verify the destination test fails after mutation**

Run:

```bash
go test -run TestCommandRejectsUnusableReportDestinationBeforeUpdating -v
```

Expected: FAIL because the Terraform version changes before `os.WriteFile` reports the missing directory.

- [ ] **Step 7: Implement temporary reservation and publication**

Before updates, create a temporary file in the report destination directory. After successful updates, marshal JSON, apply mode `0600`, write and close the temporary file, then rename it to the requested path. On ordinary processing or write failure, close and remove the temporary file before exiting.

- [ ] **Step 8: Verify destination and report tests pass**

Run:

```bash
go test -run 'TestCommand(RejectsReportInputCollision|RejectsUnusableReportDestinationBeforeUpdating|WritesExactUpdatedBlockCounts|ConfigDryRunOutputContract)' -v
```

Expected: PASS with pristine output.

- [ ] **Step 9: Commit the destination fix**

Stage `command_test.go` and `main.go`, then create a signed commit named `fix: prepare report output safely`.

### Task 3: Correct contributor documentation

**Files:**
- Modify: `CLAUDE.md`

**Interfaces:**
- Consumes: current production helper names in `main.go`.
- Produces: contributor guidance that names `updateModuleVersionWithCount`, `updateModuleBlockResult`, `updateProviderVersionWithCount`, `updateProviderBlockSyntaxResult`, and `updateProviderAttributeVersionResult` accurately.

- [ ] **Step 1: Update the architecture and testing examples**

Correct the update-flow paragraphs, module precedence owner, provider helper chain, and representative call. The call must capture all three return values:

```go
updated, blocksChanged, err := updateModuleVersionWithCount(...)
```

- [ ] **Step 2: Verify documentation**

Run:

```bash
make docs-check
```

Expected: PASS.

- [ ] **Step 3: Commit the documentation correction**

Stage `CLAUDE.md`, then create a signed commit named `docs: correct report helper architecture`.

### Task 4: Cleanup and final verification

**Files:**
- Review: all branch changes against `main`
- Verify against: `/tmp/tf-version-bump-par-findings.md`

**Interfaces:**
- Consumes: completed Tasks 1-3.
- Produces: a clean, fully verified, signed topic branch with every review finding adjudicated.

- [ ] **Step 1: Run the required independent test-cleanup pass**

Use the `test-cleanup` skill in a separate subagent. Apply only legitimate cleanup findings through a new TDD-preserving signed commit.

- [ ] **Step 2: Run full validation**

Run:

```bash
make test-coverage
golangci-lint run --timeout=5m
go vet ./...
make docs-check
make build
make test-github-actions TEST_GIT=/tmp/tf-version-bump-signed-test-git
```

Expected: every command passes, race-enabled coverage remains at least 90%, and lint output contains zero issues.

- [ ] **Step 3: Verify the peer-review findings**

Use `$verify` against `/tmp/tf-version-bump-par-findings.md`, checking each finding and collateral behaviour from the cumulative `main...HEAD` diff.

- [ ] **Step 4: Verify repository integrity**

Confirm the worktree is clean, `git diff --check main...HEAD` passes, and every `main..HEAD` commit has a good signature.
