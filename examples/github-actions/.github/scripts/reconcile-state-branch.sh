#!/usr/bin/env bash

set -euo pipefail
set +x
export LC_ALL=C

usage() {
    cat <<'EOF'
Usage: reconcile-state-branch.sh classify
       reconcile-state-branch.sh publish
       reconcile-state-branch.sh --help

classify checks result.json, its immutable identity and candidate digest, and prints
the classification. Requires RECONCILE_RESULT_DIR and RECONCILE_RUN_ID,
RECONCILE_RUN_ATTEMPT, RECONCILE_AUTOMATION_POLICY_ID, RECONCILE_CONTROL_OID,
RECONCILE_STATE_BRANCH, RECONCILE_BASE_OID, RECONCILE_REF_HASH.

publish additionally checks the clean exact-base RECONCILE_TARGET_CHECKOUT and
RECONCILE_TERRAFORM_ROOTS, creates one owned update commit, and reconciles marked
PRs and failure issues. Requires RECONCILE_DRY_RUN (true/false), RUNNER_TEMP,
RECONCILE_RUN_URL, RECONCILE_GIT_REMOTE, RECONCILE_REPOSITORY, GH_TOKEN,
RECONCILE_COMMIT_AUTHOR_NAME and RECONCILE_COMMIT_AUTHOR_EMAIL.
It respects Git signing configuration. Dry runs never mutate remote refs or GitHub.
Automation and invalid results never publish or clean up GitHub records.
EOF
}

RECONCILE_TEMPORARY_PATH=''
CLASSIFICATION=''
MANIFEST=''
PATCH=''

reconcile_error() { echo "reconciliation error: $*" >&2; exit 1; }

# shellcheck disable=SC2329 # Invoked by the EXIT trap.
cleanup_reconcile_temporaries() {
    [[ -z "$RECONCILE_TEMPORARY_PATH" ]] || rm -rf -- "$RECONCILE_TEMPORARY_PATH"
}

bounded() { timeout --signal=TERM --kill-after=5s 120s "$@"; }

sha256_file() {
    local digest
    digest=$(sha256sum "$1")
    printf '%s\n' "${digest%% *}"
}

validate_result() {
    require_common_identity
    : "${RECONCILE_RESULT_DIR:?RECONCILE_RESULT_DIR must be set}"
    MANIFEST="$RECONCILE_RESULT_DIR/result.json"
    PATCH="$RECONCILE_RESULT_DIR/candidate.patch"
    [[ -d "$RECONCILE_RESULT_DIR" && ! -L "$RECONCILE_RESULT_DIR" \
        && -f "$MANIFEST" && ! -L "$MANIFEST" \
        && -d "$RECONCILE_RESULT_DIR/logs" && ! -L "$RECONCILE_RESULT_DIR/logs" ]] \
        || reconcile_error 'result directory is missing or invalid'
    jq -e --arg run "$RECONCILE_RUN_ID" --arg attempt "$RECONCILE_RUN_ATTEMPT" \
        --arg policy "$RECONCILE_AUTOMATION_POLICY_ID" --arg control "$RECONCILE_CONTROL_OID" \
        --arg branch "$RECONCILE_STATE_BRANCH" --arg base "$RECONCILE_BASE_OID" \
        --arg hash "$RECONCILE_REF_HASH" '
        .schema_version == 3 and .run_id == $run and .run_attempt == $attempt and
        .automation_policy_id == $policy and .control_oid == $control and
        .state_branch == $branch and .base_oid == $base and .ref_hash == $hash and
        (.roots | type == "array" and length > 0 and length == (unique | length) and
          all(.[]; type == "string" and length > 0 and
            (startswith("/") | not) and (test("[[:cntrl:]]") | not) and
            (split("/") | all(.[]; . != ".." and . != ".git" and . != ".terraform")))) and
        (.classification as $c | if $c == "success" then
            (.patch_sha256 | type == "string" and test("^[0-9a-f]{64}$")) and (has("failure") | not)
          elif $c == "no-change" then (has("patch_sha256") | not) and (has("failure") | not)
          elif $c == "automation" then (has("patch_sha256") | not)
          elif ["branch-update", "branch-init", "branch-format", "branch-validation"] | index($c) then
            (has("patch_sha256") | not) and
            (.failure.status | type == "number" and . > 0 and . <= 255 and floor == .) and
            (.failure.root as $r | .roots | index($r) != null) and
            (.failure.stage == ({"branch-update":"tf-version-bump", "branch-init":"terraform init",
                "branch-format":"terraform fmt", "branch-validation":"terraform validate"}[$c]))
          else false end)' "$MANIFEST" >/dev/null \
        || reconcile_error 'result manifest or immutable identity is invalid'
    CLASSIFICATION=$(jq -r '.classification' "$MANIFEST")
    if [[ "$CLASSIFICATION" == success ]]; then
        [[ -f "$PATCH" && ! -L "$PATCH" && -s "$PATCH" ]] \
            || reconcile_error 'candidate patch must be a nonempty regular file'
        [[ "$(sha256_file "$PATCH")" == "$(jq -r '.patch_sha256' "$MANIFEST")" ]] \
            || reconcile_error 'candidate patch digest is invalid'
    else
        [[ ! -e "$PATCH" && ! -L "$PATCH" ]] || reconcile_error 'non-success result contains a patch'
    fi
}

verify_checkout_and_roots() {
    : "${RECONCILE_TARGET_CHECKOUT:?RECONCILE_TARGET_CHECKOUT must be set}"
    : "${RECONCILE_TERRAFORM_ROOTS:?RECONCILE_TERRAFORM_ROOTS must be set}"
    local checkout root canonical relative
    checkout=$(realpath "$RECONCILE_TARGET_CHECKOUT")
    [[ "$(realpath "$(git -C "$checkout" rev-parse --show-toplevel)")" == "$checkout" ]] \
        || reconcile_error 'publication checkout must be the Git worktree root'
    [[ "$(git -C "$checkout" rev-parse HEAD)" == "$RECONCILE_BASE_OID" ]] \
        || reconcile_error 'publication checkout HEAD does not match base OID'
    [[ -z "$(git -C "$checkout" status --porcelain=v1 --untracked-files=all --ignored=matching)" ]] \
        || reconcile_error 'publication checkout must be clean'
    local -a roots=()
    while IFS= read -r root; do
        [[ -n "$root" && "$root" != /* && "/$root/" != *'/../'* ]] \
            || reconcile_error 'configured Terraform root is invalid'
        canonical=$(realpath "$checkout/$root")
        [[ -d "$canonical" && ( "$canonical" == "$checkout" || "$canonical" == "$checkout/"* ) ]] \
            || reconcile_error 'configured Terraform root escapes checkout or is missing'
        relative=${canonical#"$checkout"/}
        [[ "$canonical" != "$checkout" ]] || relative=.
        roots+=("$relative")
    done < <(printf '%s\n' "${RECONCILE_TERRAFORM_ROOTS%$'\n'}")
    local roots_json
    roots_json=$(jq -cn '$ARGS.positional' --args -- "${roots[@]}")
    jq -e --argjson roots "$roots_json" '.roots == $roots' "$MANIFEST" >/dev/null \
        || reconcile_error 'result roots do not match configured Terraform roots'
}

allowed_candidate_path() {
    local path=$1 parent=${1%/*} root
    [[ "$parent" != "$path" ]] || parent=.
    [[ -n "$path" && "$path" != /* && "/$path/" != *'/../'* \
        && "/$path/" != *'/.git/'* && "/$path/" != *'/.terraform/'* && "$path" != *$'\n'* ]] || return 1
    while IFS= read -r root; do
        if [[ "$path" == *.tf && ( "$root" == . || "$path" == "$root/"* ) ]]; then return 0; fi
        if [[ "${path##*/}" == .terraform.lock.hcl && "$parent" == "$root" ]]; then return 0; fi
    done < <(jq -r '.roots[]' "$MANIFEST")
    return 1
}

preflight_candidate() {
    local checkout=$RECONCILE_TARGET_CHECKOUT index="$RECONCILE_TEMPORARY_PATH/index"
    GIT_INDEX_FILE="$index" git -C "$checkout" read-tree "$RECONCILE_BASE_OID"
    GIT_INDEX_FILE="$index" git -C "$checkout" apply --cached --binary "$PATCH" \
        || reconcile_error 'candidate patch does not apply to exact base'
    GIT_INDEX_FILE="$index" git -C "$checkout" diff --cached --raw -z --no-renames >"$RECONCILE_TEMPORARY_PATH/changes"
    [[ -s "$RECONCILE_TEMPORARY_PATH/changes" ]] || reconcile_error 'success candidate has no changes'
    local raw path old_mode new_mode
    while IFS= read -r -d '' raw && IFS= read -r -d '' path; do
        read -r old_mode new_mode _ <<<"$raw"
        [[ "$new_mode" == 100644 && ( "$old_mode" == :000000 || "$old_mode" == :100644 ) ]] \
            || reconcile_error 'candidate deletion, symlink or file mode change is forbidden'
        allowed_candidate_path "$path" || reconcile_error 'candidate contains an unexpected path'
    done <"$RECONCILE_TEMPORARY_PATH/changes"
}

construct_update_commit() {
    : "${RECONCILE_COMMIT_AUTHOR_NAME:?RECONCILE_COMMIT_AUTHOR_NAME must be set}"
    : "${RECONCILE_COMMIT_AUTHOR_EMAIL:?RECONCILE_COMMIT_AUTHOR_EMAIL must be set}"
    local checkout=$RECONCILE_TARGET_CHECKOUT message="$RECONCILE_TEMPORARY_PATH/commit-message"
    git -C "$checkout" apply --index --binary "$PATCH" || reconcile_error 'could not apply candidate patch'
    printf '%s\n\n%s\n%s\n' 'chore: update Terraform dependencies' \
        "Tf-Version-Bump-Automation: $RECONCILE_AUTOMATION_POLICY_ID/$RECONCILE_REF_HASH" \
        "Tf-Version-Bump-Base: $RECONCILE_BASE_OID" >"$message"
    # Keep the caller's signing policy; a failed signer must stop publication.
    git -C "$checkout" -c user.name="$RECONCILE_COMMIT_AUTHOR_NAME" \
        -c user.email="$RECONCILE_COMMIT_AUTHOR_EMAIL" commit -F "$message" >/dev/null \
        || reconcile_error 'could not construct Terraform update commit'
}

publish_update_ref() {
    local checkout=$RECONCILE_TARGET_CHECKOUT state_ref="refs/heads/$RECONCILE_STATE_BRANCH"
    local update_ref="refs/heads/update_$RECONCILE_STATE_BRANCH" refs observed='' fetched message
    refs=$(bounded git -C "$checkout" ls-remote --refs "$RECONCILE_GIT_REMOTE" "$state_ref" "$update_ref") \
        || reconcile_error 'could not inspect remote refs'
    local oid ref state_oid=''
    while read -r oid ref; do
        [[ "$ref" != "$state_ref" ]] || state_oid=$oid
        [[ "$ref" != "$update_ref" ]] || observed=$oid
    done <<<"$refs"
    [[ "$state_oid" == "$RECONCILE_BASE_OID" ]] || reconcile_error 'state ref moved after discovery'
    if [[ -n "$observed" ]]; then
        fetched="refs/remotes/tf-version-bump/$RECONCILE_REF_HASH/update"
        bounded git -C "$checkout" fetch --quiet --no-tags --no-write-fetch-head \
            "$RECONCILE_GIT_REMOTE" "+$update_ref:$fetched" || reconcile_error 'could not fetch existing update ref'
        [[ "$(git -C "$checkout" rev-parse "$fetched")" == "$observed" ]] \
            || reconcile_error 'update ref moved during ownership check'
        message=$(git -C "$checkout" show -s --format=%B "$observed")
        grep -Fx "Tf-Version-Bump-Automation: $RECONCILE_AUTOMATION_POLICY_ID/$RECONCILE_REF_HASH" <<<"$message" >/dev/null \
            || reconcile_error 'existing update ref is not owned by this automation policy'
    fi
    bounded git -C "$checkout" push --quiet --force-with-lease="$update_ref:$observed" \
        "$RECONCILE_GIT_REMOTE" "HEAD:$update_ref" || reconcile_error 'update ref push failed its exact lease'
}

write_github_body() {
    local body="$RECONCILE_TEMPORARY_PATH/body"
    printf '%s\n\nTerraform dependency update for %s.\n\nResult: %s\n\nBase: %s\n\nWorkflow run: <a href="%s">run %s, attempt %s</a>\n' \
        "$(github_marker)" "$(html_code "$RECONCILE_STATE_BRANCH")" "$(html_code "$CLASSIFICATION")" \
        "$(html_code "$RECONCILE_BASE_OID")" "$(html_escape "$RECONCILE_RUN_URL")" \
        "$RECONCILE_RUN_ID" "$RECONCILE_RUN_ATTEMPT" >"$body"
    if [[ "$CLASSIFICATION" == branch-* ]]; then
        printf '\nStage: %s\n\nRoot: %s\n\nStatus: %s\n' \
            "$(html_code "$(jq -r '.failure.stage' "$MANIFEST")")" \
            "$(html_code "$(jq -r '.failure.root' "$MANIFEST")")" \
            "$(jq -r '.failure.status' "$MANIFEST")" >>"$body"
    fi
}

reconcile_lifecycle() {
    local number record closed body="$RECONCILE_TEMPORARY_PATH/body"
    if [[ "$CLASSIFICATION" == no-change ]]; then close_marked_pr; close_marked_issue; return; fi
    write_github_body
    if [[ "$CLASSIFICATION" == success ]]; then
        number=$(marked_pr_number "$(github_marker)") || reconcile_error 'could not look up marked update pull request'
        if [[ -n "$number" ]]; then
            bounded gh pr edit "$number" --repo "$RECONCILE_REPOSITORY" --title 'Terraform dependency update' --body-file "$body" >/dev/null
        else
            bounded gh pr create --repo "$RECONCILE_REPOSITORY" --head "update_$RECONCILE_STATE_BRANCH" \
                --base "$RECONCILE_STATE_BRANCH" --title 'Terraform dependency update' --body-file "$body" >/dev/null
        fi
        close_marked_issue
    else
        close_marked_pr
        record=$(marked_issue_record "$(github_marker)") || reconcile_error 'could not look up marked failure issue'
        read -r number closed <<<"$record"
        if [[ -n "$number" ]]; then
            [[ "$closed" != true ]] || bounded gh issue reopen "$number" --repo "$RECONCILE_REPOSITORY" >/dev/null
            bounded gh issue edit "$number" --repo "$RECONCILE_REPOSITORY" --title 'Terraform dependency update failed' --body-file "$body" >/dev/null
        else
            bounded gh issue create --repo "$RECONCILE_REPOSITORY" --title 'Terraform dependency update failed' --body-file "$body" >/dev/null
        fi
    fi
}

publish_result() {
    validate_result
    [[ "$CLASSIFICATION" != automation ]] || return 0
    : "${RECONCILE_DRY_RUN:?RECONCILE_DRY_RUN must be set}"
    [[ "$RECONCILE_DRY_RUN" == true || "$RECONCILE_DRY_RUN" == false ]] || reconcile_error 'dry-run must be true or false'
    verify_checkout_and_roots
    : "${RUNNER_TEMP:?RUNNER_TEMP must be set}"
    RECONCILE_TEMPORARY_PATH=$(mktemp -d "$RUNNER_TEMP/tf-version-bump-publish.XXXXXX")
    [[ "$CLASSIFICATION" != success ]] || preflight_candidate
    [[ "$RECONCILE_DRY_RUN" != true ]] || return 0
    : "${RECONCILE_GIT_REMOTE:?RECONCILE_GIT_REMOTE must be set}"
    : "${RECONCILE_REPOSITORY:?RECONCILE_REPOSITORY must be set}"
    : "${RECONCILE_RUN_URL:?RECONCILE_RUN_URL must be set}"
    : "${GH_TOKEN:?GH_TOKEN must be set}"
    if [[ "$CLASSIFICATION" == success ]]; then construct_update_commit; publish_update_ref; fi
    reconcile_lifecycle
}

require_common_identity_values() {
    : "${RECONCILE_RUN_ID:?RECONCILE_RUN_ID must be set}"
    : "${RECONCILE_RUN_ATTEMPT:?RECONCILE_RUN_ATTEMPT must be set}"
    : "${RECONCILE_AUTOMATION_POLICY_ID:?RECONCILE_AUTOMATION_POLICY_ID must be set}"
    : "${RECONCILE_CONTROL_OID:?RECONCILE_CONTROL_OID must be set}"
    : "${RECONCILE_STATE_BRANCH:?RECONCILE_STATE_BRANCH must be set}"
    : "${RECONCILE_BASE_OID:?RECONCILE_BASE_OID must be set}"
    : "${RECONCILE_REF_HASH:?RECONCILE_REF_HASH must be set}"

    [[ "$RECONCILE_RUN_ID" =~ ^[1-9][0-9]*$ ]] \
        || reconcile_error "run ID must be a positive integer"
    [[ "$RECONCILE_RUN_ATTEMPT" =~ ^[1-9][0-9]*$ ]] \
        || reconcile_error "run attempt must be a positive integer"
    [[ "$RECONCILE_AUTOMATION_POLICY_ID" =~ ^[a-z0-9][a-z0-9-]{0,31}$ ]] \
        || reconcile_error "automation policy ID is invalid"
    [[ "$RECONCILE_CONTROL_OID" =~ ^[0-9a-f]{40}$ ]] \
        || reconcile_error "control OID is invalid"
    [[ "$RECONCILE_BASE_OID" =~ ^[0-9a-f]{40}$ ]] \
        || reconcile_error "base OID is invalid"
    [[ "$RECONCILE_REF_HASH" =~ ^[0-9a-f]{64}$ ]] \
        || reconcile_error "ref hash is invalid"
    [[ -n "$RECONCILE_STATE_BRANCH" && "$RECONCILE_STATE_BRANCH" != *$'\n'* ]] \
        || reconcile_error "state branch is invalid"

    local computed_ref_hash
    computed_ref_hash=$(printf '%s' "refs/heads/$RECONCILE_STATE_BRANCH" | sha256sum)
    [[ "${computed_ref_hash%% *}" == "$RECONCILE_REF_HASH" ]] \
        || reconcile_error "state ref hash does not match state branch"
}


require_common_identity() {
    require_common_identity_values
    git check-ref-format "refs/heads/$RECONCILE_STATE_BRANCH" >/dev/null 2>&1 \
        || reconcile_error "state branch is invalid"
}


github_marker() {
    printf '<!-- tf-version-bump:%s:%s -->\n' \
        "$RECONCILE_AUTOMATION_POLICY_ID" "$RECONCILE_REF_HASH"
}


html_escape() {
    printf '%s' "$1" | jq -Rr '@html' | sed 's/@/\&#64;/g'
}


html_code() {
    printf '<code>%s</code>' "$(html_escape "$1")"
}


marked_pr_number() {
    local marker=$1 response
    response=$(bounded gh pr list --repo "$RECONCILE_REPOSITORY" --state open \
        --head "update_$RECONCILE_STATE_BRANCH" --base "$RECONCILE_STATE_BRANCH" \
        --json number,body) || return 1
    [[ -n "$response" ]] || response='[]'
    jq -r --arg marker "$marker" \
        '[.[] | select((.body // "") | contains($marker)) | .number] | first // empty' \
        <<<"$response"
}


close_marked_pr() {
    local pr_number
    pr_number=$(marked_pr_number "$(github_marker)") \
        || reconcile_error "could not look up marked update pull request"
    if [[ -n "$pr_number" ]]; then
        bounded gh pr close "$pr_number" --repo "$RECONCILE_REPOSITORY" >/dev/null \
            || reconcile_error "could not close marked update pull request"
    fi
}


marked_issue_record() {
    local marker=$1 response
    response=$(bounded gh issue list --repo "$RECONCILE_REPOSITORY" --state all \
        --search "$RECONCILE_REF_HASH in:body" --limit 100 --json number,body,closed) || return 1
    [[ -n "$response" ]] || response='[]'
    jq -r --arg marker "$marker" \
        '[.[] | select((.body // "") | contains($marker))] | first // {} |
         [(.number // ""), (.closed // "")] | @tsv' \
        <<<"$response"
}


close_marked_issue() {
    local record issue_number issue_closed
    record=$(marked_issue_record "$(github_marker)") \
        || reconcile_error "could not look up marked failure issue"
    read -r issue_number issue_closed <<<"$record"
    if [[ -n "$issue_number" && "$issue_closed" != "true" ]]; then
        bounded gh issue close "$issue_number" --repo "$RECONCILE_REPOSITORY" >/dev/null \
            || reconcile_error "could not close marked failure issue"
    fi
}


if [[ "${1-}" == --help && $# -eq 1 ]]; then usage; exit 0; fi
trap cleanup_reconcile_temporaries EXIT
if [[ "${1-}" == classify && $# -eq 1 ]]; then
    validate_result
    printf '%s\n' "$CLASSIFICATION"
elif [[ "${1-}" == publish && $# -eq 1 ]]; then
    publish_result
else usage >&2; exit 2; fi
