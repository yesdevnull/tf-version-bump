#!/usr/bin/env bash

# Requires bash >= 4.0 (associative arrays), matching the readarray/mapfile floor its sibling
# scripts already require. This is the single file the standalone-install contract ships alone,
# so this floor -- not an ambient macOS system bash 3.2 -- is what a copyable install must supply.
set -euo pipefail
set +x
export LC_ALL=C

usage() {
    cat <<'EOF'
Usage: reconcile-state-branch.sh --help
       reconcile-state-branch.sh verify
       reconcile-state-branch.sh publish

Verify preparation and validation artefacts, or publish one verified result.
EOF
}

reconcile_error() {
    echo "reconciliation error: $*" >&2
    exit 1
}

RECONCILE_TEMPORARY_PATH=""

# shellcheck disable=SC2329 # Invoked by the verify/publish EXIT trap.
cleanup_reconcile_temporaries() {
    if [[ -n "$RECONCILE_TEMPORARY_PATH" ]]; then
        rm -rf -- "$RECONCILE_TEMPORARY_PATH"
        RECONCILE_TEMPORARY_PATH=""
    fi
}

sha256_file() {
    local digest
    digest=$(sha256sum "$1")
    printf '%s\n' "${digest%% *}"
}

require_common_identity() {
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
    git check-ref-format "refs/heads/$RECONCILE_STATE_BRANCH" >/dev/null 2>&1 \
        || reconcile_error "state branch is invalid"

    local computed_ref_hash
    computed_ref_hash=$(printf '%s' "refs/heads/$RECONCILE_STATE_BRANCH" | sha256sum)
    [[ "${computed_ref_hash%% *}" == "$RECONCILE_REF_HASH" ]] \
        || reconcile_error "state ref hash does not match state branch"
}

manifest_matches_identity() {
    local manifest=$1
    jq -e '
        .schema_version == 1 and
        .run_id == env.RECONCILE_RUN_ID and
        .run_attempt == env.RECONCILE_RUN_ATTEMPT and
        .automation_policy_id == env.RECONCILE_AUTOMATION_POLICY_ID and
        .control_oid == env.RECONCILE_CONTROL_OID and
        .state_branch == env.RECONCILE_STATE_BRANCH and
        .base_oid == env.RECONCILE_BASE_OID and
        .ref_hash == env.RECONCILE_REF_HASH
    ' "$manifest" >/dev/null
}

path_is_declared_direct_file() {
    local manifest=$1 relative_path=$2
    [[ -n "$relative_path" && "$relative_path" != /* ]] || return 1
    # A literal newline in relative_path can never reach here intact: it is read one line at a
    # time by the newline-splitting `read` loop in verify_declared_candidate, which would already
    # have split it into separate (non-matching) lines. The real defence against a smuggled
    # newline-bearing path is the byte-exact sorted actual/declared path-set comparison in
    # verify_declared_candidate.
    [[ "/$relative_path/" != *"/../"* ]] || return 1

    local filename=${relative_path##*/}
    local parent=${relative_path%/*}
    [[ "$parent" != "$relative_path" ]] || parent="."
    [[ "$filename" == *.tf || "$filename" == ".terraform.lock.hcl" ]] || return 1
    jq -e --arg parent "$parent" '.roots | any(.path == $parent)' "$manifest" >/dev/null
}

verify_declared_candidate() {
    local manifest=$1 patch=$2 checkout=$3
    local head
    head=$(git -C "$checkout" rev-parse HEAD 2>/dev/null) \
        || reconcile_error "target checkout is not a Git worktree"
    [[ "$head" == "$RECONCILE_BASE_OID" ]] \
        || reconcile_error "target checkout HEAD does not match base OID"
    [[ -z "$(git -C "$checkout" status --porcelain=v1 --untracked-files=all)" ]] \
        || reconcile_error "target checkout must be clean"

    local relative_path
    while IFS= read -r relative_path; do
        path_is_declared_direct_file "$manifest" "$relative_path" \
            || reconcile_error "candidate contains an undeclared or unsafe path"
    done < <(jq -er '.changed_files[].path' "$manifest")

    git -C "$checkout" apply --check --index --binary "$patch" >/dev/null \
        || reconcile_error "candidate patch does not apply to exact base"
    git -C "$checkout" apply --index --binary "$patch" >/dev/null \
        || reconcile_error "candidate patch could not be applied"

    # Mode and path are read together from one raw diff so no per-path `git ls-files` pathspec
    # lookup can have its glob metacharacters (e.g. "a[5].tf") matched against a sibling file.
    local -a actual_paths=() actual_modes=()
    local raw_entry mode
    while IFS= read -r -d '' raw_entry && IFS= read -r -d '' relative_path; do
        read -r _ mode _ _ _ <<<"$raw_entry"
        actual_paths+=("$relative_path")
        actual_modes+=("$mode")
    done < <(git -C "$checkout" diff --cached --raw -z --no-renames)

    local raw_paths_digest normalised_paths_digest
    raw_paths_digest=$(printf '%s\0' "${actual_paths[@]}" | sha256sum)
    normalised_paths_digest=$(printf '%s\0' "${actual_paths[@]}" | jq -Rjsc . | sha256sum)
    [[ "${raw_paths_digest%% *}" == "${normalised_paths_digest%% *}" ]] \
        || reconcile_error "candidate patch path is not valid UTF-8"

    local actual_paths_json declared_paths_json
    actual_paths_json=$(jq -cn '$ARGS.positional | sort' --args -- "${actual_paths[@]}")
    declared_paths_json=$(jq -c '[.changed_files[].path] | sort' "$manifest")
    [[ "$actual_paths_json" == "$declared_paths_json" ]] \
        || reconcile_error "candidate patch paths do not match the manifest"

    local -a file_sha256s=()
    if [[ ${#actual_paths[@]} -gt 0 ]]; then
        local sha_line
        while IFS= read -r sha_line; do
            file_sha256s+=("${sha_line%% *}")
        done < <(cd "$checkout" && sha256sum -- "${actual_paths[@]}" 2>/dev/null)
        [[ ${#file_sha256s[@]} -eq ${#actual_paths[@]} ]] \
            || reconcile_error "candidate file digest does not match the manifest"
    fi

    # Build the declared path -> mode/digest lookup with one jq pass over the manifest instead of
    # two jq queries per declared path; NUL-delimited fields keep this safe for a path containing
    # any byte (including a legitimate tab or newline) other than NUL, which Git paths can't hold.
    local -A expected_mode_by_path=() expected_sha256_by_path=()
    local declared_path declared_mode declared_sha256
    while IFS= read -r -d '' declared_path \
        && IFS= read -r -d '' declared_mode \
        && IFS= read -r -d '' declared_sha256; do
        expected_mode_by_path["$declared_path"]=$declared_mode
        expected_sha256_by_path["$declared_path"]=$declared_sha256
    done < <(jq -j '([0] | implode) as $nul | .changed_files[] | (.path, $nul, .mode, $nul, .sha256, $nul)' "$manifest")

    local index expected_mode expected_digest
    for index in "${!actual_paths[@]}"; do
        relative_path=${actual_paths[$index]}
        expected_digest=${expected_sha256_by_path[$relative_path]}
        [[ "${file_sha256s[$index]}" == "$expected_digest" ]] \
            || reconcile_error "candidate file digest does not match the manifest"
        expected_mode=${expected_mode_by_path[$relative_path]}
        [[ "${actual_modes[$index]}" == "$expected_mode" ]] \
            || reconcile_error "candidate file mode does not match the manifest"
    done
}

bounded_failure_json() {
    jq -ce '.failure |
        select((.stage | type) == "string" and (.root | type) == "string" and
               (.status | type) == "number") |
        {stage, root, status}' "$1" \
        || reconcile_error "failure details are invalid"
}

write_verified_result() {
    local classification=$1 preparation_digest=$2 outcome_digest=${3-} patch_digest=${4-}
    local failure_json=${5-null} run_url=${6-}
    local destination=$RECONCILE_VERIFIED_RESULT_DIR
    [[ ! -e "$destination" && ! -L "$destination" ]] \
        || reconcile_error "verified result destination must be absent"
    local parent=${destination%/*}
    [[ "$parent" != "$destination" && -d "$parent" ]] \
        || reconcile_error "verified result parent must exist"
    local stage
    stage=$(mktemp -d "$parent/.verified-result.XXXXXX")
    RECONCILE_TEMPORARY_PATH=$stage
    chmod 700 "$stage"

    jq -n \
        --arg run_id "$RECONCILE_RUN_ID" \
        --arg run_attempt "$RECONCILE_RUN_ATTEMPT" \
        --arg policy "$RECONCILE_AUTOMATION_POLICY_ID" \
        --arg control_oid "$RECONCILE_CONTROL_OID" \
        --arg branch "$RECONCILE_STATE_BRANCH" \
        --arg base_oid "$RECONCILE_BASE_OID" \
        --arg ref_hash "$RECONCILE_REF_HASH" \
        --arg classification "$classification" \
        --arg preparation_digest "$preparation_digest" \
        --arg outcome_digest "$outcome_digest" \
        --arg patch_digest "$patch_digest" \
        --argjson failure "$failure_json" \
        --arg run_url "$run_url" '
        {schema_version: 1, run_id: $run_id, run_attempt: $run_attempt,
         automation_policy_id: $policy, control_oid: $control_oid,
         state_branch: $branch, base_oid: $base_oid, ref_hash: $ref_hash,
         classification: $classification,
         preparation_manifest_sha256: $preparation_digest}
        + if $outcome_digest == "" then {} else
            {validation_outcome_sha256: $outcome_digest} end
        + if $patch_digest == "" then {} else {patch_sha256: $patch_digest} end
        + if $failure == null then {} else
            {failure: $failure, run_url: $run_url} end
        ' >"$stage/manifest.json"
    if [[ -n "$patch_digest" ]]; then
        cp -- "$RECONCILE_PREPARATION_BUNDLE_DIR/candidate.patch" "$stage/candidate.patch"
    fi
    chmod 444 "$stage"/*
    mv -- "$stage" "$destination"
    RECONCILE_TEMPORARY_PATH=""
    chmod 555 "$destination"
}

verify_result() {
    require_common_identity
    : "${RECONCILE_PREPARATION_BUNDLE_DIR:?RECONCILE_PREPARATION_BUNDLE_DIR must be set}"
    : "${RECONCILE_TARGET_CHECKOUT:?RECONCILE_TARGET_CHECKOUT must be set}"
    : "${RECONCILE_VERIFIED_RESULT_DIR:?RECONCILE_VERIFIED_RESULT_DIR must be set}"

    local preparation_manifest="$RECONCILE_PREPARATION_BUNDLE_DIR/manifest.json"
    [[ -f "$preparation_manifest" && ! -L "$preparation_manifest" ]] \
        || reconcile_error "preparation manifest must be a regular file"
    manifest_matches_identity "$preparation_manifest" \
        || reconcile_error "preparation manifest identity or schema is invalid"
    local preparation_classification preparation_digest
    preparation_classification=$(jq -er '.classification' "$preparation_manifest")
    preparation_digest=$(sha256_file "$preparation_manifest")
    if [[ "$preparation_classification" == "branch-update" \
        || "$preparation_classification" == "branch-init" ]]; then
        [[ -z "${RECONCILE_VALIDATION_OUTCOME_DIR:-}" \
            || ! -e "$RECONCILE_VALIDATION_OUTCOME_DIR" ]] \
            || reconcile_error "preparation failure must not have a validation outcome"
        [[ "$(git -C "$RECONCILE_TARGET_CHECKOUT" rev-parse HEAD 2>/dev/null)" \
            == "$RECONCILE_BASE_OID" ]] \
            || reconcile_error "target checkout HEAD does not match base OID"
        : "${RECONCILE_RUN_URL:?RECONCILE_RUN_URL must be set for failures}"
        local preparation_failure
        preparation_failure=$(bounded_failure_json "$preparation_manifest")
        write_verified_result "$preparation_classification" "$preparation_digest" "" "" \
            "$preparation_failure" "$RECONCILE_RUN_URL"
        return
    fi
    [[ "$preparation_classification" == "success" \
        || "$preparation_classification" == "no-change" ]] \
        || reconcile_error "preparation classification is not supported"

    local patch="" patch_digest="" expected_patch_digest outcome outcome_digest outcome_classification
    if [[ "$preparation_classification" == "success" ]]; then
        patch="$RECONCILE_PREPARATION_BUNDLE_DIR/candidate.patch"
        [[ -f "$patch" && ! -L "$patch" ]] \
            || reconcile_error "candidate patch must be a regular file"
        patch_digest=$(sha256_file "$patch")
        expected_patch_digest=$(jq -er '.patch_sha256' "$preparation_manifest")
        [[ "$patch_digest" == "$expected_patch_digest" ]] \
            || reconcile_error "candidate patch digest does not match the manifest"
    else
        [[ ! -e "$RECONCILE_PREPARATION_BUNDLE_DIR/candidate.patch" \
            && ! -L "$RECONCILE_PREPARATION_BUNDLE_DIR/candidate.patch" ]] \
            || reconcile_error "no-change preparation must not contain a patch"
        jq -e '.changed_files == [] and (has("patch_sha256") | not)' \
            "$preparation_manifest" >/dev/null \
            || reconcile_error "no-change preparation manifest is invalid"
    fi

    : "${RECONCILE_VALIDATION_OUTCOME_DIR:?RECONCILE_VALIDATION_OUTCOME_DIR must be set}"
    outcome="$RECONCILE_VALIDATION_OUTCOME_DIR/manifest.json"
    [[ -f "$outcome" && ! -L "$outcome" ]] \
        || reconcile_error "validation outcome must be a regular file"
    manifest_matches_identity "$outcome" \
        || reconcile_error "validation outcome identity or schema is invalid"
    jq -e --arg preparation_digest "$preparation_digest" \
        --arg expected_classification "$preparation_classification" '
        (.classification == $expected_classification or
         .classification == "branch-validation") and
        .candidate_manifest_sha256 == $preparation_digest
    ' "$outcome" >/dev/null \
        || reconcile_error "validation outcome does not match the candidate"
    outcome_digest=$(sha256_file "$outcome")
    outcome_classification=$(jq -er '.classification' "$outcome")

    if [[ "$outcome_classification" == "branch-validation" ]]; then
        [[ "$(git -C "$RECONCILE_TARGET_CHECKOUT" rev-parse HEAD 2>/dev/null)" \
            == "$RECONCILE_BASE_OID" ]] \
            || reconcile_error "target checkout HEAD does not match base OID"
        : "${RECONCILE_RUN_URL:?RECONCILE_RUN_URL must be set for failures}"
        local validation_failure
        validation_failure=$(bounded_failure_json "$outcome")
        write_verified_result "branch-validation" "$preparation_digest" "$outcome_digest" "" \
            "$validation_failure" "$RECONCILE_RUN_URL"
        return
    fi

    if [[ "$preparation_classification" == "success" ]]; then
        verify_declared_candidate "$preparation_manifest" "$patch" "$RECONCILE_TARGET_CHECKOUT"
        write_verified_result "success" "$preparation_digest" "$outcome_digest" "$patch_digest"
    else
        [[ "$(git -C "$RECONCILE_TARGET_CHECKOUT" rev-parse HEAD 2>/dev/null)" \
            == "$RECONCILE_BASE_OID" ]] \
            || reconcile_error "target checkout HEAD does not match base OID"
        [[ -z "$(git -C "$RECONCILE_TARGET_CHECKOUT" status --porcelain=v1 --untracked-files=all)" ]] \
            || reconcile_error "target checkout must be clean"
        write_verified_result "no-change" "$preparation_digest" "$outcome_digest"
    fi
}

construct_update_commit() {
    : "${RECONCILE_TARGET_CHECKOUT:?RECONCILE_TARGET_CHECKOUT must be set}"
    : "${RECONCILE_COMMIT_AUTHOR_NAME:?RECONCILE_COMMIT_AUTHOR_NAME must be set}"
    : "${RECONCILE_COMMIT_AUTHOR_EMAIL:?RECONCILE_COMMIT_AUTHOR_EMAIL must be set}"
    local checkout=$RECONCILE_TARGET_CHECKOUT
    [[ "$(git -C "$checkout" rev-parse HEAD 2>/dev/null)" == "$RECONCILE_BASE_OID" ]] \
        || reconcile_error "publication checkout HEAD does not match base OID"
    [[ -z "$(git -C "$checkout" status --porcelain=v1 --untracked-files=all)" ]] \
        || reconcile_error "publication checkout must be clean"

    local patch="$RECONCILE_VERIFIED_RESULT_DIR/candidate.patch"
    local verified_manifest="$RECONCILE_VERIFIED_RESULT_DIR/manifest.json"
    [[ -f "$patch" && ! -L "$patch" ]] \
        || reconcile_error "verified candidate patch must be a regular file"
    [[ "$(sha256_file "$patch")" == "$(jq -er '.patch_sha256' "$verified_manifest")" ]] \
        || reconcile_error "verified candidate patch digest is invalid"
    git -C "$checkout" apply --check --index --binary "$patch" >/dev/null \
        || reconcile_error "verified candidate patch does not apply to exact base"
    git -C "$checkout" apply --index --binary "$patch" >/dev/null \
        || reconcile_error "verified candidate patch could not be applied"

    git -C "$checkout" config --local user.name "$RECONCILE_COMMIT_AUTHOR_NAME"
    git -C "$checkout" config --local user.email "$RECONCILE_COMMIT_AUTHOR_EMAIL"

    local message_file="$checkout/.git/tf-version-bump-commit-message"
    printf '%s\n\n%s\n%s\n' \
        "chore: update Terraform for $RECONCILE_STATE_BRANCH" \
        "Tf-Version-Bump-Automation: $RECONCILE_AUTOMATION_POLICY_ID/$RECONCILE_REF_HASH" \
        "Tf-Version-Bump-Base: $RECONCILE_BASE_OID" >"$message_file"
    local commit_date
    commit_date=$(git -C "$checkout" show -s --format=%cI "$RECONCILE_BASE_OID")
    GIT_AUTHOR_DATE="$commit_date" GIT_COMMITTER_DATE="$commit_date" \
        git -C "$checkout" -c commit.gpgsign=false commit -F "$message_file" >/dev/null \
        || reconcile_error "could not construct update commit"
    rm -f -- "$message_file"
}

publish_update_ref() {
    : "${RECONCILE_GIT_REMOTE:?RECONCILE_GIT_REMOTE must be set}"
    local checkout=$RECONCILE_TARGET_CHECKOUT
    local state_ref="refs/heads/$RECONCILE_STATE_BRANCH"
    local update_ref="refs/heads/update_$RECONCILE_STATE_BRANCH"
    local temporary_prefix="refs/remotes/tf-version-bump/$RECONCILE_REF_HASH"
    local state_tracking_ref="$temporary_prefix/state"
    local update_tracking_ref="$temporary_prefix/update"
    git -C "$checkout" update-ref -d "$state_tracking_ref"
    git -C "$checkout" fetch --quiet --no-tags --no-write-fetch-head \
        "$RECONCILE_GIT_REMOTE" "+$state_ref:$state_tracking_ref" \
        || reconcile_error "could not fetch state ref before publication"
    [[ "$(git -C "$checkout" rev-parse "$state_tracking_ref")" == "$RECONCILE_BASE_OID" ]] \
        || reconcile_error "state ref moved after discovery"

    git -C "$checkout" update-ref -d "$update_tracking_ref"
    local observed_update_oid=0000000000000000000000000000000000000000
    local fetch_diagnostic="$checkout/.git/tf-version-bump-update-fetch"
    if git -C "$checkout" fetch --quiet --no-tags --no-write-fetch-head \
        "$RECONCILE_GIT_REMOTE" "+$update_ref:$update_tracking_ref" \
        2>"$fetch_diagnostic"; then
        observed_update_oid=$(git -C "$checkout" rev-parse "$update_tracking_ref")
        local ownership_trailer
        ownership_trailer="Tf-Version-Bump-Automation: $RECONCILE_AUTOMATION_POLICY_ID/$RECONCILE_REF_HASH"
        git -C "$checkout" show -s --format=%B "$observed_update_oid" \
            | grep -Fx -- "$ownership_trailer" >/dev/null \
            || reconcile_error "existing update ref is not owned by this automation policy"
    elif ! grep -F "couldn't find remote ref" "$fetch_diagnostic" >/dev/null; then
        rm -f -- "$fetch_diagnostic"
        reconcile_error "could not inspect existing update ref"
    fi
    rm -f -- "$fetch_diagnostic"

    git -C "$checkout" push --quiet \
        --force-with-lease="$update_ref:$observed_update_oid" \
        "$RECONCILE_GIT_REMOTE" "$(git -C "$checkout" rev-parse HEAD):$update_ref" \
        || reconcile_error "update ref push failed its exact lease"
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
    response=$(gh pr list --repo "$RECONCILE_REPOSITORY" --state open \
        --head "update_$RECONCILE_STATE_BRANCH" --base "$RECONCILE_STATE_BRANCH" \
        --json number,body)
    [[ -n "$response" ]] || response='[]'
    jq -r --arg marker "$marker" \
        '[.[] | select((.body // "") | contains($marker)) | .number] | first // empty' \
        <<<"$response"
}

marked_issue_record() {
    local marker=$1 response
    response=$(gh issue list --repo "$RECONCILE_REPOSITORY" --state all \
        --search "$RECONCILE_REF_HASH in:body" --limit 100 --json number,body,closed)
    [[ -n "$response" ]] || response='[]'
    jq -r --arg marker "$marker" \
        '[.[] | select((.body // "") | contains($marker))] | first // {} |
         [(.number // ""), (.closed // "")] | @tsv' \
        <<<"$response"
}

reconcile_success_lifecycle() {
    local marker payload_dir body_file pr_number issue_number issue_closed
    local branch_html base_html policy_html
    marker=$(github_marker)
    branch_html=$(html_code "$RECONCILE_STATE_BRANCH")
    base_html=$(html_code "$RECONCILE_BASE_OID")
    policy_html=$(html_code "$RECONCILE_AUTOMATION_POLICY_ID")
    : "${RUNNER_TEMP:?RUNNER_TEMP must be set}"
    payload_dir=$(mktemp -d "$RUNNER_TEMP/tf-version-bump-github.XXXXXX")
    RECONCILE_TEMPORARY_PATH=$payload_dir
    body_file="$payload_dir/body"
    printf '%s\n\nAutomated Terraform dependency update for %s.\n\nBase: %s\nPolicy: %s\n' \
        "$marker" "$branch_html" "$base_html" "$policy_html" >"$body_file"
    pr_number=$(marked_pr_number "$marker")
    if [[ -n "$pr_number" ]]; then
        gh pr edit "$pr_number" --repo "$RECONCILE_REPOSITORY" \
            --title "Terraform dependency update" --body-file "$body_file" >/dev/null
    else
        gh pr create --repo "$RECONCILE_REPOSITORY" \
            --head "update_$RECONCILE_STATE_BRANCH" --base "$RECONCILE_STATE_BRANCH" \
            --title "Terraform dependency update" --body-file "$body_file" >/dev/null
    fi
    read -r issue_number issue_closed < <(marked_issue_record "$marker")
    [[ -z "$issue_number" || "$issue_closed" == "true" ]] \
        || gh issue close "$issue_number" --repo "$RECONCILE_REPOSITORY" >/dev/null
    rm -rf -- "$payload_dir"
    RECONCILE_TEMPORARY_PATH=""
}

reconcile_failure_lifecycle() {
    local classification=$1 verified_manifest=$2
    local marker payload_dir body_file issue_number issue_closed
    local branch_html classification_html base_html run_id_html run_attempt_html
    local stage root status run_url stage_html root_html status_html run_url_html run_url_href
    marker=$(github_marker)
    branch_html=$(html_code "$RECONCILE_STATE_BRANCH")
    classification_html=$(html_code "$classification")
    base_html=$(html_code "$RECONCILE_BASE_OID")
    run_id_html=$(html_code "$RECONCILE_RUN_ID")
    run_attempt_html=$(html_code "$RECONCILE_RUN_ATTEMPT")
    stage=$(jq -er '.failure.stage' "$verified_manifest")
    root=$(jq -er '.failure.root' "$verified_manifest")
    status=$(jq -er '.failure.status | select(type == "number")' "$verified_manifest")
    run_url=$(jq -er '.run_url | select(type == "string" and length > 0)' "$verified_manifest")
    stage_html=$(html_code "$stage")
    root_html=$(html_code "$root")
    status_html=$(html_code "$status")
    run_url_html=$(html_code "$run_url")
    run_url_href=$(html_escape "$run_url")
    : "${RUNNER_TEMP:?RUNNER_TEMP must be set}"
    payload_dir=$(mktemp -d "$RUNNER_TEMP/tf-version-bump-github.XXXXXX")
    RECONCILE_TEMPORARY_PATH=$payload_dir
    body_file="$payload_dir/body"
    printf '%s\n\nAutomated Terraform update failed for %s.\n\nClassification: %s\nStage: %s\nRoot: %s\nStatus: %s\nBase: %s\nRun: %s attempt %s\nWorkflow run: <a href="%s">%s</a>\n' \
        "$marker" "$branch_html" "$classification_html" \
        "$stage_html" "$root_html" "$status_html" "$base_html" \
        "$run_id_html" "$run_attempt_html" "$run_url_href" "$run_url_html" >"$body_file"
    read -r issue_number issue_closed < <(marked_issue_record "$marker")
    if [[ -n "$issue_number" ]]; then
        [[ "$issue_closed" != "true" ]] || gh issue reopen "$issue_number" --repo "$RECONCILE_REPOSITORY" >/dev/null
        gh issue edit "$issue_number" --repo "$RECONCILE_REPOSITORY" \
            --title "Terraform dependency update failed" --body-file "$body_file" >/dev/null
    else
        gh issue create --repo "$RECONCILE_REPOSITORY" \
            --title "Terraform dependency update failed" --body-file "$body_file" >/dev/null
    fi
    rm -rf -- "$payload_dir"
    RECONCILE_TEMPORARY_PATH=""
}

publish_result() {
    require_common_identity
    : "${RECONCILE_VERIFIED_RESULT_DIR:?RECONCILE_VERIFIED_RESULT_DIR must be set}"
    local verified_manifest="$RECONCILE_VERIFIED_RESULT_DIR/manifest.json"
    [[ -f "$verified_manifest" && ! -L "$verified_manifest" ]] \
        || reconcile_error "verified result manifest must be a regular file"
    manifest_matches_identity "$verified_manifest" \
        || reconcile_error "verified result identity or schema is invalid"
    local classification
    classification=$(jq -er '.classification' "$verified_manifest")
    [[ "$classification" != "no-change" ]] || return 0
    if [[ "$classification" == "success" ]]; then
        : "${RECONCILE_DRY_RUN:?RECONCILE_DRY_RUN must be set}"
        [[ "$RECONCILE_DRY_RUN" == "true" || "$RECONCILE_DRY_RUN" == "false" ]] \
            || reconcile_error "dry-run must be true or false"
        construct_update_commit
        [[ "$RECONCILE_DRY_RUN" == "true" ]] && return
        publish_update_ref
        : "${RECONCILE_REPOSITORY:?RECONCILE_REPOSITORY must be set}"
        : "${GH_TOKEN:?GH_TOKEN must be set}"
        reconcile_success_lifecycle
        return
    fi
    [[ "$classification" == "branch-update" || "$classification" == "branch-init" \
        || "$classification" == "branch-validation" ]] \
        || reconcile_error "verified result classification is not supported"
    : "${RECONCILE_DRY_RUN:?RECONCILE_DRY_RUN must be set}"
    [[ "$RECONCILE_DRY_RUN" == "true" || "$RECONCILE_DRY_RUN" == "false" ]] \
        || reconcile_error "dry-run must be true or false"
    [[ "$RECONCILE_DRY_RUN" == "true" ]] && return
    : "${RECONCILE_REPOSITORY:?RECONCILE_REPOSITORY must be set}"
    : "${GH_TOKEN:?GH_TOKEN must be set}"
    reconcile_failure_lifecycle "$classification" "$verified_manifest"
}

if [[ "${1:-}" == "--help" ]]; then
    usage
    exit 0
fi
if [[ "${1:-}" == "verify" && $# -eq 1 ]]; then
    trap cleanup_reconcile_temporaries EXIT
    verify_result
    exit 0
fi
if [[ "${1:-}" == "publish" && $# -eq 1 ]]; then
    trap cleanup_reconcile_temporaries EXIT
    publish_result
    exit 0
fi

usage >&2
exit 2
