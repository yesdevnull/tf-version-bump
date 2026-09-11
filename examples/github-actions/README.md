# GitHub Actions state-branch automation

This copyable example updates Terraform dependencies across selected branches and opens or refreshes a pull request for each changed branch. It uses three jobs:

1. **Discover** selects branches by literal prefix and records their commit IDs.
2. **Process** updates each branch in a disposable checkout, runs `terraform init`, optionally formats the candidate, then runs `terraform validate`.
3. **Publish** checks the result and patch, then manages the update branch, pull request and failure issue.

The processing job has read-only repository permissions. Only the separate publication job has repository write permissions; it does not run Terraform or receive the registry token.

## Intended environment

Use this example with trusted official providers and your organisation's private modules, including modules from the HCP Terraform registry. The checks prevent configuration mistakes and accidental publication of unrelated files. They do not sandbox malicious providers or modules.

The workflow uses disposable Ubuntu runners and a pinned Terraform CLI. Docker is only used by this repository's local test harness. Processing and validation share one checkout and one initialisation; there is no separate fresh-checkout validation stage.

## Install

Copy the example `.github` directory onto the default branch of your Terraform repository:

```bash
source=/path/to/tf-version-bump
consumer=/path/to/terraform-repository
mkdir -p "$consumer/.github"
cp -R "$source/examples/github-actions/.github/." "$consumer/.github/"
```

Review and commit the copied files. The example supplies separate callers for:

- **Non-production:** `state/nonproduction/`, `state/staging/`, `aws-state/nonproduction/` and `aws-state/staging/`.
- **Production:** `state/production/` and `aws-state/production/`.

Both callers run only from the default branch. Their schedules are Monday 04:17 and Sunday 04:43 respectively in `Australia/Melbourne`. They also run when their control configuration changes, and can be started manually. A manual `branch_prefix` can narrow the configured prefixes but cannot select another branch family.

Allow the workflow's `contents`, `pull-requests` and `issues` write permissions. Enable **Settings → Actions → General → Workflow permissions → Allow GitHub Actions to create and approve pull requests** before live publication.

Create an Actions secret named `TF_API_TOKEN` with read access to your HCP Terraform registry modules and providers. The workflow exposes it as `TF_TOKEN_app_terraform_io` only during processing. The processing checkouts disable persisted Git credentials.

## Supply environment variables

`terraform init`, `fmt` and `validate` receive whatever you supply here; nothing else in the
workflow does. Non-sensitive entries go in the `terraform_env` input, one `NAME=VALUE` per line:

```yaml
terraform_env: |
  TF_VAR_region=ap-southeast-2
  TF_VAR_environment=production
```

Input values are not masked in logs. Put anything sensitive in the optional `TERRAFORM_ENV`
Actions secret instead, in the same `NAME=VALUE` shape; both supplied callers already pass it
through.

Both sources take one `NAME=VALUE` per line, so a multi-line value is written on one line with
escapes: `\n` becomes a real newline and `\\` becomes a single literal backslash. Any other
backslash sequence is passed through unchanged, so `\t` stays as a backslash followed by `t`. A
literal carriage return in an entry is rejected.

Terraform commands can need extra variables, such as a token for another private registry or
credentials for a module source during `terraform init`. As an example of a multi-line value,
GitHub App authentication for the `integrations/github` provider reads `GITHUB_APP_ID`,
`GITHUB_APP_INSTALLATION_ID` and `GITHUB_APP_PEM_FILE`, and the last holds the private key's PEM
contents rather than a path, so the `TERRAFORM_ENV` secret looks like this (key body abbreviated)
and Terraform receives the key with real newlines:

```text
GITHUB_APP_ID=12332432
GITHUB_APP_INSTALLATION_ID=12435523
GITHUB_APP_PEM_FILE=-----BEGIN RSA PRIVATE KEY-----\nMIIEow...\n-----END RSA PRIVATE KEY-----\n
```

Each of the secret's values is registered with `::add-mask::`, which redacts it from the workflow
console only. A multi-line value is registered as a single mask, so the console may not redact it
line by line. Every command's output is captured to log files inside the processing artefact, and
those files are not redacted: the artefact is retained for seven days and can be downloaded by
anyone with read access to the repository, so a credential a provider echoes into Terraform's
output appears there in plaintext. Supplied variables at least cannot turn on Terraform's trace
logging, because `TF_LOG` and `TF_LOG_PATH` are reserved.

A name may appear only once across both sources. Names the automation or the runner sets are
rejected, as are names that would redirect the programs Terraform runs or its configuration,
credentials, logging or plug-in sources.
The reserved prefixes are `PROCESS_`, `RECONCILE_`, `DISCOVERY_`, `RUNNER_`, `ACTIONS_`, `LD_`,
`DYLD_`, `TF_CLI_ARGS`, `TF_LOG`, `TF_PLUGIN_CACHE` and `GIT_`. The reserved exact names are
`PATH`, `IFS`, `ENV`, `BASH_ENV`, `SHELLOPTS`, `BASHOPTS`, `TF_DATA_DIR`, `TF_IN_AUTOMATION`,
`CHECKPOINT_DISABLE`, `TF_CLI_CONFIG_FILE`, `TERRAFORM_CONFIG`, `TF_WORKSPACE`, `HOME`, `TMPDIR`,
`SSL_CERT_FILE`, `SSL_CERT_DIR`, `GITHUB_ENV`, `GITHUB_PATH`, `GITHUB_OUTPUT`,
`GITHUB_STEP_SUMMARY`, `GITHUB_STATE` and `TF_TOKEN_app_terraform_io`. The last of those is
reserved in any letter case or other spelling Terraform maps to the `app.terraform.io` host,
because it would silently shadow the registry token the workflow injects from the `TF_API_TOKEN`
secret; `TF_TOKEN_*` names for other registries remain allowed. The five `GITHUB_*` names are the
runner's own command channels rather than provider configuration, which is why `GITHUB_APP_ID` and
the provider's other `GITHUB_` variables are accepted while `GITHUB_ENV` is not. Treat the list as best
effort rather than exhaustive. The structural protection is a separate rule: a file newly created
during a run is only ever publishable if it is a `.terraform.lock.hcl` directly inside a configured
Terraform root. A rejected entry never prints its value, and is rejected before any Terraform
command runs and before any file in the checkout is modified.

Both supplied callers forward the same repository `TERRAFORM_ENV` secret, so production
credentials are also available to non-production state-branch jobs. To separate them, either give
each policy its own secret name — mapping, say, `TERRAFORM_ENV_PRODUCTION` and
`TERRAFORM_ENV_NONPRODUCTION` onto the reusable workflow's `TERRAFORM_ENV` secret in each caller —
or hold the secret in a GitHub Environment named for the caller's `automation_policy_id`. A caller
cannot pass an environment's secrets to a reusable workflow, so the reusable workflow's `process`
job must then also declare that environment with its job-level `environment` key; GitHub then
gives the job the environment's secret rather than the one the caller passes.

## Configure updates

Edit the control configurations on the default branch:

```text
.github/tf-version-bump/nonproduction.yml
.github/tf-version-bump/production.yml
```

These are strict `tf-version-bump` configuration files. The workflow owns file selection, so do not add a `pattern` key. Pull requests changing these files run a read-only config validation check; they do not process state branches or run Terraform.

The callers process the repository root by default:

```yaml
terraform_directories: .
```

For several Terraform roots, use a newline-separated list:

```yaml
terraform_directories: |
  environments/nonproduction
  environments/staging
```

Roots must exist inside the state-branch checkout and must not resolve to duplicate directories. Version updates apply to `*.tf` files directly inside each configured root. When `terraform_fmt` is enabled and updates have changed files, formatting runs recursively below each root. Both supplied callers enable formatting; the reusable workflow defaults it to `false`.

The callers pin `tf-version-bump` to `v1.0.0-rc.11` and verify archive SHA-256 `5560b45e220650e8b18d5836eff05d471f602a6ac970aeeb9628781797f54c85` before running it.

## Initialisation and upgrades

`terraform_init_upgrade` defaults to `false`. Ordinary `init` preserves existing provider selections when they satisfy the updated constraints. If a requested version conflicts with the lock file, the run reports an initialisation failure; it does not retry with upgrade automatically.

Enable `terraform_init_upgrade` in the manual workflow inputs to use `terraform init -upgrade`. To enable it for scheduled and config-change runs, set `terraform_init_upgrade: true` in the caller's `with` block. Direct script callers can set `PROCESS_TERRAFORM_INIT_UPGRADE=true`; an omitted value defaults to `false`, and values other than exact `true` or `false` are rejected.

Upgrade applies to all eligible dependencies, including providers outside the bump configuration. Without an existing provider lock entry, ordinary `init` still selects a matching version. Modules are not covered by the provider lock file; a fresh checkout resolves their configured module constraints on each run.

Initialisation disables the backend and interactive prompts. Generated `.terraform.lock.hcl` files are included in the patch; do not ignore them in provider roots. Validation uses the same initialised directory. A provider-free root can have no lock file.

## Results and publication

The processing result contains `result.json`, diagnostic logs and, for a changed valid candidate, `candidate.patch`. Publication checks the run and branch identity, patch checksum, configured roots and permitted file changes before constructing one commit containing dependency, lock-file and formatting changes.

For `state/nonproduction/example-thing`, the managed update branch is `update_state/nonproduction/example-thing`. The publisher checks that the state branch still matches its discovered commit. It updates an existing automation-owned ref using an exact force-with-lease; ownership or lease failures stop publication for the next run to handle.

Pull requests and failure issues carry this stable marker:

```html
<!-- tf-version-bump:<policy>:<ref-hash> -->
```

Their body names the result, whether `terraform fmt` ran, the Terraform and tf-version-bump versions and the workflow run; a failure issue adds the failed stage, root and exit status.

| Result | Publication |
| --- | --- |
| Changed and valid | Create or refresh the marked PR; close the marked failure issue |
| Unchanged and valid | Close the obsolete marked PR and failure issue |
| Update, init, fmt or validation failure | Close the marked PR first, then create or refresh the failure issue |
| Automation failure or missing/invalid result | Stop without changing managed PRs, issues or refs |

A failed candidate fails its `process` job, so the jobs list shows which branches broke. When
processing wrote a result, the run summary names the branch and its classification, and for an
update, initialisation, formatting or validation failure the stage and root that failed; a
failure before the result exists writes no summary. Publication runs either way, so the table
above still applies.

Unchanged candidates still run validation. Before any PR or issue reconciliation, the publisher checks that the remote state branch still matches the discovered commit; a moved or missing branch or failed lookup stops reconciliation. Cleanup matches the policy/branch marker and the expected PR head and base. Update refs are retained. GitHub lookup and closure errors stop reconciliation.

Publication uses the built-in `GITHUB_TOKEN`. The helper respects Git's signing configuration; the example does not provision a signing key. If signing is enabled, the runner must have a working key. Do not assume token-created branches and PRs will automatically run your downstream checks; review GitHub's [GITHUB_TOKEN workflow behaviour](https://docs.github.com/en/actions/how-tos/writing-workflows/choosing-when-your-workflow-runs/triggering-a-workflow#triggering-a-workflow-from-a-workflow) when configuring required checks.

## Run and inspect

Start with a manual run from the default branch and select `dry_run`. This processes and validates candidates and checks publication locally, without pushing refs or changing PRs or issues. Inspect the candidate patch and logs before enabling live publication.

Open the workflow run to inspect `discover`, `process` and `publish`. Each `process` job's summary includes the updater's log for every configured root, so the run overview page shows which versions changed; a long log is truncated there and kept in full in the artefact. Terraform's own logs appear only in the artefact, because a provider can echo a credential into them. In a dry run, each `publish` job's summary states what a live run would have done to the branch's pull request and failure issue. Download its result artefacts to see `result.json`, `candidate.patch` and captured command logs. Artefacts are retained for seven days. Results belong to one run attempt; use **Re-run all jobs**, as partial job reruns are unsupported.

Before enabling schedules, test in a disposable private repository: run a dry run, publish a valid change twice and check that the same PR is refreshed, then introduce a validation failure and confirm that the PR closes and one failure issue is maintained. Finally, test a valid no-change result and confirm that the issue closes. No live GitHub repository is created or mutated by this repository's local component tests.
