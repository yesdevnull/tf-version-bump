# Immutable Releases and Reproducible Builds Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development
> (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Publish complete releases atomically and produce verifiable, reproducible GoReleaser
artifacts.

**Architecture:** GoReleaser creates a draft containing binary and package assets, the reusable
SLSA workflow adds provenance to that draft, and a dependent job publishes it. The GoReleaser build
uses deterministic source metadata and exact release toolchain versions.

**Tech Stack:** Go 1.26.6, GoReleaser v2.17.1, GitHub Actions, SLSA generic generator, YAML v3, Go
testing.

**Spec:** `docs/superpowers/specs/2026-08-21-immutable-releases-design.md`

## Global Constraints

- Use `/Users/dan/.codex/bin/codex-git` for every Git operation.
- Every commit must be signed; stop immediately if signing fails.
- Preserve the current archive, package, checksum, platform, changelog, and release-note behaviour.
- Publish no GitHub release until GoReleaser assets and SLSA provenance have both succeeded.
- Pin release CI to Go 1.26.6 and GoReleaser v2.17.1.
- Do not add release signing systems, package formats, or compatibility paths.

---

### Task 1: Enforce the release configuration contract

**Files:**

- Modify: `release_workflow_test.go`
- Modify: `.goreleaser.yaml`
- Modify: `.github/workflows/release.yml`

**Interfaces:**

- Consumes: GoReleaser v2 configuration and GitHub Actions workflow YAML.
- Produces: a draft release containing GoReleaser and SLSA assets, published by the
  `publish-release` job after both producer jobs succeed.

- [ ] **Step 1: Write the failing reproducibility test**

Add `TestGoReleaserBuildConfigurationIsReproducible`. Parse `.goreleaser.yaml` with YAML v3 and
assert that the build has `-trimpath`, uses `{{ .CommitDate }}` rather than `{{ .Date }}`, retains
`{{ .CommitTimestamp }}`, enables `gomod.proxy`, and has no pre-build hooks that mutate the tagged
checkout.

- [ ] **Step 2: Run the reproducibility test and verify RED**

Run:

```bash
go test -run TestGoReleaserBuildConfigurationIsReproducible -count=1
```

Expected: FAIL because `-trimpath` and `gomod.proxy` are absent, the ldflag uses `.Date`, and the
mutating hooks remain.

- [ ] **Step 3: Implement the minimal reproducible GoReleaser configuration**

Remove the `before` hooks, add `flags: [-trimpath]`, change the date ldflag to
`{{ .CommitDate }}`, and add `gomod.proxy: true`. Preserve the existing build matrix and packaging.

- [ ] **Step 4: Run the reproducibility test and verify GREEN**

Run the focused test again and require PASS.

- [ ] **Step 5: Write the failing immutable-publication tests**

Add focused tests that parse both YAML files and assert:

- GoReleaser creates a draft release.
- SLSA uploads provenance to a draft release.
- `publish-release` depends on both `release` and `assets-provenance`.
- The publishing step changes the existing draft to published.
- Release CI pins Go 1.26.6 and GoReleaser v2.17.1 exactly.

- [ ] **Step 6: Run the publication tests and verify RED**

Run:

```bash
go test -run 'Test(GoReleaserCreatesDraftRelease|ReleaseWorkflowPublishesCompleteDraft|ReleaseWorkflowPinsBuildToolchain)' -count=1
```

Expected: FAIL because the release is currently published by GoReleaser, provenance is added later,
there is no final publisher job, and both toolchain versions float.

- [ ] **Step 7: Implement draft-first publication and exact pins**

Set `release.draft: true`, pass `draft-release: true` to the SLSA workflow, pin release CI to Go
1.26.6 and GoReleaser v2.17.1, and add a `publish-release` job. That job must require both producer
jobs and run `gh release edit` with quoted environment variables to publish the existing tag.

- [ ] **Step 8: Run the focused and package tests**

```bash
go test -run 'Test(GoReleaser|ReleaseWorkflow)' -count=1
go test ./...
```

Require both commands to pass with pristine output.

- [ ] **Step 9: Commit the configuration contract**

Stage only `.goreleaser.yaml`, `.github/workflows/release.yml`, and
`release_workflow_test.go`. Create a signed commit with subject `build: prepare immutable releases`.

### Task 2: Document and verify the release process

**Files:**

- Modify: `docs/RELEASING.md`

**Interfaces:**

- Consumes: the release workflow implemented by Task 1.
- Produces: operator instructions for immutable publication and reproducible local validation.

- [ ] **Step 1: Update the release guide**

Document that releases remain drafts until SLSA provenance is attached, that a failed workflow
leaves a mutable draft, and that Dan must enable immutable releases in the repository settings.
Record the pinned release toolchain and the reason for exact pins. Human prose receives direct
review rather than a source-text test.

- [ ] **Step 2: Validate GoReleaser and create a local snapshot**

Run:

```bash
goreleaser check
goreleaser healthcheck
goreleaser release --snapshot --clean
```

Require all commands to exit zero. Inspect the generated artifacts and confirm the source worktree
contains only intended changes.

- [ ] **Step 3: Run repository verification**

```bash
gofmt -w release_workflow_test.go
go test -race -coverprofile=coverage.out -covermode=atomic ./...
go build ./...
golangci-lint run --timeout=5m
```

Require clean output and zero exit status from every command.

- [ ] **Step 4: Commit the documentation**

Stage only `docs/RELEASING.md` and create a signed commit with subject
`docs: explain immutable release publication`.

- [ ] **Step 5: Run independent cleanup and review**

Run the `test-cleanup` skill in a fresh subagent, then request a separate code review. Resolve every
critical or important finding, rerun the affected tests, and create signed commits for any fixes.

- [ ] **Step 6: Perform final verification**

Repeat the GoReleaser checks, snapshot, race-enabled test suite, build, and linter. Inspect the final
diff and verify every branch commit is signed before updating the existing pull request.

