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

# shellcheck disable=SC2329 # Used inside command substitutions in selected tests.
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


setup_gh_capture() {
    FIXTURE_GH_CAPTURE="$FIXTURE_ROOT/github-payloads"
    FIXTURE_BIN="$FIXTURE_ROOT/bin"
    mkdir -p "$FIXTURE_GH_CAPTURE" "$FIXTURE_BIN" "$FIXTURE_ROOT/runner-temp"
    cat >"$FIXTURE_BIN/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ $# -lt 2 ]]; then
    echo 'gh capture requires a command and subcommand' >&2
    exit 2
fi
printf '%s\n' "$*" >>"${GH_CAPTURE_DIR:?}/calls"
if [[ "$1 $2" == "pr list" ]]; then
    if [[ -f "$GH_CAPTURE_DIR/fail-pr-list" ]]; then
        echo 'PR lookup failed' >&2
        exit 1
    fi
    [[ ! -f "$GH_CAPTURE_DIR/existing-pr" ]] || cat "$GH_CAPTURE_DIR/existing-pr"
    exit 0
fi
if [[ "$1 $2" == "issue list" ]]; then
    if [[ -f "$GH_CAPTURE_DIR/fail-issue-list" ]]; then
        echo 'Issue lookup failed' >&2
        exit 1
    fi
    [[ ! -f "$GH_CAPTURE_DIR/existing-failure-issue" ]] \
        || cat "$GH_CAPTURE_DIR/existing-failure-issue"
    exit 0
fi
kind=$1
action=$2
shift 2
if [[ "$kind $action" == "pr close" ]]; then
    if [[ -f "$GH_CAPTURE_DIR/fail-pr-close" ]]; then
        echo 'PR closure failed' >&2
        exit 1
    fi
    printf '%s\n' "$1" >"$GH_CAPTURE_DIR/closed-pr"
    exit 0
fi
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
        RECONCILE_RUN_URL=https://github.com/yesdevnull/reconciliation-test/actions/runs/100 \
        RECONCILE_RUN_ID=${RECONCILE_RUN_ID-100} \
        RECONCILE_RUN_ATTEMPT=${RECONCILE_RUN_ATTEMPT-1} \
        RECONCILE_AUTOMATION_POLICY_ID=nonproduction \
        RECONCILE_CONTROL_OID="$FIXTURE_CONTROL_OID" \
        RECONCILE_STATE_BRANCH="$FIXTURE_STATE_BRANCH" \
        RECONCILE_BASE_OID="$FIXTURE_BASE_OID" \
        RECONCILE_REF_HASH="$(ref_hash)" \
        RECONCILE_RESULT_DIR="$FIXTURE_RESULT" \
        RECONCILE_TARGET_CHECKOUT="$FIXTURE_CHECKOUT" \
        RECONCILE_TERRAFORM_ROOTS=${RECONCILE_TERRAFORM_ROOTS-root} \
        RECONCILE_GIT_REMOTE="$FIXTURE_REMOTE" \
        RECONCILE_REPOSITORY=yesdevnull/reconciliation-test \
        RECONCILE_DRY_RUN=${RECONCILE_DRY_RUN-true} \
        RECONCILE_COMMIT_AUTHOR_NAME='Reconcile Automation' \
        RECONCILE_COMMIT_AUTHOR_EMAIL='reconcile@example.invalid' \
        GH_TOKEN=test-only-token \
        RUNNER_TEMP="$FIXTURE_ROOT/runner-temp" \
        "$RECONCILE_SCRIPT" publish
}


setup_success_fixture() {
    FIXTURE_STATE_BRANCH=state/nonproduction/example
    FIXTURE_ROOT=$(mktemp -d "$TEST_ROOT/result.XXXXXX")
    FIXTURE_REMOTE="$FIXTURE_ROOT/origin.git"
    FIXTURE_SOURCE="$FIXTURE_ROOT/source"
    FIXTURE_CHECKOUT="$FIXTURE_ROOT/checkout"
    FIXTURE_RESULT="$FIXTURE_ROOT/result"
    "$TEST_GIT" init --bare --initial-branch=main "$FIXTURE_REMOTE" >/dev/null
    "$TEST_GIT" init --initial-branch=main "$FIXTURE_SOURCE" >/dev/null
    mkdir -p "$FIXTURE_SOURCE/root/nested" "$FIXTURE_RESULT/logs"
    printf '%s\n' 'terraform { required_version = ">= 1.0" }' >"$FIXTURE_SOURCE/root/main.tf"
    "$TEST_GIT" -C "$FIXTURE_SOURCE" add -- root/main.tf
    fixture_commit "$FIXTURE_SOURCE" 'test: create reconciliation base'
    FIXTURE_BASE_OID=$("$TEST_GIT" -C "$FIXTURE_SOURCE" rev-parse HEAD)
    FIXTURE_CONTROL_OID=$FIXTURE_BASE_OID
    "$TEST_GIT" -C "$FIXTURE_SOURCE" push --quiet "$FIXTURE_REMOTE" "HEAD:refs/heads/$FIXTURE_STATE_BRANCH"
    "$TEST_GIT" clone --quiet "$FIXTURE_SOURCE" "$FIXTURE_CHECKOUT"
    printf '%s\n' 'terraform { required_version = ">= 1.15.0" }' >"$FIXTURE_SOURCE/root/main.tf"
    "$TEST_GIT" -C "$FIXTURE_SOURCE" diff --binary --full-index >"$FIXTURE_RESULT/candidate.patch"
    jq -n --arg oid "$FIXTURE_BASE_OID" --arg hash "$(ref_hash)" \
        --arg digest "$(sha256_file "$FIXTURE_RESULT/candidate.patch")" \
        '{schema_version: 3, run_id: "100", run_attempt: "1", automation_policy_id: "nonproduction",
          control_oid: $oid, state_branch: "state/nonproduction/example", base_oid: $oid,
          ref_hash: $hash, classification: "success", roots: ["root"], patch_sha256: $digest}' \
        >"$FIXTURE_RESULT/result.json"
    setup_gh_capture
    if [[ "$TEST_GIT" != git ]]; then ln -s "$TEST_GIT" "$FIXTURE_BIN/git"; fi
}

change_result() {
    jq "$1" "$FIXTURE_RESULT/result.json" >"$FIXTURE_ROOT/changed.json"
    mv "$FIXTURE_ROOT/changed.json" "$FIXTURE_RESULT/result.json"
}

configure_result() {
    local classification=$1 stage=${2-terraform validate}
    rm "$FIXTURE_RESULT/candidate.patch"
    change_result "del(.patch_sha256) | .classification = \"$classification\""
    if [[ "$classification" == branch-* ]]; then
        change_result ".failure = {stage: \"$stage\", root: \"root\", status: 1}"
    fi
}

existing_records() {
    local marker
    marker="<!-- tf-version-bump:nonproduction:$(ref_hash) -->"
    jq -n --arg marker "$marker" '[{number: 17, body: $marker}]' >"$FIXTURE_GH_CAPTURE/existing-pr"
    jq -n --arg marker "$marker" '[{number: 23, body: $marker, closed: false}]' >"$FIXTURE_GH_CAPTURE/existing-failure-issue"
}

assert_publish_failure() {
    local expected=$1
    if RECONCILE_DRY_RUN=false run_publish >"$FIXTURE_ROOT/stdout" 2>"$FIXTURE_ROOT/stderr"; then
        fail "publication accepted $expected"
    fi
    grep -F "$expected" "$FIXTURE_ROOT/stderr" >/dev/null || fail "unexpected publication error: $(<"$FIXTURE_ROOT/stderr")"
    [[ ! -s "$FIXTURE_ROOT/stdout" ]] || fail 'failed publication emitted stdout'
}

test_reconciles_supplied_processing_failure() {
    # Consumes the real processor's failure artefact; only GitHub is captured.
    : "${PROCESSING_RESULT_FIXTURE:?PROCESSING_RESULT_FIXTURE must be set}"
    : "${PROCESSING_TARGET_FIXTURE:?PROCESSING_TARGET_FIXTURE must be set}"
    FIXTURE_ROOT=$(mktemp -d "$TEST_ROOT/processing-failure.XXXXXX")
    FIXTURE_RESULT="$FIXTURE_ROOT/result"
    cp -R "$PROCESSING_RESULT_FIXTURE" "$FIXTURE_RESULT"
    FIXTURE_STATE_BRANCH=$(jq -r '.state_branch' "$FIXTURE_RESULT/result.json")
    FIXTURE_BASE_OID=$(jq -r '.base_oid' "$FIXTURE_RESULT/result.json")
    FIXTURE_CONTROL_OID=$(jq -r '.control_oid' "$FIXTURE_RESULT/result.json")
    FIXTURE_REMOTE="$FIXTURE_ROOT/origin.git"
    FIXTURE_CHECKOUT="$FIXTURE_ROOT/checkout"
    "$TEST_GIT" clone --quiet "$PROCESSING_TARGET_FIXTURE" "$FIXTURE_CHECKOUT"
    "$TEST_GIT" init --bare --initial-branch=main "$FIXTURE_REMOTE" >/dev/null
    "$TEST_GIT" -C "$FIXTURE_CHECKOUT" push --quiet "$FIXTURE_REMOTE" "HEAD:refs/heads/$FIXTURE_STATE_BRANCH"
    setup_gh_capture
    if [[ "$TEST_GIT" != git ]]; then ln -s "$TEST_GIT" "$FIXTURE_BIN/git"; fi
    existing_records
    rm "$FIXTURE_GH_CAPTURE/existing-failure-issue"
    RECONCILE_RUN_ID=$(jq -r '.run_id' "$FIXTURE_RESULT/result.json") \
        RECONCILE_RUN_ATTEMPT=$(jq -r '.run_attempt' "$FIXTURE_RESULT/result.json") \
        RECONCILE_DRY_RUN=false assert_silent_success 'actual processing failure publication' \
        "$FIXTURE_ROOT/stdout" "$FIXTURE_ROOT/stderr" run_publish
    [[ "$(<"$FIXTURE_GH_CAPTURE/closed-pr")" == 17 ]] || fail 'invalid configuration left obsolete PR open'
    [[ "$(sed -n '2p' "$FIXTURE_GH_CAPTURE/calls")" == 'pr close 17 '* ]] || fail 'failure issue handled before PR closure'
    grep -F 'issue create ' "$FIXTURE_GH_CAPTURE/calls" >/dev/null || fail 'invalid configuration did not create failure issue'
}

test_publishes_one_owned_commit_from_exact_base() {
    setup_success_fixture
    RECONCILE_DRY_RUN=false assert_silent_success 'publication' "$FIXTURE_ROOT/stdout" "$FIXTURE_ROOT/stderr" run_publish
    local oid message
    oid=$("$TEST_GIT" --git-dir "$FIXTURE_REMOTE" rev-parse "refs/heads/update_$FIXTURE_STATE_BRANCH")
    [[ "$("$TEST_GIT" -C "$FIXTURE_CHECKOUT" rev-parse HEAD^)" == "$FIXTURE_BASE_OID" ]] || fail 'publication created multiple commits'
    [[ "$("$TEST_GIT" --git-dir "$FIXTURE_REMOTE" rev-parse "refs/heads/$FIXTURE_STATE_BRANCH")" == "$FIXTURE_BASE_OID" ]] || fail 'publication changed state branch'
    message=$("$TEST_GIT" -C "$FIXTURE_CHECKOUT" show -s --format=%B "$oid")
    [[ "$message" == *"Tf-Version-Bump-Automation: nonproduction/$(ref_hash)"* && "$message" == *"Tf-Version-Bump-Base: $FIXTURE_BASE_OID"* ]] || fail 'ownership trailers missing'
    if [[ "$TEST_GIT" != git ]]; then
        "$TEST_GIT" -C "$FIXTURE_CHECKOUT" cat-file commit "$oid" >"$FIXTURE_ROOT/commit"
        grep -F 'gpgsig -----BEGIN SSH SIGNATURE-----' "$FIXTURE_ROOT/commit" >/dev/null || fail 'publication commit was not signed'
    fi
    grep -F 'pr create ' "$FIXTURE_GH_CAPTURE/calls" >/dev/null || fail 'success did not create PR'
    grep -F '/actions/runs/100' "$FIXTURE_GH_CAPTURE/pr-body" >/dev/null || fail 'PR omitted run link'
    cmp "$FIXTURE_SOURCE/root/main.tf" "$FIXTURE_CHECKOUT/root/main.tf" || fail 'published content differs'
    "$TEST_GIT" -C "$FIXTURE_CHECKOUT" reset --hard "$FIXTURE_BASE_OID" >/dev/null
    existing_records
    RECONCILE_DRY_RUN=false assert_silent_success 'owned update' "$FIXTURE_ROOT/stdout" "$FIXTURE_ROOT/stderr" run_publish
    grep -F 'pr edit 17 ' "$FIXTURE_GH_CAPTURE/calls" >/dev/null || fail 'existing PR not edited'
    [[ "$(<"$FIXTURE_GH_CAPTURE/closed-issue")" == 23 ]] || fail 'success did not close issue'
}

test_result_validation_prevents_mutation() {
    local mutation
    for mutation in '.run_attempt="2"' '.base_oid="bad"' '.patch_sha256=("0"*64)' '.roots=["other"]' '.classification="unknown"'; do
        setup_success_fixture
        change_result "$mutation"
        assert_publish_failure 'reconciliation error:'
        [[ ! -f "$FIXTURE_GH_CAPTURE/calls" ]] || fail 'invalid result reached GitHub'
        [[ "$("$TEST_GIT" -C "$FIXTURE_CHECKOUT" rev-parse HEAD)" == "$FIXTURE_BASE_OID" ]] || fail 'invalid result created commit'
    done
}

test_rejects_unsafe_candidate_paths_and_modes() {
    local mode
    for mode in outside nested-lock deletion symlink executable; do
        setup_success_fixture
        "$TEST_GIT" -C "$FIXTURE_SOURCE" reset --hard HEAD >/dev/null
        case "$mode" in
            outside) printf 'unexpected\n' >"$FIXTURE_SOURCE/README.md" ;;
            nested-lock) mkdir -p "$FIXTURE_SOURCE/root/nested"; printf 'lock\n' >"$FIXTURE_SOURCE/root/nested/.terraform.lock.hcl" ;;
            deletion) rm "$FIXTURE_SOURCE/root/main.tf" ;;
            symlink) rm "$FIXTURE_SOURCE/root/main.tf"; ln -s /tmp/foreign "$FIXTURE_SOURCE/root/main.tf" ;;
            executable) chmod +x "$FIXTURE_SOURCE/root/main.tf" ;;
        esac
        "$TEST_GIT" -C "$FIXTURE_SOURCE" add --all -- .
        "$TEST_GIT" -C "$FIXTURE_SOURCE" diff --cached --binary --full-index >"$FIXTURE_RESULT/candidate.patch"
        change_result ".patch_sha256=\"$(sha256_file "$FIXTURE_RESULT/candidate.patch")\""
        assert_publish_failure 'reconciliation error:'
        [[ -z "$("$TEST_GIT" -C "$FIXTURE_CHECKOUT" status --porcelain)" ]] || fail 'unsafe patch materialised before policy check'
        [[ ! -f "$FIXTURE_GH_CAPTURE/calls" ]] || fail 'unsafe patch reached GitHub'
    done
}

test_publication_refuses_moved_base_and_foreign_update() {
    local mode
    for mode in moved foreign; do
        setup_success_fixture
        fixture_commit "$FIXTURE_SOURCE" 'test: unrelated commit' --allow-empty
        if [[ "$mode" == moved ]]; then
            "$TEST_GIT" -C "$FIXTURE_SOURCE" push --quiet "$FIXTURE_REMOTE" "HEAD:refs/heads/$FIXTURE_STATE_BRANCH"
            assert_publish_failure 'state ref moved after discovery'
        else
            "$TEST_GIT" -C "$FIXTURE_SOURCE" push --quiet "$FIXTURE_REMOTE" "HEAD:refs/heads/update_$FIXTURE_STATE_BRANCH"
            assert_publish_failure 'existing update ref is not owned'
        fi
        [[ ! -f "$FIXTURE_GH_CAPTURE/calls" ]] || fail 'refusal reached GitHub'
    done
}

test_stale_results_do_not_reconcile_lifecycle() {
    local classification mode expected
    for classification in no-change branch-update branch-init branch-format branch-validation; do
        for mode in moved missing unavailable; do
            setup_success_fixture
            existing_records
            case "$classification" in
                branch-update) configure_result "$classification" tf-version-bump ;;
                branch-init) configure_result "$classification" 'terraform init' ;;
                branch-format) configure_result "$classification" 'terraform fmt' ;;
                *) configure_result "$classification" ;;
            esac
            expected='state ref moved after discovery'
            case "$mode" in
                moved)
                    fixture_commit "$FIXTURE_SOURCE" 'test: advance state branch' --allow-empty
                    "$TEST_GIT" -C "$FIXTURE_SOURCE" push --quiet "$FIXTURE_REMOTE" "HEAD:refs/heads/$FIXTURE_STATE_BRANCH"
                    ;;
                missing) "$TEST_GIT" --git-dir "$FIXTURE_REMOTE" update-ref -d "refs/heads/$FIXTURE_STATE_BRANCH" ;;
                unavailable) FIXTURE_REMOTE="$FIXTURE_ROOT/unavailable.git"; expected='could not inspect remote state ref' ;;
            esac
            assert_publish_failure "$expected"
            [[ ! -f "$FIXTURE_GH_CAPTURE/calls" ]] || fail "$classification/$mode reached GitHub lifecycle"
        done
    done
}

test_reconciles_no_change_and_failure_in_order() {
    local classification
    for classification in no-change branch-update branch-init branch-format branch-validation; do
        setup_success_fixture
        existing_records
        case "$classification" in
            branch-update) configure_result "$classification" tf-version-bump ;;
            branch-init) configure_result "$classification" 'terraform init' ;;
            branch-format) configure_result "$classification" 'terraform fmt' ;;
            *) configure_result "$classification" ;;
        esac
        RECONCILE_DRY_RUN=false assert_silent_success "$classification" "$FIXTURE_ROOT/stdout" "$FIXTURE_ROOT/stderr" run_publish
        [[ "$(<"$FIXTURE_GH_CAPTURE/closed-pr")" == 17 ]] || fail 'obsolete PR not closed'
        if [[ "$classification" == no-change ]]; then
            [[ "$(<"$FIXTURE_GH_CAPTURE/closed-issue")" == 23 ]] || fail 'obsolete issue not closed'
        else
            grep -F 'issue edit 23 ' "$FIXTURE_GH_CAPTURE/calls" >/dev/null || fail 'failure did not update issue'
            [[ "$(sed -n '2p' "$FIXTURE_GH_CAPTURE/calls")" == 'pr close '* ]] || fail 'issue handled before PR closure'
        fi
    done
}

test_api_failures_stop_followup_actions() {
    local mode
    for mode in pr-list pr-close issue-list; do
        setup_success_fixture
        existing_records
        configure_result branch-validation
        touch "$FIXTURE_GH_CAPTURE/fail-$mode"
        assert_publish_failure 'reconciliation error:'
        [[ ! -f "$FIXTURE_GH_CAPTURE/issue-body" ]] || fail 'API failure still published issue'
        if [[ "$mode" != issue-list ]]; then
            ! grep -F 'issue ' "$FIXTURE_GH_CAPTURE/calls" >/dev/null || fail 'issue API called after PR failure'
        fi
    done
}

test_dry_run_and_automation_do_not_mutate() {
    local classification remote dry_run
    for classification in success no-change branch-validation automation; do
        setup_success_fixture
        [[ "$classification" == success ]] || configure_result "$classification"
        remote=$FIXTURE_REMOTE
        FIXTURE_REMOTE="$FIXTURE_ROOT/unavailable.git"
        dry_run=true
        [[ "$classification" != automation ]] || dry_run=false
        RECONCILE_DRY_RUN=$dry_run \
            assert_silent_success 'dry run or automation' "$FIXTURE_ROOT/stdout" "$FIXTURE_ROOT/stderr" run_publish
        [[ ! -f "$FIXTURE_GH_CAPTURE/calls" ]] || fail 'dry run called GitHub'
        [[ -z "$("$TEST_GIT" --git-dir "$remote" for-each-ref --format='%(refname)' refs/heads/update_)" ]] || fail 'dry run pushed update ref'
    done
}

test_exact_lease_rejects_racing_update_ref() {
    setup_success_fixture
    fixture_commit "$FIXTURE_SOURCE" 'test: competing signed update' --allow-empty
    local race_oid
    race_oid=$("$TEST_GIT" -C "$FIXTURE_SOURCE" rev-parse HEAD)
    "$TEST_GIT" -C "$FIXTURE_SOURCE" push --quiet "$FIXTURE_REMOTE" "HEAD:refs/heads/race-object"
    rm -f "$FIXTURE_BIN/git"
    cat >"$FIXTURE_BIN/git" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ " $* " == *' push '* ]]; then
    "$GIT_RACE_COMMAND" --git-dir "$GIT_RACE_REMOTE" update-ref "$GIT_RACE_REF" "$GIT_RACE_OID"
fi
exec "$GIT_RACE_COMMAND" "$@"
EOF
    chmod +x "$FIXTURE_BIN/git"
    GIT_RACE_COMMAND="$(command -v "$TEST_GIT")" \
        GIT_RACE_REMOTE="$FIXTURE_REMOTE" GIT_RACE_REF="refs/heads/update_$FIXTURE_STATE_BRANCH" \
        GIT_RACE_OID="$race_oid" assert_publish_failure 'update ref push failed its exact lease'
    [[ "$("$TEST_GIT" --git-dir "$FIXTURE_REMOTE" rev-parse "refs/heads/update_$FIXTURE_STATE_BRANCH")" == "$race_oid" ]] \
        || fail 'publication overwrote a racing update ref'
    [[ ! -f "$FIXTURE_GH_CAPTURE/calls" ]] || fail 'failed lease reached GitHub'
}

test_failure_issue_create_reopen_and_invalid_status() {
    local mode
    for mode in create reopen invalid; do
        setup_success_fixture
        configure_result branch-validation
        case "$mode" in
            reopen)
                existing_records
                jq '.[0].closed=true' "$FIXTURE_GH_CAPTURE/existing-failure-issue" >"$FIXTURE_ROOT/closed.json"
                mv "$FIXTURE_ROOT/closed.json" "$FIXTURE_GH_CAPTURE/existing-failure-issue"
                ;;
            invalid) change_result '.failure.status=0' ;;
        esac
        if [[ "$mode" == invalid ]]; then
            assert_publish_failure 'result manifest or immutable identity is invalid'
            [[ ! -f "$FIXTURE_GH_CAPTURE/calls" ]] || fail 'invalid failure cleaned up records'
        else
            RECONCILE_DRY_RUN=false assert_silent_success "failure $mode" "$FIXTURE_ROOT/stdout" "$FIXTURE_ROOT/stderr" run_publish
            grep -F 'Status: <code>1</code>' "$FIXTURE_GH_CAPTURE/issue-body" >/dev/null \
                || fail 'failure status is not formatted as code'
            if [[ "$mode" == create ]]; then
                grep -F 'issue create ' "$FIXTURE_GH_CAPTURE/calls" >/dev/null || fail 'failure issue not created'
            else
                [[ "$(<"$FIXTURE_GH_CAPTURE/reopened-issue")" == 23 ]] || fail 'closed failure issue not reopened'
            fi
        fi
    done
}

if [[ $# -eq 0 ]]; then
    tests=(test_publishes_one_owned_commit_from_exact_base test_result_validation_prevents_mutation
        test_rejects_unsafe_candidate_paths_and_modes test_publication_refuses_moved_base_and_foreign_update
        test_stale_results_do_not_reconcile_lifecycle
        test_reconciles_no_change_and_failure_in_order test_api_failures_stop_followup_actions
        test_dry_run_and_automation_do_not_mutate test_exact_lease_rejects_racing_update_ref
        test_failure_issue_create_reopen_and_invalid_status)
else tests=("$@"); fi
for test_name in "${tests[@]}"; do
    "$test_name"
    printf 'PASS: %s\n' "$test_name"
done
