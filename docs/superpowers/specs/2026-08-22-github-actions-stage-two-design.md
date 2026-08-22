# GitHub Actions staged update and formatting design

## Status

Approved in conversation on 2026-08-22. This design extends the copyable GitHub Actions example
after `tf-version-bump v1.0.0-rc.9` added machine-readable module and provider update counts.

## Goal

Extend the branch automation example so it can optionally run `terraform fmt -recursive` after
`tf-version-bump` and `terraform init -upgrade`, publish update/init and formatting changes as two
separate commits, and include exact update counts plus stage-specific changed files in the managed
pull request.

## Requirements

- Install and verify the released `tf-version-bump v1.0.0-rc.9` Linux x86-64 archive.
- Pass `-report-file` for every configured Terraform root and aggregate the exact module and
  provider block counts across the branch.
- Run `tf-version-bump` and `terraform init -upgrade` for every path listed in
  `terraform_directories`, preserving the existing sequential root processing and deadline.
- Add an optional reusable-workflow boolean input named `terraform_fmt`, defaulting to `false`.
- When `terraform_fmt` is enabled, run `terraform fmt -recursive` from every configured Terraform
  root only when the complete update/init stage produced a Git diff.
- Permit formatting to change nested `.tf` files beneath a configured root.
- Publish update/init and formatting results as independently hashed and verified patches.
- Create a second commit only when formatting changes files.
- Show exact module/provider block counts and separate update/init and formatting file lists in the
  pull-request body.
- Treat a formatting failure as a bounded branch failure handled by the existing marked-issue
  lifecycle.
- Preserve the existing preparation, validation, verification, and privileged publication job
  boundaries.

## Non-goals

- Extend the `tf-version-bump` report schema beyond its existing module/provider counts.
- Report exact Terraform `required_version` update counts.
- Interpret `.terraform.lock.hcl` changes beyond listing them in the update/init file section.
- Combine workflow jobs or expose repository write credentials to Terraform, providers, or modules.
- Add backward compatibility for artefacts produced by an older control revision. Partial job
  reruns remain unsupported, and every artefact remains bound to the run attempt and control OID.
- Automatically merge or approve generated pull requests.

## Job and trust boundaries

The existing four-job flow remains intact:

1. **Prepare**, with `contents: read`, runs the released updater and `terraform init -upgrade`,
   optionally formats the resulting candidate, and uploads immutable staged patches and a manifest.
2. **Validate**, with `contents: read`, applies both patches in order to a fresh exact-base checkout,
   runs non-upgrade initialisation and `terraform validate`, and uploads a bound outcome.
3. **Verify**, with `contents: read`, checks preparation and validation identities, patch hashes,
   stage-specific paths, modes, file digests, and the final candidate state in another fresh
   exact-base checkout. It does not rerun the updater, Terraform initialisation, formatting, or
   validation.
4. **Publish**, with repository write permissions, consumes only the verified result, constructs
   the deterministic commit sequence, updates the managed branch under its exact lease, and
   creates or refreshes the marked pull request.

The privileged publication job never executes Terraform or downloaded provider/module code.

## Workflow inputs and release pin

The reusable workflow adds:

```yaml
terraform_fmt:
  type: boolean
  default: false
```

It passes this value to preparation as `PROCESS_TERRAFORM_FMT`. Both supplied callers explicitly
set `terraform_fmt: true` so the copyable example demonstrates the feature while consumers can
leave the reusable-workflow default disabled.

The callers, config-validation workflow, focused harness, and documentation move from
`v1.0.0-rc.8` to:

```text
tag: v1.0.0-rc.9
Linux x86-64 archive SHA-256: 38428a229a77671fd192fd6a18f5d1f9c404b5557124883f04e6a8bec154b1d2
```

## Preparation sequence

Preparation continues to use trusted temporary directories outside the target checkout for the
downloaded updater, Terraform data, reports, indexes, logs, and bundle staging.

For every configured root, in declared order:

1. Run the released updater from that root with:

   ```text
   tf-version-bump -pattern *.tf -config <config> -report-file <trusted temporary report>
   ```

2. Validate the report as schema version 1 with non-negative integer module/provider counts, then
   add those counts to branch-wide totals.
3. Run `terraform -chdir=<root> init -upgrade -backend=false -input=false -no-color` with the
   existing isolated `TF_DATA_DIR`.
4. Enforce the existing provider lock-file policy and allowed update/init paths.

After every root completes, preparation stages the update/init result in a temporary Git index and
checks its aggregate diff against the exact base:

- If there is no diff, classify the branch as `no-change`. Formatting does not run, even when the
  input is enabled.
- If there is a diff, write an `update.patch`, record its SHA-256, and record the update/init
  changed-file metadata and aggregate block counts.

When formatting is enabled and the update/init diff is non-empty, run this command from every
configured root in declared order:

```text
terraform -chdir=<root> fmt -recursive
```

Formatting shares the remaining preparation deadline. After every root succeeds, stage the final
candidate in another temporary index and compare it with the update/init tree:

- Write `format.patch` only when that comparison is non-empty.
- Record formatting paths separately from update/init paths.
- Record final file metadata from the complete base-to-final comparison.

The temporary indexes and their tree objects provide the intermediate boundary without creating
an unsigned or unpublished temporary commit.

## Preparation artefact schema

Successful and no-change manifests use schema version 2. A successful manifest has this logical
shape; identity, tool, config, root, and artefact-name fields remain as in the existing contract:

```json
{
  "schema_version": 2,
  "classification": "success",
  "terraform_fmt": true,
  "updates": {
    "module_blocks_updated": 4,
    "provider_blocks_updated": 2,
    "patch_sha256": "<sha256>",
    "changed_files": [
      {"path": "root/main.tf", "mode": "100644", "sha256": "<stage-sha256>"}
    ]
  },
  "formatting": {
    "ran": true,
    "patch_sha256": "<sha256>",
    "changed_files": [
      {"path": "root/nested/child.tf", "mode": "100644", "sha256": "<final-sha256>"}
    ]
  },
  "final_changed_files": [
    {"path": "root/main.tf", "mode": "100644", "sha256": "<final-sha256>"},
    {"path": "root/nested/child.tf", "mode": "100644", "sha256": "<final-sha256>"}
  ]
}
```

`formatting` always records whether formatting ran. It omits `patch_sha256` and uses an empty
`changed_files` array when formatting ran without changing files. When formatting was disabled or
skipped because updates produced no diff, `ran` is false and no format patch exists.

A no-change manifest preserves the aggregate report counts, records empty update and final file
lists, contains no update patch, and sets `formatting.ran: false`. Counts may be non-zero when
sequential config entries changed a block and ultimately returned it to its base value; the Git diff,
not the counts, controls no-change classification and formatting eligibility. Failure bundles
contain the existing bounded failure record and logs but no update or format patch. The new bounded
preparation classification is `branch-format` with failure stage `terraform fmt`.

All three job-produced manifest types move together to schema version 2. A run cannot mix schemas
because artefact names include the run attempt and immutable control identity, and partial reruns
are rejected.

## Stage-specific path policy

Update/init paths retain the existing policy:

- Direct `.tf` files beneath exactly one declared root.
- A direct `.terraform.lock.hcl` beneath a declared root.
- Regular non-symlink files resolving inside the target checkout.

Formatting paths use this policy:

- A `.tf` file at any depth beneath at least one configured root.
- No path beneath a `.terraform` directory.
- A regular non-symlink file resolving inside the target checkout.
- The existing newline, UTF-8, Git mode, digest, and unexpected-path protections.

Overlapping configured roots remain allowed. Formatting runs once from each supplied path as
requested; stage file lists contain unique Git paths, so a nested file is reported once even if two
recursive invocations reached it.

The update and formatting file lists may overlap. An update-list digest identifies the
intermediate update/init tree, while a formatting-list and final-list digest identifies the final
formatted tree.

## Validation

Validation downloads the preparation bundle into a fresh exact-base checkout and validates schema
version 2 before running Terraform:

1. Verify and apply `update.patch` to the exact base when classification is `success`.
2. Verify and apply `format.patch` to the update state when the formatting manifest declares a
   non-empty formatting diff.
3. Confirm the resulting Git status and final file metadata match `final_changed_files` exactly.
4. Run the existing per-root non-upgrade initialisation. Use `-lockfile=readonly` when a lock file
   exists.
5. Run `terraform validate -no-color` for every root.
6. Confirm Terraform did not alter the candidate checkout and upload a validation outcome bound to
   the preparation-manifest SHA-256.

Validation failures retain the existing `branch-validation` classification and marked-issue
lifecycle.

## Verification

Verification receives the preparation bundle and validation outcome in its current credential-free
job. It must:

- Validate schema version 2 and the complete run/control/base/policy identity.
- Require the correct presence or absence of `update.patch` and `format.patch` for the declared
  classification and formatting state.
- Recompute both patch SHA-256 values.
- Apply the patches in order to a clean exact-base index/worktree.
- Validate each patch against its stage-specific path policy and changed-file metadata.
- Validate the final base-to-candidate paths, modes, and digests against `final_changed_files`.
- Bind the successful or failed validation outcome to the exact preparation manifest.
- Copy both patches and the verified staged metadata into the verified-result artefact only after
  every check succeeds.

Verification validates provenance and integrity of the staged result. It deliberately does not
repeat the preparation or validation commands.

## Publication and commits

Publication applies the verified update patch first and creates the update/init commit. Its subject
is chosen from the exact report counts:

| Updated block counts | Commit subject |
|----------------------|----------------|
| Modules and providers | `chore: bump Terraform provider and module versions` |
| Providers only | `chore: bump Terraform provider versions` |
| Modules only | `chore: bump Terraform module versions` |
| Neither, but update/init files changed | `chore: update Terraform configuration` |

The fallback covers Terraform `required_version` or lock-only changes because report schema version
1 intentionally excludes an exact `required_version` count.

When a verified format patch exists, publication applies it to the first commit and creates a second
commit with the fixed subject:

```text
chore: run Terraform fmt
```

Both commits carry the existing automation ownership and base trailers. Publication uses the same
deterministic author, committer, timestamp policy, local verification, exact update-ref lease, state
ref rechecks, and compensation behaviour. The managed update ref points to the formatting commit
when present and otherwise to the update/init commit.

The example retains its current explicitly unsigned automation-commit policy. Commit signing and
GitHub App authentication remain separate deferred production-hardening work.

Dry runs construct and verify the same local one- or two-commit sequence but do not push refs or
mutate pull requests or issues.

## Pull-request body

The managed pull request retains its exact hidden marker and existing branch, base, and policy
metadata. It adds:

- Exact module blocks updated.
- Exact provider blocks updated.
- `Dependency and lock-file changes`, including the unique file count and encoded paths from the
  update/init stage.
- `Formatting changes`, including the unique file count and encoded paths, or `None` when no format
  patch exists.

A path changed by both stages appears in both sections because each list describes a commit, not
only the final union. All dynamic values use the existing HTML/Markdown encoding helpers so paths
cannot create mentions or inject markup. Reruns replace the body of the existing exactly marked
pull request rather than appending stale results.

## Documentation

Update the copyable example README and advanced-usage guide to explain:

- The `terraform_fmt` input and its default.
- That formatting runs only after a non-empty update/init stage and runs recursively from every
  configured root.
- The optional second commit and its fixed subject.
- The dynamic first commit subjects.
- The split pull-request file lists and exact report counts.
- The new `terraform fmt` failure classification.
- The `v1.0.0-rc.9` release pin and digest.

## Test strategy

Use the existing Bash integration harness and repository documentation checks. Add tests before
implementation for:

- The reusable input contract and caller opt-in.
- `v1.0.0-rc.9` download, checksum, and version verification.
- Aggregating module/provider counts across multiple configured roots.
- Disabled formatting.
- Skipping formatting when update/init produces no diff.
- Recursive formatting of a nested `.tf` file from each configured root.
- Formatting enabled but producing no format patch.
- Bounded format failure artefacts, diagnostics, verification, and marked-issue lifecycle.
- Rejection of nested formatting paths outside roots, paths beneath `.terraform`, symlinks,
  undeclared files, changed modes, invalid UTF-8, and tampered patch/file digests.
- Applying the two patches in order during validation and verification.
- One update commit when formatting is unchanged and two commits when formatting changes files.
- All four dynamic update/init commit subjects.
- The fixed `chore: run Terraform fmt` subject.
- Both commits carrying the automation ownership/base trailers.
- Pull-request counts and split, safely encoded file lists, including a path present in both lists.
- Dry-run creation of the same local commit topology without remote or GitHub mutation.
- Existing exact-lease, compensation, issue, no-change, timeout, and failure behaviour remaining
  intact.

Run the complete GitHub Actions example harness, documentation checks, Go race suite, lint, vet,
and build before publication. Follow implementation with the required independent test-cleanup and
code-review passes.

## Acceptance criteria

Stage two is complete when:

- The example downloads and verifies `v1.0.0-rc.9`.
- Formatting is opt-in at the reusable boundary and enabled by both supplied callers.
- No update/init diff means no formatting invocation and no publication.
- A successful formatted candidate is represented by two verified patches and two deterministic
  commits only when the second patch is non-empty.
- Recursive formatting can change safe nested `.tf` files beneath configured roots and no other
  path class.
- The managed pull request reports exact module/provider block counts and separate file lists for
  both commits.
- Formatting failures use the existing bounded issue lifecycle without publishing partial work.
- Terraform/provider execution remains separated from repository write credentials.
- All focused and full validation commands pass with pristine expected output.
