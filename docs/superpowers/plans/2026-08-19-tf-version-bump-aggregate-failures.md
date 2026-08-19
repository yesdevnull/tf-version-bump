# Aggregate CLI Failure and v1.0.0-rc.8 Release Plan

> **For Codex:** Use `superpowers:executing-plans` to implement this plan. Follow
> `superpowers:test-driven-development` one behaviour at a time. After implementation, run the
> `test-cleanup` skill in a separate subagent, then use `superpowers:verification-before-completion`.

**Goal:** Make every CLI mode finish processing selected files but return a non-zero process status
when any selected file fails, document that contract, and publish the first immutable release that
the GitHub Actions automation may consume.

**Architecture:** The three processing paths return both their update count and aggregate error
count. Mode runners print the existing summaries, then return an error when the aggregate count is
non-zero; `main` converts that error to the existing fatal exit path. Successful output remains
unchanged and later files continue after an earlier failure. Release publication is a separate,
explicitly authorised gate after the code lands on `main`.

**Tech stack:** Go 1.25+, standard `testing`, Markdown documentation, GoReleaser, GitHub Actions.

---

## Constraints

- Work on a topic branch and use `/Users/dan/.codex/bin/codex-git` for every Git operation.
- Every Codex-created commit and tag must be signed. Stop immediately if signing fails.
- Do not push a branch or release tag without Dan's explicit approval at that step.
- Preserve per-file continuation and existing successful output.
- Do not introduce a compatibility flag for the old zero-exit behaviour; that would require Dan's
  separate approval.
- Add one focused failing test before each behaviour change. A missing helper is not sufficient
  evidence for multiple behaviours.

### Task 1: Aggregate module-update errors

**Files:**

- Modify: `main.go`
- Modify: `main_integration_test.go`
- Modify: `cli_functions_test.go`
- Modify: `coverage_test.go`
- Modify: `integration_config_test.go`

**Step 1: Add one failing module-mode test**

Add a focused test that processes two real files in deterministic order: malformed HCL first and
a valid matching module second. Capture the real runner result and output. Assert that the valid
file is updated, the malformed-file diagnostic is captured exactly, and the runner reports
failure.

Run:

```bash
go test -run TestRunCLIModeReportsModuleFileFailure -count=1
```

Expected: FAIL because module processing currently returns only an update count and the mode runner
cannot report the earlier file error.

**Step 2: Implement only module error aggregation**

Change `processFiles` to return update and error counts. Increment the error count where the
existing diagnostic is logged, continue processing, and have module CLI mode return an aggregate
error after printing its existing summary.

Run the focused test again and expect PASS.

**Step 3: Cover module config mode separately**

Add a focused config-mode test with the same malformed-then-valid ordering. Assert continuation and
a failing result, run it RED, make the smallest config-runner change, and rerun it GREEN.

**Step 4: Run the affected suite and commit**

```bash
go test -run 'TestRun(CLI|Config)ModeReportsModuleFileFailure' -count=1
go test ./...
```

Commit the focused change with a signed commit such as `fix: report aggregate module update
failures`.

### Task 2: Aggregate Terraform and provider errors

**Files:**

- Modify: `main.go`
- Modify: `main_integration_test.go`
- Modify: `cli_functions_test.go`
- Modify: `integration_config_test.go`
- Modify: `terraform_provider_test.go`

**Step 1: Add and satisfy the Terraform-version CLI behaviour**

Add a test with a malformed Terraform file followed by a valid `required_version` file. Assert the
valid file is updated and the runner reports failure. Run only that test to prove RED, change
`processTerraformVersion` to return update and error counts, propagate the failure through CLI mode,
then rerun it GREEN.

**Step 2: Add and satisfy the Terraform-version config behaviour**

Add an independent `runConfigFileMode` test using a real config that selects the malformed and valid
Terraform-version files. Prove RED because config mode still discards the aggregate error, propagate
that error through config mode without changing continuation, then rerun GREEN.

**Step 3: Add and satisfy the provider-version CLI behaviour**

Repeat the focused RED/GREEN cycle for a malformed file followed by a valid `required_providers`
file through CLI mode. Change `processProviderVersion` and its CLI caller only as required.

**Step 4: Add and satisfy the provider-version config behaviour**

Add a separate `runConfigFileMode` RED using a real provider config, then propagate the provider
aggregate error through config mode and rerun GREEN. Do not treat the Terraform config test as
evidence for this distinct path.

**Step 5: Add mixed-config aggregation and continuation**

Add a config containing module, Terraform, and provider operations where an earlier operation has a
malformed selected file and later operations have valid updates. Assert every later operation still
runs, every successful change is written, and the runner returns one aggregate failure after the
final summary. Prove RED before changing the config-mode orchestration, then implement the smallest
cross-operation aggregation and rerun GREEN.

**Step 6: Add the all-success regressions**

Add a focused test for each mode showing that multiple valid files retain a successful result and
the existing summary. This guards against treating “no updates” as an error.

**Step 7: Run the affected suite and commit**

```bash
go test -run 'TestRun.*(Terraform|Provider|MixedConfig).*Failure|TestRun.*AllFilesSucceed' -count=1
go test ./...
```

Commit with a signed commit such as `fix: propagate Terraform and provider file failures`.

### Task 3: Document and verify the process-status contract

**Files:**

- Modify: `docs/USAGE.md`
- Modify: `main_coverage_test.go`

**Step 1: Add an end-to-end main-path status test**

Invoke `main` through the repository's existing `stubExit` and `withFlagArgs` harness with an
explicit `-config` argument whose real config selects malformed and valid files. Assert exit code 1,
the exact captured diagnostic, and that the later valid file changed. This tests the complete public
config command path consumed by the automation rather than only helper return values, without adding
a second test binary harness. A separate CLI-mode main test may remain for its distinct flag path.

Run the focused test RED before the final `main` propagation, implement the smallest change from a
runner error to the existing fatal exit mechanism, then rerun it GREEN.

**Step 2: Correct the usage documentation**

Replace the statement that individual file errors leave a zero exit status. State that processing
continues, every file-level diagnostic is written, and the command exits non-zero after the final
summary when any selected operation failed.

Human prose does not receive a grep-based test. Review the rendered section directly against the
end-to-end test's observed contract.

**Step 3: Verify and commit**

```bash
gofmt -w main.go main_integration_test.go cli_functions_test.go coverage_test.go \
  integration_config_test.go terraform_provider_test.go main_coverage_test.go
go test -run TestCommandReportsAggregateFileFailure -count=1
go test -race ./...
go build ./...
golangci-lint run --timeout=5m
```

Run `test-cleanup` in a separate subagent. Apply only removals that preserve all distinct behaviour
coverage, rerun the full commands above, inspect the complete diff, then create a signed commit such
as `docs: define aggregate CLI failure status`.

### Task 4: Merge and publish v1.0.0-rc.8

This task changes remote state and begins only after review, branch integration, and Dan's explicit
release approval.

**Step 1: Verify the release commit on main**

Confirm the approved commits are on current `main`, every PR-branch commit is signed, the worktree
is clean, and the complete repository verification is fresh:

```bash
make test-coverage
golangci-lint run --timeout=5m
go build ./...
```

Run a local GoReleaser snapshot as described in `docs/RELEASING.md`, inspect its output, and confirm
it did not leave unexpected source changes.

**Step 2: Obtain explicit authorisation**

Stop and ask Dan before creating or pushing `v1.0.0-rc.8`. Do not infer tag approval from approval
to implement this plan.

**Step 3: Create and push the signed annotated tag**

Use `/Users/dan/.codex/bin/codex-git` for both operations. Verify the tag signature locally before
pushing. If signing or verification fails, stop; do not create an unsigned replacement.

**Step 4: Verify published distribution**

Wait for the release and provenance workflows. Confirm all expected archives, packages, checksum
manifest, and provenance exist. Download the Linux amd64 archive used by the automation, verify its
SHA-256 checksum and SLSA provenance using the documented commands, then run its `-version` output
and the aggregate-failure scenario against that independently downloaded binary.

Record the release URL, archive checksum, provenance verification result, and acceptance transcript.
Only then is the GitHub Actions implementation plan unblocked.
