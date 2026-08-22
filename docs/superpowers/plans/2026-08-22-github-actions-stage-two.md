# GitHub Actions Stage Two Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add optional recursive Terraform formatting, exact module/provider update reporting, two-stage commits, and split pull-request tracking to the copyable GitHub Actions example.

**Architecture:** Preparation runs the released updater and upgrade initialisation for every configured root, records one update tree, then optionally records a formatting tree. Validation and post-Terraform verification share one credential-free checkout and produce one narrowly scoped verified result per matrix entry; a separate write-capable publication job consumes only that result and creates one or two deterministic commits.

**Tech Stack:** Bash 4+, Git, jq, yq, Terraform 1.15.5, GitHub Actions YAML, the published `tf-version-bump v1.0.0-rc.9` Linux x86-64 archive, Go 1.26 repository checks

**Spec:** `docs/superpowers/specs/2026-08-22-github-actions-stage-two-design.md`

## Global Constraints

- Use `tf-version-bump v1.0.0-rc.9` with Linux x86-64 SHA-256 `38428a229a77671fd192fd6a18f5d1f9c404b5557124883f04e6a8bec154b1d2`.
- Keep Terraform pinned to `1.15.5` and preserve every existing action commit pin, timeout, deadline, exact-lease, ownership, dry-run, and compensation rule.
- Add `terraform_fmt` as a boolean reusable-workflow input with default `false`; the supplied production and non-production callers set it to `true`.
- Run `tf-version-bump`, then `terraform init -upgrade`, for every `terraform_directories` entry before deciding whether formatting is eligible.
- Run `terraform fmt -recursive` from every configured root only when formatting is enabled and the aggregate update/init tree differs from the exact base.
- Use schema version 2 consistently for preparation, validation-outcome, and verified-result manifests. Do not implement schema-version-1 compatibility.
- Keep update/init paths direct beneath one declared root; allow formatting `.tf` paths recursively beneath a declared root, but never beneath `.terraform`.
- Treat every configured provider and module source as trusted. Same-runner post-Terraform checks detect accidental mutation but do not recreate an adversarial clean-verifier boundary.
- Keep publication separate from every Terraform invocation and give repository write credentials only to publication.
- Retain the example's explicitly unsigned automation-commit policy. Every repository commit created by an agent must instead use `/Users/dan/.codex/bin/codex-git` and must have a verified signature.
- Follow red-green TDD for every behaviour change. Run the independent `test-cleanup` skill after implementation and before final review.

## File Structure

- `examples/github-actions/.github/scripts/process-state-branch.sh` remains the standalone preparation/validation owner. Add report aggregation, tree capture, formatting, stage-policy validation, and schema-version-2 validation outcomes here.
- `examples/github-actions/.github/scripts/reconcile-state-branch.sh` remains the standalone verification/publication owner. Update it to recheck the already validated checkout, emit schema-version-2 results, construct staged commits, and render the managed PR body.
- `examples/github-actions/.github/workflows/tf-version-bump-reusable.yml` owns the job graph, input propagation, always-run result assembly, artefact movement, and credential boundary.
- `examples/github-actions/.github/workflows/tf-version-bump-{nonproduction,production}.yml` own caller policy and opt into formatting.
- `examples/github-actions/.github/workflows/tf-version-bump-config-validation.yml` owns the independently verified release download used by config pull requests.
- `examples/github-actions/test.sh` remains the real preparation/validation and static-workflow integration harness.
- `examples/github-actions/reconcile-test.sh` remains the real Git/artefact/publication integration harness.
- `examples/github-actions/README.md` and `docs/ADVANCED-USAGE.md` document the copyable operator contract.
- Do not split the standalone scripts during this change: consumers currently copy each script as one file, and a new sourced-file dependency would enlarge the installation contract without serving this feature.

---

### Task 1: Pin the Stage-Two Release

**Files:**
- Modify: `examples/github-actions/test.sh:40-42`
- Modify: `examples/github-actions/.github/workflows/tf-version-bump-nonproduction.yml:45-47`
- Modify: `examples/github-actions/.github/workflows/tf-version-bump-production.yml:42-44`
- Modify: `examples/github-actions/.github/workflows/tf-version-bump-config-validation.yml:22-58`

**Interfaces:**
- Consumes: published release tag `v1.0.0-rc.9` and its verified Linux x86-64 digest.
- Produces: shared harness constants `TF_VERSION_BUMP_VERSION`, `TF_VERSION_BUMP_ARCHIVE_SHA256`, and `TF_VERSION_BUMP_ARCHIVE_URL` that every later preparation test uses.

- [ ] **Step 1: Change the harness expectation to the released archive**

```bash
TF_VERSION_BUMP_VERSION="v1.0.0-rc.9"
TF_VERSION_BUMP_ARCHIVE_SHA256="38428a229a77671fd192fd6a18f5d1f9c404b5557124883f04e6a8bec154b1d2"
TF_VERSION_BUMP_ARCHIVE_URL="https://github.com/yesdevnull/tf-version-bump/releases/download/v1.0.0-rc.9/tf-version-bump_1.0.0-rc.9_linux_x86_64.tar.gz"
```

- [ ] **Step 2: Run the workflow harness to prove the copied workflows still reference rc.8**

Run: `make test-github-actions`

Expected: FAIL in `test_callers_define_weekly_policies_and_tool_pins` or `test_config_validation_workflow_is_read_only` because the workflows do not match the rc.9 constants.

- [ ] **Step 3: Update both callers to the exact rc.9 pin**

```yaml
tf_version_bump_version: v1.0.0-rc.9
tf_version_bump_archive_sha256: 38428a229a77671fd192fd6a18f5d1f9c404b5557124883f04e6a8bec154b1d2
```

- [ ] **Step 4: Update config validation download, version assertion, and cleanup paths**

```bash
archive_path="$RUNNER_TEMP/tf-version-bump_1.0.0-rc.9_linux_x86_64.tar.gz"
curl --fail --silent --show-error --location \
  --output "$archive_path" \
  https://github.com/yesdevnull/tf-version-bump/releases/download/v1.0.0-rc.9/tf-version-bump_1.0.0-rc.9_linux_x86_64.tar.gz
printf '%s  %s\n' \
  38428a229a77671fd192fd6a18f5d1f9c404b5557124883f04e6a8bec154b1d2 \
  "$archive_path" | sha256sum --check --status
version_output=$("$tool_directory/tf-version-bump" -version)
[[ "${version_output%%$'\n'*}" == "tf-version-bump 1.0.0-rc.9" ]]
```

Update the unconditional cleanup step to remove the rc.9 archive path.

- [ ] **Step 5: Run the workflow harness**

Run: `make test-github-actions`

Expected: PASS with the released rc.9 archive downloaded, checksum-verified, version-verified, and used by the real preparation tests.

- [ ] **Step 6: Commit the release pin**

```bash
/Users/dan/.codex/bin/codex-git add examples/github-actions/test.sh examples/github-actions/.github/workflows/tf-version-bump-nonproduction.yml examples/github-actions/.github/workflows/tf-version-bump-production.yml examples/github-actions/.github/workflows/tf-version-bump-config-validation.yml
/Users/dan/.codex/bin/codex-git commit -m "chore: pin GitHub Actions example to rc.9"
```

Verify the commit with `/Users/dan/.codex/bin/codex-git log -1 --show-signature --format=fuller` before continuing.

---

### Task 2: Introduce the Schema-Version-2 Update Stage

**Files:**
- Modify: `examples/github-actions/test.sh:250-440,1499-2039,2201-2229`
- Modify: `examples/github-actions/reconcile-test.sh:54-427`
- Modify: `examples/github-actions/.github/scripts/process-state-branch.sh:172-397,666-824,889-974`
- Modify: `examples/github-actions/.github/scripts/reconcile-state-branch.sh:71-327,508-559`

**Interfaces:**
- Consumes: report JSON `{schema_version,module_blocks_updated,provider_blocks_updated}` written by rc.9 for each configured root.
- Produces: `update.patch`; schema-version-2 preparation fields `updates`, `formatting`, and `final_changed_files`; schema-version-2 validation outcomes; schema-version-2 verified results that carry the update counts and patch.

- [ ] **Step 1: Add a real multi-root report aggregation test**

Add `test_processing_aggregates_released_cli_reports()` to `examples/github-actions/test.sh`. Create two direct Terraform roots, give each a provider and registry module targeted by the existing control config, run the released binary through `run_processing_prepare`, and assert:

```bash
jq -e '
  .schema_version == 2 and
  .classification == "success" and
  .updates.module_blocks_updated == 2 and
  .updates.provider_blocks_updated == 2 and
  (.updates.changed_files | length) >= 2 and
  .formatting == {ran: false, changed_files: []} and
  .final_changed_files == .updates.changed_files
' "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json" >/dev/null
[[ -f "$PROCESS_PREPARATION_BUNDLE_DIR/update.patch" ]]
[[ ! -e "$PROCESS_PREPARATION_BUNDLE_DIR/candidate.patch" ]]
```

Register it in `test_processing_preparation()`.

- [ ] **Step 2: Add report-contract failure cases**

Add a table-driven `test_processing_rejects_invalid_update_reports_as_automation()` using the existing controlled executable fixture boundary. Exercise missing output, malformed JSON, schema `2`, negative counts, fractional counts, and extra keys. For every row assert:

```bash
jq -e '
  .schema_version == 2 and
  .classification == "automation" and
  .failure.stage == "tf-version-bump report" and
  has("updates") == false
' "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json" >/dev/null
[[ ! -e "$PROCESS_PREPARATION_BUNDLE_DIR/update.patch" ]]
[[ ! -e "$PROCESS_PREPARATION_BUNDLE_DIR/format.patch" ]]
```

The controlled executable represents the published CLI boundary; the assertions exercise the real report validator and bundle writer, not duplicated updater behaviour.

- [ ] **Step 3: Migrate reconciliation fixtures and assertions to schema version 2**

Change `setup_success_fixture()` to create `update.patch` and this manifest payload:

```jq
{schema_version: 2, run_id: "100", run_attempt: "1",
 automation_policy_id: "nonproduction", control_oid: $control_oid,
 state_branch: $state_branch, base_oid: $base_oid, ref_hash: $ref_hash,
 artifact_name: ("preparation-100-1-nonproduction-" + $ref_hash),
 classification: "success", terraform_fmt: false,
 tools: {terraform: {version: "1.15.5"},
         tf_version_bump: {version: "v1.0.0-rc.9",
                           archive_sha256: "38428a229a77671fd192fd6a18f5d1f9c404b5557124883f04e6a8bec154b1d2"}},
 config_path: ".github/tf-version-bump/nonproduction.yml",
 roots: [{path: "root"}],
 updates: {module_blocks_updated: 1, provider_blocks_updated: 1,
           changed_files: [{path: $changed_path, mode: "100644", sha256: $file_digest}],
           patch_sha256: $patch_digest},
 formatting: {ran: false, changed_files: []},
 final_changed_files: [{path: $changed_path, mode: "100644", sha256: $file_digest}]}
```

Change the outcome fixture to schema version 2 with `command_status: 0`. Update every fixture mutation to edit nested fields and every patch assertion to use `update.patch`.

- [ ] **Step 4: Run the two harnesses to establish the schema failure**

Run: `examples/github-actions/reconcile-test.sh`

Expected: FAIL because reconciliation still requires schema version 1 and `candidate.patch`.

Run: `make test-github-actions`

Expected: FAIL because preparation neither passes `-report-file` nor emits schema version 2.

- [ ] **Step 5: Add strict report validation and aggregation to preparation**

Add these globals near the other preparation state:

```bash
PREPARATION_MODULE_BLOCKS_UPDATED=0
PREPARATION_PROVIDER_BLOCKS_UPDATED=0
```

Add this function beside `copy_preparation_logs()`:

```bash
accumulate_update_report() {
    local report=$1 relative_root=$2
    jq -e '
        type == "object" and
        (keys == ["module_blocks_updated", "provider_blocks_updated", "schema_version"]) and
        .schema_version == 1 and
        (.module_blocks_updated | type == "number" and . >= 0 and floor == .) and
        (.provider_blocks_updated | type == "number" and . >= 0 and floor == .)
    ' "$report" >/dev/null || return 1
    local module_count provider_count
    module_count=$(jq -er '.module_blocks_updated' "$report")
    provider_count=$(jq -er '.provider_blocks_updated' "$report")
    PREPARATION_MODULE_BLOCKS_UPDATED=$((PREPARATION_MODULE_BLOCKS_UPDATED + module_count))
    PREPARATION_PROVIDER_BLOCKS_UPDATED=$((PREPARATION_PROVIDER_BLOCKS_UPDATED + provider_count))
}
```

For each root, allocate `"$PREPARATION_DATA_ROOT/report-$root_index.json"`, pass it with `-report-file`, validate it immediately after the updater returns zero, and write an `automation`/`tf-version-bump report` failure bundle before any later root or initialisation runs when validation fails.

- [ ] **Step 6: Emit the one-stage schema-version-2 preparation bundle**

Rename `candidate.patch` to `update.patch`. In `write_preparation_candidate_bundle()`, construct:

```jq
. + {
  classification: $classification,
  terraform_fmt: false,
  tools: {
    tf_version_bump: {version: $tf_version_bump_version,
                      archive_sha256: $tf_version_bump_archive_sha256},
    terraform: {version: $terraform_version}
  },
  config_path: $config_path,
  roots: $roots,
  updates: {
    module_blocks_updated: $module_blocks_updated,
    provider_blocks_updated: $provider_blocks_updated,
    changed_files: $changed_files
  } + if $patch_sha256 == "" then {} else {patch_sha256: $patch_sha256} end,
  formatting: {ran: false, changed_files: []},
  final_changed_files: $changed_files
}
```

Set `schema_version: 2` in `preparation_identity_json()` and every failure manifest. Reject unknown top-level bundle entries and preserve immutable modes for `manifest.json`, `logs/`, and `update.patch`.

- [ ] **Step 7: Migrate validation to the nested update contract**

In `validation_contract()`, require schema version 2, validate the exact success/no-change field presence, set `VALIDATION_UPDATE_PATCH`, and compare its digest with `.updates.patch_sha256`. Change `apply_validation_candidate()` to apply `update.patch` and compare the resulting paths, modes, and SHA-256 values with `.updates.changed_files` and `.final_changed_files`.

Emit schema version 2 from `write_validation_outcome()` while retaining the seven identity fields, `candidate_manifest_sha256`, `classification`, `command_status`, optional bounded failure, and logs.

- [ ] **Step 8: Migrate one-stage verification and publication input**

In `manifest_matches_identity()`, require schema version 2. Update `verify_result()` and `write_verified_result()` to require `preparation_manifest_sha256` for every class, require `validation_outcome_sha256` only for success/no-change/branch-validation, copy `updates`, `formatting`, and `final_changed_files`, copy `update.patch`, and reject schema-version-1 inputs.

Keep publication's existing single commit behaviour in this task, but read the verified update digest from `.updates.patch_sha256` and apply `update.patch`.

- [ ] **Step 9: Run the focused harnesses**

Run: `examples/github-actions/reconcile-test.sh`

Expected: PASS.

Run: `make test-github-actions`

Expected: PASS, including real rc.9 report aggregation and strict report failure bundles.

- [ ] **Step 10: Commit the staged update contract**

```bash
/Users/dan/.codex/bin/codex-git add examples/github-actions/test.sh examples/github-actions/reconcile-test.sh examples/github-actions/.github/scripts/process-state-branch.sh examples/github-actions/.github/scripts/reconcile-state-branch.sh
/Users/dan/.codex/bin/codex-git commit -m "feat: record staged Terraform update reports"
```

Verify the commit signature before continuing.

---

### Task 3: Prepare and Validate Optional Formatting

**Files:**
- Modify: `examples/github-actions/test.sh`
- Modify: `examples/github-actions/.github/scripts/process-state-branch.sh`
- Modify: `examples/github-actions/.github/workflows/tf-version-bump-reusable.yml`

**Interfaces:**
- Consumes: schema-version-2 one-stage preparation from Task 2 and `PROCESS_TERRAFORM_FMT` as exact `true` or `false`.
- Produces: update and final Git tree OIDs, optional `format.patch`, recursive formatting metadata, `branch-format`, and a validation checkout with update then format patches applied.

- [ ] **Step 1: Add the reusable input contract test**

Extend `test_reusable_workflow_declares_lean_interface()` so the sorted key set includes `terraform_fmt` and assert:

```jq
.terraform_fmt == {type: "boolean", default: false}
```

Do not opt the supplied callers in until Task 5.

- [ ] **Step 2: Add disabled and ineligible formatting tests**

Add `test_processing_formatting_disabled()` and `test_processing_skips_formatting_without_update_diff()`. Use a call-log-aware Terraform executable and assert respectively:

```bash
jq -e '.terraform_fmt == false and .formatting == {ran: false, changed_files: []}' \
  "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json" >/dev/null
! grep -F 'fmt -recursive' "$PROCESS_TEST_CALL_LOG"
```

```bash
jq -e '.classification == "no-change" and .terraform_fmt == true and
       .formatting == {ran: false, changed_files: []}' \
  "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json" >/dev/null
! grep -F 'fmt -recursive' "$PROCESS_TEST_CALL_LOG"
```

- [ ] **Step 3: Add real recursive formatting tests**

Add `test_processing_formats_nested_files_from_every_root()` with two declared roots and an
unformatted nested `.tf` file under each root. Set `PROCESS_TERRAFORM_FMT=true`, run preparation,
and assert the call log contains one `terraform -chdir=root-a fmt -recursive` invocation and one
`terraform -chdir=root-b fmt -recursive` invocation,
`format.patch` exists, and the sorted formatting paths contain both nested files exactly once.

Add `test_processing_formatting_without_diff_omits_patch()` using already formatted files and
assert `formatting.ran == true`, `formatting.changed_files == []`, and no `format.patch` exists.

- [ ] **Step 4: Add formatting cancellation and bounded failure tests**

Create a controlled formatter that restores the update-stage content to the exact base and assert:

```bash
jq -e '.classification == "no-change" and
       .formatting.ran == true and
       .updates.changed_files == [] and
       .formatting.changed_files == [] and
       .final_changed_files == []' \
  "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json" >/dev/null
[[ ! -e "$PROCESS_PREPARATION_BUNDLE_DIR/update.patch" ]]
[[ ! -e "$PROCESS_PREPARATION_BUNDLE_DIR/format.patch" ]]
```

Create a formatter that returns status `9` only for `fmt -recursive` and assert a `branch-format`
manifest with `failure.stage == "terraform fmt"`, the declared root, status `9`, retained bounded
logs, and no patches.

- [ ] **Step 5: Add stage-path and tamper tests**

Cover a nested path outside every root, a path beneath `.terraform`, a symlink, an executable mode,
invalid UTF-8, an undeclared path, a changed digest, a tampered update patch, and a tampered format
patch. Each case must fail without producing a verified result. Include one valid file present in
both stage lists and assert its update-stage digest differs from and is checked before its final
digest.

- [ ] **Step 6: Run the workflow harness to prove formatting is absent**

Run: `make test-github-actions`

Expected: FAIL at the new input or formatting tests.

- [ ] **Step 7: Add input parsing and root-by-root formatting**

Add the workflow input:

```yaml
terraform_fmt:
  type: boolean
  default: false
```

Pass it to preparation as `PROCESS_TERRAFORM_FMT: ${{ inputs.terraform_fmt }}`. In
`prepare_bundle_contract()`, require the environment value and accept only `true` or `false`.

After every update and upgrade initialisation succeeds, compute the aggregate update tree. If it
differs from the base and formatting is true, run for each declared root in order:

```bash
run_before_preparation_deadline "$PREPARATION_DATA_ROOT/format-$root_index.log" \
    terraform -chdir="$terraform_root" fmt -recursive
```

On failure, write `branch-format` with stage `terraform fmt`, the relative root, the exact command,
and the observed status before raising the bounded processing error.

- [ ] **Step 8: Add reusable tree and metadata helpers**

Add `capture_checkout_tree()` and `changed_files_json_between_trees()` rather than duplicating the
raw-diff logic. `changed_files_json_between_trees()` must read `git diff --raw -z --no-renames`,
take the target blob OID from each raw record, calculate the file SHA-256 with `git cat-file blob`,
and validate every path with the supplied stage policy before emitting sorted JSON records.

Use one temporary index for the update tree and another for the final tree. Generate:

```bash
git -C "$PREPARATION_TARGET_CHECKOUT" diff --binary --full-index --no-color \
    "$base_tree" "$update_tree" >"$PREPARATION_BUNDLE_STAGE/update.patch"
git -C "$PREPARATION_TARGET_CHECKOUT" diff --binary --full-index --no-color \
    "$update_tree" "$final_tree" >"$PREPARATION_BUNDLE_STAGE/format.patch"
```

Remove an empty format patch. If `base_tree == final_tree`, remove both patches, clear all lists,
and classify the final bundle as no-change while retaining `formatting.ran: true`.

- [ ] **Step 9: Add the recursive formatting path policy**

Keep `path_is_declared_direct_file()` semantics for update/init. Add a formatting predicate that
requires a `.tf` path beneath at least one canonical declared root, rejects every `.terraform`
component, and calls the existing regular-file, symlink, mode, UTF-8, newline, and checkout-boundary
checks. Deduplicate overlapping-root results by exact Git path.

- [ ] **Step 10: Apply and verify both patches during validation**

Replace `apply_validation_candidate()` with an ordered stage application:

```bash
apply_validation_stage "$VALIDATION_UPDATE_PATCH" updates
verify_validation_stage updates
if jq -e '.formatting | has("patch_sha256")' "$VALIDATION_MANIFEST" >/dev/null; then
    apply_validation_stage "$VALIDATION_FORMAT_PATCH" formatting
    verify_validation_stage formatting
fi
verify_final_candidate
VALIDATION_CANDIDATE_STATUS=$(git -C "$VALIDATION_TARGET_CHECKOUT" status --porcelain=v1 --untracked-files=all)
```

The update check compares intermediate paths/modes/digests before the formatting patch can replace
them; the final check compares the complete base-to-final state.

- [ ] **Step 11: Run preparation and validation tests**

Run: `make test-github-actions`

Expected: PASS for disabled, skipped, recursive, unchanged, cancellation, failure, path-policy,
tamper, and overlapping-root formatting cases.

- [ ] **Step 12: Commit optional formatting preparation**

```bash
/Users/dan/.codex/bin/codex-git add examples/github-actions/test.sh examples/github-actions/.github/scripts/process-state-branch.sh examples/github-actions/.github/workflows/tf-version-bump-reusable.yml
/Users/dan/.codex/bin/codex-git commit -m "feat: stage Terraform formatting changes"
```

Verify the commit signature before continuing.

---

### Task 4: Verify and Publish the Two-Commit Result

**Files:**
- Modify: `examples/github-actions/reconcile-test.sh`
- Modify: `examples/github-actions/test.sh`
- Modify: `examples/github-actions/.github/scripts/reconcile-state-branch.sh`

**Interfaces:**
- Consumes: the already-applied validation checkout, local schema-version-2 validation outcome, `update.patch`, optional `format.patch`, and stage metadata from Task 3.
- Produces: a strict verified-result directory and deterministic one- or two-commit publication with exact counts and split PR file lists.

- [ ] **Step 1: Extend the reconciliation success fixture to two stages**

Create an update candidate that changes `root/main.tf`, capture `update.patch` and its intermediate
digest, then format that file and `root/nested/child.tf`, capture `format.patch`, and populate
`updates`, `formatting`, and `final_changed_files`. Apply both patches to `FIXTURE_CHECKOUT` before
`run_verify` so the fixture matches the combined-job contract.

- [ ] **Step 2: Add strict verified-result variant tests**

Assert every verified classification has `preparation_manifest_sha256`; success/no-change and
branch-validation have `validation_outcome_sha256`; preparation failures and automation forbid the
outcome digest. Assert the verified directory contains exactly:

```text
manifest.json
update.patch
format.patch
```

for a two-stage success, only `manifest.json` plus `update.patch` for one-stage success, and only
`manifest.json` for no-change or any failure. Add rejection cases for every unexpected entry and
unknown manifest field.

- [ ] **Step 3: Add post-Terraform recheck tests**

Mutate each of the control checkout, preparation manifest, update patch, format patch, validation
outcome, intermediate metadata, final metadata, and already-applied checkout after validation.
Assert `verify` rejects every row and leaves no verified-result directory.

The test describes accidental-mutation detection only; it must not claim same-runner protection
against malicious provider code.

- [ ] **Step 4: Add commit topology and subject tests**

For each count tuple `(1,1)`, `(0,1)`, `(1,0)`, and `(0,0)`, run dry-run publication and assert the
first commit subject is respectively:

```text
chore: bump Terraform provider and module versions
chore: bump Terraform provider versions
chore: bump Terraform module versions
chore: update Terraform configuration
```

For a format patch, assert `HEAD` has subject `chore: run Terraform fmt`, `HEAD^` has the dynamic
update subject, both commits carry the existing ownership/base trailers, and the final tree equals
the verified final metadata. For no format patch, assert exactly one new commit.

- [ ] **Step 5: Add pull-request body tests**

Capture the body passed to `gh pr create` and `gh pr edit`. Assert it contains exact module/provider
counts, `Dependency and lock-file changes (N)`, `Formatting changes (N)` or `None`, and HTML-safe
code-formatted paths. Include one hostile path and one path present in both lists; the latter must
appear once in each section.

- [ ] **Step 6: Add automation no-op tests**

Create a schema-version-2 `automation` preparation, run verification without an outcome or patch,
and assert the result contains the preparation digest, diagnostic failure, and raw run URL. Run
publication with Git/GitHub sentinel executables and assert it returns without invoking either.

- [ ] **Step 7: Run reconciliation tests to prove the old verifier is incomplete**

Run: `examples/github-actions/reconcile-test.sh`

Expected: FAIL on already-applied checkout verification, format-patch copying, commit topology, or PR body assertions.

- [ ] **Step 8: Recheck the already-applied candidate after Terraform**

Require `RECONCILE_CONTROL_CHECKOUT`, verify its HEAD equals the control OID and its complete status
is clean, and verify the target checkout has the exact declared final state. After that check, reset
the disposable target checkout to the exact base and replay `update.patch` and optional
`format.patch` in order, checking intermediate then final metadata again. This reset is confined to
the disposable validation checkout and occurs only after Terraform mutation detection.

Retain the existing exact path, mode, UTF-8, symlink, digest, identity, and allowed-file checks.

- [ ] **Step 9: Emit the complete verified-result contract**

Refactor `write_verified_result()` to atomically create the exact file set and build the manifest
from the checked preparation/outcome. Copy the seven identity fields and
`preparation_manifest_sha256` for all classes; copy the outcome digest only where allowed; copy
stage metadata and patches only for success; preserve bounded failure plus raw run URL for
branch/automation failures. Reject all unknown classifications and fields.

- [ ] **Step 10: Construct deterministic staged commits**

Add this subject selector:

```bash
update_commit_subject() {
    local manifest=$1 modules providers
    modules=$(jq -er '.updates.module_blocks_updated' "$manifest")
    providers=$(jq -er '.updates.provider_blocks_updated' "$manifest")
    if [[ "$modules" -gt 0 && "$providers" -gt 0 ]]; then
        printf '%s\n' 'chore: bump Terraform provider and module versions'
    elif [[ "$providers" -gt 0 ]]; then
        printf '%s\n' 'chore: bump Terraform provider versions'
    elif [[ "$modules" -gt 0 ]]; then
        printf '%s\n' 'chore: bump Terraform module versions'
    else
        printf '%s\n' 'chore: update Terraform configuration'
    fi
}
```

Apply and commit `update.patch` first using this subject. If present, apply and commit
`format.patch` second with `chore: run Terraform fmt`. Preserve the existing deterministic author,
committer, date, unsigned automation policy, ownership/base trailers, local verification,
exact-lease push, compensation, and dry-run behaviour.

- [ ] **Step 11: Render counts and split encoded paths**

Build the managed PR body only from the verified manifest. Use the existing `html_code()` helper for
every dynamic value. Render the module/provider counts followed by an update section and formatting
section; derive each displayed file count with jq `length`, and render `None` when the formatting
list is empty. Do not consult preparation or validation directories in publication.

- [ ] **Step 12: Run reconciliation and full example tests**

Run: `examples/github-actions/reconcile-test.sh`

Expected: PASS.

Run: `make test-github-actions`

Expected: PASS.

- [ ] **Step 13: Commit staged verification and publication**

```bash
/Users/dan/.codex/bin/codex-git add examples/github-actions/reconcile-test.sh examples/github-actions/test.sh examples/github-actions/.github/scripts/reconcile-state-branch.sh
/Users/dan/.codex/bin/codex-git commit -m "feat: publish staged Terraform update commits"
```

Verify the commit signature before continuing.

---

### Task 5: Fold Verification into Validation

**Files:**
- Modify: `examples/github-actions/test.sh:705-946,1956-2039`
- Modify: `examples/github-actions/reconcile-test.sh:363-427`
- Modify: `examples/github-actions/.github/workflows/tf-version-bump-reusable.yml:1-372`
- Modify: `examples/github-actions/.github/workflows/tf-version-bump-nonproduction.yml`
- Modify: `examples/github-actions/.github/workflows/tf-version-bump-production.yml`

**Interfaces:**
- Consumes: `process-state-branch.sh validate` and `reconcile-state-branch.sh verify` from Tasks 3-4.
- Produces: job graph `discover -> prepare -> validate -> publish`, one verified artefact per well-formed matrix entry, and caller formatting opt-in.

- [ ] **Step 1: Rewrite the static workflow contract tests first**

Change the expected job keys to:

```jq
keys == ["discover", "prepare", "publish", "validate"]
```

Assert `publish.needs == ["discover", "validate"]`, there is no `validation-*` artefact, validation
calls both helper modes, validation uploads `verified-*`, and publication downloads it. Remove all
checkout and secret-boundary expectations for a `verify` job. Continue to assert Terraform setup is
absent from publication and persisted credentials are false in preparation/validation.

- [ ] **Step 2: Add always-run workflow control tests**

Assert the candidate-validation step has `continue-on-error: true`; reconciliation, verified upload,
and final classification use `if: ${{ always() }}`; publication retains
`if: ${{ always() && needs.discover.result == 'success' }}`. Assert bounded branch failures pass the
final classification, while `automation` exits non-zero only after the verified artefact exists.

- [ ] **Step 3: Add caller opt-in tests**

Extend `test_callers_define_weekly_policies_and_tool_pins()` with:

```jq
.terraform_fmt == true
```

for both callers.

- [ ] **Step 4: Run the harness to prove the five-job workflow remains**

Run: `make test-github-actions`

Expected: FAIL because `verify` and the validation artefact still exist and callers have not opted in.

- [ ] **Step 5: Move trusted reconciliation into validation**

Keep the existing validation checkouts. Replace validation artefact upload with local outcome
staging beneath `RUNNER_TEMP`. After the continue-on-error processing step, add an always-run step
that invokes:

```yaml
run: '"$CONTROL_CHECKOUT/.github/scripts/reconcile-state-branch.sh" verify'
```

Provide the common identity, control checkout, preparation bundle, optional local outcome,
already-applied target checkout, verified-result destination, and trusted run URL. Upload
`verified-*` immediately afterward under `if: ${{ always() }}`.

- [ ] **Step 6: Add the final validation classification gate**

Under `if: ${{ always() }}`, require the verified manifest and accept `success`, `no-change`,
`branch-update`, `branch-init`, `branch-format`, or `branch-validation`. Exit non-zero for
`automation` after confirming its verified result exists. Any missing or malformed result also exits
non-zero.

- [ ] **Step 7: Remove the verify job and validation artefact**

Delete the complete `verify` job and the validation artefact upload/download steps. Change
publication to `needs: [discover, validate]`; retain its always-run condition so bounded failures and
the uploaded automation no-op reach publication. Do not add job outputs: matrix copies cannot safely
publish per-entry values through one shared job-output name.

- [ ] **Step 8: Enable formatting in both supplied callers**

```yaml
terraform_directories: .
terraform_fmt: true
```

Leave the reusable input default false.

- [ ] **Step 9: Run workflow and action syntax verification**

Run: `make test-github-actions`

Expected: PASS with exactly four jobs, no validation artefact, one fresh validation checkout, and no Terraform or provider execution in publication.

- [ ] **Step 10: Commit the combined job graph**

```bash
/Users/dan/.codex/bin/codex-git add examples/github-actions/test.sh examples/github-actions/reconcile-test.sh examples/github-actions/.github/workflows/tf-version-bump-reusable.yml examples/github-actions/.github/workflows/tf-version-bump-nonproduction.yml examples/github-actions/.github/workflows/tf-version-bump-production.yml
/Users/dan/.codex/bin/codex-git commit -m "refactor: fold verification into Terraform validation"
```

Verify the commit signature before continuing.

---

### Task 6: Document and Fully Verify Stage Two

**Files:**
- Modify: `examples/github-actions/README.md:1-113`
- Modify: `docs/ADVANCED-USAGE.md:9-48`

**Interfaces:**
- Consumes: the final input, artefact, job, commit, failure, and PR contracts from Tasks 1-5.
- Produces: copyable operator documentation and fresh full-repository verification evidence.

- [ ] **Step 1: Add documentation contract assertions**

Extend the existing documentation checks in `examples/github-actions/test.sh` to require the exact
input name/default, rc.9 pin, four job names, two commit subjects, split PR sections, block counts,
recursive configured-root semantics, trusted-provider warning, and `branch-format` description.

- [ ] **Step 2: Run documentation checks to prove the guides are stale**

Run: `make docs-check`

Expected: FAIL if the assertions live in Go documentation tests; otherwise run
`make test-github-actions` and expect the new Bash documentation assertions to fail.

- [ ] **Step 3: Update the copyable example README**

Document:

- `terraform_fmt` defaults false and both supplied callers set it true;
- updater then upgrade-init runs in every configured root before recursive formatting eligibility;
- validation and verification share one checkout and trust private/first-party provider code;
- preparation and verified artefacts remain, while the separate validation artefact/job is gone;
- one dynamic dependency commit plus optional `chore: run Terraform fmt` commit;
- exact module/provider counts and separate file lists in the managed PR;
- `branch-format`, automation failure, net-zero formatting, dry-run, and operator inspection behaviour;
- the rc.9 tag and SHA-256.

- [ ] **Step 4: Update advanced usage**

Keep the overview concise but mirror the four-job topology, formatting input/default, caller opt-in,
trusted-provider limitation, two-commit result, PR tracking, and rc.9 pin. Remove statements that
claim a separate fresh verification checkout or a validation artefact.

- [ ] **Step 5: Run the full example harness**

Run: `make test-github-actions`

Expected: PASS with pristine output.

- [ ] **Step 6: Run documentation checks**

Run: `make docs-check`

Expected: PASS.

- [ ] **Step 7: Run the full Go race and coverage suite**

Run: `go test -v -race -coverprofile=coverage.out -covermode=atomic ./...`

Expected: PASS with no unexpected stderr and a freshly written `coverage.out`.

- [ ] **Step 8: Run static analysis and build**

Run: `golangci-lint run --timeout=5m`

Expected: PASS with no findings.

Run: `go vet ./...`

Expected: PASS with no output.

Run: `go build ./...`

Expected: PASS with no output.

- [ ] **Step 9: Check the complete diff**

Run: `/Users/dan/.codex/bin/codex-git diff --check`

Expected: exit 0 with no output.

Run: `/Users/dan/.codex/bin/codex-git status --short`

Expected: only the intended documentation changes; ignored `coverage.out` is absent from the status,
and no binary or unrelated file appears.

- [ ] **Step 10: Commit documentation**

```bash
/Users/dan/.codex/bin/codex-git add examples/github-actions/README.md docs/ADVANCED-USAGE.md examples/github-actions/test.sh
/Users/dan/.codex/bin/codex-git commit -m "docs: explain staged Terraform update pull requests"
```

Verify the commit signature before continuing.

---

### Task 7: Run Independent Test Cleanup

**Files:**
- Review: `examples/github-actions/test.sh`
- Review: `examples/github-actions/reconcile-test.sh`
- Modify only if the independent cleanup identifies redundant or mock-only tests.

**Interfaces:**
- Consumes: the complete implementation and test diff from Tasks 1-6.
- Produces: a test suite containing distinct behavioural regression coverage without duplicated assertions or tests of controlled-executable behaviour alone.

- [ ] **Step 1: Invoke the required test-cleanup skill in a separate subagent**

Run the `test-cleanup` skill against `main...HEAD`. Instruct the cleanup reviewer to preserve every
distinct schema variant, path-policy rejection, stage ordering, failure lifecycle, commit topology,
and workflow trust-boundary test. It may remove only tests whose behaviour is already exercised by a
stronger real integration case.

- [ ] **Step 2: Review the cleanup diff independently**

Reject any change that lowers coverage, converts a real integration assertion into a stub assertion,
or removes pristine-output checking. Accept only deletions or consolidations with an identified
stronger owner test.

- [ ] **Step 3: Run the affected harness after cleanup**

Run: `make test-github-actions`

Expected: PASS with pristine output.

- [ ] **Step 4: Commit cleanup only when files changed**

```bash
/Users/dan/.codex/bin/codex-git add examples/github-actions/test.sh examples/github-actions/reconcile-test.sh
/Users/dan/.codex/bin/codex-git commit -m "test: consolidate staged workflow coverage"
```

If cleanup correctly recommends no changes, record that outcome in the execution journal and do not create an empty commit. Verify any created commit signature.

---

### Task 8: Review, Verify, and Hand Off the Branch

**Files:**
- Review: complete `main...HEAD` branch diff
- Modify: only files required by legitimate review findings

**Interfaces:**
- Consumes: implementation, tests, docs, and cleanup result from Tasks 1-7.
- Produces: independently reviewed, findings-verified, fully tested, signed branch history ready for PR creation.

- [ ] **Step 1: Run the requesting-code-review skill**

Invoke `superpowers:requesting-code-review` on the complete branch against `main`. Require reviewers
to check the approved spec, same-runner trust statement, schema presence rules, stage patch
integrity, matrix failure flow, write-credential boundary, exact commit topology, PR encoding, and
real integration coverage.

- [ ] **Step 2: Run peer adversarial review**

Invoke `$par` on the complete branch. Save and report the consolidated findings file produced by the
skill. Do not implement disputed or architectural findings without Dan's decision.

- [ ] **Step 3: Address every approved finding with TDD**

Use `superpowers:receiving-code-review`. For each approved behaviour defect, add a failing regression
test, run it to observe the expected failure, implement the smallest root-cause fix, rerun the focused
test, then rerun `make test-github-actions`. Commit each coherent correction with the Codex Git
wrapper and verify its signature.

- [ ] **Step 4: Verify the findings file**

Invoke `$verify` against the saved tf-version-bump findings file. Require every item to be resolved
and no collateral issues before continuing.

- [ ] **Step 5: Run final verification from a clean worktree**

Run these commands independently and inspect their complete output:

```bash
make test-github-actions
make docs-check
go test -v -race -coverprofile=coverage.out -covermode=atomic ./...
golangci-lint run --timeout=5m
go vet ./...
go build ./...
/Users/dan/.codex/bin/codex-git diff --check main...HEAD
/Users/dan/.codex/bin/codex-git status --short --branch
```

Expected: every command exits 0; test output is pristine; Git reports no worktree changes and only
the intended topic-branch commits ahead of `main`.

- [ ] **Step 6: Verify every branch commit signature**

Run:

```bash
/Users/dan/.codex/bin/codex-git log --show-signature --format=fuller main..HEAD
```

Expected: every commit reports a good signature from the GenAI key; stop without pushing if any
signature is missing or invalid.

- [ ] **Step 7: Use the finishing-development-branch skill**

Invoke `superpowers:finishing-a-development-branch`, present the verified integration options to
Dan, and do not push, create a PR, rebase-merge, tag, or publish another release without his explicit
instruction.
