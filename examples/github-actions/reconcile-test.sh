#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
RECONCILE_SCRIPT="$SCRIPT_DIR/.github/scripts/reconcile-state-branch.sh"
TEST_GIT=${TEST_GIT-git}
TEST_ROOT=$(mktemp -d)

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

cleanup() {
    chmod -R u+w "$TEST_ROOT" 2>/dev/null || true
    rm -rf -- "$TEST_ROOT"
}
trap cleanup EXIT

sha256_file() {
    local digest
    digest=$(sha256sum "$1")
    printf '%s\n' "${digest%% *}"
}

fixture_commit() {
    local checkout=$1 message=$2
    shift 2
    "$TEST_GIT" -C "$checkout" \
        -c user.name='Reconcile Test' \
        -c user.email='reconcile-test@example.invalid' \
        commit "$@" -m "$message" >/dev/null
}

# Runs $4.. (redirected to $2/$3), asserts it succeeded, and asserts it emitted nothing on
# either stream.
assert_silent_success() {
    local description=$1 stdout_file=$2 stderr_file=$3
    shift 3
    if ! "$@" >"$stdout_file" 2>"$stderr_file"; then
        fail "$description failed: $(<"$stderr_file")"
    fi
    [[ ! -s "$stdout_file" && ! -s "$stderr_file" ]] \
        || fail "$description emitted unexpected output: stdout=$(<"$stdout_file") stderr=$(<"$stderr_file")"
}

ref_hash() {
    local digest
    digest=$(printf '%s' "refs/heads/${FIXTURE_STATE_BRANCH-state/nonproduction/example}" | sha256sum)
    printf '%s\n' "${digest%% *}"
}

setup_success_fixture() {
    FIXTURE_STATE_BRANCH=${FIXTURE_STATE_BRANCH-state/nonproduction/example}
    FIXTURE_TERRAFORM_PATH=${FIXTURE_TERRAFORM_PATH-root/main.tf}
    FIXTURE_ROOT=$(mktemp -d "$TEST_ROOT/success.XXXXXX")
    FIXTURE_REMOTE="$FIXTURE_ROOT/origin.git"
    FIXTURE_SOURCE="$FIXTURE_ROOT/source"
    FIXTURE_CHECKOUT="$FIXTURE_ROOT/checkout"
    FIXTURE_BUNDLE="$FIXTURE_ROOT/preparation"
    FIXTURE_OUTCOME="$FIXTURE_ROOT/validation"
    FIXTURE_VERIFIED="$FIXTURE_ROOT/verified"

    "$TEST_GIT" init --bare --initial-branch=main "$FIXTURE_REMOTE" >/dev/null
    "$TEST_GIT" init --initial-branch=main "$FIXTURE_SOURCE" >/dev/null
    mkdir -p "$FIXTURE_SOURCE/root"
    printf '%s\n' 'terraform { required_version = ">= 1.0" }' \
        >"$FIXTURE_SOURCE/$FIXTURE_TERRAFORM_PATH"
    "$TEST_GIT" -C "$FIXTURE_SOURCE" add -- "$FIXTURE_TERRAFORM_PATH"
    fixture_commit "$FIXTURE_SOURCE" 'test: create reconciliation base'
    FIXTURE_BASE_OID=$("$TEST_GIT" -C "$FIXTURE_SOURCE" rev-parse HEAD)
    FIXTURE_CONTROL_OID=$FIXTURE_BASE_OID
    "$TEST_GIT" -C "$FIXTURE_SOURCE" remote add origin "$FIXTURE_REMOTE"
    "$TEST_GIT" -C "$FIXTURE_SOURCE" push --quiet origin \
        "HEAD:refs/heads/$FIXTURE_STATE_BRANCH"

    "$TEST_GIT" clone --quiet "$FIXTURE_SOURCE" "$FIXTURE_CHECKOUT"
    local candidate="$FIXTURE_ROOT/candidate"
    "$TEST_GIT" clone --quiet "$FIXTURE_SOURCE" "$candidate"
    sed -i.bak 's/>= 1\.0/>= 1.15.0/' "$candidate/$FIXTURE_TERRAFORM_PATH"
    rm "$candidate/$FIXTURE_TERRAFORM_PATH.bak"

    mkdir -p "$FIXTURE_BUNDLE/logs" "$FIXTURE_OUTCOME/logs"
    "$TEST_GIT" -C "$candidate" diff --binary --full-index --no-color \
        >"$FIXTURE_BUNDLE/update.patch"
    local patch_digest file_digest branch_hash
    patch_digest=$(sha256_file "$FIXTURE_BUNDLE/update.patch")
    file_digest=$(sha256_file "$candidate/$FIXTURE_TERRAFORM_PATH")
    branch_hash=$(ref_hash)
    jq -n \
        --arg control_oid "$FIXTURE_CONTROL_OID" \
        --arg base_oid "$FIXTURE_BASE_OID" \
        --arg ref_hash "$branch_hash" \
        --arg state_branch "$FIXTURE_STATE_BRANCH" \
        --arg changed_path "$FIXTURE_TERRAFORM_PATH" \
        --arg patch_digest "$patch_digest" \
        --arg file_digest "$file_digest" \
        '{schema_version: 2, run_id: "100", run_attempt: "1",
          automation_policy_id: "nonproduction", control_oid: $control_oid,
          state_branch: $state_branch, base_oid: $base_oid,
          ref_hash: $ref_hash,
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
          final_changed_files: [{path: $changed_path, mode: "100644", sha256: $file_digest}]}' \
        >"$FIXTURE_BUNDLE/manifest.json"
    local manifest_digest
    manifest_digest=$(sha256_file "$FIXTURE_BUNDLE/manifest.json")
    jq -n \
        --arg control_oid "$FIXTURE_CONTROL_OID" \
        --arg base_oid "$FIXTURE_BASE_OID" \
        --arg ref_hash "$branch_hash" \
        --arg state_branch "$FIXTURE_STATE_BRANCH" \
        --arg manifest_digest "$manifest_digest" \
        '{schema_version: 2, run_id: "100", run_attempt: "1",
          automation_policy_id: "nonproduction", control_oid: $control_oid,
          state_branch: $state_branch, base_oid: $base_oid,
          ref_hash: $ref_hash, classification: "success",
          candidate_manifest_sha256: $manifest_digest, command_status: 0}' \
        >"$FIXTURE_OUTCOME/manifest.json"
}

run_verify() {
    RECONCILE_RUN_ID=${RECONCILE_RUN_ID-100} \
        RECONCILE_RUN_ATTEMPT=${RECONCILE_RUN_ATTEMPT-1} \
        RECONCILE_AUTOMATION_POLICY_ID=${RECONCILE_AUTOMATION_POLICY_ID-nonproduction} \
        RECONCILE_CONTROL_OID=${RECONCILE_CONTROL_OID-$FIXTURE_CONTROL_OID} \
        RECONCILE_STATE_BRANCH=${RECONCILE_STATE_BRANCH-$FIXTURE_STATE_BRANCH} \
        RECONCILE_BASE_OID=${RECONCILE_BASE_OID-$FIXTURE_BASE_OID} \
        RECONCILE_REF_HASH=${RECONCILE_REF_HASH-$(ref_hash)} \
        RECONCILE_PREPARATION_BUNDLE_DIR="$FIXTURE_BUNDLE" \
        RECONCILE_VALIDATION_OUTCOME_DIR=${RECONCILE_VALIDATION_OUTCOME_DIR-$FIXTURE_OUTCOME} \
        RECONCILE_TARGET_CHECKOUT="$FIXTURE_CHECKOUT" \
        RECONCILE_VERIFIED_RESULT_DIR="$FIXTURE_VERIFIED" \
        RECONCILE_RUN_URL=https://github.example/yesdevnull/reconciliation-test/actions/runs/100 \
        "$RECONCILE_SCRIPT" verify
}

setup_gh_capture() {
    FIXTURE_GH_CAPTURE="$FIXTURE_ROOT/github-payloads"
    FIXTURE_BIN="$FIXTURE_ROOT/bin"
    mkdir -p "$FIXTURE_GH_CAPTURE" "$FIXTURE_BIN" "$FIXTURE_ROOT/runner-temp"
    cat >"$FIXTURE_BIN/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${GH_CAPTURE_DIR:?}/calls"
if [[ "$1 $2" == "pr list" ]]; then
    exit 0
fi
if [[ "$1 $2" == "issue list" ]]; then
    [[ ! -f "$GH_CAPTURE_DIR/existing-failure-issue" ]] \
        || cat "$GH_CAPTURE_DIR/existing-failure-issue"
    exit 0
fi
kind=$1
action=$2
shift 2
if [[ "$kind $action" == "issue close" ]]; then
    printf '%s\n' "$1" >"$GH_CAPTURE_DIR/closed-issue"
    exit 0
fi
if [[ "$kind $action" == "issue reopen" ]]; then
    printf '%s\n' "$1" >"$GH_CAPTURE_DIR/reopened-issue"
    exit 0
fi
while [[ $# -gt 0 ]]; do
    case "$1" in
        --title)
            printf '%s\n' "$2" >"$GH_CAPTURE_DIR/$kind-title"
            shift 2
            ;;
        --body-file)
            cp -- "$2" "$GH_CAPTURE_DIR/$kind-body"
            shift 2
            ;;
        *)
            shift
            ;;
    esac
done
EOF
    chmod 755 "$FIXTURE_BIN/gh"
}

run_publish() {
    PATH="$FIXTURE_BIN:$PATH" \
        GH_CAPTURE_DIR="$FIXTURE_GH_CAPTURE" \
        RECONCILE_RUN_ID=100 \
        RECONCILE_RUN_ATTEMPT=1 \
        RECONCILE_AUTOMATION_POLICY_ID=nonproduction \
        RECONCILE_CONTROL_OID="$FIXTURE_CONTROL_OID" \
        RECONCILE_STATE_BRANCH="$FIXTURE_STATE_BRANCH" \
        RECONCILE_BASE_OID="$FIXTURE_BASE_OID" \
        RECONCILE_REF_HASH="$(ref_hash)" \
        RECONCILE_VERIFIED_RESULT_DIR="$FIXTURE_VERIFIED" \
        RECONCILE_TARGET_CHECKOUT="$FIXTURE_CHECKOUT" \
        RECONCILE_GIT_REMOTE="$FIXTURE_REMOTE" \
        RECONCILE_REPOSITORY=yesdevnull/reconciliation-test \
        RECONCILE_DRY_RUN=${RECONCILE_DRY_RUN-true} \
        RECONCILE_COMMIT_AUTHOR_NAME='Reconcile Automation' \
        RECONCILE_COMMIT_AUTHOR_EMAIL='reconcile@example.invalid' \
        GH_TOKEN=test-only-token \
        RUNNER_TEMP="$FIXTURE_ROOT/runner-temp" \
        "$RECONCILE_SCRIPT" publish
}

prepare_publication_fixture() {
    setup_success_fixture
    run_verify
    chmod -R u+w "$FIXTURE_VERIFIED"
    rm -rf "$FIXTURE_CHECKOUT"
    "$TEST_GIT" clone --quiet "$FIXTURE_SOURCE" "$FIXTURE_CHECKOUT"
    setup_gh_capture
}

# Mutates the preparation bundle set up by setup_success_fixture into a bounded branch-update
# failure (dropping the outcome and patch, which a branch-update failure never has), then verifies
# it. $1 overrides the failure root (default "root") for tests that need a hostile value there.
mutate_bundle_into_branch_update_failure() {
    local root=${1-root}
    jq --arg root "$root" \
        '.classification = "branch-update" |
         .failure = {stage: "tf-version-bump", root: $root, command: "tf-version-bump", status: 1} |
         del(.updates, .formatting, .final_changed_files)' \
        "$FIXTURE_BUNDLE/manifest.json" >"$FIXTURE_ROOT/failure.json"
    mv "$FIXTURE_ROOT/failure.json" "$FIXTURE_BUNDLE/manifest.json"
    rm -rf "$FIXTURE_OUTCOME" "$FIXTURE_BUNDLE/update.patch"
    RECONCILE_VALIDATION_OUTCOME_DIR="" run_verify
}

test_verifies_successful_candidate_without_credentials() {
    # Production break caught: verification cannot bind and replay one valid preparation and
    # validation result without receiving publication credentials.
    setup_success_fixture
    assert_silent_success "valid candidate verification" \
        "$FIXTURE_ROOT/verify.stdout" "$FIXTURE_ROOT/verify.stderr" run_verify
    grep -F 'required_version = ">= 1.15.0"' "$FIXTURE_CHECKOUT/root/main.tf" >/dev/null \
        || fail "verification did not apply the candidate patch"
    jq -e \
        --arg manifest_digest "$(sha256_file "$FIXTURE_BUNDLE/manifest.json")" \
        --arg outcome_digest "$(sha256_file "$FIXTURE_OUTCOME/manifest.json")" \
        --arg patch_digest "$(sha256_file "$FIXTURE_BUNDLE/update.patch")" \
        '.schema_version == 2 and .classification == "success" and
         .run_id == "100" and .run_attempt == "1" and
         .preparation_manifest_sha256 == $manifest_digest and
         .validation_outcome_sha256 == $outcome_digest and
         .updates.module_blocks_updated == 1 and
         .updates.provider_blocks_updated == 1 and
         .updates.patch_sha256 == $patch_digest and
         .formatting == {ran: false, changed_files: []} and
         .final_changed_files == .updates.changed_files' \
        "$FIXTURE_VERIFIED/manifest.json" >/dev/null \
        || fail "verified success result did not bind the candidate artefacts"
    cmp "$FIXTURE_BUNDLE/update.patch" "$FIXTURE_VERIFIED/update.patch" \
        || fail "verified candidate patch differs from the prepared patch"
}

test_verifies_candidate_with_valid_tab_in_path() {
    # A tab is valid in a Git path and must remain byte-for-byte identical between the
    # candidate manifest and the replayed patch.
    FIXTURE_TERRAFORM_PATH=$'root/module\tname.tf'
    setup_success_fixture
    assert_silent_success "special-character path verification" \
        "$FIXTURE_ROOT/verify.stdout" "$FIXTURE_ROOT/verify.stderr" run_verify
    grep -F 'required_version = ">= 1.15.0"' \
        "$FIXTURE_CHECKOUT/$FIXTURE_TERRAFORM_PATH" >/dev/null \
        || fail "verification did not apply the special-character candidate patch"
    unset FIXTURE_TERRAFORM_PATH
}

assert_verification_failure() {
    local description=$1
    if run_verify >"$FIXTURE_ROOT/failure.stdout" 2>"$FIXTURE_ROOT/failure.stderr"; then
        fail "$description succeeded"
    fi
    [[ ! -e "$FIXTURE_VERIFIED" ]] \
        || fail "$description produced a verified result"
    [[ -s "$FIXTURE_ROOT/failure.stderr" ]] \
        || fail "$description did not emit a diagnostic"
}

test_rejects_mismatched_or_corrupt_candidates() {
    # Production breaks caught: a result crosses run attempts, trusts corrupt patch bytes, or
    # applies a patch whose repository path was not declared by preparation.
    local row
    for row in wrong-run-attempt corrupt-patch undeclared-patch-path; do
        unset RECONCILE_RUN_ATTEMPT
        setup_success_fixture
        case "$row" in
            wrong-run-attempt)
                RECONCILE_RUN_ATTEMPT=2
                ;;
            corrupt-patch)
                printf '%s\n' '# corrupt' >>"$FIXTURE_BUNDLE/update.patch"
                ;;
            undeclared-patch-path)
                local candidate="$FIXTURE_ROOT/extra-candidate"
                "$TEST_GIT" clone --quiet "$FIXTURE_SOURCE" "$candidate"
                sed -i.bak 's/>= 1\.0/>= 1.15.0/' "$candidate/root/main.tf"
                rm "$candidate/root/main.tf.bak"
                printf '%s\n' 'terraform {}' >"$candidate/undeclared.tf"
                "$TEST_GIT" -C "$candidate" add -N -- undeclared.tf
                "$TEST_GIT" -C "$candidate" diff --binary --full-index --no-color \
                    >"$FIXTURE_BUNDLE/update.patch"
                local digest
                digest=$(sha256_file "$FIXTURE_BUNDLE/update.patch")
                jq --arg digest "$digest" '.updates.patch_sha256 = $digest' \
                    "$FIXTURE_BUNDLE/manifest.json" >"$FIXTURE_ROOT/manifest.json"
                mv "$FIXTURE_ROOT/manifest.json" "$FIXTURE_BUNDLE/manifest.json"
                digest=$(sha256_file "$FIXTURE_BUNDLE/manifest.json")
                jq --arg digest "$digest" '.candidate_manifest_sha256 = $digest' \
                    "$FIXTURE_OUTCOME/manifest.json" >"$FIXTURE_ROOT/outcome.json"
                mv "$FIXTURE_ROOT/outcome.json" "$FIXTURE_OUTCOME/manifest.json"
                ;;
        esac
        assert_verification_failure "$row verification"
    done
    unset RECONCILE_RUN_ATTEMPT
}

test_verifies_deleting_candidate_reports_a_classified_digest_error() {
    # Production break caught: a candidate patch that deletes a declared file makes the batched
    # `sha256sum -- "${actual_paths[@]}"` read fail for that path (it no longer exists), which
    # without a result-count check left `${file_sha256s[$index]}` reading past the end of a short
    # array and crashing with bash's raw unbound-variable error, instead of the classified
    # "candidate file digest does not match the manifest" error the old per-file code produced.
    setup_success_fixture
    local extra_path="root/extra.tf"
    printf '%s\n' 'resource "null_resource" "extra" {}' >"$FIXTURE_SOURCE/$extra_path"
    "$TEST_GIT" -C "$FIXTURE_SOURCE" add -- "$extra_path"
    fixture_commit "$FIXTURE_SOURCE" 'test: add file the candidate will delete'
    FIXTURE_BASE_OID=$("$TEST_GIT" -C "$FIXTURE_SOURCE" rev-parse HEAD)
    "$TEST_GIT" -C "$FIXTURE_SOURCE" push --quiet origin "HEAD:refs/heads/$FIXTURE_STATE_BRANCH"
    rm -rf "$FIXTURE_CHECKOUT"
    "$TEST_GIT" clone --quiet "$FIXTURE_SOURCE" "$FIXTURE_CHECKOUT"

    local candidate="$FIXTURE_ROOT/deleting-candidate"
    "$TEST_GIT" clone --quiet "$FIXTURE_SOURCE" "$candidate"
    rm -f "$candidate/$extra_path"
    "$TEST_GIT" -C "$candidate" diff --binary --full-index --no-color \
        >"$FIXTURE_BUNDLE/update.patch"

    local patch_digest
    patch_digest=$(sha256_file "$FIXTURE_BUNDLE/update.patch")
    jq --arg base_oid "$FIXTURE_BASE_OID" \
        --arg patch_digest "$patch_digest" \
        --arg extra_path "$extra_path" \
        '.base_oid = $base_oid | .updates.patch_sha256 = $patch_digest |
         .updates.changed_files = [{path: $extra_path, mode: "100644", sha256: ("0" * 64)}] |
         .final_changed_files = .updates.changed_files' \
        "$FIXTURE_BUNDLE/manifest.json" >"$FIXTURE_ROOT/deleting-manifest.json"
    mv "$FIXTURE_ROOT/deleting-manifest.json" "$FIXTURE_BUNDLE/manifest.json"
    local manifest_digest
    manifest_digest=$(sha256_file "$FIXTURE_BUNDLE/manifest.json")
    jq --arg base_oid "$FIXTURE_BASE_OID" \
        --arg manifest_digest "$manifest_digest" \
        '.base_oid = $base_oid | .candidate_manifest_sha256 = $manifest_digest' \
        "$FIXTURE_OUTCOME/manifest.json" >"$FIXTURE_ROOT/deleting-outcome.json"
    mv "$FIXTURE_ROOT/deleting-outcome.json" "$FIXTURE_OUTCOME/manifest.json"

    if run_verify >"$FIXTURE_ROOT/delete.stdout" 2>"$FIXTURE_ROOT/delete.stderr"; then
        fail "deleting-candidate verification succeeded"
    fi
    [[ ! -e "$FIXTURE_VERIFIED" ]] \
        || fail "deleting-candidate verification produced a verified result"
    [[ "$(<"$FIXTURE_ROOT/delete.stderr")" \
        == "reconciliation error: candidate file digest does not match the manifest" ]] \
        || fail "deleting-candidate verification did not report the classified digest error: $(<"$FIXTURE_ROOT/delete.stderr")"
}

test_verifies_only_bounded_branch_failures_without_a_patch() {
    # Production break caught: recognised update/init/validation failures are either discarded,
    # require an inapplicable outcome, or retain a publishable candidate patch.
    local classification expected_stage expected_status
    for classification in branch-init branch-validation; do
        setup_success_fixture
        if [[ "$classification" == "branch-init" ]]; then
            expected_stage="terraform init"
            expected_status=7
            jq '.classification = "branch-init" |
                .failure = {stage: "terraform init", root: "root", command: "terraform init", status: 7} |
                del(.updates, .formatting, .final_changed_files)' \
                "$FIXTURE_BUNDLE/manifest.json" >"$FIXTURE_ROOT/failure.json"
            mv "$FIXTURE_ROOT/failure.json" "$FIXTURE_BUNDLE/manifest.json"
            rm -rf "$FIXTURE_OUTCOME" "$FIXTURE_BUNDLE/update.patch"
            RECONCILE_VALIDATION_OUTCOME_DIR=""
        else
            expected_stage="terraform validate"
            expected_status=9
            jq '.classification = "branch-validation" |
                .command_status = 9 |
                .failure = {stage: "terraform validate", root: "root", status: 9}' \
                "$FIXTURE_OUTCOME/manifest.json" >"$FIXTURE_ROOT/failure.json"
            mv "$FIXTURE_ROOT/failure.json" "$FIXTURE_OUTCOME/manifest.json"
        fi

        if ! run_verify >"$FIXTURE_ROOT/failure.stdout" 2>"$FIXTURE_ROOT/failure.stderr"; then
            fail "$classification verification failed: $(<"$FIXTURE_ROOT/failure.stderr")"
        fi
        jq -e --arg classification "$classification" \
            '.schema_version == 2 and .classification == $classification and
             if $classification == "branch-validation" then
                 (.updates.patch_sha256 | type == "string") and
                 .final_changed_files == .updates.changed_files
             else has("updates") == false end' \
            "$FIXTURE_VERIFIED/manifest.json" >/dev/null \
            || fail "$classification did not produce a bounded verified failure"
        jq -e \
            --arg stage "$expected_stage" \
            --argjson status "$expected_status" \
            '.failure == {stage: $stage, root: "root", status: $status} and
             .run_url == "https://github.example/yesdevnull/reconciliation-test/actions/runs/100"' \
            "$FIXTURE_VERIFIED/manifest.json" >/dev/null \
            || fail "$classification did not preserve bounded failure details and run URL"
        [[ ! -e "$FIXTURE_VERIFIED/update.patch" ]] \
            || fail "$classification retained a publishable patch"

        setup_gh_capture
        RECONCILE_DRY_RUN=false run_publish
        grep -F "Stage: <code>$expected_stage</code>" "$FIXTURE_GH_CAPTURE/issue-body" >/dev/null \
            || fail "$classification issue omitted the failure stage"
        grep -F 'Root: <code>root</code>' "$FIXTURE_GH_CAPTURE/issue-body" >/dev/null \
            || fail "$classification issue omitted the failure root"
        grep -F "Status: <code>$expected_status</code>" "$FIXTURE_GH_CAPTURE/issue-body" >/dev/null \
            || fail "$classification issue omitted the numeric failure status"
        grep -F '<a href="https://github.example/yesdevnull/reconciliation-test/actions/runs/100"><code>https://github.example/yesdevnull/reconciliation-test/actions/runs/100</code></a>' \
            "$FIXTURE_GH_CAPTURE/issue-body" >/dev/null \
            || fail "$classification issue omitted the direct workflow-run URL"
        unset RECONCILE_VALIDATION_OUTCOME_DIR
        unset RECONCILE_DRY_RUN
    done

    yq -o=json '.jobs.verify.steps' \
        "$SCRIPT_DIR/.github/workflows/tf-version-bump-reusable.yml" | jq -e '
        any(.[]; .name == "Verify candidate result" and
            .env.RECONCILE_RUN_URL ==
                "${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}")
    ' >/dev/null || fail "workflow does not pass the trusted direct run URL to verification"
}

test_publish_failure_requires_dry_run_to_be_set() {
    # Production break caught: a manual failure-publish replay without RECONCILE_DRY_RUN set
    # defaulted to the mutating direction instead of failing closed, unlike the success path.
    setup_success_fixture
    mutate_bundle_into_branch_update_failure
    setup_gh_capture

    # RECONCILE_REPOSITORY/GH_TOKEN/RUNNER_TEMP are set and $FIXTURE_BIN (the gh sentinel) is put
    # on PATH so that if the RECONCILE_DRY_RUN:? guard ever regressed, the script would actually
    # reach and execute the sentinel `gh` rather than dying on some other unrelated missing input
    # -- making the "no GitHub lifecycle commands invoked" assertion below load-bearing.
    if PATH="$FIXTURE_BIN:$PATH" GH_CAPTURE_DIR="$FIXTURE_GH_CAPTURE" \
        RECONCILE_RUN_ID=100 RECONCILE_RUN_ATTEMPT=1 RECONCILE_AUTOMATION_POLICY_ID=nonproduction \
        RECONCILE_CONTROL_OID="$FIXTURE_CONTROL_OID" RECONCILE_STATE_BRANCH="$FIXTURE_STATE_BRANCH" \
        RECONCILE_BASE_OID="$FIXTURE_BASE_OID" RECONCILE_REF_HASH="$(ref_hash)" \
        RECONCILE_VERIFIED_RESULT_DIR="$FIXTURE_VERIFIED" \
        RECONCILE_REPOSITORY=yesdevnull/reconciliation-test GH_TOKEN=test-only-token \
        RUNNER_TEMP="$FIXTURE_ROOT/runner-temp" \
        "$RECONCILE_SCRIPT" publish >"$FIXTURE_ROOT/unset-dry-run.stdout" \
        2>"$FIXTURE_ROOT/unset-dry-run.stderr"; then
        fail "failure publish without RECONCILE_DRY_RUN set succeeded"
    fi
    grep -F 'RECONCILE_DRY_RUN must be set' "$FIXTURE_ROOT/unset-dry-run.stderr" >/dev/null \
        || fail "failure publish without RECONCILE_DRY_RUN set did not fail closed: $(<"$FIXTURE_ROOT/unset-dry-run.stderr")"
    [[ -z "$(find "$FIXTURE_GH_CAPTURE" -mindepth 1 -print -quit)" ]] \
        || fail "failure publish without RECONCILE_DRY_RUN set invoked GitHub lifecycle commands"
}

test_dry_run_constructs_unsigned_owned_commit_without_external_mutation() {
    # Production break caught: an inherited signing policy signs or blocks an unsigned automation commit.
    prepare_publication_fixture
    "$TEST_GIT" -C "$FIXTURE_CHECKOUT" config --local commit.gpgsign true

    assert_silent_success "dry-run publication" \
        "$FIXTURE_ROOT/publish.stdout" "$FIXTURE_ROOT/publish.stderr" run_publish
    local commit_message
    commit_message=$("$TEST_GIT" -C "$FIXTURE_CHECKOUT" log -1 --format=%B)
    [[ "$commit_message" == *'Tf-Version-Bump-Automation: nonproduction/'"$(ref_hash)"* ]] \
        || fail "dry-run commit omitted the automation ownership trailer"
    [[ "$commit_message" == *"Tf-Version-Bump-Base: $FIXTURE_BASE_OID"* ]] \
        || fail "dry-run commit omitted the exact base trailer"
    [[ -z "$("$TEST_GIT" -C "$FIXTURE_CHECKOUT" cat-file -p HEAD | sed -n '/^gpgsig /,/^$/p')" ]] \
        || fail "dry-run commit was signed despite the local signing policy"
    if "$TEST_GIT" --git-dir "$FIXTURE_REMOTE" show-ref --verify --quiet \
        refs/heads/update_state/nonproduction/example; then
        fail "dry-run created the remote update ref"
    fi
    [[ -z "$(find "$FIXTURE_GH_CAPTURE" -mindepth 1 -print -quit)" ]] \
        || fail "dry-run invoked GitHub lifecycle commands"
}

test_protected_publication_updates_only_an_owned_or_absent_ref() {
    # Production break caught: publication bypasses exact state, ownership, or lease checks.
    prepare_publication_fixture
    mkdir "$FIXTURE_ROOT/helperless"
    install -m 755 "$RECONCILE_SCRIPT" "$FIXTURE_ROOT/helperless/reconcile-state-branch.sh"
    if ! RECONCILE_SCRIPT="$FIXTURE_ROOT/helperless/reconcile-state-branch.sh" \
        RECONCILE_DRY_RUN=false run_publish >"$FIXTURE_ROOT/publish.stdout" \
        2>"$FIXTURE_ROOT/publish.stderr"; then
        fail "publication without the processing helper failed: $(<"$FIXTURE_ROOT/publish.stderr")"
    fi
    local update_ref=refs/heads/update_state/nonproduction/example
    [[ "$("$TEST_GIT" --git-dir "$FIXTURE_REMOTE" rev-parse "$update_ref")" \
        == "$("$TEST_GIT" -C "$FIXTURE_CHECKOUT" rev-parse HEAD)" ]] \
        || fail "protected publication did not push the constructed commit"
    [[ "$("$TEST_GIT" --git-dir "$FIXTURE_REMOTE" rev-parse refs/heads/state/nonproduction/example)" \
        == "$FIXTURE_BASE_OID" ]] || fail "publication changed the state ref"

    rm -rf "$FIXTURE_GH_CAPTURE"
    setup_gh_capture
    local competing_checkout="$FIXTURE_ROOT/competing"
    "$TEST_GIT" clone --quiet "$FIXTURE_SOURCE" "$competing_checkout"
    fixture_commit "$competing_checkout" "test: advance observed automation ref" --allow-empty
    local competing_oid; competing_oid=$("$TEST_GIT" -C "$competing_checkout" rev-parse HEAD)
    "$TEST_GIT" -C "$competing_checkout" push --quiet "$FIXTURE_REMOTE" "HEAD:refs/heads/competing"
    rm -rf "$FIXTURE_CHECKOUT"
    "$TEST_GIT" clone --quiet "$FIXTURE_SOURCE" "$FIXTURE_CHECKOUT"
    # Fixture setup uses TEST_GIT so agent-created commits remain signed. The proxy exercises the
    # shipped workflow, which uses the runner's ordinary Git and intentionally creates unsigned
    # automation commits, so resolve that executable before placing the proxy at the front of PATH.
    local runner_git
    runner_git=$(command -v git)
    cat >"$FIXTURE_BIN/git" <<EOF
#!/usr/bin/env bash
set -euo pipefail
command_status=0
"$runner_git" "\$@" || command_status=\$?
if [[ "\$command_status" -eq 0 && "\$*" == *"show -s --format=%B "* ]]; then
    "$TEST_GIT" --git-dir "$FIXTURE_REMOTE" update-ref "$update_ref" "$competing_oid"
fi
exit "\$command_status"
EOF
    chmod 755 "$FIXTURE_BIN/git"
    if RECONCILE_DRY_RUN=false run_publish >"$FIXTURE_ROOT/stale.stdout" 2>"$FIXTURE_ROOT/stale.stderr"; then
        fail "publication overwrote a ref advanced after its exact-lease observation"
    fi
    grep -F "reconciliation error: update ref push failed its exact lease" \
        "$FIXTURE_ROOT/stale.stderr" >/dev/null \
        || fail "publication failed before reaching the exact-lease push: $(<"$FIXTURE_ROOT/stale.stderr")"
    [[ "$("$TEST_GIT" --git-dir "$FIXTURE_REMOTE" rev-parse "$update_ref")" == "$competing_oid" ]] \
        || fail "stale exact-lease publication changed the competing update ref"
    [[ -z "$(find "$FIXTURE_GH_CAPTURE" -mindepth 1 -print -quit)" ]] \
        || fail "stale exact-lease publication invoked GitHub lifecycle commands"

    prepare_publication_fixture
    "$TEST_GIT" --git-dir "$FIXTURE_REMOTE" update-ref "$update_ref" "$FIXTURE_BASE_OID"
    RECONCILE_DRY_RUN=false
    if run_publish >"$FIXTURE_ROOT/collision.stdout" 2>"$FIXTURE_ROOT/collision.stderr"; then
        fail "publication overwrote an unowned automation ref"
    fi
    [[ "$("$TEST_GIT" --git-dir "$FIXTURE_REMOTE" rev-parse "$update_ref")" \
        == "$FIXTURE_BASE_OID" ]] || fail "collision changed the unowned automation ref"
    unset RECONCILE_DRY_RUN
}

test_reconciles_marked_pull_request_or_failure_issue_payload() {
    # Production break caught: success and bounded failure results create duplicate/unmarked
    # lifecycle records, use the wrong base/head, or let failure publication mutate Git.
    prepare_publication_fixture
    printf '[{"number":42,"body":"%s","closed":false}]\n' \
        '<!-- tf-version-bump:nonproduction:'"$(ref_hash)"' -->' \
        >"$FIXTURE_GH_CAPTURE/existing-failure-issue"
    RECONCILE_DRY_RUN=false
    run_publish
    [[ "$(<"$FIXTURE_GH_CAPTURE/pr-title")" \
        == 'Terraform dependency update' ]] \
        || fail "success publication used the wrong PR title"
    local marker
    marker='<!-- tf-version-bump:nonproduction:'"$(ref_hash)"' -->'
    local expected_pr_body
    expected_pr_body=$(printf '%s\n\n%s\n\n%s\n%s\n' \
        "$marker" \
        'Automated Terraform dependency update for <code>state/nonproduction/example</code>.' \
        "Base: <code>$FIXTURE_BASE_OID</code>" \
        'Policy: <code>nonproduction</code>')
    [[ "$(<"$FIXTURE_GH_CAPTURE/pr-body")" == "$expected_pr_body" ]] \
        || fail "success publication used the wrong marked PR body"
    [[ "$(<"$FIXTURE_GH_CAPTURE/closed-issue")" == 42 ]] \
        || fail "success publication did not close the marked failure issue"

    setup_success_fixture
    mutate_bundle_into_branch_update_failure
    setup_gh_capture
    printf '[{"number":42,"body":"%s","closed":true}]\n' \
        '<!-- tf-version-bump:nonproduction:'"$(ref_hash)"' -->' \
        >"$FIXTURE_GH_CAPTURE/existing-failure-issue"
    RECONCILE_DRY_RUN=false run_publish
    [[ "$(<"$FIXTURE_GH_CAPTURE/issue-title")" \
        == 'Terraform dependency update failed' ]] \
        || fail "failure publication used the wrong issue title"
    marker='<!-- tf-version-bump:nonproduction:'"$(ref_hash)"' -->'
    local expected_issue_body
    expected_issue_body=$(printf '%s\n\n%s\n\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n' \
        "$marker" \
        'Automated Terraform update failed for <code>state/nonproduction/example</code>.' \
        'Classification: <code>branch-update</code>' \
        'Stage: <code>tf-version-bump</code>' \
        'Root: <code>root</code>' \
        'Status: <code>1</code>' \
        "Base: <code>$FIXTURE_BASE_OID</code>" \
        'Run: <code>100</code> attempt <code>1</code>' \
        'Workflow run: <a href="https://github.example/yesdevnull/reconciliation-test/actions/runs/100"><code>https://github.example/yesdevnull/reconciliation-test/actions/runs/100</code></a>')
    [[ "$(<"$FIXTURE_GH_CAPTURE/issue-body")" == "$expected_issue_body" ]] \
        || fail "failure publication used the wrong marked issue body"
    [[ "$(<"$FIXTURE_GH_CAPTURE/reopened-issue")" == 42 ]] \
        || fail "later failure did not reopen the closed marked issue"
    grep -F "issue edit 42" "$FIXTURE_GH_CAPTURE/calls" >/dev/null \
        || fail "later failure did not edit the reopened marked issue"
    if grep -F "issue create" "$FIXTURE_GH_CAPTURE/calls" >/dev/null; then
        fail "later failure duplicated the closed marked issue"
    fi
    if "$TEST_GIT" --git-dir "$FIXTURE_REMOTE" show-ref --verify --quiet \
        refs/heads/update_state/nonproduction/example; then
        fail "failure issue publication mutated the remote update ref"
    fi
    unset RECONCILE_DRY_RUN RECONCILE_VALIDATION_OUTCOME_DIR
}

test_escapes_hostile_branch_values_and_uses_constant_titles() {
    # Production break caught: a Git-valid branch injects a mention or HTML/Markdown into a
    # generated body, or makes PR and issue titles attacker-controlled.
    FIXTURE_STATE_BRANCH='state/nonproduction/@ops</code>__alert__'
    prepare_publication_fixture
    RECONCILE_DRY_RUN=false run_publish
    [[ "$(<"$FIXTURE_GH_CAPTURE/pr-title")" == 'Terraform dependency update' ]] \
        || fail "hostile branch influenced the PR title"
    grep -F '<code>state/nonproduction/&#64;ops&lt;/code&gt;__alert__</code>' \
        "$FIXTURE_GH_CAPTURE/pr-body" >/dev/null \
        || fail "PR body did not HTML-escape and neutralise the hostile branch"
    [[ "$(<"$FIXTURE_GH_CAPTURE/pr-body")" != *"$FIXTURE_STATE_BRANCH"* ]] \
        || fail "PR body retained the raw hostile branch"

    setup_success_fixture
    mutate_bundle_into_branch_update_failure "root</code>@ops"
    setup_gh_capture
    RECONCILE_DRY_RUN=false run_publish
    [[ "$(<"$FIXTURE_GH_CAPTURE/issue-title")" == 'Terraform dependency update failed' ]] \
        || fail "hostile branch influenced the issue title"
    grep -F '<code>state/nonproduction/&#64;ops&lt;/code&gt;__alert__</code>' \
        "$FIXTURE_GH_CAPTURE/issue-body" >/dev/null \
        || fail "issue body did not HTML-escape and neutralise the hostile branch"
    grep -F 'Root: <code>root&lt;/code&gt;&#64;ops</code>' \
        "$FIXTURE_GH_CAPTURE/issue-body" >/dev/null \
        || fail "issue body did not HTML-escape and neutralise the hostile root"
    [[ "$(<"$FIXTURE_GH_CAPTURE/issue-body")" != *"$FIXTURE_STATE_BRANCH"* ]] \
        || fail "issue body retained the raw hostile branch"
    unset FIXTURE_STATE_BRANCH RECONCILE_DRY_RUN RECONCILE_VALIDATION_OUTCOME_DIR
}

test_issue_lookup_searches_the_ref_hash_in_bodies() {
    # Production break caught: issue reconciliation scans an unrelated bounded page instead of
    # narrowing candidates by the stable complete-ref hash before the exact marker check.
    setup_success_fixture
    mutate_bundle_into_branch_update_failure
    setup_gh_capture
    RECONCILE_DRY_RUN=false run_publish

    grep -F "issue list --repo yesdevnull/reconciliation-test --state all --search $(ref_hash) in:body --limit 100 --json number,body,closed" \
        "$FIXTURE_GH_CAPTURE/calls" >/dev/null \
        || fail "issue lookup did not narrow candidates with the ref-hash body search"
    unset RECONCILE_DRY_RUN RECONCILE_VALIDATION_OUTCOME_DIR
}

if [[ $# -eq 0 ]]; then
    test_verifies_successful_candidate_without_credentials
    test_verifies_candidate_with_valid_tab_in_path
    test_rejects_mismatched_or_corrupt_candidates
    test_verifies_deleting_candidate_reports_a_classified_digest_error
    test_verifies_only_bounded_branch_failures_without_a_patch
    test_publish_failure_requires_dry_run_to_be_set
    test_dry_run_constructs_unsigned_owned_commit_without_external_mutation
    test_protected_publication_updates_only_an_owned_or_absent_ref
    test_reconciles_marked_pull_request_or_failure_issue_payload
    test_escapes_hostile_branch_values_and_uses_constant_titles
    test_issue_lookup_searches_the_ref_hash_in_bodies
else
    "$@"
fi
