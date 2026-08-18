#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
SCRIPT="$SCRIPT_DIR/update-branches.sh"
PROJECT_ROOT=$(cd "$SCRIPT_DIR/.." && pwd)
TEST_ROOT=$(mktemp -d)
TF_VERSION_BUMP="$TEST_ROOT/tf-version-bump"

cleanup() {
    rm -rf "$TEST_ROOT"
}

trap cleanup EXIT

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

install_git_wrapper() {
    if [[ -z "${TFVB_GIT_WRAPPER:-}" ]]; then
        return
    fi

    mkdir -p "$TEST_ROOT/bin"
    ln -s "$TFVB_GIT_WRAPPER" "$TEST_ROOT/bin/git"
    export PATH="$TEST_ROOT/bin:$PATH"
}

build_tf_version_bump() {
    GOCACHE="$TEST_ROOT/go-cache" go build -o "$TF_VERSION_BUMP" "$PROJECT_ROOT"
}

create_repository() {
    local repository=$1

    git init -q -b main "$repository"
    git -C "$repository" config user.name "tf-version-bump tests"
    git -C "$repository" config user.email "tests@example.invalid"
    printf '%s\n' \
        'module "vpc" {' \
        '  source  = "terraform-aws-modules/vpc/aws"' \
        '  version = "1.0.0"' \
        '}' >"$repository/main.tf"
    git -C "$repository" add main.tf
    git -C "$repository" commit -q -m "initial fixture"
}

test_help_describes_required_inputs() {
    [[ -x "$SCRIPT" ]] || fail "update-branches.sh is not executable"

    local output
    output=$($SCRIPT --help)

    [[ "$output" == *"--branch-pattern"* ]] || fail "help omits --branch-pattern"
    [[ "$output" == *"--module"* ]] || fail "help omits --module"
    [[ "$output" == *"--config"* ]] || fail "help omits --config"
}

test_updates_matching_local_branches_and_restores_starting_branch() {
    local repository="$TEST_ROOT/local-branches"
    create_repository "$repository"
    git -C "$repository" branch feature/update

    "$SCRIPT" \
        --repository "$repository" \
        --branch-pattern 'feature/*' \
        --module 'terraform-aws-modules/vpc/aws' \
        --to '2.0.0' \
        --binary "$TF_VERSION_BUMP"

    local current_branch
    current_branch=$(git -C "$repository" branch --show-current)
    [[ "$current_branch" == "main" ]] || fail "script did not restore the starting branch"

    local feature_file
    feature_file=$(git -C "$repository" show feature/update:main.tf)
    [[ "$feature_file" == *'version = "2.0.0"'* ]] || fail "matching branch was not updated"

    local main_file
    main_file=$(git -C "$repository" show main:main.tf)
    [[ "$main_file" == *'version = "1.0.0"'* ]] || fail "non-matching branch was changed"

    local feature_subject
    feature_subject=$(git -C "$repository" log -1 --format=%s feature/update)
    [[ "$feature_subject" == "chore: bump terraform-aws-modules/vpc/aws to 2.0.0" ]] || fail "update was not committed"
}

install_git_wrapper
build_tf_version_bump
test_help_describes_required_inputs
test_updates_matching_local_branches_and_restores_starting_branch
echo "PASS: update-branches.sh"
