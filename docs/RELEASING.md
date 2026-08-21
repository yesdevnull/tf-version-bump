# Release process and verification

Git tags beginning with `v` trigger the release workflow. GitHub Actions uses GoReleaser to build
archives and Linux packages in a draft release, the SLSA generator attaches provenance, and a final
job publishes the complete release.

This page serves two audiences:

- Users verifying a downloaded release
- Maintainers creating or testing a release

## Published artefacts

For a tag such as `v1.0.0-rc.8`, the version portion in archive names is `1.0.0-rc.8`:

| Artefact pattern | Platform |
|------------------|----------|
| `tf-version-bump_<version>_linux_x86_64.tar.gz` | Linux amd64 |
| `tf-version-bump_<version>_linux_arm64.tar.gz` | Linux arm64 |
| `tf-version-bump_<version>_darwin_x86_64.tar.gz` | macOS amd64 |
| `tf-version-bump_<version>_darwin_arm64.tar.gz` | macOS arm64 |
| `tf-version-bump_<version>_windows_x86_64.zip` | Windows amd64 |
| `tf-version-bump_<version>_windows_arm64.zip` | Windows arm64 |
| `tf-version-bump_<version>_linux_<arch>.deb` | Debian/Ubuntu package |
| `tf-version-bump_<version>_linux_<arch>.rpm` | RPM package |
| `tf-version-bump-v<version>.checksums.txt` | SHA-256 checksums |
| `tf-version-bump-v<version>.intoto.jsonl` | SLSA provenance |

Release tags containing a semantic-version pre-release suffix, such as `-rc.8`, are published as
GitHub pre-releases automatically. Select a tag from the
[Releases page](https://github.com/yesdevnull/tf-version-bump/releases) rather than assuming a
stable release exists.

## Verify a checksum

This Linux example uses the existing `v1.0.0-rc.8` pre-release. Replace `VERSION` deliberately
when downloading another release:

```bash
VERSION="1.0.0-rc.8"

curl -LO "https://github.com/yesdevnull/tf-version-bump/releases/download/v${VERSION}/tf-version-bump_${VERSION}_linux_x86_64.tar.gz"
curl -LO "https://github.com/yesdevnull/tf-version-bump/releases/download/v${VERSION}/tf-version-bump-v${VERSION}.checksums.txt"

sha256sum -c "tf-version-bump-v${VERSION}.checksums.txt" --ignore-missing
```

`--ignore-missing` limits verification to downloaded files whose names appear in the checksum
manifest. Check that the command explicitly reports the selected archive as `OK`.

## Verify SLSA provenance

Install the official verifier:

```bash
go install github.com/slsa-framework/slsa-verifier/v2/cli/slsa-verifier@latest
```

Download and verify the provenance matching the same tag:

```bash
VERSION="1.0.0-rc.8"

curl -LO "https://github.com/yesdevnull/tf-version-bump/releases/download/v${VERSION}/tf-version-bump_${VERSION}_linux_x86_64.tar.gz"
curl -LO "https://github.com/yesdevnull/tf-version-bump/releases/download/v${VERSION}/tf-version-bump-v${VERSION}.intoto.jsonl"

slsa-verifier verify-artifact "tf-version-bump_${VERSION}_linux_x86_64.tar.gz" \
  --provenance-path "tf-version-bump-v${VERSION}.intoto.jsonl" \
  --source-uri github.com/yesdevnull/tf-version-bump \
  --source-tag "v${VERSION}"
```

The release workflow requests GitHub's OIDC token, supplies artefact digests to the reusable SLSA
generator, and uploads the resulting in-toto JSONL file to the release. Third-party actions are
pinned to commit SHAs, except for the reusable SLSA generator's upstream-required exact semantic
version tag. Jobs declare their required permissions explicitly.

GoReleaser runs the tagged module through `proxy.golang.org` and verifies dependencies through
`sum.golang.org`. This tagged proxy path records verifiable module information in the binaries; a
local snapshot does not exercise it.

## Create a release

Before tagging:

1. Ensure the intended commit is on `main` and the worktree is clean.
2. Run the full tests, `go mod tidy -diff`, and lint checks.
3. Choose a semantic version. Include a pre-release suffix when the release is not stable.
4. Review `.goreleaser.yaml` and `.github/workflows/release.yml` when changing artefact formats.

Create and push an annotated tag:

Replace `<version>` with the semantic version selected above:

```bash
git tag -a "v<version>" -m "Release v<version>"
git push origin "v<version>"
```

The tag push starts `.github/workflows/release.yml`, which:

1. Checks out full history.
2. Installs Go 1.26.6 and GoReleaser v2.17.1 exactly.
3. Runs GoReleaser with `release --clean`, creating a draft release.
4. Collects archive and package digests.
5. Generates and uploads SLSA provenance to the draft.
6. Publishes the draft only after the build and provenance jobs succeed.

If a complete workflow rerun is required after a failure, GoReleaser replaces the existing draft
for that tag and uploads a clean set of assets. If only the final publication job failed, rerunning
the failed job publishes the already-complete draft.

Before relying on release immutability, follow
[GitHub's repository instructions](https://docs.github.com/en/code-security/how-tos/secure-your-supply-chain/establish-provenance-and-integrity/prevent-release-changes#enforcing-immutable-releases-for-your-repository):
open the repository's **Settings**, scroll to **Releases**, and select **Enable release
immutability**. The setting applies only to releases published after it is enabled. Drafts remain
editable while the workflow is assembling them; after publication, GitHub locks the tag and assets.

After the workflow completes, verify that the release contains all six platform archives, four
Linux packages, the checksum manifest, and provenance file. Perform at least one checksum and
provenance verification using the published assets.

## Test a release locally

Install the release workflow's exact GoReleaser version and create a snapshot without publishing:

```bash
go install github.com/goreleaser/goreleaser/v2@v2.17.1
goreleaser --version
goreleaser release --snapshot --clean
```

GoReleaser writes snapshot output under `dist/`. The release configuration uses `-trimpath`, the
source commit date, and commit-based modification times to reduce environmental differences. It
does not run source-mutating pre-build hooks.

A local snapshot confirms GoReleaser packaging. It does not exercise tagged Go module proxy mode,
prove byte-identical reproducibility, reproduce GitHub OIDC, or run the reusable SLSA workflow.

## Troubleshooting

### GoReleaser reports a dirty worktree

Inspect `git status`. Commit intentional source or module-file changes before tagging. Do not tag
from an unreviewed dirty worktree.

### Checksum verification fails

Confirm the archive, checksum manifest, and tag all use the same version, including any
pre-release suffix. Re-download both files before investigating further.

### Provenance verification fails

Confirm the archive and `.intoto.jsonl` came from the same release and that `--source-tag` includes
the leading `v`.

### Expected artefacts are missing

Inspect both the GoReleaser and SLSA jobs in the tag-triggered workflow. A GoReleaser success does
not by itself prove that provenance generation and upload also completed.

If the release is still a draft, inspect the failed job before publishing it manually. Prefer a
workflow rerun so the draft is rebuilt or published through the same reviewed process.
