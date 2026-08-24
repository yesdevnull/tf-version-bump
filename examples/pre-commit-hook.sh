#!/usr/bin/env bash

set -euo pipefail

usage() {
    cat <<'USAGE'
Usage: pre-commit-hook.sh

Validate the configured tf-version-bump YAML file, then check whether matching
Terraform files require updates without modifying them.

Environment overrides:
  TF_VERSION_BUMP_BIN      Executable name or path (default: tf-version-bump)
  TF_VERSION_BUMP_CONFIG   Repository-relative config path (default: versions.yml)
  TF_VERSION_BUMP_PATTERN  Repository-relative glob (default: **/*.tf)
USAGE
}

if [[ ${1:-} == "--help" ]]; then
    usage
    exit 0
fi

binary=${TF_VERSION_BUMP_BIN:-tf-version-bump}
config_file=${TF_VERSION_BUMP_CONFIG:-versions.yml}
pattern=${TF_VERSION_BUMP_PATTERN:-**/*.tf}

is_repository_relative() {
    local value=$1
    [[ -n $value && $value != /* && $value != ".." && $value != ../* && $value != */../* && $value != */.. ]]
}

if ! is_repository_relative "$config_file"; then
    printf 'tf-version-bump pre-commit: config path must be repository-relative: %s\n' "$config_file" >&2
    exit 1
fi
if ! is_repository_relative "$pattern"; then
    printf 'tf-version-bump pre-commit: pattern must be repository-relative: %s\n' "$pattern" >&2
    exit 1
fi

if ! binary_path=$(command -v "$binary"); then
    printf 'tf-version-bump pre-commit: executable not found: %s\n' "$binary" >&2
    exit 1
fi
if [[ $binary_path != /* ]]; then
    binary_path=$(cd -- "$(dirname -- "$binary_path")" && pwd -P)/$(basename -- "$binary_path")
fi

if ! repository_root=$(git rev-parse --show-toplevel 2>/dev/null); then
    printf 'tf-version-bump pre-commit: not inside a Git worktree\n' >&2
    exit 1
fi
staged_root=$(mktemp -d "${TMPDIR:-/tmp}/tf-version-bump-pre-commit.XXXXXX")
cleanup() {
    rm -rf -- "$staged_root"
}
trap cleanup EXIT
if ! git -C "$repository_root" checkout-index --all --prefix="$staged_root/"; then
    printf 'tf-version-bump pre-commit: could not materialise the staged index\n' >&2
    exit 1
fi
cd -- "$staged_root"

if ! "$binary_path" -validate-config "$config_file"; then
    printf 'tf-version-bump pre-commit: configuration validation failed: %s\n' "$config_file" >&2
    exit 1
fi

set +e
"$binary_path" -pattern "$pattern" -config "$config_file" -check
check_exit=$?
set -e

case $check_exit in
    0)
        exit 0
        ;;
    2)
        printf 'tf-version-bump pre-commit: updates are required; apply them before committing\n' >&2
        exit 2
        ;;
    *)
        printf 'tf-version-bump pre-commit: version check failed\n' >&2
        exit 1
        ;;
esac
