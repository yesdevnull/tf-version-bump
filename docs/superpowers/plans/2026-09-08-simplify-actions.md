# Simpler Actions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make the copyable branch automation readable while preserving safe publication.

**Architecture:** Discover immutable branch inputs, process and validate in one job,
then publish one checked patch from a separate job. One result replaces chained manifests.

**Tech Stack:** Bash, Git, jq, Terraform, GitHub Actions and existing Go/shell tests.

**Spec:** `docs/superpowers/specs/2026-09-08-simplify-actions-design.md`

## Global Constraints

- Three jobs: discover, process, publish.
- One final patch and one update commit; no backwards compatibility adapter.
- Preserve explicit upgrade opt-in, safe publication and marked lifecycle cleanup.
- All commits created by Codex, including fixtures, must be signed; use
  `/Users/dan/.codex/bin/codex-git`. Stop immediately on signing failure.
- No new runtime or framework. Real Terraform and Git tests; captured GitHub
  boundary commands are component tests, never described as end-to-end tests.
- Do not push, merge, or create a PR during this task.

### Task 1: Implement and test the simplified automation

**Files:** process-state-branch.sh, reconcile-state-branch.sh, reusable workflow,
and their test.sh/reconcile-test.sh harnesses under examples/github-actions.
Discovery and caller configuration stay compatible unless wiring requires edits.

**Interfaces:** Consume existing discovery matrix and PROCESS inputs; produce
the version 3 result.json and candidate.patch specified in the design.
Publication consumes the same contract and workflow-supplied roots/run URL.

- [ ] Read the current scripts and harness, preserve reusable real fixtures.
- [ ] Add a failing combined-pipeline test: run `process`, assert updated Terraform,
  success classification and one patch; apply to exact base and compare content.
  An unchanged invalid resource must report branch-validation, not no-change.
- [ ] Run focused tests and record expected old-contract failures.
- [ ] Implement the combined processing path and compact publication contract.
- [ ] Adapt real provider tests: unset/false retains compatible lock, true upgrades;
  incompatible lock fails without upgrade and succeeds with it.
- [ ] Test formatter inclusion, multiple roots, failed update/init/validate,
  invalid booleans/roots/config, unexpected patch paths and corrupt identity/digest.
- [ ] Test publication with real temporary Git repositories: one owned commit,
  unchanged base, exact lease, foreign-owned update ref and moved base refusal.
- [ ] Test GitHub lifecycle decisions through command capture: success create/edit,
  failure closes PR before issue, no-change closes PR/issue, dry-run and invalid
  result no mutation, API lookup/closure failure stops subsequent actions.
- [ ] Wire three jobs and artifact names with run attempt identity; lint installed
  example layout using existing actionlint runner.
- [ ] Run focused harness and ShellCheck; self-review and commit signed changes.
- [ ] Task review checks contract and quality; fix findings with regression tests.

### Task 2: Document and verify the complete flow

**Files:** examples/github-actions/README.md and docs/ADVANCED-USAGE.md.
**Interfaces:** Describe Task 1's final exported inputs, result files and job flow.

- [ ] Replace old phase descriptions with the three-job flow and single patch/commit.
- [ ] Document trusted-code limits, explicit upgrade semantics and failure lifecycle.
- [ ] Document result/log inspection, supported reruns and signing configuration.
- [ ] Run full appropriate Go tests, build, module verification, docs-check,
  golangci-lint v2.12 and shell/action lint plus the meaningful example tests.
- [ ] Independent test-cleanup pass, then whole-branch code review and fixes.
- [ ] Commit final signed changes and report worktree, size reduction and validation.

## Self-review

Task 1 produces the same result fields Task 2 documents. Runtime and workflow
changes are kept in one task because their interfaces are tightly coupled.
The old stage-specific tests must change, but retained behaviour requires real
test coverage. Baseline `go test ./...` passed in the clean new worktree.
