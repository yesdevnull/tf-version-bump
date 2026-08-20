#!/usr/bin/env bash

set -euo pipefail
set +x
export LC_ALL=C

usage() {
    cat <<'EOF'
Usage: process-state-branch.sh --help
       process-state-branch.sh prepare
       process-state-branch.sh validate

Process one Terraform state branch through the configured workflow phases.

prepare validates the workspace, creates the pre-provider candidate, and requires:
  PROCESS_CONTROL_CHECKOUT  absolute control-checkout root
  PROCESS_TARGET_CHECKOUT   absolute target Git-checkout root
  PROCESS_CONFIG_PATH       repository-relative config file in the control checkout
  PROCESS_TERRAFORM_ROOTS   newline-separated repository-relative target directories
  RUNNER_TEMP               absolute trusted temporary directory outside both checkouts
  PROCESS_RUN_ID            workflow run ID
  PROCESS_RUN_ATTEMPT       workflow run attempt
  PROCESS_AUTOMATION_POLICY_ID  stable automation policy ID
  PROCESS_CONTROL_OID       immutable control revision
  PROCESS_STATE_BRANCH      complete state-branch name
  PROCESS_BASE_OID          immutable state-branch revision
  PROCESS_REF_HASH          SHA-256 of the complete state ref
  PROCESS_TF_VERSION_BUMP_VERSION  exact v-prefixed release tag
  PROCESS_TF_VERSION_BUMP_ARCHIVE_SHA256  Linux x86-64 release archive digest
  PROCESS_TERRAFORM_VERSION exact installed Terraform version
  PROCESS_PREPARATION_DEADLINE_EPOCH  absolute deadline recorded before workflow setup
  PROCESS_PREPARATION_BUNDLE_DIR  previously absent destination below RUNNER_TEMP

prepare downloads and verifies the released updater, verifies Terraform, updates and initialises
roots sequentially using fresh trusted TF_DATA_DIR paths and the one remaining deadline, then emits
no output. A successful bundle contains manifest.json, logs, and candidate.patch. A bounded update
or init failure contains manifest.json and logs but never candidate.patch.

validate verifies a successful preparation bundle, applies its immutable patch only to a fresh
exact-base disposable checkout, then validates every manifest-declared root with the exact
Terraform CLI installed by the workflow, and requires:
  PROCESS_TARGET_CHECKOUT   absolute, clean, exact-base disposable Git-checkout root
  PROCESS_PREPARATION_BUNDLE_DIR  directory holding the preparation manifest and patch
  PROCESS_VALIDATION_OUTCOME_DIR  previously absent destination below RUNNER_TEMP
  RUNNER_TEMP               absolute trusted temporary directory
  PROCESS_RUN_ID            workflow run ID, matched against the preparation manifest
  PROCESS_RUN_ATTEMPT       workflow run attempt, matched against the preparation manifest
  PROCESS_AUTOMATION_POLICY_ID  stable automation policy ID, matched against the manifest
  PROCESS_CONTROL_OID       immutable control revision, matched against the manifest
  PROCESS_STATE_BRANCH      complete state-branch name, matched against the manifest
  PROCESS_BASE_OID          immutable state-branch revision, matched against the manifest
  PROCESS_REF_HASH          SHA-256 of the complete state ref, matched against the manifest
  PROCESS_TERRAFORM_VERSION exact installed Terraform version, matched against the manifest
  PROCESS_VALIDATION_DEADLINE_EPOCH  absolute deadline recorded before workflow setup
EOF
}

processing_path_error() {
    echo "processing path error: $*" >&2
    exit 1
}

processing_status_error() {
    echo "processing status error: $*" >&2
    exit 1
}

processing_setup_error() {
    echo "processing setup error: $*" >&2
    exit 1
}

PREPARATION_CONTROL_CHECKOUT=""
PREPARATION_TARGET_CHECKOUT=""
PREPARATION_CONFIG_PATH=""
PREPARATION_DATA_ROOT=""
PREPARATION_TERRAFORM_ROOTS=()
PREPARATION_TF_DATA_DIRECTORIES=()
PREPARATION_BUNDLE_STAGE=""
PREPARATION_CANDIDATE_INDEX=""
PREPARATION_TF_VERSION_BUMP_BINARY=""
PREPARATION_REF_HASH=""

VALIDATION_TARGET_CHECKOUT=""
VALIDATION_MANIFEST=""
VALIDATION_PATCH=""
VALIDATION_MANIFEST_SHA256=""
VALIDATION_DATA_ROOT=""
VALIDATION_CANDIDATE_STATUS=""
VALIDATION_PREPARATION_CLASSIFICATION=""

# shellcheck disable=SC2329 # Invoked by the validate-mode EXIT trap.
cleanup_validation_temporaries() {
    [[ -z "$VALIDATION_DATA_ROOT" ]] || rm -rf -- "$VALIDATION_DATA_ROOT"
}

# shellcheck disable=SC2329 # Invoked by the prepare-mode EXIT trap.
cleanup_preparation_temporaries() {
    if [[ -n "$PREPARATION_BUNDLE_STAGE" ]]; then
        # The staging name remains set until final destination modes are applied, so either path
        # is incomplete and must not survive an interrupted finalisation.
        local bundle_path
        for bundle_path in \
            "$PREPARATION_BUNDLE_STAGE" \
            "${PROCESS_PREPARATION_BUNDLE_DIR-}"; do
            if [[ -n "$bundle_path" && ( -e "$bundle_path" || -L "$bundle_path" ) ]]; then
                chmod -R u+w "$bundle_path" 2>/dev/null || true
                rm -rf -- "$bundle_path"
            fi
        done
        PREPARATION_BUNDLE_STAGE=""
    fi
    if [[ -n "$PREPARATION_DATA_ROOT" ]]; then
        rm -rf -- "$PREPARATION_DATA_ROOT"
        PREPARATION_DATA_ROOT=""
    fi
}

remaining_seconds() {
    local deadline=$1
    local phase=$2
    [[ "$deadline" =~ ^[0-9]+$ ]] \
        || processing_setup_error "$phase deadline must be an epoch timestamp"
    local remaining=$((deadline - $(date +%s)))
    [[ "$remaining" -gt 0 ]] || return 1
    printf '%s\n' "$remaining"
}

remaining_preparation_seconds() {
    : "${PROCESS_PREPARATION_DEADLINE_EPOCH:?PROCESS_PREPARATION_DEADLINE_EPOCH must be set}"
    remaining_seconds "$PROCESS_PREPARATION_DEADLINE_EPOCH" preparation
}

remaining_validation_seconds() {
    : "${PROCESS_VALIDATION_DEADLINE_EPOCH:?PROCESS_VALIDATION_DEADLINE_EPOCH must be set}"
    remaining_seconds "$PROCESS_VALIDATION_DEADLINE_EPOCH" validation
}

run_before_deadline() {
    local remaining_function=$1 log_file=$2
    shift 2
    local remaining
    if ! remaining=$("$remaining_function"); then
        return 124
    fi
    if [[ "$remaining" -gt 1 ]]; then
        timeout --signal=TERM --kill-after=1s "$((remaining - 1))s" \
            "$@" >"$log_file" 2>&1
    else
        timeout --signal=KILL "${remaining}s" \
            "$@" >"$log_file" 2>&1
    fi
}

run_before_preparation_deadline() {
    run_before_deadline remaining_preparation_seconds "$@"
}

run_before_validation_deadline() {
    run_before_deadline remaining_validation_seconds "$@"
}

relative_terraform_root() {
    local terraform_root=$1
    if [[ "$terraform_root" == "$PREPARATION_TARGET_CHECKOUT" ]]; then
        printf '%s\n' "."
    else
        printf '%s\n' "${terraform_root#"$PREPARATION_TARGET_CHECKOUT"/}"
    fi
}

prepare_bundle_contract() {
    : "${PROCESS_RUN_ID:?PROCESS_RUN_ID must be set}"
    : "${PROCESS_RUN_ATTEMPT:?PROCESS_RUN_ATTEMPT must be set}"
    : "${PROCESS_AUTOMATION_POLICY_ID:?PROCESS_AUTOMATION_POLICY_ID must be set}"
    : "${PROCESS_CONTROL_OID:?PROCESS_CONTROL_OID must be set}"
    : "${PROCESS_STATE_BRANCH:?PROCESS_STATE_BRANCH must be set}"
    : "${PROCESS_BASE_OID:?PROCESS_BASE_OID must be set}"
    : "${PROCESS_REF_HASH:?PROCESS_REF_HASH must be set}"
    : "${PROCESS_PREPARATION_BUNDLE_DIR:?PROCESS_PREPARATION_BUNDLE_DIR must be set}"

    [[ "$PROCESS_RUN_ID" =~ ^[1-9][0-9]*$ ]] \
        || processing_setup_error "run ID must be a positive integer"
    [[ "$PROCESS_RUN_ATTEMPT" =~ ^[1-9][0-9]*$ ]] \
        || processing_setup_error "run attempt must be a positive integer"
    [[ "$PROCESS_AUTOMATION_POLICY_ID" =~ ^[a-z0-9][a-z0-9-]{0,31}$ ]] \
        || processing_setup_error "automation policy ID is invalid"
    [[ "$PROCESS_CONTROL_OID" =~ ^[0-9a-f]{40}$ ]] \
        || processing_setup_error "control OID must be 40 lowercase hexadecimal characters"
    [[ "$PROCESS_BASE_OID" =~ ^[0-9a-f]{40}$ ]] \
        || processing_setup_error "base OID must be 40 lowercase hexadecimal characters"
    [[ "$PROCESS_REF_HASH" =~ ^[0-9a-f]{64}$ ]] \
        || processing_setup_error "ref hash must be 64 lowercase hexadecimal characters"
    git check-ref-format "refs/heads/$PROCESS_STATE_BRANCH" >/dev/null 2>&1 \
        || processing_setup_error "state branch is not a valid branch name"

    local control_repository_root
    if ! control_repository_root=$(git -C "$PREPARATION_CONTROL_CHECKOUT" \
        rev-parse --show-toplevel 2>/dev/null); then
        processing_setup_error "control checkout is not a Git worktree"
    fi
    control_repository_root=$(realpath "$control_repository_root")
    if [[ "$control_repository_root" != "$PREPARATION_CONTROL_CHECKOUT" ]]; then
        processing_setup_error "control checkout must be the Git worktree root"
    fi
    local control_head
    control_head=$(git -C "$PREPARATION_CONTROL_CHECKOUT" rev-parse HEAD)
    if [[ "$control_head" != "$PROCESS_CONTROL_OID" ]]; then
        processing_setup_error "control checkout HEAD does not match control OID"
    fi
    local target_head
    target_head=$(git -C "$PREPARATION_TARGET_CHECKOUT" rev-parse HEAD)
    if [[ "$target_head" != "$PROCESS_BASE_OID" ]]; then
        processing_setup_error "target checkout HEAD does not match base OID"
    fi
    local computed_ref_hash
    computed_ref_hash=$(printf '%s' "refs/heads/$PROCESS_STATE_BRANCH" | sha256sum)
    computed_ref_hash=${computed_ref_hash%% *}
    if [[ "$computed_ref_hash" != "$PROCESS_REF_HASH" ]]; then
        processing_setup_error "state ref hash does not match ref hash"
    fi
    PREPARATION_REF_HASH=$computed_ref_hash

    if [[ "$PROCESS_PREPARATION_BUNDLE_DIR" != /* ]]; then
        processing_setup_error "preparation bundle directory must be absolute"
    fi
    if [[ -e "$PROCESS_PREPARATION_BUNDLE_DIR" || -L "$PROCESS_PREPARATION_BUNDLE_DIR" ]]; then
        processing_setup_error "preparation bundle destination must not already exist"
    fi
    local bundle_parent=${PROCESS_PREPARATION_BUNDLE_DIR%/*}
    if [[ ! -d "$bundle_parent" ]]; then
        processing_setup_error "preparation bundle parent must exist"
    fi
    bundle_parent=$(realpath "$bundle_parent")
    local runner_temp
    # shellcheck disable=SC2153 # Set by the reusable workflow or the focused harness.
    runner_temp=$(realpath "$RUNNER_TEMP")
    if ! path_is_within "$bundle_parent" "$runner_temp"; then
        processing_setup_error "preparation bundle must be below RUNNER_TEMP"
    fi

    PREPARATION_BUNDLE_STAGE=$(mktemp -d "$runner_temp/preparation-bundle-stage.XXXXXX")
    chmod 700 "$PREPARATION_BUNDLE_STAGE"
    mkdir -m 700 "$PREPARATION_BUNDLE_STAGE/logs"
}

copy_preparation_logs() {
    local log_file
    for log_file in "$PREPARATION_DATA_ROOT"/*.log; do
        if [[ -f "$log_file" ]]; then
            cp -- "$log_file" "$PREPARATION_BUNDLE_STAGE/logs/${log_file##*/}"
        fi
    done
}

preparation_identity_json() {
    jq -cn \
        --arg run_id "$PROCESS_RUN_ID" --arg run_attempt "$PROCESS_RUN_ATTEMPT" \
        --arg policy "$PROCESS_AUTOMATION_POLICY_ID" --arg control_oid "$PROCESS_CONTROL_OID" \
        --arg branch "$PROCESS_STATE_BRANCH" --arg base_oid "$PROCESS_BASE_OID" \
        --arg ref_hash "$PREPARATION_REF_HASH" \
        '{schema_version: 1, run_id: $run_id, run_attempt: $run_attempt,
          automation_policy_id: $policy, control_oid: $control_oid,
          state_branch: $branch, base_oid: $base_oid, ref_hash: $ref_hash,
          artifact_name: ("preparation-" + $run_id + "-" + $run_attempt + "-" +
                          $policy + "-" + $ref_hash)}'
}

finalise_preparation_bundle() {
    chmod 444 "$PREPARATION_BUNDLE_STAGE/manifest.json"
    local log_file
    for log_file in "$PREPARATION_BUNDLE_STAGE"/logs/*; do
        if [[ -f "$log_file" ]]; then
            chmod 444 -- "$log_file"
        fi
    done
    [[ ! -e "$PREPARATION_BUNDLE_STAGE/candidate.patch" ]] \
        || chmod 444 "$PREPARATION_BUNDLE_STAGE/candidate.patch"
    chmod 555 "$PREPARATION_BUNDLE_STAGE/logs"
    mv -- "$PREPARATION_BUNDLE_STAGE" "$PROCESS_PREPARATION_BUNDLE_DIR"
    chmod 555 "$PROCESS_PREPARATION_BUNDLE_DIR"
    PREPARATION_BUNDLE_STAGE=""
    rm -rf -- "$PREPARATION_DATA_ROOT"
    PREPARATION_DATA_ROOT=""
}

write_preparation_failure_bundle() {
    local classification=$1
    local failure_stage=$2
    local failure_root=$3
    local failure_command=$4
    local failure_status=$5

    copy_preparation_logs
    preparation_identity_json | jq \
        --arg classification "$classification" \
        --arg stage "$failure_stage" \
        --arg root "$failure_root" \
        --arg command "$failure_command" \
        --argjson status "$failure_status" \
        '. + {classification: $classification,
              failure: {stage: $stage, root: $root, command: $command, status: $status}}' \
        >"$PREPARATION_BUNDLE_STAGE/manifest.json"
    finalise_preparation_bundle
}

write_preparation_candidate_bundle() {
    PREPARATION_CANDIDATE_INDEX="$PREPARATION_DATA_ROOT/candidate.index"
    rm -f -- "$PREPARATION_CANDIDATE_INDEX"
    GIT_INDEX_FILE="$PREPARATION_CANDIDATE_INDEX" \
        git -C "$PREPARATION_TARGET_CHECKOUT" read-tree HEAD
    GIT_INDEX_FILE="$PREPARATION_CANDIDATE_INDEX" \
        git -C "$PREPARATION_TARGET_CHECKOUT" add --all -- .
    local classification="success" patch_sha256="" diff_status=0
    GIT_INDEX_FILE="$PREPARATION_CANDIDATE_INDEX" \
        git -C "$PREPARATION_TARGET_CHECKOUT" diff --cached --quiet --exit-code \
        || diff_status=$?
    if [[ "$diff_status" -eq 0 ]]; then
        classification="no-change"
    elif [[ "$diff_status" -eq 1 ]]; then
        GIT_INDEX_FILE="$PREPARATION_CANDIDATE_INDEX" \
            git -C "$PREPARATION_TARGET_CHECKOUT" diff --cached --binary --full-index --no-color \
            >"$PREPARATION_BUNDLE_STAGE/candidate.patch"
        patch_sha256=$(sha256sum "$PREPARATION_BUNDLE_STAGE/candidate.patch")
        patch_sha256=${patch_sha256%% *}
    else
        processing_status_error "could not inspect candidate index changes"
    fi
    copy_preparation_logs

    local -a relative_roots=()
    local terraform_root
    for terraform_root in "${PREPARATION_TERRAFORM_ROOTS[@]}"; do
        relative_roots+=("$(relative_terraform_root "$terraform_root")")
    done
    local roots_json
    roots_json=$(jq -cn '$ARGS.positional | map({path: .})' --args -- "${relative_roots[@]}")

    # Mode and path are read together from one raw diff so a glob metacharacter in a changed
    # file's name (e.g. "a[5].tf") can never make a separate pathspec lookup read a sibling's
    # mode. --no-renames keeps this a flat one-path-per-record stream.
    local -a relative_paths=() git_modes=()
    local raw_entry relative_path new_mode
    while IFS= read -r -d '' raw_entry && IFS= read -r -d '' relative_path; do
        read -r _ new_mode _ _ _ <<<"$raw_entry"
        relative_paths+=("$relative_path")
        git_modes+=("$new_mode")
    done < <(
        GIT_INDEX_FILE="$PREPARATION_CANDIDATE_INDEX" \
            git -C "$PREPARATION_TARGET_CHECKOUT" diff --cached --raw -z --no-renames
    )
    local -a file_sha256s=()
    if [[ ${#relative_paths[@]} -gt 0 ]]; then
        local sha_line
        while IFS= read -r sha_line; do
            file_sha256s+=("${sha_line%% *}")
        done < <(cd "$PREPARATION_TARGET_CHECKOUT" && sha256sum -- "${relative_paths[@]}")
    fi
    local -a interleaved=()
    local index
    for index in "${!relative_paths[@]}"; do
        interleaved+=("${relative_paths[$index]}" "${git_modes[$index]}" "${file_sha256s[$index]}")
    done
    local changed_files_json='[]'
    if [[ ${#interleaved[@]} -gt 0 ]]; then
        changed_files_json=$(jq -cn \
            '$ARGS.positional | [range(0; length; 3) as $i |
                {path: .[$i], mode: .[$i + 1], sha256: .[$i + 2]}]' \
            --args -- "${interleaved[@]}")
    fi
    preparation_identity_json | jq \
        --arg config_path "$PROCESS_CONFIG_PATH" \
        --arg tf_version_bump_version "$PROCESS_TF_VERSION_BUMP_VERSION" \
        --arg tf_version_bump_archive_sha256 "$PROCESS_TF_VERSION_BUMP_ARCHIVE_SHA256" \
        --arg terraform_version "$PROCESS_TERRAFORM_VERSION" \
        --argjson roots "$roots_json" \
        --argjson changed_files "$changed_files_json" \
        --arg patch_sha256 "$patch_sha256" \
        --arg classification "$classification" \
        '. + {config_path: $config_path, tools: {
            tf_version_bump: {
              version: $tf_version_bump_version,
              archive_sha256: $tf_version_bump_archive_sha256
            },
            terraform: {version: $terraform_version}
          },
          roots: $roots,
          changed_files: $changed_files,
          classification: $classification}
          + if $patch_sha256 == "" then {} else {patch_sha256: $patch_sha256} end' \
        >"$PREPARATION_BUNDLE_STAGE/manifest.json"
    finalise_preparation_bundle
}

path_is_within() {
    local path=$1
    local root=$2
    [[ "$path" == "$root" || "$path" == "$root/"* ]]
}

validate_terraform_file() {
    local target_checkout=$1 terraform_root=$2 terraform_file=$3 description=$4
    [[ -f "$terraform_file" && ! -L "$terraform_file" ]] \
        || processing_path_error "$description must be a regular non-symlink"
    local canonical_file
    canonical_file=$(realpath "$terraform_file")
    if ! path_is_within "$canonical_file" "$terraform_root" \
        || ! path_is_within "$canonical_file" "$target_checkout"; then
        processing_path_error "$description resolves outside its configured root"
    fi
}

validate_terraform_files() {
    local target_checkout=$1 terraform_root=$2 terraform_file
    while IFS= read -r -d '' terraform_file; do
        validate_terraform_file "$target_checkout" "$terraform_root" "$terraform_file" "Terraform file"
    done < <(find "$terraform_root" -mindepth 1 -maxdepth 1 -name '*.tf' -print0)
}

validate_lock_file() {
    local target_checkout=$1 terraform_root=$2
    local lock_file="$terraform_root/.terraform.lock.hcl"
    [[ -e "$lock_file" || -L "$lock_file" ]] || return 0
    validate_terraform_file "$target_checkout" "$terraform_root" "$lock_file" "Terraform lock file"
}

reject_repository_terraform_entries() {
    local target_checkout=$1
    local terraform_entry

    terraform_entry=$(find "$target_checkout" \
        -path "$target_checkout/.git" -prune \
        -o -name .terraform -print -quit)
    if [[ -n "$terraform_entry" ]]; then
        processing_path_error "repository .terraform entry is forbidden"
    fi
}

reject_escaping_directory_symlinks() {
    local target_checkout=$1
    local repository_symlink
    local canonical_link

    while IFS= read -r -d '' repository_symlink; do
        if [[ ! -d "$repository_symlink" ]]; then
            continue
        fi
        canonical_link=$(realpath "$repository_symlink")
        if ! path_is_within "$canonical_link" "$target_checkout"; then
            processing_path_error "repository directory symlink resolves outside target checkout"
        fi
    done < <(find "$target_checkout" \
        -path "$target_checkout/.git" -prune \
        -o -type l -print0)
}

validate_changed_path() {
    local target_checkout=$1 relative_path=$2
    shift 2
    local -a terraform_roots=("$@")
    if [[ "$relative_path" == *$'\n'* ]]; then
        processing_status_error "changed path must not contain a newline"
    fi
    local changed_path="$target_checkout/$relative_path"
    if [[ -L "$changed_path" || ! -f "$changed_path" ]]; then
        processing_status_error "changed path must be a regular non-symlink file"
    fi

    local canonical_path
    canonical_path=$(realpath "$changed_path")
    if ! path_is_within "$canonical_path" "$target_checkout"; then
        processing_status_error "changed path resolves outside target checkout"
    fi

    local filename=${canonical_path##*/} parent_path=${canonical_path%/*}
    local owning_roots=0 terraform_root
    for terraform_root in "${terraform_roots[@]}"; do
        if [[ "$parent_path" == "$terraform_root" ]]; then
            owning_roots=$((owning_roots + 1))
        fi
    done

    if [[ "$filename" == *.tf ]]; then
        if [[ "$owning_roots" -ne 1 ]]; then
            processing_status_error "changed Terraform file must be directly within exactly one configured root"
        fi
        return
    fi
    if [[ "$filename" == ".terraform.lock.hcl" && "$owning_roots" -eq 1 ]]; then
        return
    fi
    processing_status_error "unexpected changed or untracked path"
}

validate_git_status_stream() {
    local target_checkout=$1
    shift
    local -a terraform_roots=("$@")
    local status_entry status_code relative_path raw_digest normalised_digest

    while IFS= read -r -d '' status_entry; do
        if [[ ${#status_entry} -lt 4 || "${status_entry:2:1}" != " " ]]; then
            processing_status_error "invalid NUL-delimited Git status entry"
        fi
        status_code=${status_entry:0:2}
        relative_path=${status_entry:3}
        if [[ "$status_code" == "!!" ]]; then
            processing_status_error "ignored path is forbidden"
        fi
        if [[ "$status_code" == *R* || "$status_code" == *C* ]]; then
            processing_status_error "renamed or copied paths are forbidden"
        fi
        raw_digest=$(printf '%s' "$relative_path" | sha256sum)
        normalised_digest=$(printf '%s' "$relative_path" | jq -Rjsc . | sha256sum)
        [[ "${raw_digest%% *}" == "${normalised_digest%% *}" ]] \
            || processing_status_error "changed path is not valid UTF-8"
        validate_changed_path "$target_checkout" "$relative_path" "${terraform_roots[@]}"
    done
}

validate_git_status() {
    local target_checkout=$1
    shift
    local repository_root
    if ! repository_root=$(git -C "$target_checkout" rev-parse --show-toplevel 2>/dev/null); then
        processing_status_error "target checkout is not a Git repository"
    fi
    repository_root=$(realpath "$repository_root")
    if [[ "$repository_root" != "$target_checkout" ]]; then
        processing_status_error "target checkout must be the Git worktree root"
    fi

    git -C "$target_checkout" status --porcelain=v1 -z --untracked-files=all --ignored \
        | validate_git_status_stream "$target_checkout" "$@"
}

prepare_workspace() {
    : "${PROCESS_CONTROL_CHECKOUT:?PROCESS_CONTROL_CHECKOUT must be set}"
    : "${PROCESS_TARGET_CHECKOUT:?PROCESS_TARGET_CHECKOUT must be set}"
    : "${PROCESS_CONFIG_PATH:?PROCESS_CONFIG_PATH must be set}"
    : "${PROCESS_TERRAFORM_ROOTS:?PROCESS_TERRAFORM_ROOTS must be set}"
    : "${RUNNER_TEMP:?RUNNER_TEMP must be set}"

    if [[ "$PROCESS_CONFIG_PATH" == /* ]]; then
        processing_path_error "config path must be repository-relative"
    fi
    if [[ "/$PROCESS_CONFIG_PATH/" == *"/../"* ]]; then
        processing_path_error "config path must not contain '..'"
    fi

    if [[ "$PROCESS_CONTROL_CHECKOUT" != /* || ! -d "$PROCESS_CONTROL_CHECKOUT" ]]; then
        processing_path_error "control checkout must be an absolute existing directory"
    fi
    local control_checkout config_path
    control_checkout=$(realpath "$PROCESS_CONTROL_CHECKOUT")
    if [[ ! -e "$control_checkout/$PROCESS_CONFIG_PATH" \
        && ! -L "$control_checkout/$PROCESS_CONFIG_PATH" ]]; then
        processing_path_error "config path does not exist"
    fi
    if ! config_path=$(realpath "$control_checkout/$PROCESS_CONFIG_PATH" 2>/dev/null); then
        processing_path_error "config path does not exist"
    fi
    if ! path_is_within "$config_path" "$control_checkout"; then
        processing_path_error "config path resolves outside control checkout"
    fi
    if [[ ! -f "$config_path" ]]; then
        processing_path_error "config path is not a regular file"
    fi

    if [[ "$PROCESS_TARGET_CHECKOUT" != /* || ! -d "$PROCESS_TARGET_CHECKOUT" ]]; then
        processing_path_error "target checkout must be an absolute existing directory"
    fi
    local target_checkout
    target_checkout=$(realpath "$PROCESS_TARGET_CHECKOUT")
    if path_is_within "$control_checkout" "$target_checkout" \
        || path_is_within "$target_checkout" "$control_checkout"; then
        processing_path_error "control and target checkouts must be distinct and non-overlapping"
    fi
    if [[ "$RUNNER_TEMP" != /* || ! -d "$RUNNER_TEMP" ]]; then
        processing_path_error "RUNNER_TEMP must be an absolute existing directory"
    fi
    local runner_temp
    runner_temp=$(realpath "$RUNNER_TEMP")
    if path_is_within "$runner_temp" "$control_checkout" \
        || path_is_within "$runner_temp" "$target_checkout"; then
        processing_path_error "RUNNER_TEMP must resolve outside both checkouts"
    fi

    local terraform_root canonical_root existing_root
    local -a configured_roots=()
    local -a terraform_roots=()
    readarray -t configured_roots < <(printf '%s' "$PROCESS_TERRAFORM_ROOTS")
    for terraform_root in "${configured_roots[@]}"; do
        if [[ -z "$terraform_root" ]]; then
            processing_path_error "Terraform root entry must not be empty"
        fi
        if [[ "$terraform_root" == /* ]]; then
            processing_path_error "Terraform root must be repository-relative"
        fi
        if [[ "/$terraform_root/" == *"/../"* ]]; then
            processing_path_error "Terraform root must not contain '..'"
        fi
        if [[ ! -e "$target_checkout/$terraform_root" \
            && ! -L "$target_checkout/$terraform_root" ]]; then
            processing_path_error "Terraform root does not exist"
        fi
        if ! canonical_root=$(realpath "$target_checkout/$terraform_root" 2>/dev/null); then
            processing_path_error "Terraform root does not exist"
        fi
        if [[ ! -d "$canonical_root" ]]; then
            processing_path_error "Terraform root is not a directory"
        fi
        if ! path_is_within "$canonical_root" "$target_checkout"; then
            processing_path_error "Terraform root resolves outside target checkout"
        fi
        for existing_root in "${terraform_roots[@]}"; do
            if [[ "$canonical_root" == "$existing_root" ]]; then
                processing_path_error "duplicate canonical Terraform root"
            fi
        done
        terraform_roots+=("$canonical_root")
    done

    reject_repository_terraform_entries "$target_checkout"
    reject_escaping_directory_symlinks "$target_checkout"
    for terraform_root in "${terraform_roots[@]}"; do
        validate_terraform_files "$target_checkout" "$terraform_root"
        validate_lock_file "$target_checkout" "$terraform_root"
    done
    validate_git_status "$target_checkout" "${terraform_roots[@]}"

    umask 077
    local data_root
    if ! data_root=$(mktemp -d "$runner_temp/tf-version-bump-data.XXXXXX"); then
        processing_path_error "could not create trusted Terraform data root"
    fi
    data_root=$(realpath "$data_root")
    if path_is_within "$data_root" "$control_checkout" \
        || path_is_within "$data_root" "$target_checkout"; then
        rm -rf -- "$data_root"
        processing_path_error "TF_DATA_DIR root must resolve outside both checkouts"
    fi

    local root_index=0 data_directory
    local -a tf_data_directories=()
    for terraform_root in "${terraform_roots[@]}"; do
        root_index=$((root_index + 1))
        data_directory="$data_root/root-$root_index"
        if ! mkdir -m 700 -- "$data_directory"; then
            rm -rf -- "$data_root"
            processing_path_error "could not create TF_DATA_DIR"
        fi
        tf_data_directories+=("$data_directory")
    done

    PREPARATION_CONTROL_CHECKOUT=$control_checkout
    PREPARATION_TARGET_CHECKOUT=$target_checkout
    PREPARATION_CONFIG_PATH=$config_path
    PREPARATION_DATA_ROOT=$data_root
    PREPARATION_TERRAFORM_ROOTS=("${terraform_roots[@]}")
    PREPARATION_TF_DATA_DIRECTORIES=("${tf_data_directories[@]}")
}

validation_contract() {
    : "${PROCESS_TARGET_CHECKOUT:?PROCESS_TARGET_CHECKOUT must be set}"
    : "${PROCESS_RUN_ID:?PROCESS_RUN_ID must be set}"
    : "${PROCESS_RUN_ATTEMPT:?PROCESS_RUN_ATTEMPT must be set}"
    : "${PROCESS_AUTOMATION_POLICY_ID:?PROCESS_AUTOMATION_POLICY_ID must be set}"
    : "${PROCESS_CONTROL_OID:?PROCESS_CONTROL_OID must be set}"
    : "${PROCESS_STATE_BRANCH:?PROCESS_STATE_BRANCH must be set}"
    : "${PROCESS_BASE_OID:?PROCESS_BASE_OID must be set}"
    : "${PROCESS_REF_HASH:?PROCESS_REF_HASH must be set}"
    : "${PROCESS_TERRAFORM_VERSION:?PROCESS_TERRAFORM_VERSION must be set}"
    : "${PROCESS_PREPARATION_BUNDLE_DIR:?PROCESS_PREPARATION_BUNDLE_DIR must be set}"
    : "${PROCESS_VALIDATION_OUTCOME_DIR:?PROCESS_VALIDATION_OUTCOME_DIR must be set}"
    : "${RUNNER_TEMP:?RUNNER_TEMP must be set}"

    [[ "$PROCESS_TARGET_CHECKOUT" == /* && -d "$PROCESS_TARGET_CHECKOUT" ]] \
        || processing_path_error "validation checkout must be an absolute directory"
    VALIDATION_TARGET_CHECKOUT=$(realpath "$PROCESS_TARGET_CHECKOUT")
    [[ "$(git -C "$VALIDATION_TARGET_CHECKOUT" rev-parse HEAD)" == "$PROCESS_BASE_OID" ]] \
        || processing_setup_error "validation checkout HEAD does not match base OID"
    [[ -z "$(git -C "$VALIDATION_TARGET_CHECKOUT" status --porcelain=v1 --untracked-files=all)" ]] \
        || processing_setup_error "validation checkout must be clean before candidate apply"
    [[ -d "$PROCESS_PREPARATION_BUNDLE_DIR" ]] \
        || processing_path_error "preparation bundle must be a directory"
    VALIDATION_MANIFEST="$(realpath "$PROCESS_PREPARATION_BUNDLE_DIR")/manifest.json"
    VALIDATION_PATCH="$(realpath "$PROCESS_PREPARATION_BUNDLE_DIR")/candidate.patch"
    [[ -f "$VALIDATION_MANIFEST" && ! -L "$VALIDATION_MANIFEST" ]] \
        || processing_status_error "candidate manifest must be a regular file"
    jq -e '.schema_version == 1 and
         (.classification == "success" or .classification == "no-change") and
         .run_id == env.PROCESS_RUN_ID and .run_attempt == env.PROCESS_RUN_ATTEMPT and
         .automation_policy_id == env.PROCESS_AUTOMATION_POLICY_ID and
         .control_oid == env.PROCESS_CONTROL_OID and .state_branch == env.PROCESS_STATE_BRANCH and
         .base_oid == env.PROCESS_BASE_OID and .ref_hash == env.PROCESS_REF_HASH and
         .tools.terraform.version == env.PROCESS_TERRAFORM_VERSION and (.roots | length > 0)' \
        "$VALIDATION_MANIFEST" >/dev/null \
        || processing_status_error "candidate manifest identity or schema is invalid"
    VALIDATION_PREPARATION_CLASSIFICATION=$(jq -er '.classification' "$VALIDATION_MANIFEST")
    read -r VALIDATION_MANIFEST_SHA256 _ < <(sha256sum "$VALIDATION_MANIFEST")
    if [[ "$VALIDATION_PREPARATION_CLASSIFICATION" == "success" ]]; then
        [[ -f "$VALIDATION_PATCH" && ! -L "$VALIDATION_PATCH" ]] \
            || processing_status_error "candidate patch must be a regular file"
        local patch_sha256
        read -r patch_sha256 _ < <(sha256sum "$VALIDATION_PATCH")
        [[ "$patch_sha256" == "$(jq -er '.patch_sha256' "$VALIDATION_MANIFEST")" ]] \
            || processing_status_error "candidate patch SHA-256 does not match manifest"
    else
        [[ ! -e "$VALIDATION_PATCH" && ! -L "$VALIDATION_PATCH" ]] \
            || processing_status_error "no-change candidate must not contain a patch"
        jq -e '.changed_files == [] and (has("patch_sha256") | not)' \
            "$VALIDATION_MANIFEST" >/dev/null \
            || processing_status_error "no-change candidate manifest is invalid"
    fi
    if [[ "$PROCESS_VALIDATION_OUTCOME_DIR" != /* ]]; then
        processing_setup_error "validation outcome directory must be absolute"
    fi
    if [[ -e "$PROCESS_VALIDATION_OUTCOME_DIR" || -L "$PROCESS_VALIDATION_OUTCOME_DIR" ]]; then
        processing_setup_error "validation outcome destination must be absent"
    fi
    local outcome_parent=${PROCESS_VALIDATION_OUTCOME_DIR%/*}
    if [[ ! -d "$outcome_parent" ]]; then
        processing_setup_error "validation outcome parent must exist"
    fi
    outcome_parent=$(realpath "$outcome_parent")
    local runner_temp
    runner_temp=$(realpath "$RUNNER_TEMP")
    if ! path_is_within "$outcome_parent" "$runner_temp"; then
        processing_setup_error "validation outcome must be below RUNNER_TEMP"
    fi
    umask 077
    VALIDATION_DATA_ROOT=$(mktemp -d "$runner_temp/tf-version-bump-validation-data.XXXXXX")
}

write_validation_outcome() {
    local classification=$1 command_status=$2 failure_stage=${3-} failure_root=${4-}
    mkdir -m 700 "$PROCESS_VALIDATION_OUTCOME_DIR"
    mkdir -m 700 "$PROCESS_VALIDATION_OUTCOME_DIR/logs"
    local log_file
    for log_file in "$VALIDATION_DATA_ROOT"/*.log; do
        if [[ -f "$log_file" ]]; then
            cp -- "$log_file" "$PROCESS_VALIDATION_OUTCOME_DIR/logs/${log_file##*/}"
        fi
    done
    jq \
        --arg candidate_manifest_sha256 "$VALIDATION_MANIFEST_SHA256" \
        --arg classification "$classification" \
        --argjson command_status "$command_status" \
        --arg failure_stage "$failure_stage" \
        --arg failure_root "$failure_root" \
        '{schema_version, run_id, run_attempt, automation_policy_id, control_oid,
          state_branch, base_oid, ref_hash,
          candidate_manifest_sha256: $candidate_manifest_sha256,
          classification: $classification,
          command_status: $command_status}
          + if $classification == "branch-validation" then
              {failure: {stage: $failure_stage, root: $failure_root,
                         status: $command_status}}
            else {} end' \
        "$VALIDATION_MANIFEST" >"$PROCESS_VALIDATION_OUTCOME_DIR/manifest.json"
    chmod -R a-w "$PROCESS_VALIDATION_OUTCOME_DIR"
    rm -rf -- "$VALIDATION_DATA_ROOT"
    VALIDATION_DATA_ROOT=""
}

finalise_validation_outcome() {
    local classification=$1 command_status=$2 failure_stage=${3-} failure_root=${4-}

    reject_repository_terraform_entries "$VALIDATION_TARGET_CHECKOUT"
    [[ "$(git -C "$VALIDATION_TARGET_CHECKOUT" status --porcelain=v1 --untracked-files=all)" \
        == "$VALIDATION_CANDIDATE_STATUS" ]] \
        || processing_setup_error "Terraform validation changed the candidate checkout"
    write_validation_outcome "$classification" "$command_status" \
        "$failure_stage" "$failure_root"
}

validate_candidate_roots() {
    local root_index=0 relative_root terraform_root data_directory command_status
    while IFS= read -r relative_root; do
        terraform_root=$(realpath "$VALIDATION_TARGET_CHECKOUT/$relative_root")
        if [[ -z "$relative_root" || ! -d "$terraform_root" ]] \
            || ! path_is_within "$terraform_root" "$VALIDATION_TARGET_CHECKOUT"; then
            processing_status_error "candidate contains an unsafe Terraform root"
        fi
        root_index=$((root_index + 1))
        data_directory="$VALIDATION_DATA_ROOT/root-$root_index"
        mkdir -m 700 "$data_directory"

        local -a init_arguments=(-chdir="$terraform_root" init -backend=false -input=false -no-color)
        [[ ! -f "$terraform_root/.terraform.lock.hcl" ]] || init_arguments+=(-lockfile=readonly)
        command_status=0
        TF_DATA_DIR="$data_directory" TF_IN_AUTOMATION=1 CHECKPOINT_DISABLE=1 \
            run_before_validation_deadline "$VALIDATION_DATA_ROOT/init-$root_index.log" \
            terraform "${init_arguments[@]}" || command_status=$?
        if [[ "$command_status" -ne 0 ]]; then
            finalise_validation_outcome "branch-validation" "$command_status" \
                "terraform init" "$relative_root"
            processing_status_error "validation initialisation failed for Terraform root $relative_root"
        fi

        command_status=0
        TF_DATA_DIR="$data_directory" TF_IN_AUTOMATION=1 CHECKPOINT_DISABLE=1 \
            run_before_validation_deadline "$VALIDATION_DATA_ROOT/validate-$root_index.log" \
            terraform -chdir="$terraform_root" validate -no-color \
            || command_status=$?
        if [[ "$command_status" -ne 0 ]]; then
            finalise_validation_outcome "branch-validation" "$command_status" \
                "terraform validate" "$relative_root"
            processing_status_error "terraform validate failed for Terraform root $relative_root"
        fi
    done < <(jq -er '.roots[].path' "$VALIDATION_MANIFEST")
    finalise_validation_outcome "$VALIDATION_PREPARATION_CLASSIFICATION" 0
}

apply_validation_candidate() {
    git -C "$VALIDATION_TARGET_CHECKOUT" apply --check --binary "$VALIDATION_PATCH" || processing_status_error "candidate patch does not apply to exact base"
    git -C "$VALIDATION_TARGET_CHECKOUT" apply --binary "$VALIDATION_PATCH" || processing_status_error "candidate patch could not be applied"
    VALIDATION_CANDIDATE_STATUS=$(git -C "$VALIDATION_TARGET_CHECKOUT" status --porcelain=v1 --untracked-files=all)
}

install_tf_version_bump() {
    : "${PROCESS_TF_VERSION_BUMP_VERSION:?PROCESS_TF_VERSION_BUMP_VERSION must be set}"
    : "${PROCESS_TF_VERSION_BUMP_ARCHIVE_SHA256:?PROCESS_TF_VERSION_BUMP_ARCHIVE_SHA256 must be set}"

    if [[ ! "$PROCESS_TF_VERSION_BUMP_VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
        processing_setup_error "tf-version-bump version must be a v-prefixed semantic version"
    fi
    if [[ ! "$PROCESS_TF_VERSION_BUMP_ARCHIVE_SHA256" =~ ^[0-9a-f]{64}$ ]]; then
        processing_setup_error "tf-version-bump archive SHA-256 must be 64 lowercase hexadecimal characters"
    fi

    local release_version=${PROCESS_TF_VERSION_BUMP_VERSION#v}
    local archive_name="tf-version-bump_${release_version}_linux_x86_64.tar.gz"
    local archive_url="https://github.com/yesdevnull/tf-version-bump/releases/download/$PROCESS_TF_VERSION_BUMP_VERSION/$archive_name"
    local cache_directory="$RUNNER_TEMP/tf-version-bump-release-cache"
    local archive_path="$cache_directory/$PROCESS_TF_VERSION_BUMP_ARCHIVE_SHA256.tar.gz"
    local download_path="$archive_path.download.$BASHPID.$RANDOM"

    umask 077
    mkdir -p -- "$cache_directory"
    if [[ -f "$archive_path" ]]; then
        if ! printf '%s  %s\n' "$PROCESS_TF_VERSION_BUMP_ARCHIVE_SHA256" "$archive_path" \
            | sha256sum --check --status; then
            rm -f -- "$archive_path"
        fi
    fi
    if [[ ! -f "$archive_path" ]]; then
        if ! run_before_preparation_deadline "$PREPARATION_DATA_ROOT/download.log" \
            curl --fail --silent --show-error --location \
                --output "$download_path" "$archive_url"; then
            rm -f -- "$download_path"
            processing_setup_error "could not download tf-version-bump release archive"
        fi
        if ! printf '%s  %s\n' "$PROCESS_TF_VERSION_BUMP_ARCHIVE_SHA256" "$download_path" \
            | sha256sum --check --status; then
            rm -f -- "$download_path"
            processing_setup_error "tf-version-bump release archive checksum mismatch"
        fi
        mv -- "$download_path" "$archive_path"
    fi

    local tool_directory="$PREPARATION_DATA_ROOT/tf-version-bump"
    if ! remaining_preparation_seconds >/dev/null; then
        processing_setup_error "preparation deadline expired during tool setup"
    fi
    mkdir -m 700 -- "$tool_directory"
    tar -xzf "$archive_path" -C "$tool_directory"
    local binary="$tool_directory/tf-version-bump"
    if [[ -L "$binary" || ! -f "$binary" || ! -x "$binary" ]]; then
        processing_setup_error "tf-version-bump release archive did not contain the expected executable"
    fi

    local version_log="$PREPARATION_DATA_ROOT/tf-version-bump-version.log"
    if ! run_before_preparation_deadline "$version_log" "$binary" -version; then
        processing_setup_error "could not read tf-version-bump version"
    fi
    local version_output
    version_output=$(<"$version_log")
    local first_line=${version_output%%$'\n'*}
    if [[ "$first_line" != "tf-version-bump $release_version" ]]; then
        processing_setup_error "tf-version-bump reported an unexpected version"
    fi
    printf '%s\n' "$binary"
}

prepare_candidate_roots() {
    local root_index=0
    local terraform_root
    local update_status
    local data_directory
    local relative_root
    local lock_file
    local init_status
    for terraform_root in "${PREPARATION_TERRAFORM_ROOTS[@]}"; do
        root_index=$((root_index + 1))
        update_status=0
        (cd "$terraform_root" && run_before_preparation_deadline \
            "$PREPARATION_DATA_ROOT/update-$root_index.log" \
            "$PREPARATION_TF_VERSION_BUMP_BINARY" -pattern '*.tf' -config "$PREPARATION_CONFIG_PATH") \
            || update_status=$?
        if [[ "$update_status" -ne 0 ]]; then
            relative_root=$(relative_terraform_root "$terraform_root")
            write_preparation_failure_bundle \
                "branch-update" \
                "tf-version-bump" \
                "$relative_root" \
                "tf-version-bump -pattern *.tf -config $PROCESS_CONFIG_PATH" \
                "$update_status"
            if [[ "$update_status" -eq 124 || "$update_status" -eq 137 ]]; then
                processing_status_error "tf-version-bump timed out for Terraform root $relative_root"
            fi
            processing_status_error "tf-version-bump failed for Terraform root $relative_root"
        fi
        data_directory=${PREPARATION_TF_DATA_DIRECTORIES[$((root_index - 1))]}
        init_status=0
        TF_DATA_DIR="$data_directory" TF_IN_AUTOMATION=1 CHECKPOINT_DISABLE=1 \
            run_before_preparation_deadline "$PREPARATION_DATA_ROOT/init-$root_index.log" \
            terraform -chdir="$terraform_root" init -upgrade -backend=false -input=false -no-color \
            || init_status=$?
        if [[ "$init_status" -ne 0 ]]; then
            relative_root=$(relative_terraform_root "$terraform_root")
            write_preparation_failure_bundle \
                "branch-init" \
                "terraform init" \
                "$relative_root" \
                "terraform -chdir=$relative_root init -upgrade -backend=false -input=false -no-color" \
                "$init_status"
            if [[ "$init_status" -eq 124 || "$init_status" -eq 137 ]]; then
                processing_status_error "terraform init timed out for Terraform root $relative_root"
            fi
            processing_status_error "terraform init failed for Terraform root $relative_root"
        fi

        relative_root=$(relative_terraform_root "$terraform_root")
        lock_file="$terraform_root/.terraform.lock.hcl"
        if [[ -d "$data_directory/providers" ]] \
            && [[ -n "$(find "$data_directory/providers" -type f -print -quit)" ]]; then
            if [[ ! -e "$lock_file" && ! -L "$lock_file" ]]; then
                write_preparation_failure_bundle \
                    "automation" \
                    "provider lock policy" \
                    "$relative_root" \
                    "require persisted .terraform.lock.hcl" \
                    "1"
                processing_status_error "provider root did not produce a required lock file for Terraform root $relative_root"
            fi
            validate_lock_file "$PREPARATION_TARGET_CHECKOUT" "$terraform_root"
            local relative_lock_path
            if [[ "$relative_root" == "." ]]; then
                relative_lock_path=".terraform.lock.hcl"
            else
                relative_lock_path="$relative_root/.terraform.lock.hcl"
            fi
            if git -C "$PREPARATION_TARGET_CHECKOUT" check-ignore -q -- "$relative_lock_path"; then
                write_preparation_failure_bundle \
                    "automation" \
                    "provider lock policy" \
                    "$relative_root" \
                    "require tracked or untracked .terraform.lock.hcl" \
                    "1"
                processing_status_error "required provider lock file is ignored for Terraform root $relative_root"
            fi
        else
            validate_lock_file "$PREPARATION_TARGET_CHECKOUT" "$terraform_root"
        fi
    done

    validate_git_status "$PREPARATION_TARGET_CHECKOUT" "${PREPARATION_TERRAFORM_ROOTS[@]}"
}

verify_terraform_version() {
    local data_root=$1 run_before_deadline_function=$2
    : "${PROCESS_TERRAFORM_VERSION:?PROCESS_TERRAFORM_VERSION must be set}"
    if [[ ! "$PROCESS_TERRAFORM_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        processing_setup_error "Terraform version must be an exact semantic version"
    fi

    local version_log="$data_root/terraform-version.json"
    if ! "$run_before_deadline_function" "$version_log" terraform version -json; then
        processing_setup_error "could not read Terraform version"
    fi
    local reported_version
    if ! reported_version=$(jq -er '.terraform_version' "$version_log"); then
        processing_setup_error "Terraform returned invalid version JSON"
    fi
    if [[ "$reported_version" != "$PROCESS_TERRAFORM_VERSION" ]]; then
        processing_setup_error "Terraform reported an unexpected version"
    fi
}

if [[ "${1:-}" == "--help" ]]; then
    usage
    exit 0
fi

if [[ "${1:-}" == "prepare" && $# -eq 1 ]]; then
    trap cleanup_preparation_temporaries EXIT
    trap 'exit 129' HUP
    trap 'exit 130' INT
    trap 'exit 143' TERM
    if ! remaining_preparation_seconds >/dev/null; then
        processing_setup_error "preparation deadline expired before workspace setup"
    fi
    prepare_workspace
    prepare_bundle_contract
    PREPARATION_TF_VERSION_BUMP_BINARY=$(install_tf_version_bump)
    verify_terraform_version "$PREPARATION_DATA_ROOT" run_before_preparation_deadline
    prepare_candidate_roots
    write_preparation_candidate_bundle
    exit 0
fi

if [[ "${1:-}" == "validate" && $# -eq 1 ]]; then
    trap cleanup_validation_temporaries EXIT
    trap 'exit 129' HUP
    trap 'exit 130' INT
    trap 'exit 143' TERM
    if ! remaining_validation_seconds >/dev/null; then
        processing_setup_error "validation deadline expired before candidate setup"
    fi
    validation_contract
    verify_terraform_version "$VALIDATION_DATA_ROOT" run_before_validation_deadline
    if [[ "$VALIDATION_PREPARATION_CLASSIFICATION" == "success" ]]; then
        apply_validation_candidate
    else
        VALIDATION_CANDIDATE_STATUS=$(git -C "$VALIDATION_TARGET_CHECKOUT" \
            status --porcelain=v1 --untracked-files=all)
    fi
    validate_candidate_roots
    exit 0
fi

usage >&2
exit 2
