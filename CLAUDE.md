# CLAUDE.md - AI Assistant Guide for tf-version-bump

Guidance for AI assistants working on this codebase. User-facing docs begin in
[README.md](README.md) and continue under [`docs/`](docs); for a shorter agent primer see
[AGENTS.md](AGENTS.md).

## Project Overview

**tf-version-bump** is a Go CLI that updates Terraform module versions, `required_version` in
terraform blocks, and provider versions in `required_providers`, across files matched by a glob.
It parses HCL with HashiCorp's `hclwrite`; comments and structure survive while whitespace may be
normalised when a changed file is formatted.

This repository is an experiment for generative AI coding tools. It may contain bugs or incomplete
features. Keep changes under version control and test them.

**Stack**: Go 1.26+ (CI pins 1.26), `hashicorp/hcl/v2`,
`hashicorp/terraform-registry-address`, `zclconf/go-cty`, `yaml.v3`, and
`bmatcuk/doublestar/v4`. Dependency versions live in `go.mod` — don't restate them here or in
AGENTS.md; they drift.

## Layout

All Go code is in a single flat `main` package — no subdirectories, no package graph.

```
main.go                  # CLI parsing, HCL processing, all version updates
config.go                # YAML config loading and validation
*_test.go                # split by concern (see Testing)
schema/config-schema.json # JSON Schema for the YAML config
examples/                # Sample .tf/.yml files and branch automation
docs/USAGE.md            # Detailed CLI and behaviour reference
docs/CONFIGURATION.md    # YAML configuration reference
docs/ADVANCED-USAGE.md   # Cross-branch automation guide
docs/RELEASING.md        # Release and artefact verification
```

## Commands

```bash
make test           # go test -v ./...                       (~2s)
make test-coverage  # -race -coverprofile, prints func-level coverage
make coverage-html  # writes coverage.html
make coverage-func  # re-print coverage from an existing coverage.out
make build          # go build -v -o tf-version-bump .
make clean          # remove binary + coverage artefacts
```

Full validation before committing (mirrors CI):

```bash
go mod download && go mod verify
go test -v -race -coverprofile=coverage.out -covermode=atomic ./...
golangci-lint run --timeout=5m
go build -o tf-version-bump .
```

**golangci-lint must match CI's version, currently v2.12** (see `.github/workflows/lint.yml`;
`.golangci.yml` is `version: "2"` schema). It enables a curated linter set rather than the
defaults, sets `gocyclo` min-complexity to 15, and lints test files too. Bump the version here
when the workflow pins a new one.

```bash
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin v2.12
```

## Gotchas

**Globbing uses `doublestar`, not `filepath.Glob`.** `findMatchingFiles` deliberately calls
`doublestar.Glob` against an `fs.FS`, because `filepath.Glob` has no recursive wildcard — it
treats `**` as a plain `*` that never crosses a separator. Don't "simplify" this back to the
stdlib. With doublestar, `**` spans zero or more directories, so `**/*.tf` matches `top.tf`,
`a/mid.tf` and `a/b/deep.tf`.

**The glob filesystem and options carry real weight — don't drop them.** `findMatchingFiles`
uses `visibleDirFS` and two options, each fixing a way `**` can misbehave once it genuinely
recurses:

- `visibleDirFS` — wildcard traversal skips dot-directories, so `**/*.tf` never descends into
  `.terraform/modules` (whose vendored copies `terraform init` regenerates, making any bump
  written there silently vanish). Because the non-glob base is resolved first, an explicit
  `.terraform/**/*.tf` still matches. This is deliberately shell-like; there is no custom
  exclusion list.
- `WithNoFollow` — without it a directory symlink matches the same physical file twice, and a
  symlink cycle matches it until the OS hits its link limit.
- `WithFilesOnly` — without it `-pattern "modules/**"` returns directories, which get counted
  as files and then fail with "is a directory".

Note this means the tool cannot protect a user who `cd`s into `.terraform` and globs from
there; that is treated as deliberate, exactly as a shell would.

**HCL attribute tokens include their quotes.** Use the `trimQuotes` helper when reading an
attribute's value; don't compare against raw token bytes.

**Never string-manipulate HCL.** Always go through the `hclwrite` API, or formatting and
comments are lost. `hclwrite.Format()` may adjust whitespace — that's expected.

**Local modules are skipped by design.** `isLocalModule` treats `./`, `../` and `/` sources as
local; Terraform gives them no version attribute, so there is nothing to bump.

**Don't run concurrent instances over the same files.** There is no file locking and writes are
not atomic. Files are processed in memory, so very large files (>100MB) are impractical.

## Architecture

### Update flow

`main()` → `validateOperationModes` → either standalone config validation or
`findMatchingFiles` → `runConfigFileMode` (YAML) / `runCLIMode` (one direct operation).
Each update mode dispatches to one of three update paths:
`updateModuleVersionWithCount`, `updateTerraformVersion`, or `updateProviderVersionWithCount`.

`updateModuleVersionWithCount` reads and parses the file, then bundles its many
parameters into a `moduleUpdateOptions` struct and delegates per-block work to
`updateModuleBlockResult` → `shouldSkipModuleVersion`. Add new per-module filtering there rather
than growing the parameter list.

Provider updates are the fiddliest path: `required_providers` entries can be either a nested
block or an object expression, so `updateProviderVersionWithCount` branches through
`updateProviderBlockSyntaxResult` and `updateProviderAttributeVersionResult` /
`providerAttributeObject` / `replaceProviderObjectVersion`. Attribute-object updates replace only
the version expression's byte range so other expressions such as `configuration_aliases` remain.

### Standard hclwrite pattern

```go
fileInfo, err := os.Stat(filename)          // capture mode first — writes must preserve it
src, err := os.ReadFile(filename)
file, diags := hclwrite.ParseConfig(src, filename, hcl.Pos{Line: 1, Column: 1})
if diags.HasErrors() { return false, fmt.Errorf("failed to parse HCL: %s", diags.Error()) }

for _, block := range file.Body().Blocks() {
    if block.Type() == "module" {
        block.Body().SetAttributeValue("version", cty.StringVal(targetVersion))
    }
}

output := hclwrite.Format(file.Bytes())
os.WriteFile(filename, output, fileInfo.Mode().Perm())
```

### Module update precedence

Evaluated across `updateModuleBlockResult` and `shouldSkipModuleVersion`:

1. Source must match exactly.
2. Local sources are skipped.
3. `ignore_modules` matches the module *name* → skip.
4. A missing version is skipped unless `-force-add` is set and the source is a registry module.
5. `ignore_versions` contains the current value → skip (takes precedence over `from`).
6. `from` is set and does not contain the current value → skip.
7. Otherwise update.

### Ignore-pattern matching

Custom wildcard matcher (`shouldIgnoreModule` → `matchPattern`), matching module **names**,
not sources. `*` means zero or more characters: `vpc` (exact), `legacy-*` (prefix),
`*-test` (suffix), `*-vpc-*` (contains).

### Config shape (`config.go`)

Parsed with `KnownFields(true)` — unknown YAML keys are an error. `FromVersions` has a custom
`UnmarshalYAML` accepting either a string or a list. Values are whitespace-trimmed and empties
dropped (`trimNonEmptyStrings`).

```go
type ModuleUpdate struct {
    Source         string       // required
    Version        string       // required
    From           FromVersions // optional: only update from these versions
    IgnoreVersions FromVersions // optional
    IgnoreModules  []string     // optional: name patterns
}
```

Adding a config field means updating `schema/config-schema.json` too.

### Errors and output

File-level errors log and continue to the next file; bad flags, invalid globs, no file matches,
and an unparseable config are fatal (`fatalf`). Warnings go to stderr prefixed `Warning:` for
local modules, missing version attributes without `-force-add`, and non-registry sources where
`-force-add` cannot add a version. Filtered modules are printed only with `-verbose`. Prefer
skipping over guessing.

Success is prefixed `✓`; dry-run lines use `→` with the verb "Would update". User-facing values are wrapped with
`quote(s, format)`: `'vpc'` for `text` output, `` `vpc` `` for `md`. Thread `outputFormat`
through rather than hardcoding quotes.

`-report-file` is the machine-readable automation contract. It writes schema version 2 JSON with
exact counts of unique Terraform, module, and provider blocks whose version values changed across
the complete command. Keep human summaries and the report separate: existing summaries count
source/file operations, while the report counts individual changed blocks. Dry-run reports contain
zero counts because no file values changed.

`-check` uses the existing dry-run update paths but has a separate automation exit contract. The
mode runners return their update-operation total to `main`: a processing error exits 1, a successful
check with a positive total exits 2, and a successful check with no eligible update returns normally
with status 0. Check mode rejects `-dry-run` and `-report-file`, so it never writes Terraform or
report files.

## Testing

Follow TDD. Tests are commonly table-driven with `t.Run` subtests; prefer `t.TempDir()` for new
filesystem tests. Name them `Test<Function>_<Scenario>`.

Final test layout by concern:

- `pattern_test.go` — wildcard matching.
- `file_selection_test.go` — file selection and exclusions.
- `module_update_test.go` — module updates, filtering, diagnostics, permissions, and errors.
- `terraform_version_test.go` — Terraform required-version updates.
- `provider_update_test.go` — provider updates and attribute preservation.
- `config_test.go` / `config_schema_test.go` — YAML configuration and schema validation.
- `documentation_test.go` — local documentation links, schema-backed examples, and constraints.
- `command_test.go` — CLI parsing, output, and exit behaviour.
- `integration_test.go` — cross-file and cross-operation continuation.
- `release_workflow_test.go` — release workflow artefact validation.
- `test_helpers_test.go` — shared test helpers.

Testing rules:

- Keep one strongest owner per observable contract.
- Capture and assert expected diagnostics.
- Test output must contain no leaked application output.
- Total coverage must remain at least 90%.
- Follow an implementation or TDD phase with the separate `test-cleanup` pass.

A representative call — note the full 10-parameter signature:

```go
updated, changedBlocks, err := updateModuleVersionWithCount(
    tmpFile, "terraform-aws-modules/vpc/aws", "5.0.0",
    nil, nil, nil, // fromVersions, ignoreVersions, ignorePatterns
    false, false, false, "text", // forceAdd, dryRun, verbose, outputFormat
)
```

Copy files from `examples/` to a temporary directory before manual write-mode testing.

## CI

The primary CI workflow runs on push/PR to `main`, skipping `**/*.md`.

- **Test** — Go 1.26, `-race` + coverage; uploads to Codecov
- **Build** — needs Test; cross-compiles 6 targets (linux/darwin/windows × amd64/arm64)
- **Lint** — golangci-lint, only on Go/dependency file changes
- **Documentation** — a separate path-filtered workflow runs `make docs-check` for Markdown,
  schema, maintained example, and documentation-test changes
- **CodeQL** and **Release** (GoReleaser + SLSA, tag-triggered) run separately

## Conventions

CLI flags and the YAML config format are user-facing contracts — don't break them. Version
The JSON Schema accepts common Terraform constraint syntax (`1.0.0`, `~> 3.0`,
`>= 1.5, < 2.0`, pre-release, build metadata), but the runtime YAML loader does not execute that
schema. Keep the dependency list minimal.
Use Australian/British spelling in prose and comments.
