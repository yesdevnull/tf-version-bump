# GitHub Actions staged update and formatting design

## Status

Revised following review on 2026-08-22. This design extends the copyable GitHub Actions example
after `tf-version-bump v1.0.0-rc.9` added machine-readable module and provider update counts. The
revision folds staged-result verification into validation while retaining isolated publication.

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
- Keep preparation and validation unprivileged, fold staged-result verification into validation,
  and retain a separate privileged publication job.

## Non-goals

- Extend the `tf-version-bump` report schema beyond its existing module/provider counts.
- Report exact Terraform `required_version` update counts.
- Interpret `.terraform.lock.hcl` changes beyond listing them in the update/init file section.
- Expose repository write credentials to Terraform, providers, or modules.
- Add backward compatibility for artefacts produced by an older control revision. Partial job
  reruns remain unsupported, and every artefact remains bound to the run attempt and control OID.
- Automatically merge or approve generated pull requests.

## Job and trust boundaries

After discovery, the workflow uses three processing jobs:

1. **Prepare**, with `contents: read`, runs the released updater and `terraform init -upgrade`,
   optionally formats the resulting candidate, and uploads immutable staged patches and a manifest.
2. **Validate**, with `contents: read`, checks the preparation identity and patch integrity, applies
   both patches in order to one fresh exact-base checkout, verifies the staged paths and file
   metadata, runs non-upgrade initialisation and `terraform validate`, confirms that validation did
   not mutate the candidate, and uploads the verified result.
3. **Publish**, with repository write permissions, consumes only the verified result, constructs
   the deterministic commit sequence, updates the managed branch under its exact lease, and
   creates or refreshes the marked pull request.

The privileged publication job never executes Terraform or downloaded provider/module code.

This simplified topology deliberately trusts every configured provider and module source. Provider
plugins execute with the validation runner's authority, so combining validation and verification no
longer protects publication from malicious provider code in the way a verifier on a clean runner
would. Private or first-party provenance is the trust decision; it is not a sandbox boundary.

After Terraform exits, a trusted host-side reconciliation step rechecks the exact control checkout,
preparation manifest, validation outcome, both patch digests, stage metadata, and final candidate
before atomically creating the verified result. These checks catch accidental or non-adversarial
mutation. Consumers that do not fully trust all provider code must restore an independent clean
verifier or provide equivalent isolation. The separate publication job remains mandatory so
Terraform never receives repository write credentials.

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

Every manifest type has the same seven identity fields: `run_id`, `run_attempt`,
`automation_policy_id`, `control_oid`, `state_branch`, `base_oid`, and `ref_hash`. Every preparation
manifest uses schema version 2 and additionally requires its existing `artifact_name`. Successful
and no-change preparations also require `tools`, `config_path`, and non-empty `roots`. A successful
manifest has this logical shape in addition to those fields:

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

The preparation bundle has these normative variants:

| Classification | Required manifest data | Patch files |
|----------------|------------------------|-------------|
| `success` | `terraform_fmt`, `updates`, `formatting`, and non-empty `final_changed_files` | `update.patch`; `format.patch` only when `formatting.changed_files` is non-empty |
| `no-change` | `terraform_fmt`, aggregate counts in `updates`, empty stage/final file lists, and `formatting.ran` reflecting whether formatting actually ran | None |
| `branch-update` | Existing bounded `failure` with stage `tf-version-bump` | None |
| `branch-init` | Existing bounded `failure` with stage `terraform init` | None |
| `branch-format` | Existing bounded `failure` with stage `terraform fmt` | None |
| `automation` | Existing diagnostic failure data identifying the control-contract stage | None |

The bundle's exact top-level allow-list is `manifest.json`, `logs/`, and the patch files permitted
by the table. `logs/` contains only the existing bounded, regular, non-symlink stage log filenames;
unknown entries at either level invalidate the bundle.

`updates.module_blocks_updated` and `updates.provider_blocks_updated` are non-negative integers.
Every changed-file list contains unique path/mode/SHA-256 records and is sorted by encoded Git path.
`updates.patch_sha256` exists exactly when `update.patch` exists. `formatting.ran` always records
whether formatting ran; `formatting.patch_sha256` exists exactly when `format.patch` exists. A
successful formatting run that changes no files therefore has `ran: true`, no formatting digest,
and an empty formatting list.

After optional formatting, preparation compares the final tree with the exact base again. If they
are equal, it discards both intermediate patches, clears every changed-file list, and classifies the
bundle as `no-change`; `formatting.ran` remains true when formatting performed the cancellation.
Aggregate report counts remain available for diagnostics but never override final-tree
classification. Counts may also be non-zero when sequential config entries changed a block and
ultimately returned it to its base value.

If the updater exits zero but its report is absent, malformed, not schema version 1, or contains an
invalid count, preparation emits an `automation` diagnostic bundle. It retains bounded logs but no
patch or validation outcome. Validation converts the checked bundle into an `automation` verified
disposition so the matching publication matrix entry can terminate cleanly without a branch issue,
commit, or pull request. This is a trusted tool/control-contract failure rather than a state-branch
failure.

Preparation, validation-outcome, and verified-result manifests move together to schema version 2.
Artefact names bind the run ID, run attempt, automation policy ID, and state-ref hash. Manifest
identity validation separately binds the immutable control OID. Partial job reruns remain rejected.

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

The update and formatting file lists may overlap. The update patch digest plus its per-file metadata
bind the intermediate update/init tree. The optional format patch digest, formatting metadata, and
final per-file metadata bind the final formatted tree.

## Validation and verification

Validation downloads the preparation bundle into one fresh exact-base checkout. In that checkout,
the job validates schema version 2 and the complete run/control/base/policy identity before running
Terraform. For a successful preparation it performs the following sequence exactly:

1. Check the allowed bundle files, recompute both declared patch SHA-256 values, and validate the
   update patch's structure, paths, and policy without applying it.
2. Apply `update.patch` to the exact base, then compare the complete intermediate Git diff with
   `updates.changed_files`, including every path, mode, and intermediate file digest.
3. When declared, validate the format patch's structure, paths, and policy without applying it.
4. Apply `format.patch` to the verified intermediate state, then compare that stage's exact paths
   and resulting file metadata with `formatting.changed_files`.
5. Compare the complete base-to-candidate paths, modes, and digests with `final_changed_files`.
6. Run the existing per-root non-upgrade initialisation. Use `-lockfile=readonly` when a lock file
   exists.
7. Run `terraform validate -no-color` for every root.
8. Confirm Terraform did not alter the candidate checkout and write the local validation outcome.

The local validation-outcome directory contains only `manifest.json` and `logs/`; its logs use the
existing bounded regular-file allow-list. The manifest uses schema version 2 and copies exactly the
seven common identity fields.
It has this normative shape:

```json
{
  "schema_version": 2,
  "classification": "success",
  "candidate_manifest_sha256": "<preparation-manifest-sha256>",
  "command_status": 0
}
```

For a successful or no-change preparation, an outcome is required. Its classification must match
the preparation classification unless Terraform initialisation or validation fails, in which case
it is `branch-validation`, has a non-zero `command_status`, and includes the existing bounded
`failure` object. Preparation classifications `branch-update`, `branch-init`, and `branch-format`
forbid a validation outcome because Terraform is not run. An `automation` preparation failure also
forbids an outcome and is represented only by a non-publishing verified disposition.

The candidate-validation step uses `continue-on-error` so a bounded `branch-validation` outcome
does not skip reconciliation. A trusted reconciliation step runs under `if: always()` after all
Terraform processes exit. It rechecks the control checkout identity and cleanliness, preparation
manifest digest, allowed bundle files, both patch digests, stage metadata, candidate state, and the
required presence or absence and identity of the local outcome. It then atomically assembles the
verified result. The verified-result upload and final classification step also use `if: always()`.
Bounded preparation or validation failures count as successfully packaged workflow results so the
publication job can maintain their marked issues. A well-formed `automation` preparation result is
also packaged, but publication treats it as a no-op. Malformed contracts fail closed without a
verified result, causing that matrix entry to remain red rather than reaching repository mutation.
Every well-formed matrix entry therefore uploads exactly one verified result, avoiding unsafe
aggregation through matrix job outputs. After an `automation` result is uploaded, the final
classification step exits non-zero; publication still runs under its existing `if: always()`
dependency, consumes the verified no-op disposition, and performs no mutation. The validation job
and overall workflow therefore remain visibly failed.

A preparation branch failure skips Terraform and reconciliation creates the verified failure result
directly from the checked manifest and logs. A separate validation-outcome artefact and a second
checkout are unnecessary because the local outcome is checked and bound before the verified result
leaves this job. The combined job remains credential-free beyond `contents: read` and does not
repeat preparation commands.

## Verified-result artefact schema

The verified-result directory contains only `manifest.json`, `update.patch` when required, and
`format.patch` when required. Its manifest uses schema version 2, copies exactly the seven common
identity fields, and binds the source artefacts. A successful result has this logical shape:

```json
{
  "schema_version": 2,
  "classification": "success",
  "preparation_manifest_sha256": "<sha256>",
  "validation_outcome_sha256": "<sha256>",
  "terraform_fmt": true,
  "updates": {
    "module_blocks_updated": 4,
    "provider_blocks_updated": 2,
    "patch_sha256": "<update-patch-sha256>",
    "changed_files": [
      {"path": "root/main.tf", "mode": "100644", "sha256": "<stage-sha256>"}
    ]
  },
  "formatting": {
    "ran": true,
    "patch_sha256": "<format-patch-sha256>",
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

`preparation_manifest_sha256` is required for every classification and binds the exact preparation
manifest from which the result was derived. The verified result has these additional presence rules:

| Classification | Outcome binding | Staged metadata and patches | Failure data |
|----------------|-----------------|-----------------------------|--------------|
| `success` | Required | Copy all counts and lists; require `update.patch`; require `format.patch` exactly when its digest exists | Forbidden |
| `no-change` | Required | Copy counts and formatting-run state; require empty lists and no patches | Forbidden |
| `branch-update`, `branch-init`, `branch-format` | Forbidden | Forbidden | Required bounded `failure` and validated raw workflow `run_url` |
| `branch-validation` | Required | Forbidden | Required bounded `failure` and validated raw workflow `run_url` |
| `automation` | Forbidden | Forbidden | Required diagnostic `failure` and validated raw workflow `run_url` |

Publication accepts only these five variant groups. It revalidates schema and common identity,
requires the exact allowed file set, recomputes every present patch digest, enforces all
classification-specific presence rules, and rejects unknown fields that could create an ambiguous
contract. The preparation and validation digests are audit bindings; their source manifests are not
copied into the narrowly scoped verified result. Publication applies the existing contextual
HTML/Markdown escaping to the raw `run_url`; producers must not pre-encode it.

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

A verified `no-change` result creates no commit and performs no GitHub mutation, including when
formatting cancelled the complete update/init diff. Publication obtains all counts and file lists
from the verified-result manifest; it never consults the preparation bundle or local validation
outcome.

A verified `automation` result also performs no Git or GitHub mutation. It exists only to give each
validation/publication matrix pair a deterministic, per-entry disposition without relying on shared
matrix job outputs.

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
- Formatting completely cancelling the update/init tree and producing a final no-change result with
  no commits or pull request.
- Bounded format failure artefacts, diagnostics, validation-time verification, and marked-issue
  lifecycle.
- Missing, malformed, wrong-schema, and invalid-count report files producing an uploaded
  non-publishing `automation` disposition while validation and the workflow remain failed.
- Rejection of nested formatting paths outside roots, paths beneath `.terraform`, symlinks,
  undeclared files, changed modes, invalid UTF-8, and tampered patch/file digests.
- Applying and verifying the two patches in order once during validation, including intermediate
  digests for files touched by both patches.
- Schema and exact-file-set rejection tests for every preparation, validation-outcome, and
  verified-result classification.
- Always-run reconciliation and upload after bounded preparation and validation failures, with no
  local outcome for preparation failures.
- Post-Terraform control, patch, outcome, stage-metadata, and candidate rechecks before verified
  result assembly.
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
- A final tree equal to the base after formatting means no commits or publication.
- A successful formatted candidate is represented by two verified patches and two deterministic
  commits only when the second patch is non-empty.
- Recursive formatting can change safe nested `.tf` files beneath configured roots and no other
  path class.
- The managed pull request reports exact module/provider block counts and separate file lists for
  both commits.
- Formatting failures use the existing bounded issue lifecycle without publishing partial work.
- Terraform/provider execution remains separated from repository write credentials.
- All focused and full validation commands pass with pristine expected output.
