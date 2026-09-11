# State-branch version report

Dan approved this design in chat on 11 September 2026. A report-only workflow
checks every discovered state branch against its policy's control configuration
without changing anything. It produces two reports from the same collected data:

- a **legacy report** that reproduces an existing report's format exactly, with
  its known defects kept deliberately, and
- an **improved report** that runs alongside it and fixes those defects.

## Delivery

Two pull requests, in order:

1. The `-audit-file` CLI flag, with Go tests and documentation, followed by a
   release.
2. The report workflow example, pinned to that release's version and archive
   SHA-256. Neither exists before the release, so this cannot merge first. Its
   local tests build the CLI from source. Its final commit moves every example
   pin, the update callers included, to the new release with
   `scripts/update-actions-release-pin.sh`, which gains the report workflow: the
   repository keeps all example pins on one release.

## CLI: `-audit-file`

```text
tf-version-bump -pattern <glob> -config <file> -audit-file <path>
```

- Config mode only. It never writes Terraform files and cannot be combined with
  `-dry-run`, `-check`, `-report-file` or `-force-add`.
- The audit is written only after every selected file parses. An unparseable
  file exits 1 and writes nothing. The destination is validated as
  `-report-file` validates its destination: never a directory or one of the
  selected inputs, and written through a temporary file.
- A written audit exits 0 whatever it contains. After the usual
  `Found <n> file(s)` line, success prints `✓ Wrote audit to '<path>'`.

Schema version 1:

```json
{
  "schema_version": 1,
  "terraform": [
    {"file": "versions.tf", "actual": ">= 1.10", "expected": ">= 1.10", "matches": true}
  ],
  "providers": [
    {"file": "versions.tf", "name": "aws", "actual": "~> 5.0", "expected": "~> 6.0", "matches": false}
  ],
  "modules": [
    {"file": "main.tf", "name": "vpc", "source": "terraform-aws-modules/vpc/aws",
     "actual": "4.2.0", "expected": "5.0.0", "matches": false, "skip": null},
    {"file": "main.tf", "name": "legacy_vpc", "source": "terraform-aws-modules/vpc/aws",
     "actual": "3.19.0", "expected": "5.0.0", "matches": false,
     "skip": {"filter": "ignore_modules", "values": ["legacy_*"]}}
  ]
}
```

- `terraform`: one entry per `terraform` block when the config sets
  `terraform_version`. `actual` is null when the block has no
  `required_version`.
- `providers`: one entry per local name in `required_providers`, in block or
  object syntax, that matches a config provider. `actual` is null when the entry
  has no `version`.
- `modules`: one entry per module block per config entry with an equal
  `source`. A block matched by two config entries appears twice. `skip` records
  the first filter that would stop the updater, in its precedence order:
  `local_source` (with empty `values`), `ignore_modules`, `ignore_versions`,
  `from`. A block without a `version` is never skipped by a version filter; its
  `actual` is null.
- `actual` is the value as written, quotes trimmed; a non-literal expression is
  reported as its source text. `matches` uses the updater's exact comparison
  (`expressionHasStringValue`), so no consumer reimplements it. A matching value
  is never changed by an update. A value can also stay unchanged without
  matching: a skipped module, or an object-syntax provider without `version`,
  which the updater does not add.
- Config entries that no selected file uses produce no entries.
- Entries follow the glob's file order, then block order within a file, then
  config order.

The code lives in new `audit.go` and `audit_test.go` files in the flat `main`
package. `docs/USAGE.md` gains the flag, its command form and a section on the
schema; CLAUDE.md's layout, testing and output sections name the new files and
the contract. Go tests own the schema and semantics; `command_test.go` owns flag
conflicts, exit status and output. Coverage stays at or above 90%.

## Workflow

`examples/github-actions/.github/workflows/tf-version-bump-report.yml`:

- Triggers: `workflow_dispatch` and a weekly schedule on Monday at 06:17
  `Australia/Melbourne`, after both policies' scheduled update runs.
- Runs only from the default branch, which discovery already requires.
- `contents: read` only. No Terraform, registry token or `TERRAFORM_ENV`: the
  report only parses HCL.
- One `report` job with `fail-fast: false` and a matrix entry per policy
  carrying its ID, config path, branch prefixes and Terraform directories. The
  prefixes repeat the update callers' lists; that duplication is the accepted
  cost of a standalone workflow, and a harness test fails if they drift apart.
- Discovery runs unchanged, so a policy whose prefixes match no branch, or more
  than 256, fails its report job at discovery without a summary or artefact,
  exactly as the same prefixes fail the update workflow. The job's timeout is 30
  minutes.

Steps:

1. Check out the default branch into `control` with persisted read credentials,
   as the update workflow's discovery job does.
2. Discover state branches: run `discover-state-branches.sh` unchanged and write
   its JSON to `$RUNNER_TEMP/branches.json`.
3. `collect`: download the pinned CLI and verify its SHA-256, then for each
   discovered branch fetch its commit by ID into a detached worktree and, for
   each root, run `-audit-file` and record whether `main.tf` and `providers.tf`
   exist. Writes `records.json`.
4. `report`: the improved report's summary section and `version-report.csv`.
5. `legacy`: runs whenever `collect` succeeded, even if `report` failed. Writes
   the legacy summary section and `legacy-report.csv`.
6. Upload both CSVs and `records.json` as one artefact per policy, retained for
   seven days like the update artefacts.

## Script

`.github/scripts/report-state-branches.sh` with `collect`, `report` and
`legacy` subcommands; inputs use a `REPORT_` prefix. The download and checksum
check repeat about six lines of `process-state-branch.sh`; its copy is bound to
the processing logs, error stage and Terraform version check, and sharing it
would restructure the update path.

Roots are relative, without `..`, and free of glob characters (`*?[]{}\`),
because each root becomes part of a `-pattern`. Within each branch a root must
resolve inside the checkout and not duplicate another root. A root whose `*.tf`
files include a symlink is a branch error, matching processing's refusal of
symlinked Terraform inputs.

`records.json`:

```json
{
  "policy": "nonproduction",
  "roots": ["."],
  "branches": [
    {"branch": "state/staging/example-thing", "commit": "<oid>", "error": null,
     "roots": [
       {"root": ".", "exists": true,
        "files": {"main.tf": true, "providers.tf": false},
        "audit": {"schema_version": 1, "terraform": [], "providers": [], "modules": []}}
     ]}
  ]
}
```

A root with no `*.tf` files has an empty audit. A missing root has
`exists: false` and no audit. A failed fetch, rejected root or CLI failure sets
the branch's `error` to a one-line message and records no roots for it;
collection continues with the next branch and still exits 0.

## Legacy report

Reproduces the existing report, defects included:

- Summary: a `## Legacy version report (<policy>)` heading, then a table headed
  `| Result | Test | Comment | State Branch |` with FAIL rows only, or
  `All <n> checks passed.` when there are none. Its cells are escaped for
  Markdown and HTML, as the improved report's are. This is the one deliberate
  difference from the existing report: it changes the raw Markdown but not the
  rendered table, and stops a `|` or newline in a value breaking a row. The CSV
  is not escaped.
- CSV: every row, PASS and FAIL, under the header
  `"Result","Test","Comment","State Branch"`; jq's `@csv` quotes every field,
  the header's included.
- Per branch: `main.tf`, then `providers.tf`, then one row per module entry.
- File checks: Test `main.tf` or `providers.tf`; Comment `found: main.tf` or
  `not found: main.tf`.
- Modules only. Test is the module source. Filters (`skip`) are ignored, and
  blocks sharing a source produce identical rows.
- Comment `version matched: act. <actual> exp. <expected>` when `matches` is
  true, otherwise `version mismatch: act. <actual> exp. <expected>`, with
  `none` for a null actual.
- More than one configured root: exits 1 with `legacy report supports one
  Terraform root only` and writes neither summary nor CSV.
- A branch with an error is omitted; the improved report fails the job for it.

## Improved report

| Legacy defect | Improved behaviour |
| --- | --- |
| Blocks sharing a source give identical rows | `block` and `file` identify each block |
| Test mixes sources and file names | Separate `kind` and `subject` |
| Versions packed into a free-text comment | Separate `actual`, `expected` and `detail` |
| Modules only | Terraform `required_version` and providers too |
| Filters ignored, so deliberate pins fail | `SKIP`, naming the filter |
| One root only | `file` is relative to the repository root |
| FAIL-only summary gives no scale | Counts per policy and per branch |
| `none` could be read as a version | Empty `actual`; the reason is in `detail` |
| Unreadable branches vanish | `ERROR` row, and the job fails |

- CSV: every row under the header
  `"status","branch","kind","subject","block","file","actual","expected","detail"`,
  every field quoted by `@csv`.
- Both CSVs keep values exactly as written. A value beginning with `=`, `+`,
  `-` or `@` may be evaluated as a formula by a spreadsheet that opens the file
  directly; an exact pin such as `= 5.0.0` is valid Terraform. The README says
  to import the CSVs as text instead; values are not altered to prevent it.
- Statuses: `PASS`, `FAIL`, `SKIP`, `ERROR`. A value that matches is `PASS`
  even when a filter would skip it; otherwise a skipped value is `SKIP`, and
  anything else is `FAIL`. Kinds: `file`, `root`, `terraform`, `provider`,
  `module`, `branch`.
- Subjects: the file name for `file`, the root path for `root`,
  `required_version` for `terraform`, the provider name for `provider`, the
  source for `module`, empty for `branch`.
- Details: `found` or `not found` for files and roots; `version differs`;
  `no required_version`; `no version attribute`; `skipped by <filter>
  (<values>)` or `skipped: local module source`; the collection message for
  `ERROR`.
- Order: branches in discovery order; within a branch each root's root check,
  file checks, Terraform, providers, then modules.
- Summary: a `## Version report (<policy>)` heading and a
  `Checked <n> branch(es): …` counts line; a `### <branch>` table of its FAIL,
  SKIP and ERROR rows for each branch that has any; and a
  `Branches where every check passed:` list. Cell values are escaped for
  Markdown and HTML, and newlines in a value become `<br>` so a multi-line
  expression stays on its row.
- `report` exits 1 after writing everything when any branch has an error.
  Mismatches alone never fail the job.

## Validation

TDD throughout, with a separate test-cleanup pass and independent review after
each pull request. `examples/github-actions/report-test.sh` runs from the
existing harness, builds the CLI from source, and uses local Git remotes with
real fixture branches as the discovery tests do. It checks the workflow wiring
with yq, runs `collect` against fixtures, and asserts both CSVs and both
summaries exactly, including the multi-root legacy failure, a missing root, a
symlinked input and an unparseable branch. `make actionlint` and
`make shellcheck` cover the new workflow and script. The example README gains a
section describing the report.

## Out of scope

Providers, Terraform versions and filters in the legacy report; PRs, issues or
any other GitHub writes; running Terraform; cutting the release, which is Dan's
action.

## Approval and scope

Chat approvals: `-audit-file` as the collection mechanism and its name; a
standalone workflow; both reports as steps in one job per policy; the legacy
report's format, module-only scope, per-block rows, ignored filters, `none`,
single root and non-failing mismatches; the legacy step failing on several
roots; `@csv` quoting for both CSVs; reporting readable branches before failing
on unreadable ones; one plan in two parts, with the release between them. After
the adversarial review: discovery's no-match and 256-branch limits are kept and
documented, with a 30-minute job timeout; the legacy summary keeps its cell
escaping as a documented difference; the CSVs' spreadsheet formula risk is
documented rather than prevented. This spec does not authorise a push, PR,
merge or release.
