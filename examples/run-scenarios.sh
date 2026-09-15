#!/usr/bin/env bash
set -euo pipefail

usage() {
    cat <<'USAGE'
Usage: examples/run-scenarios.sh

Build tf-version-bump and verify the maintained force-add, idempotency,
provider-targeting, and same-source-ranges examples in an isolated temporary
directory.
USAGE
}

fail() {
    printf 'Example scenario failure: %s\n' "$*" >&2
    exit 1
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

file_modification_time() {
    if stat -f '%m' "$1" >/dev/null 2>&1; then
        stat -f '%m' "$1"
    else
        stat -c '%Y' "$1"
    fi
}

if [[ ${1:-} == "--help" ]]; then
    usage
    exit 0
fi
if (($# != 0)); then
    usage >&2
    exit 2
fi

for dependency in go grep cmp cp mktemp sed stat touch; do
    require_command "$dependency"
done

script_directory=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
repository_root=$(cd -- "$script_directory/.." && pwd -P)
workspace=$(mktemp -d "${TMPDIR:-/tmp}/tf-version-bump-scenarios.XXXXXX")
cleanup() {
    rm -rf -- "$workspace"
}
trap cleanup EXIT

# Copies a scenario's checked-in main.tf and config.yml into the workspace, so no run changes them.
prepare_scenario() {
    mkdir -p -- "$workspace/$1"
    cp -- "$repository_root/examples/scenarios/$1/main.tf" "$repository_root/examples/scenarios/$1/config.yml" "$workspace/$1/"
}

# Applies a scenario's config again, passing any extra flags, and fails unless the run leaves the
# Terraform bytes and modification time unchanged and reports that nothing needed updating.
assert_second_run_changes_nothing() {
    local name=$1 first_modification_time second_modification_time
    shift
    cp -- "$workspace/$name/main.tf" "$workspace/$name-first.tf"
    touch -t 200001010000 "$workspace/$name/main.tf"
    first_modification_time=$(file_modification_time "$workspace/$name/main.tf")
    "$binary" -pattern "$workspace/$name/main.tf" -config "$workspace/$name/config.yml" "$@" \
        >"$workspace/$name-second.stdout" 2>"$workspace/$name-second.stderr"
    second_modification_time=$(file_modification_time "$workspace/$name/main.tf")
    cmp -s "$workspace/$name/main.tf" "$workspace/$name-first.tf" \
        || fail "second $name run changed Terraform bytes"
    [[ $second_modification_time == "$first_modification_time" ]] \
        || fail "second $name run changed the Terraform modification time"
    grep -F 'No updates were performed.' "$workspace/$name-second.stdout" >/dev/null \
        || fail "second $name run did not report an already-current configuration"
}

binary="$workspace/tf-version-bump"
if ! (
    cd -- "$repository_root"
    GOCACHE="$workspace/go-cache" go build -o "$binary" .
) >"$workspace/build.stdout" 2>"$workspace/build.stderr"; then
    sed -n '1,40p' "$workspace/build.stderr" >&2
    fail "could not build tf-version-bump"
fi

force_add_directory="$workspace/force-add"
prepare_scenario force-add

"$binary" -pattern "$force_add_directory/main.tf" -config "$force_add_directory/config.yml" \
    >"$workspace/force-add-skip.stdout" 2>"$workspace/force-add-skip.stderr"
cmp -s "$force_add_directory/main.tf" "$repository_root/examples/scenarios/force-add/main.tf" \
    || fail "force-add scenario changed the module without -force-add"
grep -F "has no version attribute, skipping" \
    "$workspace/force-add-skip.stderr" >/dev/null \
    || fail "force-add scenario did not report the default missing-version warning"

"$binary" -pattern "$force_add_directory/main.tf" -config "$force_add_directory/config.yml" \
    -force-add >"$workspace/force-add.stdout" 2>"$workspace/force-add.stderr"
grep -F 'version = "5.0.0"' "$force_add_directory/main.tf" >/dev/null \
    || fail "force-add scenario did not add the configured module version"

idempotency_directory="$workspace/idempotency"
prepare_scenario idempotency

"$binary" -pattern "$idempotency_directory/main.tf" -config "$idempotency_directory/config.yml" \
    >"$workspace/idempotency-first.stdout" 2>"$workspace/idempotency-first.stderr"
grep -F 'required_version = ">= 1.5"' "$idempotency_directory/main.tf" >/dev/null \
    || fail "idempotency scenario did not update Terraform required_version"
grep -F 'version = "~> 5.0"' "$idempotency_directory/main.tf" >/dev/null \
    || fail "idempotency scenario did not update the provider version"
grep -F 'version = "2.0.0"' "$idempotency_directory/main.tf" >/dev/null \
    || fail "idempotency scenario did not update the module version"

assert_second_run_changes_nothing idempotency

provider_directory="$workspace/provider-targeting"
prepare_scenario provider-targeting

"$binary" -pattern "$provider_directory/main.tf" -config "$provider_directory/config.yml" -force-add \
    >"$workspace/provider-first.stdout" 2>"$workspace/provider-first.stderr"
cmp -s "$provider_directory/main.tf" \
    "$repository_root/examples/scenarios/provider-targeting/expected.tf.golden" \
    || fail "provider-targeting scenario did not produce the exact expected provider configuration"

assert_second_run_changes_nothing provider-targeting -force-add

same_source_directory="$workspace/same-source-ranges"
prepare_scenario same-source-ranges

"$binary" -pattern "$same_source_directory/main.tf" -config "$same_source_directory/config.yml" \
    >"$workspace/same-source-first.stdout" 2>"$workspace/same-source-first.stderr"
cmp -s "$same_source_directory/main.tf" \
    "$repository_root/examples/scenarios/same-source-ranges/expected.tf.golden" \
    || fail "same-source-ranges scenario did not produce the exact expected module versions"

assert_second_run_changes_nothing same-source-ranges

printf 'Example scenarios passed\n'
