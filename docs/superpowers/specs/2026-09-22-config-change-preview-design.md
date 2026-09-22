# Config-change preview for the GitHub Actions example

## Goal

When a pull request changes a policy's control configuration (`.github/tf-version-bump/nonproduction.yml` or `production.yml`), the GitHub Actions example previews the change on a sample of real state branches: one branch per configured branch prefix, processed exactly as a live run would process it, with nothing published. The reviewer sees in the run summary how the changed configuration would land on those branches before it is merged.

## Scope and assumptions

- The consuming repository is never forked; pull requests come from branches in the same repository, so the `TF_API_TOKEN` and `TERRAFORM_ENV` secrets are available to them. A pull request from a fork skips the preview rather than failing.
- Anyone who can open such a pull request can already edit the workflows, so running Terraform with the registry token at pull-request time adds no new exposure.
- Merging still starts the full live run through each caller's existing `push` trigger. The config-validation workflow is unchanged.
- Only the policy whose configuration the pull request changes is previewed, because each caller triggers on its own configuration path.

## Design

### Discovery: sampling and the caller-ref guard

`discover-state-branches.sh` gains one optional input, `DISCOVERY_SAMPLE_SEED`. When it is empty or unset, discovery behaves exactly as it does today.

When it is set:

- **Guard.** The caller ref must match `refs/pull/<digits>/merge` instead of `refs/heads/<default branch>`. The seed must be a positive decimal integer. A pull-request ref without a seed is still refused, so the guard is relaxed only in a run that cannot publish.
- **Selection.** Prefix validation, exclusions and matching are unchanged: each branch belongs to the first prefix it matches, as the existing loop assigns it.
- **Sampling.** For each prefix with at least one matched branch, the branches are sorted and one is picked at index `sha256("<seed>\t<prefix>") mod count`. The pick is stable for a pull request across pushes and reruns, and different prefixes and pull requests land on different branches.
- **Empty prefixes.** A prefix that matches no branch produces a `::warning::` annotation on stderr, as an unmatched exclusion does. If no prefix matches any branch, the existing "no remote branches matched" failure applies.
- **Output.** The matrix JSON shape is unchanged, so the `process` job needs no change to consume it. The 256-branch limit applies to the sample.

A manual `branch_prefix` does not arise, because pull-request runs pass none.

### Reusable workflow

- New input `preview` (boolean, default `false`).
- The discovery step passes `DISCOVERY_SAMPLE_SEED: ${{ inputs.preview && github.event.number || '' }}`.
- The `publish` job's `if` adds `!inputs.preview`, so a preview never changes refs, pull requests or issues.
- In a preview, the `Report processing result` step also writes `candidate.patch`, when it exists, to the job summary in a fenced block, truncated at 64 KiB with the same fence and truncation handling as the updater log. The patch holds only `.tf`, formatting and lock-file changes, never Terraform's logs, so it cannot carry a credential Terraform echoed.

### Callers

Each of `tf-version-bump-nonproduction.yml` and `tf-version-bump-production.yml`:

- Adds `pull_request: paths:` naming its own configuration file.
- Extends its job `if` to also accept `github.event_name == 'pull_request' && github.event.pull_request.head.repo.full_name == github.repository`.
- Passes `preview: ${{ github.event_name == 'pull_request' }}`.
- Suffixes its concurrency group with `-pr-<number>` on pull-request events, so a preview never queues behind or blocks a live run.
- Cancels an older preview for the same pull request when a newer push arrives, if GitHub's documentation confirms that `cancel-in-progress` can be combined with the `queue: max` key the callers use. If it cannot, previews queue per pull request instead. This is checked against GitHub's documentation during implementation, not assumed.

A pull-request run checks out the merge commit as the control checkout, so the preview uses the pull request's configuration and scripts. Each state branch is still checked out at its discovered commit.

### Failure behaviour

A sampled branch whose update, initialisation, formatting or validation fails fails its `process` job, as it does today, so the pull request's check fails. The summary names the branch, classification, failed stage and root as it does now.

## Testing

Following TDD, in `examples/github-actions/test.sh`:

- Discovery with a seed picks the same branches for the same seed and one branch per prefix.
- Exclusions are honoured before sampling.
- A prefix with no branches produces the warning and the run continues; no matches at all still fails.
- A pull-request ref is accepted with a seed and refused without one; a malformed seed is refused; the default-branch path is unchanged.
- Workflow structure: `publish` is gated on `!inputs.preview`; each caller triggers on pull requests for its own configuration path and passes `preview`.

A separate test-cleanup pass follows implementation.

## Documentation

- A README section, "Preview on pull requests", covering what runs, how the sample is chosen, that nothing is published and that a failed sample fails the check.
- The install section notes that previews use the same secrets and that fork pull requests skip the preview.

## Size

About 30 lines of discovery shell, 15 to 20 lines of workflow YAML, a comparable amount of harness tests and one README section, in keeping with the example's brevity.
