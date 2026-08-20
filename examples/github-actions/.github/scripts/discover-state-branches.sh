#!/usr/bin/env bash

set -euo pipefail
set +x
export LC_ALL=C

usage() {
    cat <<'EOF'
Usage: discover-state-branches.sh --help

Discover immutable inputs for configured Terraform state branches.
EOF
}

if [[ "${1:-}" == "--help" ]]; then
    usage
    exit 0
fi

if [[ $# -ne 0 ]]; then
    usage >&2
    exit 2
fi

fail_discovery() {
    local stage=$1
    shift
    echo "discovery $stage error: $*" >&2
    exit 1
}

validate_branch_prefix() {
    local prefix=$1
    local description=$2

    [[ -n "$prefix" ]] || fail_discovery input "$description must not be empty"
    [[ "$prefix" != /* ]] || fail_discovery input "$description must not be absolute-looking"
    [[ ! "$prefix" =~ [[:cntrl:]] ]] \
        || fail_discovery input "$description must not contain control characters"
    git check-ref-format "refs/heads/${prefix}placeholder" >/dev/null 2>&1 \
        || fail_discovery input "$description is not a valid literal branch prefix"
}

expected_caller_ref="refs/heads/$DISCOVERY_DEFAULT_BRANCH"
[[ "$DISCOVERY_CALLER_REF" == "$expected_caller_ref" ]] \
    || fail_discovery caller "caller ref must be $expected_caller_ref"
# shellcheck disable=SC2153 # Set by the reusable workflow or the focused harness.
[[ "$CONTROL_CHECKOUT" == /* && -d "$CONTROL_CHECKOUT" ]] \
    || fail_discovery input "CONTROL_CHECKOUT must be an absolute existing directory"
control_checkout=$(realpath "$CONTROL_CHECKOUT")

run_id=$DISCOVERY_RUN_ID
run_attempt=$DISCOVERY_RUN_ATTEMPT
policy_id=$DISCOVERY_POLICY_ID
control_oid=$DISCOVERY_CONTROL_OID
[[ "$policy_id" =~ ^[a-z0-9][a-z0-9-]{0,31}$ ]] \
    || fail_discovery input "automation policy ID must match ^[a-z0-9][a-z0-9-]{0,31}$"

[[ -n "$DISCOVERY_ALLOWED_PREFIXES" ]] || fail_discovery input "allowed prefixes must not be empty"
readarray -t allowed_prefixes < <(printf '%s' "$DISCOVERY_ALLOWED_PREFIXES")
for prefix in "${allowed_prefixes[@]}"; do
    validate_branch_prefix "$prefix" "allowed prefix"
done

selection_prefixes=("${allowed_prefixes[@]}")
if [[ -n "$DISCOVERY_MANUAL_PREFIX" ]]; then
    validate_branch_prefix "$DISCOVERY_MANUAL_PREFIX" "manual prefix"
    manual_prefix_allowed=false
    for prefix in "${allowed_prefixes[@]}"; do
        if [[ "$DISCOVERY_MANUAL_PREFIX" == "$prefix"* ]]; then
            manual_prefix_allowed=true
            break
        fi
    done
    if [[ "$manual_prefix_allowed" != true ]]; then
        echo "discovery input error: manual prefix must narrow an allowed prefix" >&2
        exit 1
    fi
    selection_prefixes=("$DISCOVERY_MANUAL_PREFIX")
fi

umask 077
remote_heads_file=$(mktemp "$RUNNER_TEMP/tf-version-bump-remote-heads.XXXXXX")
if ! git -C "$control_checkout" ls-remote --heads --refs origin >"$remote_heads_file"; then
    rm -f -- "$remote_heads_file"
    fail_discovery remote "could not list remote branches"
fi

branch_records=()
while IFS=$'\t' read -r oid ref; do
    branch=${ref#refs/heads/}
    for prefix in "${selection_prefixes[@]}"; do
        if [[ "$branch" == "$prefix"* ]]; then
            branch_records+=("$branch"$'\t'"$oid")
            break
        fi
    done
done <"$remote_heads_file"
rm -f -- "$remote_heads_file"

[[ ${#branch_records[@]} -gt 0 ]] \
    || fail_discovery selection "no remote branches matched the configured prefixes"
[[ ${#branch_records[@]} -le 256 ]] \
    || fail_discovery matrix "more than 256 branches matched; narrow or partition the prefix policy"

readarray -t branch_records < <(printf '%s\n' "${branch_records[@]}" | sort)
entries=()
for record in "${branch_records[@]}"; do
    IFS=$'\t' read -r branch oid <<<"$record"
    ref="refs/heads/$branch"
    ref_hash=$(printf '%s' "$ref" | sha256sum | awk '{print $1}')
    entries+=("$(jq -cn \
        --arg run_id "$run_id" \
        --arg run_attempt "$run_attempt" \
        --arg policy_id "$policy_id" \
        --arg control_oid "$control_oid" \
        --arg branch "$branch" \
        --arg base_oid "$oid" \
        --arg ref_hash "$ref_hash" \
        '{
            run_id: $run_id,
            run_attempt: $run_attempt,
            automation_policy_id: $policy_id,
            control_oid: $control_oid,
            branch: $branch,
            base_oid: $base_oid,
            ref_hash: $ref_hash
        }')")
done

printf '%s\n' "${entries[@]}" | jq -s '{include: .}'
