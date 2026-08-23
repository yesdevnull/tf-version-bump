#!/usr/bin/env bash
set -euo pipefail

usage() {
    cat <<'USAGE'
Usage: examples/run-scenarios.sh

Build tf-version-bump and verify the maintained force-add and idempotency
examples in an isolated temporary directory.
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

binary="$workspace/tf-version-bump"
if ! (
    cd -- "$repository_root"
    GOCACHE="$workspace/go-cache" go build -o "$binary" .
) >"$workspace/build.stdout" 2>"$workspace/build.stderr"; then
    sed -n '1,40p' "$workspace/build.stderr" >&2
    fail "could not build tf-version-bump"
fi

force_add_directory="$workspace/force-add"
mkdir -p -- "$force_add_directory"
cp -- "$repository_root/examples/scenarios/force-add/main.tf" "$force_add_directory/main.tf"
cp -- "$repository_root/examples/scenarios/force-add/config.yml" "$force_add_directory/config.yml"

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
mkdir -p -- "$idempotency_directory"
cp -- "$repository_root/examples/scenarios/idempotency/main.tf" "$idempotency_directory/main.tf"
cp -- "$repository_root/examples/scenarios/idempotency/config.yml" "$idempotency_directory/config.yml"

"$binary" -pattern "$idempotency_directory/main.tf" -config "$idempotency_directory/config.yml" \
    >"$workspace/idempotency-first.stdout" 2>"$workspace/idempotency-first.stderr"
grep -F 'required_version = ">= 1.5"' "$idempotency_directory/main.tf" >/dev/null \
    || fail "idempotency scenario did not update Terraform required_version"
grep -F 'version = "~> 5.0"' "$idempotency_directory/main.tf" >/dev/null \
    || fail "idempotency scenario did not update the provider version"
grep -F 'version = "2.0.0"' "$idempotency_directory/main.tf" >/dev/null \
    || fail "idempotency scenario did not update the module version"

cp -- "$idempotency_directory/main.tf" "$workspace/idempotency-first.tf"
touch -t 200001010000 "$idempotency_directory/main.tf"
first_modification_time=$(file_modification_time "$idempotency_directory/main.tf")
"$binary" -pattern "$idempotency_directory/main.tf" -config "$idempotency_directory/config.yml" \
    >"$workspace/idempotency-second.stdout" 2>"$workspace/idempotency-second.stderr"
second_modification_time=$(file_modification_time "$idempotency_directory/main.tf")
cmp -s "$idempotency_directory/main.tf" "$workspace/idempotency-first.tf" \
    || fail "second idempotency run changed Terraform bytes"
[[ $second_modification_time == "$first_modification_time" ]] \
    || fail "second idempotency run changed the Terraform modification time"
grep -F 'No updates were performed.' "$workspace/idempotency-second.stdout" >/dev/null \
    || fail "second idempotency run did not report an already-current configuration"

printf 'Example scenarios passed\n'
