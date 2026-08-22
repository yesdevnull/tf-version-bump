# GitHub Actions state-branch automation POC

This copyable proof of concept discovers selected Terraform state branches, prepares and validates one candidate per branch, verifies the result, then opens or refreshes a pull request when files change. The reusable workflow has four jobs: `discover`, `prepare`, `validate`, and `publish`. An unchanged branch still validates every configured root and finishes without a commit, Git ref mutation, or GitHub mutation. Update, initialisation, formatting, and validation failures create or refresh a marked issue instead.

## Operating assumptions and limits

Use this POC only where Terraform modules are written by your organisation, providers are official HashiCorp providers or organisation-developed providers, and provider/module egress is controlled by an NVA. The scripts do not sandbox malicious Terraform, providers, or modules; private and first-party providers are trusted code that can execute in the runner, and private and first-party module sources are trusted code. Post-Terraform checks run on the same runner and detect only accidental or non-adversarial mutation. Untrusted provider or module code requires independent verification or isolation. A successful validation is only the observed result of Terraform in that run.

It intentionally omits comprehensive hostile-content defence, recovery after interrupted publication, automatic clean-up, and exhaustive publication-race handling. A lease or ownership failure is reported for the next run to handle. Review the workflow and helper scripts before using them with a different trust model.

The no-change result does not remove an update branch or close an existing pull request left by an earlier run. That reconciliation remains deferred with the other automatic clean-up work.

Both Terraform jobs install the pinned Terraform CLI with `hashicorp/setup-terraform` and run it directly. Docker is not a workflow prerequisite or production validation boundary. The repository harness uses a Docker container only as reproducible local Linux/Terraform test infrastructure, where the helper is deliberately run without a Docker executable.

Each caller also runs when its own control config changes on the default branch. The existing
default-branch guard safely skips matching changes on other branches. Pull requests that change
either control config run a read-only CLI dry-run check against a temporary fixture; it does not
discover state branches, run Terraform, or publish anything. A full pull-request state-branch
dry-run remains deferred.

## Install

On the default branch of the target repository, copy the example `.github` directory into the repository root:

```bash
source=/path/to/tf-version-bump
consumer=/path/to/terraform-repository
mkdir -p "$consumer/.github"
cp -R "$source/examples/github-actions/.github/." "$consumer/.github/"
```

Review and commit the copied files. The example supplies two callers:

- `tf-version-bump-nonproduction.yml` for `state/nonproduction/`, `state/staging/`, `aws-state/nonproduction/`, and `aws-state/staging/`;
- `tf-version-bump-production.yml` for `state/production/` and `aws-state/production/`.

Both callers run only when the workflow revision is on the default branch. Their schedules are Monday 04:17 and Sunday 04:43 respectively in `Australia/Melbourne`.
Changing `.github/tf-version-bump/nonproduction.yml` or `production.yml` on that branch also starts
the matching caller. The separate configuration-validation workflow runs for pull requests that
change either file.

Configure Actions to permit the workflow's `contents`, `pull-requests`, and `issues` write permissions. Before live publication, enable **Settings → Actions → General → Workflow permissions → Allow GitHub Actions to create and approve pull requests** for the repository or organisation.

Create a repository Actions secret named `TF_API_TOKEN` containing an HCP Terraform token with
read access to the private modules and providers used by the configured roots. The supplied callers
pass it to the reusable workflow, which exposes it as `TF_TOKEN_app_terraform_io` only while the
prepare and validate helpers run. The publish job does not receive the token in its environment.

`actions/checkout` manages the built-in token only for discovery's read-only control checkout and publication's write-capable target checkout. Terraform checkouts disable persisted credentials, and the validate job performs validation and verification in its one target checkout; no custom token files, App, or PAT credentials are used.
The reconciliation helper owns its exact-ref fetches and exact-lease publication directly; the
processing helper only prepares and validates candidates.

## Configure roots and version changes

The control configuration lives on the default branch at:

```text
.github/tf-version-bump/nonproduction.yml
.github/tf-version-bump/production.yml
```

Edit the provider and module targets in those files. They are strict `tf-version-bump` config files, so the workflow owns the `*.tf` file selection; do not add a `pattern` key.

The callers are configured for the repository root with:

```yaml
terraform_directories: .
```

For several direct Terraform roots, use a newline-separated list in the caller instead:

```yaml
terraform_directories: |
  environments/nonproduction
  environments/staging
```

Each configured root is processed independently. Keep the config path repository-relative and committed on the default branch that starts the run. `terraform_fmt` defaults to `false`, and both supplied callers set `terraform_fmt: true`. For every configured root, preparation runs the updater and `terraform init -upgrade` before formatting is eligible. When enabled for a changed candidate, Terraform runs `terraform fmt -recursive` in every configured root.
When a root has provider selections, its resulting `.terraform.lock.hcl` change is included in the
update branch for reproducible runs. A provider-free root can legitimately have no lock file.

The callers pin `tf-version-bump` to `v1.0.0-rc.9` and verify the archive SHA-256 `38428a229a77671fd192fd6a18f5d1f9c404b5557124883f04e6a8bec154b1d2` before execution.

## Results, failures, and pull requests

Validation and verification use one target checkout in `validate`; there is no separate fresh verification checkout. The run retains `preparation-*` and `verified-*` artefacts; it has no separate validation artefact or verification job. Inspect the `preparation-*` and `verified-*` artefacts for manifests and captured logs.

For a changed candidate, publication first creates one dynamic dependency commit. Its subject reflects the update result: `chore: bump Terraform provider and module versions`, `chore: bump Terraform provider versions`, `chore: bump Terraform module versions`, or `chore: update Terraform configuration`. If recursive formatting changed files, publication adds the second commit, `chore: run Terraform fmt`. A formatting run with no file changes produces no format patch and no `chore: run Terraform fmt` commit.

The managed pull request reports exact `Module blocks updated` and `Provider blocks updated` counts, followed by separate `Dependency and lock-file changes` and `Formatting changes` file lists. A `branch-format` result means `terraform fmt -recursive` failed in a configured root; it creates or refreshes the marked failure issue rather than publishing an update branch.

## Authentication and publication

The POC uses only the workflow's built-in `GITHUB_TOKEN` to publish update branches, pull requests, and failure issues. Automation commits are explicitly unsigned, even when repository or runner Git configuration enables signing.

GitHub suppresses push events generated with `GITHUB_TOKEN`, and workflows triggered by opening, synchronising, or reopening its pull requests require approval. Do not rely on those events to pass required checks automatically. GitHub App authentication, commit signing, and publication-environment approval are deferred from this POC.

## Run and inspect

Start with a manual dry run from the default branch. In **Actions**, select the non-production workflow, choose **Run workflow**, leave `branch_prefix` empty (or enter a literal configured prefix), and select `dry_run`. A dry run performs preparation, validation, verification, and local commit construction only when files changed. A dry run does not push a branch or create or update a pull request or issue.

The weekly schedule uses all configured prefixes. A manual `branch_prefix` only narrows that policy; it cannot select an unconfigured branch family.

For a state branch such as `state/nonproduction/example-thing`, the update branch is `update_state/nonproduction/example-thing`. Pull requests and issues include the stable marker:

```html
<!-- tf-version-bump:<policy>:<ref-hash> -->
```

Reruns use that marker to refresh rather than duplicate the pull request or issue. If a run needs to be repeated, use **Re-run all jobs**. Partial job reruns are unsupported because the artefacts are tied to one run attempt.

Open the workflow run to inspect the `discover`, `prepare`, `validate`, and `publish` jobs. Download and inspect the `preparation-*` and `verified-*` artefacts from the run for their manifests and captured logs; example artefacts are retained for seven days.

## Operator-run battle test

No disposable GitHub repository is created or mutated by this example repository. Before enabling regular publication, an operator should run the following in a disposable private repository. Replace every angle-bracket value with an operator-supplied value.

```bash
repository=<OWNER/REPOSITORY>
default_branch=<DEFAULT-BRANCH>
branch_prefix=state/nonproduction/

gh workflow run tf-version-bump-nonproduction.yml --repo "$repository" --ref "$default_branch" \
  -f branch_prefix="$branch_prefix" -f dry_run=true
gh run list --repo "$repository" --workflow tf-version-bump-nonproduction.yml --limit 1
```

Confirm the dry run reaches all four jobs and creates no update branch, pull request, or issue. Then repeat with `dry_run=false`, record the run URL, and confirm it creates or refreshes the expected `update_<state-branch>` pull request. Run it once more and confirm the same pull request is refreshed. Finally, introduce a controlled validation error on one disposable matching state branch, run again, and confirm one marked issue is created or refreshed. Record the workflow URLs and observed result in your deployment change record; do not add credentials or private repository content to this example.
