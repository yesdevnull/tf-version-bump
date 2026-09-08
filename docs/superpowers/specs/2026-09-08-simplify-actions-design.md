# Simpler GitHub Actions branch automation

Dan approved the review recommendation on 8 September 2026: simplify the
copyable example for trusted official providers and private organisation
modules, using a new worktree. This document records that approved design.

## Flow

Use three jobs: discover, process, publish. Discovery keeps literal prefix
selection and immutable branch/run identities. Processing uses one disposable
checkout, runs update, init, optional recursive fmt, then validate, and emits
one result manifest, one final candidate patch on success, and diagnostic logs.
Publication runs in a separate job with repository write permissions and no
Terraform registry token. It checks the candidate and reconciles the update
branch, marked PR and marked failure issue.

## Contract

The result directory contains result.json, logs/, and candidate.patch only for
a changed, successfully validated candidate. Schema version 3 identifies the
new internal format; no compatibility adapter for the old artefacts or modes.
The manifest includes run_id, run_attempt, automation_policy_id, control_oid,
state_branch, base_oid, ref_hash, classification, roots (relative path strings),
and on success patch_sha256. Failures carry failure {stage, root, status}.
Classifications remain success, no-change, branch-update, branch-init,
branch-format, branch-validation and automation. Optional counts may be omitted;
use a constant meaningful commit subject and a concise PR summary linking the run.

Process entry point: process-state-branch.sh process. Existing PROCESS identity,
tool pin, config, root, formatting and upgrade variables retain their meanings.
PROCESS_RESULT_DIR replaces preparation/outcome destinations.
Reconcile entry points: classify and publish. RECONCILE_RESULT_DIR replaces
RECONCILE_VERIFIED_RESULT_DIR. RECONCILE_RUN_URL is provided by the workflow.
Publication roots are checked against RECONCILE_TERRAFORM_ROOTS.

## Protections to preserve

- Default-branch control configuration and allowed literal branch prefixes.
- Required inputs, boolean options, immutable base/run/policy identity.
- Existing distinct roots and config contained inside their respective checkouts.
- Clean exact-base checkout; no symlink Terraform inputs escaping roots.
- Plain init by default, explicit upgrade, backend disabled and noninteractive.
- Include generated provider lock files; reject ignored lock files.
- Validate no-change candidates, then close obsolete marked PR and failure issue.
- One final patch restricted to Terraform files below configured roots and
  lock files directly inside them; reject deletion and symlink/type changes.
- Verify patch digest, allowed paths and base before creating one update commit.
- Check the remote base, update-ref ownership and exact force-with-lease.
- Close marked PR before publishing a branch failure; fail on API lookup errors.
- Automation or missing/invalid results never trigger cleanup or publication.
- Dry runs do not mutate remote refs or GitHub records.
- Bound commands and retain useful diagnostics without treating log filenames
  as part of the publication contract.

## Deliberate reductions

Drop the second init/fresh-checkout verification, intermediate formatting patch
and commit, per-file digest inventories, repeated exhaustive manifest schemas,
log-name validation, readonly artefact chmod protocol and release download cache.
Keep straightforward Bash, jq, Git, Terraform and the existing tools; no new
runtime or framework. Prefer clear sequential code to dense abstractions.
Do not execute Terraform with publication credentials or claim hostile-code
isolation. Expected target is roughly 1,000–1,400 helper lines, not a hard cap.

## Validation

TDD covers the combined pipeline with real Terraform and released updater,
including provider lock/upgrade behaviour, formatting, no-change validation,
failure artefacts and input checks. Publication tests use real temporary Git
repositories and boundary-level GitHub command capture for PR/issue decisions;
these are component tests, not live GitHub end-to-end tests. Preserve discovery
tests. Replace assertions on obsolete internal stages with observable new
contracts. Independent review and test-cleanup follow implementation.

Every commit made by Codex, including fixtures, must be signed. Never run the
old publication path that disables signing. Respect caller signing configuration
in the simplified publisher; fixture Git uses the dedicated Codex wrapper.

## Approval and scope

The chat approval authorises this restructuring and its documented trade-offs.
It does not request a push, PR or merge for this new branch.
