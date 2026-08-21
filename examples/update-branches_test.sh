#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
SCRIPT="$SCRIPT_DIR/update-branches.sh"
PROJECT_ROOT=$(cd "$SCRIPT_DIR/.." && pwd)
TEST_ROOT=$(mktemp -d)
TF_VERSION_BUMP="$TEST_ROOT/tf-version-bump"
TEST_SIGNING_KEY="$TEST_ROOT/signing-key"
ORIGINAL_PATH=$PATH

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

create_test_signing_key() {
    if [[ -n "${TFVB_GIT_WRAPPER:-}" ]]; then
        return
    fi

    command -v ssh-keygen >/dev/null || fail "ssh-keygen is required to test signed commits"
    ssh-keygen -q -t ed25519 -N '' -f "$TEST_SIGNING_KEY"
}

build_tf_version_bump() {
    GOCACHE="$TEST_ROOT/go-cache" go build -o "$TF_VERSION_BUMP" "$PROJECT_ROOT"
}

create_repository() {
    local repository=$1

    git init -q -b main "$repository"
    git -C "$repository" config user.name "tf-version-bump tests"
    git -C "$repository" config user.email "tests@example.invalid"
    if [[ -z "${TFVB_GIT_WRAPPER:-}" ]]; then
        git -C "$repository" config gpg.format ssh
        git -C "$repository" config gpg.ssh.program ssh-keygen
        git -C "$repository" config user.signingkey "$TEST_SIGNING_KEY"
        git -C "$repository" config commit.gpgsign false
    fi
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
    [[ "$output" == *"--sign-commits"* ]] || fail "help omits --sign-commits"
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

    if [[ -z "${TFVB_GIT_WRAPPER:-}" ]] && git -C "$repository" cat-file commit feature/update | grep -q '^gpgsig '; then
        fail "update commit was signed without --sign-commits"
    fi
}

test_applies_config_file_updates() {
    local repository="$TEST_ROOT/config-mode"
    local config_file="$TEST_ROOT/updates.yml"
    create_repository "$repository"
    printf '%s\n' \
        '' \
        'terraform {' \
        '  required_version = ">= 1.5"' \
        '}' >>"$repository/main.tf"
    git -C "$repository" add main.tf
    git -C "$repository" commit -q -m "add Terraform settings"
    git -C "$repository" branch release/config
    printf '%s\n' \
        'terraform_version: ">= 1.9"' \
        'modules:' \
        '  - source: "terraform-aws-modules/vpc/aws"' \
        '    version: "3.0.0"' >"$config_file"

    "$SCRIPT" \
        --repository "$repository" \
        --branch-pattern 'release/*' \
        --config "$config_file" \
        --binary "$TF_VERSION_BUMP" \
        --sign-commits

    local updated_file
    updated_file=$(git -C "$repository" show release/config:main.tf)
    [[ "$updated_file" == *'required_version = ">= 1.9"'* ]] || fail "config Terraform version was not applied"
    [[ "$updated_file" == *'version = "3.0.0"'* ]] || fail "config module version was not applied"

    local subject
    subject=$(git -C "$repository" log -1 --format=%s release/config)
    [[ "$subject" == "chore: apply tf-version-bump config" ]] || fail "config update used the wrong commit message"

    git -C "$repository" cat-file commit release/config | grep -q '^gpgsig ' || fail "--sign-commits did not sign the update commit"
}

test_dry_run_leaves_branches_unchanged() {
    local repository="$TEST_ROOT/dry-run"
    create_repository "$repository"
    git -C "$repository" branch feature/preview

    local original_head
    original_head=$(git -C "$repository" rev-parse feature/preview)

    "$SCRIPT" \
        --repository "$repository" \
        --branch-pattern 'feature/*' \
        --module 'terraform-aws-modules/vpc/aws' \
        --to '2.0.0' \
        --binary "$TF_VERSION_BUMP" \
        --dry-run

    local current_head
    current_head=$(git -C "$repository" rev-parse feature/preview)
    [[ "$current_head" == "$original_head" ]] || fail "dry run created a commit"

    local feature_file
    feature_file=$(git -C "$repository" show feature/preview:main.tf)
    [[ "$feature_file" == *'version = "1.0.0"'* ]] || fail "dry run changed a Terraform file"
}

test_remote_dry_run_does_not_create_local_branches() {
    local repository="$TEST_ROOT/remote-dry-run"
    local remote="$TEST_ROOT/preview-origin.git"
    create_repository "$repository"
    git -C "$repository" branch feature/preview-remote
    git init -q --bare "$remote"
    git -C "$repository" remote add origin "$remote"
    git -C "$repository" push -q origin main feature/preview-remote
    git -C "$repository" branch -D feature/preview-remote >/dev/null

    "$SCRIPT" \
        --repository "$repository" \
        --branch-pattern 'feature/*' \
        --module 'terraform-aws-modules/vpc/aws' \
        --to '2.0.0' \
        --binary "$TF_VERSION_BUMP" \
        --include-remotes \
        --dry-run

    if git -C "$repository" show-ref --verify --quiet refs/heads/feature/preview-remote; then
        fail "remote dry run created a local branch"
    fi

    local remote_file
    remote_file=$(git --git-dir "$remote" show refs/heads/feature/preview-remote:main.tf)
    [[ "$remote_file" == *'version = "1.0.0"'* ]] || fail "remote dry run changed the remote branch"
}

test_includes_remote_only_branches_without_pushing() {
    local repository="$TEST_ROOT/remote-branches"
    local remote="$TEST_ROOT/origin.git"
    create_repository "$repository"
    git -C "$repository" branch feature/remote
    git init -q --bare "$remote"
    git -C "$repository" remote add origin "$remote"
    git -C "$repository" push -q origin main feature/remote
    git -C "$repository" branch -D feature/remote >/dev/null

    "$SCRIPT" \
        --repository "$repository" \
        --branch-pattern 'feature/*' \
        --module 'terraform-aws-modules/vpc/aws' \
        --to '2.0.0' \
        --binary "$TF_VERSION_BUMP" \
        --include-remotes

    local local_file
    local_file=$(git -C "$repository" show feature/remote:main.tf)
    [[ "$local_file" == *'version = "2.0.0"'* ]] || fail "remote-only branch was not updated locally"

    local remote_file
    remote_file=$(git --git-dir "$remote" show refs/heads/feature/remote:main.tf)
    [[ "$remote_file" == *'version = "1.0.0"'* ]] || fail "script pushed a remote branch"
}

test_writes_a_log_file() {
    local repository="$TEST_ROOT/logging"
    local log_file="$TEST_ROOT/update.log"
    create_repository "$repository"
    git -C "$repository" branch feature/logged

    "$SCRIPT" \
        --repository "$repository" \
        --branch-pattern 'feature/*' \
        --module 'terraform-aws-modules/vpc/aws' \
        --to '2.0.0' \
        --binary "$TF_VERSION_BUMP" \
        --log-file "$log_file"

    [[ -f "$log_file" ]] || fail "log file was not created"
    local log_output
    log_output=$(<"$log_file")
    [[ "$log_output" == *'Processing branch: feature/logged'* ]] || fail "log omits the processed branch"
    [[ "$log_output" == *'Successfully updated 1 file(s)'* ]] || fail "log omits tf-version-bump output"
}

test_refuses_a_log_file_inside_the_repository() {
    local repository="$TEST_ROOT/internal-log"
    local log_file="$repository/version-bump.log"
    create_repository "$repository"
    git -C "$repository" branch feature/logged

    local output
    if output=$("$SCRIPT" \
        --repository "$repository" \
        --branch-pattern 'feature/*' \
        --module 'terraform-aws-modules/vpc/aws' \
        --to '2.0.0' \
        --binary "$TF_VERSION_BUMP" \
        --log-file "$log_file" 2>&1); then
        fail "log file inside the target repository was accepted"
    fi

    [[ "$output" == *'log file must be outside the target repository'* ]] || fail "internal log file error was unclear"
    [[ ! -e "$log_file" ]] || fail "internal log file was created before validation"
    [[ -z "$(git -C "$repository" status --porcelain)" ]] || fail "internal log file left the repository dirty"
}

test_refuses_a_log_file_through_a_symlinked_parent_directory() {
    local repository="$TEST_ROOT/symlinked-log-parent"
    local log_directory="$TEST_ROOT/repository-link"
    local target_file="$repository/version-bump.log"
    create_repository "$repository"
    git -C "$repository" branch feature/logged
    ln -s "$repository" "$log_directory"

    local output
    if output=$("$SCRIPT" \
        --repository "$repository" \
        --branch-pattern 'feature/*' \
        --module 'terraform-aws-modules/vpc/aws' \
        --to '2.0.0' \
        --binary "$TF_VERSION_BUMP" \
        --log-file "$log_directory/version-bump.log" 2>&1); then
        fail "log file through a symlinked repository directory was accepted"
    fi

    [[ "$output" == *'log file must be outside the target repository'* ]] || fail "symlinked log directory error was unclear"
    [[ ! -e "$target_file" ]] || fail "symlinked log directory created a repository file"
    [[ -z "$(git -C "$repository" status --porcelain)" ]] || fail "symlinked log directory left the repository dirty"
}

test_refuses_a_log_file_symlink_to_a_repository_file() {
    local repository="$TEST_ROOT/symlinked-log"
    local log_file="$TEST_ROOT/version-bump-link.log"
    create_repository "$repository"
    git -C "$repository" branch feature/logged
    ln -s "$repository/main.tf" "$log_file"

    local original_file
    original_file=$(<"$repository/main.tf")

    local output
    if output=$("$SCRIPT" \
        --repository "$repository" \
        --branch-pattern 'feature/*' \
        --module 'terraform-aws-modules/vpc/aws' \
        --to '2.0.0' \
        --binary "$TF_VERSION_BUMP" \
        --log-file "$log_file" 2>&1); then
        fail "log file symlink to a repository file was accepted"
    fi

    [[ "$output" == *'log file must not be a symbolic link'* ]] || fail "log file symlink error was unclear"
    [[ "$(<"$repository/main.tf")" == "$original_file" ]] || fail "log output changed the symlink target"
    [[ -z "$(git -C "$repository" status --porcelain)" ]] || fail "log file symlink left the repository dirty"
}

test_refuses_a_dangling_log_file_symlink() {
    local repository="$TEST_ROOT/dangling-log"
    local target_file="$repository/version-bump.log"
    local log_file="$TEST_ROOT/dangling-version-bump-link.log"
    create_repository "$repository"
    git -C "$repository" branch feature/logged
    ln -s "$target_file" "$log_file"

    local output
    if output=$("$SCRIPT" \
        --repository "$repository" \
        --branch-pattern 'feature/*' \
        --module 'terraform-aws-modules/vpc/aws' \
        --to '2.0.0' \
        --binary "$TF_VERSION_BUMP" \
        --log-file "$log_file" 2>&1); then
        fail "dangling log file symlink was accepted"
    fi

    [[ "$output" == *'log file must not be a symbolic link'* ]] || fail "dangling log file symlink error was unclear"
    [[ ! -e "$target_file" ]] || fail "dangling log file symlink created its repository target"
    [[ -z "$(git -C "$repository" status --porcelain)" ]] || fail "dangling log file symlink left the repository dirty"
}

test_filters_branches_by_tip_commit_age() {
    local repository="$TEST_ROOT/recent-branches"
    create_repository "$repository"
    git -C "$repository" branch feature/recent
    git -C "$repository" checkout -q -b feature/old
    printf '%s\n' 'old branch' >"$repository/old.txt"
    git -C "$repository" add old.txt
    GIT_AUTHOR_DATE='2020-01-01T00:00:00Z' \
        GIT_COMMITTER_DATE='2020-01-01T00:00:00Z' \
        git -C "$repository" commit -q -m "old fixture"
    git -C "$repository" checkout -q main

    "$SCRIPT" \
        --repository "$repository" \
        --branch-pattern 'feature/*' \
        --module 'terraform-aws-modules/vpc/aws' \
        --to '2.0.0' \
        --binary "$TF_VERSION_BUMP" \
        --since-days 30

    local recent_file
    recent_file=$(git -C "$repository" show feature/recent:main.tf)
    [[ "$recent_file" == *'version = "2.0.0"'* ]] || fail "recent branch was not updated"

    local old_file
    old_file=$(git -C "$repository" show feature/old:main.tf)
    [[ "$old_file" == *'version = "1.0.0"'* ]] || fail "old branch was updated"
}

test_refuses_a_dirty_repository() {
    local repository="$TEST_ROOT/dirty-repository"
    create_repository "$repository"
    git -C "$repository" branch feature/dirty
    printf '%s\n' 'uncommitted work' >"$repository/notes.txt"

    local output
    if output=$("$SCRIPT" \
        --repository "$repository" \
        --branch-pattern 'feature/*' \
        --module 'terraform-aws-modules/vpc/aws' \
        --to '2.0.0' \
        --binary "$TF_VERSION_BUMP" 2>&1); then
        fail "dirty repository was accepted"
    fi
    [[ "$output" == *'repository has uncommitted or untracked changes'* ]] || fail "dirty repository error was unclear"

    local feature_file
    feature_file=$(git -C "$repository" show feature/dirty:main.tf)
    [[ "$feature_file" == *'version = "1.0.0"'* ]] || fail "dirty repository branch was changed"
}

test_commit_failure_leaves_changes_on_the_affected_branch() {
    local repository="$TEST_ROOT/commit-failure"
    create_repository "$repository"
    git -C "$repository" branch feature/blocked
    printf '%s\n' '#!/bin/sh' 'exit 1' >"$repository/.git/hooks/pre-commit"
    chmod +x "$repository/.git/hooks/pre-commit"

    local output
    if output=$("$SCRIPT" \
        --repository "$repository" \
        --branch-pattern 'feature/*' \
        --module 'terraform-aws-modules/vpc/aws' \
        --to '2.0.0' \
        --binary "$TF_VERSION_BUMP" 2>&1); then
        fail "script succeeded after its commit was rejected"
    fi

    local current_branch
    current_branch=$(git -C "$repository" branch --show-current)
    [[ "$current_branch" == "feature/blocked" ]] || fail "failed update was moved to another branch"
    [[ "$output" == *'not restoring the starting branch'* ]] || fail "failure did not explain where the changes remain"

    local working_file
    working_file=$(<"$repository/main.tf")
    [[ "$working_file" == *'version = "2.0.0"'* ]] || fail "failed commit changes were lost"
}

test_signing_failure_leaves_changes_on_the_affected_branch() {
    local repository="$TEST_ROOT/signing-failure"
    create_repository "$repository"
    git -C "$repository" branch feature/signing
    git -C "$repository" config gpg.format ssh
    git -C "$repository" config gpg.ssh.program ssh-keygen
    git -C "$repository" config user.signingkey "$TEST_ROOT/missing-signing-key"
    git -C "$repository" config commit.gpgsign false

    local original_head
    original_head=$(git -C "$repository" rev-parse feature/signing)

    local output
    if output=$(PATH="$ORIGINAL_PATH" "$SCRIPT" \
        --repository "$repository" \
        --branch-pattern 'feature/*' \
        --module 'terraform-aws-modules/vpc/aws' \
        --to '2.0.0' \
        --binary "$TF_VERSION_BUMP" \
        --sign-commits 2>&1); then
        fail "script succeeded without access to the configured signing key"
    fi

    local current_branch
    current_branch=$(git -C "$repository" branch --show-current)
    [[ "$current_branch" == "feature/signing" ]] || fail "failed signed update was moved to another branch"
    [[ "$(git -C "$repository" rev-parse feature/signing)" == "$original_head" ]] || fail "signing failure created a commit"
    [[ "$output" == *'not restoring the starting branch'* ]] || fail "signing failure did not explain where the changes remain"

    local working_file
    working_file=$(<"$repository/main.tf")
    [[ "$working_file" == *'version = "2.0.0"'* ]] || fail "signing failure discarded the update"
}

install_git_wrapper
create_test_signing_key
build_tf_version_bump
test_help_describes_required_inputs
test_updates_matching_local_branches_and_restores_starting_branch
test_applies_config_file_updates
test_dry_run_leaves_branches_unchanged
test_remote_dry_run_does_not_create_local_branches
test_includes_remote_only_branches_without_pushing
test_writes_a_log_file
test_refuses_a_log_file_inside_the_repository
test_refuses_a_log_file_through_a_symlinked_parent_directory
test_refuses_a_log_file_symlink_to_a_repository_file
test_refuses_a_dangling_log_file_symlink
test_filters_branches_by_tip_commit_age
test_refuses_a_dirty_repository
test_commit_failure_leaves_changes_on_the_affected_branch
test_signing_failure_leaves_changes_on_the_affected_branch
echo "PASS: update-branches.sh"
