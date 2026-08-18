# GitHub Actions State-Branch Automation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development
> (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a copyable GitHub Actions example that safely updates, initialises, validates, and
opens one managed pull request or failure issue for each selected Terraform state branch.

**Architecture:** Two Bash helpers provide branch discovery and per-branch preparation,
validation, and reconciliation. A four-stage reusable workflow runs target-controlled Terraform
only in credential-free jobs, uploads the publishable candidate before provider execution, and
publishes from a fresh protected reconciliation runner. Production and non-production caller
workflows supply separate prefix policy, schedules, and configurations.

**Tech Stack:** Go 1.25, Bash, Git, `jq`, Terraform 1.15.5, GitHub CLI, GitHub Actions,
actionlint 1.7.12

**Spec:** `docs/superpowers/specs/2026-08-18-github-actions-branch-automation-design.md`

## Global Constraints

- Work on a feature branch or isolated worktree created with `superpowers:using-git-worktrees`;
  never implement directly on `main`.
- Use `/Users/dan/.codex/bin/codex-git` for every Git operation. Every created or rewritten commit
  must be signed; signing failure is a hard stop.
- Follow TDD for every behaviour change: write one failing test, run it and inspect the expected
  failure, implement the smallest fix, then rerun the focused and affected suites.
- Run `test-cleanup` as a separate subagent after implementation and before final verification.
- Keep the example under `examples/github-actions/`; consumers copy its `.github` directory into
  their default branch.
- Match branch names by literal prefixes only. A manual prefix may narrow but never widen the
  caller allow-list.
- Treat workflow contexts, inputs, refs, manifests, target files, and artefacts as untrusted data.
  Pass them into shell through `env:`, double-quote expansions, use full refspecs and `--`, create
  JSON with `jq --arg`, and create Markdown bodies through files.
- Every checkout uses `persist-credentials: false`. Discovery, preparation, and validation receive
  `contents: read` only and never receive publication or signing secrets.
- Run `terraform init -upgrade -backend=false -input=false -no-color` in preparation. Upload the
  immutable candidate before running `terraform validate` or any provider RPC.
- Validate provider-using candidates with
  `terraform init -backend=false -input=false -no-color -lockfile=readonly`; provider-free roots
  may omit `.terraform.lock.hcl`.
- Reconciliation executes neither Terraform nor target-supplied executables. It independently
  reproduces the `.tf` diff and accepts lock bytes only from the pre-provider candidate.
- Dry run performs real disposable updates, initialisation, and validation but never enters the
  publication environment or mutates refs, repository content, pull requests, or issues.
- State branches are read-only. Map `state/nonproduction/example` to
  `update_state/nonproduction/example`; naming alone never proves automation ownership.
- Rewrite and deletion operations use exact expected remote OIDs. Never use unconditional force.
- Built-in-token publication requires manual approval for generated PR checks. App mode is required
  for unattended required checks and keeps the effective `GITHUB_TOKEN` read-only.
- Optional SSH signing has no unsigned fallback after selection. Materialise the key only in
  reconciliation after candidate validation, verify the signature locally, and remove key files in
  unconditional cleanup.
- Pin every action to an immutable SHA with a release comment. Use these verified pins:

  | Action | SHA | Release |
  |---|---|---|
  | `actions/checkout` | `3d3c42e5aac5ba805825da76410c181273ba90b1` | v7.0.1 |
  | `actions/setup-go` | `b7ad1dad31e06c5925ef5d2fc7ad053ef454303e` | v7.0.0 |
  | `hashicorp/setup-terraform` | `dfe3c3f87815947d99a8997f908cb6525fc44e9e` | v4.0.1 |
  | `actions/upload-artifact` | `043fb46d1a93c77aae656e7c1c64a875d1fc6a0a` | v7.0.1 |
  | `actions/download-artifact` | `3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c` | v8.0.1 |
  | `actions/create-github-app-token` | `bcd2ba49218906704ab6c1aa796996da409d3eb1` | v3.2.0 |

- The checked-in workflows target GitHub.com. Documentation must warn that GitHub Enterprise
  Server consumers need compatible queue and artefact-action versions.
- Do not mock `gh` or GitHub API behaviour. Test deterministic payload and Git operations locally;
  verify tokens, PRs, issues, permissions, queueing, and event triggering against a disposable real
  GitHub repository.

---

### Task 1: Make Per-File CLI Failures Observable to Automation

**Files:**
- Modify: `main.go`
- Modify: `main_integration_test.go`
- Modify: `integration_config_test.go`
- Modify: `coverage_test.go`
- Modify: `terraform_provider_test.go`
- Modify: `cli_functions_test.go`
- Modify: `main_coverage_test.go`

**Interfaces:**
- Produces:
  - `type processingResult struct { updates int; failures int }`
  - `func (r processingResult) add(other processingResult) processingResult`
  - `func processFiles(files []string, updates []ModuleUpdate, flags *cliFlags) processingResult`
  - `func processTerraformVersion(files []string, version string, dryRun bool, outputFormat string) processingResult`
  - `func processProviderVersion(files []string, providerName, version string, dryRun bool, outputFormat string) processingResult`
  - `func exitAfterProcessingFailures(failures int)`
- Consumers: Tasks 3 and 7 rely on the CLI exiting non-zero after any file operation fails.

- [ ] **Step 1: Add a failing aggregate-result test**

  Add `TestProcessFiles_ReportsFailuresAfterLaterFilesRun` to `main_integration_test.go`. Create one
  invalid HCL file followed by one valid matching module file, capture log output, and assert the
  wished-for result:

  ```go
  result := processFiles(
      []string{invalidFile, validFile},
      []ModuleUpdate{{Source: "terraform-aws-modules/vpc/aws", Version: "2.0.0"}},
      &cliFlags{output: "text"},
  )

  if result.updates != 1 || result.failures != 1 {
      t.Fatalf("processFiles() = %+v, want 1 update and 1 failure", result)
  }
  updated, err := os.ReadFile(validFile)
  if err != nil {
      t.Fatal(err)
  }
  if !strings.Contains(string(updated), `version = "2.0.0"`) {
      t.Fatalf("later valid file was not processed: %s", updated)
  }
  ```

- [ ] **Step 2: Run the focused test and verify RED**

  Run: `go test -run TestProcessFiles_ReportsFailuresAfterLaterFilesRun -v`

  Expected: compilation fails because `processFiles` still returns `int`.

- [ ] **Step 3: Return update and failure counts from every processing loop**

  Add this domain result near the processing functions:

  ```go
  type processingResult struct {
      updates  int
      failures int
  }

  func (r processingResult) add(other processingResult) processingResult {
      r.updates += other.updates
      r.failures += other.failures
      return r
  }
  ```

  Change all three file-processing loops to increment `failures` when an update function returns an
  error, continue to later files, and return the struct. Preserve the existing per-file error log.
  Update existing tests and summaries to read `.updates` rather than expecting an `int`.

- [ ] **Step 4: Add the failing non-zero-exit test**

  Add `TestExitAfterProcessingFailures_UsesDeterministicCount` to `cli_functions_test.go`. Capture
  stderr and replace `exitFunc` with the existing panic-based test helper. Assert exit code `1` and
  this exact final line:

  ```text
  Failed to process 2 file operation(s)
  ```

- [ ] **Step 5: Run the exit test and verify RED**

  Run: `go test -run TestExitAfterProcessingFailures_UsesDeterministicCount -v`

  Expected: compilation fails because `exitAfterProcessingFailures` is undefined.

- [ ] **Step 6: Exit only after summaries and all file processing finish**

  Implement:

  ```go
  func exitAfterProcessingFailures(failures int) {
      if failures == 0 {
          return
      }
      fmt.Fprintf(os.Stderr, "Failed to process %d file operation(s)\n", failures)
      exitFunc(1)
  }
  ```

  Accumulate results across Terraform, provider, and module operations in config mode. Print the
  existing summary first, then call `exitAfterProcessingFailures`. Apply the same ordering to each
  direct CLI mode.

- [ ] **Step 7: Run focused and full Go verification**

  Run:

  ```text
  go test -run 'TestProcessFiles_ReportsFailuresAfterLaterFilesRun|TestExitAfterProcessingFailures_UsesDeterministicCount' -v
  make test-coverage
  golangci-lint run --timeout=5m
  go build ./...
  ```

  Expected: all pass; race coverage remains at least 80%; no warnings or uncaptured error output.

- [ ] **Step 8: Create a signed checkpoint commit**

  Write `/tmp/tfvb-task-1-commit.md` with:

  ```text
  fix: report aggregate file processing failures
  ```

  Stage only the files listed in this task and run:

  ```text
  /Users/dan/.codex/bin/codex-git add main.go main_integration_test.go integration_config_test.go coverage_test.go terraform_provider_test.go cli_functions_test.go main_coverage_test.go
  /Users/dan/.codex/bin/codex-git commit -F /tmp/tfvb-task-1-commit.md
  /Users/dan/.codex/bin/codex-git log -1 --show-signature --format=fuller
  ```

---

### Task 2: Discover Immutable State-Branch Inputs

**Files:**
- Create: `examples/github-actions/.github/scripts/discover-state-branches.sh`
- Create: `examples/github-actions/github-actions_test.sh`

**Interfaces:**
- Produces CLI:

  ```text
  discover-state-branches.sh \
    --repository <path> \
    --allowed-prefixes-file <path> \
    --narrow-prefix <literal-or-empty> \
    --control-oid <40-or-64-hex-oid> \
    --output <matrix-json-path>
  ```

- Produces JSON:

  ```json
  {
    "control_oid": "<oid>",
    "include": [
      {
        "branch": "state/nonproduction/example",
        "base_oid": "<oid>",
        "artifact_key": "<sha256-of-full-ref>"
      }
    ]
  }
  ```

- Consumers: Task 5 passes `.include` to all three per-branch matrices.

- [ ] **Step 1: Build the shell-test harness and write discovery RED cases**

  Create a Bash test that uses `mktemp -d`, real Git repositories, and bare remotes. Add focused
  tests for:

  - literal allow-list matches and manual narrowing;
  - widening rejection;
  - overlapping-prefix deduplication and bytewise sorting;
  - no matches;
  - exact base OIDs and one caller-supplied control OID;
  - hostile valid refs containing slashes, semicolons, quotes, `$()`, leading dashes, Unicode, and
    percent signs without command evaluation.

  The key assertion should use `jq -e`, for example:

  ```bash
  jq -e '
    .control_oid == $control_oid and
    [.include[].branch] == [
      "state/nonproduction/a",
      "state/nonproduction/b"
    ]
  ' --arg control_oid "$control_oid" "$matrix_file" >/dev/null ||
      fail "discovery output was not deterministic"
  ```

- [ ] **Step 2: Run the discovery tests and verify RED**

  Run: `examples/github-actions/github-actions_test.sh discovery`

  Expected: FAIL because `discover-state-branches.sh` does not exist.

- [ ] **Step 3: Implement literal-prefix discovery**

  The script must:

  - use `git -C "$repository" ls-remote --heads origin` rather than checkout fetch depth;
  - validate prefix lines and optional narrowing without `eval` or glob matching;
  - compare with Bash literal slicing:

    ```bash
    [[ "$branch" == "$prefix"* ]]
    ```

  - hold refs as tab-separated or NUL-delimited data, never executable source;
  - compute `artifact_key` with `shasum -a 256` on macOS or `sha256sum` on Linux through one
    documented helper;
  - build entries with `jq -n --arg`, sort with `LC_ALL=C`, deduplicate, reject an empty result, and
    write atomically to the requested output path;
  - provide complete `--help` output and stage-specific errors.

- [ ] **Step 4: Run syntax and discovery tests GREEN**

  Run:

  ```text
  bash -n examples/github-actions/.github/scripts/discover-state-branches.sh
  examples/github-actions/github-actions_test.sh discovery
  ```

  Expected: PASS with no unexpected output.

- [ ] **Step 5: Create a signed checkpoint commit**

  Commit message file content:

  ```text
  feat: discover immutable state branches
  ```

  Run:

  ```text
  /Users/dan/.codex/bin/codex-git add examples/github-actions/.github/scripts/discover-state-branches.sh examples/github-actions/github-actions_test.sh
  /Users/dan/.codex/bin/codex-git commit -F /tmp/tfvb-task-2-commit.md
  /Users/dan/.codex/bin/codex-git log -1 --show-signature --format=fuller
  ```

---

### Task 3: Prepare Immutable Candidates Before Provider Execution

**Files:**
- Create: `examples/github-actions/.github/scripts/process-state-branch.sh`
- Modify: `examples/github-actions/github-actions_test.sh`
- Create: `examples/github-actions/testdata/hostile-provider.sh`

**Interfaces:**
- Produces modes:

  ```text
  process-state-branch.sh prepare --control <path> --target <path> --branch <ref> \
    --base-oid <oid> --control-oid <oid> --config <relative-path> \
    --directories-file <path> --tf-version-bump <path> --terraform <path> \
    --bundle <directory>

  process-state-branch.sh validate --control <path> --target <path> \
    --bundle <directory> --outcome <directory> --terraform <path>
  ```

- Produces successful `manifest.json` schema:

  ```json
  {
    "schema_version": 1,
    "control_oid": "<oid>",
    "state_branch": "<ref>",
    "base_oid": "<oid>",
    "artifact_key": "<sha256-of-ref>",
    "classification": "success",
    "directories": [{"path": ".", "provider_dependencies": true}],
    "files": [{"path": "main.tf", "mode": "100644", "kind": "terraform", "sha256": "<hex>"}],
    "patch_sha256": "<hex>"
  }
  ```

- Classification values are exactly `automation`, `shared/init`, `branch-update`,
  `branch-validation`, and `success`.
- A failed preparation bundle contains `manifest.json` and logs but no `candidate.patch`.
- A validation outcome contains `schema_version`, `candidate_manifest_sha256`, `classification`,
  optional `directory`, and log names; it never contains source or lock bytes.
- Consumers: Task 4 reconciles only a successful candidate plus its exact bound outcome.

- [ ] **Step 1: Write preparation RED cases**

  Extend the test harness with real `tf-version-bump` and Terraform executables. Add cases for:

  - root-directory update and lock staging;
  - two sequential Terraform roots;
  - provider-free success without `.terraform.lock.hcl`;
  - required provider lock creation and ignored-lock rejection;
  - duplicate, missing, absolute, escaping, and symlinked directory/config paths;
  - symlinked `.tf` and lock files;
  - an unexpected changed path;
  - aggregate CLI failure after a later valid `.tf` file changes;
  - init failure classified `shared/init` with logs and no candidate patch.

- [ ] **Step 2: Run preparation tests and verify RED**

  Run: `examples/github-actions/github-actions_test.sh prepare`

  Expected: FAIL because `process-state-branch.sh` is absent.

- [ ] **Step 3: Implement containment and preflight helpers**

  In `process-state-branch.sh`, implement and reuse:

  ```text
  canonical_under_root <root> <relative-path>
  require_regular_file <root> <relative-path>
  load_unique_directories <target-root> <directories-file>
  enumerate_direct_tf_files <directory>
  classify_changed_paths <target-root> <directories-json>
  sha256_file <path>
  ```

  `canonical_under_root` must reject absolute paths, `..`, missing paths, symlinks, and canonical
  paths outside the root. Use `lstat`-equivalent shell checks (`[[ -L ... ]]`, `[[ -f ... ]]`) before
  any write and repeat checks for every staged path.

- [ ] **Step 4: Implement preparation and immutable bundle creation**

  For each canonical directory:

  ```text
  tf-version-bump -pattern '*.tf' -config <control-config>
  terraform -chdir=<directory> init -upgrade -backend=false -input=false -no-color
  ```

  Inspect canonical `.terraform/providers` before deleting `.terraform/`. Require a regular lock
  when any provider package exists; otherwise mark the root provider-free. Stage only declared
  direct `.tf` files and required lock files in the disposable target index, then create a binary
  full-index patch from the index so new lock files are included. Create every JSON value with
  `jq --arg`, hash the manifest payload, patch, and declared files, and write logs with bounded
  routine output.

- [ ] **Step 5: Write validation and hostile-provider RED cases**

  Add tests that:

  - copy a successful preparation bundle aside, record all hashes, and validate from a fresh target
    checkout;
  - reject missing, duplicate, traversal, symlink-mode, hash-mismatched, ref-mismatched, and
    candidate-digest-mismatched artefacts;
  - install `hostile-provider.sh` through a temporary Terraform filesystem mirror. Its process-start
    code tries to overwrite the validation checkout lock, alter a supplied control path, append to
    `GITHUB_ENV` and `GITHUB_PATH`, and create a fake outcome before exiting with an invalid plugin
    handshake;
  - assert the original preparation bundle hashes are unchanged and the outcome is
    `branch-validation` or `automation`, never `success`.

- [ ] **Step 6: Run validation tests and verify RED**

  Run: `examples/github-actions/github-actions_test.sh validate`

  Expected: FAIL because validate mode is not implemented.

- [ ] **Step 7: Implement fresh validation without publishable output**

  Validate the candidate manifest and patch before applying them to a fresh exact-base checkout.
  For provider roots run:

  ```text
  terraform -chdir=<directory> init -backend=false -input=false -no-color -lockfile=readonly
  terraform -chdir=<directory> validate -no-color
  ```

  Omit `-lockfile=readonly` only for manifest-declared provider-free roots. Bind the outcome to the
  SHA-256 of the exact candidate manifest. Write only outcome JSON and captured logs, then discard
  the validation checkout. Never copy its `.tf` or lock files back to the bundle.

- [ ] **Step 8: Run preparation and validation GREEN**

  Run:

  ```text
  bash -n examples/github-actions/.github/scripts/process-state-branch.sh
  bash -n examples/github-actions/testdata/hostile-provider.sh
  examples/github-actions/github-actions_test.sh prepare
  examples/github-actions/github-actions_test.sh validate
  ```

  Expected: PASS; the hostile provider cannot change the saved candidate.

- [ ] **Step 9: Create a signed checkpoint commit**

  Commit message file content:

  ```text
  feat: prepare and validate state branch candidates
  ```

  Run:

  ```text
  /Users/dan/.codex/bin/codex-git add examples/github-actions/.github/scripts/process-state-branch.sh examples/github-actions/github-actions_test.sh examples/github-actions/testdata/hostile-provider.sh
  /Users/dan/.codex/bin/codex-git commit -F /tmp/tfvb-task-3-commit.md
  /Users/dan/.codex/bin/codex-git log -1 --show-signature --format=fuller
  ```

---

### Task 4: Reconcile Owned Branches, Pull Requests, and Issues

**Files:**
- Modify: `examples/github-actions/.github/scripts/process-state-branch.sh`
- Modify: `examples/github-actions/github-actions_test.sh`

**Interfaces:**
- Adds mode:

  ```text
  process-state-branch.sh reconcile --control <path> --target <path> \
    --bundle <directory> --outcome <directory-or-empty> --remote origin \
    --repository <owner/name> --run-url <url> --token-mode <builtin|app> \
    --bot-login <github-actions[bot]-or-app-slug[bot]> \
    --signing-key <path-or-empty> --author-name <name-or-empty> \
    --author-email <email-or-empty>
  ```

- Consumes publication credential only through `GH_TOKEN` in reconcile mode.
- Produces update ref `update_<state-branch>`.
- Produces commit trailers:

  ```text
  TF-Version-Bump-Managed: true
  TF-Version-Bump-State-Branch: <full-ref>
  TF-Version-Bump-Base-OID: <oid>
  TF-Version-Bump-Control-OID: <oid>
  ```

- Produces PR/issue marker `<!-- tf-version-bump-managed:sha256:<artifact-key> -->`.

- [ ] **Step 1: Write reconciliation safety RED cases**

  Source the script from the harness and test its real Git helpers against bare remotes. Cover:

  - independent reproduction of `.tf` changes before applying candidate lock bytes;
  - base movement between validation and publication;
  - stable automation-branch creation and regeneration;
  - unowned existing ref refusal;
  - ownership requiring both exact commit trailers and the marked PR record supplied to the helper;
  - update force-with-lease mismatch;
  - deletion expected-old-OID mismatch;
  - no-change owned-ref deletion and obsolete PR payload;
  - success after a previous marked failure issue;
  - deterministic validation failure after a previous marked PR;
  - PR and issue bodies containing hostile ref text as data.

  Do not stub or mock `gh`; pass fixture JSON into pure selection/body functions and leave actual
  API calls to real-service acceptance.

- [ ] **Step 2: Run reconciliation tests and verify RED**

  Run: `examples/github-actions/github-actions_test.sh reconcile`

  Expected: FAIL because reconcile mode and ownership helpers are undefined.

- [ ] **Step 3: Implement candidate revalidation and Git ownership**

  Reconciliation must re-check both artefacts, bind the outcome to the candidate digest, fresh
  checkout the exact control/base OIDs, rerun only `tf-version-bump`, and require the `.tf` hashes to
  match. Apply only declared lock hunks from the pre-provider patch and verify the complete result.

  Before changing an existing update ref, fetch its exact OID and require matching trailers plus a
  marked PR. Use exact leases:

  ```bash
  git push --force-with-lease="refs/heads/$update_branch:$expected_oid" \
      origin "HEAD:refs/heads/$update_branch"
  git push --force-with-lease="refs/heads/$update_branch:$expected_oid" \
      origin ":refs/heads/$update_branch"
  ```

  Use an empty expected OID only when creating a ref proven absent. Never retry without the lease.

- [ ] **Step 4: Implement deterministic GitHub payloads and lifecycle calls**

  Render PR and issue bodies to files with `jq --arg` and bounded log tails. Enumerate API results
  with `gh api --paginate --method GET`, then select exact base/head refs and exact hidden markers;
  never rely on a fuzzy title search. Use `gh pr create/edit/close` and `gh issue create/edit/close`
  only after exact selection. A deterministic update/validation failure closes the stale marked PR,
  lease-deletes its owned branch, and updates one marked issue. Init/automation failures leave the
  prior PR untouched and fail the job. For unsigned commits, query the exact bot login with
  `gh api "/users/$bot_login" --jq .id` and construct
  `<id>+<bot-login>@users.noreply.github.com`; do not hard-code or guess the numeric ID.

- [ ] **Step 5: Write signing RED cases**

  Add tests for:

  - unsigned commit when no key is supplied;
  - signed commit with an ephemeral Ed25519 key and explicit identity;
  - local signature verification with a generated allowed-signers file;
  - partial identity/key rejection;
  - signing failure with no unsigned fallback;
  - unconditional private-key and allowed-signers cleanup.

- [ ] **Step 6: Run signing tests and verify RED**

  Run: `examples/github-actions/github-actions_test.sh signing`

  Expected: FAIL until reconcile signing is implemented.

- [ ] **Step 7: Implement repository-scoped optional SSH signing**

  Configure only the disposable target repository:

  ```bash
  git -C "$target" config gpg.format ssh
  git -C "$target" config user.signingkey "$temporary_key"
  git -C "$target" config commit.gpgsign true
  ```

  Derive the public key with `ssh-keygen -y`, create an allowed-signers file for the exact author
  email, commit with the stable subject, and verify with `git verify-commit`. A trap removes both
  files on every exit path.

- [ ] **Step 8: Run all local script tests GREEN**

  Run:

  ```text
  examples/github-actions/github-actions_test.sh reconcile
  examples/github-actions/github-actions_test.sh signing
  examples/github-actions/github-actions_test.sh all
  ```

  Expected: PASS; no GitHub network mutation occurs.

- [ ] **Step 9: Create a signed checkpoint commit**

  Commit message file content:

  ```text
  feat: reconcile managed state branch updates
  ```

  Run:

  ```text
  /Users/dan/.codex/bin/codex-git add examples/github-actions/.github/scripts/process-state-branch.sh examples/github-actions/github-actions_test.sh
  /Users/dan/.codex/bin/codex-git commit -F /tmp/tfvb-task-4-commit.md
  /Users/dan/.codex/bin/codex-git log -1 --show-signature --format=fuller
  ```

---

### Task 5: Wire the Four-Stage Reusable Workflow

**Files:**
- Create: `examples/github-actions/.github/workflows/tf-version-bump-reusable.yml`
- Modify: `examples/github-actions/github-actions_test.sh`

**Interfaces:**
- Implements these exact `workflow_call` inputs: `allowed_branch_prefixes`, `branch_prefix`,
  `config_path`, `terraform_directories`, `terraform_version`, `tf_version_bump_version`,
  `go_version`, `dry_run`, `max_parallel`, `commit_author_name`, `commit_author_email`,
  `github_app_client_id`, and `publication_environment`.
- Exposes jobs `discover`, `prepare`, `validate`, and `reconcile`.
- Consumes `TF_VERSION_BUMP_GITHUB_APP_PRIVATE_KEY` and
  `TF_VERSION_BUMP_COMMIT_SIGNING_PRIVATE_KEY` only from the reconciliation environment.

- [ ] **Step 1: Add failing workflow-structure assertions**

  Extend the harness to copy the example `.github` tree into a temporary consumer repository and
  assert with actionlint plus exact `rg` checks that:

  - all required inputs and defaults exist;
  - manual dispatch from a non-default ref fails before matrix creation;
  - the shared tf-version-bump config is validated once against a trusted temporary fixture before
    branch discovery;
  - discovery/preparation/validation have `contents: read` only;
  - reconciliation declares the three write scopes but receives the caller's effective ceiling;
  - every checkout disables persisted credentials;
  - matrices use `fail-fast: false` and the `max_parallel` input;
  - validate and reconcile use `if: always()` as required;
  - dry run cannot enter the reconciliation environment;
  - no `${{ matrix.branch }}`, input, ref, or manifest expression appears directly inside `run:`;
  - every `uses:` value is one of the exact pinned SHAs in Global Constraints.

- [ ] **Step 2: Run workflow structure tests and verify RED**

  Run: `examples/github-actions/github-actions_test.sh workflow`

  Expected: FAIL because the reusable workflow does not exist.

- [ ] **Step 3: Implement discovery and credential-free matrices**

  Reject manual execution unless `github.ref` equals
  `refs/heads/${{ github.event.repository.default_branch }}`. Use exact control checkout
  `github.sha`, validate the shared config once with the pinned CLI against a trusted temporary
  Terraform fixture, emit the discovery JSON through `$GITHUB_OUTPUT`, and pass all untrusted matrix
  values through step `env:`. Install Go and Terraform from their exact workflow inputs, with
  `terraform_wrapper: false`, and install `tf-version-bump` from its exact tag input.

  Preparation must always upload one collision-resistant artefact. Validation runs with `always()`,
  downloads only the expected successful candidate, uploads only its bound outcome/log artefact,
  and never receives secrets or write permissions. Preserve script exit status through
  `continue-on-error` steps and a final status step whose value arrives through `env:`.

- [ ] **Step 4: Implement protected reconciliation**

  Run only when `dry_run == false`, after all preparation and validation matrix jobs. Set
  `environment.name` from `publication_environment`, download only exact run/branch artefact names,
  and validate before credential creation.

  For App mode use the pinned token action with:

  ```yaml
  client-id: ${{ inputs.github_app_client_id }}
  private-key: ${{ secrets.TF_VERSION_BUMP_GITHUB_APP_PRIVATE_KEY }}
  permission-contents: write
  permission-pull-requests: write
  permission-issues: write
  ```

  Leave `owner` and `repositories` unset so the token is scoped to the current repository. Pass
  either the App token or built-in token as `GH_TOKEN` only to exact identity/publication steps.
  Pass `${{ steps.app-token.outputs.app-slug }}[bot]` as the App bot login and
  `github-actions[bot]` as the built-in bot login through `env:` rather than interpolating either
  value into shell source.
  After artefact validation, reject an App client ID without a private key, a private key without a
  client ID, or a signing key without both explicit commit identity inputs. Materialise the signing
  secret immediately before reconcile and remove it in `if: always()` cleanup.

- [ ] **Step 5: Validate workflow syntax GREEN**

  Run actionlint 1.7.12 against the temporary consumer root with only this exact temporary ignore:

  ```text
  -ignore '^unexpected key "queue" for "concurrency" section\.'
  ```

  Then run: `examples/github-actions/github-actions_test.sh workflow`

  Expected: PASS with no other ignored diagnostics.

- [ ] **Step 6: Create a signed checkpoint commit**

  Commit message file content:

  ```text
  feat: add reusable state branch workflow
  ```

  Run:

  ```text
  /Users/dan/.codex/bin/codex-git add examples/github-actions/.github/workflows/tf-version-bump-reusable.yml examples/github-actions/github-actions_test.sh
  /Users/dan/.codex/bin/codex-git commit -F /tmp/tfvb-task-5-commit.md
  /Users/dan/.codex/bin/codex-git log -1 --show-signature --format=fuller
  ```

---

### Task 6: Add Production and Non-Production Consumers

**Files:**
- Create: `examples/github-actions/.github/workflows/tf-version-bump-nonproduction.yml`
- Create: `examples/github-actions/.github/workflows/tf-version-bump-production.yml`
- Create: `examples/github-actions/.github/tf-version-bump/nonproduction.yml`
- Create: `examples/github-actions/.github/tf-version-bump/production.yml`
- Modify: `examples/github-actions/github-actions_test.sh`

**Interfaces:**
- Non-production prefixes:

  ```text
  state/nonproduction/
  state/staging/
  aws-state/nonproduction/
  ```

- Production prefixes:

  ```text
  state/production/
  aws-state/production/
  ```

- Both callers expose `branch_prefix` string and `dry_run` Boolean (`false`) manual inputs.

- [ ] **Step 1: Add caller-policy RED assertions**

  Assert exact schedules, timezones, prefix sets, config paths, tool pins, manual inputs, separate
  concurrency groups, `queue: max`, no in-progress cancellation, and local reusable-workflow paths.
  Assert scheduled calls supply an empty narrowing prefix and `dry_run: false`.

- [ ] **Step 2: Run caller tests and verify RED**

  Run: `examples/github-actions/github-actions_test.sh callers`

  Expected: FAIL because caller workflows/configs are missing.

- [ ] **Step 3: Create built-in-token caller examples**

  The checked-in callers use built-in-token mode with job permissions:

  ```yaml
  contents: write
  pull-requests: write
  issues: write
  ```

  They pass exact illustrative versions `go_version: '1.25.0'`,
  `terraform_version: '1.15.5'`, and `tf_version_bump_version: 'v1.0.0-rc.6'`, use
  `terraform_directories: '.'`, and leave `github_app_client_id` empty. README instructions in Task
  8 show the deliberate three-permission change to `contents: read` plus App client ID when
  switching modes; do not imply runtime permission toggling.

  Use schedules:

  ```yaml
  # Non-production
  - cron: '17 4 * * *'
    timezone: Australia/Melbourne

  # Production
  - cron: '43 4 * * 0'
    timezone: Australia/Melbourne
  ```

- [ ] **Step 4: Add separate illustrative YAML configs**

  Keep the configs valid under the existing schema and deliberately different so consumers can see
  that production and non-production policy are independent. Use syntax examples, not claims that
  module/provider versions are recommendations.

- [ ] **Step 5: Run callers and actionlint GREEN**

  Run:

  ```text
  examples/github-actions/github-actions_test.sh callers
  examples/github-actions/github-actions_test.sh workflow
  ```

  Expected: PASS, including timezone and local reusable-path validation.

- [ ] **Step 6: Create a signed checkpoint commit**

  Commit message file content:

  ```text
  feat: add state branch workflow consumers
  ```

  Run:

  ```text
  /Users/dan/.codex/bin/codex-git add examples/github-actions/.github/workflows/tf-version-bump-nonproduction.yml examples/github-actions/.github/workflows/tf-version-bump-production.yml examples/github-actions/.github/tf-version-bump/nonproduction.yml examples/github-actions/.github/tf-version-bump/production.yml examples/github-actions/github-actions_test.sh
  /Users/dan/.codex/bin/codex-git commit -F /tmp/tfvb-task-6-commit.md
  /Users/dan/.codex/bin/codex-git log -1 --show-signature --format=fuller
  ```

---

### Task 7: Integrate Local Validation into Make and CI

**Files:**
- Modify: `Makefile`
- Modify: `.github/workflows/ci.yml`
- Modify: `examples/github-actions/github-actions_test.sh`

**Interfaces:**
- Produces `make test-github-actions`.
- CI runs the example suite once on Go 1.25 after installing Terraform 1.15.5.

- [ ] **Step 1: Add a failing Make target test**

  Add a harness assertion that `make -n test-github-actions` resolves to the maintained test script
  and does not silently skip actionlint or either helper's `bash -n` checks.

- [ ] **Step 2: Run the target assertion and verify RED**

  Run: `examples/github-actions/github-actions_test.sh repository-integration`

  Expected: FAIL because the Make target is absent.

- [ ] **Step 3: Add the Make target and CI step**

  Add `test-github-actions` to `.PHONY` and help, with one recipe invoking
  `examples/github-actions/github-actions_test.sh all`. In CI's Go 1.25 matrix entry, install
  Terraform with the pinned setup action and `terraform_wrapper: false`, then run the Make target.
  Keep the existing `update-branches_test.sh` step.

- [ ] **Step 4: Run repository integration GREEN**

  Run:

  ```text
  examples/github-actions/github-actions_test.sh repository-integration
  make test-github-actions
  actionlint
  ```

  Expected: PASS with only the exact documented queue diagnostic filtered inside the example
  validator; repository workflows produce no actionlint output.

- [ ] **Step 5: Create a signed checkpoint commit**

  Commit message file content:

  ```text
  ci: validate GitHub Actions automation example
  ```

  Run:

  ```text
  /Users/dan/.codex/bin/codex-git add Makefile .github/workflows/ci.yml examples/github-actions/github-actions_test.sh
  /Users/dan/.codex/bin/codex-git commit -F /tmp/tfvb-task-7-commit.md
  /Users/dan/.codex/bin/codex-git log -1 --show-signature --format=fuller
  ```

---

### Task 8: Document Installation, Security, and Real-Service Acceptance

**Files:**
- Create: `examples/github-actions/README.md`
- Modify: `examples/README.md`
- Modify: `docs/ADVANCED-USAGE.md`
- Modify: `README.md`

**Interfaces:**
- Documents the copy boundary: `examples/github-actions/.github/` to consumer `.github/`.
- Documents fixed secrets:
  - `TF_VERSION_BUMP_GITHUB_APP_PRIVATE_KEY`
  - `TF_VERSION_BUMP_COMMIT_SIGNING_PRIVATE_KEY`
- Documents the complete disposable-repository acceptance transcript required by the spec.

- [ ] **Step 1: Write documentation acceptance checks before prose**

  Extend the harness to require links and headings for installation, built-in token, App mode,
  signing, single/multiple roots, dry run, lock files/provider-free roots, ownership/leases,
  troubleshooting, orphan cleanup, GHES compatibility, and real-service acceptance.

- [ ] **Step 2: Run documentation checks and verify RED**

  Run: `examples/github-actions/github-actions_test.sh documentation`

  Expected: FAIL because the example README and cross-links are absent.

- [ ] **Step 3: Write the consumer guide**

  Include exact copy commands, files to customise, protected environment setup, default-branch
  restrictions, trusted-dispatch assumptions, repository setting for Actions-created PRs, exact
  version pins, and prefix/config separation.

  Clearly distinguish:

  - built-in mode: three write permissions, no App client ID, suppressed push workflows, and
    approval-required PR checks;
  - App mode: `contents: read` caller ceiling, client ID input, protected App key, and unattended PR
    checks;
  - unsigned mode versus explicit SSH signing with non-interactive key and registered public key;
  - required lock for installed providers versus valid provider-free absence;
  - GitHub.com support versus GHES queue and artefact-action compatibility checks.

- [ ] **Step 4: Document deterministic recovery and deferred orphan cleanup**

  Explain stable refs, ownership trailers/markers, state/base OIDs, update/deletion leases, unowned
  collisions, stale-base failures, no-change cleanup, and the manual dry-run-first procedure for
  orphaned refs/issues. Do not add automatic garbage collection.

- [ ] **Step 5: Write the real-service acceptance procedure**

  Provide exact steps for a disposable repository covering:

  - matching/non-matching branches, provider and provider-free roots;
  - default-branch-only protected publication environment;
  - built-in and App permission summaries;
  - alternate-ref rejection;
  - hostile-provider candidate isolation;
  - success, idempotence, validation failure/issue, recovery, and obsolete PR cleanup;
  - three rapid queued dispatches;
  - built-in approval-required checks versus App automatic checks;
  - dry-run no-mutation comparison;
  - unowned ref, lease races, and state-base movement;
  - optional signed commit locally and GitHub-verified.

  State explicitly that whoever executes acceptance must retain the terminal transcript and inspect
  repository refs, PRs, issues, Actions permissions, and artefacts before claiming completion.

- [ ] **Step 6: Add concise cross-links**

  Add the GitHub Actions example to `examples/README.md`, distinguish it from local sequential
  `update-branches.sh` in `docs/ADVANCED-USAGE.md`, and update the root README documentation table
  without duplicating the consumer guide.

- [ ] **Step 7: Run documentation checks GREEN**

  Run:

  ```text
  examples/github-actions/github-actions_test.sh documentation
  /Users/dan/.codex/bin/codex-git diff --check
  ```

  Expected: PASS; no placeholders, broken relative links, or trailing whitespace.

- [ ] **Step 8: Create a signed checkpoint commit**

  Commit message file content:

  ```text
  docs: explain state branch Actions automation
  ```

  Run:

  ```text
  /Users/dan/.codex/bin/codex-git add examples/github-actions/README.md examples/README.md docs/ADVANCED-USAGE.md README.md
  /Users/dan/.codex/bin/codex-git commit -F /tmp/tfvb-task-8-commit.md
  /Users/dan/.codex/bin/codex-git log -1 --show-signature --format=fuller
  ```

---

### Task 9: Cleanup, Full Verification, and Real-Service Gate

**Files:**
- Review all files changed by Tasks 1-8.
- Modify only files required by findings from cleanup/review.

**Interfaces:**
- Produces the evidence required by the design completion criteria.

- [ ] **Step 1: Run the mandatory independent test-cleanup pass**

  Commit or stash a clean implementation checkpoint, then invoke the `test-cleanup` skill as a
  fresh subagent. It must classify every Go and shell test changed on the branch, remove only
  low-value duplication, rerun affected suites, and signed-commit any cleanup. Do not let an
  implementation agent clean its own tests.

- [ ] **Step 2: Request an adversarial implementation review**

  Use `superpowers:requesting-code-review` after cleanup. Require the reviewer to compare the branch
  against the design and concentrate on credential boundaries, artefact trust, hostile refs/paths,
  provider execution, leases, ownership, dry-run mutations, and GitHub event behaviour. Resolve all
  verified findings with TDD and signed commits.

- [ ] **Step 3: Run the full local verification gate**

  Run each command separately and inspect its complete output:

  ```text
  go mod download
  go mod verify
  go build ./...
  make test-coverage
  golangci-lint run --timeout=5m
  examples/update-branches_test.sh
  make test-github-actions
  actionlint
  /Users/dan/.codex/bin/codex-git diff --check main...HEAD
  ```

  Expected: every command exits zero, coverage remains at least 80%, and output contains no
  warnings, ignored failures, uncaptured expected errors, or diagnostics other than the one exact
  queue false positive filtered within the example validator.

- [ ] **Step 4: Verify every branch commit signature**

  List `main..HEAD`, inspect each with `--show-signature`, and stop if any Codex-created commit lacks
  a good signature. Do not amend or rewrite with an unsigned fallback.

- [ ] **Step 5: Obtain authority for disposable GitHub acceptance**

  If Dan has not already authorised creation and deletion of the disposable repository and its App
  installation/environment configuration, stop and request that authority. This gate must not be
  replaced by mocks or claims based only on local tests.

- [ ] **Step 6: Execute and retain the real-service transcript**

  Follow `examples/github-actions/README.md` exactly. Verify every case enumerated in Task 8,
  including three queued dispatches, App-mode read-only `GITHUB_TOKEN`, hostile-provider isolation,
  built-in/App check triggering, dry-run no-mutation, leases, and optional signing. Save the
  transcript outside the repository unless Dan explicitly requests a committed evidence file.

- [ ] **Step 7: Run final repository-state checks**

  Run:

  ```text
  /Users/dan/.codex/bin/codex-git status --short --branch
  /Users/dan/.codex/bin/codex-git log --oneline --show-signature main..HEAD
  ```

  Expected: clean feature branch, no generated binaries/coverage artefacts staged, and every branch
  commit signed.

- [ ] **Step 8: Hand off with evidence**

  Report the implementation commits, local verification results, coverage, cleanup/review outcome,
  real-service acceptance repository/transcript location, and any operational setup Dan must retain.
  Do not push or open a PR unless Dan requests it.
