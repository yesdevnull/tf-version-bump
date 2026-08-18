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
- Maintain stable update branches, pull requests, and validation-failure issues without creating
  duplicates on scheduled runs.
- Support a side-effect-free dry run that still applies and validates the proposed changes in the
  disposable runner checkout.
- Support the built-in `GITHUB_TOKEN`, optional GitHub App authentication, and optional SSH commit
  signing.
- Show how the same workflow can validate multiple Terraform root modules without complicating the
  root-directory example.

## Non-goals

- Change the `tf-version-bump` CLI or its YAML configuration format.
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
failure logs for diagnosis.

## Workflow architecture

The production and non-production callers own policy. Each caller defines its allowed prefixes,
configuration path, pinned tool versions, schedule, and concurrency group, then invokes the local
reusable workflow.

The reusable workflow contains two jobs:

1. `discover` validates the configuration and produces a sorted JSON matrix of matching remote
   state branches.
2. `update` runs once per matrix entry with `fail-fast: false`, so every discovered state branch is
   processed even if another branch fails.

The update matrix defaults to four concurrent branches. This bounds Terraform downloads and GitHub
API writes while retaining useful parallelism. A caller can lower or raise the limit through the
reusable workflow's `max_parallel` input.

Each update job uses two independent checkouts:

- A control checkout of `github.event.repository.default_branch` supplies the reviewed workflow,
  scripts, and `tf-version-bump` configuration.
- A target checkout of the selected remote state branch receives the proposed changes.

The separation ensures that a state branch cannot replace the automation or configuration that is
processing it.

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

Each caller has its own concurrency group with cancellation disabled. A new run queues behind an
active run for the same environment rather than interrupting branch, PR, or issue reconciliation.
Production and non-production can run independently.

## Reusable workflow interface

| Input | Type | Required/default | Purpose |
|---|---|---|---|
| `allowed_branch_prefixes` | string | Required | Newline-separated literal prefix allow-list |
| `branch_prefix` | string | `""` | Optional manual narrowing prefix |
| `config_path` | string | Required | Config path in the default-branch control checkout |
| `file_pattern` | string | `**/*.tf` | Terraform files passed to `tf-version-bump` |
| `terraform_directories` | string | `.` | Newline-separated root-module directories |
| `terraform_version` | string | Required | Exact Terraform version installed for the run |
| `tf_version_bump_version` | string | Required | Exact `tf-version-bump` release tag used by `go install` |
| `go_version` | string | Required | Exact Go version used by `go install` |
| `dry_run` | boolean | `false` | Suppress every remote mutation |
| `max_parallel` | number | `4` | Maximum concurrent state-branch jobs |
| `commit_author_name` | string | Bot identity | Commit author and committer name |
| `commit_author_email` | string | Bot identity | Commit author and committer email |
| `github_app_client_id` | string | `""` | Enable GitHub App authentication when paired with its key |

Optional reusable-workflow secrets are:

- `github_app_private_key`
- `commit_signing_private_key`

The workflow rejects partial authentication or signing configurations. When signing is enabled,
the caller must explicitly supply both commit identity inputs rather than inherit a bot default.

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

Discovery lists remote heads from `origin`; it does not depend on whatever subset
`actions/checkout` fetched. A state branch matches when its complete name begins with one of the
configured literal prefixes. The remainder of the branch name is unrestricted except by Git's
normal ref-name rules.

The discovery script:

1. Rejects an empty allow-list, empty entries, absolute-looking values, control characters, and
   values that are not valid literal branch prefixes.
2. If `branch_prefix` is supplied, verifies that it begins with at least one allowed prefix.
3. Selects remote branches using the allow-list plus optional narrowing prefix.
4. Deduplicates branches selected by overlapping prefixes.
5. Sorts names bytewise for deterministic output.
6. Fails when no branches match so a configuration mistake cannot look successful.
7. Encodes the final list as JSON for the matrix without evaluating branch names as shell code.

The manual prefix can narrow `state/nonproduction/` to `state/nonproduction/example-`, but a
non-production caller rejects `state/production/`.

All repository-relative paths are resolved beneath their checkout roots. The workflow rejects
absolute paths, `..` traversal, missing config files, missing Terraform directories, and
directories that resolve outside the target checkout.

## Per-branch update flow

Each matrix job performs the following sequence:

1. Create the control and target checkouts.
2. Install the exact Go, `tf-version-bump`, and Terraform versions supplied by the caller.
3. Apply `tf-version-bump` once across the target checkout with the config from the control
   checkout and the configured file pattern.
4. For every configured Terraform directory, run sequentially:

   ```text
   terraform -chdir=<directory> init -upgrade -backend=false -input=false -no-color
   terraform -chdir=<directory> validate -no-color
   ```

5. Require a `.terraform.lock.hcl` file in every configured Terraform directory after
   initialisation, including a newly created untracked lock file.
6. Remove or ignore disposable `.terraform/` working data, then confirm that only Terraform source
   files and `.terraform.lock.hcl` files changed. Any other unexpected path fails the safety check.
7. If the run is a dry run, report the proposed file changes and stop without remote side effects.
8. If no allowed files changed, reconcile obsolete automation state and stop without an empty
   commit.
9. Stage only allowed changed files.
10. Create a commit from the latest state-branch tip on `update_<state-branch>`.
11. Fetch any existing automation branch and update it using an explicit force-with-lease that
    protects against an unseen concurrent writer.
12. Create or update the single pull request from the automation branch to the state branch.
13. Close any open validation-failure issue for the state branch.

For example, `state/nonproduction/example-thing` maps to
`update_state/nonproduction/example-thing`. Only branches beginning with `update_` are regenerated
or deleted by this workflow. State branches are read-only bases.

When a successful non-dry run produces no changes, the workflow closes an existing automation PR
as obsolete and deletes only the corresponding `update_<state-branch>` branch. It also closes any
open validation-failure issue because the branch completed the update and validation flow.

## Dry-run semantics

Dry-run mode is a remote side-effect boundary, not a read-only filesystem mode. The workflow still:

- Runs `tf-version-bump` without the CLI's `-dry-run` flag.
- Runs real Terraform initialisation and validation.
- Creates or updates the required lock files in the disposable checkout.
- Performs the allowed-path safety check.
- Reports the proposed changes and failures in workflow output.

It never commits, pushes, creates or edits a pull request, creates or edits an issue, closes an
issue or pull request, or deletes an automation branch. This validates the proposed result rather
than the branch's old contents.

## Commit and pull-request lifecycle

The stable commit subject is:

```text
chore: apply tf-version-bump config
```

The pull request identifies the state branch and is looked up by exact base and head refs rather
than title alone. Its body is refreshed on every run with:

- State-branch base revision.
- Config path.
- Go, `tf-version-bump`, and Terraform versions.
- Changed files.
- Initialised and validated Terraform directories.
- Workflow-run link.
- A statement that the branch is automation-owned and may be regenerated.

The workflow recreates the automation commit from the latest state-branch tip instead of
accumulating obsolete generated commits or rebasing them indefinitely. The force-with-lease applies
only to the stable automation ref. A lease mismatch fails as an automation error and does not retry
with an unconditional force.

## Failure classification and issues

Failures from these stages are state-branch failures:

- `tf-version-bump`
- `terraform init -upgrade -backend=false`
- `terraform validate`

A non-dry run creates or updates one open issue per failing state branch. The issue is identified
by an exact stable title and a machine-readable marker, not a fuzzy title search. Its body is
replaced with the latest:

- State branch and base revision.
- Failing stage and Terraform directory, when applicable.
- Executed command without secret values.
- Concise tail of captured standard output and standard error.
- Workflow-run and uploaded-log links.

Full captured logs are uploaded as a workflow artefact with a branch-safe name. The issue body is
bounded so recurring scheduled failures do not grow without limit. A successful later non-dry run
closes the issue with a recovery comment.

Authentication, GitHub API, invalid input, unsafe diff, signing, commit, push, lease, PR, issue
reconciliation, and workflow configuration failures are automation failures. They fail the matrix
job without creating a misleading state-validation issue. `fail-fast: false` ensures other state
branches continue.

## Authentication and permissions

The caller job grants only:

- `contents: write`
- `pull-requests: write`
- `issues: write`

The default path uses the job's `GITHUB_TOKEN`. Consumers must enable the repository setting that
permits GitHub Actions to create pull requests. Pull-request workflows generated with this token
may require manual approval under GitHub's current recursion protection.

When both GitHub App inputs are supplied, the workflow uses the GitHub-maintained App-token action
to mint a short-lived installation token limited to the current repository and the same three
explicit permissions. It obtains the App bot's user ID through the GitHub API and constructs the
correct bot name and noreply address for commit attribution. App-authenticated PR activity allows
future PR checks to run without the built-in token's approval gate.

Every referenced action is pinned to an immutable commit SHA with a version comment, matching the
repository's existing workflow convention.

## Optional SSH commit signing

When `commit_signing_private_key` is absent, the workflow creates the correctly attributed unsigned
bot or App commit.

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

Signing and verification failure stops the job. There is no unsigned fallback. The documentation
states that the signing key must work non-interactively and that its public key must be registered
as a signing key on the GitHub account associated with the author email for GitHub to show the
commit as verified.

## Multiple Terraform root modules

The example callers use `terraform_directories: .`. A consumer with multiple root modules can pass
newline-separated paths such as:

```text
environments/network
environments/platform
```

`tf-version-bump` still runs once for the configured repository file pattern. Terraform
initialisation and validation then run sequentially in each root module. All roots must pass before
the workflow publishes any commit. A failure issue names the affected root.

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
- No-match and invalid-path failures.
- Successful update, initialisation, and validation with the real `tf-version-bump` binary and a
  pinned real Terraform CLI.
- Terraform source and lock-file staging.
- Multi-directory success and failure attribution.
- Dry-run isolation.
- Rejection of unexpected changed files.
- Stable automation-branch regeneration and explicit lease protection.
- Signed commits, local signature verification, cleanup of key material, and signing failure.
- Unsigned operation when no signing key is configured.
- Obsolete no-change branch and PR payload generation.

The tests do not mock `gh` or assert against invented GitHub API behaviour. Deterministic PR and
issue payload construction is tested locally. Actual PR, issue, token, and permissions behaviour is
covered by the real-service acceptance run.

### Workflow validation

The project adds a pinned `actionlint` invocation for all three example workflow files. It also
runs `bash -n` and the integration test for both helper scripts. Existing Go tests, race detection,
branch-automation tests, linting, and build validation remain required and must produce pristine
output.

After implementation and TDD finish, a separate test-cleanup pass reviews new tests for duplicated
or low-value coverage.

### Real-service acceptance

The example README defines an acceptance procedure using a disposable GitHub repository with:

- One matching non-production state branch.
- One non-matching production branch.
- A committed lock file and default-branch config.
- Built-in-token and GitHub App runs.
- Successful update, repeated idempotent update, validation failure, issue update, recovery, and
  obsolete-PR cleanup.
- A dry run that demonstrates no remote refs, PRs, or issues changed.
- An optional signed run whose pushed commit is locally verified and shown as verified by GitHub
  when the signing identity is configured correctly.

The acceptance transcript is reviewed before completion is claimed.

## Documentation changes

`examples/github-actions/README.md` documents:

- Files to copy and paths to customise.
- Repository settings and least-privilege permissions.
- Exact tool-version pinning.
- Production and non-production prefix/config separation.
- Manual narrowing and dry-run behaviour.
- Built-in-token limitations and optional GitHub App configuration.
- Unsigned and signed commit setup.
- Stable branch, PR, issue, and cleanup lifecycles.
- Single-root and multi-root configurations.
- Backend-disabled Terraform initialisation.
- Troubleshooting and real-service acceptance.

`examples/README.md` and `docs/ADVANCED-USAGE.md` link to the GitHub Actions example and distinguish
it from the local sequential `update-branches.sh` workflow. The root README remains concise.

## Completion criteria

The feature is complete when:

- The three copyable workflows and two documented scripts implement this design.
- Non-production and production examples have manual and timezone-aware scheduled triggers.
- Dry runs perform real local updates and validation without any remote mutation.
- Normal runs safely reconcile signed or unsigned commits, stable automation branches, PRs, and
  failure issues.
- Every configured Terraform root has a lock file after initialisation; new and changed lock files
  are staged, disposable `.terraform/` data is excluded, and unexpected paths are rejected.
- Local integration tests, pinned `actionlint`, the repository's full validation suite, and the
  disposable-repository acceptance procedure all pass with pristine output.
- User-facing documentation describes configuration, permissions, security boundaries, and
  recovery behaviour without relying on unstated setup.
