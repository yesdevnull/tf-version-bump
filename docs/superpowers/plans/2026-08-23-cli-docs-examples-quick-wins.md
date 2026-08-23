# CLI, documentation, and examples quick wins implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add CI check semantics, schema-v2 Terraform reporting, and maintained day-to-day examples, then publish `v1.0.0-rc.10`.

**Architecture:** Keep `-check` as a command-level policy layered over the existing dry-run update paths, while returning update totals to `main` so errors take precedence over exit status 2. Extend the existing physical-file/block identity report recorder to Terraform blocks. Exercise runnable examples through one shell harness invoked by documentation checks.

**Tech Stack:** Go 1.26, HashiCorp HCL/hclwrite, Bash, YAML, JSON, jq, Make, GitHub Actions/GoReleaser.

**Spec:** `docs/superpowers/specs/2026-08-23-day-to-day-quick-wins-design.md`

## Global Constraints

- `-check` exits 0 when current, 2 when updates are required, and 1 on errors.
- `-check` performs no filesystem writes and is incompatible with `-dry-run` and `-report-file`.
- Report schema version 2 has exact Terraform, module, and provider block counts.
- Do not preserve report schema version 1 compatibility.
- Use TDD for production behaviour and observable example-runner behaviour.
- Use Australian/British English in prose and comments.
- All Codex commits must be SSH-signed with the dedicated GenAI key through `/Users/dan/.codex/bin/codex-git`.

---

### Task 1: Commit the approved design and execution plans

**Files:**
- Create: `docs/superpowers/specs/2026-08-23-day-to-day-quick-wins-design.md`
- Create: `docs/superpowers/plans/2026-08-23-cli-docs-examples-quick-wins.md`
- Create: `docs/superpowers/plans/2026-08-23-actions-rc10-migration.md`

**Interfaces:**
- Consumes: Dan's approved choices for the check flag, statuses, report schema, release, and worktree.
- Produces: the reviewed requirements and ordered execution steps for both pull requests.

- [ ] **Step 1: Review the plans against the design**

Confirm every design section maps to a task and that the second pull request starts only after the
rc.10 acceptance checks.

- [ ] **Step 2: Scan for placeholders and inconsistent names**

Run: `rg -n 'T[B]D|T[O]DO|i[m]plement later|a[p]propriate error|a[d]d validation|w[r]ite tests for|s[i]milar to Task' docs/superpowers/specs/2026-08-23-day-to-day-quick-wins-design.md docs/superpowers/plans/2026-08-23-*.md`

Expected: no plan placeholders.

- [ ] **Step 3: Verify local documentation links**

Run: `make docs-check`

Expected: PASS against the new cross-linked plan and specification files.

- [ ] **Step 4: Commit the planning artefacts**

Stage the three files explicitly and commit with subject `docs: design day-to-day quick wins`.

### Task 2: Add the `-check` command contract

**Files:**
- Modify: `command_test.go`
- Modify: `main.go`
- Modify: `CLAUDE.md`

**Interfaces:**
- Consumes: existing `cliFlags.dryRun`, `runConfigFileMode([]string, *cliFlags)`, and `runCLIMode([]string, *cliFlags)`.
- Produces: `cliFlags.check bool`; both mode runners return `(int, error)`, where the integer is the number of proposed/applied update operations; `main` requests exit status 2 only after successful check processing with a positive total.

- [ ] **Step 1: Write focused failing command tests**

Add command-level tests that execute real Terraform fixtures and prove:

- direct module check mode proposes a change, preserves bytes, and exits 2;
- config check mode with module/provider/Terraform changes preserves bytes and exits 2;
- an already-current check exits normally with no writes;
- a malformed selected file exits 1 rather than 2;
- `-check -dry-run`, `-check -report-file`, and `-validate-config ... -check` are rejected with exact diagnostics;
- parsing records `check: true` and existing dry-run parsing remains distinct.

The production mutations caught are an accidental write, wrong status precedence, a missing
conflict, and treating all successful checks as status 2.

- [ ] **Step 2: Run the focused tests and verify RED**

Run: `go test -run 'Test(ParseFlagsContract|CommandCheck|ValidateOperationModes)' ./...`

Expected: FAIL because `-check`, its conflicts, and status 2 do not exist.

- [ ] **Step 3: Implement the minimal command policy**

Add the flag and validation. After mode validation, make check mode use the existing dry-run update
path while retaining a distinct banner. Return update totals from both command-mode runners. In
`main`, process all files, handle any error as status 1, publish only allowed reports, then call
`exitFunc(2)` when check mode found one or more updates.

- [ ] **Step 4: Run focused and complete Go tests**

Run: `go test -run 'Test(ParseFlagsContract|CommandCheck|ValidateOperationModes)' ./...`

Expected: PASS with pristine output.

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 5: Update the internal architecture contract**

Update `CLAUDE.md` to describe check status precedence and the mode-runner update total. Do not add
user-facing cookbook prose yet.

- [ ] **Step 6: Commit check mode**

Stage `main.go`, `command_test.go`, and `CLAUDE.md`; commit with subject
`feat: add CI check mode`.

### Task 3: Report exact Terraform block updates in schema version 2

**Files:**
- Modify: `command_test.go`
- Modify: `terraform_version_test.go`
- Modify: `test_helpers_test.go`
- Modify: `main.go`
- Modify: `CLAUDE.md`

**Interfaces:**
- Consumes: `updateReport.fileIdentity`, existing module/provider recorders, and top-level Terraform block iteration.
- Produces: `updateReport.TerraformBlocksUpdated int`; `recordTerraformBlocks(string, []int)`; `updateTerraformVersionWithCount(string, string, bool) (bool, []int, error)`; `processTerraformVersion(..., *updateReport)`.

- [ ] **Step 1: Write the failing Terraform report tests**

Add command-level report assertions for multiple Terraform blocks, already-current blocks, a
missing `required_version`, two hard-linked paths, config mode, direct mode, and dry-run zeros. Add a
focused updater test proving changed block indexes identify only blocks that would be rewritten.
Update no existing expected report strings yet.

The production mutations caught are counting files instead of blocks, counting semantic no-ops,
double-counting a hard link, and forgetting either direct or config mode.

- [ ] **Step 2: Run focused tests and verify RED**

Run: `go test -run 'Test(CommandReport.*Terraform|UpdateTerraformVersion.*Count)' ./...`

Expected: FAIL because the report has no Terraform field or recorder.

- [ ] **Step 3: Implement Terraform block identity recording**

Refactor the updater through `updateTerraformVersionWithCount`, retain the existing convenience
adapter for tests, pass the report recorder through direct/config processing, and record changed
block indexes only after actual writes. Set `SchemaVersion` to 2 before publishing.

- [ ] **Step 4: Update all schema-v1 report expectations mechanically**

Change existing expected JSON to schema version 2 and include
`"terraform_blocks_updated": 0` or the independently derived non-zero value. Do not weaken exact
JSON assertions.

- [ ] **Step 5: Run focused and complete Go tests**

Run: `go test -run 'Test(CommandReport|UpdateTerraformVersion)' ./...`

Expected: PASS.

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 6: Update the internal report contract and commit**

Update `CLAUDE.md` from report schema version 1 to version 2 and describe the unique Terraform block
count. Stage only the task files and commit with subject `feat: report Terraform block updates`.

### Task 4: Add the automation cookbook and maintained scenarios

**Files:**
- Create: `examples/scenarios/force-add/main.tf`
- Create: `examples/scenarios/force-add/config.yml`
- Create: `examples/scenarios/idempotency/main.tf`
- Create: `examples/scenarios/idempotency/config.yml`
- Create: `examples/run-scenarios.sh`
- Modify: `examples/README.md`
- Modify: `README.md`
- Modify: `docs/USAGE.md`
- Modify: `docs/CONFIGURATION.md`
- Modify: `documentation_test.go`
- Modify: `Makefile`
- Modify: maintained `examples/config-*.yml`
- Modify: maintained `examples/github-actions/.github/tf-version-bump/*.yml`

**Interfaces:**
- Consumes: released/public CLI syntax, local `go build`, scenario fixtures, `jq`, and the config JSON Schema URL.
- Produces: `examples/run-scenarios.sh [--help]`; `make docs-check` executes the scenario acceptance harness.

- [ ] **Step 1: Write the failing scenario-runner contract in Go**

Add a documentation test that runs `examples/run-scenarios.sh` from the repository root, captures
stdout/stderr, and requires a zero exit with a concise success line. Add config discovery for the two
scenario YAML files so the runtime/schema constraint checks own them too.

The production mutation caught is a copyable scenario whose command, fixture, or assertion has
drifted from the real CLI.

- [ ] **Step 2: Run documentation checks and verify RED**

Run: `make docs-check`

Expected: FAIL because `examples/run-scenarios.sh` and the scenario configs do not exist.

- [ ] **Step 3: Add minimal fixtures and runner**

Create the two scenario directories and an executable Bash runner with `--help`, dependency checks,
one temporary workspace, cleanup trap, one repository binary build, precise failure messages, and
observable assertions:

- force-add first warns and preserves the fixture, then adds version `5.0.0` with `-force-add`;
- idempotency applies the combined config, records bytes and modification time, applies it again,
  and proves the second command reports no updates without changing either.

- [ ] **Step 4: Run the runner and documentation checks**

Run: `examples/run-scenarios.sh`

Expected: PASS with one concise success line and no leaked expected warning/error output.

Run: `make docs-check`

Expected: PASS.

- [ ] **Step 5: Write the human-facing cookbook and corrections**

Add schema declarations to every maintained update config. Expand `examples/README.md` with
validation, preview, check-status handling, apply/report/jq, and copy-safe scenario commands. Update
the README quick-start flag list, the usage flag/report/expression contracts, and the configuration
guide's editor setup without duplicating the full reference.

- [ ] **Step 6: Re-run documentation and complete Go tests**

Run: `make docs-check`

Expected: PASS.

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 7: Commit documentation and examples**

Stage the listed documentation, Makefile, runner, configs, scenarios, and documentation test. Commit
with subject `docs: add automation cookbook and scenarios`.

### Task 5: Clean up tests and review the first pull request

**Files:**
- Modify only branch-touched `*_test.go` files if cleanup removes slop.
- Create: reviewer findings artefact only if the selected review skill requires it.

**Interfaces:**
- Consumes: all PR A commits versus `origin/main`.
- Produces: a subtractive test-cleanup commit if warranted and resolved adversarial-review findings.

- [ ] **Step 1: Run the mandatory test-cleanup subagent**

Commit all implementation work, invoke the `test-cleanup` skill in a fresh subagent on this branch,
classify every touched test, run the relevant suite and coverage, and commit only subtractive test
changes. If nothing is removed, record that result without an empty commit.

- [ ] **Step 2: Request adversarial review**

Use the repository's peer/adversarial review workflow on the complete diff. Verify every finding
against code and tests before accepting it. Apply confirmed fixes with TDD and signed commits; reply
to rejected findings with technical evidence.

- [ ] **Step 3: Re-run cleanup if review fixes changed tests materially**

Invoke a fresh cleanup pass only if review fixes added or substantially rewrote tests.

### Task 6: Verify, merge, and publish rc.10

**Files:**
- No source changes expected.

**Interfaces:**
- Consumes: reviewed PR A branch with signed commits.
- Produces: merged `main`, annotated tag `v1.0.0-rc.10`, published release artefacts, and the verified Linux x86-64 archive SHA-256 needed by PR B.

- [ ] **Step 1: Run fresh pre-PR verification**

Run each command separately and inspect its complete output:

```bash
go mod download
go mod verify
go mod tidy -diff
go test -v -race -coverprofile=coverage.out -covermode=atomic ./...
go tool cover -func=coverage.out
golangci-lint run --timeout=5m
go build -o tf-version-bump .
make docs-check
make test-github-actions
```

Expected: every command exits 0, output is pristine, and total coverage remains at least 90%.

- [ ] **Step 2: Verify branch state and signatures**

Inspect `origin/main...HEAD`, confirm only intended files changed, and verify every PR commit reports a
good signature from Dan's GenAI key. Stop on any signing failure.

- [ ] **Step 3: Push, open the pull request, and verify CI**

Push through the Codex wrapper, create a PR with a body covering behaviour, report migration, docs,
tests, and the planned rc.10 dependency. Wait for every required check and resolve failures rather
than bypassing them.

- [ ] **Step 4: Rebase-merge and refresh local main**

Rebase-merge only after all branch commits are signed and checks pass. Pull `main` with rebase and
verify the merged tree matches the reviewed PR result.

- [ ] **Step 5: Re-run release-gate verification on merged main**

Repeat the full commands from Step 1 on the exact merged commit. Confirm a clean worktree.

- [ ] **Step 6: Create and push the annotated release tag**

Create annotated tag `v1.0.0-rc.10` with message `Release v1.0.0-rc.10` through the Codex Git wrapper
and push that exact tag. Do not move or recreate the tag after publication.

- [ ] **Step 7: Verify the published release**

Wait for the release and provenance workflows. Confirm six platform archives, four Linux packages,
the checksum manifest, and the in-toto provenance file. Download the Linux x86-64 archive and
checksum manifest, verify the archive checksum, verify provenance using the documented command, run
the extracted binary's `-version`, and record its SHA-256 for the Actions migration.
