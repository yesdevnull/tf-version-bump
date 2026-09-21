# CLAUDE.md - AI Assistant Guide for tf-version-bump

Guidance for AI assistants working on this codebase. User-facing docs begin in [README.md](README.md) and continue under [`docs/`](docs); for a shorter agent primer see [AGENTS.md](AGENTS.md).

## Project Overview

**tf-version-bump** is a Go CLI that updates Terraform module versions, `required_version` in terraform blocks, and provider versions in `required_providers`, across files matched by a glob. It parses HCL with HashiCorp's `hclwrite`; comments and structure survive while whitespace may be normalised when a changed file is formatted.

This repository is an experiment for generative AI coding tools. It may contain bugs or incomplete features. Keep changes under version control and test them.

**Stack**: Go 1.25+ (CI pins 1.26.8), `hashicorp/hcl/v2`, `hashicorp/terraform-registry-address`, `zclconf/go-cty`, `yaml.v3`, and `bmatcuk/doublestar/v4`. Dependency versions live in `go.mod` — don't restate them here or in AGENTS.md; they drift.

## Layout

All Go code is in a single flat `main` package — no subdirectories, no package graph.

```
main.go                  # CLI parsing, HCL processing, all version updates
config.go                # YAML config loading and validation
audit.go                 # -audit-file: read-only comparison of files with a config
*_test.go                # split by concern (see Testing)
schema/config-schema.json # JSON Schema for the YAML config
examples/                # Sample .tf/.yml files, runnable scenarios, branch and Actions automation
scripts/                 # Lint launchers and the Actions example's release-pin updater
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
make actionlint     # this repo's workflows, then the example's from a temporary repo copy
make shellcheck     # every tracked *.sh
make docs-check     # documentation, schema, example config and scenario tests
make test-github-actions # the Actions example harness (uses Docker)
```

Full validation before committing (mirrors CI):

```bash
go mod download && go mod verify
go test -v -race -coverprofile=coverage.out -covermode=atomic ./...
golangci-lint run --timeout=5m
make actionlint && make shellcheck
go build -o tf-version-bump .
```

**golangci-lint must match CI's version, currently v2.12** (see `.github/workflows/lint.yml`; `.golangci.yml` is `version: "2"` schema). It enables a curated linter set rather than the defaults, sets `gocyclo` min-complexity to 15, and lints test files too. Bump the version here when the workflow pins a new one.

```bash
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin v2.12
```

**shellcheck must match CI's pinned version, currently 0.11.0** (see `.github/workflows/lint.yml`, which verifies the release archive's SHA-256). actionlint also runs shellcheck over workflow `run:` blocks using the first shellcheck on PATH, so a different local version can disagree with CI in either direction. Bump the version and digest together.

## Gotchas

**Globbing uses `doublestar`, not `filepath.Glob`.** `findMatchingFiles` deliberately calls `doublestar.Glob` against an `fs.FS`, because `filepath.Glob` has no recursive wildcard — it treats `**` as a plain `*` that never crosses a separator. Don't "simplify" this back to the stdlib. With doublestar, `**` spans zero or more directories, so `**/*.tf` matches `top.tf`, `a/mid.tf` and `a/b/deep.tf`.

**The glob filesystem and options carry real weight — don't drop them.** `findMatchingFiles` uses `visibleDirFS` and two options, each fixing a way `**` can misbehave once it genuinely recurses:

- `visibleDirFS` — wildcard traversal skips dot-directories, so `**/*.tf` never descends into `.terraform/modules` (whose vendored copies `terraform init` regenerates, making any bump written there silently vanish). Because the non-glob base is resolved first, an explicit `.terraform/**/*.tf` still matches. This is deliberately shell-like; there is no custom exclusion list.
- `WithNoFollow` — without it a directory symlink matches the same physical file twice, and a symlink cycle matches it until the OS hits its link limit.
- `WithFilesOnly` — without it `-pattern "modules/**"` returns directories, which get counted as files and then fail with "is a directory".

Note this means the tool cannot protect a user who `cd`s into `.terraform` and globs from there; that is treated as deliberate, exactly as a shell would.

**HCL attribute tokens include their quotes.** Use the `trimQuotes` helper when reading an attribute's value; don't compare against raw token bytes.

**Never string-manipulate HCL.** Always go through the `hclwrite` API, or formatting and comments are lost. `hclwrite.Format()` may adjust whitespace — that's expected.

**Local modules are skipped by design.** `isLocalModule` treats `./`, `../` and `/` sources as local; Terraform gives them no version attribute, so there is nothing to bump.

**Don't run concurrent instances over the same files.** There is no file locking. A write copies the file's original bytes to a `tf-version-bump-backup-*` file in the system temporary directory, then rewrites the file in place: a failed rewrite is undone and the backup removed, and when the file's state cannot be confirmed the backup is kept, the error names it and `readTerraformFile` refuses the file for the rest of the run. A crash or power loss part way through a write can still leave a mixed file, and the backup may not survive a reboot. Files are processed in memory, so very large files (>100MB) are impractical.

## Architecture

### Update flow

`main()` → `validateOperationModes` → either standalone config validation or `findMatchingFiles` → `runAuditMode` (`-audit-file`: `buildAudit`, never writes Terraform files) or `runUpdateMode` → `runConfigFileMode` (YAML) / `runCLIMode` (one direct operation). Each update mode dispatches to one of three update paths: `processFiles` → `applyModuleVersion` for modules, `processTerraformVersion` → `updateTerraformVersionWithCount`, or `processProviderVersion` → `updateProviderVersionWithCount`.

`processFiles` parses each file once with `readTerraformFile`, then applies the module entries to it in YAML order through `applyModuleVersion`, which writes after each change unless in a dry run. Entries for one source therefore chain — an entry whose `from` lists an earlier entry's target moves the block again in the same run — and dry runs and checks report the same chain because the parsed file carries every earlier change. A file that cannot be read or parsed counts once per entry; each failed write counts once, and the file is read again for later entries, a read that a file whose write kept its backup refuses. `updateModuleVersionWithCount` is the single-entry wrapper most module tests call.

`applyModuleVersion` bundles its many parameters into a `moduleUpdateOptions` struct and delegates per-block work to `updateModuleBlockResult` → `shouldSkipModuleVersion`. Add new per-module filtering there rather than growing the parameter list.

Provider updates are the fiddliest path: `required_providers` entries can be either a nested block or an object expression, so `updateProviderVersionWithCount` branches through `updateProviderBlockSyntaxResult` and `updateProviderAttributeVersionResult` / `providerAttributeObject` / `replaceProviderObjectVersion`. Attribute-object updates replace only the version expression's byte range so other expressions such as `configuration_aliases` remain.

### Standard hclwrite pattern

```go
file, err := readTerraformFile(filename) // stat, read and parse; refuses a file a failed write left untrusted
if err != nil { return false, nil, err }

for _, block := range file.hcl.Body().Blocks() {
    if block.Type() == "module" {
        block.Body().SetAttributeValue("version", cty.StringVal(targetVersion))
    }
}

if updated && !dryRun {
    err = file.write() // hclwrite.Format, then an in-place rewrite that writes the original bytes back if it fails
}
```

All three update paths use `readTerraformFile` and `terraformFile.write`, so their stat, read, parse and write errors read identically.

### Module update precedence

Evaluated across `updateModuleBlockResult` and `shouldSkipModuleVersion`:

1. Source must match exactly.
2. Local sources are skipped.
3. `ignore_modules` matches the module _name_ → skip.
4. A missing version is skipped unless `-force-add` is set and the source is a registry module.
5. `ignore_versions` contains the current value → skip (takes precedence over `from`).
6. `from` is set and does not contain the current value → skip.
7. Otherwise update.

### Ignore-pattern matching

Custom wildcard matcher (`shouldIgnoreModule` → `matchPattern`), matching module **names**, not sources. `*` means zero or more characters: `vpc` (exact), `legacy-*` (prefix), `*-test` (suffix), `*-vpc-*` (contains).

An entry may be branch-scoped as `<branch-pattern>/<module-pattern>`. `splitIgnoreModuleEntry` (config.go) divides it at the **last** `/`, which is unambiguous because Terraform module names cannot contain one; `sanitizeModuleUpdates` rejects an empty part so `-validate-config` catches the mistake. `resolveBranchIgnoreModules` (main.go) then records the patterns applicable to `-branch` in `ModuleUpdate.resolvedIgnoreModules`, leaving `IgnoreModules` as the config writes it. Filtering reads the resolved list; the audit's `skip.values` lists every configured entry as written, including entries scoped to other branches, just as the other filters list their full configured values. The branch part reuses `matchPattern`, so `*` spans `/`.

**Both readers must go through `loadResolvedConfig`.** It is the only place that pairs `loadConfig` with `resolveBranchIgnoreModules`, so `runConfigFileMode` and `runAuditMode` cannot drift into disagreeing about which modules are excluded — the state-branch version report is built on the audit, so drift would mark deliberately excluded modules as out of date. Tests that build a `Config` literal must resolve it too (see `auditConfig` in audit_test.go).

A branch-scoped entry without `-branch` is a hard error: silently dropping the exclusion would bump a module the config protects. For the same reason `-branch HEAD` is rejected, because `git rev-parse --abbrev-ref HEAD` prints it on a detached checkout, and so is any value beginning `refs/`, because `GITHUB_REF` holds a full ref such as `refs/heads/main`. `origin/` is deliberately allowed: remotes can have any name, and a local branch may start with `origin/`. The docs recommend `git branch --show-current`, which is empty on a detached checkout. The tool never reads the branch from Git — the caller supplies it (the state-branch automation has it as `PROCESS_STATE_BRANCH`, and its processing checkout is detached anyway).

### Config shape (`config.go`)

Parsed with `KnownFields(true)` — unknown YAML keys are an error. `FromVersions` has a custom `UnmarshalYAML` accepting either a string or a list. Values are whitespace-trimmed and empties dropped (`trimNonEmptyStrings`).

```go
type ModuleUpdate struct {
    Source         string       // required
    Version        string       // required
    From           FromVersions // optional: only update from these versions
    IgnoreVersions FromVersions // optional
    IgnoreModules  []string     // optional: name patterns, optionally '<branch>/<name>', as written

    resolvedIgnoreModules []string // set by resolveBranchIgnoreModules; what filtering reads
}
```

Adding a config field means updating `schema/config-schema.json` too.

### Errors and output

File-level errors log and continue to the next file; bad flags, invalid globs, no file matches, and an unparseable config are fatal (`fatalf`). Warnings go to stderr prefixed `Warning:` for local modules, missing version attributes without `-force-add`, non-registry sources where `-force-add` cannot add a version, and a write's backup that could not be removed. Filtered modules are printed only with `-verbose`. Prefer skipping over guessing.

Success is prefixed `✓`; dry-run lines use `→` with the verb "Would update". User-facing values are wrapped with `quote(s, format)`: `'vpc'` for `text` output, `` `vpc` `` for `md`. Thread `outputFormat` through rather than hardcoding quotes.

Every summary ends with `failureNote`, which reports `N update(s) failed; see the errors on stderr` whenever the mode's *update* error count is non-zero, so a summary of what succeeded is never mistaken for the whole run. The note counts file-per-entry attempts, the same scale as the summaries and not the block counts `-report-file` writes, and every counted failure is logged where it happens, so the count is also the number of diagnostics. It names stderr rather than saying "above" because the summary goes to stdout, and a failure after the summary, such as a report that cannot be written, reaches only the fatal line. `skipNote` sits beside it and reports `N module(s) skipped; see the warnings on stderr`, counting only the skips that leave a block unpinned or defeat an explicit `-force-add` — a missing `version` attribute without `-force-add`, and `-force-add` on a source that cannot take a version. A module excluded by `ignore_modules`, `from` or `ignore_versions`, a local module and a module already at the target version were all skipped exactly as asked, so counting them would make the line noise; the count is threaded from `updateModuleBlockResult` through `applyModuleVersion` and `processFiles` as block indexes rather than a total, because entries for one source are applied to the same file in turn and the line speaks for modules, not for the attempts that reached them; exit codes are unchanged, because the updater cannot clear a skip on its own and a check that demanded one would loop. A run that updated nothing and failed, or left a module unpinned, omits its count line — the success line, or `would update 0 file(s)` in a dry run — rather than reporting a clean zero it cannot vouch for, and config mode says nothing about the config in that case, because a failed run must not send the operator to the config when the files or their permissions are at fault. A config-mode run that did nothing reports it as already applied, skipped or matching nothing, pointing at `-audit-file` for which — "skipped" covers the filters and the missing-version warning, and dropping it would repeat the fault this wording exists to fix. Direct mode says the same thing in its own words and points at nothing, because `-audit-file` needs a config and `-verbose` lists only the modules a filter skipped. A config that asks for nothing never reaches either message: `runConfigFileMode` refuses it, as `-validate-config` does, through the shared `Config.declaresNoUpdates`. That refusal lives in the runner rather than in `loadResolvedConfig`, which audit mode shares and which must keep auditing such a config rather than failing the report automation that calls it.

`-report-file` is the machine-readable automation contract. It writes schema version 2 JSON with exact counts of unique Terraform, module, and provider blocks whose version values changed across the complete command. A failed run writes no report, and removes any report already at the destination when — and only when — it changed files, so a consumer that reads the file without checking the exit status fails loudly instead of acting on counts for a tree that has since changed. `runUpdateMode` decides that from the update total and `-dry-run`, and the publish failure runs the same epilogue, because a run whose files changed and whose counts never landed is the case most certain to need it. A run refused for its config, a run whose every file failed and a dry run all leave the tree as they found it, so they leave the report too. Audit mode never removes its destination, for the same reason: a failed audit changes nothing on disk and the previous audit is still a true snapshot. Keep human summaries and the report separate: existing summaries count source/file operations, while the report counts individual changed blocks. Dry-run reports contain zero counts because no file values changed.

`-audit-file` is the read-only comparison contract. In config mode it writes schema version 1 JSON listing each configured Terraform, provider and module version value the selected files declare, its current and expected values, whether they already match and, for modules, the first filter that would skip an update. The audit and the updater share `moduleVersionFilter` and the `attributeHasStringValue` comparison, and `recordModuleBlock` judges a source's entries in YAML order against the version earlier entries would write, as `processFiles` applies them; keep the audit in step with any change to update filtering or ordering. Unlike the update modes, a selected file that cannot be read or parsed stops the audit: the command exits 1 and writes nothing, leaving any existing audit untouched.

`-check` uses the existing dry-run update paths but has a separate automation exit contract. The mode runners' update-operation total reaches `main` through `runUpdateMode`: a processing error exits 1, a successful check with a positive total exits 2, and a successful check with no eligible update returns normally with status 0. Check mode rejects `-dry-run` and `-report-file`, so it never writes Terraform or report files.

## Testing

Follow TDD. Tests are commonly table-driven with `t.Run` subtests; prefer `t.TempDir()` for new filesystem tests. Name them `Test<Function>_<Scenario>`.

Final test layout by concern:

- `pattern_test.go` — wildcard matching.
- `file_selection_test.go` — file selection and exclusions.
- `module_update_test.go` — module updates, filtering, diagnostics, permissions, and errors.
- `terraform_version_test.go` — Terraform required-version updates.
- `provider_update_test.go` — provider updates and attribute preservation.
- `audit_test.go` — audit entries, filter precedence, and agreement with the updater.
- `config_test.go` / `config_schema_test.go` — YAML configuration and schema validation.
- `documentation_test.go` — local documentation links, unwrapped Markdown prose, schema-backed examples, constraints, and runnable scenarios.
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
updated, changedBlocks, skipped, err := updateModuleVersionWithCount(
    tmpFile, "terraform-aws-modules/vpc/aws", "5.0.0",
    nil, nil, nil, // fromVersions, ignoreVersions, ignorePatterns
    false, false, false, "text", // forceAdd, dryRun, verbose, outputFormat
)
```

Copy files from `examples/` to a temporary directory before manual write-mode testing.

## CI

CI/Build and Lint run for every push and pull request targeting `main`, so their required status checks are always reported.

- **Test** — matrix of Go 1.25.14 (the go.mod floor) and 1.26.8, `-race` + coverage. The version-independent steps (branch automation, GitHub Actions POC checks, Codecov upload) run once, on the 1.26.8 leg flagged `primary` in the matrix.
- **Build** — needs Test; cross-compiles 6 targets (linux/darwin/windows × amd64/arm64)
- **Lint** — golangci-lint, then `make actionlint` and `make shellcheck` with pinned shellcheck
- **Documentation** — a separate path-filtered workflow runs `make docs-check` for Markdown, schema, maintained example, and documentation-test changes
- **CodeQL** and **Release** (GoReleaser + SLSA, tag-triggered) run separately

## Conventions

CLI flags and the YAML config format are user-facing contracts — don't break them. The JSON Schema accepts common Terraform constraint syntax (`1.0.0`, `~> 3.0`, `>= 1.5, < 2.0`, pre-release, build metadata), but the runtime YAML loader does not execute that schema. Keep the dependency list minimal. Use Australian/British spelling in prose and comments. Write each Markdown paragraph, list item and quote on one line; `TestDocumentationProseIsNotHardWrapped` fails on hard-wrapped prose.
