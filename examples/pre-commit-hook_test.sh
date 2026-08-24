#!/usr/bin/env bash

set -euo pipefail

fail() {
    printf 'Pre-commit hook test failure: %s\n' "$*" >&2
    exit 1
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

for dependency in chmod cmp cp env git go grep mkdir mktemp sed; do
    require_command "$dependency"
done

script_directory=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
repository_root=$(cd -- "$script_directory/.." && pwd -P)
workspace=$(mktemp -d "${TMPDIR:-/tmp}/tf-version-bump-pre-commit.XXXXXX")
cleanup() {
    rm -rf -- "$workspace"
}
trap cleanup EXIT

binary="$workspace/tf-version-bump"
if ! (
    cd -- "$repository_root"
    GOCACHE="$workspace/go-cache" go build -o "$binary" .
) >"$workspace/build.stdout" 2>"$workspace/build.stderr"; then
    fail "could not build tf-version-bump"
fi

test_repository="$workspace/repository"
mkdir -p -- "$test_repository/nested"
git -C "$test_repository" init --quiet
hook="$test_repository/.git/hooks/pre-commit"
cp -- "$repository_root/examples/pre-commit-hook.sh" "$hook"
chmod 755 "$hook"

set +e
"$hook" --help >"$workspace/help.stdout" 2>"$workspace/help.stderr"
help_exit=$?
set -e
[[ $help_exit == 0 ]] || fail "hook help exited $help_exit, want 0"
grep -F 'Usage: pre-commit-hook.sh' "$workspace/help.stdout" >/dev/null \
    || fail "hook help omits usage"

cat >"$test_repository/main.tf" <<'EOF'
module "example" {
  source  = "example/module"
  version = "2.0.0"
}
EOF
cat >"$test_repository/versions.yml" <<'EOF'
modules:
  - source: example/module
    version: 2.0.0
EOF
git -C "$test_repository" add main.tf versions.yml

run_hook() {
    local name=$1
    shift
    set +e
    (
        cd -- "$test_repository/nested"
        env TF_VERSION_BUMP_BIN="$binary" "$@" "$hook"
    ) >"$workspace/$name.stdout" 2>"$workspace/$name.stderr"
    hook_exit=$?
    set -e
}

run_hook clean
[[ $hook_exit == 0 ]] || fail "clean repository exited $hook_exit, want 0"

sed 's/version: 2.0.0/version: 3.0.0/' "$test_repository/versions.yml" \
    >"$workspace/update-versions.yml"
cp -- "$workspace/update-versions.yml" "$test_repository/versions.yml"
git -C "$test_repository" add versions.yml
sed 's/version = "2.0.0"/version = "3.0.0"/' "$test_repository/main.tf" \
    >"$workspace/current-main.tf"
cp -- "$workspace/current-main.tf" "$test_repository/main.tf"
cp -- "$test_repository/main.tf" "$workspace/main-before-check.tf"
run_hook updates-required
[[ $hook_exit == 2 ]] || fail "outdated repository exited $hook_exit, want 2"
grep -F 'updates are required' "$workspace/updates-required.stderr" >/dev/null \
    || fail "update-required diagnostic is missing"
cmp -s "$test_repository/main.tf" "$workspace/main-before-check.tf" \
    || fail "update-required check changed Terraform files"

cat >"$test_repository/invalid.yml" <<'EOF'
unknown_field: true
EOF
git -C "$test_repository" add invalid.yml
run_hook invalid-config TF_VERSION_BUMP_CONFIG=invalid.yml TF_VERSION_BUMP_PATTERN='['
[[ $hook_exit == 1 ]] || fail "invalid configuration exited $hook_exit, want 1"
grep -F 'configuration validation failed: invalid.yml' "$workspace/invalid-config.stderr" >/dev/null \
    || fail "invalid configuration diagnostic is missing"
if grep -F 'pattern' "$workspace/invalid-config.stderr" >/dev/null; then
    fail "file selection ran before standalone configuration validation"
fi

run_hook invalid-pattern TF_VERSION_BUMP_PATTERN='['
[[ $hook_exit == 1 ]] || fail "invalid pattern exited $hook_exit, want 1"
grep -F 'pattern' "$workspace/invalid-pattern.stderr" >/dev/null \
    || fail "processing failure diagnostic is missing"

run_hook absolute-config TF_VERSION_BUMP_CONFIG="$test_repository/versions.yml"
[[ $hook_exit == 1 ]] || fail "absolute configuration path exited $hook_exit, want 1"
grep -F 'must be repository-relative' "$workspace/absolute-config.stderr" >/dev/null \
    || fail "absolute configuration path diagnostic is missing"

run_hook parent-pattern TF_VERSION_BUMP_PATTERN='../*.tf'
[[ $hook_exit == 1 ]] || fail "parent-traversing pattern exited $hook_exit, want 1"
grep -F 'must be repository-relative' "$workspace/parent-pattern.stderr" >/dev/null \
    || fail "parent-traversing pattern diagnostic is missing"

printf 'Pre-commit hook examples passed\n'
