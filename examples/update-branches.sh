#!/usr/bin/env bash

set -euo pipefail

usage() {
    cat <<'EOF'
Usage:
  update-branches.sh --branch-pattern <glob> --module <source> --to <version> [options]
  update-branches.sh --branch-pattern <glob> --config <file> [options]

Required selection:
  --branch-pattern <glob>  Select local branch names, for example 'release/*'.

Update mode (choose one):
  --module <source>        Update this module source (requires --to).
  --to <version>           Set the target module version.
  --config <file>          Apply updates from a tf-version-bump YAML config.

Options:
  --repository <path>      Git repository to process (default: current directory).
  --file-pattern <glob>    Terraform files to update (default: '**/*.tf').
  --binary <path>          tf-version-bump executable (default: from PATH).
  --dry-run                Preview every branch without changing or committing it.
  --include-remotes        Fetch and include branches that exist only on the remote.
  --remote <name>          Remote used with --include-remotes (default: origin).
  --since-days <number>    Process branches whose tip changed within this many days.
  --log-file <path>        Append command output to a log file.
                           Write-mode updates require a signed commit.
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
dry_run=false
include_remotes=false
remote="origin"
log_file=""
since_days=""

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
        --dry-run)
            dry_run=true
            shift
            ;;
        --include-remotes)
            include_remotes=true
            shift
            ;;
        --remote)
            [[ $# -ge 2 ]] || die "--remote requires a name"
            remote=$2
            shift 2
            ;;
        --log-file)
            [[ $# -ge 2 ]] || die "--log-file requires a path"
            log_file=$2
            shift 2
            ;;
        --since-days)
            [[ $# -ge 2 ]] || die "--since-days requires a number"
            since_days=$2
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

if [[ -n "$log_file" ]]; then
    exec > >(tee -a "$log_file") 2>&1
fi

[[ -n "$branch_pattern" ]] || die "--branch-pattern is required"
if [[ -n "$since_days" && ! "$since_days" =~ ^[1-9][0-9]*$ ]]; then
    die "--since-days must be a positive integer"
fi
if [[ -n "$config_file" ]]; then
    [[ -z "$module_source" && -z "$target_version" ]] || die "--config cannot be combined with --module or --to"
    [[ -f "$config_file" ]] || die "config file does not exist: $config_file"
    config_directory=$(cd "$(dirname "$config_file")" && pwd)
    config_file="$config_directory/$(basename "$config_file")"
    commit_message="chore: apply tf-version-bump config"
else
    [[ -n "$module_source" ]] || die "either --module or --config is required"
    [[ -n "$target_version" ]] || die "--to is required with --module"
    commit_message="chore: bump $module_source to $target_version"
fi
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
        if [[ -n "$(git -C "$repository" status --porcelain)" ]]; then
            echo "Warning: changes remain on $current_branch; not restoring the starting branch $starting_branch" >&2
            return
        fi
        git -C "$repository" checkout -q "$starting_branch"
    fi
}

trap restore_starting_branch EXIT

branch_is_recent() {
    local ref=$1
    if [[ -z "$since_days" ]]; then
        return 0
    fi

    local tip_timestamp
    local cutoff_timestamp
    tip_timestamp=$(git -C "$repository" log -1 --format=%ct "$ref")
    cutoff_timestamp=$(($(date +%s) - since_days * 86400))
    [[ "$tip_timestamp" -ge "$cutoff_timestamp" ]]
}

process_branch() {
    local branch=$1
    local tracking_ref=${2:-}
    echo "Processing branch: $branch"
    if [[ -n "$tracking_ref" ]]; then
        if [[ "$dry_run" == true ]]; then
            git -C "$repository" checkout -q --detach "$tracking_ref"
        else
            git -C "$repository" checkout -q -b "$branch" --track "$tracking_ref"
        fi
    else
        git -C "$repository" checkout -q "$branch"
    fi

    (
        cd "$repository"
        arguments=(-pattern "$file_pattern")
        if [[ -n "$config_file" ]]; then
            arguments+=(-config "$config_file")
        else
            arguments+=(-module "$module_source" -to "$target_version")
        fi
        if [[ "$dry_run" == true ]]; then
            arguments+=(-dry-run)
        fi
        "$binary" "${arguments[@]}"
    )

    if git -C "$repository" diff --quiet; then
        echo "No changes needed on $branch"
        return
    fi

    git -C "$repository" add -u -- .
    git -C "$repository" commit -S -m "$commit_message"
}

matched_branch=false
while IFS= read -r branch; do
    # The unquoted right-hand side is the caller's Bash glob, not a literal string.
    # shellcheck disable=SC2053
    if [[ "$branch" != $branch_pattern ]]; then
        continue
    fi
    matched_branch=true
    if ! branch_is_recent "$branch"; then
        echo "Skipping branch outside the activity window: $branch"
        continue
    fi
    process_branch "$branch"
done < <(git -C "$repository" for-each-ref --format='%(refname:short)' refs/heads/)

if [[ "$include_remotes" == true ]]; then
    git -C "$repository" remote get-url "$remote" >/dev/null 2>&1 || die "remote does not exist: $remote"
    git -C "$repository" fetch "$remote"

    while IFS= read -r branch; do
        # The unquoted right-hand side is the caller's Bash glob, not a literal string.
        # shellcheck disable=SC2053
        if [[ "$branch" == "HEAD" || "$branch" != $branch_pattern ]]; then
            continue
        fi
        matched_branch=true
        if git -C "$repository" show-ref --verify --quiet "refs/heads/$branch"; then
            continue
        fi
        if ! branch_is_recent "$remote/$branch"; then
            echo "Skipping branch outside the activity window: $branch"
            continue
        fi
        process_branch "$branch" "$remote/$branch"
    done < <(git -C "$repository" for-each-ref --format='%(refname:strip=3)' "refs/remotes/$remote/")
fi

if [[ "$matched_branch" == false ]]; then
    die "no local branches matched: $branch_pattern"
fi

echo "Done. Returned to $starting_branch"
