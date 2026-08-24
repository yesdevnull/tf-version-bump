#!/usr/bin/env bash

set -euo pipefail

usage() {
    cat <<'USAGE'
Usage: update-actions-release-pin.sh <version> <linux-x86-64-sha256>

Update the maintained GitHub Actions example, harness, and user guides to an
independently verified tf-version-bump release and Linux x86-64 archive digest.
USAGE
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
if [[ ! $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$ ]]; then
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
if [[ ! $current_version =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$ ]]; then
    printf 'Could not determine the current release version from %s\n' "$production_workflow" >&2
    exit 1
fi
if [[ ! $current_digest =~ ^[0-9a-f]{64}$ ]]; then
    printf 'Could not determine the current Linux x86-64 SHA-256 from %s\n' "$production_workflow" >&2
    exit 1
fi

files=(
    "examples/github-actions/.github/workflows/tf-version-bump-production.yml"
    "examples/github-actions/.github/workflows/tf-version-bump-nonproduction.yml"
    "examples/github-actions/test.sh"
    "examples/github-actions/README.md"
    "docs/ADVANCED-USAGE.md"
)
expected_version_counts=(1 1 3 1 1)

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

for index in "${!files[@]}"; do
    relative_file=${files[$index]}
    source_file="$repository_root/$relative_file"
    if [[ ! -f $source_file ]]; then
        printf 'Maintained release-pin file is missing: %s\n' "$relative_file" >&2
        exit 1
    fi
    version_count=$(count_literal "$source_file" "${current_version#v}")
    digest_count=$(count_literal "$source_file" "$current_digest")
    if [[ $version_count != "${expected_version_counts[$index]}" || $digest_count != 1 ]]; then
        printf 'Unexpected release-pin layout in %s: version occurrences %s, digest occurrences %s\n' \
            "$relative_file" "$version_count" "$digest_count" >&2
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

for relative_file in "${files[@]}"; do
    source_file="$repository_root/$relative_file"
    version_file="$temporary_directory/version"
    updated_file="$temporary_directory/updated"
    replace_literal "$source_file" "$version_file" "${current_version#v}" "${version#v}"
    replace_literal "$version_file" "$updated_file" "$current_digest" "$digest"
    cat "$updated_file" >"$source_file"
done

printf 'Updated GitHub Actions example pin to %s\n' "$version"
