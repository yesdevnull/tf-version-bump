#!/usr/bin/env bash

set -euo pipefail
set +x
export LC_ALL=C

usage() {
    cat <<'EOF'
Usage: report-state-branches.sh collect
       report-state-branches.sh --help

Compare each discovered state branch with its policy's control configuration, without
changing anything.

collect reads REPORT_BRANCHES (the discovery script's JSON), fetches each branch's
discovered commit from REPORT_CONTROL_CHECKOUT's origin into a temporary worktree and,
for every root in REPORT_TERRAFORM_ROOTS (newline separated, relative, no glob
characters), runs tf-version-bump -audit-file against REPORT_CONFIG_PATH (relative to
the control checkout) and records whether main.tf and providers.tf exist. It installs
REPORT_TF_VERSION_BUMP_VERSION after checking REPORT_TF_VERSION_BUMP_ARCHIVE_SHA256 and
writes records.json for REPORT_POLICY_ID into REPORT_OUTPUT_DIR, which must be absent.
A branch it cannot read is recorded with an error; collect still exits 0. Temporary
files live below RUNNER_TEMP.
EOF
}

WORK_ROOT=""
CONTROL_CHECKOUT=""
TOOL=""

report_error() { echo "report error: $*" >&2; exit 1; }

# shellcheck disable=SC2329 # Called by the EXIT trap.
cleanup() {
    [[ -n "$WORK_ROOT" ]] || return 0
    rm -rf -- "$WORK_ROOT"
    [[ -z "$CONTROL_CHECKOUT" ]] || git -C "$CONTROL_CHECKOUT" worktree prune
}
trap cleanup EXIT

path_is_within() {
    [[ "$1" == "$2" || "$1" == "$2/"* ]]
}

install_tool() {
    [[ "$REPORT_TF_VERSION_BUMP_VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] \
        || report_error 'tf-version-bump version must be a v-prefixed semantic version'
    [[ "$REPORT_TF_VERSION_BUMP_ARCHIVE_SHA256" =~ ^[0-9a-f]{64}$ ]] \
        || report_error 'tf-version-bump archive SHA-256 must be 64 lowercase hexadecimal characters'
    local version=${REPORT_TF_VERSION_BUMP_VERSION#v} archive="$WORK_ROOT/tf-version-bump.tar.gz"
    curl --fail --silent --show-error --location --output "$archive" \
        "https://github.com/yesdevnull/tf-version-bump/releases/download/$REPORT_TF_VERSION_BUMP_VERSION/tf-version-bump_${version}_linux_x86_64.tar.gz" \
        || report_error 'could not download the tf-version-bump release archive'
    # Compare the digest directly: the harness runs this on macOS too, whose sha256sum lacks
    # GNU's --check --status.
    local digest
    digest=$(sha256sum "$archive")
    [[ "${digest%% *}" == "$REPORT_TF_VERSION_BUMP_ARCHIVE_SHA256" ]] \
        || report_error 'tf-version-bump release archive checksum mismatch'
    tar -xzf "$archive" -C "$WORK_ROOT" tf-version-bump
    TOOL="$WORK_ROOT/tf-version-bump"
    [[ "$("$TOOL" -version | head -n 1)" == "tf-version-bump $version" ]] \
        || report_error 'tf-version-bump reported an unexpected version'
}

# Prints the JSON record of one root, or one error line on stderr and returns 1. It runs
# inside collect_branch's if condition, where errexit does not apply, so every step checks
# its own status.
collect_root() {
    local worktree=$1 root=$2 config=$3 canonical_roots=$4
    local path="$worktree/$root" canonical audit="$WORK_ROOT/audit.json"
    if [[ ! -e "$path" && ! -L "$path" ]]; then
        jq -cn --arg root "$root" \
            '{root: $root, exists: false, files: {"main.tf": false, "providers.tf": false}, audit: null}'
        return
    fi
    canonical=$(realpath "$path") || return 1
    path_is_within "$canonical" "$worktree" \
        || { echo "root $root resolves outside the checkout" >&2; return 1; }
    [[ -d "$canonical" ]] || { echo "root $root is not a directory" >&2; return 1; }
    ! grep -qxF -- "$canonical" "$canonical_roots" \
        || { echo "root $root duplicates another root" >&2; return 1; }
    printf '%s\n' "$canonical" >>"$canonical_roots"
    [[ -z "$(find "$canonical" -maxdepth 1 -name '*.tf' -type l -print -quit)" ]] \
        || { echo "root $root contains a symlinked Terraform file" >&2; return 1; }
    rm -f -- "$audit"
    if [[ -z "$(find "$canonical" -maxdepth 1 -name '*.tf' -type f -print -quit)" ]]; then
        printf '%s\n' '{"schema_version": 1, "terraform": [], "providers": [], "modules": []}' >"$audit"
    elif ! (cd "$worktree" && "$TOOL" -pattern "$root/*.tf" -config "$config" -audit-file "$audit") \
        >"$WORK_ROOT/audit.log" 2>&1; then
        local failure
        failure=$(tail -n 1 "$WORK_ROOT/audit.log")
        # The CLI logs through Go's log package, which starts each line with the date and time;
        # dropping it keeps the recorded error stable between runs.
        failure=${failure#[0-9][0-9][0-9][0-9]/[0-9][0-9]/[0-9][0-9] [0-9][0-9]:[0-9][0-9]:[0-9][0-9] }
        echo "could not audit root $root: $failure" >&2
        return 1
    fi
    local main_found=false providers_found=false
    [[ ! -f "$canonical/main.tf" ]] || main_found=true
    [[ ! -f "$canonical/providers.tf" ]] || providers_found=true
    jq -c --arg root "$root" --argjson main "$main_found" --argjson providers "$providers_found" \
        '{root: $root, exists: true, files: {"main.tf": $main, "providers.tf": $providers}, audit: .}' "$audit"
}

# Prints a JSON array of one branch's root records, or one error line on stderr and returns 1.
collect_branch() {
    local commit=$1 config=$2
    shift 2
    local worktree="$WORK_ROOT/worktree" root
    local roots_file="$WORK_ROOT/roots.jsonl" canonical_roots="$WORK_ROOT/canonical-roots"
    : >"$roots_file"
    : >"$canonical_roots"
    git -C "$CONTROL_CHECKOUT" fetch --quiet --no-tags --depth=1 origin "$commit" 2>"$WORK_ROOT/git.log" \
        || { echo "could not fetch commit $commit: $(tail -n 1 "$WORK_ROOT/git.log")" >&2; return 1; }
    git -C "$CONTROL_CHECKOUT" worktree add --quiet --detach "$worktree" "$commit" 2>"$WORK_ROOT/git.log" \
        || { echo "could not check out commit $commit: $(tail -n 1 "$WORK_ROOT/git.log")" >&2; return 1; }
    worktree=$(realpath "$worktree") || return 1
    for root in "$@"; do
        collect_root "$worktree" "$root" "$config" "$canonical_roots" >>"$roots_file" || return 1
    done
    jq -s . "$roots_file"
}

collect() {
    local name
    for name in POLICY_ID CONTROL_CHECKOUT CONFIG_PATH TERRAFORM_ROOTS BRANCHES OUTPUT_DIR \
        TF_VERSION_BUMP_VERSION TF_VERSION_BUMP_ARCHIVE_SHA256; do
        name="REPORT_$name"
        [[ -n "${!name-}" ]] || report_error "$name must be set"
    done
    [[ -n "${RUNNER_TEMP-}" && -d "$RUNNER_TEMP" ]] || report_error 'RUNNER_TEMP must be an existing directory'
    [[ "$REPORT_POLICY_ID" =~ ^[a-z0-9][a-z0-9-]{0,31}$ ]] || report_error 'policy ID is invalid'
    [[ "$REPORT_CONTROL_CHECKOUT" == /* && -d "$REPORT_CONTROL_CHECKOUT" ]] \
        || report_error 'control checkout must be an absolute existing directory'
    CONTROL_CHECKOUT=$(realpath "$REPORT_CONTROL_CHECKOUT")
    [[ "$REPORT_CONFIG_PATH" != /* && "/$REPORT_CONFIG_PATH/" != *"/../"* ]] \
        || report_error 'config path must be relative and must not contain ..'
    local config
    if ! config=$(realpath "$CONTROL_CHECKOUT/$REPORT_CONFIG_PATH" 2>/dev/null) \
        || [[ ! -f "$config" ]] || ! path_is_within "$config" "$CONTROL_CHECKOUT"; then
        report_error 'config path must name a file inside the control checkout'
    fi
    # Each root becomes part of a -pattern, so a glob character would widen the selection.
    local root glob_characters='[][*?{}\]'
    local -a roots=()
    readarray -t roots < <(printf '%s' "$REPORT_TERRAFORM_ROOTS")
    for root in "${roots[@]}"; do
        [[ -n "$root" && "$root" != /* && "/$root/" != *"/../"* ]] \
            || report_error 'Terraform roots must be non-empty relative paths without ..'
        [[ ! "$root" =~ $glob_characters ]] || report_error "Terraform root $root contains a glob character"
    done
    jq -e '.include | type == "array" and all(.[]; (.branch | type == "string") and (.base_oid | test("^[0-9a-f]{40}$")))' \
        "$REPORT_BRANCHES" >/dev/null 2>&1 || report_error 'branches file is not discovery output'
    [[ "$REPORT_OUTPUT_DIR" == /* && ! -e "$REPORT_OUTPUT_DIR" && ! -L "$REPORT_OUTPUT_DIR" ]] \
        || report_error 'output directory must be absolute and absent'

    umask 077
    WORK_ROOT=$(mktemp -d "$RUNNER_TEMP/tf-version-bump-report.XXXXXX")
    install_tool
    mkdir -p -- "$REPORT_OUTPUT_DIR"
    local branch commit error records="$WORK_ROOT/branches.jsonl"
    : >"$records"
    while IFS=$'\t' read -r branch commit; do
        if collect_branch "$commit" "$config" "${roots[@]}" >"$WORK_ROOT/branch.json" 2>"$WORK_ROOT/branch.error"; then
            jq -c --arg branch "$branch" --arg commit "$commit" \
                '{branch: $branch, commit: $commit, error: null, roots: .}' "$WORK_ROOT/branch.json" >>"$records"
        else
            error=$(tail -n 1 "$WORK_ROOT/branch.error")
            jq -cn --arg branch "$branch" --arg commit "$commit" --arg error "${error:-could not read the branch}" \
                '{branch: $branch, commit: $commit, error: $error, roots: []}' >>"$records"
        fi
        [[ ! -e "$WORK_ROOT/worktree" ]] || git -C "$CONTROL_CHECKOUT" worktree remove --force "$WORK_ROOT/worktree"
    done < <(jq -r '.include[] | [.branch, .base_oid] | @tsv' "$REPORT_BRANCHES")
    jq -s --arg policy "$REPORT_POLICY_ID" '{policy: $policy, roots: $ARGS.positional, branches: .}' \
        --args "${roots[@]}" <"$records" >"$REPORT_OUTPUT_DIR/records.json"
}

if [[ $# -ne 1 ]]; then
    usage >&2
    exit 2
fi
case "$1" in
    --help) usage ;;
    collect) collect ;;
    *) usage >&2; exit 2 ;;
esac
