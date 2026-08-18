#!/usr/bin/env bash

set -euo pipefail

usage() {
    cat <<'EOF'
Usage:
  update-branches.sh --branch-pattern <glob> --module <source> --to <version> [options]
  update-branches.sh --branch-pattern <glob> --config <file> [options]

Required:
  --branch-pattern <glob>  Select local branch names, for example 'release/*'.
  --module <source>        Update this module source (requires --to).
  --to <version>           Set the target module version.
  --config <file>          Apply updates from a tf-version-bump YAML config.

Options:
  --repository <path>      Git repository to process (default: current directory).
  --file-pattern <glob>    Terraform files to update (default: '**/*.tf').
  --binary <path>          tf-version-bump executable (default: from PATH).
  -h, --help               Show this help.
EOF
}

die() {
    echo "Error: $*" >&2
    exit 2
}

repository="."
branch_pattern=""
module_source=""
target_version=""
config_file=""
file_pattern="**/*.tf"
binary="tf-version-bump"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --repository)
            [[ $# -ge 2 ]] || die "--repository requires a path"
            repository=$2
            shift 2
            ;;
        --branch-pattern)
            [[ $# -ge 2 ]] || die "--branch-pattern requires a glob"
            branch_pattern=$2
            shift 2
            ;;
        --module)
            [[ $# -ge 2 ]] || die "--module requires a source"
            module_source=$2
            shift 2
            ;;
        --to)
            [[ $# -ge 2 ]] || die "--to requires a version"
            target_version=$2
            shift 2
            ;;
        --config)
            [[ $# -ge 2 ]] || die "--config requires a file"
            config_file=$2
            shift 2
            ;;
        --file-pattern)
            [[ $# -ge 2 ]] || die "--file-pattern requires a glob"
            file_pattern=$2
            shift 2
            ;;
        --binary)
            [[ $# -ge 2 ]] || die "--binary requires a path"
            binary=$2
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            die "unknown argument: $1"
            ;;
    esac
done

[[ -n "$branch_pattern" ]] || die "--branch-pattern is required"
[[ -z "$config_file" ]] || die "--config mode is not implemented"
[[ -n "$module_source" ]] || die "--module is required"
[[ -n "$target_version" ]] || die "--to is required with --module"
git -C "$repository" rev-parse --git-dir >/dev/null 2>&1 || die "not a Git repository: $repository"

if [[ -n "$(git -C "$repository" status --porcelain)" ]]; then
    die "repository has uncommitted or untracked changes: $repository"
fi

starting_branch=$(git -C "$repository" branch --show-current)
[[ -n "$starting_branch" ]] || die "repository is in detached HEAD state"

restore_starting_branch() {
    local current_branch
    current_branch=$(git -C "$repository" branch --show-current)
    if [[ "$current_branch" != "$starting_branch" ]]; then
        git -C "$repository" checkout -q "$starting_branch"
    fi
}

trap restore_starting_branch EXIT

matched_branch=false
while IFS= read -r branch; do
    if [[ "$branch" != $branch_pattern ]]; then
        continue
    fi

    matched_branch=true
    echo "Processing branch: $branch"
    git -C "$repository" checkout -q "$branch"

    (
        cd "$repository"
        "$binary" \
            -pattern "$file_pattern" \
            -module "$module_source" \
            -to "$target_version"
    )

    if git -C "$repository" diff --quiet; then
        echo "No changes needed on $branch"
        continue
    fi

    git -C "$repository" add -u -- .
    git -C "$repository" commit -m "chore: bump $module_source to $target_version"
done < <(git -C "$repository" for-each-ref --format='%(refname:short)' refs/heads/)

if [[ "$matched_branch" == false ]]; then
    die "no local branches matched: $branch_pattern"
fi

echo "Done. Returned to $starting_branch"
