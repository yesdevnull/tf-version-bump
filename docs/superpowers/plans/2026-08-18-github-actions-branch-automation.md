# GitHub Actions State-Branch Automation Implementation Plan

> **For Codex:** Execute this plan with `superpowers:executing-plans`. Use
> `superpowers:test-driven-development` for every behaviour. After implementation, run the
> `test-cleanup` skill in a separate subagent, then use
> `superpowers:verification-before-completion` and `superpowers:requesting-code-review`.

**Goal:** Ship a copyable GitHub Actions example that safely prepares, validates, and reconciles
Terraform dependency updates across selected state branches, with manual and scheduled production
and non-production callers.

**Architecture:** A reusable workflow discovers immutable state refs, prepares candidates before
provider execution, validates them in a constrained Terraform container under a trusted host
supervisor, verifies candidates in a separate read-only job, and reconciles verified results on a
fresh protected publication runner. The helper scripts keep untrusted values out of workflow source
and split reconciliation into credential-free verification, authenticated preflight, and the
smallest possible publication step.

**Tech stack:** Bash, Git, GitHub CLI, GitHub Actions, jq, Docker,
`tf-version-bump v1.0.0-rc.7`, Terraform 1.15.5, actionlint 1.7.12.

**Spec:** `docs/superpowers/specs/2026-08-18-github-actions-branch-automation-design.md`

---

## Hard prerequisite

Do not begin Task 1 until
`docs/superpowers/plans/2026-08-19-tf-version-bump-aggregate-failures.md` is complete and the
published `v1.0.0-rc.7` Linux artefact has passed checksum, provenance, version, and aggregate-exit
verification. Record the actual Linux x86-64 archive SHA-256 in the release acceptance transcript.
Tests and workflows must download and verify that archive; substituting a working-tree build would
reopen the partial-publication defect.

## Global constraints

- Work on a topic branch and use `/Users/dan/.codex/bin/codex-git` for every Git operation.
- Every Codex-created commit must be signed. Stop immediately if signing fails.
- Do not push or mutate a non-disposable GitHub repository without Dan's explicit approval.
- Use `apply_patch` for edits. Preserve unrelated worktree changes.
- Before starting, pull the latest base with rebase and stop for direction if the worktree is not
  clean.
- Use real Git repositories, bare remotes, Docker containers, executables, and local authenticated
  HTTP services in tests. Do not mock `gh`, provider execution, or Git transport behaviour.
- Test deterministic local payload construction separately from GitHub API behaviour. Verify API,
  token, permissions, event, environment, and queue behaviour in a disposable private GitHub
  repository.
- Introduce one behaviour at a time: focused failing test, observed RED for the intended reason,
  smallest GREEN implementation, focused rerun, then broader suite. A missing script or mode may
  establish only its scaffold, not a batch of unrelated requirements.
- Every checkout uses `persist-credentials: false`. Every untrusted Actions value enters shell via
  `env:`, never direct expression interpolation into `run:`.
- Action pins:
  - `actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1` (`v7.0.1`)
  - `actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e` (`v7.0.0`)
  - `hashicorp/setup-terraform@dfe3c3f87815947d99a8997f908cb6525fc44e9e`
    (`v4.0.1`)
  - `actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a`
    (`v7.0.1`)
  - `actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c`
    (`v8.0.1`)
  - `actions/create-github-app-token@bcd2ba49218906704ab6c1aa796996da409d3eb1`
    (`v3.2.0`)
- Tool pins:
  - `tf-version-bump v1.0.0-rc.7`
  - Terraform `1.15.5`
  - `hashicorp/terraform:1.15.5@sha256:15bf5a08b1fb9c9747c8ff01098aeeefb4aec9a6c24eb13e7661bdf9447e4aee`
  - `github.com/rhysd/actionlint/cmd/actionlint@v1.7.12`

## Planned files

```text
scripts/run-actionlint.sh
examples/github-actions/README.md
examples/github-actions/.github/scripts/discover-state-branches.sh
examples/github-actions/.github/scripts/process-state-branch.sh
examples/github-actions/.github/tf-version-bump/nonproduction.yml
examples/github-actions/.github/tf-version-bump/production.yml
examples/github-actions/.github/workflows/tf-version-bump-reusable.yml
examples/github-actions/.github/workflows/tf-version-bump-nonproduction.yml
examples/github-actions/.github/workflows/tf-version-bump-production.yml
examples/github-actions/test-fixtures/hostile-provider/**
examples/github-actions/test.sh
examples/README.md
docs/ADVANCED-USAGE.md
README.md
Makefile
.github/workflows/ci.yml
```

Keep the two runtime helpers small. `discover-state-branches.sh` owns discovery only.
`process-state-branch.sh` exposes explicit `prepare`, `validate`, `verify`, `prepublish`, and
`publish` modes; each mode validates its own input contract and cannot silently perform a later
phase.

### Task 1: Pin workflow linting and scaffold the harness

**Files:**

- Create: `scripts/run-actionlint.sh`
- Create: `examples/github-actions/test.sh`
- Modify: `Makefile`

1. Add one harness assertion that the actionlint launcher exists and reports 1.7.12. Run it and
   observe RED because the launcher is absent.
2. Add the smallest documented launcher using exactly
   `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12` plus only
   `-ignore '^unexpected key "queue" for "concurrency" section\.'`. It accepts workflow paths, has
   `--help`, explains that GitHub.com is authoritative for the newer property, and emits no output
   on success. Rerun GREEN.
3. Add one harness assertion that both runtime helpers support `--help`. Observe RED, then create
   only minimal executable help/usage scaffolds. Rerun GREEN.
4. Add `branch-automation-test` and `actionlint` Make targets that delegate to these scripts. Do not
   duplicate their command bodies in Make.
5. Run `bash -n` on all three scripts and the focused harness cases. Create a signed scaffold commit.

### Task 2: Discover immutable branch inputs

**Files:**

- Modify: `examples/github-actions/.github/scripts/discover-state-branches.sh`
- Modify: `examples/github-actions/test.sh`

Use a temporary real repository and bare remote. For each item below, add one focused test, run it
RED for that behaviour, implement only that behaviour, and rerun GREEN:

1. Literal allowed prefixes select branches and emit sorted JSON containing branch and exact remote
   OID.
2. An optional manual prefix narrows but cannot widen the allow-list.
3. Overlapping prefixes deduplicate a branch.
4. Empty, malformed, control-character, absolute-looking, no-match, and non-default-ref inputs fail
   with stage-specific diagnostics.
5. Valid hostile refs containing command substitutions, quotes, semicolons, leading dashes,
   Unicode, percent signs, and GitHub's documented injection example remain inert data.
6. Exactly 256 results succeed; 257 fail before matrix emission with a narrowing/partitioning
   instruction.
7. Output binds run ID, run attempt, policy ID, and one immutable control OID. A default-branch move
   during discovery does not change that OID.
8. `automation_policy_id` accepts only `^[a-z0-9][a-z0-9-]{0,31}$`.

Finish by running the complete discovery subset plus `bash -n`, then create a signed commit.

### Task 3: Add command-scoped authenticated Git transport

**Files:**

- Modify: `examples/github-actions/.github/scripts/discover-state-branches.sh`
- Modify: `examples/github-actions/.github/scripts/process-state-branch.sh`
- Modify: `examples/github-actions/test.sh`

1. Add a real authenticated bare HTTP Git service to the test harness. First prove unauthenticated
   `ls-remote`, fetch, and push fail.
2. Add a focused discovery test requiring authenticated `ls-remote`. Observe RED.
3. Implement a token-free `GIT_ASKPASS` helper plus mode-`0600` token file for the exact child Git
   command. Keep the token out of URLs and process arguments, disable prompts, and remove both files
   in unconditional cleanup. Rerun GREEN and inspect the service request.
4. Repeat focused RED/GREEN tests for authenticated fetch, atomic update, and exact-lease deletion
   through the processing helper.
5. Add a failure-path test proving credential files are removed and no repository config or remote
   URL retains the token.

Run the authentication subset under shell tracing disabled, then create a signed commit.

### Task 4: Enforce path and workspace safety

**Files:**

- Modify: `examples/github-actions/.github/scripts/process-state-branch.sh`
- Modify: `examples/github-actions/test.sh`

Introduce each rejection through an independent RED/GREEN cycle:

1. Config paths and Terraform roots must be relative, present, canonical, inside their checkout,
   and unique after canonicalisation.
2. Nested roots are allowed, but each `*.tf` file belongs directly to exactly one configured root.
3. Matching `.tf` files and lock files must be regular, non-symlink files whose canonical paths stay
   under the target root.
4. A repository `.terraform` entry is rejected whether it is a directory, symlink, or has a
   symlinked ancestor.
5. Every Terraform root receives a fresh absolute trusted `TF_DATA_DIR` outside both checkouts.
6. NUL-delimited status inspection rejects every changed or untracked path except a direct `.tf`
   file or its root's `.terraform.lock.hcl`.

Cover escapes into the sibling control checkout and outside both checkouts. Run the focused suite
and create a signed commit.

### Task 5: Prepare immutable candidates

**Files:**

- Modify: `examples/github-actions/.github/scripts/process-state-branch.sh`
- Modify: `examples/github-actions/test.sh`

1. Add a RED test for `prepare` downloading the `v1.0.0-rc.7` Linux x86-64 archive, verifying its
   recorded SHA-256 before extraction, checking the binary's reported version, and running it with
   `*.tf` plus the control config in one real Terraform root. Implement only that download,
   verification, and update path.
2. Add a RED test for `terraform init -upgrade -backend=false -input=false -no-color` using the
   trusted data directory and a 20-minute command timeout. Implement and rerun GREEN.
3. Add separate RED/GREEN cases for a provider root requiring a regular persisted lock, a
   provider-free root legitimately omitting one, and an ignored newly required lock failing without
   force-add.
4. Add a RED test for two roots and deterministic failure attribution. Implement sequential root
   handling and rerun GREEN.
5. Add one manifest field at a time with a focused assertion: schema version; run ID/attempt/policy;
   control/ref/base identity; config and exact tool/archive/image pins; classification; roots and
   provider dependency state; changed paths, modes, and SHA-256 hashes.
6. Add a RED test for an immutable binary patch and collision-resistant artefact key containing run
   ID, attempt, policy, and complete-ref hash. Implement and rerun GREEN.
7. Add failure-bundle cases for aggregate CLI failure and init timeout. Neither may contain a
   publishable patch; the latter is classified `branch-init` for bounded issue reporting.

Run all preparation cases with the released CLI and pinned Terraform version, then create a signed
commit.

### Task 6: Validate inside the constrained container

**Files:**

- Create/modify: `examples/github-actions/test-fixtures/hostile-provider/**`
- Modify: `examples/github-actions/.github/scripts/process-state-branch.sh`
- Modify: `examples/github-actions/test.sh`

1. Build the smallest real Linux hostile provider fixture needed to observe its environment and
   attempted filesystem writes. Pin its test build inputs, place it in a trusted read-only
   filesystem mirror, and generate a host-owned Terraform CLI configuration for test mode only. Do
   not introduce a mock provider, workflow input, or state-branch-controlled mirror/config path.
2. Add a RED `validate` test that verifies the candidate manifest and applies its patch only to a
   disposable validation checkout. Implement that boundary.
3. Add a RED test that starts the digest-pinned image with no Docker socket, dropped capabilities,
   `no-new-privileges`, resource/process limits, runner UID/GID, and only the disposable checkout
   plus trusted data directory mounted. Pass only explicit non-secret Terraform variables. Implement
   the smallest container invocation and rerun GREEN.
4. Add a RED test that checks `terraform version -json` in the image before target content is used.
   Reject a tag/digest/version mismatch.
5. Add focused tests for provider and provider-free init/validate commands, including
   `-lockfile=readonly` only when a lock exists.
6. Add hostile delayed/background attempts against credentials, candidate, lock, control checkout,
   `GITHUB_ENV`, `GITHUB_PATH`, outcome path, and container socket. The constrained container mounts
   the reviewed test mirror and CLI config read-only only when the harness enables its internal test
   mode. The trusted host—not a mounted target process—must create the outcome from the observed
   container status after termination.
7. Add a hanging-provider RED test. Enforce the 20-minute per-root timeout with injectable shorter
   test duration and classify it as `branch-validation`.
8. State in test names and assertions that a zero supervised exit is all this boundary authenticates;
   it cannot prove an adversarial provider's semantic honesty.

Run the tests on a host with Docker available and create a signed commit.

### Task 7: Verify artefacts without credentials

**Files:**

- Modify: `examples/github-actions/.github/scripts/process-state-branch.sh`
- Modify: `examples/github-actions/test.sh`

For each check, add a targeted corrupt bundle, observe RED, implement rejection, and rerun GREEN:

1. Unknown schema, missing/duplicate/unexpected artefact, wrong run ID or attempt, wrong policy,
   control OID, branch, base OID, or candidate digest.
2. Absolute, escaping, duplicate, symlink, non-regular, mode-mismatched, hash-mismatched, or
   undeclared paths.
3. Patch paths outside the declaration or a changed `.tf` file not mapped directly to one root.
4. Contradictory or unknown result classifications.
5. Independent `v1.0.0-rc.7` source reproduction against a fresh exact-base checkout, accepting only
   an exact source diff before applying declared lock bytes.
6. Full-run and failed-job rerun fixtures whose run-attempt-specific artefacts cannot cross-consume.

The resulting `verify` mode writes only a local verified-result file. Add a test proving no GitHub
write token, App key, or signing key exists in its environment or temporary tree. Run the complete
subset and create a signed commit.

### Task 8: Preflight ownership and remote state

**Files:**

- Modify: `examples/github-actions/.github/scripts/process-state-branch.sh`
- Modify: `examples/github-actions/test.sh`

1. Add focused RED/GREEN tests for the exact mapping
   `state/nonproduction/example-thing` to
   `update_state/nonproduction/example-thing`.
2. Define exact machine commit trailers and hidden PR/issue markers containing policy, state/base,
   control OID, run ID, and run attempt. Add parser tests for missing, duplicate, malformed, and
   mismatched markers before accepting valid ownership.
3. Add a foreign-policy collision test. Even with the required branch name, it must fail without
   adoption or mutation.
4. Add tests that an existing ref requires both its marked tip commit and corresponding exact-base/
   exact-head PR to agree. Cover open and closed PRs.
5. Add issue ownership tests requiring the expected marker, policy, and immutable bot author login.
6. Add a `prepublish` test that command-scoped auth fetches current state/update refs and queries
   records only after credential-free verification. The signing key remains absent.
7. Add a test for policy-independent per-state publication locking so overlapping policies cannot
   publish the same update ref concurrently.

Local tests cover parsers and real Git. Defer GitHub API assertions to the disposable-repository
gate. Run the subset and create a signed commit.

### Task 9: Sign and publish with atomic Git guards

**Files:**

- Modify: `examples/github-actions/.github/scripts/process-state-branch.sh`
- Modify: `examples/github-actions/test.sh`

1. Add a RED test that signing material is created only after a verified-result and successful
   preflight. Implement the publish-only key materialisation and unconditional cleanup.
2. Add a RED signed-commit test using a temporary SSH key. Configure signing only in the target
   repository, require explicit author identity, verify locally, and stop without unsigned fallback
   on signing failure. Add a separate unsigned case when no key is configured.
3. Add a RED real-bare-remote test for one atomic push containing a no-op state ref guarded by the
   exact validated-base lease and the create/update automation ref guarded by its expected OID.
   Implement and rerun GREEN.
4. Move the state ref at the push boundary and prove neither ref update is accepted. This test must
   exercise the atomic push, not only an earlier comparison.
5. Add exact-lease tests for automation-ref update and deletion races. Never retry unconditionally.
6. Add post-push and post-PR state checks. On detected movement, roll back only the OID written by
   the current invocation with an exact lease.
7. Add failpoints after ref create/update. A later PR failure exact-lease-deletes a newly created
   ref or restores the previous OID. An ambiguous response first re-reads markers. A compensation
   lease mismatch reports manual recovery and preserves the unseen writer's change.
8. For no-change cleanup, delete the marked ref atomically before closing the PR. Add recovery for
   API failure after deletion.

Run the atomic publication suite repeatedly to expose races, then create a signed commit.

### Task 10: Reconcile PRs, issues, and safe Markdown

**Files:**

- Modify: `examples/github-actions/.github/scripts/process-state-branch.sh`
- Modify: `examples/github-actions/test.sh`

1. Add one Markdown encoder test using hostile branch names, paths, diagnostics, HTML, links, and
   mentions. Escape `&`, `<`, `>`, neutralise `@`, use bounded `<code>`/`<pre>` output, and pass JSON
   values with `jq --arg` or body files.
2. Add deterministic PR-body tests for every required identity, tool/image pin, changed file, root,
   run link, ownership warning, and semantic-validation limitation.
3. Add classification tests with precedence `automation`, `branch-update`, `branch-init`,
   `branch-validation`, `success`. Only update, init, and validation failures produce branch issues;
   auth, signing, lease, manifest, and repository-policy failures do not. Init issues explicitly
   state that registry, authentication, rate-limit, network, or other dependency causes may be
   transient.
4. Add deterministic issue-body tests for exact stage/root/command, bounded captured output, run/log
   links, and stable marker/title.
5. Add lifecycle payload tests: success creates/refreshes one exact PR and closes a marked issue;
   update/init/validation failure closes the stale marked PR, exact-lease-deletes its owned ref,
   then creates or updates one issue; automation failure leaves the prior PR unchanged; recovery
   adds a bounded comment and closes only the correctly authored issue.
6. Add PR edit/close and issue edit/close failpoints. Each must have either explicit compensation or
   a safe idempotent retry determined by re-reading exact markers.

Do not mock `gh`. These local tests call pure payload/decision modes and real Git; Task 15 verifies
API effects. Run the focused suite and create a signed commit.

### Task 11: Wire the reusable workflow

**Files:**

- Create: `examples/github-actions/.github/workflows/tf-version-bump-reusable.yml`
- Modify: `examples/github-actions/test.sh`

Add each workflow contract through a focused structural or actionlint RED/GREEN cycle:

1. Declare the complete typed interface, including the release archive SHA-256, fixed protected-
   environment secrets, semantic-version tag and digest validation, policy regex, paired
   App/signing inputs, and `unattended_checks_safe` gate.
2. Add `discover` with `contents: read`, immutable control SHA, released CLI download/verification,
   command-scoped authenticated discovery, and a 10-minute job timeout.
3. Add `prepare` and `validate` matrices with `fail-fast: false`, `max-parallel`, `contents: read`,
   exact OID checkouts, no credentials/secrets, run-attempt artefact names, unconditional result
   upload, 20-minute operation timeouts, and 30-minute job timeouts.
4. Add a separate `verify` matrix with `always()`, `contents: read`, no publication environment or
   protected secrets, exact artefact enumeration, a 15-minute operation limit, and a 20-minute job
   timeout. It uploads one narrowly scoped verified-result artefact per current run attempt.
5. Add a dependent `publish` matrix on a fresh protected runner with a 10-minute operation limit and
   15-minute job timeout. It validates the verified-result identity without sourcing it, then uses
   App/built-in auth only for `prepublish` and exact child commands; the signing secret is
   materialised only for `publish`.
6. Ensure dry run never references the protected publication environment or enters verification or
   publication, but still executes real preparation and validation and retains diagnostics.
7. Add App token creation pinned to the stated SHA and repository/permission limits. In App mode,
   the caller ceiling leaves the effective `GITHUB_TOKEN` read-only; it is never passed to a write
   child.
8. Add `queue: max` to caller concurrency and to policy-independent state-branch publication
   concurrency, with no in-progress cancellation. Add the exact actionlint 1.7.12 suppression for
   its stale `queue` parser diagnostic and no other warning.
9. Verify every checkout has `persist-credentials: false`, every action pin matches the approved
   SHA, and no untrusted expression appears directly in `run:` source.

Run the pinned actionlint launcher against the temporary consumer-style `.github` tree after every
workflow increment. Only the exact documented `queue` diagnostic may be ignored. Create a signed
commit.

### Task 12: Add production and non-production callers

**Files:**

- Create: `examples/github-actions/.github/workflows/tf-version-bump-nonproduction.yml`
- Create: `examples/github-actions/.github/workflows/tf-version-bump-production.yml`
- Create: `examples/github-actions/.github/tf-version-bump/nonproduction.yml`
- Create: `examples/github-actions/.github/tf-version-bump/production.yml`
- Modify: `examples/github-actions/test.sh`

1. Add the non-production caller with manual `branch_prefix`/`dry_run`, a 04:17 daily
   `Australia/Melbourne` schedule, policy `nonproduction`, the three documented prefixes, root `.`,
   exact release/archive/image pins, and `queue: max` concurrency. Populate the archive digest only
   from the completed `v1.0.0-rc.7` release acceptance record.
2. Add the production caller with the same manual interface, a Sunday 04:43
   `Australia/Melbourne` schedule, policy `production`, and the two production prefixes.
3. Make built-in-token mode the copyable default. Add commented/documented App inputs only with the
   explicit `unattended_checks_safe: true` acknowledgement.
4. Add representative separate config files and validate each with the released CLI against a
   trusted fixture before any matrix is created.
5. Add tests for exact schedules, default-branch-only dispatch, permission ceilings, config paths,
   prefix separation, and `queue: max`. All three rapid runs completing is a real-service assertion,
   not an invented local scheduler.

Run actionlint and the caller subset, then create a signed commit.

### Task 13: Integrate repository CI

**Files:**

- Modify: `Makefile`
- Modify: `.github/workflows/ci.yml`
- Modify: `examples/github-actions/test.sh`

1. Add a focused self-test proving `make branch-automation-test` invokes shell syntax checks, the
   full harness, Docker-boundary tests, and `scripts/run-actionlint.sh`; it must not call a bare PATH
   `actionlint`.
2. Add one CI step on Go 1.25 for the branch-automation target while preserving the existing
   `examples/update-branches_test.sh` coverage.
3. If Docker is unavailable, fail with an explicit prerequisite diagnostic; do not skip hostile
   boundary tests silently.
4. Run the Make target locally and inspect pristine output. Create a signed commit.

### Task 14: Document installation, limits, and recovery

**Files:**

- Create: `examples/github-actions/README.md`
- Modify: `examples/README.md`
- Modify: `docs/ADVANCED-USAGE.md`
- Modify: `README.md`

Write the example guide specified by the design, including:

- Copy paths, default-branch config ownership, single and newline-separated multiple roots.
- `v1.0.0-rc.7` checksum/provenance prerequisite and digest-pinned Terraform image.
- Literal allow-lists, manual narrowing, dry run, 256-branch limit, and exact schedules.
- Protected publication environment, least privileges, fixed secret names, optional signing, and
  no unsigned fallback after signing failure.
- Command-scoped private-repository Git auth and why checkout persistence remains disabled.
- Trusted data directories, provider/container boundary, timeouts, lock requirements, and valid
  provider-free roots.
- The honest security limit: the host authenticates process status, not semantic truth from a
  malicious provider.
- Current GitHub.com built-in-token recursion/approval behaviour and the need for GitHub Enterprise
  Server consumers to verify their installed version. App mode is permitted only after the
  documented downstream-workflow audit and acknowledgement; App event delivery is not itself a
  security boundary.
- Stable `update_<state-branch>` names, policy-scoped ownership, exact leases, best-effort Git/API
  compensation, residual race, and manual recovery after a compensation lease conflict.
- Run-attempt artefact identity, GitHub.com's `queue: max` behaviour and bound, and GitHub Enterprise
  Server compatibility checks.
- Failure classification and the manual, strongly marked orphan-cleanup procedure; automatic
  destructive garbage collection remains out of scope.
- The complete disposable private-repository acceptance procedure from the spec.

Link the detailed example from the existing examples, advanced-usage, and root documentation
without duplicating the guide. Human prose receives direct review, not grep tests. Run link checks
already present in the repository, if any, and create a signed docs commit.

### Task 15: Cleanup, full verification, and disposable-service gate

**Files:** Review all files changed by this plan.

1. Run the `test-cleanup` skill in a separate subagent. Remove only duplicated or implementation-
   shaped tests; retain every distinct security, race, lifecycle, and failure-class behaviour.
2. Run `superpowers:requesting-code-review`, resolve every actionable finding through focused TDD,
   and rerun cleanup if tests changed materially.
3. Run the complete local gate with pristine output:

   ```bash
   bash -n scripts/run-actionlint.sh
   bash -n examples/github-actions/.github/scripts/discover-state-branches.sh
   bash -n examples/github-actions/.github/scripts/process-state-branch.sh
   scripts/run-actionlint.sh examples/github-actions/.github/workflows/*.yml
   examples/github-actions/test.sh
   examples/update-branches_test.sh
   make test-coverage
   golangci-lint run --timeout=5m
   go build ./...
   ```

4. With Dan's explicit approval for external mutations, run the documented acceptance in a
   disposable private repository. It must cover authenticated discovery/push/delete, built-in and
   audited App modes, alternate-ref environment denial, hostile-container and hostile-downstream
   cases, provider/provider-free roots, run-attempt reruns, policy/unowned collisions, state and
   deletion races, a real post-push PR-creation rejection with exact-lease rollback, dry run,
   failure/recovery/cleanup, three rapid dispatches, and optional signed publication.
5. Review the transcript against every completion criterion in the design. Do not claim completion
   if any API, permissions, event, environment, queue, signing, compensation, or hostile-boundary
   assertion was inferred rather than observed.
6. Inspect the complete branch diff and worktree state with the Codex Git wrapper, ensure all
   commits are signed, and create the final signed commit if verification or cleanup changed files.
   Do not push without Dan's explicit instruction.
