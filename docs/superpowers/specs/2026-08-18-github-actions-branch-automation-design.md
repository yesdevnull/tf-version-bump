# GitHub Actions state-branch automation design

## Purpose

Add a copyable GitHub Actions example that applies a `tf-version-bump` configuration across
Terraform state branches, upgrades provider selections, validates the result, and reconciles one
pull request or failure issue per state branch.

The example lives in this repository under `examples/github-actions/`, but consumers copy its
`.github` tree into their own default branch. The implementation is deliberately separate from
the core Go CLI and from `examples/update-branches.sh`. It reuses the existing script's safe branch
selection and commit principles while using isolated GitHub Actions jobs for the PR and issue
lifecycle.

## Goals

- Provide one reusable workflow containing the update behaviour.
- Provide non-production and production caller workflows with both manual and scheduled triggers.
- Select remote state branches using configured literal prefixes.
- Permit a manual run to narrow, but never widen, the caller's branch allow-list.
- Use separate default-branch configurations for production and non-production updates.
- Apply updates, run `terraform init -upgrade -backend=false`, and run `terraform validate` before
  publishing a branch.
- Persist `.terraform.lock.hcl` changes alongside Terraform source changes.
- Return a non-zero CLI status after any per-file processing error so automation cannot publish a
  partial update as successful.
- Install an immutable released CLI artefact that contains that aggregate failure-status contract;
  the example must never validate a working-tree binary and publish with an older release.
- Maintain stable update branches, pull requests, and validation-failure issues without creating
  duplicates on scheduled runs.
- Support a dry run that still applies and validates proposed changes in a disposable checkout
  while suppressing repository refs and GitHub content mutations.
- Support the built-in `GITHUB_TOKEN`, optional GitHub App authentication, and optional SSH commit
  signing.
- Keep target-selected providers away from Actions workflow command files and capture their process
  status through a trusted host supervisor, without claiming that an adversarial provider performs
  semantically honest validation.
- Prevent unattended downstream pull-request checks from silently escaping the same hostile-code
  boundary.
- Show how the same workflow can validate multiple Terraform root modules without complicating the
  root-directory example.

## Non-goals

- Change the `tf-version-bump` YAML configuration format.
- Turn `examples/update-branches.sh` into a GitHub API orchestration tool.
- Initialise or access Terraform state backends.
- Merge or approve generated pull requests.
- Delete, reset, force-push, or otherwise modify a state branch.
- Provide arbitrary regular-expression or shell-glob branch matching.
- Add configurable post-update command hooks in the initial example.
- Prove that an adversarial provider truthfully implements its own validation protocol. The
  automation can authenticate Terraform's supervised process result, not the provider's intent.
- Make Git ref updates and GitHub pull-request API mutations one distributed transaction. The
  workflow instead uses atomic Git ref updates, exact leases, compensating rollback, and explicit
  recovery rules.

## Example layout

The example mirrors the destination paths in a consuming repository:

```text
examples/github-actions/
├── README.md
└── .github/
    ├── scripts/
    │   ├── discover-state-branches.sh
    │   └── process-state-branch.sh
    ├── tf-version-bump/
    │   ├── nonproduction.yml
    │   └── production.yml
    └── workflows/
        ├── tf-version-bump-reusable.yml
        ├── tf-version-bump-nonproduction.yml
        └── tf-version-bump-production.yml
```

GitHub requires reusable workflow files to be directly under `.github/workflows`, so consumers
copy the example's `.github` directory rather than invoke a workflow from its example path.

The shell scripts keep non-trivial orchestration out of YAML blocks. They have help output,
validate their inputs, fail with stage-specific messages, and limit routine output while retaining
failure logs for diagnosis. They expose separate preparation, validation, and reconciliation
operations so the privileged job never executes target-branch Terraform. Reconciliation itself is
split into credential-free verification/staging, authenticated ownership preflight, and publication
operations so write credentials and signing material appear only when required.

## Workflow architecture

The production and non-production callers own policy. Each caller defines its allowed prefixes,
configuration path, pinned tool versions, schedule, and concurrency group, then invokes the local
reusable workflow.

The reusable workflow contains five job stages:

1. `discover` checks out the exact caller commit, validates the shared configuration, and produces
   a sorted JSON matrix containing each matching state branch and its immutable tip OID.
2. `prepare` runs once per matrix entry with `fail-fast: false`. It applies the update and runs
   Terraform initialisation on an unprivileged fresh runner, then uploads a preparation bundle
   before any provider plugin executes. A successful bundle contains the immutable candidate.
3. `validate` runs once per successful candidate on another unprivileged fresh runner. A trusted
   host supervisor consumes the candidate without changing its publishable payload and runs
   Terraform inside a pinned, constrained container that receives no Actions runtime variables,
   workflow command-file paths, control checkout, candidate bundle, or host outcome path. The host
   supervisor records the container exit status and uploads only a candidate-bound outcome and
   captured logs.
4. `verify` runs once per matrix entry on another fresh runner after the complete validation
   matrix. With `contents: read` and no publication environment, it treats both artefacts as
   untrusted data, revalidates and stages the pre-provider candidate, and emits a narrowly scoped
   verified-result artefact. It executes neither Terraform nor target-supplied code.
5. `publish` runs once per verified matrix entry on a fresh protected runner. It validates the
   verified-result identity, mints the selected credential, checks ownership and remote refs,
   materialises signing key data only immediately before commit creation, and publishes, reports a
   deterministic branch failure, or records a workflow-level failure.

Preparation captures update and initialisation status and uploads its bundle in unconditional
cleanup steps before returning the recorded failure status. Validation does the same for its
host-supervised, candidate-bound outcome and logs. Verification and publication use `always()` so
one failed matrix entry does not suppress reporting for the others. Verification expects one
preparation bundle for every entry, and a validation outcome only when preparation produced a
successful candidate. Publication expects exactly one verified result for its current entry and
attempt. A missing, duplicate, unexpected, wrong-attempt, or mismatched artefact is an automation
failure and can never be interpreted as success.

The verified-result artefact contains a versioned manifest and the already verified patch needed to
recreate the staged tree. Its name and manifest bind the run ID, run attempt, policy, control OID,
state ref, base OID, candidate digest, validation-outcome digest, changed paths, modes, and hashes.
It contains no executable control logic or credentials. Publication validates those fields and
applies the patch as data to a fresh exact-base checkout; it never sources files from the artefact or
executes target content.

The update matrix defaults to four concurrent branches. This bounds Terraform downloads and GitHub
API writes while retaining useful parallelism. A caller can lower or raise the limit through the
reusable workflow's `max_parallel` input.

The preparation and validation jobs have `contents: read`, use `persist-credentials: false` for
every checkout, and never receive a write token, GitHub App private key, or commit-signing key. Each
uses two independent checkouts:

- A control checkout of the immutable caller SHA supplies the reviewed scripts and
  `tf-version-bump` configuration.
- A target checkout of the immutable discovered state-branch OID receives the proposed changes.

Terraform can execute target-selected provider plugins during validation. Therefore checkout
separation is not treated as a process-security boundary. Preparation uploads the immutable source
and lock-file candidate before provider execution. Validation mounts only a disposable validation
workspace and a trusted `TF_DATA_DIR` into the container; it never mounts the candidate bundle,
control checkout, host outcome directory, Docker socket, Actions command files, or Actions runtime
environment. The container receives only explicit Terraform configuration values. When it exits,
the trusted host supervisor constructs the outcome from its observed status. A provider may still
lie through its own protocol and make Terraform report success; that semantic limitation is stated
explicitly. The fresh publication job is the mutation boundary and executes neither
Terraform nor target-supplied executables.

A successful preparation bundle contains a versioned JSON manifest, captured preparation logs, and
a binary Git patch. The manifest records the workflow run ID and attempt, stable automation policy
ID, control OID, state branch, validated base OID, config path, exact tool versions, release archive
digest, and validation image digest, preparation classification, configured Terraform directories,
provider-dependency state, changed paths, file modes, and SHA-256 hashes. A failed preparation
bundle contains its
classification and logs but no publishable patch. Artefact names include the run ID, run attempt,
policy ID, and branch hash. The validation outcome records those identities, the candidate manifest
digest, the host-observed container result, validation classification, and captured logs.
Reconciliation rejects unknown manifest versions, mismatched run identities, policies, refs, OIDs,
or candidate digests, absolute or escaping paths, symlink or non-regular modes, unexpected file
types, hash mismatches, and patch paths outside the declared set. It applies the patch only after a
fresh exact-base checkout and independently reproduces the `tf-version-bump` source changes before
accepting lock-file changes from the pre-provider candidate bundle.

Every stage has a bounded inner operation timeout plus a larger job timeout so the workflow can
capture logs and classifications before GitHub terminates the job. Discovery is limited to 10
minutes. Preparation and validation commands are limited to 20 minutes per Terraform root and their
jobs to 30 minutes. Verification is limited to 15 minutes and its job to 20 minutes; publication is
limited to 10 minutes and its job to 15 minutes. A preparation initialisation timeout is
`branch-init`; a validation timeout is `branch-validation`; all other job timeouts are automation
failures.

## Triggers and schedules

Both caller workflows support `workflow_dispatch` and `schedule`.

The manual interface contains:

- `branch_prefix`: an optional literal prefix used to narrow the configured allow-list.
- `dry_run`: a Boolean input whose default is `false`.

Scheduled runs always use the complete caller allow-list and `dry_run: false`.

The example schedules are deliberately away from the top of the hour and use GitHub's IANA
timezone support:

- Non-production: daily at 04:17 in `Australia/Melbourne`.
- Production: Sunday at 04:43 in `Australia/Melbourne`.

Each caller has its own concurrency group with `queue: max` and no in-progress cancellation.
Current GitHub.com queues up to 100 pending runs in that group instead of replacing an existing
pending run. Production and non-production can run independently. Publication additionally uses a
policy-independent per-state-branch concurrency group with `queue: max`, derived from the repository
ID and branch hash. Two policies can prepare concurrently but cannot publish the same
`update_<state-branch>` ref concurrently, and neither policy's pending publication is silently
replaced. GitHub Enterprise Server consumers must confirm that their installed version supports the
`queue` property before copying the example.

Manual runs are accepted only when `github.ref` is the repository's default branch. This catches
accidental alternate-ref dispatches. The publication job also uses a required protected GitHub
environment restricted to the default branch. App and signing private keys live only in that
environment; they are not passed through `workflow_call`. Repository administrators must limit
manual dispatch to trusted actors. Built-in-token mode additionally assumes repository write
access is trusted because a writer can submit modified workflow YAML even when no optional secret
is exposed. App-mode callers cap the reusable workflow's `GITHUB_TOKEN` at `contents: read`; only
the short-lived App token receives publication permissions.

Built-in-token mode is the safe default for repositories whose downstream pull-request workflows
have not been audited; generated checks remain approval-gated. App mode is accepted only when the
caller explicitly sets `unattended_checks_safe: true`. That assertion means every downstream
workflow applicable to the state branches uses reviewed workflow definitions, a read-only token,
no repository or environment secrets, no privileged environment, and equivalent isolation before
executing target-controlled code. The reusable workflow cannot enforce another workflow's runtime
permissions, so it rejects App mode without this explicit policy assertion and the documentation
requires a repository audit. App mode is not described as safe for generic unattended checks.

## Reusable workflow interface

| Input | Type | Required/default | Purpose |
|---|---|---|---|
| `automation_policy_id` | string | Required | Stable lowercase identity such as `nonproduction` or `production` |
| `allowed_branch_prefixes` | string | Required | Newline-separated literal prefix allow-list |
| `branch_prefix` | string | `""` | Optional manual narrowing prefix |
| `config_path` | string | Required | Config path in the default-branch control checkout |
| `terraform_directories` | string | `.` | Newline-separated module directories processed independently |
| `terraform_version` | string | Required | Exact Terraform version installed for the run |
| `terraform_image` | string | Required | Validation image pinned by tag and multi-platform digest |
| `tf_version_bump_version` | string | Required | Exact `tf-version-bump` release tag used in the archive URL and version check |
| `tf_version_bump_archive_sha256` | string | Required | Recorded SHA-256 of the Linux x86-64 release archive |
| `dry_run` | boolean | `false` | Suppress repository refs and GitHub content mutations |
| `max_parallel` | number | `4` | Maximum concurrent state-branch jobs |
| `commit_author_name` | string | `""` | Explicit signed-commit identity; derive bot name when unsigned |
| `commit_author_email` | string | `""` | Explicit signed-commit identity; derive bot email when unsigned |
| `github_app_client_id` | string | `""` | Enable GitHub App authentication when paired with its key |
| `unattended_checks_safe` | boolean | `false` | Required acknowledgement of the downstream-check contract for App mode |
| `publication_environment` | string | Required | Default-branch-restricted environment for publication |

Optional protected-environment secrets with fixed names are:

- `TF_VERSION_BUMP_GITHUB_APP_PRIVATE_KEY`
- `TF_VERSION_BUMP_COMMIT_SIGNING_PRIVATE_KEY`

The workflow validates `automation_policy_id` against
`^[a-z0-9][a-z0-9-]{0,31}$`, validates the release tag as a `v`-prefixed semantic version, requires
the archive digest to be exactly 64 lowercase hexadecimal characters, and binds all three into every
applicable artefact and manifest; the policy also binds every marker and ownership check.
The required update branch name remains exactly `update_<state-branch>`; a different policy's marked
ref is foreign and causes a safe failure rather than adoption. The workflow rejects partial
authentication or signing configurations. A GitHub App client ID requires the App private key and
`unattended_checks_safe: true`. When the signing key is present, the caller must explicitly supply
both commit identity inputs rather than inherit a bot default. Environment secrets cannot be passed
or selected by an untrusted caller.

The example pins `tf_version_bump_version: v1.0.0-rc.7` and the Linux x86-64 archive's actual
published SHA-256. Implementation of this workflow cannot start until the aggregate per-file
failure change has been merged, released under that tag, and the published artefact has passed
checksum/provenance verification. Each job downloads that exact archive, verifies it against the
recorded digest before extraction, and verifies the binary's reported version. Tests execute the
same downloaded binary; they do not substitute a binary built from the automation feature branch.

The example pairs `terraform_version: 1.15.5` with
`terraform_image: hashicorp/terraform:1.15.5@sha256:15bf5a08b1fb9c9747c8ff01098aeeefb4aec9a6c24eb13e7661bdf9447e4aee`.
The validation supervisor verifies `terraform version -json` inside that image before applying
target content and rejects a mismatch.

Production and non-production callers demonstrate separate config paths, prefix lists, schedules,
concurrency groups, and tool versions. Their illustrative allow-lists are:

```text
# Non-production
state/nonproduction/
state/staging/
aws-state/nonproduction/

# Production
state/production/
aws-state/production/
```

## Branch discovery and validation

Discovery verifies that the caller ref is the default branch and records `github.sha` as the
immutable control OID. It checks out that exact commit with persisted credentials disabled,
downloads and verifies the pinned release archive, and validates the shared configuration once
against a trusted temporary Terraform fixture before creating any branch matrix. A malformed
config is therefore one workflow failure rather than one issue per branch.

Discovery lists remote heads and OIDs from `origin`; it does not depend on whatever subset
`actions/checkout` fetched. Because persisted checkout credentials are disabled, the workflow
supplies its read-only token to that exact Git subprocess through a temporary `GIT_ASKPASS` helper.
The helper contains no token, the token is absent from the URL and process arguments, terminal
prompts are disabled, and the helper is removed unconditionally. A state branch matches when its
complete name begins with one of the configured literal prefixes. The remainder of the branch name
is unrestricted except by Git's normal ref-name rules.

The discovery script:

1. Rejects an empty allow-list, empty entries, absolute-looking values, control characters, and
   values that are not valid literal branch prefixes.
2. If `branch_prefix` is supplied, verifies that it begins with at least one allowed prefix.
3. Selects remote branches using the allow-list plus optional narrowing prefix.
4. Deduplicates branches selected by overlapping prefixes.
5. Records each branch's exact remote tip OID and sorts names bytewise for deterministic output.
6. Fails when no branches match and when more than GitHub's 256-job matrix limit match, with a
   diagnostic instructing the caller to narrow or partition its prefix policy.
7. Encodes the run ID, run attempt, automation policy ID, control OID, and final branch/OID/ref-hash
   triples as JSON for the matrix without evaluating branch names as shell code. The ref hash is the
   lowercase hexadecimal SHA-256 of the complete branch ref and is used for artefact and concurrency
   keys; the branch name itself is never interpolated into those expressions.

The manual prefix can narrow `state/nonproduction/` to `state/nonproduction/example-`, but a
non-production caller rejects `state/production/`.

Every value originating in a workflow context, input, branch name, manifest, or target checkout is
untrusted data. Shell steps receive those values through `env:` only; direct `${{ ... }}`
interpolation into `run:` source is forbidden. Scripts double-quote expansions, use `--` and full
refspecs for operands, consume NUL-delimited Git output, create JSON with `jq --arg`, and create PR
and issue bodies through files. Branch names are never evaluated as code. Dynamic values rendered
for GitHub Markdown are HTML-escaped, wrapped in safe `<code>` or `<pre>` elements, and have `@`
neutralised before insertion so target-controlled values cannot create mentions, links, HTML, or
misleading structure.

All repository-relative paths are canonicalised beneath their checkout roots. The workflow rejects
absolute paths, `..` traversal, missing config files, missing Terraform directories, duplicate
canonical directories, and directories that resolve outside the target checkout. Nested module
directories are allowed, but each directory processes only the `.tf` files directly within it.

Before either preparation or verification invokes `tf-version-bump`, the script enumerates that
directory's matching files, uses `lstat` to reject symlinks and non-regular files, and verifies each
canonical path remains under the target checkout. Required lock files receive the same checks.
Containment is repeated for every changed and untracked path before a preparation bundle or commit
is created. Repository `.terraform` paths are rejected. Every Terraform invocation uses a fresh,
absolute `TF_DATA_DIR` created under a trusted runner temporary directory, never the target
checkout, so a tracked `.terraform` symlink cannot redirect initialisation writes.

## Per-branch preparation and validation flow

Each unprivileged preparation job performs the following sequence:

1. Check out the control OID and discovered state-branch OID with persisted credentials disabled.
2. Install the exact released `tf-version-bump` and Terraform versions supplied by the caller and
   verify their reported versions. The released CLI must be the same artefact exercised by tests.
3. Validate directory containment and reject symlinked or non-regular candidate files before any
   write, including any repository `.terraform` path.
4. For every configured Terraform directory, run `tf-version-bump` from that directory with the
   control config and the non-recursive pattern `*.tf`.
5. Create a fresh absolute `TF_DATA_DIR` under the trusted runner temporary directory and, for that
   same directory, run with a 20-minute command timeout:

   ```text
   terraform -chdir=<directory> init -upgrade -backend=false -input=false -no-color
   ```

6. After successful initialisation, inspect the trusted data directory's provider package tree
   before removing it. If initialisation installed any provider package, require a regular
   `.terraform.lock.hcl`, including a newly created untracked file. If it installed none, record the
   directory as provider-free and allow the lock file to be absent. If a new required lock file is
   ignored, fail with a repository-configuration error; never force-add it.
7. Remove disposable `.terraform/` working data, then consume NUL-delimited Git status output and
   confirm that every changed `.tf` file is directly inside exactly one configured directory and
   every other change is that directory's lock file. Any other path fails the safety check.
8. Produce the versioned manifest and patch, verify their hashes locally, upload the immutable
   candidate bundle under a name containing the run ID, run attempt, policy ID, and
   collision-resistant branch hash, and remove the checkouts and trusted data directories.
   Terraform initialisation downloads provider packages but does not execute provider validation
   RPCs, so this captures the publishable candidate before any provider plugin executes.

A fresh unprivileged validation job downloads only its exact run-attempt candidate, validates the
manifest, paths, modes, hashes, refs, OIDs, policy, and run identity, and applies the patch to a
fresh exact-base validation checkout. The host creates a trusted data directory and starts the
pinned Terraform image with `--rm`, no Docker socket, all capabilities dropped,
`no-new-privileges`, bounded processes/memory/CPU, the runner UID/GID, and only the disposable
validation checkout and trusted data directory mounted. It passes no host environment variables
except explicit non-secret Terraform settings. For each directory with provider selections, the
container runs under a 20-minute host timeout:

```text
terraform -chdir=<directory> init -backend=false -input=false -no-color -lockfile=readonly
terraform -chdir=<directory> validate -no-color
```

For a manifest-declared provider-free directory it omits `-lockfile=readonly` because no lock file
exists, then runs the same validation command. Neither the candidate bundle nor the host outcome
directory is mounted into the container. After the container stops, the trusted host supervisor
creates the outcome from the observed exit status, then uploads only that candidate-bound outcome
and captured logs. A timeout is `branch-validation`. The validation job never uploads source or
lock-file bytes and discards its checkout and data directory. Provider execution therefore cannot
alter the immutable candidate, host outcome, or Actions control plane later used for publication.

This boundary authenticates that the supervised Terraform process returned zero; it does not prove
that an adversarial provider truthfully implemented schema or configuration validation. That
semantic limitation appears in the PR body and consumer documentation.

`tf-version-bump` continues processing later files after a per-file parse, stat, read, or write
error, but accumulates those errors and exits non-zero after processing completes. Its output ends
with a deterministic aggregate failure count. This intentionally changes the prior unsafe exit
status contract; automation never parses human prose to infer success.

Each configured directory is a Terraform module validation unit. Running the bump with `*.tf`
ensures every changed source file is loaded by the immediately following init/validate commands.
Consumers list nested modules explicitly. Duplicate directories are rejected, so a changed source
belongs to exactly one unit.

## Verification and privileged publication flow

A non-dry run starts fresh verification and publication matrices after all preparation and
validation jobs have uploaded their artefacts. Reconciliation has three explicit phases across two
job boundaries:

1. **Verify and stage in a credential-free job.** With `contents: read`, no publication environment,
   and no write token or protected secret, download only the exact run-attempt
   preparation bundle and applicable validation outcome; validate their manifests, paths, modes,
   hashes, run identity, policy, control OID, branch, validated base OID, and candidate binding.
   Check out the exact control and base OIDs with persisted credentials disabled, repeat
   path/symlink preflight, independently rerun the released `tf-version-bump` for each configured
   directory, require its `.tf` diff to match, apply only declared lock-file bytes, verify the full
   result, and upload a run-attempt-bound verified-result artefact that contains no token or key
   material.
2. **Authenticated prepublication in a protected job.** On a fresh runner, download and validate
   the verified-result identity without executing or sourcing it, then mint the App token when
   configured or select the built-in token. Supply the selected token only to exact Git/GitHub
   commands through a temporary credential file and command-scoped environment. Fetch the current
   state and automation refs and query exact PR/issue records. Verify the state OID, policy-scoped
   ownership, PR marker, issue author identity, and expected automation-ref OID. No signing key is
   materialised.
3. **Publish in the same protected job.** Materialise the signing key only after prepublication
   succeeds, create and locally verify the commit, then use one authenticated atomic Git push
   containing a no-op state-ref refspec guarded by an exact state-ref lease and the automation-ref
   create/update guarded by its own exact lease. This makes the state/base comparison atomic with
   the update-ref transaction. Create or refresh the PR, reconcile the marked issue, remove every
   credential/key helper in unconditional cleanup, and return the result status.

For example, `state/nonproduction/example-thing` maps to
`update_state/nonproduction/example-thing`. State branches are read-only bases. The `update_`
namespace is reserved for this automation, but naming alone never grants ownership.

An automation commit carries exact machine-readable trailers identifying the workflow, state
branch, automation policy ID, validated base OID, control OID, run ID, and run attempt. A managed PR
carries a corresponding policy-scoped hidden marker. Before rewriting or deleting an existing
automation ref, reconciliation requires both its tip commit marker and the corresponding marked PR,
open or closed, to agree. An unmarked, different-policy, or mismatched existing ref is an automation
failure and is never adopted. A marked issue is edited or closed only when its immutable author
login equals the selected built-in/App bot identity.

Git authentication never relies on checkout persistence or on Git interpreting `GH_TOKEN`.
Discovery and publication create a token-free `GIT_ASKPASS` helper and a mode-`0600` token file only
for their exact Git commands; the token is absent from remotes and command arguments, prompts are
disabled, and both files are removed unconditionally. GitHub CLI commands receive `GH_TOKEN` only
in their own child environment.

The Git ref transaction guarantees that the state ref equalled the validated base at the instant
the automation ref was created or updated. GitHub PR API calls are not part of that transaction.
Reconciliation rechecks the state ref after the push and after PR mutation; detected movement causes
an exact-lease rollback of the automation ref written by the current invocation and closes or
restores the managed PR as applicable. State movement after the final check is normal concurrent
repository activity and is corrected by the next queued run; the documentation does not claim
strict atomicity across Git and GitHub APIs.

Every mutation boundary has compensation. If initial PR creation fails after creating the ref, the
workflow exact-lease-deletes that ref. If refreshing an existing PR fails after updating its ref, it
exact-lease-restores the previous ref OID. Ambiguous API responses are reconciled by re-reading exact
markers before compensation. A compensation lease failure never triggers an unconditional retry;
it produces an automation error with explicit manual recovery details. Failpoint integration tests
cover ref create/update, PR create/edit/close, and ref deletion.

When a successful non-dry run produces no changes, the workflow closes the marked automation PR as
obsolete and deletes only its verified automation branch using an atomic no-op state ref plus an
expected-old-OID deletion lease. A lease mismatch fails without an unconditional retry. Deletion is
performed before PR closure so a failed delete leaves the recoverable marked PR/branch pair intact;
an API failure after deletion is recovered by locating and closing the marked PR on the next run.
It also closes any open marked validation-failure issue because the branch completed the update and
validation flow.

## Dry-run semantics

Dry-run mode is a repository/API side-effect boundary, not a read-only filesystem mode. The
workflow still:

- Runs `tf-version-bump` without the CLI's `-dry-run` flag.
- Runs real Terraform initialisation and validation.
- Creates or updates dependency lock files for roots with provider selections in the disposable
  checkout; provider-free roots may remain without one.
- Performs the allowed-path safety check.
- Uploads diagnostic preparation and outcome artefacts and reports proposed changes and failures in
  workflow output.

It never enters the protected publication environment and never commits, pushes, changes GitHub
repository content, creates or edits a pull request, creates or edits an issue, closes an issue or
pull request, or deletes an automation branch. GitHub's unavoidable workflow run, logs, summaries,
and explicitly uploaded diagnostic artefacts are permitted. This validates the proposed result
rather than the branch's old contents.

## Commit and pull-request lifecycle

The stable commit subject is:

```text
chore: apply tf-version-bump config
```

The pull request identifies the state branch and is looked up by exact base and head refs rather
than title alone. Its body is refreshed on every run with:

- Automation policy ID.
- State-branch base revision.
- Immutable control revision.
- Workflow run ID and attempt.
- Config path.
- Released `tf-version-bump`, release archive digest, Terraform version, and validation image
  digest.
- Changed files.
- Initialised and validated Terraform directories.
- Workflow-run link.
- A statement that the branch is automation-owned and may be regenerated.
- A statement that success records Terraform's supervised exit status and cannot prove that a
  malicious provider truthfully implemented validation.

Every dynamic value uses the shared Markdown encoder: escape `&`, `<`, and `>`, neutralise `@` with
a zero-width separator, and render scalar values in `<code>` and log tails in bounded `<pre>`
blocks. Machine markers contain only fixed syntax plus validated policy IDs and hexadecimal hashes.

The workflow recreates the automation commit from the latest state-branch tip instead of
accumulating obsolete generated commits or rebasing them indefinitely. One atomic push guards the
unchanged state ref and stable automation ref with their exact expected OIDs after ownership markers
pass. A lease mismatch or detected state-base movement fails as an automation error and does not
retry with an unconditional force. Branch deletion uses the same atomic state guard and
expected-old-OID protection. GitHub API failures use the compensation rules above.

## Failure classification and issues

Failures from these stages are state-branch failures eligible for one reconciled issue:

- `tf-version-bump`
- `terraform init`
- `terraform validate`

The control config is validated before matrix creation, so a shared config failure stops discovery
without branch issues. An init issue states that the cause may be branch configuration or a
transient registry, authentication, rate-limit, network, or other external dependency; a later
successful non-dry-run closes it through the normal recovery lifecycle. Missing/ignored locks,
unsafe paths, manifest failures, ref movement, and ownership or lease failures are automation or
repository-policy failures rather than update/init/validation issues.

A non-dry run creates or updates one open issue per update/init/validation failing state branch. The
issue is identified by an exact stable title and a machine-readable marker, not a fuzzy title
search. Its body is replaced with the latest:

- State branch and base revision.
- Failing stage and Terraform directory, when applicable.
- Executed command without secret values.
- Concise tail of captured standard output and standard error.
- Workflow-run and uploaded-log links.

Full captured logs are uploaded as a workflow artefact with a collision-resistant name derived
from the run ID, run attempt, policy ID, and hash of the complete branch ref. The issue body uses the
shared Markdown encoder and is bounded so recurring scheduled failures do not grow without limit. A
successful later non-dry run closes the issue with a recovery comment, but only after verifying the
marker, policy, and immutable bot author login.

When a newer control or base revision produces an update/init/validation failure,
reconciliation closes the previous marked PR as obsolete, deletes its verified automation branch
with an expected-OID lease, and then creates or updates the failure issue. A stale previously
successful PR is never left open and apparently mergeable after a newer branch failure.
The init issue makes any possible transient cause explicit rather than leaving an older proposal
open as though the latest run had validated it.

Authentication, GitHub API, invalid input, unsafe diff, signing, commit, push, lease, PR, issue
reconciliation, and workflow configuration failures are automation failures. They fail the matrix
job without creating a misleading state-validation issue. The candidate manifest and its bound
validation outcome together carry an explicit result class with precedence `automation`,
`branch-update`, `branch-init`, `branch-validation`, then `success`; reconciliation rejects unknown,
mismatched, or contradictory classes. A supervised validation command timeout is
`branch-validation`; a job-level timeout or container-supervisor failure is `automation`.
`fail-fast: false` ensures other state branches continue.

## Authentication and permissions

The caller sets the reusable workflow's permission ceiling according to its authentication mode:

- Built-in-token callers grant `contents: write`, `pull-requests: write`, and `issues: write`.
- GitHub App callers grant only `contents: read`; unspecified scopes remain `none`.

Reusable workflows can only maintain or reduce the caller's permission ceiling. The reusable
workflow declares narrower permissions per job:

- Discovery, preparation, validation, and verification: `contents: read` only.
- Publication: `contents: write`, `pull-requests: write`, and `issues: write`.

The publication declaration supports built-in-token callers, but an App caller's read-only ceiling
keeps the effective publication `GITHUB_TOKEN` read-only. Every checkout sets
`persist-credentials: false`. The built-in publication path makes `GITHUB_TOKEN` available only to
authenticated prepublication/publication steps and their exact Git/GitHub CLI children. It is not
present during candidate verification or source reproduction. The App publication path never
passes `GITHUB_TOKEN` to a write operation; it uses only the short-lived App token. Consumers using
the built-in token must enable the repository setting that permits GitHub Actions to create pull
requests.

Current GitHub.com recursion protection distinguishes the events created with the built-in token.
Resulting `push` events do not create workflow runs. Pull-request `opened`, `synchronize`, and
`reopened` events do create workflow runs, but those runs wait for approval from a repository
writer. Other pull-request activity types do not create runs. GitHub Enterprise Server versions
can retain the older rule that suppresses these pull-request runs as well, so consumers must verify
their installed version rather than assume GitHub.com behaviour. Built-in-token mode is therefore
suitable only when manual approval of generated PR checks is acceptable on the target platform.
Consumers whose required checks must run unattended may use App mode only after satisfying and
acknowledging the downstream-check contract; otherwise they retain approval or use an explicit
consumer-managed `workflow_dispatch` after review. App authentication changes event delivery, not
the trustworthiness of downstream workflow code.

When the App client ID and protected-environment private key are supplied, publication uses the
GitHub-maintained App-token action to mint a short-lived installation token limited to the current
repository and the same three explicit permissions. It obtains the App bot's user ID through the
GitHub API and constructs the correct bot name and noreply address for commit attribution.
App-authenticated PR activity allows audited PR checks to run without the built-in token's approval
gate. The App key is not referenced, passed, or materialised in discovery, preparation, validation,
or verification.

Every referenced action is pinned to an immutable commit SHA with a version comment, matching the
repository's existing workflow convention.

## Optional SSH commit signing

When `TF_VERSION_BUMP_COMMIT_SIGNING_PRIVATE_KEY` is absent from the protected publication
environment, the workflow creates the correctly attributed unsigned bot or App commit.

When it is present, the workflow:

1. Requires explicit `commit_author_name` and `commit_author_email` values.
2. Writes the private key under the runner's temporary directory with mode `0600` without printing
   it.
3. Configures SSH signing only in the target repository: `gpg.format=ssh`, the temporary key path,
   and `commit.gpgsign=true`.
4. Derives the public key with `ssh-keygen` and constructs a temporary allowed-signers file for the
   configured author email.
5. Creates the commit with an explicit signature and verifies it locally before pushing.
6. Removes the private key and allowed-signers file in an unconditional cleanup step.

Signing and verification failure stops reconciliation. There is no unsigned fallback. The key is
materialised only after the manifest, ownership, base OID, source diff, and lock-file payload pass
validation. The documentation states that the signing key must work non-interactively and that its
public key must be registered as a signing key on the GitHub account associated with the author
email for GitHub to show the commit as verified.

## Multiple Terraform root modules

The example callers use `terraform_directories: .`. A consumer with multiple root modules can pass
newline-separated paths such as:

```text
environments/network
environments/platform
```

`tf-version-bump` runs separately in each configured directory with the direct-file pattern
`*.tf`. Terraform initialisation and validation immediately follow in that same directory. All
modules must pass before the workflow publishes any commit. A deterministic failure issue names
the affected module.

The initial example does not run root modules in parallel within a state-branch job. Branch-level
matrix parallelism already provides concurrency, while sequential roots keep output and failure
attribution straightforward.

## Testing strategy

Implementation follows test-driven development.

### Local integration tests

A maintained shell test creates temporary real Git repositories and bare remotes. It exercises:

- Prefix allow-lists and manual narrowing.
- Rejection of manual widening.
- Overlapping-prefix deduplication and deterministic ordering.
- Controlled discovery at 256 matches and a clear pre-matrix failure at 257 matches.
- Branch names containing slashes.
- Valid hostile ref names containing shell metacharacters, quotes, leading dashes, Unicode, and
  percent signs without evaluating them as commands.
- No-match and invalid-path failures.
- Rejection of manual dispatch from a non-default ref.
- One immutable control OID across a default-branch movement.
- Exact discovered base OIDs and rejection when a state ref advances before publication.
- Successful update, initialisation, and validation with the independently downloaded
  `v1.0.0-rc.7` release artefact and the digest-pinned Terraform image used by the workflows.
- One-to-one mapping of directly changed source files to configured Terraform directories,
  including rejection of omitted, duplicate, and ambiguous roots.
- Terraform source and lock-file staging into an immutable candidate before provider execution.
- Provider-free root success without `.terraform.lock.hcl`, alongside rejection of a missing lock
  when initialisation installs a provider package.
- Multi-directory success and failure attribution.
- Missing, duplicate, path-traversing, symlink-mode, hash-mismatched, and ref-mismatched result
  bundles are rejected by fresh reconciliation.
- Dry-run isolation.
- Rejection of unexpected changed files.
- Pre-write rejection of Terraform/lock symlinks and repository `.terraform` paths, including
  symlinks at each ancestor that could escape into the control checkout or elsewhere on the
  runner. Terraform uses a fresh absolute trusted `TF_DATA_DIR` for every root.
- Stable automation-branch regeneration, ownership-marker validation, unowned-ref refusal, and
  explicit leases for updates and deletion races. A branch marked for a different policy is
  foreign and fails safely even though the required ref name remains `update_<state-branch>`.
- Atomic publication tests move the state ref at the push boundary and prove that a no-op state-ref
  update with an exact lease prevents stale update-ref publication.
- Failpoint tests after ref creation or update, PR creation or edit, PR close, and ref deletion
  prove the documented exact-lease compensation and retry behaviour.
- Malformed shared-config failure before matrix creation.
- Branch-issue classification of a real init/download failure with bounded diagnostics.
- Success followed by a new deterministic failure closes the stale marked PR and safely removes
  its owned update branch.
- Signed commits, local signature verification, cleanup of key material, and signing failure.
- A hostile purpose-built Terraform provider fixture runs in the same constrained container
  boundary as production. It attempts delayed writes after parent exit, credential discovery,
  candidate and lock-file replacement, workflow-command poisoning, outcome forgery, and container
  escape. Host-side tests verify that it cannot alter the immutable candidate or trusted outcome
  and that the host records the observed process status. These tests do not claim that a malicious
  provider must report truthful semantic validation diagnostics. The repository test harness builds
  the reviewed Linux fixture, places it in a trusted read-only filesystem mirror, and supplies a
  host-created Terraform CLI configuration only in test mode; neither the mirror nor this test-only
  configuration is selectable from state-branch content or reusable-workflow inputs.
- A hanging provider is terminated at the configured timeout and produces the validation-timeout
  classification without unbounded runner use.
- Artefact names and manifests bind run ID, run attempt, policy ID, control OID, state ref, and base
  OID. Full-run and failed-job re-run cases cannot consume another attempt's artefacts.
- The command-scoped Git authentication helper works against a real authenticated bare HTTP test
  service without storing a token in a remote URL or process argument and removes its temporary
  files after each command.
- Dynamic PR and issue text neutralises mentions and HTML/Markdown control, and renders bounded
  diagnostics without allowing attacker-controlled presentation.
- The credential-free verification job reaches a complete verified result without an App token or
  signing key. The protected publication job refuses to mutate when either selected credential is
  unavailable.
- Unsigned operation when no signing key is configured.
- Obsolete no-change branch and PR payload generation.
- Clear failure when a newly required provider lock file is ignored; the workflow never force-adds
  it.

The tests do not mock `gh` or assert against invented GitHub API behaviour. Deterministic PR and
issue payload construction is tested locally. Actual PR, issue, token, and permissions behaviour is
covered by the real-service acceptance run.

### Workflow validation

The project adds one repository-owned launcher that runs exactly
`go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12`; the local harness, Make target, CI, and
final verification all use that launcher for the three example workflow files. That parser predates
GitHub.com's supported `queue` property, so the launcher applies only the exact
`-ignore '^unexpected key "queue" for "concurrency" section\.'` suppression. It suppresses no other
diagnostic. Validation copies the example `.github` tree into a temporary consumer-style repository
root first, so local reusable-workflow paths resolve exactly as they do after installation. It also
runs `bash -n`, verifies that Docker is available for the container-boundary tests, and runs the
integration tests for both helper scripts. Existing Go tests, race detection, branch-automation
tests, linting, and build validation remain required and must produce pristine output.

Each behaviour is introduced through its own focused failing test and smallest implementation,
followed by the focused test again. A missing script or mode is used only to establish the initial
scaffold, not as the failing reason for a batch of unrelated assertions.

After implementation and TDD finish, a separate test-cleanup pass reviews new tests for duplicated
or low-value coverage.

### Real-service acceptance

The example README defines an acceptance procedure using a disposable GitHub repository with:

- One matching non-production state branch.
- One non-matching production branch.
- A provider-using root with a committed lock file, a provider-free root without one, and a
  default-branch config covering both.
- A default-branch-restricted publication environment.
- A built-in-token run and an App-mode run made only after the repository's downstream workflows
  satisfy the documented unattended-check contract.
- An App-mode run whose job permission summary shows `contents: read` and no write scopes, and whose
  publication succeeds with the short-lived App token, proving `GITHUB_TOKEN` is not the write
  credential.
- An alternate-ref manual dispatch that cannot enter the protected publication job or read its
  secrets.
- The actual GitHub-hosted validation runner executes the hostile fixture in the digest-pinned
  container through the repository harness's reviewed read-only filesystem mirror and records its
  mount, environment, permission, delayed-write, and timeout results. This is a CI boundary test,
  not a state-branch-selectable production input. The fixture has no write/App/signing credentials,
  workflow-command files, control checkout, immutable candidate, trusted outcome path, or container
  socket available to it.
- App mode is rejected unless `unattended_checks_safe: true`. The accepted App case uses reviewed
  default-branch workflows with a read-only token, no secrets or privileged environments, and an
  isolated target-code step. A hostile downstream test cannot mutate repository content or read a
  secret sentinel.
- Private-repository discovery, fetch, update, and deletion using command-scoped authentication.
- Successful update, repeated idempotent update, validation failure, issue update, recovery, and
  obsolete-PR cleanup.
- Three rapid dispatches demonstrating that `queue: max` preserves and eventually executes all
  three runs rather than replacing either pending run.
- A built-in-token PR whose `push` workflows remain absent and whose `pull_request` checks wait for
  writer approval, followed by an App-mode PR whose checks start without that approval gate.
- A dry run that demonstrates no repository refs, content, PRs, or issues changed while diagnostic
  logs and artefacts remain available.
- An unowned update-ref collision and concurrent update/deletion races that preserve the remote
  ref.
- A state-base movement that prevents stale publication.
- Full-workflow and failed-job re-runs whose artefacts remain isolated by run attempt.
- A policy collision that refuses to adopt or overwrite the foreign marked ref and PR.
- A built-in-token run with repository PR creation temporarily disabled, demonstrating that a real
  post-push PR-creation rejection triggers exact-lease rollback without deleting an unseen remote
  update. The remaining mutation-boundary compensation paths are covered by the local state-machine
  and real-Git failpoint tests rather than an invented GitHub API mock.
- An optional signed run whose pushed commit is locally verified and shown as verified by GitHub
  when the signing identity is configured correctly.

The acceptance transcript is reviewed before completion is claimed.

## Documentation changes

`examples/github-actions/README.md` documents:

- Files to copy and paths to customise.
- Repository settings and least-privilege permissions.
- Protected publication-environment setup, default-branch restrictions, trusted-dispatch
  assumptions, and fixed optional secret names.
- Exact tool-version pinning.
- The prerequisite `v1.0.0-rc.7` release, its aggregate-failure contract, and verification of the
  downloaded artefact's checksum and provenance before use.
- Production and non-production prefix/config separation.
- Manual narrowing and dry-run behaviour.
- Built-in-token limitations and optional GitHub App configuration.
- The `automation_policy_id` ownership boundary and the explicit `unattended_checks_safe`
  downstream-workflow contract required for App mode.
- Unsigned and signed commit setup.
- Stable branch, PR, issue, and cleanup lifecycles.
- Reserved namespace, ownership markers, immutable control/base OIDs, and lease failure recovery.
- Run-ID and run-attempt artefact provenance, command-scoped private-repository Git authentication,
  publication compensation, and the remaining non-transactional GitHub-API race.
- Single-root and multi-root configurations.
- Direct-file-per-module processing and the requirement to list every nested module explicitly.
- Backend-disabled Terraform initialisation.
- Digest-pinned constrained Terraform execution, trusted `TF_DATA_DIR`, command and job timeouts,
  host-observed process status, and the limit that a malicious provider can lie about semantic
  validation results even though it cannot alter the publishable candidate or obtain credentials.
- Branch-scoped update, init, and validation issues, including possible transient causes in init
  diagnostics, versus automation failures that never create issues.
- Required provider lock files, valid provider-free roots without locks, and rejection of ignored
  required locks and symlinked Terraform/lock paths.
- GitHub.com's `queue: max` behaviour, its 100-pending-run bound, and the need for GitHub Enterprise
  Server consumers to confirm support before installation.
- The 256-branch matrix ceiling and the controlled failure when an allow-list exceeds it.
- Built-in-token event suppression and approval-required PR checks, including when App mode is
  required for unattended checks.
- Manual identification and cleanup of strongly marked orphaned automation refs and issues when a
  state branch is deleted, renamed, or removed from the allow-list. Automatic orphan garbage
  collection is intentionally deferred until operational need justifies a separate destructive
  workflow; a future cleanup workflow must default to dry run.
- Troubleshooting and real-service acceptance.

`examples/README.md` and `docs/ADVANCED-USAGE.md` link to the GitHub Actions example and distinguish
it from the local sequential `update-branches.sh` workflow. The root README remains concise.

## Completion criteria

The feature is complete when:

- The three copyable workflows and two documented scripts implement this design.
- Non-production and production examples have manual and timezone-aware scheduled triggers.
- Dry runs perform real local updates and validation without repository or GitHub content mutation;
  workflow logs and diagnostic artefacts are retained.
- Terraform executes only in credential-free jobs with `contents: read`, the immutable publishable
  candidate is uploaded before provider execution, credential-free verification runs in a separate
  read-only job, and privileged publication runs on a fresh protected runner without executing
  target-supplied code.
- App-mode publication has an effective read-only `GITHUB_TOKEN`; only its short-lived App token
  can publish repository content, refs, PRs, or issues.
- Untrusted refs and inputs remain data across Actions, shell, Git, JSON, Markdown, and artefact
  boundaries.
- Normal runs safely reconcile signed or unsigned commits, stable automation branches, PRs, and
  failure issues.
- Every run pins one control OID and one validated base OID per state branch; ref movement blocks
  publication.
- Existing update refs require verifiable ownership, and every rewrite or deletion uses an exact
  expected remote OID.
- The independently downloaded `v1.0.0-rc.7` CLI reports aggregate per-file failure with a non-zero
  status, and the workflow downloads and verifies the recorded SHA-256 of that exact released
  archive before use.
- Every configured Terraform module directory with installed provider packages has a lock file
  after initialisation; new and changed required lock files are staged unless ignored (which is a
  clear failure), provider-free directories may omit the file, disposable `.terraform/` data is
  excluded, and symlinked, escaping, non-regular, or otherwise unexpected paths are rejected before
  writing.
- GitHub.com caller and publication concurrency use the supported `queue: max` behaviour so pending
  runs are preserved. The pinned actionlint launcher suppresses only its exact stale-parser
  diagnostic for that property.
- Built-in-token documentation and acceptance distinguish suppressed `push` workflows from
  approval-required PR workflows, and require App mode for unattended required checks.
- Every changed Terraform source file belongs directly to exactly one configured and validated
  module directory.
- Local integration tests, pinned `actionlint`, the repository's full validation suite, and the
  disposable-repository acceptance procedure all pass with pristine output.
- User-facing documentation describes configuration, permissions, security boundaries, and
  recovery behaviour without relying on unstated setup.
