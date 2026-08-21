# Contract-Centred Test-Suite Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement
> this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the 219-test historical suite with a smaller, responsibility-based suite whose
tests each protect a distinct observable contract while retaining at least 90% statement coverage.

**Architecture:** Establish shared capture and exit helpers, add one focused target test file per
production responsibility, and then delete the historical bucket files wholesale once their
valuable contracts have explicit owners. Every migration batch is race- and coverage-checked; no
production change is mixed into a cleanup commit.

**Tech Stack:** Go 1.26+, standard `testing`, HashiCorp HCL, `doublestar`, YAML v3, Markdown,
`gofmt`, the Go race detector, and `golangci-lint` v2.12.

**Spec:** `docs/superpowers/specs/2026-08-20-test-suite-cleanup-design.md`

## Global Constraints

- Read the specification before editing tests.
- Work on a topic branch and use `/Users/dan/.codex/bin/codex-git` for every Git operation.
- Pull with rebase before beginning execution; if the worktree is dirty, stop and ask Dan how to
  handle it.
- Every Codex-created commit must be signed. Stop immediately if signing fails.
- Immediately after every commit, run
  `/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump verify-commit HEAD`; stop if
  verification fails.
- Do not push without Dan's explicit approval.
- Do not commit modifications to `main.go` or `config.go` in a cleanup task. The explicit
  uncommitted sensitivity mutations in this plan are permitted only long enough to observe the
  named failure and must be reversed with `apply_patch` before any other step.
- If a strengthened test exposes a production defect, stop that cleanup task, add one focused
  failing test, and fix the defect in a separate TDD commit before resuming.
- Use only the existing test stack; do not add an assertion library, snapshot system, golden-file
  framework, mutation-testing dependency, or new runner.
- Expected diagnostics must be captured and asserted. Passing verbose output may contain Go's
  `RUN`, `PASS`, and package-status lines, but no leaked application output.
- Every task must leave total statement coverage at or above 90.0%.
- Put coverage profiles under `/tmp`; never stage generated coverage or binary artefacts.
- Use `gofmt` for Go formatting and do not manually reformat unrelated code.
- Do not use `t.Parallel`: these package-main tests necessarily replace process-global flags,
  streams, logging, and exit hooks.
- Each task ends with a signed commit only after its focused tests, full tests, race test, and
  coverage gate pass.

---

## Final File Map

| Path | Final responsibility |
|---|---|
| `test_helpers_test.go` | Mechanical file creation, stream capture, runner capture, and exact exit interception |
| `pattern_test.go` | Canonical module-name wildcard matching and ignore-list iteration |
| `file_selection_test.go` | `doublestar` selection, sorting, hidden paths, symlinks, errors, and dry-run output |
| `config_test.go` | YAML decoding, `FromVersions`, sanitisation, strict fields, and validation errors |
| `config_schema_test.go` | Distinct JSON Schema contracts |
| `module_update_test.go` | Module update, skip, filter, preservation, dry-run, and filesystem contracts |
| `terraform_version_test.go` | Terraform constraint update, preservation, dry-run, and error contracts |
| `provider_update_test.go` | Provider syntax, selection, preservation, dry-run, and error contracts |
| `command_test.go` | Flags, validation, summaries, diagnostics, public command output, and exit status |
| `integration_test.go` | Cross-file continuation and cross-operation aggregation only |
| `release_workflow_test.go` | The real release workflow's immutable SLSA reference policy |

The following files are deleted after their valuable cases have owners:

```text
chaos_advanced_test.go
chaos_test.go
cli_functions_test.go
coverage_test.go
edge_cases_test.go
integration_config_test.go
main_coverage_test.go
main_integration_test.go
main_test.go
pattern_boundary_test.go
pattern_edge_case_test.go
terraform_provider_test.go
validation_test.go
```

### Task 1: Establish the baseline and shared test harness

**Files:**

- Create: `test_helpers_test.go`
- Modify: `main_coverage_test.go:1-120`
- Modify: `main_integration_test.go:1-51`

**Interfaces:**

- Consumes: production `hookMu`, `exitFunc`, and `main()`.
- Produces: `exitCall`, `stubExit`, `requireExitCall`, `withFlagArgs`, `captureRunnerOutput`,
  `captureStdout`, `captureStderr`, `captureLog`, `commandResult`, `runMainCommand`,
  `writeTestFile`, and `readTestFile` for later tasks.

- [ ] **Step 1: Confirm execution state and record the fresh baseline**

Run:

```bash
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump status --short --branch
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump pull --rebase origin main
rg --stats --count-matches '^func Test' -g '*_test.go'
rg --stats --count-matches '^' -g '*_test.go'
go test -count=1 ./...
go test -count=1 -race -coverprofile=/tmp/tf-version-bump-baseline.cover -covermode=atomic ./...
go tool cover -func=/tmp/tf-version-bump-baseline.cover
```

Expected: clean topic branch; 219 top-level tests; 11,631 test lines; tests and race detector pass;
total coverage is 98.8%. Record the package duration printed by both Go test commands for the final
comparison.

- [ ] **Step 2: Move the existing shared helpers into `test_helpers_test.go`**

Move these declarations without changing their behaviour:

```go
type exitCall struct {
	code int
}

func stubExit(t *testing.T) (restore func(), code *int)
func withFlagArgs(t *testing.T, args []string, fn func())
type commandResult struct {
	stdout      string
	diagnostics string
	exitCode    int
}
func runMainCommand(t *testing.T, args []string) commandResult
func captureRunnerOutput(t *testing.T, run func() error) (stdout, diagnostic string, runnerErr error)
```

Remove the original definitions from `main_coverage_test.go` and `main_integration_test.go` so the
package has exactly one owner for the harness. Remove imports used only by those moved declarations
from the two source files and add the complete required imports to `test_helpers_test.go`.

- [ ] **Step 3: Add exact exit and mechanical file helpers**

Add these helpers to `test_helpers_test.go`:

```go
func requireExitCall(t *testing.T, fn func()) {
	t.Helper()

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		fn()
	}()

	call, ok := recovered.(exitCall)
	if !ok {
		t.Fatalf("recovered value = %#v, want exitCall", recovered)
	}
	if call.code != 1 {
		t.Fatalf("exit code = %d, want 1", call.code)
	}
}

func writeTestFile(t *testing.T, dir, name, contents string) string {
	t.Helper()
	filename := filepath.Join(dir, name)
	if err := os.WriteFile(filename, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", filename, err)
	}
	return filename
}

func readTestFile(t *testing.T, filename string) string {
	t.Helper()
	contents, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("read %s: %v", filename, err)
	}
	return string(contents)
}
```

Add a package-level `var testOutputMu sync.Mutex`. Add these helpers beside the existing runner
capture:

```go
func captureStream(t *testing.T, stream **os.File, name string, fn func()) string {
	t.Helper()
	testOutputMu.Lock()
	defer testOutputMu.Unlock()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create %s pipe: %v", name, err)
	}
	original := *stream
	restore := func() { *stream = original }
	t.Cleanup(func() {
		restore()
		_ = writer.Close()
		_ = reader.Close()
	})
	defer restore()
	*stream = writer

	fn()
	restore()
	if err := writer.Close(); err != nil {
		t.Fatalf("close %s writer: %v", name, err)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(output)
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	return captureStream(t, &os.Stdout, "stdout", fn)
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	return captureStream(t, &os.Stderr, "stderr", fn)
}

func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	testOutputMu.Lock()
	defer testOutputMu.Unlock()

	var output bytes.Buffer
	originalWriter := log.Writer()
	originalFlags := log.Flags()
	originalPrefix := log.Prefix()
	restore := func() {
		log.SetOutput(originalWriter)
		log.SetFlags(originalFlags)
		log.SetPrefix(originalPrefix)
	}
	t.Cleanup(restore)
	defer restore()
	log.SetOutput(&output)
	log.SetFlags(0)
	log.SetPrefix("")
	fn()
	return output.String()
}
```

Apply `testOutputMu` once around the sections of `captureRunnerOutput` and `runMainCommand` that
replace process-global streams or logging. Keep the production exit hook protected by the existing
`hookMu`.

- [ ] **Step 4: Format and verify the harness move**

Run:

```bash
gofmt -w test_helpers_test.go main_coverage_test.go main_integration_test.go
go test -run '^(TestCommandReportsAggregateFileFailureCLI|TestRunCLIModeReportsModuleFileFailure)$' -count=1
go test -count=1 ./...
go test -count=1 -race -coverprofile=/tmp/tf-version-bump-task1.cover -covermode=atomic ./...
go tool cover -func=/tmp/tf-version-bump-task1.cover
```

Expected: all commands pass and total coverage remains at least 90.0%.

- [ ] **Step 5: Review and commit the harness**

Run:

```bash
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump diff --check
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump status --short
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump add test_helpers_test.go main_coverage_test.go main_integration_test.go
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump commit -m "test: centralise process test helpers"
```

Expected: a signed commit; no production files staged.

### Task 2: Consolidate pattern and file-selection contracts

**Files:**

- Create: `pattern_test.go`
- Create: `file_selection_test.go`
- Reference only: `main_test.go`, `pattern_boundary_test.go`, `pattern_edge_case_test.go`,
  `edge_cases_test.go`, `chaos_test.go`, `chaos_advanced_test.go`, `validation_test.go`,
  `cli_functions_test.go`, `main_coverage_test.go`

**Interfaces:**

- Consumes: `matchPattern`, `shouldIgnoreModule`, `findMatchingFiles`, `cliFlags`,
  `captureRunnerOutput`, `captureLog`, `stubExit`, and `requireExitCall`.
- Produces: the sole final owners of matching and file-selection behaviour. Historical sources
  remain temporarily and are deleted in Tasks 6 and 7.

- [ ] **Step 1: Add the canonical pattern contract table**

Create `pattern_test.go` with one table named `TestMatchPatternContract`. Use this exact behavioural
set; do not copy the larger overlapping tables:

```go
tests := []struct {
	name, input, pattern string
	want                 bool
}{
	{name: "exact", input: "vpc", pattern: "vpc", want: true},
	{name: "different literal", input: "vpc", pattern: "s3", want: false},
	{name: "wildcard matches empty", input: "", pattern: "*", want: true},
	{name: "prefix", input: "legacy-vpc", pattern: "legacy-*", want: true},
	{name: "suffix", input: "vpc-test", pattern: "*-test", want: true},
	{name: "contains", input: "prod-vpc-test", pattern: "*-vpc-*", want: true},
	{name: "ordered middles", input: "aws-prod-vpc-au", pattern: "aws-*-vpc-*", want: true},
	{name: "middles out of order", input: "aws-vpc-prod-au", pattern: "aws-*-prod-vpc-*", want: false},
	{name: "missing middle", input: "aws-prod-s3-au", pattern: "aws-*-vpc-*", want: false},
	{name: "overlap too short", input: "abc", pattern: "abc*abc", want: false},
	{name: "overlap minimum", input: "abcabc", pattern: "abc*abc", want: true},
	{name: "zero-width middle", input: "module-test", pattern: "module*-test", want: true},
	{name: "repeated part", input: "a-b-a-b", pattern: "a-*-a-*", want: true},
	{name: "Unicode", input: "módulo-vpc-produção", pattern: "módulo-*-produção", want: true},
}
```

For every row, call `matchPattern` and compare `got` with `want`. Add one compact
`TestShouldIgnoreModuleContract` table with exactly four rows: empty module name is never ignored,
empty patterns do not ignore, the second pattern matches, and no pattern matches.

- [ ] **Step 2: Prove the canonical pattern table is sensitive**

Temporarily add `return true` as the first statement of `matchPattern`, run the focused test, and
confirm the false rows fail. Restore `main.go` with the inverse `apply_patch` before continuing.

Run:

```bash
go test -run '^TestMatchPatternContract$' -count=1
```

Expected during the controlled mutation: FAIL on `different_literal`, `middles_out_of_order`,
`missing_middle`, and `overlap_too_short`. After restoration: PASS and no diff in `main.go`.

- [ ] **Step 3: Add focused file-selection tests using exact result slices**

Create these tests in `file_selection_test.go`:

```go
func TestFindMatchingFilesRecursiveAndSorted(t *testing.T)
func TestFindMatchingFilesHiddenPathPolicy(t *testing.T)
func TestFindMatchingFilesExcludesSymlinksAndDirectories(t *testing.T)
func TestFindMatchingFilesRejectsInvalidSelection(t *testing.T)
func TestFindMatchingFilesReportsDryRun(t *testing.T)
```

Use the strongest fixtures from `cli_functions_test.go:113-395`, with these consolidations:

- `RecursiveAndSorted` creates top-level, one-level, and two-level `.tf` files plus a non-`.tf`
  file and asserts one exact lexically sorted slice using `slices.Equal`.
- `HiddenPathPolicy` creates a root dotfile, `.terraform`, `.git`, and a nested `.terraform` tree.
  A wildcard pattern must return the dotfile and ordinary file only; an explicit
  `.terraform/**/*.tf` pattern must return the explicitly selected vendored file.
- `ExcludesSymlinksAndDirectories` creates one real directory and file, a directory symlink, a
  file symlink, and a trailing-`**` pattern. Assert the exact sorted slice contains the real file
  and file symlink, while excluding the directory and directory symlink.
- `RejectsInvalidSelection` covers missing pattern, invalid `[` glob, and no matches. For each row,
  call `stubExit`, capture logs, invoke `findMatchingFiles` through `requireExitCall`, and assert the
  exact diagnostic including its newline.
- `ReportsDryRun` captures stdout around a successful one-file selection and asserts exactly:

```go
want := "Found 1 file(s) matching pattern '" + pattern + "'\n" +
	"Running in dry-run mode - no files will be modified\n"
```

Invoke every successful selection through `captureRunnerOutput`, assert empty diagnostics, and
assert the exact `Found N file(s)` line. This prevents the non-dry-run selection tests from leaking
their normal output under `go test -v`.

- [ ] **Step 4: Prove ordering and dry-run assertions are sensitive**

Temporarily remove the `slices.Sort(files)` call from `findMatchingFiles`, run
`TestFindMatchingFilesRecursiveAndSorted`, and confirm it fails on the deliberately mixed-depth
fixture. Restore the call with `apply_patch` and rerun the test. Then temporarily change the dry-run
fixture's expected second line to `dry run\n`, confirm failure, restore it, and rerun.

Run:

```bash
go test -run '^TestFindMatchingFiles(RecursiveAndSorted|ReportsDryRun)$' -count=1
```

Expected after restoration: PASS and no diff in `main.go`.

- [ ] **Step 5: Format, verify, and commit matching ownership**

Run:

```bash
gofmt -w pattern_test.go file_selection_test.go
go test -run '^(TestMatchPatternContract|TestShouldIgnoreModuleContract|TestFindMatchingFiles.*)$' -count=1
go test -count=1 ./...
go test -count=1 -race -coverprofile=/tmp/tf-version-bump-task2.cover -covermode=atomic ./...
go tool cover -func=/tmp/tf-version-bump-task2.cover
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump diff --check
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump add pattern_test.go file_selection_test.go
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump commit -m "test: consolidate matching and file selection contracts"
```

Expected: all checks pass, coverage is at least 90.0%, and the signed commit contains only the two
new test owners.

### Task 3: Collapse configuration and schema tests

**Files:**

- Replace: `config_test.go`
- Modify: `config_schema_test.go`
- Delete after the separate TDD defect fix: `from_versions_regression_test.go`
- Reference only: configuration cases in `chaos_test.go`, `chaos_advanced_test.go`,
  `edge_cases_test.go`, `coverage_test.go`, and `integration_config_test.go`

**Interfaces:**

- Consumes: `FromVersions.UnmarshalYAML`, `loadConfig`, `sanitizeProviderUpdates`,
  `sanitizeModuleUpdates`, `Config`, and the real schema file.
- Produces: the sole final unit-test owner of YAML decoding and validation. Cross-operation config
  execution remains for Task 6.

**Implementation discovery:** YAML v3 coerces non-string scalar sequence items when decoding
directly into `[]string`, so `[4.0.0, 4]` exposed a production defect. Before resuming this cleanup
task, fix that defect in the separate TDD commit required by the global constraints, using
`from_versions_regression_test.go` as the focused RED test. Once this task's canonical
`TestFromVersionsUnmarshalYAML` owns the same contract, delete the temporary regression file in the
cleanup commit.

- [ ] **Step 1: Replace the `FromVersions` permutations with one node-kind table**

Use a small wrapper so every row exercises YAML decoding rather than constructing mocked nodes:

```go
type fromDocument struct {
	From FromVersions `yaml:"from"`
}
```

Create `TestFromVersionsUnmarshalYAML` with exactly these YAML values and expectations:

| YAML | Result |
|---|---|
| `from: 4.0.0` | `FromVersions{"4.0.0"}` |
| `from: [4.0.0, "~> 3.0"]` | both strings in order |
| `from: ""` | empty slice |
| `from: 4` | error contains `must be a string or array of strings` |
| `from: [4.0.0, 4]` | error contains `array contains non-string values` |
| `from: {old: 4.0.0}` | error contains `must be either a string or an array of strings` |

Compare successful values with `slices.Equal`. Assert the stated stable error component for each
failure; do not accept merely non-empty errors.

- [ ] **Step 2: Add one comprehensive successful `loadConfig` contract**

Create `TestLoadConfigSanitisesAllOperations`. Write one config containing a trimmed Terraform
constraint, one provider, and one module with scalar `from`, two `ignore_versions`, and
`ignore_modules` containing surrounding whitespace and an empty entry. Assert `reflect.DeepEqual`
against this exact value:

```go
Config{
	TerraformVersion: ">= 1.6",
	Providers: []ProviderUpdate{
		{Name: "aws", Version: "~> 5.0"},
	},
	Modules: []ModuleUpdate{
		{
			Source:         "terraform-aws-modules/vpc/aws",
			Version:        "5.0.0",
			From:           FromVersions{"4.0.0"},
			IgnoreVersions: FromVersions{"3.0.0", "~> 3.0"},
			IgnoreModules:  []string{"legacy-*", "*-test"},
		},
	},
}
```

This single test owns successful decoding, trimming, empty-entry removal, and the complete config
shape. Do not retain separate comment, quote-style, long-string, Git-source, local-source, or
duplicate-source decoding tests.

- [ ] **Step 3: Add focused load and validation failures**

Create `TestLoadConfigRejectsInvalidInput` with one row for each distinct branch:

- unknown top-level field;
- malformed YAML;
- module missing source;
- module missing version;
- valid module followed by a module missing source at index 1;
- provider missing name;
- provider missing version; and
- valid provider followed by a provider missing name at index 1.

Assert the exact validation error for missing fields, and a stable parser component for YAML
failures. `TestFromVersionsUnmarshalYAML/mapping` is the precise decoder owner for invalid `from`
mapping input, and the malformed-YAML row already owns `loadConfig`'s parse wrapper. Add
`TestLoadConfigReadError`, use a missing file, require
`errors.Is(err, os.ErrNotExist)`, and assert the `failed to read config file:` wrapper. Add one
`TestLoadConfigEmptyDocument` case for the valid empty/EOF contract.

- [ ] **Step 4: Keep only distinct schema consumer contracts**

Retain the two real-schema tests in `config_schema_test.go`. Remove only assertions genuinely
owned by `loadConfig`; retain schema-only editor/validator contracts. The final tests must prove:

- the schema exposes `terraform_version`, `providers`, module `from`, `ignore_versions`, and
  `ignore_modules` with their documented scalar/array shapes;
- `from` and `ignore_versions` expose exactly two order-independent alternatives: a scalar string
  whose complete assertion-bearing structure is one shared version-constraint reference plus
  annotation-only metadata, and an array with only the array/items/minimum assertions plus
  annotation-only metadata; contradictory or undocumented assertion keywords are rejected;
- the top-level `anyOf` contains exactly those three singleton required clauses, one per operation;
- the schema has no unconditional top-level `required` fields, so provider-only and
  `terraform_version`-only documents remain valid; and
- provider items require `name` and `version`, and provider/module versions reference the shared
  version-constraint definition; and
- the version-pattern schema accepts Terraform constraints including
  `~> 3.0.0-beta.1+build.5`, rejects an empty version, and rejects non-empty malformed values
  such as `invalid` and `1.2.3.4`.

Both tests must unmarshal `schema/config-schema.json` and inspect the real schema tree. Do not build
synthetic schema fragments.

- [ ] **Step 5: Prove strict decoding and sanitisation assertions are sensitive**

Temporarily change `decoder.KnownFields(true)` to `decoder.KnownFields(false)`. Run the unknown-field
subtest and confirm it fails because no error is returned. Restore the production line. Then
temporarily remove `strings.TrimSpace` from the Terraform-version assignment, run
`TestLoadConfigSanitisesAllOperations`, confirm the exact struct comparison fails, and restore it.

Run:

```bash
go test -run '^(TestFromVersionsUnmarshalYAML|TestLoadConfig.*|TestConfigSchema.*)$' -count=1
```

Expected after both restorations: PASS and no diff in `config.go`.

- [ ] **Step 6: Format, verify, and commit configuration ownership**

Run:

```bash
gofmt -w config_test.go config_schema_test.go
go test -run '^(TestFromVersionsUnmarshalYAML|TestLoadConfig.*|TestConfigSchema.*)$' -count=1
go test -count=1 ./...
go test -count=1 -race -coverprofile=/tmp/tf-version-bump-task3.cover -covermode=atomic ./...
go tool cover -func=/tmp/tf-version-bump-task3.cover
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump diff --check
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump status --short
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump add config_test.go config_schema_test.go from_versions_regression_test.go
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump commit -m "test: reduce configuration tests to distinct contracts"
```

Expected: all checks pass, coverage remains at least 90.0%, the temporary regression file is
deleted after its contract moves to `config_test.go`, and the commit is signed.

### Task 4: Consolidate module-update contracts

**Files:**

- Create: `module_update_test.go`
- Reference only: module cases in `main_test.go`, `coverage_test.go`, `main_coverage_test.go`,
  `main_integration_test.go`, `chaos_test.go`, `chaos_advanced_test.go`, and `edge_cases_test.go`

**Interfaces:**

- Consumes: `updateModuleVersion`, `isLocalModule`, `captureStdout`,
  `captureStderr`, `writeTestFile`, and `readTestFile`.
- Produces: the sole final owner of module-update unit behaviour. Command-level aggregation remains
  for Task 6.

- [ ] **Step 1: Add one focused module update table**

Create `TestUpdateModuleVersionContract` using a table with explicit `input`, `source`, `version`,
filters, flags, `wantUpdated`, and complete `wantContent`. Include exactly these rows:

- matching registry module updates;
- non-matching source remains byte-for-byte unchanged;
- force-add adds a missing version to a registry module;
- relative local source is skipped even with force-add;
- matching `from` filter updates;
- second `from` entry matches;
- non-matching `from` filter preserves the original version;
- matching `ignore_versions` preserves the original version;
- second `ignore_versions` entry matches and preserves the original version;
- mixed ignored and eligible modules update only the eligible module; and
- every eligible block with the same source updates;
- an ignored version that does not match `from` remains unchanged and, with verbose output,
  reports the `ignore-version` reason rather than the `from` reason; and
- module-name ignore preserves unchanged content.

The same-source row owns continuation after a successful update, while the existing mixed row owns
continuation after a skipped update. The two second-entry rows own list-as-OR matching through
observable updater behaviour. The module-name ignore row owns ignore-pattern preservation.

For changed files, compare against a literal, fully formatted HCL result. For skipped files,
compare against the exact original input. Capture stderr for the local-source row and assert its
exact local-module warning. Capture stdout for the non-matching `from`, ignore-version precedence,
and module-name-ignore rows; assert each final exact diagnostic and empty stderr. The remaining rows
emit no output. Do not use
`!strings.Contains(oldVersion)` as the success condition.

Use this options shape in the table so every filter is visible:

```go
type moduleCase struct {
	name           string
	input          string
	source         string
	version        string
	from           []string
	ignoreVersions []string
	ignoreModules  []string
	forceAdd       bool
	dryRun         bool
	verbose        bool
	wantUpdated    bool
	wantContent    string
}
```

Set `verbose: true` on the non-matching `from`, ignore-version precedence, and module-name-ignore
rows, and pass every table field directly to
`updateModuleVersion` with output format `"text"`.

- [ ] **Step 2: Repair the false missing-version warning test with a RED fixture**

Create `TestUpdateModuleVersionWarnsWhenVersionMissing`. First use the old faulty local-source
fixture while expecting the registry missing-version warning:

```go
module "vpc" {
  source = "./modules/vpc"
}
```

Capture `os.Stderr`, call `updateModuleVersion`, and assert the exact missing-version warning.

Run:

```bash
go test -run '^TestUpdateModuleVersionWarnsWhenVersionMissing$' -count=1
```

Expected: FAIL because the actual diagnostic says the source is a local module. Change only the
fixture source to `terraform-aws-modules/vpc/aws`, rerun, and expect PASS. Also assert
`updated == false`, `err == nil`, and exact unchanged file content.

- [ ] **Step 3: Add preservation and dry-run contracts**

Add these focused tests:

```go
func TestUpdateModuleVersionPreservesHCL(t *testing.T)
func TestUpdateModuleVersionPreservesPermissions(t *testing.T)
func TestUpdateModuleVersionDryRunContract(t *testing.T)
```

`PreservesHCL` uses the strongest fixture from the existing
`TestUpdateModuleVersionPreservesFormatting`: comments, unrelated attributes, and a non-target
module must survive while the target version changes. Assert the complete output.
`PreservesPermissions` uses mode `0o640`, performs a real update, and asserts `Mode().Perm()` is
still `0o640`. `DryRun` captures all emitted output, asserts `updated == true`, and asserts the file
is byte-for-byte unchanged.

- [ ] **Step 4: Add only credible module helper and error boundaries**

Add:

- `TestIsLocalModuleContract` with relative, parent-relative, absolute, registry, and Git source
  rows;
- `TestUpdateModuleVersionErrors` with missing-file/stat and malformed-HCL rows; and
- a write-error row using a read-only file, skipped only when `os.Geteuid() == 0`, with the
  `failed to write file:` wrapper asserted.

Do not retain a separate `processFiles` error-count test. Task 6 exercises that path through the
observable CLI continuation and aggregate-error contracts.

Do not copy long-string, nested-source, query-string, whitespace-only, duplicate-name, BOM, binary,
or arbitrary character permutations.

- [ ] **Step 5: Prove full-state and precedence assertions are sensitive**

Temporarily change `SetAttributeValue("version", cty.StringVal(opts.version))` to set
`cty.StringVal("wrong")`. Run the module contract and preservation tests and confirm they fail on
exact content. Restore the line. Temporarily swap the `ignoreVersions` and `fromVersions` branches
in `shouldSkipModuleVersion`; run the precedence row and confirm its exact verbose diagnostic
fails, then restore the original order.

Run:

```bash
go test -run '^(TestUpdateModuleVersion.*|TestIsLocalModuleContract)$' -count=1
```

Expected after restoration: PASS and no diff in `main.go`.

- [ ] **Step 6: Format, verify, and commit module ownership**

Run:

```bash
gofmt -w module_update_test.go
go test -run '^(TestUpdateModuleVersion.*|TestIsLocalModuleContract)$' -count=1
go test -count=1 ./...
go test -count=1 -race -coverprofile=/tmp/tf-version-bump-task4.cover -covermode=atomic ./...
go tool cover -func=/tmp/tf-version-bump-task4.cover
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump diff --check
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump add module_update_test.go
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump commit -m "test: consolidate module update contracts"
```

Expected: all checks pass, coverage remains at least 90.0%, and the commit is signed.

### Task 5: Consolidate Terraform and provider contracts

**Files:**

- Create: `terraform_version_test.go`
- Create: `provider_update_test.go`
- Reference only: `terraform_provider_test.go`, `main_integration_test.go`, and
  `main_coverage_test.go`

Do not edit these reference-only files in Task 5. Superseded tests remain temporarily and are
deleted with their containing files wholesale in Task 6.

**Interfaces:**

- Consumes: `updateTerraformVersion`, `updateProviderVersion`, `writeTestFile`, and
  `readTestFile`.
- Produces: the sole final unit-test owners for Terraform and provider updates. Runner aggregation
  remains for Task 6.

- [ ] **Step 1: Add the Terraform-version contract table**

Create `TestUpdateTerraformVersionContract` with complete input and output strings for:

- update an existing `required_version` while preserving `required_providers`;
- update every Terraform block containing `required_version`;
- leave a file with no Terraform block unchanged; and
- add `required_version` to a Terraform block without one while preserving its
  `required_providers`.

Assert `updated`, `err`, and exact file content for every row. Add
`TestUpdateTerraformVersionDryRunContract`: assert `updated == true`, no error, and byte-for-byte
unchanged content.

- [ ] **Step 2: Add Terraform real-error contracts**

Add `TestUpdateTerraformVersionErrors` covering missing file, malformed HCL, and write failure. Use
`errors.Is(err, os.ErrNotExist)` for the missing file, assert `failed to parse HCL:` for malformed
input, and retain the read-only-file skip only for effective UID zero. Do not migrate a separate
`processTerraformVersion` counter test into the new owners; Task 6 deletes the legacy copies
wholesale. Task 6 owns continuation and aggregate failure through the runner boundary.

- [ ] **Step 3: Add real-file provider syntax and preservation contracts**

Create `TestUpdateProviderVersionContract` with exact input/output rows for:

- legacy provider block syntax;
- attribute syntax with `source` and `version`;
- attribute syntax preserving `configuration_aliases = [aws.alternate]`;
- mixed providers where only the named provider changes;
- mixed block and attribute syntax across two Terraform blocks; and
- a missing target provider leaving the file byte-for-byte unchanged.

Every row must call `updateProviderVersion` on a real temporary file. Do not migrate the mocked-AST
`TestUpdateProviderAttributeVersionVariants` cases. Leave the legacy file untouched in Task 5;
Task 6 deletes it wholesale.

- [ ] **Step 4: Add provider dry-run and error contracts**

Add:

```go
func TestUpdateProviderVersionDryRunContract(t *testing.T)
func TestUpdateProviderVersionErrors(t *testing.T)
```

Mirror the Terraform boundaries using real provider HCL. The dry-run test must prove
`updated == true` without file mutation. Do not migrate a separate `processProviderVersion`
counter test into the new owners; Task 6 deletes the legacy copies wholesale. Task 6 owns
continuation and aggregate failure through the runner boundary.

- [ ] **Step 5: Prove provider preservation is sensitive**

Immediately after the successful `providerAttributeObject` call in
`updateProviderAttributeVersion`, temporarily insert this early return:

```go
nestedBlock.Body().SetAttributeValue(providerName, cty.ObjectVal(map[string]cty.Value{
	"version": cty.StringVal(newVersion),
}))
return true
```

Run the provider contract and confirm the `configuration_aliases` and `source` preservation rows
fail. Remove the inserted block with `apply_patch`, rerun, and confirm PASS. Do not commit the
controlled mutation.

Run:

```bash
go test -run '^(TestUpdateTerraformVersion.*|TestUpdateProviderVersion.*)$' -count=1
```

Expected after restoration: PASS and no diff in `main.go`.

- [ ] **Step 6: Format, verify, and commit Terraform/provider ownership**

Run:

```bash
gofmt -w terraform_version_test.go provider_update_test.go
go test -run '^(TestUpdateTerraformVersion.*|TestUpdateProviderVersion.*)$' -count=1
go test -count=1 ./...
go test -count=1 -race -coverprofile=/tmp/tf-version-bump-task5.cover -covermode=atomic ./...
go tool cover -func=/tmp/tf-version-bump-task5.cover
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump diff --check
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump add terraform_version_test.go provider_update_test.go
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump commit -m "test: consolidate Terraform and provider contracts"
```

Expected: all checks pass, coverage remains at least 90.0%, and the commit is signed.

### Task 6: Consolidate command and integration contracts

**Files:**

- Create: `command_test.go`
- Create: `integration_test.go`
- Delete: `cli_functions_test.go`
- Delete: `coverage_test.go`
- Delete: `integration_config_test.go`
- Delete: `main_coverage_test.go`
- Delete: `main_integration_test.go`
- Delete: `main_test.go`
- Delete: `terraform_provider_test.go`

**Interfaces:**

- Consumes: all helpers from `test_helpers_test.go`; production `parseFlags`, `loadModuleUpdates`,
  `validateOperationModes`, `runCLIMode`, `runConfigFileMode`, `quote`, and `main`.
- Produces: the sole final owners of command-boundary and cross-operation behaviour; removes the
  broad historical core, coverage, CLI, and integration files.

- [ ] **Step 1: Add compact flag and formatting contracts**

In `command_test.go`, add:

```go
func TestParseFlagsContract(t *testing.T)
func TestLoadModuleUpdatesContract(t *testing.T)
```

`ParseFlagsContract` parses one all-options argument list through `withFlagArgs` and compares the
complete `cliFlags` value. `LoadModuleUpdatesContract` asserts one exact `ModuleUpdate`, including trimmed
comma-separated ignore patterns and both repeated version flag slices.

- [ ] **Step 2: Replace permissive fatal-path tests with exact exit tests**

Add `TestParseFlagsRejectsInvalidOutput`, `TestValidateOperationModesContract`,
`TestLoadModuleUpdatesRequiresFlags`, and `TestRunCLIModeRequiresProviderVersion`.

For every fatal row:

1. call `stubExit` and immediately register `t.Cleanup(restoreExit)` inside that fatal-row
   subtest;
2. capture the relevant log or stdout stream;
3. invoke only the expected production call through `requireExitCall(t, fn)`; the helper requires
   exact exit code 1; and
4. assert the exact diagnostic or stable usage prefix.

`TestValidateOperationModesContract` must call the real function for three invalid cases: config
mixed with module flags, no operation, and multiple operations. Valid Terraform and provider
returns are owned more strongly by the full-main command tests. The no-operation row asserts the
printed `Usage:` prefix and exact exit code. This replaces the test that merely built flags without
invoking production code.

- [ ] **Step 3: Add exact public command contracts**

Move the strong `TestCommandReportsAggregateFileFailureCLI` and
`TestCommandReportsAggregateFileFailureConfig` scenarios into one table named
`TestCommandReportsAggregateFileFailure` with `CLI` and `config` rows. Preserve exact stdout,
diagnostics, exit code 1, continuation, and exact resulting valid-file content.

Add `TestCommandVersion` by temporarily setting and restoring the package `version`, `commit`, and
`date` globals. Assert this exact output and exit code 0:

```text
tf-version-bump 1.2.3
  commit: abc123
  built:  2026-08-20
```

Add `TestRunConfigFileModeReturnsLoadErrorContract`, requiring no exit panic,
`errors.Is(err, os.ErrNotExist)`, and the `Error loading config file:` wrapper.

Add `TestRunCLIModeMarkdownOutput` with one real module update and `output: "md"`; capture stdout
and assert the exact update line uses backticks around the module source and version, followed by
the exact summary `\nSuccessfully updated 1 file(s)\n`. Require the complete output, including its
trailing newline.
Add `TestCommandNoMatchingModuleIsSuccess` through `runMainCommand`; use one selected file with a
different module source and assert exact zero-update stdout, empty diagnostics, unchanged content,
and `exitCode == -1` because normal success returns without invoking the exit hook.

Add `TestCommandDryRunOutputContract` as one three-row real-command table for module, Terraform,
and provider modes. The module row includes a matching `-from` value. Every row asserts the exact
selection banner, dry-run action line, operation-specific dry-run summary, empty diagnostics,
normal-return status, and byte-for-byte non-mutation. The module row owns text output, while the
Terraform and provider rows own Markdown quoting through the direct dry-run plumbing. This table
owns direct-operation dry-run output; lower-level updater tests continue to own only update
detection and non-mutation.

Add `TestCommandConfigDryRunOutputContract` as the combined config-mode owner for Terraform,
provider, and module dry-run execution. It runs with Markdown output and asserts backtick quoting
for the selection banner and all three operation values, along with the exact summary,
normal-return status, empty diagnostics, and byte-for-byte non-mutation of the selected file.

The deleted `TestQuoteContract` is subsumed by exact text quoting in command and integration output
assertions, and by exact Markdown quoting in `TestRunCLIModeMarkdownOutput`.

- [ ] **Step 4: Add only cross-boundary integration scenarios**

Create these tests in `integration_test.go`:

```go
func TestRunCLIModeContinuesAfterFileFailure(t *testing.T)
func TestRunConfigFileModeAppliesCombinedUpdates(t *testing.T)
func TestRunConfigFileModeAggregatesMixedFailures(t *testing.T)
```

`ContinuesAfterFileFailure` is the final two-row table for Terraform and provider modes. Each row
receives a malformed file followed by a valid file, captures runner output, asserts the exact runner
error count/type, and compares the valid file with complete expected content. The stronger
`TestCommandReportsAggregateFileFailure/CLI` module owner covers module continuation.

`AppliesCombinedUpdates` uses one real config containing Terraform, provider, and module updates
against two valid files. Assert exact final HCL for both files, empty diagnostics, nil error,
exact per-file output ordering, and exact summary counts of two for Terraform, provider, and module
updates. This owner replaces separate two-successful-file processor tests.

`AggregatesMixedFailures` reuses the fixture from the existing
`TestRunConfigModeAggregatesMixedFileFailures` scenario: malformed files precede a valid file and
all three later operations continue. Strengthen the new owner to assert the exact final HCL,
stdout, diagnostic sequence, and returned `10 update error(s)` aggregate.

Do not migrate the individual config-mode module/Terraform/provider error tests; the mixed test
owns cross-operation aggregation, while the unit and CLI-mode tables own the individual paths.

- [ ] **Step 5: Verify the new owners before deleting legacy files**

Run:

```bash
gofmt -w command_test.go integration_test.go
go test -run '^(TestParseFlagsContract|TestLoadModuleUpdatesContract|TestParseFlagsRejectsInvalidOutput|TestValidateOperationModesContract|TestLoadModuleUpdatesRequiresFlags|TestRunCLIModeRequiresProviderVersion|TestCommandReportsAggregateFileFailure|TestCommandVersion|TestRunConfigFileModeReturnsLoadErrorContract|TestRunCLIModeMarkdownOutput|TestCommandNoMatchingModuleIsSuccess|TestCommandDryRunOutputContract|TestCommandConfigDryRunOutputContract|TestRunCLIModeContinuesAfterFileFailure|TestRunConfigFileModeAppliesCombinedUpdates|TestRunConfigFileModeAggregatesMixedFailures)$' -count=1
```

Expected: PASS with all expected process output captured.

- [ ] **Step 6: Delete the broad legacy files wholesale**

Delete exactly:

```text
cli_functions_test.go
coverage_test.go
integration_config_test.go
main_coverage_test.go
main_integration_test.go
main_test.go
terraform_provider_test.go
```

Do not copy the no-panic summary tests, output-format file-only tests, hand-reimplemented
conditionals, mocked provider AST tests, duplicate dry-run/process matrices, or negative-only
assertions from those files.

- [ ] **Step 7: Run the deletion coverage gate**

Run:

```bash
go test -count=1 ./...
go test -count=1 -race -coverprofile=/tmp/tf-version-bump-task6.cover -covermode=atomic ./...
go tool cover -func=/tmp/tf-version-bump-task6.cover
rg --stats --count-matches '^func Test' -g '*_test.go'
rg --stats --count-matches '^' -g '*_test.go'
```

Expected: all tests pass, no application diagnostics leak, and total coverage remains at least
90.0%. If coverage falls below 90.0%, use `go tool cover -func` to identify the uncovered
production branch and restore only the smallest meaningful contract from the deleted files.

- [ ] **Step 8: Review and commit command/integration consolidation**

Run:

```bash
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump diff --check
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump status --short
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump add command_test.go integration_test.go cli_functions_test.go coverage_test.go integration_config_test.go main_coverage_test.go main_integration_test.go main_test.go terraform_provider_test.go
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump commit -m "test: replace broad suites with contract owners"
```

Expected: a signed commit containing the two final owners and the seven explicit deletions.

### Task 7: Remove residual edge-case buckets and update documentation

**Files:**

- Delete: `chaos_advanced_test.go`
- Delete: `chaos_test.go`
- Delete: `edge_cases_test.go`
- Delete: `pattern_boundary_test.go`
- Delete: `pattern_edge_case_test.go`
- Delete: `validation_test.go`
- Modify: `release_workflow_test.go`
- Modify: `CLAUDE.md:189-200`

**Interfaces:**

- Consumes: the contract owners completed in Tasks 2-6 and the real
  `.github/workflows/release.yml` artefact.
- Produces: the final responsibility-based suite layout and matching contributor documentation.

- [ ] **Step 1: Check every residual valuable contract has an owner**

Before deletion, verify these mappings:

| Residual behaviour | Final owner |
|---|---|
| Unicode/overlap matching | `pattern_test.go` |
| Recursive globs and symlink policy | `file_selection_test.go` |
| File permissions | `module_update_test.go` |
| Missing-version warnings | `module_update_test.go` |
| YAML empty and invalid node handling | `config_test.go` |
| Real parse/read/write errors | responsibility-specific test files |
| Provider attribute preservation | `provider_update_test.go` |
| Cross-file continuation | `integration_test.go` |

Do not retain arbitrary long strings, nested directories, 10,000-entry ignore lists, wall-clock
thresholds, parser pass-through cases, success-or-error assertions, or standard-library glob tests.

- [ ] **Step 2: Delete the six residual bucket files**

Delete exactly:

```text
chaos_advanced_test.go
chaos_test.go
edge_cases_test.go
pattern_boundary_test.go
pattern_edge_case_test.go
validation_test.go
```

- [ ] **Step 3: Make the release workflow test inspect only the real artefact**

Keep the existing YAML read/unmarshal and generator-prefix assertion. Delete the synthetic regex
case table. Validate only the actual workflow reference:

```go
version := strings.TrimPrefix(reference, generator)
exactSemanticVersion := regexp.MustCompile(`^v[1-9]\d*\.\d+\.\d+(?:-rc\.\d+)?$`)
if !exactSemanticVersion.MatchString(version) {
	t.Fatalf("SLSA generator version = %q, want an exact semantic-version tag", version)
}
```

- [ ] **Step 4: Update contributor test-layout documentation**

Replace the historical file list in `CLAUDE.md` with the final file map. State these rules in the
testing section:

- one strongest owner per observable contract;
- expected diagnostics are captured and asserted;
- test output must contain no leaked application output;
- total coverage must remain at least 90%; and
- an implementation or TDD phase is followed by the separate `test-cleanup` pass.

Do not expand user-facing README documentation; this is contributor guidance.

- [ ] **Step 5: Format and run the complete suite with verbose-output inspection**

Run:

```bash
gofmt -w release_workflow_test.go
go test -count=1 ./...
go test -count=1 -v ./...
go test -count=1 -race -coverprofile=/tmp/tf-version-bump-task7.cover -covermode=atomic ./...
go tool cover -func=/tmp/tf-version-bump-task7.cover
rg --stats --count-matches '^func Test' -g '*_test.go'
rg --stats --count-matches '^' -g '*_test.go'
```

Expected: tests pass; verbose output contains only Go test runner progress plus package status; no
warning, update, summary, or expected-error line leaks; coverage is at least 90.0%; and both counts
are materially below the 219-test/11,631-line baseline.

- [ ] **Step 6: Review and commit the residual cleanup**

Run:

```bash
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump diff --check
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump status --short
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump add CLAUDE.md release_workflow_test.go chaos_advanced_test.go chaos_test.go edge_cases_test.go pattern_boundary_test.go pattern_edge_case_test.go validation_test.go
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump commit -m "test: remove low-value edge-case buckets"
```

Expected: a signed commit with documentation, the focused workflow assertion, and the six explicit
deletions.

### Task 8: Independent test-cleanup review and final verification

**Files:**

- Review: every final `*_test.go` file
- Modify: test files only when the independent reviewer classifies a retained test as Slop

**Interfaces:**

- Consumes: the completed implementation diff and the `test-cleanup` skill.
- Produces: an independent Keep/Slop/Suspect classification, a subtractive cleanup commit when
  warranted, final metrics, and a fully verified branch.

- [ ] **Step 1: Confirm the implementation is committed before review**

Run:

```bash
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump status --short --branch
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump log --oneline origin/main..HEAD
```

Expected: the worktree is clean and Tasks 1-7 are committed. Do not stash unfinished work for the
reviewer; finish or commit the implementation first.

- [ ] **Step 2: Dispatch the mandatory independent cleanup reviewer**

The orchestrating agent—not the implementer of Task 7—must invoke the `test-cleanup` skill in a
fresh subagent on the existing feature branch. Give it the specification, this plan, and the full
branch diff. Require it to classify every added or modified test as Keep, Slop, or Suspect and
apply the skill's decision procedure to every Suspect test.

The subagent may only delete Slop or perform a consolidation that reduces test code. It must not
write tests, rewrite assertions, rename tests, change helpers, touch `CLAUDE.md` or production
files, create a sub-branch, or push. The project instructions override the skill's generic Git
examples: it must use `/Users/dan/.codex/bin/codex-git` and its cleanup commit must be signed.

Ask it to pay particular attention to these questions while applying the skill's taxonomy:

```text
1. Does every retained test protect a distinct observable contract?
2. Are any assertions still negative-only, non-empty-only, no-panic-only, or success-or-error?
3. Are expected diagnostics fully captured and asserted?
4. Do integration tests duplicate narrower tests?
5. Did the migration lose a contract named in the specification?
6. Can any table row or helper be removed without losing behaviour coverage?
```

It must run focused tests, the full suite, and coverage after removal. If coverage on a changed file
drops by more than five percentage points, it must re-examine the removal. Total coverage must also
remain at least 90.0%. It commits justified deletions directly to the feature branch with a body
listing the count removed per slop category, then reports:

```text
Test cleanup complete on branch <branch>:
- Tests touched on branch: <N>
- Kept: <N>
- Removed as slop: <N>  (<breakdown by category>)
- Flagged for follow-up: <N>  (<brief list>)
- Coverage delta: <before>% → <after>%
- Suite status: pass | fail
```

If it finds no Slop, it reports zero removals and does not create an empty commit.

- [ ] **Step 3: Inspect the independent result**

Wait for the subagent, inspect its diff and signed commit, and verify it touched test files only.
If it flags a real production or assertion problem, do not let the cleanup reviewer fix it. Stop
the final handoff, dispatch a separate implementer for one focused TDD change, commit that change,
and then repeat Task 8 with another fresh cleanup reviewer.

- [ ] **Step 4: Run final formatting and repository verification**

Run:

```bash
gofmt -w test_helpers_test.go pattern_test.go file_selection_test.go config_test.go config_schema_test.go module_update_test.go terraform_version_test.go provider_update_test.go command_test.go integration_test.go release_workflow_test.go
go test -count=1 ./...
go test -count=1 -v ./...
go test -count=1 -race -coverprofile=/tmp/tf-version-bump-final.cover -covermode=atomic ./...
go tool cover -func=/tmp/tf-version-bump-final.cover
go build ./...
golangci-lint run --timeout=5m
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump diff --check
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump diff origin/main -- main.go config.go
```

Expected: all commands pass; verbose test output is pristine; total coverage is at least 90.0%;
and the production-file diff is empty. If a separate TDD defect commit was required, replace the
last expectation with a review that the production diff contains only that committed fix.

- [ ] **Step 5: Record final metrics and inspect the complete branch**

Run:

```bash
rg --stats --count-matches '^func Test' -g '*_test.go'
rg --stats --count-matches '^' -g '*_test.go'
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump diff --stat origin/main...HEAD
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump log --show-signature --oneline origin/main..HEAD
/Users/dan/.codex/bin/codex-git -C /Users/dan/Code/tf-version-bump status --short --branch
```

Compare final file count, top-level test count, test lines, non-race duration, race duration, and
coverage with the Task 1 baseline. Prepare the handoff list of retained contract families from the
specification.

Verify every branch commit's signature and make sure the worktree is clean before handing the
branch back to Dan.
