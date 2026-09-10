#!/usr/bin/env bash

set -euo pipefail

if [[ "${1:-}" == "--help" ]]; then
    cat <<'EOF'
Usage: run-shellcheck.sh

Lint every shell script (*.sh) that Git tracks below the current directory with
the shellcheck on PATH. Run it from the repository root, as `make shellcheck`
does; CI pins the shellcheck version in .github/workflows/lint.yml.

Fails when Git cannot list the tracked files, for example outside a working
tree, or lists no scripts, so a broken listing never passes as a clean lint.
EOF
    exit 0
fi

listing=$(mktemp)
trap 'rm -f "$listing"' EXIT
if ! git ls-files -z -- '*.sh' >"$listing"; then
    echo "run-shellcheck.sh: git ls-files could not list the tracked shell scripts" >&2
    exit 1
fi
scripts=()
while IFS= read -r -d '' script; do
    scripts+=("$script")
done <"$listing"
if [[ ${#scripts[@]} -eq 0 ]]; then
    echo "run-shellcheck.sh: git ls-files found no tracked shell scripts" >&2
    exit 1
fi
shellcheck -- "${scripts[@]}"
