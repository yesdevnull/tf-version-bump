# Task 8 lint fixes

Implemented the three requested test-only lint fixes on `wip/test-suite-cleanup`:

- Replaced the `/tmp` no-match fixture with a `t.TempDir()`-based pattern and dynamically constructed diagnostic.
- Rewrote the module output assertion chain as an equivalent `switch`.
- Simplified `requireExitCall` to assert exit code 1 and updated every call site.

Validation:

- Focused affected tests: passed.
- `golangci-lint run --timeout=5m`: passed (`0 issues`).
- `go test -count=1 ./...`: passed.
- `codex-git diff --check`: passed.
