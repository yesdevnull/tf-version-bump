#!/usr/bin/env bash

set -euo pipefail
set +x
export LC_ALL=C

usage() {
    cat <<'EOF'
Usage: process-state-branch.sh process
       process-state-branch.sh mask
       process-state-branch.sh --help

Update, initialise, optionally format, then validate every configured Terraform root.
Requires PROCESS_CONTROL_CHECKOUT and PROCESS_TARGET_CHECKOUT (distinct absolute Git
checkouts), PROCESS_CONFIG_PATH (relative to control), PROCESS_TERRAFORM_ROOTS (newline
separated relative directories), and RUNNER_TEMP outside both checkouts.

Identity: PROCESS_RUN_ID, PROCESS_RUN_ATTEMPT, PROCESS_AUTOMATION_POLICY_ID,
PROCESS_CONTROL_OID, PROCESS_STATE_BRANCH, PROCESS_BASE_OID, PROCESS_REF_HASH.
Tools: PROCESS_TF_VERSION_BUMP_VERSION (exact v-prefixed release),
PROCESS_TF_VERSION_BUMP_ARCHIVE_SHA256, PROCESS_TERRAFORM_VERSION (exact version).
Options: PROCESS_TERRAFORM_FMT (true/false), PROCESS_TERRAFORM_INIT_UPGRADE
(optional true/false, default false), PROCESS_PREPARATION_DEADLINE_EPOCH (absolute
deadline shared by setup and all commands), PROCESS_RESULT_DIR (absent destination
below RUNNER_TEMP), PROCESS_TERRAFORM_ENV and PROCESS_TERRAFORM_SECRET_ENV (optional
newline separated NAME=VALUE entries supplied to terraform init, fmt and validate;
within a value \n is a newline and \\ a backslash, and reserved names are rejected).

The mask subcommand parses both environment sources and prints one ::add-mask::
workflow command per non-empty PROCESS_TERRAFORM_SECRET_ENV value; run it before
process. For an invalid environment it prints the name-only diagnostic, emits no
masks and exits 0, so process reports the failure.

Emits result.json and logs/, plus candidate.patch only for changed, validated
success. Plain init uses -backend=false -input=false; explicit upgrade adds
-upgrade. Every root uses its own temporary TF_DATA_DIR. No commits are created.
EOF
}

DATA_ROOT=""
RESULT_STAGE=""
CONTROL_CHECKOUT=""
TARGET_CHECKOUT=""
CONFIG_PATH=""
TERRAFORM_ROOTS=()
TF_DATA_DIRECTORIES=()
ROOTS_JSON='[]'
TERRAFORM_ENVIRONMENT=()
SECRET_ENVIRONMENT_COUNT=0
UNESCAPED_VALUE=''
# Names the automation or the runner sets, and names that would redirect the programs
# Terraform runs or its configuration, credentials, logging or plug-in sources; a
# supplied entry must never shadow them.
RESERVED_ENVIRONMENT_PREFIXES=(PROCESS_ RECONCILE_ DISCOVERY_ RUNNER_ ACTIONS_ LD_ DYLD_
    TF_CLI_ARGS TF_LOG TF_PLUGIN_CACHE GIT_)
# TF_TOKEN_app_terraform_io is reserved, and parse_terraform_environment also rejects
# every other TF_TOKEN_ name Terraform maps to the same host: each would shadow the
# registry token the workflow injects, while other registries' TF_TOKEN_ names remain a
# legitimate use.
# GITHUB_ is not a reserved prefix, because the GitHub provider reads its credentials
# from GITHUB_ names and a supplied entry reaches only the terraform process. The
# GITHUB_ names reserved here are the runner's command channels, not provider settings.
RESERVED_ENVIRONMENT_NAMES=(PATH IFS ENV BASH_ENV SHELLOPTS BASHOPTS TF_DATA_DIR TF_IN_AUTOMATION
    CHECKPOINT_DISABLE TF_CLI_CONFIG_FILE TERRAFORM_CONFIG TF_WORKSPACE HOME TMPDIR SSL_CERT_FILE
    SSL_CERT_DIR TF_TOKEN_app_terraform_io GITHUB_ENV GITHUB_PATH GITHUB_OUTPUT GITHUB_STEP_SUMMARY
    GITHUB_STATE)

processing_path_error() { echo "processing path error: $*" >&2; exit 1; }
processing_status_error() { echo "processing status error: $*" >&2; exit 1; }
processing_setup_error() { echo "processing setup error: $*" >&2; exit 1; }

# shellcheck disable=SC2329 # Called by the EXIT trap.
cleanup() {
    local command_status=$?
    if [[ "$command_status" -ne 0 && -n "$RESULT_STAGE" && -d "$RESULT_STAGE" ]]; then
        write_result automation processing "$(jq -r '.[0]' <<<"$ROOTS_JSON")" "$command_status"
    fi
    [[ -z "$DATA_ROOT" ]] || rm -rf -- "$DATA_ROOT"
    [[ -z "$RESULT_STAGE" ]] || rm -rf -- "$RESULT_STAGE"
}

run_bounded() {
    local log=$1 remaining
    shift
    remaining=$((PROCESS_PREPARATION_DEADLINE_EPOCH - $(date +%s)))
    [[ "$remaining" -gt 0 ]] || return 124
    timeout --signal=TERM --kill-after=1s "${remaining}s" "$@" >"$log" 2>&1
}

# Translates \n to a newline and \\ to one backslash in a single left-to-right pass,
# leaving any other backslash sequence as it is, so a multi-line credential fits on
# one line. The result goes to UNESCAPED_VALUE because command substitution would
# strip the trailing newline a PEM ends with.
unescape_environment_value() {
    local remaining=$1 prefix
    UNESCAPED_VALUE=''
    while [[ "$remaining" == *\\* ]]; do
        prefix=${remaining%%\\*}
        UNESCAPED_VALUE+=$prefix
        remaining=${remaining#"$prefix"\\}
        case "$remaining" in
            n*) UNESCAPED_VALUE+=$'\n'; remaining=${remaining#n} ;;
            \\*) UNESCAPED_VALUE+="\\"; remaining=${remaining#\\} ;;
            *) UNESCAPED_VALUE+="\\" ;;
        esac
    done
    UNESCAPED_VALUE+=$remaining
}

# Diagnostics name the offending variable only; a supplied value is never printed.
parse_terraform_environment() {
    local entry name candidate host
    for entry in "$@"; do
        [[ -n "$entry" ]] || continue
        name=${entry%%=*}
        [[ "$entry" == *=* && "$name" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] \
            || processing_setup_error 'Terraform environment entries must be one NAME=VALUE per line; write a newline inside a value as \n'
        [[ "$entry" != *$'\r'* ]] \
            || processing_setup_error "Terraform environment value for $name must not contain a carriage return"
        for candidate in "${RESERVED_ENVIRONMENT_PREFIXES[@]}"; do
            [[ "$name" != "$candidate"* ]] \
                || processing_setup_error "Terraform environment name $name is reserved"
        done
        for candidate in "${RESERVED_ENVIRONMENT_NAMES[@]}"; do
            [[ "$name" != "$candidate" ]] \
                || processing_setup_error "Terraform environment name $name is reserved"
        done
        # Terraform reads a TF_TOKEN_ name's host by turning __ into - and then _ into .,
        # and compares hosts in lower case.
        host=${name#TF_TOKEN_}
        host=${host//__/-}
        host=${host//_/.}
        [[ "$name" != TF_TOKEN_* || "${host,,}" != app.terraform.io ]] \
            || processing_setup_error "Terraform environment name $name is reserved"
        for candidate in "${TERRAFORM_ENVIRONMENT[@]}"; do
            [[ "${candidate%%=*}" != "$name" ]] \
                || processing_setup_error "Terraform environment name $name is set twice"
        done
        unescape_environment_value "${entry#*=}"
        TERRAFORM_ENVIRONMENT+=("$name=$UNESCAPED_VALUE")
    done
}

prepare_environment() {
    local -a entries=()
    readarray -t entries <<<"${PROCESS_TERRAFORM_SECRET_ENV-}"
    parse_terraform_environment "${entries[@]}"
    SECRET_ENVIRONMENT_COUNT=${#TERRAFORM_ENVIRONMENT[@]}
    readarray -t entries <<<"${PROCESS_TERRAFORM_ENV-}"
    parse_terraform_environment "${entries[@]}"
}

# Registers one mask per value, never one per line: a short line would redact every
# occurrence of itself throughout the log. Workflow command data encodes per cent and
# line feed, and per cent must go first or the line feed's encoding would be encoded
# again. No carriage return needs encoding, because the parser rejects them.
print_secret_masks() {
    local index=0 value
    while [[ "$index" -lt "$SECRET_ENVIRONMENT_COUNT" ]]; do
        value=${TERRAFORM_ENVIRONMENT[index]#*=}
        index=$((index + 1))
        [[ -n "$value" ]] || continue
        value=${value//%/%25}
        printf '::add-mask::%s\n' "${value//$'\n'/%0A}"
    done
}

prepare_contract() {
    local name
    for name in RUN_ID RUN_ATTEMPT AUTOMATION_POLICY_ID CONTROL_OID STATE_BRANCH BASE_OID REF_HASH \
        TF_VERSION_BUMP_VERSION TF_VERSION_BUMP_ARCHIVE_SHA256 TERRAFORM_VERSION TERRAFORM_FMT RESULT_DIR; do
        name="PROCESS_$name"
        [[ -n "${!name-}" ]] || processing_setup_error "$name must be set"
    done
    [[ "$PROCESS_RUN_ID" =~ ^[1-9][0-9]*$ && "$PROCESS_RUN_ATTEMPT" =~ ^[1-9][0-9]*$ ]] \
        || processing_setup_error 'run ID and attempt must be positive integers'
    [[ "$PROCESS_AUTOMATION_POLICY_ID" =~ ^[a-z0-9][a-z0-9-]{0,31}$ ]] \
        || processing_setup_error 'automation policy ID is invalid'
    [[ "$PROCESS_CONTROL_OID" =~ ^[0-9a-f]{40}$ && "$PROCESS_BASE_OID" =~ ^[0-9a-f]{40}$ ]] \
        || processing_setup_error 'control and base OIDs must be 40 lowercase hexadecimal characters'
    git check-ref-format "refs/heads/$PROCESS_STATE_BRANCH" >/dev/null 2>&1 \
        || processing_setup_error 'state branch is not a valid branch name'
    local hash
    hash=$(printf '%s' "refs/heads/$PROCESS_STATE_BRANCH" | sha256sum)
    [[ "$PROCESS_REF_HASH" == "${hash%% *}" ]] || processing_setup_error 'state ref hash does not match ref hash'
    [[ "$(git -C "$CONTROL_CHECKOUT" rev-parse HEAD)" == "$PROCESS_CONTROL_OID" ]] \
        || processing_setup_error 'control checkout HEAD does not match control OID'
    [[ "$(git -C "$TARGET_CHECKOUT" rev-parse HEAD)" == "$PROCESS_BASE_OID" ]] \
        || processing_setup_error 'target checkout HEAD does not match base OID'
    [[ "$PROCESS_TERRAFORM_FMT" == true || "$PROCESS_TERRAFORM_FMT" == false ]] \
        || processing_setup_error 'Terraform formatting must be true or false'
    PROCESS_TERRAFORM_INIT_UPGRADE=${PROCESS_TERRAFORM_INIT_UPGRADE-false}
    [[ "$PROCESS_TERRAFORM_INIT_UPGRADE" == true || "$PROCESS_TERRAFORM_INIT_UPGRADE" == false ]] \
        || processing_setup_error 'Terraform init upgrade must be true or false'
    [[ "$PROCESS_RESULT_DIR" == /* && ! -e "$PROCESS_RESULT_DIR" && ! -L "$PROCESS_RESULT_DIR" ]] \
        || processing_setup_error 'result destination must be absolute and absent'
    local parent
    parent=$(realpath "${PROCESS_RESULT_DIR%/*}")
    # RUNNER_TEMP is supplied by GitHub Actions (or the processing harness).
    # shellcheck disable=SC2153
    if [[ ! -d "$parent" ]] || ! path_is_within "$parent" "$(realpath "$RUNNER_TEMP")"; then
        processing_setup_error 'result parent must exist below RUNNER_TEMP'
    fi
    RESULT_STAGE=$(mktemp -d "$RUNNER_TEMP/result-stage.XXXXXX")
    mkdir "$RESULT_STAGE/logs"
    local root relative
    local -a roots=()
    for root in "${TERRAFORM_ROOTS[@]}"; do
        relative=${root#"$TARGET_CHECKOUT"/}
        [[ "$root" != "$TARGET_CHECKOUT" ]] || relative=.
        roots+=("$relative")
    done
    ROOTS_JSON=$(jq -cn '$ARGS.positional' --args -- "${roots[@]}")
}

write_result() {
    local classification=$1 stage=${2-} root=${3-} status=${4-0} digest=''
    if [[ "$classification" == success ]]; then
        digest=$(sha256sum "$RESULT_STAGE/candidate.patch")
        digest=${digest%% *}
    else
        rm -f -- "$RESULT_STAGE/candidate.patch"
    fi
    jq -n --arg run_id "$PROCESS_RUN_ID" --arg run_attempt "$PROCESS_RUN_ATTEMPT" \
        --arg policy "$PROCESS_AUTOMATION_POLICY_ID" --arg control_oid "$PROCESS_CONTROL_OID" \
        --arg branch "$PROCESS_STATE_BRANCH" --arg base_oid "$PROCESS_BASE_OID" \
        --arg ref_hash "$PROCESS_REF_HASH" --arg classification "$classification" \
        --argjson roots "$ROOTS_JSON" --arg digest "$digest" \
        --arg stage "$stage" --arg root "$root" --argjson status "$status" \
        '{schema_version: 3, run_id: $run_id, run_attempt: $run_attempt,
          automation_policy_id: $policy, control_oid: $control_oid,
          state_branch: $branch, base_oid: $base_oid, ref_hash: $ref_hash,
          classification: $classification, roots: $roots}
        + (if $classification == "success" then {patch_sha256: $digest}
           elif $stage != "" then {failure: {stage: $stage, root: $root, status: $status}}
           else {} end)' >"$RESULT_STAGE/result.json"
    mv -- "$RESULT_STAGE" "$PROCESS_RESULT_DIR"
    RESULT_STAGE=''
}

branch_command() {
    local classification=$1 stage=$2 root=$3 log=$4 command_status=0
    shift 4
    run_bounded "$RESULT_STAGE/logs/$log" "$@" || command_status=$?
    if [[ "$command_status" -ne 0 ]]; then
        write_result "$classification" "$stage" "$root" "$command_status"
        processing_status_error "$stage failed for Terraform root $root"
    fi
}

terraform_command() {
    local classification=$1 stage=$2 root=$3 log=$4
    shift 4
    branch_command "$classification" "$stage" "$root" "$log" \
        env -- "${TERRAFORM_ENVIRONMENT[@]}" terraform "$@"
}

install_tools() {
    [[ "$PROCESS_TF_VERSION_BUMP_VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] \
        || processing_setup_error 'tf-version-bump version must be a v-prefixed semantic version'
    [[ "$PROCESS_TF_VERSION_BUMP_ARCHIVE_SHA256" =~ ^[0-9a-f]{64}$ ]] \
        || processing_setup_error 'tf-version-bump archive SHA-256 must be 64 lowercase hexadecimal characters'
    [[ "$PROCESS_TERRAFORM_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] \
        || processing_setup_error 'Terraform version must be an exact semantic version'
    local version=${PROCESS_TF_VERSION_BUMP_VERSION#v} archive="$DATA_ROOT/updater.tar.gz"
    local url="https://github.com/yesdevnull/tf-version-bump/releases/download/$PROCESS_TF_VERSION_BUMP_VERSION/tf-version-bump_${version}_linux_x86_64.tar.gz"
    run_bounded "$RESULT_STAGE/logs/download.log" curl --fail --silent --show-error --location \
        --output "$archive" "$url" || processing_setup_error 'could not download tf-version-bump release archive'
    printf '%s  %s\n' "$PROCESS_TF_VERSION_BUMP_ARCHIVE_SHA256" "$archive" | sha256sum --check --status \
        || processing_setup_error 'tf-version-bump release archive checksum mismatch'
    tar -xzf "$archive" -C "$DATA_ROOT" tf-version-bump
    run_bounded "$RESULT_STAGE/logs/updater-version.log" "$DATA_ROOT/tf-version-bump" -version \
        || processing_setup_error 'could not read tf-version-bump version'
    [[ "$(head -n 1 "$RESULT_STAGE/logs/updater-version.log")" == "tf-version-bump $version" ]] \
        || processing_setup_error 'tf-version-bump reported an unexpected version'
    run_bounded "$RESULT_STAGE/logs/terraform-version.json" terraform version -json \
        || processing_setup_error 'could not read Terraform version'
    [[ "$(jq -er '.terraform_version' "$RESULT_STAGE/logs/terraform-version.json")" == "$PROCESS_TERRAFORM_VERSION" ]] \
        || processing_setup_error 'Terraform reported an unexpected version'
}

process_roots() {
    local root relative index=0 data
    local -a init_args=(init -backend=false -input=false -no-color)
    [[ "$PROCESS_TERRAFORM_INIT_UPGRADE" != true ]] || init_args+=(-upgrade)
    for root in "${TERRAFORM_ROOTS[@]}"; do
        index=$((index + 1))
        relative=$(jq -r --argjson i "$((index - 1))" '.[$i]' <<<"$ROOTS_JSON")
        # The updater resolves its glob relative to its working directory.
        (cd "$root" && branch_command branch-update tf-version-bump "$relative" "update-$index.log" \
            "$DATA_ROOT/tf-version-bump" -pattern '*.tf' -config "$CONFIG_PATH")
    done
    # Local module references must see every root's final dependency constraints during init.
    index=0
    for root in "${TERRAFORM_ROOTS[@]}"; do
        index=$((index + 1))
        relative=$(jq -r --argjson i "$((index - 1))" '.[$i]' <<<"$ROOTS_JSON")
        data=${TF_DATA_DIRECTORIES[$((index - 1))]}
        TF_DATA_DIR="$data" TF_IN_AUTOMATION=1 CHECKPOINT_DISABLE=1 \
            terraform_command branch-init 'terraform init' "$relative" "init-$index.log" -chdir="$root" "${init_args[@]}"
        validate_lock_file "$TARGET_CHECKOUT" "$root"
        if [[ -f "$root/.terraform.lock.hcl" ]] && git -C "$TARGET_CHECKOUT" check-ignore -q -- "$root/.terraform.lock.hcl"; then
            write_result automation 'provider lock policy' "$relative" 1
            processing_status_error "required provider lock file is ignored for Terraform root $relative"
        fi
    done
    local candidate_status
    candidate_status=$(git -C "$TARGET_CHECKOUT" status --porcelain=v1 --untracked-files=all)
    if [[ "$PROCESS_TERRAFORM_FMT" == true && -n "$candidate_status" ]]; then
        index=0
        for root in "${TERRAFORM_ROOTS[@]}"; do
            index=$((index + 1))
            relative=$(jq -r --argjson i "$((index - 1))" '.[$i]' <<<"$ROOTS_JSON")
            terraform_command branch-format 'terraform fmt' "$relative" "fmt-$index.log" -chdir="$root" fmt -recursive -no-color
        done
    fi
    index=0
    for root in "${TERRAFORM_ROOTS[@]}"; do
        index=$((index + 1))
        relative=$(jq -r --argjson i "$((index - 1))" '.[$i]' <<<"$ROOTS_JSON")
        data=${TF_DATA_DIRECTORIES[$((index - 1))]}
        TF_DATA_DIR="$data" TF_IN_AUTOMATION=1 CHECKPOINT_DISABLE=1 \
            terraform_command branch-validation 'terraform validate' "$relative" "validate-$index.log" -chdir="$root" validate -no-color
    done
}

write_candidate() {
    local index="$DATA_ROOT/candidate.index" raw path old_mode new_mode
    GIT_INDEX_FILE="$index" git -C "$TARGET_CHECKOUT" read-tree HEAD
    GIT_INDEX_FILE="$index" git -C "$TARGET_CHECKOUT" add --all --force -- .
    GIT_INDEX_FILE="$index" git -C "$TARGET_CHECKOUT" diff --cached --raw -z --no-renames >"$DATA_ROOT/changes"
    while IFS= read -r -d '' raw && IFS= read -r -d '' path; do
        read -r old_mode new_mode _ <<<"$raw"
        [[ "$old_mode" == :000000 || "$old_mode" == ":$new_mode" ]] \
            || processing_status_error 'candidate deletion or type change is forbidden'
        [[ "$old_mode" != :000000 ]] || validate_created_changed_path "$path"
        validate_changed_tree_identity "$path" "$new_mode"
        validate_changed_tree_entry "$TARGET_CHECKOUT" "$path"
        validate_final_changed_path "$TARGET_CHECKOUT" "$path" "${TERRAFORM_ROOTS[@]}"
    done <"$DATA_ROOT/changes"
    GIT_INDEX_FILE="$index" git -C "$TARGET_CHECKOUT" diff --cached --binary --full-index --no-color >"$RESULT_STAGE/candidate.patch"
    if [[ -s "$RESULT_STAGE/candidate.patch" ]]; then write_result success; else write_result no-change; fi
}

path_is_within() {
    local path=$1
    local root=$2
    [[ "$path" == "$root" || "$path" == "$root/"* ]]
}


# prepare_workspace requires the checkout to be free of tracked changes, untracked
# and ignored files, so a path with no previous mode was created during this run.
# Only terraform init is expected to create one, its provider lock file; the updater
# and formatter rewrite existing files. validate_final_changed_path then confirms
# the declared root.
validate_created_changed_path() {
    local relative_path=$1
    [[ "${relative_path##*/}" == ".terraform.lock.hcl" ]] \
        || processing_status_error "candidate created a path that is not a provider lock file"
}


validate_changed_tree_identity() {
    local relative_path=$1 git_mode=$2
    [[ -n "$relative_path" && "$relative_path" != /* && "/$relative_path/" != *"/../"* ]] \
        || processing_status_error "changed path is unsafe"
    if [[ "$relative_path" == *$'\n'* ]]; then
        processing_status_error "changed path must not contain a newline"
    fi
    local raw_digest normalised_digest
    raw_digest=$(printf '%s' "$relative_path" | sha256sum)
    normalised_digest=$(printf '%s' "$relative_path" | jq -Rjsc . | sha256sum)
    [[ "${raw_digest%% *}" == "${normalised_digest%% *}" ]] \
        || processing_status_error "changed path is not valid UTF-8"
    [[ "$git_mode" == "100644" ]] \
        || processing_status_error "changed file mode must be 100644"
}


validate_changed_tree_entry() {
    local target_checkout=$1 relative_path=$2
    local changed_path="$target_checkout/$relative_path"
    [[ -f "$changed_path" && ! -L "$changed_path" ]] \
        || processing_status_error "changed path must be a regular non-symlink file"
    local canonical_path
    if ! canonical_path=$(realpath "$changed_path" 2>/dev/null) \
        || ! path_is_within "$canonical_path" "$target_checkout"; then
        processing_status_error "changed path resolves outside target checkout"
    fi
}


validate_formatting_changed_path() {
    local target_checkout=$1 relative_path=$2
    shift 2
    local -a terraform_roots=("$@")
    [[ "$relative_path" == *.tf ]] \
        || processing_status_error "formatting changed a non-Terraform path"
    [[ "/$relative_path/" != *"/.terraform/"* ]] \
        || processing_status_error "formatting changed a path beneath .terraform"

    local terraform_root relative_root
    for terraform_root in "${terraform_roots[@]}"; do
        if [[ "$terraform_root" == "$target_checkout" ]]; then
            relative_root="."
        else
            relative_root=${terraform_root#"$target_checkout/"}
        fi
        if [[ "$relative_root" == "." || "$relative_path" == "$relative_root/"* ]]; then
            return
        fi
    done
    processing_status_error "formatted Terraform file must be beneath a configured root"
}


validate_final_changed_path() {
    local target_checkout=$1 relative_path=$2
    shift 2
    local -a terraform_roots=("$@")
    local filename=${relative_path##*/} parent_path=${relative_path%/*}
    [[ "$parent_path" != "$relative_path" ]] || parent_path="."
    local terraform_root relative_root

    if [[ "$filename" == ".terraform.lock.hcl" ]]; then
        for terraform_root in "${terraform_roots[@]}"; do
            if [[ "$terraform_root" == "$target_checkout" ]]; then
                relative_root="."
            else
                relative_root=${terraform_root#"$target_checkout/"}
            fi
            [[ "$parent_path" != "$relative_root" ]] || return 0
        done
        processing_status_error "final candidate contains an undeclared lock file"
    fi
    validate_formatting_changed_path \
        "$target_checkout" "$relative_path" "${terraform_roots[@]}"
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
    done < <(find "$terraform_root" -name '*.tf' -print0)
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
    local checkout
    for checkout in "$control_checkout" "$target_checkout"; do
        [[ "$(realpath "$(git -C "$checkout" rev-parse --show-toplevel)")" == "$checkout" ]] \
            || processing_path_error 'checkout must be the Git worktree root'
        [[ -z "$(git -C "$checkout" status --porcelain=v1 --untracked-files=all --ignored=matching)" ]] \
            || processing_path_error 'checkout must be clean'
    done

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

    CONTROL_CHECKOUT=$control_checkout
    TARGET_CHECKOUT=$target_checkout
    CONFIG_PATH=$config_path
    DATA_ROOT=$data_root
    TERRAFORM_ROOTS=("${terraform_roots[@]}")
    TF_DATA_DIRECTORIES=("${tf_data_directories[@]}")
}


if [[ "${1-}" == --help && $# -eq 1 ]]; then usage; exit 0; fi
# Masking runs immediately before process in one workflow run block. An invalid
# environment must fail there, not here: process re-parses the same input and
# reports it before any Terraform command runs, so nothing unmasked is printed
# in between, and its result file keeps the artefact and publish steps honest.
# The parser's diagnostic still reaches the step log; it names the offending
# variable only, so it carries no supplied value. The subshell only decides
# whether the environment is valid, because errexit is off inside a || list: a
# valid environment is parsed again and its masks are written under errexit, so a
# mask that cannot be written fails the step rather than leaving a value unmasked.
if [[ "${1-}" == mask && $# -eq 1 ]]; then
    (prepare_environment) || exit 0
    prepare_environment
    print_secret_masks
    exit 0
fi
if [[ "${1-}" != process || $# -ne 1 ]]; then usage >&2; exit 2; fi
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
[[ "${PROCESS_PREPARATION_DEADLINE_EPOCH-}" =~ ^[0-9]+$ ]] \
    || processing_setup_error 'processing deadline must be an epoch timestamp'
[[ "$PROCESS_PREPARATION_DEADLINE_EPOCH" -gt "$(date +%s)" ]] \
    || processing_setup_error 'processing deadline expired before workspace setup'
prepare_workspace
prepare_contract
prepare_environment
install_tools
process_roots
write_candidate
