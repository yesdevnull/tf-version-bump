#!/usr/bin/env bash

set -euo pipefail

usage() {
    cat <<'USAGE'
Usage: update-actions-release-pin.sh <version> <linux-x86-64-sha256>

Update the maintained GitHub Actions example, harness, and user guides to an
independently verified tf-version-bump release and Linux x86-64 archive digest.
USAGE
}

is_semantic_version() {
    local candidate=$1 version_without_build prerelease identifier
    local identifiers=()
    [[ $candidate =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-([0-9A-Za-z-]+([.][0-9A-Za-z-]+)*))?(\+([0-9A-Za-z-]+([.][0-9A-Za-z-]+)*))?$ ]] || return 1
    version_without_build=${candidate%%+*}
    if [[ $version_without_build != *-* ]]; then
        return 0
    fi
    prerelease=${version_without_build#*-}
    IFS='.' read -r -a identifiers <<<"$prerelease"
    for identifier in "${identifiers[@]}"; do
        if [[ $identifier =~ ^0[0-9]+$ ]]; then
            return 1
        fi
    done
}

if [[ ${1:-} == "--help" ]]; then
    usage
    exit 0
fi
if (($# != 2)); then
    usage >&2
    exit 2
fi

version=$1
digest=$2
if ! is_semantic_version "$version"; then
    printf 'Invalid release version: %s\n' "$version" >&2
    usage >&2
    exit 2
fi
if [[ ! $digest =~ ^[0-9a-f]{64}$ ]]; then
    printf 'Invalid Linux x86-64 SHA-256: %s\n' "$digest" >&2
    usage >&2
    exit 2
fi

repository_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
production_workflow="$repository_root/examples/github-actions/.github/workflows/tf-version-bump-production.yml"
current_version=$(awk '$1 == "tf_version_bump_version:" { print $2; exit }' "$production_workflow")
current_digest=$(awk '$1 == "tf_version_bump_archive_sha256:" { print $2; exit }' "$production_workflow")
if ! is_semantic_version "$current_version"; then
    printf 'Could not determine the current release version from %s\n' "$production_workflow" >&2
    exit 1
fi
if [[ ! $current_digest =~ ^[0-9a-f]{64}$ ]]; then
    printf 'Could not determine the current Linux x86-64 SHA-256 from %s\n' "$production_workflow" >&2
    exit 1
fi

production_file="examples/github-actions/.github/workflows/tf-version-bump-production.yml"
nonproduction_file="examples/github-actions/.github/workflows/tf-version-bump-nonproduction.yml"
test_file="examples/github-actions/test.sh"
readme_file="examples/github-actions/README.md"
guide_file="docs/ADVANCED-USAGE.md"
files=("$production_file" "$nonproduction_file" "$test_file" "$readme_file" "$guide_file")

current_archive_url="https://github.com/yesdevnull/tf-version-bump/releases/download/$current_version/tf-version-bump_${current_version#v}_linux_x86_64.tar.gz"
new_archive_url="https://github.com/yesdevnull/tf-version-bump/releases/download/$version/tf-version-bump_${version#v}_linux_x86_64.tar.gz"
pin_files=(
    "$production_file" "$production_file"
    "$nonproduction_file" "$nonproduction_file"
    "$test_file" "$test_file" "$test_file"
    "$readme_file" "$readme_file"
    "$guide_file" "$guide_file"
)
pin_markers=(
    "      tf_version_bump_version:" "      tf_version_bump_archive_sha256:"
    "      tf_version_bump_version:" "      tf_version_bump_archive_sha256:"
    "TF_VERSION_BUMP_VERSION=" "TF_VERSION_BUMP_ARCHIVE_SHA256=" "TF_VERSION_BUMP_ARCHIVE_URL="
    "" ""
    "" ""
)
current_values=(
    "tf_version_bump_version: $current_version" "tf_version_bump_archive_sha256: $current_digest"
    "tf_version_bump_version: $current_version" "tf_version_bump_archive_sha256: $current_digest"
    "TF_VERSION_BUMP_VERSION=\"$current_version\"" "TF_VERSION_BUMP_ARCHIVE_SHA256=\"$current_digest\"" "TF_VERSION_BUMP_ARCHIVE_URL=\"$current_archive_url\""
    "$current_version" "$current_digest"
    "$current_version" "$current_digest"
)
new_values=(
    "tf_version_bump_version: $version" "tf_version_bump_archive_sha256: $digest"
    "tf_version_bump_version: $version" "tf_version_bump_archive_sha256: $digest"
    "TF_VERSION_BUMP_VERSION=\"$version\"" "TF_VERSION_BUMP_ARCHIVE_SHA256=\"$digest\"" "TF_VERSION_BUMP_ARCHIVE_URL=\"$new_archive_url\""
    "$version" "$digest"
    "$version" "$digest"
)

count_literal() {
    local filename=$1 literal=$2
    awk -v literal="$literal" '
        {
            line = $0
            while ((position = index(line, literal)) != 0) {
                count++
                line = substr(line, position + length(literal))
            }
        }
        END { print count + 0 }
    ' "$filename"
}

count_line_prefix() {
    local filename=$1 prefix=$2
    awk -v prefix="$prefix" 'index($0, prefix) == 1 { count++ } END { print count + 0 }' "$filename"
}

for relative_file in "${files[@]}"; do
    source_file="$repository_root/$relative_file"
    if [[ ! -f $source_file ]]; then
        printf 'Maintained release-pin file is missing: %s\n' "$relative_file" >&2
        exit 1
    fi
done
for index in "${!pin_files[@]}"; do
    relative_file=${pin_files[$index]}
    marker_count=1
    if [[ -n ${pin_markers[$index]} ]]; then
        marker_count=$(count_line_prefix "$repository_root/$relative_file" "${pin_markers[$index]}")
    fi
    occurrence_count=$(count_literal "$repository_root/$relative_file" "${current_values[$index]}")
    if [[ $marker_count != 1 || $occurrence_count != 1 ]]; then
        printf 'Unexpected release-pin layout in %s: field occurrences %s, expected-value occurrences %s\n' \
            "$relative_file" "$marker_count" "$occurrence_count" >&2
        exit 1
    fi
done

temporary_directory=$(mktemp -d "${TMPDIR:-/tmp}/tf-version-bump-release-pin.XXXXXX")
trap 'rm -rf -- "$temporary_directory"' EXIT

replace_literal() {
    local source=$1 destination=$2 old=$3 new=$4
    awk -v old="$old" -v new="$new" '
        {
            remaining = $0
            output = ""
            while ((position = index(remaining, old)) != 0) {
                output = output substr(remaining, 1, position - 1) new
                remaining = substr(remaining, position + length(old))
            }
            print output remaining
        }
    ' "$source" >"$destination"
}

changed_files=()
for relative_file in "${files[@]}"; do
    source_file="$repository_root/$relative_file"
    current_file="$temporary_directory/current"
    updated_file="$temporary_directory/updated"
    staged_file="$temporary_directory/staged/$relative_file"
    backup_file="$temporary_directory/original/$relative_file"
    mkdir -p -- "$(dirname -- "$staged_file")" "$(dirname -- "$backup_file")"
    cp -- "$source_file" "$current_file"
    cp -- "$source_file" "$backup_file"
    for index in "${!pin_files[@]}"; do
        if [[ ${pin_files[$index]} == "$relative_file" ]]; then
            replace_literal "$current_file" "$updated_file" "${current_values[$index]}" "${new_values[$index]}"
            mv -- "$updated_file" "$current_file"
        fi
    done
    cp -- "$current_file" "$staged_file"
    if ! cmp -s -- "$source_file" "$staged_file"; then
        changed_files+=("$relative_file")
    fi
done

for relative_file in "${changed_files[@]}"; do
    source_file="$repository_root/$relative_file"
    if [[ ! -w $source_file ]]; then
        printf 'Maintained release-pin file is not writable: %s\n' "$relative_file" >&2
        exit 1
    fi
done

written_files=()
for relative_file in "${changed_files[@]}"; do
    source_file="$repository_root/$relative_file"
    staged_file="$temporary_directory/staged/$relative_file"
    if ! cat "$staged_file" >"$source_file"; then
        printf 'Could not update maintained release-pin file: %s\n' "$relative_file" >&2
        for written_file in "${written_files[@]}"; do
            if ! cat "$temporary_directory/original/$written_file" >"$repository_root/$written_file"; then
                printf 'Could not restore maintained release-pin file: %s\n' "$written_file" >&2
            fi
        done
        exit 1
    fi
    written_files+=("$relative_file")
done

printf 'Updated GitHub Actions example pin to %s\n' "$version"
