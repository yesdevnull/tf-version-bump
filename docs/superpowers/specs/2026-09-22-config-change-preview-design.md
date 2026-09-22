# Config-change preview for the GitHub Actions example

## Goal

When a pull request changes a policy's control configuration or its caller workflow, the GitHub Actions example previews the result on a sample of real state branches: one branch per configured branch prefix, processed exactly as a live run processes it, with nothing published. The reviewer sees in the run summary the candidate that the merged configuration would produce on those branches before the pull request is merged.

The preview is a smoke test, not coverage. It shows the full candidate the merged configuration would produce on each sampled branch, which on a branch that lags the default branch's configuration includes changes the current configuration would already make. It does not isolate the pull request's own delta.

## Scope and assumptions

- The consuming repository is never forked; pull requests come from branches in the same repository, so the `TF_API_TOKEN` and `TERRAFORM_ENV` secrets are available to them. A pull request from a fork, or into a branch other than the default branch, skips the preview rather than failing.
- Anyone who can open such a pull request can already edit the workflows, so running Terraform with the registry token at pull-request time adds no new exposure. Accidental publication from an unmerged pull request is a new failure mode, so publication gains an independent guard (see [Publication guard](#publication-guard)).
- A pull-request run executes the merge commit's workflows and scripts. A pull request that also edits the scripts or the reusable workflow is previewed with the edited versions.
- Each caller previews only its own policy, triggered by its own configuration file or its own caller workflow file. A change to the scripts or the reusable workflow alone does not trigger a preview.
- Merging still starts the live run through each caller's existing `push` trigger, which remains limited to the configuration file. A merged change to the caller workflow alone takes effect at the next scheduled or manual run, as today. The config-validation workflow is unchanged.

## Verified GitHub Actions behaviour

The design depends on GitHub Actions behaviours that the documentation leaves open. Each was checked in a disposable private repository (`yesdevnull/tfvb-preview-probe`) on 2026-09-22 with sleep-only jobs that mirror the caller shape below. The probe's workflows also pass the pinned actionlint 1.7.12, and `yq` resolves their aliases.

1. **Job-level `queue: max` works.** GitHub's documentation describes `queue` only for workflow-level `concurrency`. Three dispatches of a job carrying `concurrency: {group, cancel-in-progress: false, queue: max}` that calls a reusable workflow ran one after another, each starting after the previous finished, and none was cancelled. The default `queue: single` would have cancelled the middle one while it was pending.
2. **A read-only preview job fails at startup.** A caller job declaring `permissions: contents: read` that calls a workflow whose `publish` job requests write permissions fails with `startup_failure`, even though `preview: true` means `publish` would be skipped. GitHub checks a called job's permissions whatever its `if`. The preview job therefore keeps the caller's permissions, and publication relies on two guards: the `publish` job's `if` and the [publication guard](#publication-guard).
3. **Job-level `cancel-in-progress: true` cancels an older preview.** On a pull request, a second push cancelled the in-progress preview for the first push, and the second run completed. In that run the live job and `publish` were both skipped, and the called workflow received `preview=true` through the aliased `with:` block.

GitHub's documentation confirms that `queue: max` combined with `cancel-in-progress: true` is a validation error, so the two are never combined in one `concurrency` block. It also confirms YAML anchors and aliases are supported in workflows. It says nothing about merge keys (`<<`), so the design does not use them.

## Design

### Callers

Each of `tf-version-bump-nonproduction.yml` and `tf-version-bump-production.yml` changes as follows:

- **Triggers.** Adds `pull_request: paths:` naming its own configuration file and its own workflow file. The `push`, `workflow_dispatch` and `schedule` triggers are unchanged.
- **Workflow-level concurrency.** Removed. It moves to the live job.
- **Two jobs.** Both call the reusable workflow, with the same `with:` and `secrets:` blocks. The live job defines them as anchors (`with: &policy-inputs`, `secrets: &policy-secrets`) and the preview job uses the aliases (`with: *policy-inputs`, `secrets: *policy-secrets`). Every policy value is therefore written once, and a pull request that edits a value, such as a tool pin, a prefix or a root, is previewed with it.
  - `automation`, the live job: its existing `if` (`github.ref` is `refs/heads/<default branch>`) is unchanged, because a pull-request run's ref is `refs/pull/<number>/merge` and never matches; it gains job-level `concurrency: {group: tf-version-bump-<policy>-<repository id>, cancel-in-progress: false, queue: max}`. Its behaviour is otherwise unchanged.
  - `preview`: `if: github.event_name == 'pull_request' && github.event.pull_request.head.repo.full_name == github.repository && github.event.pull_request.base.ref == github.event.repository.default_branch`, with job-level `concurrency: {group: tf-version-bump-<policy>-preview-<repository id>-<pull request number>, cancel-in-progress: true}`. It declares no job-level `permissions`, because a read-only caller job fails at startup (see [Verified GitHub Actions behaviour](#verified-github-actions-behaviour)).
- **The `preview` input.** The shared `with:` block includes `preview: ${{ github.event_name == 'pull_request' }}`, which is `false` for the live job and `true` for the preview job without either block differing. `branch_prefix`, `dry_run` and `terraform_init_upgrade` keep their existing event-guarded expressions, which evaluate to `''`, `false` and `false` on pull-request events.

A newer push to a pull request cancels that pull request's older preview, and a preview never waits behind or blocks a live run.

### Reusable workflow

- **New input.** `preview` (boolean, default `false`).
- **Discovery.** The discovery step passes `DISCOVERY_PREVIEW: ${{ inputs.preview }}`.
- **Publication.** The `publish` job's `if` becomes `${{ always() && needs.discover.result == 'success' && !inputs.preview }}`.
- **Summary.** The `Report processing result` step receives `PREVIEW: ${{ inputs.preview }}` through `env`, because the local harness runs step bodies without evaluating expressions. When `PREVIEW` is `true` and `candidate.patch` exists, the step appends it to the job summary under `#### Candidate patch`. The patch exists only for a changed, validated candidate, so there is none for failures or no-change results.
- **Shared rendering.** The existing fence-escalation and 64 KiB truncation code becomes one shell function in the step, `append_fenced_file <heading> <file> <noun>`, used for each updater log (`log`) and for the patch (`patch`); the noun names the file in the truncation notice. The patch holds only `.tf` changes, formatting changes and root lock files (`write_candidate`, `process-state-branch.sh`), never Terraform's logs, so it cannot carry a credential Terraform echoed.

### Discovery: sampling

`discover-state-branches.sh` gains one optional input, `DISCOVERY_PREVIEW`. Unset or `false` means discovery behaves exactly as it does today, including for the report workflow, which does not set it. Any value other than exact `true` or `false` is refused.

When it is `true`:

- **Guard.** The caller ref must match `^refs/pull/([1-9][0-9]*)/merge$`, instead of `refs/heads/<default branch>`. The captured pull request number is the sampling seed, so seed and ref cannot disagree. A pull-request ref without `DISCOVERY_PREVIEW=true` is still refused.
- **No manual prefix.** A non-empty `DISCOVERY_MANUAL_PREFIX` is refused, because a manual prefix only arises from `workflow_dispatch`.
- **Selection.** Prefix validation, exclusions and matching are unchanged. Each branch belongs to the first prefix it matches, as the existing loop assigns it.
- **Branch limit.** The 256-branch limit is checked against the full matched set before sampling, so a policy the live run would refuse fails its preview too.
- **Sampling.** For each prefix that owns at least one branch, its branches are sorted (the script already sets `LC_ALL=C`), and one is picked. The index is the first 15 hexadecimal digits of `sha256("<seed>\t<prefix>")`, read as an integer, modulo the number of branches the prefix owns. Fifteen digits stay within bash's signed 64-bit arithmetic, so the index is never negative.
- **Stability.** The pick is deterministic while the prefix's set of branches is unchanged. Adding, deleting or excluding a branch under the prefix may move it. Different pull requests are spread across a prefix's branches but may land on the same one.
- **Prefixes that own no branch.** In preview mode only, each such prefix produces `::warning::branch prefix '<prefix>' owns no branch to preview` on stderr, escaped as the existing exclusion warning is. A prefix owns no branch when none of its matches remain after exclusions and earlier prefixes. If no branch is matched at all, the existing "no remote branches matched" failure applies.
- **Output.** The matrix JSON shape is unchanged, so the `process` job consumes it unmodified. `prepare_contract` checks the control checkout's `HEAD` against `control_oid`, which is the merge commit in a pull-request run.

### Publication guard

`reconcile-state-branch.sh publish` gains required inputs `RECONCILE_CALLER_REF` (`${{ github.ref }}`) and `RECONCILE_DEFAULT_BRANCH` (`${{ github.event.repository.default_branch }}`). Before any Git or GitHub operation, it refuses to publish unless the caller ref is `refs/heads/<default branch>`, dry run or not. Publication is then guarded twice: by the `publish` job's `if` and by this check. Discovery does not claim to know whether a run can publish.

### Failure behaviour

A sampled branch whose update, initialisation, formatting or validation fails fails its `process` job, as it does today, so the pull request's check fails. The summary names the branch, classification, failed stage and root as it does now.

Because the sample is stable per pull request, a branch that already fails on the default branch's configuration fails every push and rerun of the preview. The README says that a red preview may be pre-existing, points at the branch's marked failure issue, and advises against making the preview a required check. An invalid configuration fails every sampled branch at the update stage with classification `branch-update`, while the config-validation workflow reports the cause.

## Testing

Following TDD, in `examples/github-actions/test.sh` unless noted.

**Discovery**

- A known-answer case: a fixed pull request number, prefix and branch list select a named branch, pinning the formula.
- One branch per prefix is selected, and exclusions are applied before sampling.
- A prefix that owns no branch warns in preview mode and not in live mode. The run continues when another prefix selects a branch, and fails when none does.
- The 256-branch limit fails a preview whose full matched set exceeds it.
- A pull-request ref is accepted with `DISCOVERY_PREVIEW=true` and refused without it. A malformed ref (`refs/pull/0/merge`, `refs/pull/7/head`) is refused, as are a non-boolean `DISCOVERY_PREVIEW` and a manual prefix in preview mode.
- The default-branch path and its matrix output are unchanged.

**Reusable workflow**

- Replace the exact `.publish.if` assertion in `workflow_publishes_after_processing_failures` with one requiring `always()`, `needs.discover.result == 'success'` and `!inputs.preview`. The existing mutants that drop `always()` still fail, and a new mutant that drops `!inputs.preview` fails too.
- `DISCOVERY_PREVIEW` is wired from `inputs.preview`, and `PREVIEW` reaches `Report processing result` through `env`.
- The summary step, run through `run_workflow_step`:
  - appends the patch in preview mode when `candidate.patch` exists;
  - omits it outside preview mode and when the file is absent;
  - escalates the fence past backticks in the patch;
  - truncates past 64 KiB with the existing notice.

**Callers**

- Each caller has the pull-request trigger for its own configuration and workflow file.
- The live job's `if` refuses pull-request events and non-default refs.
- The preview job's `if` requires a same-repository head and a default-branch base.
- The live job has job-level `queue: max` without `cancel-in-progress: true`, and the preview job has `cancel-in-progress: true` without `queue`.
- Both jobs resolve to identical `with:` and `secrets:` blocks, and the shared `preview` expression is event-derived.

**Publication guard** (`reconcile-test.sh`)

- Publication is refused for a pull-request ref and for another branch, and in dry-run mode too.
- The refusal happens before any Git or GitHub command.
- The existing default-branch cases pass with the new inputs.

A separate test-cleanup pass follows implementation.

## Documentation

Add a README section, "Preview on pull requests", that covers:
- what runs, and that nothing is published;
- how the sample is chosen and why it is stable;
- that the patch is the full candidate the merged configuration would produce, not the pull request's delta;
- that a failed sample fails the check and may be pre-existing, and that the check should not be required;
- that pull requests with a merge conflict run no preview, because GitHub does not run `pull_request` workflows for them;
- that the preview is not refreshed when the base or state branches move, only on `opened`, `synchronize` and `reopened`;
- that a branch-scoped `ignore_modules` entry is previewed only if its branch happens to be sampled;
- that a pull request editing the scripts is previewed with the edited scripts;
- that an invalid configuration appears as `branch-update` on every sampled branch.

Edit the passages the change makes false:
- `examples/github-actions/README.md`, the Install section's "Both callers run only from the default branch." The live job still does; the preview job runs for same-repository pull requests into the default branch.
- `examples/github-actions/README.md`, the Configure updates section's "Pull requests changing these files run a read-only config validation check; they do not process state branches or run Terraform."
- `examples/github-actions/README.md`, the Run and inspect section's description of the `process` summary, which gains the patch in a preview.
- `docs/ADVANCED-USAGE.md`, the GitHub Actions paragraph's "callers that run only from the default branch" and "full state-branch dry runs remain deferred".

Also in the README:
- The Install section notes that previews use the same secrets.
- The environment-variable section notes that a GitHub Environment with deployment-branch rules may withhold its secrets from `refs/pull/<number>/merge`, which fails or holds the preview.

## Size

Roughly:
- 40 lines of discovery shell;
- 10 lines of publication guard;
- 30 lines of caller and reusable workflow YAML, net of the shared rendering function;
- a comparable amount of harness tests;
- one README section, plus four passage edits.

This is in keeping with the example's brevity.
