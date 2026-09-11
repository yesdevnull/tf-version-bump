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
   local tests build the CLI from source.

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
- A written audit exits 0 whatever it contains. Success prints one line,
  `✓ Wrote audit to <path>`.

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
  `from`. A block without a `version` is never skipped; its `actual` is null.
- `actual` is the value as written, quotes trimmed; a non-literal expression is
  reported as its source text. `matches` uses the updater's exact comparison
  (`expressionHasStringValue`), so no consumer reimplements it.
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
  cost of a standalone workflow.

Steps:

1. Check out the default branch into `control` with persisted read credentials,
   as the update workflow's discovery job does.
2. `collect`: run `discover-state-branches.sh` unchanged, download the pinned
   CLI and verify its SHA-256, then for each branch fetch its discovered commit
   into a detached worktree and, for each root, run `-audit-file` and record
   whether `main.tf` and `providers.tf` exist. Writes `records.json`.
3. `report`: the improved report's summary section and `version-report.csv`.
4. `legacy`: runs whenever `collect` succeeded, even if `report` failed. Writes
   the legacy summary section and `legacy-report.csv`.
5. Upload both CSVs and `records.json` as one artefact per policy, retained for
   seven days like the update artefacts.

## Script

`.github/scripts/report-state-branches.sh` with `collect`, `report` and
`legacy` subcommands; inputs use a `REPORT_` prefix. The download and checksum
check repeat about six lines of `process-state-branch.sh`; its copy is bound to
the processing logs, error stage and Terraform version check, and sharing it
would restructure the update path.

Roots are validated as processing validates them: relative, inside the
checkout, not duplicated. A root whose `*.tf` files include a symlink is a
branch error, matching processing's refusal of symlinked Terraform inputs.

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
  `All <n> checks passed.` when there are none.
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
- Statuses: `PASS`, `FAIL`, `SKIP`, `ERROR`. Kinds: `file`, `root`,
  `terraform`, `provider`, `module`, `branch`.
- Subjects: the file name for `file`, the root path for `root`,
  `required_version` for `terraform`, the provider name for `provider`, the
  source for `module`, empty for `branch`.
- Details: `found` or `not found` for files and roots; `version differs`;
  `no required_version`; `no version attribute`; `skipped by <filter>
  (<values>)` or `skipped: local module source`; the collection message for
  `ERROR`.
- Order: branches in discovery order; within a branch each root's file checks,
  root check, Terraform, providers, then modules.
- Summary: a `## Version report (<policy>)` heading and a counts line; a
  `### <branch>` table of its FAIL, SKIP and ERROR rows for each branch that has
  any; and a list of branches whose checks all passed. Cell values are escaped
  for Markdown and HTML.
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
on unreadable ones. This spec does not authorise a push, PR, merge or release.
