# Task 8 final fix-wave report

Date: 2026-08-22

Reviewed head: `b96749143fc6e194c236ab7b2756ad0faa89bfb8`

Findings source: `/tmp/tf-version-bump-par-findings.md`

Authoritative design: `docs/superpowers/specs/2026-08-22-github-actions-stage-two-design.md`

## Result

All six accepted findings are resolved. The focused regressions, complete affected harnesses, and
the full repository verification gate pass. The required separate test-cleanup pass removed no
tests: every new row protects a distinct contract variant, stage boundary, or forbidden side
effect. The findings-verification pass found no remaining or collateral issue.

## Finding 1: complete pre-Terraform preparation validation

Status: Resolved.

### RED

Command:

```text
TEST_GIT=/Users/dan/.codex/bin/codex-git examples/github-actions/test.sh test_processing_validation_rejects_incomplete_contract_before_terraform
```

Observed exit: 1.

Observed output:

```text
FAIL: validation invoked Terraform before rejecting incomplete contract row artifact-name
```

### Implementation

- `examples/github-actions/.github/scripts/process-state-branch.sh:931` adds the preparation-log
  predicate used before Terraform.
- `examples/github-actions/.github/scripts/process-state-branch.sh:961` requires the complete
  validation-side control inputs.
- `examples/github-actions/.github/scripts/process-state-branch.sh:1009` binds the expected
  artefact name, config path, updater version, updater archive digest, Terraform version, and all
  existing run/control/base identity.
- `examples/github-actions/.github/scripts/process-state-branch.sh:1060` enforces the required
  `logs/` directory, bounded names/indexes, and exact classification-specific top-level entries.
- `examples/github-actions/.github/workflows/tf-version-bump-reusable.yml:201` passes the config and
  updater pins into validation.
- `examples/github-actions/test.sh:2906` covers wrong artefact identity, invalid config type,
  mismatched updater version/digest, missing logs, and an unknown regular log. Every row asserts
  that neither Terraform nor checkout materialisation occurs before rejection.

### GREEN

Command: same as RED.

Observed exit: 0. Observed output: empty.

Collateral command:

```text
TEST_GIT=/Users/dan/.codex/bin/codex-git examples/github-actions/test.sh test_reusable_workflow_wires_current_attempt_pipeline
```

Observed exit: 0. Observed output: empty.

## Finding 2: validate patch policy before materialising checkout

Status: Resolved.

### RED

Command:

```text
TEST_GIT=/Users/dan/.codex/bin/codex-git examples/github-actions/test.sh test_processing_validation_preflights_stage_policy_before_materialisation
```

Observed exit: 1.

Observed output:

```text
FAIL: validation materialised an unsafe update patch before rejection
```

### Implementation

- `examples/github-actions/.github/scripts/process-state-branch.sh:356` centralises changed-tree
  path, mode, blob, digest, sorting, and policy evaluation for both worktree and index-tree checks.
- `examples/github-actions/.github/scripts/process-state-branch.sh:540` separates tree identity
  checks from post-application regular-file and checkout-containment checks.
- `examples/github-actions/.github/scripts/process-state-branch.sh:569` makes update, formatting,
  and final path policies usable against a tree before the worktree changes.
- `examples/github-actions/.github/scripts/process-state-branch.sh:1225` applies each patch to a
  throwaway index, writes its tree, checks exact stage paths/modes/digests and stage policy, and
  retains the intermediate tree for formatting preflight.
- `examples/github-actions/.github/scripts/process-state-branch.sh:1256` preflights both patches
  and final metadata before Terraform or target-checkout mutation.
- Existing post-apply stage and final verification remains at
  `examples/github-actions/.github/scripts/process-state-branch.sh:1274`.
- `examples/github-actions/test.sh:2982` supplies digest-consistent unsafe update and format
  patches and asserts exact-base checkout state, no Terraform call, and no outcome after rejection.

### GREEN

Command: same as RED.

Observed exit: 0. Observed output: empty.

Collateral command:

```text
TEST_GIT=/Users/dan/.codex/bin/codex-git examples/github-actions/test.sh test_processing_validation
```

Observed exit: 0. Observed output: empty.

## Finding 3: reject ignored recursive formatting files

Status: Resolved.

### RED

Command:

```text
TEST_GIT=/Users/dan/.codex/bin/codex-git examples/github-actions/test.sh test_processing_rejects_unsafe_recursive_formatting_paths
```

Observed exit: 1.

Observed output:

```text
FAIL: ignored recursive formatting path succeeded
```

### Implementation

- `examples/github-actions/.github/scripts/process-state-branch.sh:779` rejects ignored files
  created by formatting, retaining the more specific `.terraform` diagnostic.
- `examples/github-actions/.github/scripts/process-state-branch.sh:442` invokes that rejection
  before the final force-add tree capture and patch construction.
- `examples/github-actions/test.sh:2156` adds a real formatter-created ignored nested `.tf` row and
  asserts that no preparation or validation result is consumable.

### GREEN

Command: same as RED.

Observed exit: 0. Observed output: empty.

## Finding 4: exact bounded preparation and outcome log allow-lists

Status: Resolved.

### RED

Command:

```text
TEST_GIT=/Users/dan/.codex/bin/codex-git examples/github-actions/reconcile-test.sh test_rejects_unexpected_contract_logs
```

Observed exit: 1.

Observed output:

```text
FAIL: unexpected preparation log succeeded
```

### Implementation

- `examples/github-actions/.github/scripts/reconcile-state-branch.sh:92` accepts only known
  preparation stage-log basenames, canonical positive indexes, root-bounded indexes when roots are
  present, and formatting logs only for a formatting run/failure.
- `examples/github-actions/.github/scripts/reconcile-state-branch.sh:130` accepts only
  `terraform-version.json` plus root-bounded `init-N.log` and `validate-N.log` outcome logs.
- `examples/github-actions/.github/scripts/reconcile-state-branch.sh:548` and line 637 apply those
  predicates before verified-result assembly.
- `examples/github-actions/reconcile-test.sh:600` rejects unknown regular log files independently
  in the preparation bundle and validation outcome.

### GREEN

Command: same as RED.

Observed exit: 0. Observed output: empty.

Collateral command:

```text
TEST_GIT=/Users/dan/.codex/bin/codex-git examples/github-actions/reconcile-test.sh
```

Observed exit: 0. Observed output: empty.

## Finding 5: bind failure classifications to stages at every boundary

Status: Resolved.

### RED

Command:

```text
TEST_GIT=/Users/dan/.codex/bin/codex-git examples/github-actions/reconcile-test.sh test_binds_failure_classifications_to_stages_at_every_boundary
```

Observed exit: 1.

Observed output:

```text
FAIL: branch-update mismatched preparation stage succeeded
```

### Implementation

- `examples/github-actions/.github/scripts/reconcile-state-branch.sh:174` binds preparation
  `branch-update`, `branch-init`, `branch-format`, and `automation` classifications to their exact
  permitted stage values. Automation retains the two producer-owned stages:
  `tf-version-bump report` and `provider lock policy`.
- `examples/github-actions/.github/scripts/reconcile-state-branch.sh:245` limits
  `branch-validation` outcomes to `terraform init` or `terraform validate`.
- `examples/github-actions/.github/scripts/reconcile-state-branch.sh:827` re-establishes the same
  bindings at the verified-result publication boundary.
- `examples/github-actions/reconcile-test.sh:615` rejects mismatches at the preparation, outcome,
  and verified-result boundaries for every failure classification and proves the existing provider
  lock-policy automation stage remains valid.

### GREEN

Command: same as RED.

Observed exit: 0. Observed output: empty.

## Finding 6: update runtime formatting help

Status: Resolved.

### RED

Command:

```text
examples/github-actions/test.sh test_processing_help_documents_prepare_safety_contract
```

Observed exit: 1.

Observed output:

```text
FAIL: processing help did not document both staged preparation patches
```

### Implementation

- `examples/github-actions/.github/scripts/process-state-branch.sh:34` describes `update.patch`,
  optional `format.patch`, ordered validation, and bounded formatting failures.
- `examples/github-actions/test.sh:1302` owns the executable help contract.

### GREEN

Command: same as RED.

Observed exit: 0. Observed output: empty.

## Separate test-cleanup pass

Scope: tests added since reviewed head `b96749143fc6e194c236ab7b2756ad0faa89bfb8`.

- Tests/rows reviewed: 23 (six incomplete-contract rows, two stage-preflight rows, one ignored-path
  row, two unknown-log rows, eleven classification-boundary rows, and one help-contract update).
- Kept: 23.
- Removed as slop: 0.
- Flagged for follow-up: 0.
- Reason: every row catches a separate malformed variant, consumer boundary, or forbidden real
  checkout/Terraform side effect. No row asserts mocked behaviour or framework mechanics.
- Coverage: Go coverage remains 93.3%; the Bash contract suite passes in full.

## Formal findings verification

1. Resolved — complete pre-Terraform preparation contract and exact class-specific files/logs.
2. Resolved — update and format patches preflight completely before target materialisation.
3. Resolved — ignored recursive `.tf` output fails before force-add capture can publish it.
4. Resolved — preparation and outcome logs use bounded filename/index allow-lists.
5. Resolved — every failure schema boundary binds classification to stage.
6. Resolved — executable runtime help describes both patches and formatting failures.

Regressions or new issues: None.

Current-session follow-up: None.

## Complete final test evidence

All commands were run independently from `/Users/dan/Code/tf-version-bump`.

| Command | Exit | Evidence |
|---|---:|---|
| `TEST_GIT=/Users/dan/.codex/bin/codex-git make test-github-actions` | 0 | Complete Bash integration, Docker/provider, actionlint, preparation, validation, reconciliation, publication, and lifecycle harness passed with no diagnostic output. |
| `make docs-check` | 0 | Documentation/schema/example tests passed. |
| `go test -v -race -coverprofile=coverage.out -covermode=atomic ./...` | 0 | `PASS`; `coverage: 93.3% of statements`. |
| `golangci-lint run --timeout=5m` | 0 | `0 issues.` |
| `go vet ./...` | 0 | No output. |
| `go build ./...` | 0 | No output on the final unsandboxed rerun. |
| `bash -n examples/github-actions/.github/scripts/process-state-branch.sh` | 0 | No output. |
| `bash -n examples/github-actions/.github/scripts/reconcile-state-branch.sh` | 0 | No output. |
| `bash -n examples/github-actions/test.sh` | 0 | No output. |
| `bash -n examples/github-actions/reconcile-test.sh` | 0 | No output. |
| `/Users/dan/.codex/bin/codex-git diff --check` | 0 | No output before the implementation commit. |

The first sandboxed `go build ./...` exited 0 but emitted a denied module stat-cache warning. It was
rerun with the required filesystem permission and then exited 0 with pristine empty output; only
the pristine rerun is used as final build evidence.

## Commits and signatures

Implementation commit:

```text
3efd0e4743d5825839cf4ebe28449376ca72af6e
Good "git" signature for dan@danbarrett.com.au with ED25519 key
SHA256:BKWdtyT/fFbRdgNCIElSN7XsWyEgHJOsOIo0WKAq6vs
```

Commit subject: `fix: harden staged workflow artefact validation`.

The report is committed separately after this evidence is recorded, so its own commit cannot be
self-referentially embedded in its contents. The final branch-wide signature command verifies both
the implementation and report commits, together with all earlier branch commits.

## Concerns

None. No push, pull request, rebase, tag, release, or GitHub mutation was performed. The topic
branch has no configured upstream, so the required initial `pull --rebase` attempt stopped without
changing history and reported that no tracking information exists.
