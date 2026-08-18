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
- Maintain stable update branches, pull requests, and validation-failure issues without creating
  duplicates on scheduled runs.
- Support a dry run that still applies and validates proposed changes in a disposable checkout
  while suppressing repository refs and GitHub content mutations.
- Support the built-in `GITHUB_TOKEN`, optional GitHub App authentication, and optional SSH commit
  signing.
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
failure logs for diagnosis. They expose separate validation and reconciliation operations so the
privileged job never executes target-branch Terraform.

## Workflow architecture

The production and non-production callers own policy. Each caller defines its allowed prefixes,
configuration path, pinned tool versions, schedule, and concurrency group, then invokes the local
reusable workflow.

The reusable workflow contains four job stages:

1. `discover` checks out the exact caller commit, validates the shared configuration, and produces
   a sorted JSON matrix containing each matching state branch and its immutable tip OID.
2. `prepare` runs once per matrix entry with `fail-fast: false`. It applies the update and runs
   Terraform initialisation on an unprivileged fresh runner, then uploads a preparation bundle
   before any provider plugin executes. A successful bundle contains the immutable candidate.
3. `validate` runs once per successful candidate on another unprivileged fresh runner. It consumes
   the candidate without changing its publishable payload, executes the target-selected providers,
   and uploads only a candidate-bound outcome and captured logs.
4. `reconcile` runs once per matrix entry on another fresh runner after the complete validation
   matrix. It treats both artefacts as untrusted data, revalidates them, and either publishes the
   pre-provider candidate, reports a deterministic branch failure, or records a workflow-level
   failure.

Preparation captures update and initialisation status and uploads its bundle in unconditional
cleanup steps before returning the recorded failure status. Validation does the same for its
candidate-bound outcome and logs. Reconciliation uses an `always()` condition so one failed matrix
entry does not suppress reporting for the others. It expects one preparation bundle for every
entry, and a validation outcome only when preparation produced a successful candidate. A missing,
duplicate, unexpected, or mismatched artefact is an automation failure and can never be interpreted
as success.

The update matrix defaults to four concurrent branches. This bounds Terraform downloads and GitHub
API writes while retaining useful parallelism. A caller can lower or raise the limit through the
reusable workflow's `max_parallel` input.

The validation jobs have `contents: read`, use `persist-credentials: false` for every checkout, and
never receive a write token, GitHub App private key, or commit-signing key. Each validation job uses
two independent checkouts:

- A control checkout of the immutable caller SHA supplies the reviewed scripts and
  `tf-version-bump` configuration.
- A target checkout of the immutable discovered state-branch OID receives the proposed changes.

Terraform can execute target-selected provider plugins during validation. Therefore checkout
separation is not treated as a process-security boundary. Preparation uploads the immutable source
and lock-file candidate before provider execution, and validation cannot replace that artefact. The
fresh reconciliation job is the publication boundary: it does not execute Terraform or any
target-supplied executable, and it materialises write credentials and signing material only after
validating the candidate and outcome.

A successful preparation bundle contains a versioned JSON manifest, captured preparation logs, and
a binary Git patch. The manifest records the control OID, state branch, validated base OID,
preparation classification, configured Terraform directories, provider-dependency state, changed
paths, file modes, and SHA-256 hashes. A failed preparation bundle contains its classification and
logs but no publishable patch. The validation outcome records the candidate manifest digest,
validation classification, and captured validation logs. Reconciliation rejects unknown manifest
versions, mismatched refs, OIDs, or candidate digests, absolute or escaping paths, symlink or
non-regular modes, unexpected file types, hash mismatches, and patch paths outside the declared set.
It applies the patch only after a fresh exact-base checkout and independently reproduces the
`tf-version-bump` source changes before accepting lock-file changes from the pre-provider candidate
bundle.

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

Each caller has its own concurrency group with `queue: max` and no in-progress cancellation. Up to
100 runs wait behind the active run instead of replacing the existing pending run. Production and
non-production can run independently. This is current GitHub.com syntax; GitHub Enterprise Server
consumers must confirm that their installed version supports the `queue` property before copying
the example.

Manual runs are accepted only when `github.ref` is the repository's default branch. This catches
accidental alternate-ref dispatches. The reconciliation job also uses a required protected GitHub
environment restricted to the default branch. App and signing private keys live only in that
environment; they are not passed through `workflow_call`. Repository administrators must limit
manual dispatch to trusted actors. Built-in-token mode additionally assumes repository write
access is trusted because a writer can submit modified workflow YAML even when no optional secret
is exposed. App-mode callers cap the reusable workflow's `GITHUB_TOKEN` at `contents: read`; only
the short-lived App token receives publication permissions.

## Reusable workflow interface

| Input | Type | Required/default | Purpose |
|---|---|---|---|
| `allowed_branch_prefixes` | string | Required | Newline-separated literal prefix allow-list |
| `branch_prefix` | string | `""` | Optional manual narrowing prefix |
| `config_path` | string | Required | Config path in the default-branch control checkout |
| `terraform_directories` | string | `.` | Newline-separated module directories processed independently |
| `terraform_version` | string | Required | Exact Terraform version installed for the run |
| `tf_version_bump_version` | string | Required | Exact `tf-version-bump` release tag used by `go install` |
| `go_version` | string | Required | Exact Go version used by `go install` |
| `dry_run` | boolean | `false` | Suppress repository refs and GitHub content mutations |
| `max_parallel` | number | `4` | Maximum concurrent state-branch jobs |
| `commit_author_name` | string | `""` | Explicit signed-commit identity; derive bot name when unsigned |
| `commit_author_email` | string | `""` | Explicit signed-commit identity; derive bot email when unsigned |
| `github_app_client_id` | string | `""` | Enable GitHub App authentication when paired with its key |
| `publication_environment` | string | Required | Default-branch-restricted environment for reconciliation |

Optional protected-environment secrets with fixed names are:

- `TF_VERSION_BUMP_GITHUB_APP_PRIVATE_KEY`
- `TF_VERSION_BUMP_COMMIT_SIGNING_PRIVATE_KEY`

The workflow rejects partial authentication or signing configurations. A GitHub App client ID
requires the App private key in the protected environment. When the signing key is present, the
caller must explicitly supply both commit identity inputs rather than inherit a bot default.
Environment secrets cannot be passed or selected by an untrusted caller.

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
installs the pinned CLI, and validates the shared configuration once against a trusted temporary
Terraform fixture before creating any branch matrix. A malformed config is therefore one workflow
failure rather than one issue per branch.

Discovery lists remote heads and OIDs from `origin`; it does not depend on whatever subset
`actions/checkout` fetched. A state branch matches when its complete name begins with one of the
configured literal prefixes. The remainder of the branch name is unrestricted except by Git's
normal ref-name rules.

The discovery script:

1. Rejects an empty allow-list, empty entries, absolute-looking values, control characters, and
   values that are not valid literal branch prefixes.
2. If `branch_prefix` is supplied, verifies that it begins with at least one allowed prefix.
3. Selects remote branches using the allow-list plus optional narrowing prefix.
4. Deduplicates branches selected by overlapping prefixes.
5. Records each branch's exact remote tip OID and sorts names bytewise for deterministic output.
6. Fails when no branches match so a configuration mistake cannot look successful.
7. Encodes the control OID and final branch/OID pairs as JSON for the matrix without evaluating
   branch names as shell code.

The manual prefix can narrow `state/nonproduction/` to `state/nonproduction/example-`, but a
non-production caller rejects `state/production/`.

Every value originating in a workflow context, input, branch name, manifest, or target checkout is
untrusted data. Shell steps receive those values through `env:` only; direct `${{ ... }}`
interpolation into `run:` source is forbidden. Scripts double-quote expansions, use `--` and full
refspecs for operands, consume NUL-delimited Git output, create JSON with `jq --arg`, and create PR
and issue bodies through files. Branch names are never evaluated as code.

All repository-relative paths are canonicalised beneath their checkout roots. The workflow rejects
absolute paths, `..` traversal, missing config files, missing Terraform directories, duplicate
canonical directories, and directories that resolve outside the target checkout. Nested module
directories are allowed, but each directory processes only the `.tf` files directly within it.

Before either validation or reconciliation invokes `tf-version-bump`, the script enumerates that
directory's matching files, uses `lstat` to reject symlinks and non-regular files, and verifies each
canonical path remains under the target checkout. Required lock files receive the same checks.
Containment is repeated for every changed and untracked path before a preparation bundle or commit
is created.

## Per-branch preparation and validation flow

Each unprivileged preparation job performs the following sequence:

1. Check out the control OID and discovered state-branch OID with persisted credentials disabled.
2. Install the exact Go, `tf-version-bump`, and Terraform versions supplied by the caller.
3. Validate directory containment and reject symlinked or non-regular candidate files before any
   write.
4. For every configured Terraform directory, run `tf-version-bump` from that directory with the
   control config and the non-recursive pattern `*.tf`.
5. For that same directory, run:

   ```text
   terraform -chdir=<directory> init -upgrade -backend=false -input=false -no-color
   ```

6. After successful initialisation, inspect the canonical `.terraform/providers` package tree
   before removing it. If initialisation installed any provider package, require a regular
   `.terraform.lock.hcl`, including a newly created untracked file. If it installed none, record the
   directory as provider-free and allow the lock file to be absent. If a new required lock file is
   ignored, fail with a repository-configuration error; never force-add it.
7. Remove disposable `.terraform/` working data, then consume NUL-delimited Git status output and
   confirm that every changed `.tf` file is directly inside exactly one configured directory and
   every other change is that directory's lock file. Any other path fails the safety check.
8. Produce the versioned manifest and patch, verify their hashes locally, upload the immutable
   candidate bundle with a collision-resistant branch hash in its artefact name, and remove the
   checkouts. Terraform initialisation downloads provider packages but does not execute provider
   validation RPCs, so this captures the publishable candidate before any provider plugin executes.

A fresh unprivileged validation job downloads only its expected candidate, validates the manifest,
paths, modes, hashes, refs, and OIDs, and applies the patch to a fresh exact-base checkout. For each
directory with provider selections it runs:

```text
terraform -chdir=<directory> init -backend=false -input=false -no-color -lockfile=readonly
terraform -chdir=<directory> validate -no-color
```

For a manifest-declared provider-free directory it omits `-lockfile=readonly` because no lock file
exists, then runs the same validation command. The validation job never uploads source or lock-file
bytes and discards its checkout after uploading only its candidate-bound outcome and logs. Provider
execution therefore cannot alter the immutable candidate later considered for publication.

`tf-version-bump` continues processing later files after a per-file parse, stat, read, or write
error, but accumulates those errors and exits non-zero after processing completes. Its output ends
with a deterministic aggregate failure count. This intentionally changes the prior unsafe exit
status contract; automation never parses human prose to infer success.

Each configured directory is a Terraform module validation unit. Running the bump with `*.tf`
ensures every changed source file is loaded by the immediately following init/validate commands.
Consumers list nested modules explicitly. Duplicate directories are rejected, so a changed source
belongs to exactly one unit.

## Privileged reconciliation flow

A non-dry run starts a fresh reconciliation matrix after all validation jobs have uploaded their
bundles. For each branch, reconciliation:

1. Downloads only that run's expected artefact and validates its manifest, paths, modes, hashes,
   control OID, branch name, and validated base OID before using any payload.
2. For a successful result, checks out the exact control and base OIDs with persisted credentials
   disabled, repeats path/symlink preflight, independently reruns `tf-version-bump` for each
   configured directory, and requires its `.tf` diff to match the manifest.
3. Applies only the lock-file portion from the pre-provider candidate bundle, verifies the complete
   resulting diff and hashes, and stages only declared regular `.tf` and lock files.
4. Fetches the state ref immediately before publication and requires its remote OID still to equal
   the validated base OID. Movement fails without publication; a later run processes the new tip.
5. Verifies ownership of any existing automation ref and PR, then creates the signed or unsigned
   commit, updates the ref with an explicit expected-OID lease, and creates or refreshes the PR.
6. Reconciles the branch's marked failure issue and returns the result status so the overall
   workflow fails whenever validation or reconciliation failed.

For example, `state/nonproduction/example-thing` maps to
`update_state/nonproduction/example-thing`. State branches are read-only bases. The `update_`
namespace is reserved for this automation, but naming alone never grants ownership.

An automation commit carries exact machine-readable trailers identifying the workflow, state
branch, validated base OID, and control OID. A managed PR carries a corresponding hidden marker.
Before rewriting or deleting an existing automation ref, reconciliation requires both its tip
commit marker and the corresponding marked PR to agree. An unmarked or mismatched existing ref is
an automation failure and is never adopted.

When a successful non-dry run produces no changes, the workflow closes the marked automation PR as
obsolete and deletes only its verified automation branch using an expected-old-OID lease. A lease
mismatch fails without an unconditional retry. It also closes any open marked validation-failure
issue because the branch completed the update and validation flow.

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

It never enters the protected reconciliation environment and never commits, pushes, changes GitHub
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

- State-branch base revision.
- Immutable control revision.
- Config path.
- Go, `tf-version-bump`, and Terraform versions.
- Changed files.
- Initialised and validated Terraform directories.
- Workflow-run link.
- A statement that the branch is automation-owned and may be regenerated.

The workflow recreates the automation commit from the latest state-branch tip instead of
accumulating obsolete generated commits or rebasing them indefinitely. The force-with-lease applies
only to the stable automation ref after its ownership markers pass. A lease mismatch or state-base
movement fails as an automation error and does not retry with an unconditional force. Branch
deletion uses the same expected-old-OID protection.

## Failure classification and issues

Failures from these stages are deterministic state-branch failures:

- `tf-version-bump`
- `terraform validate`

The control config is validated before matrix creation, so a shared config failure stops discovery
without branch issues. All `terraform init` failures are workflow-level failures because its exit
status does not reliably distinguish branch configuration defects from registry outages,
authentication failures, rate limits, or other transient dependencies. Init failures retain logs
and fail the workflow but do not create branch issues. Missing/ignored locks, unsafe paths,
manifest failures, ref movement, and ownership or lease failures are likewise automation or
repository-policy failures rather than update/validation issues.

A non-dry run creates or updates one open issue per deterministic failing state branch. The issue
is identified by an exact stable title and a machine-readable marker, not a fuzzy title search. Its
body is replaced with the latest:

- State branch and base revision.
- Failing stage and Terraform directory, when applicable.
- Executed command without secret values.
- Concise tail of captured standard output and standard error.
- Workflow-run and uploaded-log links.

Full captured logs are uploaded as a workflow artefact with a collision-resistant name derived
from a hash of the complete branch ref. The issue body is bounded so recurring scheduled failures
do not grow without limit. A successful later non-dry run closes the issue with a recovery comment.

When a newer control or base revision produces a deterministic update/validation failure,
reconciliation closes the previous marked PR as obsolete, deletes its verified automation branch
with an expected-OID lease, and then creates or updates the failure issue. A stale previously
successful PR is never left open and apparently mergeable after a newer deterministic failure.
Workflow-level init or transient failures leave the prior PR unchanged because they do not establish
that its validated proposal is defective.

Authentication, GitHub API, invalid input, unsafe diff, signing, commit, push, lease, PR, issue
reconciliation, and workflow configuration failures are automation failures. They fail the matrix
job without creating a misleading state-validation issue. The candidate manifest and its bound
validation outcome together carry an explicit result class with precedence `automation`,
`shared/init`, `branch-update`, `branch-validation`, then `success`; reconciliation rejects unknown,
mismatched, or contradictory classes. `fail-fast: false` ensures other state branches continue.

## Authentication and permissions

The caller sets the reusable workflow's permission ceiling according to its authentication mode:

- Built-in-token callers grant `contents: write`, `pull-requests: write`, and `issues: write`.
- GitHub App callers grant only `contents: read`; unspecified scopes remain `none`.

Reusable workflows can only maintain or reduce the caller's permission ceiling. The reusable
workflow declares narrower permissions per job:

- Discovery and validation: `contents: read` only.
- Reconciliation: `contents: write`, `pull-requests: write`, and `issues: write`.

The reconciliation declaration supports built-in-token callers, but an App caller's read-only
ceiling keeps the effective reconciliation `GITHUB_TOKEN` read-only. Every checkout sets
`persist-credentials: false`. The built-in publication path injects `GITHUB_TOKEN` only into the
exact Git/GitHub CLI steps that need it. The App publication path never passes `GITHUB_TOKEN` to a
write operation; it uses only the short-lived App token. Consumers using the built-in token must
enable the repository setting that permits GitHub Actions to create pull requests.

GitHub recursion protection distinguishes the events created with the built-in token. Resulting
`push` events do not create workflow runs. Pull-request `opened`, `synchronize`, and `reopened`
events do create workflow runs, but those runs wait for approval from a repository writer. Other
pull-request activity types do not create runs. Built-in-token mode is therefore suitable only when
manual approval of generated PR checks is acceptable. Consumers whose required checks must run
unattended use App mode; an explicit consumer-managed `workflow_dispatch` is the documented manual
fallback rather than an assumed automatic check run.

When the App client ID and protected-environment private key are supplied, reconciliation uses the
GitHub-maintained App-token action to mint a short-lived installation token limited to the current
repository and the same three explicit permissions. It obtains the App bot's user ID through the
GitHub API and constructs the correct bot name and noreply address for commit attribution.
App-authenticated PR activity allows future PR checks to run without the built-in token's approval
gate. The App key never exists in discovery or validation.

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
- Branch names containing slashes.
- Valid hostile ref names containing shell metacharacters, quotes, leading dashes, Unicode, and
  percent signs without evaluating them as commands.
- No-match and invalid-path failures.
- Rejection of manual dispatch from a non-default ref.
- One immutable control OID across a default-branch movement.
- Exact discovered base OIDs and rejection when a state ref advances before publication.
- Successful update, initialisation, and validation with the real `tf-version-bump` binary and a
  pinned real Terraform CLI.
- Aggregate non-zero CLI status after a real per-file failure while later files still run.
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
- Pre-write rejection of Terraform/lock symlinks into the control checkout and outside both
  checkouts.
- Stable automation-branch regeneration, ownership-marker validation, unowned-ref refusal, and
  explicit leases for updates and deletion races.
- Malformed shared-config failure before matrix creation.
- Workflow-level classification of a real deterministic init/download failure without branch
  issues.
- Success followed by a new deterministic failure closes the stale marked PR and safely removes
  its owned update branch.
- Signed commits, local signature verification, cleanup of key material, and signing failure.
- A hostile purpose-built Terraform provider fixture that attempts to find credentials, alter
  control files and the validation checkout's lock file, poison later validation steps, and
  construct a malicious outcome. Local coverage verifies that it cannot replace the pre-provider
  candidate and that reconciliation treats every resulting payload as untrusted data; the actual
  runner and protected-secret boundary is verified by real-service acceptance.
- Unsigned operation when no signing key is configured.
- Obsolete no-change branch and PR payload generation.
- Clear failure when a newly required provider lock file is ignored; the workflow never force-adds
  it.

The tests do not mock `gh` or assert against invented GitHub API behaviour. Deterministic PR and
issue payload construction is tested locally. Actual PR, issue, token, and permissions behaviour is
covered by the real-service acceptance run.

### Workflow validation

The project adds a pinned `actionlint` invocation for all three example workflow files. GitHub.com
supports `concurrency.queue`, but actionlint 1.7.12 predates that syntax and reports `queue` as an
unexpected concurrency key. Until a pinned actionlint release supports it, the invocation includes
the exact `-ignore '^unexpected key "queue" for "concurrency" section\.'` filter for that known false
positive; it does not disable other syntax checks. The real-service queue acceptance remains
mandatory. Validation copies the example `.github` tree into a temporary consumer-style repository
root first, so local reusable-workflow paths resolve exactly as they do after installation. It also
runs `bash -n` and the integration test for both helper scripts. Existing Go tests, race detection,
branch-automation tests, linting, and build validation remain required and must produce pristine
output.

After implementation and TDD finish, a separate test-cleanup pass reviews new tests for duplicated
or low-value coverage.

### Real-service acceptance

The example README defines an acceptance procedure using a disposable GitHub repository with:

- One matching non-production state branch.
- One non-matching production branch.
- A provider-using root with a committed lock file, a provider-free root without one, and a
  default-branch config covering both.
- A default-branch-restricted publication environment.
- Built-in-token and GitHub App runs.
- An App-mode run whose job permission summary shows `contents: read` and no write scopes, and whose
  publication succeeds with the short-lived App token, proving `GITHUB_TOKEN` is not the write
  credential.
- An alternate-ref manual dispatch that cannot enter the protected publication job or read its
  secrets.
- A state branch using the hostile provider fixture. The provider attempts to read write/App/signing
  credentials, alter the control checkout and validation lock file, poison `GITHUB_ENV` and
  `GITHUB_PATH`, and submit a malicious outcome. The validation job exposes no protected or write
  credentials, cannot replace the pre-provider candidate, the fresh reconciliation runner rejects
  the tampered outcome, and no repository ref, content, PR, or issue mutation occurs.
- Successful update, repeated idempotent update, validation failure, issue update, recovery, and
  obsolete-PR cleanup.
- Three rapid dispatches demonstrating `queue: max` rather than pending-run replacement.
- A built-in-token PR whose `push` workflows remain absent and whose `pull_request` checks wait for
  writer approval, followed by an App-mode PR whose checks start without that approval gate.
- A dry run that demonstrates no repository refs, content, PRs, or issues changed while diagnostic
  logs and artefacts remain available.
- An unowned update-ref collision and concurrent update/deletion races that preserve the remote
  ref.
- A state-base movement that prevents stale publication.
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
- Production and non-production prefix/config separation.
- Manual narrowing and dry-run behaviour.
- Built-in-token limitations and optional GitHub App configuration.
- Unsigned and signed commit setup.
- Stable branch, PR, issue, and cleanup lifecycles.
- Reserved namespace, ownership markers, immutable control/base OIDs, and lease failure recovery.
- Single-root and multi-root configurations.
- Direct-file-per-module processing and the requirement to list every nested module explicitly.
- Backend-disabled Terraform initialisation.
- Workflow-level init failures versus deterministic branch update/validation issues.
- Required provider lock files, valid provider-free roots without locks, and rejection of ignored
  required locks and symlinked Terraform/lock paths.
- GitHub.com `queue: max` semantics, the temporary actionlint 1.7.12 false-positive ignore, and the
  need to confirm queue support before using the example on GitHub Enterprise Server.
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
  candidate is uploaded before provider execution, and privileged reconciliation runs on a fresh
  protected runner without executing target-supplied code.
- App-mode reconciliation has an effective read-only `GITHUB_TOKEN`; only its short-lived App token
  can publish repository content, refs, PRs, or issues.
- Untrusted refs and inputs remain data across Actions, shell, Git, JSON, Markdown, and artefact
  boundaries.
- Normal runs safely reconcile signed or unsigned commits, stable automation branches, PRs, and
  failure issues.
- Every run pins one control OID and one validated base OID per state branch; ref movement blocks
  publication.
- Existing update refs require verifiable ownership, and every rewrite or deletion uses an exact
  expected remote OID.
- The CLI reports aggregate per-file failure with a non-zero status.
- Every configured Terraform module directory with installed provider packages has a lock file
  after initialisation; new and changed required lock files are staged unless ignored (which is a
  clear failure), provider-free directories may omit the file, disposable `.terraform/` data is
  excluded, and symlinked, escaping, non-regular, or otherwise unexpected paths are rejected before
  writing.
- GitHub.com runs use the supported bounded `queue: max` concurrency behaviour; actionlint ignores
  only its pinned version's exact known false positive until parser support is released.
- Built-in-token documentation and acceptance distinguish suppressed `push` workflows from
  approval-required PR workflows, and require App mode for unattended required checks.
- Every changed Terraform source file belongs directly to exactly one configured and validated
  module directory.
- Local integration tests, pinned `actionlint`, the repository's full validation suite, and the
  disposable-repository acceptance procedure all pass with pristine output.
- User-facing documentation describes configuration, permissions, security boundaries, and
  recovery behaviour without relying on unstated setup.
