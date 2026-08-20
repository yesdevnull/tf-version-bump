# Contract-centred test-suite cleanup design

## Purpose

Aggressively reduce the Go test suite while preserving the behaviours that make
`tf-version-bump` safe to use. The resulting suite will be organised by production
responsibility, exercise real logic, fail for the regressions it claims to prevent, and remain
straightforward to extend.

This is a test-only restructuring and quality pass. Coverage is a safety floor rather than a
reason to retain duplicate or non-behavioural tests.

## Current state

The review baseline contains 219 top-level Go tests and 11,631 lines of test code for 1,311 lines
of production Go code, a test-to-production ratio of approximately 8.9:1. The full race-enabled
suite passes in approximately 1.74 seconds with 98.8% statement coverage.

The high headline coverage masks substantial low-value volume:

- Excluding the top-level tests in `chaos_test.go`, `chaos_advanced_test.go`,
  `edge_cases_test.go`, `validation_test.go`, `pattern_boundary_test.go`, and
  `pattern_edge_case_test.go` leaves 98.4% coverage. Those files account for 54 top-level tests
  and 2,373 lines while contributing approximately 0.4 percentage points of coverage.
- Several tests reproduce production conditionals or test standard-library behaviour without
  calling the production path.
- Several assertions can pass after deletion or incorrect mutation because they check only that
  the old value is absent, an error is non-empty, or no panic occurred.
- Some failure-path tests recover any panic rather than asserting the expected exit type and code.
- The ordinary verbose suite emits application diagnostics from expected-error tests instead of
  capturing and asserting them.
- Pattern, configuration, ignore, dry-run, formatting, and integration scenarios are repeated
  across historical `chaos`, `coverage`, `validation`, and `edge_cases` buckets.

An aggressive cleanup is therefore warranted. Dan has approved deletion and reorganisation of
entire test files, provided total statement coverage remains at least 90%.

## Goals

- Retain one strongest test for each distinct observable contract.
- Repair or replace tests that currently pass for the wrong reason.
- Consolidate genuine variants of one behaviour into focused tables.
- Delete duplicate, speculative, library-only, permissive, or implementation-detail tests.
- Organise test files by production responsibility rather than historical motivation.
- Exercise real production logic and real filesystem data instead of mocked domain behaviour.
- Keep expected error output captured and asserted so test output is pristine.
- Keep the complete suite deterministic, race-clean, and above 90% total statement coverage.
- Report before-and-after file count, top-level test count, test lines, runtime, and coverage.
- Update the repository's test-layout documentation to match the final structure.

## Non-goals

- Change user-visible production behaviour as part of a cleanup commit.
- Maximise coverage or preserve the current 98.8% figure.
- Introduce a test framework, assertion library, snapshot system, golden-file framework, or
  mutation-testing dependency.
- Retain tests solely because they add statement coverage.
- Add broad fuzz, load, benchmark, platform-emulation, or performance testing.
- Preserve the current test filenames or historical test taxonomy.
- Reformat or refactor production code to make the tests easier to organise.

If a strengthened test exposes a production defect, that defect will be handled in a separate,
focused TDD commit before the cleanup continues. It will not be concealed inside a test-migration
commit.

## Test value model

A retained test must protect a distinct externally observable behaviour or a necessary error
boundary through production code. It must identify its action and expected result clearly enough
that a future failure points to a specific broken contract.

A test is retained only when all of the following are true:

1. It calls the production behaviour it claims to cover.
2. It protects a contract not already owned more strongly elsewhere.
3. Its assertion verifies the complete relevant state, diagnostic, error, or exit code.
4. It would fail for the regression described by its name and fixture.
5. Its input represents a documented behaviour or a credible failure mode.
6. It is deterministic and independent of uncontrolled wall-clock timing or global state.

Tests are removed when they:

- duplicate a stronger unit, integration, or command-boundary test;
- reproduce production conditionals rather than invoking the production function;
- test Go or third-party library behaviour that the application does not wrap into a distinct
  contract;
- merely prove that printing does not panic;
- allow contradictory outcomes such as success or failure;
- inspect incidental formatting or implementation details without protecting a documented
  preservation contract;
- add arbitrary length, depth, volume, character, or timing dimensions without a credible failure
  mechanism;
- exercise mocked domain behaviour instead of real logic;
- assert only that an old value disappeared, that an error is non-empty, or that any panic
  occurred; or
- test a test-local regex, parser, or conditional rather than the repository artefact.

Coverage does not override this value model. If removal crosses the coverage floor, the smallest
meaningful behavioural test will be restored or added; a coverage-only probe will not be created.

## Target test architecture

The suite will be split by production responsibility:

| File | Responsibility |
|---|---|
| `module_update_test.go` | Module source matching, version filters, force-add, local-source exclusion, HCL preservation, and file permissions |
| `terraform_version_test.go` | `required_version` updates, dry-run behaviour, parsing, and filesystem failures |
| `provider_update_test.go` | Provider syntax variants, preservation of unrelated attributes, filtering, and failure paths |
| `pattern_test.go` | One canonical table for exact, prefix, suffix, ordered-middle, overlap, zero-width, and Unicode matching |
| `file_selection_test.go` | Recursive globs, vendored-directory exclusions, hidden files, sorting, symlinks, and files-only selection |
| `config_test.go` | YAML decoding, sanitisation, and configuration validation |
| `config_schema_test.go` | JSON Schema contracts that are not duplicates of Go decoding tests |
| `command_test.go` | Flags, operation-mode validation, summaries, diagnostics, and exit behaviour |
| `integration_test.go` | Cross-operation behaviour that cannot be proved at a narrower boundary, especially continuation and aggregate failure reporting |
| `test_helpers_test.go` | Minimal shared filesystem, output-capture, and process-exit helpers |

Repository workflow tests remain separate only where they protect a real workflow contract that
is not already enforced by the configured workflow linters. A workflow test must inspect the real
workflow artefact; synthetic rows that merely exercise a regex declared inside the test are not
sufficient.

The names above are ownership boundaries, not an instruction to produce empty or token files. A
file is omitted if no distinct contract remains in that category after consolidation.

### Structural rules

- One strongest test owns each contract.
- Integration tests do not repeat helper-level case matrices.
- Table-driven tests contain variants of one behaviour, not unrelated scenarios sharing setup.
- Each table row names the behavioural distinction and declares exact expected state.
- Fatal-path tests recover only around the expected call and assert the recovered exit type and
  code. Assertions after the expected panic remain reachable.
- Tests that change flags, writers, log destinations, exit hooks, parsers, or other package globals
  register restoration with `t.Cleanup` immediately.
- Shared helpers remove mechanical setup only. They do not hide the production action or expected
  result.
- Process-boundary substitution is permitted for unavoidable exit interception, but module,
  provider, Terraform, configuration, and filesystem behaviour remains real.
- Temporary files use `t.TempDir()` and retain real read, parse, update, and write paths.

## Contracts to preserve

The final suite will retain one strong owner for each of the following contract families.

### Module updates

- Exact source selection and non-matching-source preservation.
- Local source exclusion.
- Missing-version behaviour with and without force-add.
- `ignore_versions` precedence over `from` filters.
- Module-name ignore matching.
- Dry-run non-mutation.
- Comment, expression, unrelated-attribute, and supported formatting preservation.
- Original file permission preservation.
- Real read, parse, and write failures.

### Terraform and provider updates

- `required_version` selection and update.
- Supported provider block and attribute syntax.
- Preservation of provider attributes such as `configuration_aliases`.
- Non-target provider preservation.
- Dry-run non-mutation.
- Real read, parse, and write failures.

### Matching and file selection

- Exact, prefix, suffix, contains, ordered-middle, repeated-part, overlap, zero-width, and Unicode
  pattern behaviour.
- Recursive `**` selection at zero and multiple directory depths.
- Deterministic lexical ordering.
- `.git` and `.terraform` exclusion.
- Hidden-file behaviour, including explicit hidden patterns.
- File-only results and the defined file and directory symlink policy.
- Invalid application-level pattern handling through the production selection path.

### Configuration

- Strict YAML decoding and unknown-field rejection.
- Required field validation and whitespace sanitisation.
- `from` decoding for one string and a sequence of strings.
- Empty and invalid `from` node kinds.
- Module, provider, Terraform, and ignore configuration fields.
- Schema acceptance and rejection where the schema adds a distinct consumer contract.

### Command behaviour

- Mutually exclusive operation-mode validation.
- Required flag validation.
- Text and Markdown user-visible output where formats genuinely differ.
- Exact summaries and expected diagnostics.
- Continued processing after a selected file fails.
- Non-zero aggregate command status after one or more file failures.
- Successful status when all selected operations succeed, including the valid no-update case.
- Combined configuration execution where later operations continue after an earlier failure.

## Known removals and repairs

The implementation plan will identify exact test names, but the review has already established the
following required outcomes:

- Replace `TestValidateOperationModes` in `cli_functions_test.go`, which does not call
  `validateOperationModes`, with direct production-path cases or delete it if the command tests
  already own those cases.
- Delete the `TestLoadModuleUpdatesErrorCases` condition reimplementation in
  `main_integration_test.go`.
- Delete print-summary tests that only assert that no panic occurred.
- Delete output-format tests that inspect only file content on a path where output format cannot
  affect file mutation.
- Repair the dry-run-message test so it captures and asserts the message, or remove it if a stronger
  command test owns the contract.
- Repair the missing-version warning test so it reaches the intended registry-module branch and
  asserts the captured diagnostic.
- Delete tests of `filepath.Glob` and the incorrect assumptions about `**`; production uses
  `doublestar` and is covered through `findMatchingFiles`.
- Delete the invalid-version test that permits success, failure, or `(false, nil)`.
- Replace synthetic release-workflow regex rows with a real artefact assertion if that contract is
  not already enforced by workflow linting.
- Repair fatal-path tests so they reject unrelated panics and restore global logging state.
- Replace non-empty error assertions with exact errors or stable, meaningful error components.
- Replace negative-only file assertions with the exact preserved or updated content.
- Consolidate the repeated pattern matrices into one canonical behavioural table.
- Consolidate configuration `from`, ignore-module, ignore-version, formatting, dry-run, and
  end-to-end matrices around the strongest existing owner.
- Remove arbitrary long strings, deep directories, 10,000-entry ignore lists, wall-clock
  thresholds, and parser pass-through permutations that do not protect application logic.

The historical `chaos*_test.go`, `coverage_test.go`, `main_coverage_test.go`,
`edge_cases_test.go`, `validation_test.go`, `pattern_boundary_test.go`, and
`pattern_edge_case_test.go` files will disappear after their valuable cases are migrated. The
current broad CLI and integration files will likewise be dissolved into the responsibility-based
layout when their remaining cases have owners.

## Migration strategy

The cleanup will be implemented in behaviour-sized batches rather than as one opaque deletion.

1. Record the fresh baseline: files, top-level tests, test lines, runtime, race result, statement
   coverage, and ordinary test output.
2. Build a working contract inventory from the production responsibilities and the approved
   contract families above.
3. Select the strongest existing owner for each contract.
4. Move or rewrite the selected tests into the target files, strengthening false-pass assertions
   as they are encountered.
5. Demonstrate that each repaired test is sensitive to the regression it names. Where the current
   implementation is correct, use a temporary, uncommitted controlled mutation of the exercised
   behaviour or fixture, run the focused test to observe failure, then restore the mutation before
   continuing.
6. Remove superseded tests once the focused replacement passes.
7. Run concern-focused tests, the complete suite, race detection, and coverage before each signed
   cleanup commit.
8. Delete emptied historical files and update `CLAUDE.md` to describe the final layout.
9. Run the dedicated `test-cleanup` skill in a separate subagent after the implementation phase,
   apply only justified findings, and repeat complete verification.
10. Report the final metrics and the retained contract families.

Each commit must leave the suite passing, race-clean, and at or above 90% coverage. Production
changes, if required, receive their own preceding TDD commit so test-only commits remain auditable.

## Verification

The implementation will use the repository's existing toolchain. At minimum, final verification
will run:

```bash
go test ./...
go test -race -coverprofile=coverage.out -covermode=atomic ./...
go tool cover -func=coverage.out
go build ./...
golangci-lint run --timeout=5m
```

Passing output means:

- `go test ./...` contains only the Go runner's package status, with no leaked application logs or
  expected-error diagnostics;
- race detection reports no races;
- the coverage total is at least 90.0%;
- the build and configured linter pass; and
- the working tree contains no generated binary or coverage artefact intended for commit.

Focused test commands will be specified in the implementation plan for every migration batch. The
final diff will be checked for accidental production changes, silently weakened assertions,
unrestored global state, and duplicated contract ownership.

## Risks and controls

### Removing distinct behaviour accidentally

The contract inventory and behaviour-sized commits make each deletion reviewable. Coverage is a
secondary tripwire; it is not the sole proof of retained behaviour.

### Preserving weak tests under new names

Every migrated test must meet the value model, including full-state assertions and demonstrated
regression sensitivity. Mechanical movement alone does not justify retention.

### Over-consolidating tables

Tables are limited to one behaviour dimension. Scenarios with different actions or failure
boundaries remain separate tests even when their setup is similar.

### Hiding failures through shared helpers

Helpers are restricted to mechanical setup and cleanup. Production calls and expected results stay
visible in each test.

### Coverage-driven backsliding

The 90% threshold is enforced after each batch. If coverage falls below it, only a meaningful
behavioural test may be restored or added.

### Discovering production defects

Cleanup pauses at the affected boundary. The defect receives a focused failing test and the
smallest production fix in a separate commit, followed by the normal cleanup verification.

## Completion criteria

The cleanup is complete when:

- the suite follows the responsibility-based architecture;
- every retained test satisfies the value model;
- the identified false-pass tests are repaired or removed;
- the historical bucket files and duplicate matrices are gone;
- the complete suite is deterministic, pristine, race-clean, and at least 90% covered;
- the build and linter pass;
- `CLAUDE.md` describes the new layout;
- a separate `test-cleanup` review has been completed and resolved;
- before-and-after metrics and retained contract families are reported; and
- all commits created by Codex are signed.
