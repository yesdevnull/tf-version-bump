#!/usr/bin/env bash

set -euo pipefail

if [[ "${1:-}" == "--help" ]]; then
    cat <<'EOF'
Usage: run-actionlint.sh [workflow path ...]

Lint GitHub Actions workflow files with actionlint 1.7.12.

GitHub.com is authoritative for the newer concurrency.queue property. The
pinned actionlint release predates that property, so only its stale diagnostic
is suppressed.
EOF
    exit 0
fi

go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 -ignore '^unexpected key "queue" for "concurrency" section\.' "$@"
