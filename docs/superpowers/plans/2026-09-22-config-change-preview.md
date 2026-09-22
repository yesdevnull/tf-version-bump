# Config-change preview Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When a same-repository pull request into the default branch changes a policy's control config or caller workflow, the GitHub Actions example processes one sampled state branch per configured prefix, exactly as a live run would, and publishes nothing.

**Architecture:**
- Discovery gains a preview mode. It accepts only a `refs/pull/<n>/merge` caller ref, seeds a deterministic per-prefix sample from `<n>`, and otherwise emits the same matrix as today.
- The reusable workflow gains a `preview` input. It skips `publish` and adds the candidate patch to the `process` summary.
- Each caller gains a `preview` job. It reuses the live job's `with:`/`secrets:` blocks through YAML aliases and carries its own cancelling concurrency.
- `reconcile-state-branch.sh publish` independently refuses any caller ref other than the default branch.

**Tech Stack:** Bash (the scripts run under `set -euo pipefail` with `LC_ALL=C`), GitHub Actions YAML, `jq`, mikefarah `yq`, the example's own harnesses (`examples/github-actions/test.sh`, `reconcile-test.sh`, `report-test.sh`), Go documentation tests, actionlint via `make actionlint`, shellcheck 0.11.0 via `make shellcheck`.

**Spec:** `docs/superpowers/specs/2026-09-22-config-change-preview-design.md`. Read it before starting any task; it carries the reasoning and the verified GitHub Actions behaviour.

## Global Constraints

**Prose and Markdown**
- Use Australian / British spelling in prose and comments.
- Write each Markdown paragraph, list item and quote on one line. `TestDocumentationProseIsNotHardWrapped` fails on hard-wrapped prose.

**Process**
- Follow TDD in every task: write the failing test, watch it fail, implement, watch it pass.
- Test output must be pristine. Capture and assert expected diagnostics.

**Commits and pushes**
- Commit with the GenAI wrapper, passing the message through a file: `/Users/dan/.claude/bin/claude-git -C /Users/dan/Code/tf-version-bump commit -F <message file>`. Write the message file in the session scratchpad.
- End every commit message with these two lines:
  - `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`
  - `Claude-Session: https://claude.ai/code/session_01AmjqpPp4yfPeg11EgGETuQ`
- Never `git add -A`. Stage named paths.
- Never push without Dan's confirmation.

**Harness**
- Run the example harness with the GenAI signer so fixture commits never reach Dan's 1Password signer: `TEST_GIT=/Users/dan/.claude/bin/claude-git examples/github-actions/test.sh <test_name>` (from the repository root).
- Discovery and workflow-step tests need no Docker; `test_processing_*` tests do.

**Workflow YAML**
- Keep each caller's `with:` keys indented six spaces. `scripts/update-actions-release-pin.sh` matches `      tf_version_bump_version:` and `      tf_version_bump_archive_sha256:` exactly once per caller.
- Never combine `queue: max` with `cancel-in-progress: true` in one `concurrency` block; GitHub rejects it.
- Do not use YAML merge keys (`<<`). Only anchors (`&name`) and whole-node aliases (`*name`).
- The preview job must not declare job-level `permissions`. A read-only caller job fails at startup when the called workflow's `publish` job requests write, even when it is skipped (verified; see the spec).
- Pass values into `run:` bodies only through `env:`, never `${{ }}` inside a script body.

**Size**
- Keep scripts and workflows small; consumers copy them.

---

## File Structure

| File | Change | Responsibility |
| --- | --- | --- |
| `examples/github-actions/.github/scripts/discover-state-branches.sh` | Modify | Preview guard (`DISCOVERY_PREVIEW`, PR merge ref, seed) and per-prefix sampling |
| `examples/github-actions/.github/scripts/reconcile-state-branch.sh` | Modify | Refuse publication from any ref but the default branch |
| `examples/github-actions/.github/workflows/tf-version-bump-reusable.yml` | Modify | `preview` input, discovery and publish wiring, `publish` gate, shared fenced renderer, preview patch |
| `examples/github-actions/.github/workflows/tf-version-bump-nonproduction.yml` | Modify | PR trigger, job-level concurrency, `preview` job via aliases |
| `examples/github-actions/.github/workflows/tf-version-bump-production.yml` | Modify | Same as non-production |
| `examples/github-actions/test.sh` | Modify | Discovery, reusable-workflow and caller tests |
| `examples/github-actions/reconcile-test.sh` | Modify | Publication guard tests |
| `examples/github-actions/README.md` | Modify | New "Preview on pull requests" section; four passage edits |
| `docs/ADVANCED-USAGE.md` | Modify | GitHub Actions paragraph |

---

### Task 1: Discovery preview guard

**Files:**
- Modify: `examples/github-actions/.github/scripts/discover-state-branches.sh:83-85` (the caller-ref check)
- Modify: `examples/github-actions/test.sh:362-430` (`setup_discovery_repository`, `run_discovery`)
- Test: `examples/github-actions/test.sh` (new `test_discovery_preview_accepts_only_pull_request_merge_refs`)

**Interfaces:**
- Consumes: nothing new.
- Produces:
  - Env input `DISCOVERY_PREVIEW`: unset means `false`. Otherwise it must be exactly `true` or `false`.
  - Shell variables `preview` (`true`/`false`) and `preview_seed` (the decimal PR number, set only when `preview=true`). Task 2 reads both.
  - Diagnostics, exactly:
    - `discovery input error: DISCOVERY_PREVIEW must be true or false`
    - `discovery caller error: preview caller ref must be refs/pull/<number>/merge`
    - `discovery input error: a preview cannot take a manual prefix`

- [ ] **Step 1: Let the harness pass `DISCOVERY_PREVIEW` only when a test sets it**

In `setup_discovery_repository` (`test.sh:362`), add this as its second line, after `cleanup_discovery_repository`:

```bash
    unset DISCOVERY_PREVIEW
```

In `run_discovery` (`test.sh:414`), export `DISCOVERY_PREVIEW` inside the existing subshell only when a test has set it. An unset `DISCOVERY_PREVIEW` then stays unset for the script, which is the report workflow's path. Do not use `env` for this: `test_discovery_uses_runner_git_not_a_workstation_shim` runs discovery with a `PATH` that has no `env`. The subshell keeps the export from leaking. Change the line after `cd "$DISCOVERY_REPO"` to:

```bash
        cd "$DISCOVERY_REPO"
        [[ -z "${DISCOVERY_PREVIEW+set}" ]] || export DISCOVERY_PREVIEW
```

- [ ] **Step 2: Write the failing test**

Add after `test_discovery_rejects_invalid_inputs_by_stage` in `test.sh`:

```bash
test_discovery_preview_accepts_only_pull_request_merge_refs() {
    # Production break caught: relaxing the default-branch guard for previews lets any ref drive
    # discovery, a pull-request ref passes without asking for a preview, or a preview's seed can
    # disagree with the pull request it runs for.
    setup_discovery_repository
    add_discovery_branch "state/prod/example"
    DISCOVERY_ALLOWED_PREFIXES="state/prod/"

    DISCOVERY_CALLER_REF="refs/pull/42/merge"
    assert_discovery_failure "discovery caller error: caller ref must be refs/heads/main" \
        "a pull-request ref without a preview"

    DISCOVERY_PREVIEW=true
    local output
    output=$(run_discovery)
    jq -e '.include | map(.branch) == ["state/prod/example"]' <<<"$output" >/dev/null \
        || fail "preview discovery refused a pull-request merge ref: $output"

    local ref
    for ref in refs/heads/main refs/pull/0/merge refs/pull/042/merge refs/pull/7/head refs/pull/x/merge; do
        DISCOVERY_CALLER_REF=$ref
        assert_discovery_failure "discovery caller error: preview caller ref must be refs/pull/<number>/merge" \
            "a preview from $ref"
    done

    DISCOVERY_CALLER_REF="refs/pull/42/merge"
    DISCOVERY_MANUAL_PREFIX="state/prod/"
    assert_discovery_failure "discovery input error: a preview cannot take a manual prefix" \
        "a preview with a manual prefix"

    DISCOVERY_MANUAL_PREFIX=""
    local value
    for value in "" TRUE yes; do
        DISCOVERY_PREVIEW=$value
        assert_discovery_failure "discovery input error: DISCOVERY_PREVIEW must be true or false" \
            "a preview flag of '$value'"
    done
}
```

`test_discovery_*` functions are collected automatically by `compgen` in the harness's no-argument run, so there is no list to update.

- [ ] **Step 3: Run it and watch it fail**

Run: `TEST_GIT=/Users/dan/.claude/bin/claude-git examples/github-actions/test.sh test_discovery_preview_accepts_only_pull_request_merge_refs`

Expected: the harness exits non-zero without printing `PASS:`. Discovery still demands `refs/heads/main`, so `output=$(run_discovery)` fails under the harness's `set -e`, and discovery's own `discovery caller error: caller ref must be refs/heads/main` is the last line on stderr.

- [ ] **Step 4: Implement the guard**

In `discover-state-branches.sh`, replace lines 83–85:

```bash
expected_caller_ref="refs/heads/$DISCOVERY_DEFAULT_BRANCH"
[[ "$DISCOVERY_CALLER_REF" == "$expected_caller_ref" ]] \
    || fail_discovery caller "caller ref must be $expected_caller_ref"
```

with:

```bash
preview=${DISCOVERY_PREVIEW-false}
[[ "$preview" == true || "$preview" == false ]] \
    || fail_discovery input "DISCOVERY_PREVIEW must be true or false"
if [[ "$preview" == true ]]; then
    # A preview runs from a pull request's merge ref. Its number seeds the sample, so the seed
    # cannot disagree with the pull request, and a manual prefix only arises from a dispatch.
    [[ "$DISCOVERY_CALLER_REF" =~ ^refs/pull/([1-9][0-9]*)/merge$ ]] \
        || fail_discovery caller "preview caller ref must be refs/pull/<number>/merge"
    preview_seed=${BASH_REMATCH[1]}
    [[ -z "$DISCOVERY_MANUAL_PREFIX" ]] \
        || fail_discovery input "a preview cannot take a manual prefix"
else
    expected_caller_ref="refs/heads/$DISCOVERY_DEFAULT_BRANCH"
    [[ "$DISCOVERY_CALLER_REF" == "$expected_caller_ref" ]] \
        || fail_discovery caller "caller ref must be $expected_caller_ref"
fi
```

Also add `DISCOVERY_PREVIEW` to `usage()` so `--help` documents it. Replace the heredoc body line `Discover immutable inputs for configured Terraform state branches.` with:

```text
Discover immutable inputs for configured Terraform state branches.
With DISCOVERY_PREVIEW=true, run from refs/pull/<number>/merge and select one
sampled branch per prefix instead of every matching branch.
```

- [ ] **Step 5: Run the new test and every discovery test**

Run: `TEST_GIT=/Users/dan/.claude/bin/claude-git examples/github-actions/test.sh test_discovery_preview_accepts_only_pull_request_merge_refs`

Expected: `PASS: test_discovery_preview_accepts_only_pull_request_merge_refs`

Then run every existing discovery test by name (list them with `grep -o '^test_discovery_[a-z_]*' examples/github-actions/test.sh`), passing all names as arguments in one call. Expected: one `PASS:` line per test and nothing else.

Then run `examples/github-actions/report-test.sh`, whose report workflow calls discovery without `DISCOVERY_PREVIEW`. Expected: all `PASS:`.

- [ ] **Step 6: Shellcheck and commit**

Run: `make shellcheck`. Expected: no output, exit 0.

```bash
git -C /Users/dan/Code/tf-version-bump add examples/github-actions/.github/scripts/discover-state-branches.sh examples/github-actions/test.sh
```

Commit message: `feat(actions-example): accept pull-request refs in preview discovery`.

---

### Task 2: Discovery per-prefix sampling

**Files:**
- Modify: `examples/github-actions/.github/scripts/discover-state-branches.sh`, from the branch-matching loop (~line 143) to the sort (~line 174)
- Test: `examples/github-actions/test.sh` (new `test_discovery_preview_samples_one_branch_per_prefix`, `test_discovery_preview_warns_for_prefixes_that_own_no_branch`, `test_discovery_preview_keeps_the_live_matrix_limit`)

**Interfaces:**
- Consumes: `preview` and `preview_seed` from Task 1; the existing `selection_prefixes` array and `branch_records` array (`"<branch>\t<oid>"` entries).
- Produces:
  - In preview mode, `branch_records` holds one sorted record per prefix that owns a branch. The matrix JSON shape is unchanged.
  - Preview-only warning on stderr, exactly: `::warning::branch prefix '<prefix>' owns no branch to preview`, with `%` escaped as `%25`.

- [ ] **Step 1: Write the failing tests**

Add after the Task 1 test in `test.sh`:

```bash
test_discovery_preview_samples_one_branch_per_prefix() {
    # Production break caught: a preview processes every branch, picks differently on each push,
    # or samples a branch its caller excludes.
    setup_discovery_repository
    local name
    for name in alpha bravo charlie; do
        add_discovery_branch "state/staging/$name"
        add_discovery_branch "state/production/$name"
    done
    add_discovery_branch "state/production/excluded"
    DISCOVERY_ALLOWED_PREFIXES=$'state/staging/\nstate/production/\n!state/production/excluded'
    DISCOVERY_PREVIEW=true
    local output stderr_file="$DISCOVERY_TMP_ROOT/sample.stderr"

    # Known answers: the first 15 hex digits of sha256("<number>\t<prefix>"), modulo the three
    # branches each prefix owns, index the sorted branches. Pull request 42 picks index 2 of
    # state/production/ and 0 of state/staging/; pull request 43 picks index 1 of both.
    DISCOVERY_CALLER_REF="refs/pull/42/merge"
    output=$(run_discovery 2>"$stderr_file")
    jq -e '.include | map(.branch) == ["state/production/charlie", "state/staging/alpha"]' \
        <<<"$output" >/dev/null || fail "pull request 42 did not sample its known branches: $output"
    [[ ! -s "$stderr_file" ]] || fail "sampling reported a warning: $(<"$stderr_file")"
    output=$(run_discovery)
    jq -e '.include | map(.branch) == ["state/production/charlie", "state/staging/alpha"]' \
        <<<"$output" >/dev/null || fail "a repeat preview of pull request 42 sampled differently: $output"

    DISCOVERY_CALLER_REF="refs/pull/43/merge"
    output=$(run_discovery)
    jq -e '.include | map(.branch) == ["state/production/bravo", "state/staging/bravo"]' \
        <<<"$output" >/dev/null || fail "pull request 43 did not sample its known branches: $output"
}


test_discovery_preview_warns_for_prefixes_that_own_no_branch() {
    # Production break caught: a prefix with nothing to preview passes silently, a narrower prefix
    # listed after a broader one is reported as matching nothing without saying why, a repeated
    # prefix previews its branch twice, or live runs gain the warning.
    setup_discovery_repository
    add_discovery_branch "state/staging/alpha"
    DISCOVERY_ALLOWED_PREFIXES=$'state/\nstate/staging/\nstate/\naws-state/100%/'
    local output stderr_file="$DISCOVERY_TMP_ROOT/unowned.stderr"

    output=$(run_discovery 2>"$stderr_file")
    [[ ! -s "$stderr_file" ]] || fail "a live run reported unowned prefixes: $(<"$stderr_file")"

    DISCOVERY_PREVIEW=true
    DISCOVERY_CALLER_REF="refs/pull/42/merge"
    output=$(run_discovery 2>"$stderr_file")
    jq -e '.include | map(.branch) == ["state/staging/alpha"]' <<<"$output" >/dev/null \
        || fail "a preview did not sample the one owned branch exactly once: $output"
    [[ "$(<"$stderr_file")" == "::warning::branch prefix 'state/staging/' owns no branch to preview"$'\n'"::warning::branch prefix 'aws-state/100%25/' owns no branch to preview" ]] \
        || fail "a preview did not name each prefix that owns no branch once: $(<"$stderr_file")"
}


test_discovery_preview_keeps_the_live_matrix_limit() {
    # Production break caught: a policy the live run refuses for exceeding the matrix limit
    # previews green because the sample itself is small.
    setup_discovery_repository
    add_numbered_discovery_branches 0 256
    DISCOVERY_ALLOWED_PREFIXES="state/limit/"
    DISCOVERY_PREVIEW=true
    DISCOVERY_CALLER_REF="refs/pull/42/merge"
    assert_discovery_failure "discovery matrix error:" "a preview of more than 256 branches"
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `TEST_GIT=/Users/dan/.claude/bin/claude-git examples/github-actions/test.sh test_discovery_preview_samples_one_branch_per_prefix test_discovery_preview_warns_for_prefixes_that_own_no_branch test_discovery_preview_keeps_the_live_matrix_limit`

Expected:
- The first fails with `pull request 42 did not sample its known branches`, because all six branches are returned.
- The third already passes: the limit check runs before any sampling exists. It stays as the regression guard for the ordering in Step 3.

- [ ] **Step 3: Implement sampling**

In `discover-state-branches.sh`:
- Change `branch_records=()` (just before the `while IFS=$'\t' read -r oid ref` loop) to also declare the owner map:

```bash
branch_records=()
declare -A branch_owners=()
```

- Inside the prefix loop, record the owning prefix next to the record:

```bash
    for prefix in "${selection_prefixes[@]}"; do
        if [[ "$branch" == "$prefix"* ]]; then
            branch_records+=("$branch"$'\t'"$oid")
            branch_owners[$branch]=$prefix
            break
        fi
    done
```

- Define this function directly after `branch_is_excluded()`:

```bash
# Keeps one branch per prefix, so a preview processes a sample rather than the whole policy. A
# branch belongs to the first prefix it matches; the pick is the first 15 hex digits of
# sha256("<seed>\t<prefix>") modulo the prefix's branch count, which stays within bash's signed
# 64-bit arithmetic and is stable while that prefix's branches are unchanged.
sample_branch_records() {
    local prefix record digest
    local -a owned sampled=()
    declare -A seen_prefixes=()
    for prefix in "${selection_prefixes[@]}"; do
        [[ -z "${seen_prefixes[$prefix]-}" ]] || continue
        seen_prefixes[$prefix]=1
        owned=()
        for record in "${branch_records[@]}"; do
            [[ "${branch_owners[${record%%$'\t'*}]}" != "$prefix" ]] || owned+=("$record")
        done
        if [[ ${#owned[@]} -eq 0 ]]; then
            echo "::warning::branch prefix '${prefix//'%'/%25}' owns no branch to preview" >&2
            continue
        fi
        digest=$(printf '%s\t%s' "$preview_seed" "$prefix" | sha256sum)
        sampled+=("${owned[$((16#${digest:0:15} % ${#owned[@]}))]}")
    done
    readarray -t branch_records < <(printf '%s\n' "${sampled[@]}" | sort)
}
```

- Directly after the existing line `readarray -t branch_records < <(printf '%s\n' "${branch_records[@]}" | sort)`, add:

```bash
[[ "$preview" != true ]] || sample_branch_records
```

This order keeps the selection and 256-branch checks on the full matched set. It also means `owned` is built from sorted records.

`sampled` is never empty here. Selection already failed if nothing matched, and every matched record has an owner.

- [ ] **Step 4: Run the tests and watch them pass**

Run the Step 2 command again. Expected: three `PASS:` lines and nothing else.

Then run every discovery test by name, as in Task 1 Step 5, plus `examples/github-actions/report-test.sh`. Expected: all `PASS:`.

- [ ] **Step 5: Shellcheck and commit**

Run: `make shellcheck`. Expected: no output.

```bash
git -C /Users/dan/Code/tf-version-bump add examples/github-actions/.github/scripts/discover-state-branches.sh examples/github-actions/test.sh
```

Commit message: `feat(actions-example): sample one state branch per prefix in preview discovery`.

---

### Task 3: Publication guard

**Files:**
- Modify: `examples/github-actions/.github/scripts/reconcile-state-branch.sh` (`usage()`, new `require_default_branch_caller`, first line of `publish_result`)
- Modify: `examples/github-actions/.github/workflows/tf-version-bump-reusable.yml` (`Publish processing result` step `env`)
- Modify: `examples/github-actions/reconcile-test.sh` (`run_publish`, new test, test list)
- Test: `examples/github-actions/test.sh` (`test_workflow_runs_three_jobs_with_current_attempt_results`)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - Required `publish` inputs `RECONCILE_CALLER_REF` and `RECONCILE_DEFAULT_BRANCH`.
  - Diagnostic `reconciliation error: publication runs only from refs/heads/<default branch>`.

- [ ] **Step 1: Give the reconcile harness a default-branch caller**

In `reconcile-test.sh` `run_publish`, add two lines after `RECONCILE_RUN_URL=...`:

```bash
        RECONCILE_CALLER_REF=${RECONCILE_CALLER_REF-refs/heads/main} \
        RECONCILE_DEFAULT_BRANCH=main \
```

- [ ] **Step 2: Write the failing reconcile test**

Add before the `if [[ $# -eq 0 ]]` block in `reconcile-test.sh`:

```bash
test_publication_runs_only_from_the_default_branch() {
    # Production break caught: a preview whose publish gate is lost, or any other run from a
    # non-default ref, pushes update refs or changes pull requests and issues from unmerged config.
    local caller_ref dry_run remote
    for caller_ref in refs/pull/7/merge refs/heads/feature; do
        for dry_run in true false; do
            setup_success_fixture
            remote=$FIXTURE_REMOTE
            if RECONCILE_CALLER_REF=$caller_ref RECONCILE_DRY_RUN=$dry_run run_publish \
                >"$FIXTURE_ROOT/stdout" 2>"$FIXTURE_ROOT/stderr"; then
                fail "publication from $caller_ref (dry run $dry_run) was accepted"
            fi
            [[ "$(<"$FIXTURE_ROOT/stderr")" == 'reconciliation error: publication runs only from refs/heads/main' ]] \
                || fail "publication from $caller_ref reported: $(<"$FIXTURE_ROOT/stderr")"
            [[ ! -s "$FIXTURE_ROOT/stdout" ]] || fail 'refused publication emitted stdout'
            [[ ! -f "$FIXTURE_GH_CAPTURE/calls" ]] || fail "publication from $caller_ref called GitHub"
            [[ -z "$("$TEST_GIT" --git-dir "$remote" for-each-ref --format='%(refname)' refs/heads/update_)" ]] \
                || fail "publication from $caller_ref pushed an update ref"
        done
    done
}
```

Append `test_publication_runs_only_from_the_default_branch` to the `tests=(...)` list at the bottom of `reconcile-test.sh`.

- [ ] **Step 3: Run it and watch it fail**

Run: `TEST_GIT=/Users/dan/.claude/bin/claude-git examples/github-actions/reconcile-test.sh test_publication_runs_only_from_the_default_branch`

Expected: `FAIL: publication from refs/pull/7/merge (dry run true) was accepted`.

- [ ] **Step 4: Implement the guard**

In `reconcile-state-branch.sh`, add after `require_common_identity()`:

```bash
# Publication must not depend on the publish job's `if` alone: a pull-request run executes the
# workflows it changes, so this refuses every ref but the default branch, dry run or not.
require_default_branch_caller() {
    : "${RECONCILE_CALLER_REF:?RECONCILE_CALLER_REF must be set}"
    : "${RECONCILE_DEFAULT_BRANCH:?RECONCILE_DEFAULT_BRANCH must be set}"
    [[ "$RECONCILE_CALLER_REF" == "refs/heads/$RECONCILE_DEFAULT_BRANCH" ]] \
        || reconcile_error "publication runs only from refs/heads/$RECONCILE_DEFAULT_BRANCH"
}
```

Make it the first line of `publish_result()`, before `validate_result`:

```bash
publish_result() {
    require_default_branch_caller
    validate_result
```

In `usage()`, change `PRs and failure issues. Requires RECONCILE_DRY_RUN (true/false), RUNNER_TEMP,` to:

```text
PRs and failure issues, only when RECONCILE_CALLER_REF is refs/heads/ followed by
RECONCILE_DEFAULT_BRANCH. Requires RECONCILE_DRY_RUN (true/false), RUNNER_TEMP,
```

- [ ] **Step 5: Run the reconcile harness**

Run: `TEST_GIT=/Users/dan/.claude/bin/claude-git examples/github-actions/reconcile-test.sh`

Expected: every test prints `PASS:`, including the new one.

- [ ] **Step 6: Wire the workflow and pin it with a failing check**

In `test.sh` `test_workflow_runs_three_jobs_with_current_attempt_results`, add this clause before the closing `'` of its jq program (after the `RECONCILE_TF_VERSION_BUMP_VERSION` clause, joined with `and`):

```text
        and ([.publish.steps[] | select(.env.RECONCILE_RUN_URL != null)
              | .env | {RECONCILE_CALLER_REF, RECONCILE_DEFAULT_BRANCH}]
             == [{RECONCILE_CALLER_REF: "${{ github.ref }}",
                  RECONCILE_DEFAULT_BRANCH: "${{ github.event.repository.default_branch }}"}])
```

Run: `TEST_GIT=/Users/dan/.claude/bin/claude-git examples/github-actions/test.sh test_workflow_runs_three_jobs_with_current_attempt_results`

Expected: `FAIL: workflow does not wire the three-job current-attempt result contract`.

In `tf-version-bump-reusable.yml`, in the `Publish processing result` step's `env`, add after `RECONCILE_RUN_URL`:

```yaml
          RECONCILE_CALLER_REF: ${{ github.ref }}
          RECONCILE_DEFAULT_BRANCH: ${{ github.event.repository.default_branch }}
```

Run the same test again. Expected: `PASS:`.

- [ ] **Step 7: Lint and commit**

Run: `make shellcheck` and `make actionlint`. Expected: no findings.

```bash
git -C /Users/dan/Code/tf-version-bump add examples/github-actions/.github/scripts/reconcile-state-branch.sh examples/github-actions/.github/workflows/tf-version-bump-reusable.yml examples/github-actions/reconcile-test.sh examples/github-actions/test.sh
```

Commit message: `feat(actions-example): refuse publication from any ref but the default branch`.

---

### Task 4: Reusable workflow preview mode

**Files:**
- Modify: `examples/github-actions/.github/workflows/tf-version-bump-reusable.yml`:
  - `workflow_call.inputs`
  - `discover` step `env`
  - `publish.if`
  - `Report processing result` step
- Test: `examples/github-actions/test.sh`:
  - `workflow_publishes_after_processing_failures` and `test_workflow_publishes_every_branch_after_processing_failures`
  - `test_workflow_reports_the_processing_result`
  - new `test_workflow_previews_without_publishing`
  - new `run_preview_report_step` and `test_workflow_summarises_the_preview_patch`
  - the `tests=(...)` list

**Interfaces:**
- Consumes: `DISCOVERY_PREVIEW` (Task 1).
- Produces:
  - Reusable input `preview` (boolean, default `false`), which callers pass in Task 5.
  - `PREVIEW` env on `Report processing result`.
  - Shell function `append_fenced_file <heading> <file> <noun>` inside that step.

- [ ] **Step 1: Write the failing wiring and gate tests**

In `test.sh`, change the condition in `workflow_publishes_after_processing_failures`:

```bash
        | jq -e --arg condition "\${{ always() && needs.discover.result == 'success' && !inputs.preview }}" '
```

In `test_workflow_publishes_every_branch_after_processing_failures`, add this mutant after the `publish-condition.yml` block:

```bash
    mutant="$TEST_TMP_ROOT/publish-without-preview-gate.yml"
    yq ".jobs.publish.if = \"\${{ always() && needs.discover.result == 'success' }}\"" \
        "$REUSABLE_WORKFLOW" >"$mutant"
    ! workflow_publishes_after_processing_failures "$mutant" \
        || fail 'the guard passes when a preview can reach publication'
```

In `test_workflow_reports_the_processing_result`, extend the first jq check. Change `and .[0].env.RESULT_MANIFEST == $result + "/result.json"` to:

```text
          and .[0].env.RESULT_MANIFEST == $result + "/result.json"
          and .[0].env.PREVIEW == "${{ inputs.preview }}"
```

Add a new test after `test_workflow_fails_discovery_when_the_script_fails`:

```bash
test_workflow_previews_without_publishing() {
    # Production break caught: callers cannot ask for a preview, or discovery never hears of it and
    # processes every branch from a pull request.
    yq -o=json '.' "$REUSABLE_WORKFLOW" | jq -e '
        .on.workflow_call.inputs.preview == {type: "boolean", default: false}
        and ([.jobs.discover.steps[] | select(.id == "discover") | .env.DISCOVERY_PREVIEW]
             == ["${{ inputs.preview }}"])
    ' >/dev/null || fail 'the reusable workflow does not offer a preview input wired into discovery'
}
```

Add `test_workflow_previews_without_publishing` to the `tests=(...)` list, after `test_workflow_fails_discovery_when_the_script_fails`.

- [ ] **Step 2: Run them and watch them fail**

Run: `TEST_GIT=/Users/dan/.claude/bin/claude-git examples/github-actions/test.sh test_workflow_publishes_every_branch_after_processing_failures test_workflow_reports_the_processing_result test_workflow_previews_without_publishing`

Expected: `FAIL: a processing failure cancels other branches or skips their publication`, the gate string no longer matching. Run the other two individually and see them fail too.

- [ ] **Step 3: Implement the input, discovery wiring and gate**

In `tf-version-bump-reusable.yml`:
- Under `workflow_call.inputs`, after `dry_run`:

```yaml
      preview:
        type: boolean
        default: false
```

- In the `discover` step `env`, after `DISCOVERY_MANUAL_PREFIX`:

```yaml
          DISCOVERY_PREVIEW: ${{ inputs.preview }}
```

- Replace the `publish` job `if`:

```yaml
    if: ${{ always() && needs.discover.result == 'success' && !inputs.preview }}
```

- In the `Report processing result` step `env`, after `RESULT_MANIFEST`:

```yaml
          PREVIEW: ${{ inputs.preview }}
```

- [ ] **Step 4: Run the Step 2 tests and watch them pass**

Expected: three `PASS:` lines.

- [ ] **Step 5: Write the failing patch-summary test**

Add after `test_workflow_summarises_update_logs` in `test.sh`:

```bash
run_preview_report_step() {
    run_workflow_step process 'Report processing result' "$3" \
        PROCESS_OUTCOME="$2" RESULT_MANIFEST="$1" PREVIEW="$4"
}


test_workflow_summarises_the_preview_patch() {
    # Production break caught: a preview hides the candidate it produced, a live run's summary
    # gains the patch, or backticks or size in the patch break the summary.
    local work="$TEST_TMP_ROOT/report-patch"
    rm -rf -- "$work"
    mkdir -p "$work/logs"
    local manifest="$work/result.json" summary="$work/summary.md" diagnostics="$work/stderr"
    local report

    write_report_manifest "$manifest" success null '["."]'
    printf '%s\n' '-  version = "1.0.0"' '+  version = "2.0.0"' '```' >"$work/candidate.patch"
    assert_silent_success 'summarising a preview patch' "$work/stdout" "$diagnostics" \
        run_preview_report_step "$manifest" success "$summary" true
    report=$(<"$summary")
    [[ "$report" == *'#### Candidate patch'* && "$report" == *'+  version = "2.0.0"'* ]] \
        || fail "the preview summary omits the candidate patch: $report"
    [[ $(grep -cx '````' "$summary") -eq 2 ]] \
        || fail "the patch's fence does not outlast the backticks inside it: $report"

    assert_silent_success 'summarising a live result' "$work/stdout" "$diagnostics" \
        run_preview_report_step "$manifest" success "$summary" false
    [[ "$(<"$summary")" != *'Candidate patch'* ]] \
        || fail 'a live run summary shows the candidate patch'

    awk 'BEGIN { for (i = 1; i <= 1200; i++) printf "+line %04d %060d\n", i, 0 }' \
        >"$work/candidate.patch"
    assert_silent_success 'summarising an oversized preview patch' "$work/stdout" "$diagnostics" \
        run_preview_report_step "$manifest" success "$summary" true
    [[ "$(<"$summary")" == *$'\n```\n\nThis patch was truncated'* ]] \
        || fail "an oversized patch was not truncated with its closing fence on its own line: $(<"$summary")"

    rm -f -- "$work/candidate.patch"
    write_report_manifest "$manifest" no-change
    assert_silent_success 'summarising a preview without a patch' "$work/stdout" "$diagnostics" \
        run_preview_report_step "$manifest" success "$summary" true
    [[ "$(<"$summary")" != *'Candidate patch'* ]] \
        || fail 'a preview without a candidate patch claims one'
}
```

Add `test_workflow_summarises_the_preview_patch` to the `tests=(...)` list, after `test_workflow_summarises_update_logs`.

- [ ] **Step 6: Run it and watch it fail**

Run: `TEST_GIT=/Users/dan/.claude/bin/claude-git examples/github-actions/test.sh test_workflow_summarises_the_preview_patch`

Expected: `FAIL: the preview summary omits the candidate patch`.

- [ ] **Step 7: Implement the shared renderer and the patch**

In the `Report processing result` step's `run:` body, replace everything from `# Only the updater's log is shown: ...` through `done < <(jq -r '(.roots // [])[]' "$RESULT_MANIFEST")` with:

```bash
          # Appends a file in a fence one backtick longer than any run inside it, truncating past
          # 64 KiB at a line boundary so the closing fence stays on a line of its own.
          append_fenced_file() {
            local heading=$1 file=$2 noun=$3 fence='```' longest
            longest=$({ grep -aoE '`+' "$file" || true; } | awk '{ if (length > max) max = length } END { print max + 0 }')
            while [[ ${#fence} -le $longest ]]; do fence+='`'; done
            printf '\n#### %s\n\n%s\n' "$heading" "$fence" >>"$GITHUB_STEP_SUMMARY"
            if [[ $(wc -c <"$file") -gt 65536 ]]; then
              head -c 65536 "$file" | sed '$d' >>"$GITHUB_STEP_SUMMARY"
              printf '%s\n\nThis %s was truncated; the processing artefact holds it in full.\n' "$fence" "$noun" >>"$GITHUB_STEP_SUMMARY"
            else
              awk 1 "$file" >>"$GITHUB_STEP_SUMMARY"
              printf '%s\n' "$fence" >>"$GITHUB_STEP_SUMMARY"
            fi
          }
          # Only the updater's log is shown: Terraform's logs can carry a credential a provider echoed.
          index=0
          while IFS= read -r root; do
            index=$((index + 1))
            log="${RESULT_MANIFEST%/*}/logs/update-$index.log"
            [[ -f "$log" ]] || continue
            append_fenced_file "Updates in \`$root\`" "$log" log
          done < <(jq -r '(.roots // [])[]' "$RESULT_MANIFEST")
          # The patch holds only Terraform files and root lock files, never Terraform's logs.
          patch="${RESULT_MANIFEST%/*}/candidate.patch"
          if [[ "$PREVIEW" == true && -f "$patch" ]]; then
            append_fenced_file 'Candidate patch' "$patch" patch
          fi
```

`PREVIEW` is unset when the harness's existing `run_report_step` runs the body. The step runs under `bash -e` without `-u`, so `"$PREVIEW" == true` is simply false there.

- [ ] **Step 8: Run the summary tests**

Run: `TEST_GIT=/Users/dan/.claude/bin/claude-git examples/github-actions/test.sh test_workflow_summarises_the_preview_patch test_workflow_summarises_update_logs test_workflow_reports_the_processing_result`

Expected: three `PASS:` lines. `test_workflow_summarises_update_logs` still passes unchanged, which proves the refactor kept the log rendering byte-for-byte, including `This log was truncated`.

- [ ] **Step 9: Lint and commit**

Run: `make actionlint` and `make shellcheck`. actionlint also runs shellcheck over the `run:` body. Expected: no findings.

```bash
git -C /Users/dan/Code/tf-version-bump add examples/github-actions/.github/workflows/tf-version-bump-reusable.yml examples/github-actions/test.sh
```

Commit message: `feat(actions-example): add a preview mode to the reusable workflow`.

---

### Task 5: Callers preview pull requests

**Files:**
- Modify: `examples/github-actions/.github/workflows/tf-version-bump-nonproduction.yml`
- Modify: `examples/github-actions/.github/workflows/tf-version-bump-production.yml`
- Test: `examples/github-actions/test.sh` (new `test_callers_preview_pull_requests`, `tests=(...)` list)

**Interfaces:**
- Consumes: reusable input `preview` (Task 4).
- Produces: caller jobs `automation` and `preview`, and YAML anchors `policy-inputs` and `policy-secrets`. `report-test.sh` and `test_workflow_offers_input_and_secret_terraform_environment` keep reading `.jobs.automation.with` and `.jobs.automation.secrets`, which stay in place.

- [ ] **Step 1: Write the failing test**

Add after `test_workflow_previews_without_publishing` in `test.sh`:

```bash
test_callers_preview_pull_requests() {
    # Production break caught: a preview runs with inputs that differ from its live run, queues
    # behind or blocks live runs, previews a fork or a non-default base, narrows its permissions so
    # GitHub refuses to start it, or a live run starts from a pull request.
    local policy workflow
    for policy in nonproduction production; do
        workflow="$SCRIPT_DIR/.github/workflows/tf-version-bump-$policy.yml"
        yq -o=json '.' "$workflow" | jq -e --arg policy "$policy" '
            .jobs as $jobs
            | (has("concurrency") | not)
              and .on.pull_request == {paths: [$jobs.automation.with.config_path,
                                              ".github/workflows/tf-version-bump-\($policy).yml"]}
              and ($jobs | keys) == ["automation", "preview"]
              and $jobs.automation.if == "${{ github.ref == format('"'"'refs/heads/{0}'"'"', github.event.repository.default_branch) }}"
              and $jobs.automation.concurrency == {group: "tf-version-bump-\($policy)-${{ github.repository_id }}",
                                                   "cancel-in-progress": false, queue: "max"}
              and $jobs.preview.if == "${{ github.event_name == '"'"'pull_request'"'"' && github.event.pull_request.head.repo.full_name == github.repository && github.event.pull_request.base.ref == github.event.repository.default_branch }}"
              and $jobs.preview.concurrency == {group: "tf-version-bump-\($policy)-preview-${{ github.repository_id }}-${{ github.event.number }}",
                                                "cancel-in-progress": true}
              and ($jobs.preview | has("permissions") | not)
              and $jobs.preview.uses == $jobs.automation.uses
              and $jobs.preview.with == $jobs.automation.with
              and $jobs.preview.secrets == $jobs.automation.secrets
              and $jobs.automation.with.preview == "${{ github.event_name == '"'"'pull_request'"'"' }}"
        ' >/dev/null || fail "the $policy caller does not preview pull requests with its live inputs"
        # The aliases keep one copy of each policy value, so a pull request's edits reach its preview.
        grep -qxF '    with: *policy-inputs' "$workflow" \
            && grep -qxF '    secrets: *policy-secrets' "$workflow" \
            || fail "the $policy caller's preview does not reuse the live job's inputs by alias"
    done
}
```

Add `test_callers_preview_pull_requests` to the `tests=(...)` list, after `test_workflow_previews_without_publishing`.

- [ ] **Step 2: Run it and watch it fail**

Run: `TEST_GIT=/Users/dan/.claude/bin/claude-git examples/github-actions/test.sh test_callers_preview_pull_requests`

Expected: `FAIL: the nonproduction caller does not preview pull requests with its live inputs`.

- [ ] **Step 3: Rewrite the non-production caller**

Replace `examples/github-actions/.github/workflows/tf-version-bump-nonproduction.yml` with the following. Everything under `with:` except the new `preview:` line is copied unchanged from the current file:

```yaml
name: Terraform version bump (non-production)

on:
  push:
    paths:
      - .github/tf-version-bump/nonproduction.yml
  pull_request:
    paths:
      - .github/tf-version-bump/nonproduction.yml
      - .github/workflows/tf-version-bump-nonproduction.yml
  workflow_dispatch:
    inputs:
      branch_prefix:
        description: Optional literal prefix that narrows the configured branch policy
        type: string
        default: ""
      dry_run:
        description: Run all local stages without publishing Git or GitHub changes
        type: boolean
        default: false
      terraform_init_upgrade:
        description: Upgrade dependencies within their version constraints during preparation
        type: boolean
        default: false
  schedule:
    - cron: "17 4 * * 1"
      timezone: Australia/Melbourne

permissions:
  contents: write
  pull-requests: write
  issues: write

jobs:
  automation:
    if: ${{ github.ref == format('refs/heads/{0}', github.event.repository.default_branch) }}
    concurrency:
      group: tf-version-bump-nonproduction-${{ github.repository_id }}
      cancel-in-progress: false
      queue: max
    uses: ./.github/workflows/tf-version-bump-reusable.yml
    secrets: &policy-secrets
      TF_API_TOKEN: ${{ secrets.TF_API_TOKEN }}
      TERRAFORM_ENV: ${{ secrets.TERRAFORM_ENV }}
    with: &policy-inputs
      automation_policy_id: nonproduction
      allowed_branch_prefixes: |
        state/nonproduction/
        state/staging/
        aws-state/nonproduction/
        aws-state/staging/
      branch_prefix: ${{ github.event_name == 'workflow_dispatch' && inputs.branch_prefix || '' }}
      config_path: .github/tf-version-bump/nonproduction.yml
      terraform_directories: .
      terraform_fmt: true
      terraform_init_upgrade: ${{ github.event_name == 'workflow_dispatch' && inputs.terraform_init_upgrade || false }}
      terraform_version: 1.15.5
      tf_version_bump_version: v1.0.0-rc.17
      tf_version_bump_archive_sha256: 137eac8239d08e2a93409e9eda1fd59b52abcf3d774fa7c3882ce53534f56a4d
      dry_run: ${{ github.event_name == 'workflow_dispatch' && inputs.dry_run || false }}
      preview: ${{ github.event_name == 'pull_request' }}
      max_parallel: 4
      commit_author_name: github-actions[bot]
      commit_author_email: 41898282+github-actions[bot]@users.noreply.github.com
  # Previews a same-repository pull request into the default branch on one sampled branch per
  # prefix, without publishing. The aliases give it the live job's inputs, including any the pull
  # request edits. A newer push cancels an older preview, while live runs queue: GitHub rejects
  # both settings in one concurrency block. It keeps the workflow's permissions because GitHub
  # refuses to start a read-only caller of a workflow whose publish job requests write.
  preview:
    if: ${{ github.event_name == 'pull_request' && github.event.pull_request.head.repo.full_name == github.repository && github.event.pull_request.base.ref == github.event.repository.default_branch }}
    concurrency:
      group: tf-version-bump-nonproduction-preview-${{ github.repository_id }}-${{ github.event.number }}
      cancel-in-progress: true
    uses: ./.github/workflows/tf-version-bump-reusable.yml
    secrets: *policy-secrets
    with: *policy-inputs
```

- [ ] **Step 4: Rewrite the production caller**

Replace `examples/github-actions/.github/workflows/tf-version-bump-production.yml` with:

```yaml
name: Terraform version bump (production)

on:
  push:
    paths:
      - .github/tf-version-bump/production.yml
  pull_request:
    paths:
      - .github/tf-version-bump/production.yml
      - .github/workflows/tf-version-bump-production.yml
  workflow_dispatch:
    inputs:
      branch_prefix:
        description: Optional literal prefix that narrows the configured branch policy
        type: string
        default: ""
      dry_run:
        description: Run all local stages without publishing Git or GitHub changes
        type: boolean
        default: false
      terraform_init_upgrade:
        description: Upgrade dependencies within their version constraints during preparation
        type: boolean
        default: false
  schedule:
    - cron: "43 4 * * 0"
      timezone: Australia/Melbourne

permissions:
  contents: write
  pull-requests: write
  issues: write

jobs:
  automation:
    if: ${{ github.ref == format('refs/heads/{0}', github.event.repository.default_branch) }}
    concurrency:
      group: tf-version-bump-production-${{ github.repository_id }}
      cancel-in-progress: false
      queue: max
    uses: ./.github/workflows/tf-version-bump-reusable.yml
    secrets: &policy-secrets
      TF_API_TOKEN: ${{ secrets.TF_API_TOKEN }}
      TERRAFORM_ENV: ${{ secrets.TERRAFORM_ENV }}
    with: &policy-inputs
      automation_policy_id: production
      allowed_branch_prefixes: |
        state/production/
        aws-state/production/
      branch_prefix: ${{ github.event_name == 'workflow_dispatch' && inputs.branch_prefix || '' }}
      config_path: .github/tf-version-bump/production.yml
      terraform_directories: .
      terraform_fmt: true
      terraform_init_upgrade: ${{ github.event_name == 'workflow_dispatch' && inputs.terraform_init_upgrade || false }}
      terraform_version: 1.15.5
      tf_version_bump_version: v1.0.0-rc.17
      tf_version_bump_archive_sha256: 137eac8239d08e2a93409e9eda1fd59b52abcf3d774fa7c3882ce53534f56a4d
      dry_run: ${{ github.event_name == 'workflow_dispatch' && inputs.dry_run || false }}
      preview: ${{ github.event_name == 'pull_request' }}
      max_parallel: 2
      commit_author_name: github-actions[bot]
      commit_author_email: 41898282+github-actions[bot]@users.noreply.github.com
  # Previews a same-repository pull request into the default branch on one sampled branch per
  # prefix, without publishing. The aliases give it the live job's inputs, including any the pull
  # request edits. A newer push cancels an older preview, while live runs queue: GitHub rejects
  # both settings in one concurrency block. It keeps the workflow's permissions because GitHub
  # refuses to start a read-only caller of a workflow whose publish job requests write.
  preview:
    if: ${{ github.event_name == 'pull_request' && github.event.pull_request.head.repo.full_name == github.repository && github.event.pull_request.base.ref == github.event.repository.default_branch }}
    concurrency:
      group: tf-version-bump-production-preview-${{ github.repository_id }}-${{ github.event.number }}
      cancel-in-progress: true
    uses: ./.github/workflows/tf-version-bump-reusable.yml
    secrets: *policy-secrets
    with: *policy-inputs
```

Before replacing each file, diff its current `with:` block against the block above (`git diff` after the edit). Only the `preview:` line may differ.

- [ ] **Step 5: Run the caller tests and everything that reads the callers**

Run: `TEST_GIT=/Users/dan/.claude/bin/claude-git examples/github-actions/test.sh test_callers_preview_pull_requests test_workflow_offers_input_and_secret_terraform_environment`

Expected: two `PASS:` lines.

Run: `TEST_GIT=/Users/dan/.claude/bin/claude-git examples/github-actions/report-test.sh`. Expected: all `PASS:`. Its report matrix still matches `.jobs.automation.with`.

Run: `go test -C /Users/dan/Code/tf-version-bump -run 'TestUpdateActionsReleasePin' -v ./...`. Expected: `ok` with every `TestUpdateActionsReleasePin*` test passing. These are in `release_workflow_test.go`, and together they show the release-pin updater still finds exactly one pin line per caller.

- [ ] **Step 6: Lint and commit**

Run: `make actionlint`. Expected: no findings. The launcher already ignores actionlint 1.7.12's stale `queue` diagnostic. The spec's probe confirmed the job-level form passes.

```bash
git -C /Users/dan/Code/tf-version-bump add examples/github-actions/.github/workflows/tf-version-bump-nonproduction.yml examples/github-actions/.github/workflows/tf-version-bump-production.yml examples/github-actions/test.sh
```

Commit message: `feat(actions-example): preview config changes on pull requests`.

---

### Task 6: Documentation

**Files:**
- Modify: `examples/github-actions/README.md`:
  - line 35 (Install)
  - line 50 (secret)
  - line 78 (Environments)
  - line 89 (Configure updates)
  - line 167 (Run and inspect)
  - a new section before `## Version report`
- Modify: `docs/ADVANCED-USAGE.md:9`

**Interfaces:**
- Consumes: the behaviour from Tasks 1–5.
- Produces: the anchor `#preview-on-pull-requests`.

- [ ] **Step 1: Edit the existing passages**

In `examples/github-actions/README.md`:
- **Line 35.** Replace `Both callers run only from the default branch.` with:

  ```markdown
  Both callers' live runs start only from the default branch; each caller also previews same-repository pull requests into the default branch (see [Preview on pull requests](#preview-on-pull-requests)).
  ```
- **Line 50.** Append this sentence: `Pull-request previews run the same processing job, so they receive the same token.`
- **Line 78.** Append this sentence:

  ```markdown
  An Environment's deployment-branch rules also apply to previews, which run from `refs/pull/<number>/merge`, so a rule that admits only the default branch may withhold the secret from a preview or hold it for approval.
  ```
- **Line 89.** Replace `Pull requests changing these files run a read-only config validation check; they do not process state branches or run Terraform.` with:

  ```markdown
  Pull requests changing these files run a read-only config validation check and, for same-repository pull requests into the default branch, a preview that processes one sampled state branch per prefix without publishing anything (see [Preview on pull requests](#preview-on-pull-requests)).
  ```
- **Line 167.** After the sentence ending `kept in full in the artefact.`, insert: `In a preview, the summary also shows each changed branch's candidate patch, truncated the same way.`

- [ ] **Step 2: Add the new section**

Insert before `## Version report`:

```markdown
## Preview on pull requests

A same-repository pull request into the default branch that changes a caller's configuration file or the caller workflow itself starts that caller's `preview` job. It discovers the policy's branches exactly as a live run does, then processes one branch per configured prefix, with its update, `terraform init`, optional formatting and `terraform validate`, and publishes nothing: the `publish` job is skipped, and publication also refuses any ref but the default branch. Pull requests from forks and pull requests into another branch skip the preview.

The preview job shares the live job's inputs through YAML aliases, so a pull request that edits a pin, a prefix or a root is previewed with its edits. It also runs the pull request's own scripts and reusable workflow, so a pull request that edits those is previewed with the edited versions. A newer push to the pull request cancels its older preview; previews never wait behind or block a live run.

The branch for each prefix is picked from the pull request number, so every push to the same pull request previews the same branches while that prefix's branches are unchanged. Adding, deleting or excluding a branch under a prefix may move the pick, and different pull requests may land on the same branch. A branch belongs to the first prefix it matches, and a prefix that owns no branch is named in a warning annotation. The preview is a smoke test, not coverage: a branch-scoped `ignore_modules` entry is previewed only if its branch happens to be sampled.

Each `process` job's summary shows the updater's log and, for a changed candidate, the candidate patch. The patch is the full candidate the merged configuration would produce, not only what the pull request changes: on a branch whose update pull request has not been merged, it also includes the changes the current configuration would already make.

A sampled branch whose update, initialisation, formatting or validation fails fails its `process` job, and with it the pull request's check. The failure may already exist on that branch, so check the branch's marked failure issue before blaming the pull request, and do not make the preview a required check. An invalid configuration fails every sampled branch at the update stage as `branch-update`, while the configuration validation check reports the cause.

GitHub runs pull-request workflows only for the `opened`, `synchronize` and `reopened` activities, and not at all while the pull request has a merge conflict. A preview is therefore not refreshed when the default branch or the state branches move; push to the pull request or re-run the workflow to refresh it.
```

- [ ] **Step 3: Edit `docs/ADVANCED-USAGE.md` line 9**

Replace `callers that run only from the default branch, plus config-change triggers, read-only pull-request config validation,` with `callers whose live runs start only from the default branch, plus config-change triggers, read-only pull-request config validation, pull-request previews on one sampled state branch per prefix,`.

Replace this sentence:

```markdown
The pull-request check validates only the control config with `tf-version-bump`; full state-branch dry runs remain deferred.
```

with:

```markdown
The pull-request config check validates only the control config with `tf-version-bump`; the preview processes a sample of state branches and publishes nothing.
```

- [ ] **Step 4: Run the documentation checks**

Run: `make -C /Users/dan/Code/tf-version-bump docs-check`

Expected: `ok` with every test passing. `TestDocumentationLocalLinksResolve` resolves `#preview-on-pull-requests`, and `TestDocumentationProseIsNotHardWrapped` passes.

Run: `TEST_GIT=/Users/dan/.claude/bin/claude-git examples/github-actions/test.sh test_readme_documents_the_reserved_environment_names`. Expected: `PASS:`.

- [ ] **Step 5: Commit**

```bash
git -C /Users/dan/Code/tf-version-bump add examples/github-actions/README.md docs/ADVANCED-USAGE.md
```

Commit message: `docs(actions-example): document pull-request previews`.

---

### Task 7: Full validation and test cleanup

**Files:** none new. This task verifies the branch and prunes tests.

- [ ] **Step 1: Run the full example harness (needs Docker)**

Run: `make -C /Users/dan/Code/tf-version-bump test-github-actions TEST_GIT=/Users/dan/.claude/bin/claude-git`

Expected: every test prints `PASS:`, then the reconcile and report harnesses print theirs, and nothing else.

- [ ] **Step 2: Run the repository's CI-equivalent validation**

Run each and expect success with no warnings:

```bash
go -C /Users/dan/Code/tf-version-bump mod verify
go test -C /Users/dan/Code/tf-version-bump -race -coverprofile=coverage.out -covermode=atomic ./...
golangci-lint run --timeout=5m
make -C /Users/dan/Code/tf-version-bump actionlint
make -C /Users/dan/Code/tf-version-bump shellcheck
make -C /Users/dan/Code/tf-version-bump docs-check
```

Run `golangci-lint` from the repository root. Remove `coverage.out` afterwards with `make -C /Users/dan/Code/tf-version-bump clean`.

- [ ] **Step 3: Run the test-cleanup pass as a separate subagent**

Dispatch a fresh subagent with the `test-cleanup` skill against `git diff main...HEAD -- examples/github-actions/test.sh examples/github-actions/reconcile-test.sh`. The implementer must not do this pass. Apply only removals it justifies, re-run Step 1, and commit with message `test(actions-example): trim preview tests`.

- [ ] **Step 4: Report**

Tell Dan:
- which checks passed;
- anything that failed, with its output;
- that the branch `wip/config-change-preview` is ready for review.

Do not push without his confirmation.
