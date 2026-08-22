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

directory_has_exact_entries() {
    local directory=$1
    shift
    local -a actual=() expected=("$@")
    local entry
    while IFS= read -r -d '' entry; do
        actual+=("${entry##*/}")
    done < <(find "$directory" -mindepth 1 -maxdepth 1 -print0)
    local actual_json expected_json
    actual_json=$(jq -cn '$ARGS.positional | sort' --args -- "${actual[@]}")
    expected_json=$(jq -cn '$ARGS.positional | sort' --args -- "${expected[@]}")
    [[ "$actual_json" == "$expected_json" ]]
}

logs_are_bounded_regular_files() {
    local directory=$1 entry
    [[ -d "$directory" && ! -L "$directory" ]] || return 1
    while IFS= read -r -d '' entry; do
        [[ -f "$entry" && ! -L "$entry" ]] || return 1
    done < <(find "$directory" -mindepth 1 -maxdepth 1 -print0)
}

preparation_manifest_is_valid() {
    jq -e '
        def changed_files:
            type == "array" and
            all(.[]; keys == ["mode", "path", "sha256"] and
                (.path | type == "string" and length > 0) and
                .mode == "100644" and
                (.sha256 | type == "string" and test("^[0-9a-f]{64}$"))) and
            ([.[].path] == ([.[].path] | sort)) and
            (([.[].path] | unique | length) == length);
        def identity:
            .schema_version == 2 and
            .run_id == env.RECONCILE_RUN_ID and
            .run_attempt == env.RECONCILE_RUN_ATTEMPT and
            .automation_policy_id == env.RECONCILE_AUTOMATION_POLICY_ID and
            .control_oid == env.RECONCILE_CONTROL_OID and
            .state_branch == env.RECONCILE_STATE_BRANCH and
            .base_oid == env.RECONCILE_BASE_OID and
            .ref_hash == env.RECONCILE_REF_HASH and
            .artifact_name == ("preparation-" + env.RECONCILE_RUN_ID + "-" +
                env.RECONCILE_RUN_ATTEMPT + "-" + env.RECONCILE_AUTOMATION_POLICY_ID +
                "-" + env.RECONCILE_REF_HASH);
        identity and
        if .classification == "success" or .classification == "no-change" then
            keys == ["artifact_name", "automation_policy_id", "base_oid", "classification",
                     "config_path", "control_oid", "final_changed_files", "formatting",
                     "ref_hash", "roots", "run_attempt", "run_id", "schema_version",
                     "state_branch", "terraform_fmt", "tools", "updates"] and
            (.terraform_fmt | type == "boolean") and
            (.tools | keys) == ["terraform", "tf_version_bump"] and
            (.tools.terraform | keys) == ["version"] and
            (.tools.tf_version_bump | keys) == ["archive_sha256", "version"] and
            (.tools.terraform.version | type == "string" and length > 0) and
            (.tools.tf_version_bump.version | type == "string" and length > 0) and
            (.tools.tf_version_bump.archive_sha256 | type == "string" and
                test("^[0-9a-f]{64}$")) and
            (.config_path | type == "string" and length > 0) and
            (.roots | type == "array" and length > 0 and
                all(.[]; keys == ["path"] and (.path | type == "string" and length > 0))) and
            (.updates.module_blocks_updated | type == "number" and . >= 0 and floor == .) and
            (.updates.provider_blocks_updated | type == "number" and . >= 0 and floor == .) and
            (.updates.changed_files | changed_files) and
            (.formatting.ran | type == "boolean") and
            (.formatting.changed_files | changed_files) and
            (.final_changed_files | changed_files) and
            (.formatting.ran == false or .terraform_fmt == true) and
            if .classification == "success" then
                (.updates | keys) == ["changed_files", "module_blocks_updated", "patch_sha256",
                                     "provider_blocks_updated"] and
                (.updates.changed_files | length > 0) and
                (.updates.patch_sha256 | type == "string" and test("^[0-9a-f]{64}$")) and
                (.final_changed_files | length > 0) and
                (.formatting.ran == .terraform_fmt) and
                if (.formatting | has("patch_sha256")) then
                    (.formatting | keys) == ["changed_files", "patch_sha256", "ran"] and
                    .formatting.ran == true and
                    (.formatting.changed_files | length > 0) and
                    (.formatting.patch_sha256 | type == "string" and
                        test("^[0-9a-f]{64}$"))
                else
                    (.formatting | keys) == ["changed_files", "ran"] and
                    .formatting.changed_files == []
                end
            else
                (.updates | keys) == ["changed_files", "module_blocks_updated",
                                     "provider_blocks_updated"] and
                .updates.changed_files == [] and
                (.formatting | keys) == ["changed_files", "ran"] and
                .formatting.changed_files == [] and .final_changed_files == []
            end
        elif .classification == "branch-update" or .classification == "branch-init" or
             .classification == "branch-format" or .classification == "automation" then
            keys == ["artifact_name", "automation_policy_id", "base_oid", "classification",
                     "control_oid", "failure", "ref_hash", "run_attempt", "run_id",
                     "schema_version", "state_branch"] and
            (.failure | keys) == ["command", "root", "stage", "status"] and
            (.failure.stage | type == "string" and length > 0) and
            (.failure.root | type == "string") and
            (.failure.command | type == "string" and length > 0) and
            (.failure.status | type == "number" and . > 0 and floor == .)
        else false end
    ' "$1" >/dev/null
}

validation_outcome_is_valid() {
    local manifest=$1 expected_classification=$2 preparation_digest=$3
    jq -e --arg expected_classification "$expected_classification" \
        --arg preparation_digest "$preparation_digest" '
        .schema_version == 2 and
        .run_id == env.RECONCILE_RUN_ID and
        .run_attempt == env.RECONCILE_RUN_ATTEMPT and
        .automation_policy_id == env.RECONCILE_AUTOMATION_POLICY_ID and
        .control_oid == env.RECONCILE_CONTROL_OID and
        .state_branch == env.RECONCILE_STATE_BRANCH and
        .base_oid == env.RECONCILE_BASE_OID and
        .ref_hash == env.RECONCILE_REF_HASH and
        .candidate_manifest_sha256 == $preparation_digest and
        (.classification == $expected_classification or
         .classification == "branch-validation") and
        if .classification == "branch-validation" then
            keys == ["automation_policy_id", "base_oid", "candidate_manifest_sha256",
                     "classification", "command_status", "control_oid", "failure", "ref_hash",
                     "run_attempt", "run_id", "schema_version", "state_branch"] and
            (.command_status | type == "number" and . > 0 and floor == .) and
            (.failure | keys) == ["root", "stage", "status"] and
            (.failure.stage | type == "string" and length > 0) and
            (.failure.root | type == "string") and
            .failure.status == .command_status
        else
            keys == ["automation_policy_id", "base_oid", "candidate_manifest_sha256",
                     "classification", "command_status", "control_oid", "ref_hash",
                     "run_attempt", "run_id", "schema_version", "state_branch"] and
            .command_status == 0
        end
    ' "$manifest" >/dev/null
}

# shellcheck disable=SC2329 # Invoked by name as a stage-specific path policy.
path_is_declared_direct_file() {
    local manifest=$1 relative_path=$2
    [[ -n "$relative_path" && "$relative_path" != /* ]] || return 1
    # A literal newline in relative_path can never reach here intact: it is read one line at a
    # time by the newline-splitting `read` loop in verify_declared_stage_paths, which would already
    # have split it into separate (non-matching) lines. The real defence against a smuggled
    # newline-bearing path is the byte-exact sorted actual/declared path-set comparison in
    # verify_declared_stage.
    [[ "/$relative_path/" != *"/../"* ]] || return 1

    local filename=${relative_path##*/}
    local parent=${relative_path%/*}
    [[ "$parent" != "$relative_path" ]] || parent="."
    [[ "$filename" == *.tf || "$filename" == ".terraform.lock.hcl" ]] || return 1
    jq -e --arg parent "$parent" '.roots | any(.path == $parent)' "$manifest" >/dev/null
}

# shellcheck disable=SC2329 # Invoked by name as a stage-specific path policy.
path_is_declared_formatting_file() {
    local manifest=$1 relative_path=$2 root
    [[ -n "$relative_path" && "$relative_path" != /* && "$relative_path" == *.tf ]] \
        || return 1
    [[ "/$relative_path/" != *"/../"* && "/$relative_path/" != *"/.terraform/"* ]] \
        || return 1
    while IFS= read -r root; do
        if [[ "$root" == "." || "$relative_path" == "$root/"* ]]; then
            return 0
        fi
    done < <(jq -er '.roots[].path' "$manifest")
    return 1
}

# shellcheck disable=SC2329 # Invoked by name as a stage-specific path policy.
path_is_declared_final_file() {
    local manifest=$1 relative_path=$2
    if jq -e --arg path "$relative_path" \
        '.updates.changed_files | any(.path == $path)' "$manifest" >/dev/null; then
        path_is_declared_direct_file "$manifest" "$relative_path"
        return
    fi
    jq -e --arg path "$relative_path" \
        '.formatting.changed_files | any(.path == $path)' "$manifest" >/dev/null \
        && path_is_declared_formatting_file "$manifest" "$relative_path"
}

verify_declared_stage_paths() {
    local manifest=$1 stage=$2 path_policy=$3 relative_path
    while IFS= read -r relative_path; do
        "$path_policy" "$manifest" "$relative_path" \
            || reconcile_error "candidate contains an undeclared or unsafe path"
    done < <(jq -er --arg stage "$stage" \
        'if $stage == "final" then .final_changed_files[].path
         else .[$stage].changed_files[].path end' "$manifest")
}

VERIFIED_STAGE_TREE=""

verify_declared_stage() {
    local manifest=$1 checkout=$2 source_tree=$3 stage=$4 path_policy=$5
    local target_tree relative_path canonical_checkout
    canonical_checkout=$(realpath "$checkout") \
        || reconcile_error "candidate checkout could not be resolved"
    target_tree=$(git -C "$checkout" write-tree) \
        || reconcile_error "candidate index could not be read"

    local -a actual_paths=() actual_modes=()
    local raw_entry mode
    while IFS= read -r -d '' raw_entry && IFS= read -r -d '' relative_path; do
        read -r _ mode _ _ _ <<<"$raw_entry"
        "$path_policy" "$manifest" "$relative_path" \
            || reconcile_error "candidate contains an undeclared or unsafe path"
        local changed_path resolved_path
        changed_path="$checkout/$relative_path"
        [[ -e "$changed_path" || -L "$changed_path" ]] \
            || reconcile_error "candidate file digest does not match the manifest"
        [[ -f "$changed_path" && ! -L "$changed_path" ]] \
            || reconcile_error "candidate changed path must be a regular file"
        resolved_path=$(realpath "$changed_path" 2>/dev/null) \
            || reconcile_error "candidate changed path could not be resolved"
        [[ "$resolved_path" == "$canonical_checkout/"* ]] \
            || reconcile_error "candidate changed path resolves outside the checkout"
        actual_paths+=("$relative_path")
        actual_modes+=("$mode")
    done < <(git -C "$checkout" diff --raw -z --no-renames "$source_tree" "$target_tree")

    local raw_paths_digest normalised_paths_digest
    raw_paths_digest=$(printf '%s\0' "${actual_paths[@]}" | sha256sum)
    normalised_paths_digest=$(printf '%s\0' "${actual_paths[@]}" | jq -Rjsc . | sha256sum)
    [[ "${raw_paths_digest%% *}" == "${normalised_paths_digest%% *}" ]] \
        || reconcile_error "candidate patch path is not valid UTF-8"

    local actual_paths_json declared_paths_json
    actual_paths_json=$(jq -cn '$ARGS.positional | sort' --args -- "${actual_paths[@]}")
    declared_paths_json=$(jq -c --arg stage "$stage" '
        if $stage == "final" then [.final_changed_files[].path] | sort
        else [.[$stage].changed_files[].path] | sort end' "$manifest")
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

    local -A expected_mode_by_path=() expected_sha256_by_path=()
    local declared_path declared_mode declared_sha256
    while IFS= read -r -d '' declared_path \
        && IFS= read -r -d '' declared_mode \
        && IFS= read -r -d '' declared_sha256; do
        expected_mode_by_path["$declared_path"]=$declared_mode
        expected_sha256_by_path["$declared_path"]=$declared_sha256
    done < <(jq -j --arg stage "$stage" '
        ([0] | implode) as $nul |
        (if $stage == "final" then .final_changed_files else .[$stage].changed_files end)[] |
        (.path, $nul, .mode, $nul, .sha256, $nul)' "$manifest")

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
    VERIFIED_STAGE_TREE=$target_tree
}

verify_applied_candidate() {
    local manifest=$1 checkout=$2
    local head base_tree update_patch format_patch
    head=$(git -C "$checkout" rev-parse HEAD 2>/dev/null) \
        || reconcile_error "target checkout is not a Git worktree"
    [[ "$head" == "$RECONCILE_BASE_OID" ]] \
        || reconcile_error "target checkout HEAD does not match base OID"
    git -C "$checkout" diff --quiet --no-ext-diff \
        || reconcile_error "target checkout changed after validation"
    [[ -z "$(git -C "$checkout" ls-files --others --exclude-standard)" ]] \
        || reconcile_error "target checkout changed after validation"
    [[ -z "$(git -C "$checkout" ls-files --others --ignored --exclude-standard)" ]] \
        || reconcile_error "target checkout changed after validation"
    base_tree=$(git -C "$checkout" rev-parse 'HEAD^{tree}')
    verify_declared_stage "$manifest" "$checkout" "$base_tree" final \
        path_is_declared_final_file

    git -C "$checkout" reset --hard "$RECONCILE_BASE_OID" >/dev/null \
        || reconcile_error "target checkout could not be reset to exact base"
    [[ -z "$(git -C "$checkout" status --porcelain=v1 --untracked-files=all \
        --ignored=matching)" ]] \
        || reconcile_error "target checkout did not reset to exact base"

    update_patch="$RECONCILE_PREPARATION_BUNDLE_DIR/update.patch"
    verify_declared_stage_paths "$manifest" updates path_is_declared_direct_file
    git -C "$checkout" apply --check --index --binary "$update_patch" >/dev/null \
        || reconcile_error "candidate patch does not apply to exact base"
    git -C "$checkout" apply --index --binary "$update_patch" >/dev/null \
        || reconcile_error "candidate patch could not be applied"
    verify_declared_stage "$manifest" "$checkout" "$base_tree" updates \
        path_is_declared_direct_file
    local update_tree=$VERIFIED_STAGE_TREE

    if jq -e '.formatting | has("patch_sha256")' "$manifest" >/dev/null; then
        format_patch="$RECONCILE_PREPARATION_BUNDLE_DIR/format.patch"
        verify_declared_stage_paths "$manifest" formatting path_is_declared_formatting_file
        git -C "$checkout" update-index --refresh >/dev/null
        git -C "$checkout" apply --check --index --binary "$format_patch" >/dev/null \
            || reconcile_error "format patch does not apply to the verified update stage"
        git -C "$checkout" apply --index --binary "$format_patch" >/dev/null \
            || reconcile_error "format patch could not be applied"
        verify_declared_stage "$manifest" "$checkout" "$update_tree" formatting \
            path_is_declared_formatting_file
    fi
    verify_declared_stage "$manifest" "$checkout" "$base_tree" final \
        path_is_declared_final_file
}

bounded_failure_json() {
    jq -ce '.failure |
        select((.stage | type) == "string" and (.root | type) == "string" and
               (.status | type) == "number") |
        {stage, root, status}' "$1" \
        || reconcile_error "failure details are invalid"
}

write_verified_result() {
    local classification=$1 preparation_digest=$2 outcome_digest=${3-}
    local failure_json=${4-null} run_url=${5-}
    [[ -n "$preparation_digest" ]] \
        || reconcile_error "verified result requires a preparation manifest digest"
    if [[ "$classification" == "success" || "$classification" == "no-change" \
        || "$classification" == "branch-validation" ]]; then
        [[ -n "$outcome_digest" ]] \
            || reconcile_error "verified result requires a validation outcome digest"
    else
        [[ -z "$outcome_digest" ]] \
            || reconcile_error "preparation failure must not bind a validation outcome"
    fi
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
        --argjson failure "$failure_json" \
        --arg run_url "$run_url" \
        --slurpfile preparation "$RECONCILE_PREPARATION_BUNDLE_DIR/manifest.json" '
        {schema_version: 2, run_id: $run_id, run_attempt: $run_attempt,
         automation_policy_id: $policy, control_oid: $control_oid,
         state_branch: $branch, base_oid: $base_oid, ref_hash: $ref_hash,
         classification: $classification,
         preparation_manifest_sha256: $preparation_digest}
        + if $outcome_digest == "" then {} else
            {validation_outcome_sha256: $outcome_digest} end
        + if $classification == "success" or $classification == "no-change" then
            {terraform_fmt: $preparation[0].terraform_fmt,
             updates: $preparation[0].updates,
             formatting: $preparation[0].formatting,
             final_changed_files: $preparation[0].final_changed_files}
          else {} end
        + if $failure == null then {} else
            {failure: $failure, run_url: $run_url} end
        ' >"$stage/manifest.json"
    if [[ "$classification" == "success" ]]; then
        cp -- "$RECONCILE_PREPARATION_BUNDLE_DIR/update.patch" "$stage/update.patch"
        if jq -e '.formatting | has("patch_sha256")' \
            "$RECONCILE_PREPARATION_BUNDLE_DIR/manifest.json" >/dev/null; then
            cp -- "$RECONCILE_PREPARATION_BUNDLE_DIR/format.patch" "$stage/format.patch"
        fi
    fi
    chmod 444 "$stage"/*
    mv -- "$stage" "$destination"
    RECONCILE_TEMPORARY_PATH=""
    chmod 555 "$destination"
}

verify_clean_checkout() {
    local checkout=$1 expected_oid=$2 description=$3
    [[ -d "$checkout" ]] || reconcile_error "$description must be a directory"
    [[ "$(git -C "$checkout" rev-parse HEAD 2>/dev/null)" == "$expected_oid" ]] \
        || reconcile_error "$description HEAD does not match its immutable OID"
    [[ -z "$(git -C "$checkout" status --porcelain=v1 --untracked-files=all \
        --ignored=matching)" ]] \
        || reconcile_error "$description must be clean"
}

verify_preparation_entries() {
    local classification=$1 manifest=$2
    logs_are_bounded_regular_files "$RECONCILE_PREPARATION_BUNDLE_DIR/logs" \
        || reconcile_error "preparation logs are invalid"
    if [[ "$classification" == "success" ]]; then
        if jq -e '.formatting | has("patch_sha256")' "$manifest" >/dev/null; then
            directory_has_exact_entries "$RECONCILE_PREPARATION_BUNDLE_DIR" \
                format.patch logs manifest.json update.patch \
                || reconcile_error "preparation bundle contains unexpected entries"
        else
            directory_has_exact_entries "$RECONCILE_PREPARATION_BUNDLE_DIR" \
                logs manifest.json update.patch \
                || reconcile_error "preparation bundle contains unexpected entries"
        fi
    else
        directory_has_exact_entries "$RECONCILE_PREPARATION_BUNDLE_DIR" logs manifest.json \
            || reconcile_error "preparation bundle contains unexpected entries"
    fi
}

verify_result() {
    require_common_identity
    : "${RECONCILE_CONTROL_CHECKOUT:?RECONCILE_CONTROL_CHECKOUT must be set}"
    : "${RECONCILE_PREPARATION_BUNDLE_DIR:?RECONCILE_PREPARATION_BUNDLE_DIR must be set}"
    : "${RECONCILE_TARGET_CHECKOUT:?RECONCILE_TARGET_CHECKOUT must be set}"
    : "${RECONCILE_VERIFIED_RESULT_DIR:?RECONCILE_VERIFIED_RESULT_DIR must be set}"

    verify_clean_checkout "$RECONCILE_CONTROL_CHECKOUT" "$RECONCILE_CONTROL_OID" \
        "control checkout"

    local preparation_manifest="$RECONCILE_PREPARATION_BUNDLE_DIR/manifest.json"
    [[ -f "$preparation_manifest" && ! -L "$preparation_manifest" ]] \
        || reconcile_error "preparation manifest must be a regular file"
    preparation_manifest_is_valid "$preparation_manifest" \
        || reconcile_error "preparation manifest identity or schema is invalid"
    local preparation_classification preparation_digest
    preparation_classification=$(jq -er '.classification' "$preparation_manifest")
    preparation_digest=$(sha256_file "$preparation_manifest")
    if [[ "$preparation_classification" == "branch-update" \
        || "$preparation_classification" == "branch-init" \
        || "$preparation_classification" == "branch-format" \
        || "$preparation_classification" == "automation" ]]; then
        verify_preparation_entries "$preparation_classification" "$preparation_manifest"
        [[ -z "${RECONCILE_VALIDATION_OUTCOME_DIR:-}" \
            || ! -e "$RECONCILE_VALIDATION_OUTCOME_DIR" ]] \
            || reconcile_error "preparation failure must not have a validation outcome"
        verify_clean_checkout "$RECONCILE_TARGET_CHECKOUT" "$RECONCILE_BASE_OID" \
            "target checkout"
        : "${RECONCILE_RUN_URL:?RECONCILE_RUN_URL must be set for failures}"
        local preparation_failure
        preparation_failure=$(bounded_failure_json "$preparation_manifest")
        write_verified_result "$preparation_classification" "$preparation_digest" "" \
            "$preparation_failure" "$RECONCILE_RUN_URL"
        return
    fi
    [[ "$preparation_classification" == "success" \
        || "$preparation_classification" == "no-change" ]] \
        || reconcile_error "preparation classification is not supported"
    verify_preparation_entries "$preparation_classification" "$preparation_manifest"

    local patch="" patch_digest="" expected_patch_digest
    local format_patch="" format_patch_digest="" expected_format_patch_digest
    local outcome outcome_digest outcome_classification
    if [[ "$preparation_classification" == "success" ]]; then
        patch="$RECONCILE_PREPARATION_BUNDLE_DIR/update.patch"
        [[ -f "$patch" && ! -L "$patch" ]] \
            || reconcile_error "candidate patch must be a regular file"
        patch_digest=$(sha256_file "$patch")
        expected_patch_digest=$(jq -er '.updates.patch_sha256' "$preparation_manifest")
        [[ "$patch_digest" == "$expected_patch_digest" ]] \
            || reconcile_error "candidate patch digest does not match the manifest"
        if jq -e '.formatting | has("patch_sha256")' "$preparation_manifest" >/dev/null; then
            format_patch="$RECONCILE_PREPARATION_BUNDLE_DIR/format.patch"
            [[ -f "$format_patch" && ! -L "$format_patch" ]] \
                || reconcile_error "format patch must be a regular file"
            format_patch_digest=$(sha256_file "$format_patch")
            expected_format_patch_digest=$(jq -er '.formatting.patch_sha256' \
                "$preparation_manifest")
            [[ "$format_patch_digest" == "$expected_format_patch_digest" ]] \
                || reconcile_error "format patch digest does not match the manifest"
        fi
    else
        patch=""
    fi

    : "${RECONCILE_VALIDATION_OUTCOME_DIR:?RECONCILE_VALIDATION_OUTCOME_DIR must be set}"
    outcome="$RECONCILE_VALIDATION_OUTCOME_DIR/manifest.json"
    [[ -f "$outcome" && ! -L "$outcome" ]] \
        || reconcile_error "validation outcome must be a regular file"
    directory_has_exact_entries "$RECONCILE_VALIDATION_OUTCOME_DIR" logs manifest.json \
        || reconcile_error "validation outcome contains unexpected entries"
    logs_are_bounded_regular_files "$RECONCILE_VALIDATION_OUTCOME_DIR/logs" \
        || reconcile_error "validation logs are invalid"
    validation_outcome_is_valid "$outcome" "$preparation_classification" \
        "$preparation_digest" \
        || reconcile_error "validation outcome does not match the candidate"
    outcome_digest=$(sha256_file "$outcome")
    outcome_classification=$(jq -er '.classification' "$outcome")

    if [[ "$preparation_classification" == "success" ]]; then
        verify_applied_candidate "$preparation_manifest" "$RECONCILE_TARGET_CHECKOUT"
    else
        verify_clean_checkout "$RECONCILE_TARGET_CHECKOUT" "$RECONCILE_BASE_OID" \
            "target checkout"
    fi

    if [[ "$outcome_classification" == "branch-validation" ]]; then
        : "${RECONCILE_RUN_URL:?RECONCILE_RUN_URL must be set for failures}"
        local validation_failure
        validation_failure=$(bounded_failure_json "$outcome")
        write_verified_result "branch-validation" "$preparation_digest" "$outcome_digest" \
            "$validation_failure" "$RECONCILE_RUN_URL"
        return
    fi

    if [[ "$preparation_classification" == "success" ]]; then
        write_verified_result "success" "$preparation_digest" "$outcome_digest"
    else
        write_verified_result "no-change" "$preparation_digest" "$outcome_digest"
    fi
}

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

# shellcheck disable=SC2329 # Invoked by name as a verified-result path policy.
path_is_verified_file() {
    local _manifest=$1 relative_path=$2 filename
    [[ -n "$relative_path" && "$relative_path" != /* \
        && "/$relative_path/" != *"/../"* ]] || return 1
    filename=${relative_path##*/}
    [[ "$filename" == *.tf || "$filename" == ".terraform.lock.hcl" ]]
}

commit_staged_change() {
    local checkout=$1 subject=$2 commit_date=$3
    local message_file="$checkout/.git/tf-version-bump-commit-message"
    printf '%s\n\n%s\n%s\n' \
        "$subject" \
        "Tf-Version-Bump-Automation: $RECONCILE_AUTOMATION_POLICY_ID/$RECONCILE_REF_HASH" \
        "Tf-Version-Bump-Base: $RECONCILE_BASE_OID" >"$message_file"
    GIT_AUTHOR_DATE="$commit_date" GIT_COMMITTER_DATE="$commit_date" \
        git -C "$checkout" -c commit.gpgsign=false commit -F "$message_file" >/dev/null \
        || reconcile_error "could not construct Terraform update commit"
    rm -f -- "$message_file"
}

construct_update_commits() {
    : "${RECONCILE_TARGET_CHECKOUT:?RECONCILE_TARGET_CHECKOUT must be set}"
    : "${RECONCILE_COMMIT_AUTHOR_NAME:?RECONCILE_COMMIT_AUTHOR_NAME must be set}"
    : "${RECONCILE_COMMIT_AUTHOR_EMAIL:?RECONCILE_COMMIT_AUTHOR_EMAIL must be set}"
    local checkout=$RECONCILE_TARGET_CHECKOUT
    [[ "$(git -C "$checkout" rev-parse HEAD 2>/dev/null)" == "$RECONCILE_BASE_OID" ]] \
        || reconcile_error "publication checkout HEAD does not match base OID"
    [[ -z "$(git -C "$checkout" status --porcelain=v1 --untracked-files=all \
        --ignored=matching)" ]] \
        || reconcile_error "publication checkout must be clean"

    local update_patch="$RECONCILE_VERIFIED_RESULT_DIR/update.patch"
    local format_patch="$RECONCILE_VERIFIED_RESULT_DIR/format.patch"
    local verified_manifest="$RECONCILE_VERIFIED_RESULT_DIR/manifest.json"
    [[ -f "$update_patch" && ! -L "$update_patch" ]] \
        || reconcile_error "verified candidate patch must be a regular file"
    [[ "$(sha256_file "$update_patch")" \
        == "$(jq -er '.updates.patch_sha256' "$verified_manifest")" ]] \
        || reconcile_error "verified candidate patch digest is invalid"
    local base_tree
    base_tree=$(git -C "$checkout" rev-parse 'HEAD^{tree}')
    git -C "$checkout" apply --check --index --binary "$update_patch" >/dev/null \
        || reconcile_error "verified candidate patch does not apply to exact base"
    git -C "$checkout" apply --index --binary "$update_patch" >/dev/null \
        || reconcile_error "verified candidate patch could not be applied"
    verify_declared_stage "$verified_manifest" "$checkout" "$base_tree" updates \
        path_is_verified_file

    git -C "$checkout" config --local user.name "$RECONCILE_COMMIT_AUTHOR_NAME"
    git -C "$checkout" config --local user.email "$RECONCILE_COMMIT_AUTHOR_EMAIL"
    local commit_date subject
    commit_date=$(git -C "$checkout" show -s --format=%cI "$RECONCILE_BASE_OID")
    subject=$(update_commit_subject "$verified_manifest")
    commit_staged_change "$checkout" "$subject" "$commit_date"

    if [[ -e "$format_patch" || -L "$format_patch" ]]; then
        [[ -f "$format_patch" && ! -L "$format_patch" ]] \
            || reconcile_error "verified format patch must be a regular file"
        [[ "$(sha256_file "$format_patch")" \
            == "$(jq -er '.formatting.patch_sha256' "$verified_manifest")" ]] \
            || reconcile_error "verified format patch digest is invalid"
        local update_tree
        update_tree=$(git -C "$checkout" rev-parse 'HEAD^{tree}')
        git -C "$checkout" apply --check --index --binary "$format_patch" >/dev/null \
            || reconcile_error "verified format patch does not apply to the update commit"
        git -C "$checkout" apply --index --binary "$format_patch" >/dev/null \
            || reconcile_error "verified format patch could not be applied"
        verify_declared_stage "$verified_manifest" "$checkout" "$update_tree" formatting \
            path_is_verified_file
        commit_staged_change "$checkout" 'chore: run Terraform fmt' "$commit_date"
    fi

    verify_declared_stage "$verified_manifest" "$checkout" "$base_tree" final \
        path_is_verified_file
    [[ -z "$(git -C "$checkout" status --porcelain=v1 --untracked-files=all \
        --ignored=matching)" ]] \
        || reconcile_error "constructed Terraform commits left a dirty checkout"
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

verified_manifest_is_valid() {
    jq -e '
        def digest: type == "string" and test("^[0-9a-f]{64}$");
        def changed_files:
            type == "array" and
            all(.[]; keys == ["mode", "path", "sha256"] and
                (.path | type == "string" and length > 0) and .mode == "100644" and
                (.sha256 | digest)) and
            ([.[].path] == ([.[].path] | sort)) and
            (([.[].path] | unique | length) == length);
        .schema_version == 2 and
        .run_id == env.RECONCILE_RUN_ID and
        .run_attempt == env.RECONCILE_RUN_ATTEMPT and
        .automation_policy_id == env.RECONCILE_AUTOMATION_POLICY_ID and
        .control_oid == env.RECONCILE_CONTROL_OID and
        .state_branch == env.RECONCILE_STATE_BRANCH and
        .base_oid == env.RECONCILE_BASE_OID and
        .ref_hash == env.RECONCILE_REF_HASH and
        (.preparation_manifest_sha256 | digest) and
        if .classification == "success" or .classification == "no-change" then
            keys == ["automation_policy_id", "base_oid", "classification", "control_oid",
                     "final_changed_files", "formatting", "preparation_manifest_sha256",
                     "ref_hash", "run_attempt", "run_id", "schema_version", "state_branch",
                     "terraform_fmt", "updates", "validation_outcome_sha256"] and
            (.validation_outcome_sha256 | digest) and
            (.terraform_fmt | type == "boolean") and
            (.updates.module_blocks_updated | type == "number" and . >= 0 and floor == .) and
            (.updates.provider_blocks_updated | type == "number" and . >= 0 and floor == .) and
            (.updates.changed_files | changed_files) and
            (.formatting.ran | type == "boolean") and
            (.formatting.changed_files | changed_files) and
            (.final_changed_files | changed_files) and
            (.formatting.ran == false or .terraform_fmt == true) and
            if .classification == "success" then
                (.updates | keys) == ["changed_files", "module_blocks_updated", "patch_sha256",
                                     "provider_blocks_updated"] and
                (.updates.changed_files | length > 0) and (.updates.patch_sha256 | digest) and
                (.final_changed_files | length > 0) and
                (.formatting.ran == .terraform_fmt) and
                if (.formatting | has("patch_sha256")) then
                    (.formatting | keys) == ["changed_files", "patch_sha256", "ran"] and
                    .formatting.ran == true and
                    (.formatting.changed_files | length > 0) and
                    (.formatting.patch_sha256 | digest)
                else
                    (.formatting | keys) == ["changed_files", "ran"] and
                    .formatting.changed_files == []
                end
            else
                (.updates | keys) == ["changed_files", "module_blocks_updated",
                                     "provider_blocks_updated"] and
                .updates.changed_files == [] and
                (.formatting | keys) == ["changed_files", "ran"] and
                .formatting.changed_files == [] and .final_changed_files == []
            end
        elif .classification == "branch-validation" then
            keys == ["automation_policy_id", "base_oid", "classification", "control_oid",
                     "failure", "preparation_manifest_sha256", "ref_hash", "run_attempt",
                     "run_id", "run_url", "schema_version", "state_branch",
                     "validation_outcome_sha256"] and
            (.validation_outcome_sha256 | digest) and
            (.failure | keys) == ["root", "stage", "status"] and
            (.failure.stage | type == "string" and length > 0) and
            (.failure.root | type == "string") and
            (.failure.status | type == "number" and . > 0 and floor == .) and
            (.run_url | type == "string" and test("^https://[^[:space:]]+$"))
        elif .classification == "branch-update" or .classification == "branch-init" or
             .classification == "branch-format" or .classification == "automation" then
            keys == ["automation_policy_id", "base_oid", "classification", "control_oid",
                     "failure", "preparation_manifest_sha256", "ref_hash", "run_attempt",
                     "run_id", "run_url", "schema_version", "state_branch"] and
            (.failure | keys) == ["root", "stage", "status"] and
            (.failure.stage | type == "string" and length > 0) and
            (.failure.root | type == "string") and
            (.failure.status | type == "number" and . > 0 and floor == .) and
            (.run_url | type == "string" and test("^https://[^[:space:]]+$"))
        else false end
    ' "$1" >/dev/null
}

verify_verified_result() {
    local manifest=$1 classification
    verified_manifest_is_valid "$manifest" \
        || reconcile_error "verified result manifest contract is invalid"
    classification=$(jq -er '.classification' "$manifest")
    if [[ "$classification" == "success" ]]; then
        [[ -f "$RECONCILE_VERIFIED_RESULT_DIR/update.patch" \
            && ! -L "$RECONCILE_VERIFIED_RESULT_DIR/update.patch" ]] \
            || reconcile_error "verified update patch must be a regular file"
        [[ "$(sha256_file "$RECONCILE_VERIFIED_RESULT_DIR/update.patch")" \
            == "$(jq -er '.updates.patch_sha256' "$manifest")" ]] \
            || reconcile_error "verified update patch digest is invalid"
        if jq -e '.formatting | has("patch_sha256")' "$manifest" >/dev/null; then
            directory_has_exact_entries "$RECONCILE_VERIFIED_RESULT_DIR" \
                format.patch manifest.json update.patch \
                || reconcile_error "verified result contains unexpected entries"
            [[ -f "$RECONCILE_VERIFIED_RESULT_DIR/format.patch" \
                && ! -L "$RECONCILE_VERIFIED_RESULT_DIR/format.patch" ]] \
                || reconcile_error "verified format patch must be a regular file"
            [[ "$(sha256_file "$RECONCILE_VERIFIED_RESULT_DIR/format.patch")" \
                == "$(jq -er '.formatting.patch_sha256' "$manifest")" ]] \
                || reconcile_error "verified format patch digest is invalid"
        else
            directory_has_exact_entries "$RECONCILE_VERIFIED_RESULT_DIR" \
                manifest.json update.patch \
                || reconcile_error "verified result contains unexpected entries"
        fi
    else
        directory_has_exact_entries "$RECONCILE_VERIFIED_RESULT_DIR" manifest.json \
            || reconcile_error "verified result contains unexpected entries"
    fi
    printf '%s\n' "$classification"
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
    local verified_manifest=$1
    local marker payload_dir body_file pr_number issue_number issue_closed
    local branch_html base_html policy_html modules_html providers_html
    local update_count_html format_count_html relative_path
    marker=$(github_marker)
    branch_html=$(html_code "$RECONCILE_STATE_BRANCH")
    base_html=$(html_code "$RECONCILE_BASE_OID")
    policy_html=$(html_code "$RECONCILE_AUTOMATION_POLICY_ID")
    modules_html=$(html_code "$(jq -er '.updates.module_blocks_updated' "$verified_manifest")")
    providers_html=$(html_code "$(jq -er '.updates.provider_blocks_updated' "$verified_manifest")")
    update_count_html=$(html_code "$(jq -er '.updates.changed_files | length' "$verified_manifest")")
    format_count_html=$(html_code "$(jq -er '.formatting.changed_files | length' "$verified_manifest")")
    : "${RUNNER_TEMP:?RUNNER_TEMP must be set}"
    payload_dir=$(mktemp -d "$RUNNER_TEMP/tf-version-bump-github.XXXXXX")
    RECONCILE_TEMPORARY_PATH=$payload_dir
    body_file="$payload_dir/body"
    printf '%s\n\nAutomated Terraform dependency update for %s.\n\nBase: %s\nPolicy: %s\nModule blocks updated: %s\nProvider blocks updated: %s\n\nDependency and lock-file changes (%s):\n' \
        "$marker" "$branch_html" "$base_html" "$policy_html" \
        "$modules_html" "$providers_html" "$update_count_html" >"$body_file"
    while IFS= read -r relative_path; do
        printf -- '- %s\n' "$(html_code "$relative_path")" >>"$body_file"
    done < <(jq -er '.updates.changed_files[].path' "$verified_manifest")
    if jq -e '.formatting.changed_files | length > 0' "$verified_manifest" >/dev/null; then
        printf '\nFormatting changes (%s):\n' "$format_count_html" >>"$body_file"
        while IFS= read -r relative_path; do
            printf -- '- %s\n' "$(html_code "$relative_path")" >>"$body_file"
        done < <(jq -er '.formatting.changed_files[].path' "$verified_manifest")
    else
        printf '\nFormatting changes: None\n' >>"$body_file"
    fi
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
    require_common_identity_values
    : "${RECONCILE_VERIFIED_RESULT_DIR:?RECONCILE_VERIFIED_RESULT_DIR must be set}"
    local verified_manifest="$RECONCILE_VERIFIED_RESULT_DIR/manifest.json"
    [[ -f "$verified_manifest" && ! -L "$verified_manifest" ]] \
        || reconcile_error "verified result manifest must be a regular file"
    local classification
    classification=$(verify_verified_result "$verified_manifest")
    [[ "$classification" != "no-change" && "$classification" != "automation" ]] || return 0
    git check-ref-format "refs/heads/$RECONCILE_STATE_BRANCH" >/dev/null 2>&1 \
        || reconcile_error "state branch is invalid"
    if [[ "$classification" == "success" ]]; then
        : "${RECONCILE_DRY_RUN:?RECONCILE_DRY_RUN must be set}"
        [[ "$RECONCILE_DRY_RUN" == "true" || "$RECONCILE_DRY_RUN" == "false" ]] \
            || reconcile_error "dry-run must be true or false"
        construct_update_commits
        [[ "$RECONCILE_DRY_RUN" == "true" ]] && return
        publish_update_ref
        : "${RECONCILE_REPOSITORY:?RECONCILE_REPOSITORY must be set}"
        : "${GH_TOKEN:?GH_TOKEN must be set}"
        reconcile_success_lifecycle "$verified_manifest"
        return
    fi
    [[ "$classification" == "branch-update" || "$classification" == "branch-init" \
        || "$classification" == "branch-format" \
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
