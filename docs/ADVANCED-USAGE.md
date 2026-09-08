# Advanced usage

The repository includes [`examples/update-branches.sh`](../examples/update-branches.sh) for
applying the same `tf-version-bump` operation across several Git branches.

This is intentionally separate from the core CLI. `tf-version-bump` edits one checked-out
worktree; the example script is responsible for branch selection, checkouts, commits, and
restoring the starting branch.

## GitHub Actions state-branch automation POC

For a scheduled or manually dispatched GitHub Actions proof of concept, see
[`examples/github-actions`](../examples/github-actions/README.md). It copies into a consuming
repository's `.github` directory and provides separate production and non-production callers that
run only from the default branch, plus config-change triggers, read-only pull-request config
validation, dry-run support, update pull requests, and marked failure issues. The reusable
workflow has three jobs: `discover`, `process`, and `publish`. The pull-request check
validates only the control config with `tf-version-bump`; full state-branch dry runs remain
deferred.

The example targets trusted official providers and organisation-authored private modules,
including the HCP Terraform registry. It checks for configuration mistakes and unexpected
file changes; it does not sandbox malicious Terraform or providers.

Create a repository Actions secret named `TF_API_TOKEN` with read access to your HCP Terraform
registry modules and providers. The processing job receives it as `TF_TOKEN_app_terraform_io`.
Publication runs separately with repository write permissions and no registry token.

Each configured root runs the updater and `terraform init`. If the candidate changes and
`terraform_fmt` is enabled, formatting runs recursively below every configured root. All roots
then run `terraform validate` in the same initialised checkout, including unchanged candidates.
The reusable workflow defaults formatting to `false`; both supplied callers enable it.

The callers pin `tf-version-bump` to `v1.0.0-rc.11` and archive SHA-256
`5560b45e220650e8b18d5836eff05d471f602a6ac970aeeb9628781797f54c85`.

`terraform_init_upgrade` defaults to `false`. Enable the manual input, or set it to `true` in
the caller's `with` block for scheduled runs, to add `-upgrade`. Direct script callers use
`PROCESS_TERRAFORM_INIT_UPGRADE=true`. Ordinary initialisation preserves compatible locked
provider versions and fails if updated constraints exclude them; it does not retry with upgrade.
Upgrade can update all eligible providers within their constraints. Generated lock files are
included in the candidate. Modules are resolved from their configured constraints on each fresh run.

The processing result contains `result.json`, logs and, for a changed valid candidate,
`candidate.patch`. Publication checks the result identity, patch checksum, configured roots and
permitted paths, then creates one commit containing dependency, lock-file and formatting changes.
It checks the remote base and automation ownership before updating the managed ref with an exact
force-with-lease. Docker is only used by the local test harness; workflow Terraform runs directly.

A successful update refreshes the marked PR and closes any marked failure issue. A valid
no-change result closes both. Update, initialisation, formatting and validation failures close
the marked PR before creating or refreshing a failure issue. Invalid or missing results stop
publication; dry runs do not change remote refs or GitHub records. Update refs are retained.

Publication uses the workflow `GITHUB_TOKEN`. Enable **Settings → Actions → General →
Workflow permissions → Allow GitHub Actions to create and approve pull requests** before live
publication. The helper respects Git's signing configuration but does not provision keys.
Do not rely on token-created branches and PRs to run downstream checks automatically; see the
example's authentication notes. Use **Re-run all jobs**, as results are tied to one run attempt.

## Before you run it

The script:

- Requires Bash, Git, and `tf-version-bump`
- Refuses a repository with tracked or untracked worktree changes
- Refuses detached HEAD state
- Selects branch names with a case-sensitive Bash glob
- Creates a commit from tracked file changes after each successful update
- Signs update commits when `--sign-commits` is supplied
- Never pushes commits
- Restores the starting branch after a successful run

Run a dry run first and inspect the selected branches. Updating many branches creates many
independent commits; decide how those commits will be reviewed and pushed before running the
write mode.

## Basic module update

From this repository:

```bash
examples/update-branches.sh \
  --repository /path/to/terraform-repository \
  --branch-pattern 'release/*' \
  --module 'terraform-aws-modules/vpc/aws' \
  --to '5.0.0'
```

For each matching local branch, the script:

1. Checks out the branch.
2. Runs `tf-version-bump` with the requested file pattern.
3. Stages modified tracked files with `git add -u`.
4. Commits them as `chore: bump <source> to <version>`.
5. Moves to the next branch.
6. Returns to the branch that was active at startup.

Branches that already contain the target version produce no commit.

To explicitly request a signature for every update commit, add `--sign-commits`. The flag passes
`-S` to `git commit`, so Git must have a usable signing key configured. Without the flag, the
script leaves signing to the repository's Git configuration; it does not disable automatic
signing when `commit.gpgsign` is enabled.

## Preview every branch

```bash
examples/update-branches.sh \
  --repository /path/to/terraform-repository \
  --branch-pattern 'release/*' \
  --module 'terraform-aws-modules/vpc/aws' \
  --to '5.0.0' \
  --dry-run
```

`--dry-run` passes `-dry-run` to `tf-version-bump`, so Terraform files and branch histories are
unchanged. The script still checks out each selected local branch. When combined with
`--include-remotes`, it fetches the remote and checks out selected remote-only branches in detached
HEAD state, without creating local branches.

## Use a config file

```bash
examples/update-branches.sh \
  --repository /path/to/terraform-repository \
  --branch-pattern 'release/*' \
  --config /path/to/versions.yml
```

Config-mode commits use the subject `chore: apply tf-version-bump config`.

The script converts the supplied config path to an absolute path before changing branches. If the
config lives inside the target repository, each checkout can change the file at that path. Keep
the same config committed on every selected branch or place the controlling config outside the
target repository.

## Include remote-only branches

```bash
examples/update-branches.sh \
  --repository /path/to/terraform-repository \
  --branch-pattern 'feature/*' \
  --module 'terraform-aws-modules/vpc/aws' \
  --to '5.0.0' \
  --include-remotes
```

The default remote is `origin`. Select another one with `--remote <name>`:

```bash
examples/update-branches.sh \
  --repository /path/to/terraform-repository \
  --branch-pattern 'release/*' \
  --config /path/to/versions.yml \
  --include-remotes \
  --remote upstream
```

The script fetches that remote, then creates a local tracking branch in write mode when a matching
branch does not already exist locally. Dry runs use detached HEAD instead. Existing local branches
take precedence. It does not push new commits or alter the remote branch.

## Filter by recent activity

Process only branches whose tip commit falls within a number of days:

```bash
examples/update-branches.sh \
  --repository /path/to/terraform-repository \
  --branch-pattern 'feature/*' \
  --module 'terraform-aws-modules/vpc/aws' \
  --to '5.0.0' \
  --since-days 30
```

The filter compares each branch tip's Git commit timestamp with the current time. It does not
scan older commits or infer whether the branch has been merged.

## Choose the Terraform file pattern

The default is `**/*.tf`. Override it when only part of each branch should be changed:

```bash
examples/update-branches.sh \
  --repository /path/to/terraform-repository \
  --branch-pattern 'release/*' \
  --file-pattern 'environments/prod/**/*.tf' \
  --config /path/to/versions.yml
```

The pattern is evaluated from the target repository's root. See
[File selection](USAGE.md#file-selection) for glob semantics.

## Capture a log

```bash
examples/update-branches.sh \
  --repository /path/to/terraform-repository \
  --branch-pattern 'release/*' \
  --config /path/to/versions.yml \
  --log-file ./version-bump.log
```

The log is appended rather than replaced and contains output from the script, Git, and
`tf-version-bump`. The path must be outside the target repository so the log cannot interfere with
the clean-worktree check or branch checkouts. The script rejects an internal path before creating
the log file. The final path component must not be a symbolic link, even when its target is also
outside the repository.

## Use a particular binary

The script normally resolves `tf-version-bump` from `PATH`. Use a locally built binary with:

```bash
examples/update-branches.sh \
  --repository /path/to/terraform-repository \
  --branch-pattern 'release/*' \
  --module 'terraform-aws-modules/vpc/aws' \
  --to '5.0.0' \
  --binary /path/to/tf-version-bump
```

## Options

| Option | Default | Description |
|--------|---------|-------------|
| `--repository <path>` | Current directory | Git repository to process |
| `--branch-pattern <glob>` | Required | Local branch names to select |
| `--module <source>` | One mode required | Module source for direct updates |
| `--to <version>` | Required with `--module` | Target module version |
| `--config <file>` | One mode required | YAML config for aggregate updates |
| `--file-pattern <glob>` | `**/*.tf` | Terraform files within each branch |
| `--binary <path>` | `tf-version-bump` from `PATH` | CLI executable |
| `--dry-run` | Off | Preview without file changes or commits |
| `--sign-commits` | Off | Pass `-S` to Git when creating update commits |
| `--include-remotes` | Off | Fetch and include remote-only branches |
| `--remote <name>` | `origin` | Remote used by `--include-remotes` |
| `--since-days <number>` | No age filter | Include branches with recent tip commits |
| `--log-file <path>` | No log file | Append output outside the repository to a non-symlink path |

Run `examples/update-branches.sh --help` for the built-in reference.

## Failures and recovery

If `tf-version-bump`, Git staging, or Git commit fails, the script exits immediately. This includes
a signing failure when `--sign-commits` is enabled. When the failure leaves a clean worktree, the
exit trap restores the starting branch. When changes remain, the script deliberately stays on the
affected branch and prints a warning rather than carrying those edits to another branch.

Inspect the state before continuing:

```bash
git status
git diff
git diff --cached
```

Resolve or commit those changes on the affected branch, then return to your original branch
manually. The script never resets, discards, force-pushes, or deletes work.
