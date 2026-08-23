# GitHub Actions rc.10 migration implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate the copyable GitHub Actions example to the verified rc.10 artefact and use standalone configuration validation.

**Architecture:** Start a fresh feature branch from the rc.10 release commit. Change the immutable release pin, strict report reader, and config-validation workflow in one tested unit so the POC never combines rc.10 report output with an rc.9 parser.

**Tech Stack:** Bash, GitHub Actions YAML, jq, yq, Docker-backed Terraform harness, actionlint.

**Spec:** `docs/superpowers/specs/2026-08-23-day-to-day-quick-wins-design.md`

## Global Constraints

- Start only after `v1.0.0-rc.10` is published and independently accepted.
- Use the exact verified rc.10 Linux x86-64 archive SHA-256.
- Accept only update report schema version 2; do not preserve schema version 1 compatibility.
- Keep the configuration-validation workflow read-only and independent of Terraform fixtures.
- Do not propagate Terraform counts into the POC manifest or pull-request body.
- Use TDD for harness-visible workflow and script behaviour.
- All Codex commits must be SSH-signed with the dedicated GenAI key through `/Users/dan/.codex/bin/codex-git`.

---

### Task 1: Establish the rc.10 migration branch

**Files:**
- No source changes.

**Interfaces:**
- Consumes: merged `main`, published `v1.0.0-rc.10`, verified archive digest.
- Produces: clean isolated topic branch for the POC migration.

- [ ] **Step 1: Refresh and verify main**

Pull `main` with rebase through the Codex wrapper, verify the release tag resolves to the current
commit, and confirm the worktree is clean.

- [ ] **Step 2: Create an isolated topic worktree**

Create a descriptively named branch and worktree using the worktree skill. Run `go test ./...` as the
baseline.

### Task 2: Change strict harness expectations first

**Files:**
- Modify: `examples/github-actions/test.sh`
- Modify: `examples/github-actions/reconcile-test.sh` only if fixture report payloads reach the strict released-report reader.

**Interfaces:**
- Consumes: rc.10 tag and digest; existing `accumulate_update_report` boundary.
- Produces: tests requiring schema-v2 report validation, the Terraform count key, rc.10 pins, and direct config validation.

- [ ] **Step 1: Update the release constants and valid report fixture**

Change the harness release URL/version/digest to the independently verified rc.10 values. Change
the controlled valid report to exact JSON keys `schema_version`, `terraform_blocks_updated`,
`module_blocks_updated`, and `provider_blocks_updated`, with schema version 2.

- [ ] **Step 2: Add report-contract failure rows**

Require rejection of schema version 1, a missing Terraform count, a negative Terraform count, a
fractional Terraform count, and an extra key. Retain the existing malformed/missing/module/provider
contract rows.

The production mutations caught are accepting the old schema, ignoring malformed new counts, or
weakening the exact-key boundary.

- [ ] **Step 3: Change the config-workflow structural assertion**

Require the validation step to call `-validate-config` for both control files and contain no
temporary Terraform fixture, `-pattern`, `-config`, or `-dry-run`. Retain the immutable archive,
checksum-before-extraction, reported-version, read-only permission, and no-secret assertions.

The production mutation caught is falling back to update-mode validation or gaining write-capable
workflow behaviour.

- [ ] **Step 4: Run the focused harness and verify RED**

Run: `make test-github-actions`

Expected: FAIL at the report and config-validation expectations because production still consumes
rc.9/schema v1 and creates a fixture dry run.

### Task 3: Migrate the POC atomically to rc.10

**Files:**
- Modify: `examples/github-actions/.github/scripts/process-state-branch.sh`
- Modify: `examples/github-actions/.github/workflows/tf-version-bump-config-validation.yml`
- Modify: `examples/github-actions/.github/workflows/tf-version-bump-nonproduction.yml`
- Modify: `examples/github-actions/.github/workflows/tf-version-bump-production.yml`
- Modify: `examples/github-actions/README.md`

**Interfaces:**
- Consumes: strict expectations from Task 2 and published rc.10.
- Produces: a schema-v2 report reader that validates but does not propagate Terraform counts; direct standalone config validation; consistent rc.10 release pins.

- [ ] **Step 1: Update the report reader minimally**

Require exactly the four schema-v2 keys and validate all three counts as non-negative integers.
Continue adding only module/provider counts to the existing preparation manifest.

- [ ] **Step 2: Replace fixture dry runs with standalone validation**

Remove temporary fixture creation and loop over both configs with
`"$TF_VERSION_BUMP_BINARY" -validate-config "$config"`. Keep download cleanup under `always()` and
update every filename/version occurrence to rc.10.

- [ ] **Step 3: Update caller pins and operator documentation**

Use rc.10 and its verified digest in both callers. Update the README release statement and explain
that pull requests validate only the YAML runtime contract without selecting Terraform files.

- [ ] **Step 4: Run focused and full harness verification**

Run: `make test-github-actions`

Expected: PASS, including real rc.10 download, checksum/version verification, strict schema-v2
reports, direct config validation, Docker-backed Terraform processing, and actionlint.

Run: `make docs-check`

Expected: PASS.

- [ ] **Step 5: Commit the migration**

Stage only the Actions example scripts, workflows, tests, and README. Commit with subject
`chore: migrate Actions example to rc.10`.

### Task 4: Clean up, review, verify, and merge the second pull request

**Files:**
- Modify only touched test scripts if the subtractive cleanup pass removes slop.

**Interfaces:**
- Consumes: complete PR B diff.
- Produces: reviewed and merged Actions example migration.

- [ ] **Step 1: Run mandatory test cleanup**

Invoke the `test-cleanup` skill in a fresh subagent. Classify all test changes, run the Actions
harness, and commit only subtractive test changes if warranted.

- [ ] **Step 2: Request adversarial review and resolve findings**

Review the complete diff, verify findings technically, and fix confirmed problems with failing tests
first. Re-run cleanup only if review adds substantial tests.

- [ ] **Step 3: Run fresh full verification**

Run separately:

```bash
go mod verify
go mod tidy -diff
go test -v -race -coverprofile=coverage.out -covermode=atomic ./...
golangci-lint run --timeout=5m
go build -o tf-version-bump .
make docs-check
make test-github-actions
```

Expected: every command exits 0 with pristine output and coverage remains at least 90%.

- [ ] **Step 4: Verify signatures, push, and create the pull request**

Confirm all branch commits are correctly signed, push through the Codex wrapper, open the PR, and
wait for all required checks.

- [ ] **Step 5: Rebase-merge and refresh local main**

Rebase-merge after checks pass, pull local `main` with rebase, and verify the merged tree and clean
status. No additional release is required.
