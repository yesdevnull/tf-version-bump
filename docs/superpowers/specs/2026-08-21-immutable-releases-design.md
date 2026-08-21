# Immutable Releases and Reproducibility-Hardened Builds Design

## Goal

Prepare the release pipeline for GitHub immutable releases while applying GoReleaser's recommended
reproducibility hardening and verifiable-build configuration.

## Release publication

GoReleaser will create a draft GitHub release and upload its archives, packages, and checksum
manifest. The SLSA generator will attach provenance to that same draft. A final workflow job will
publish the draft only after both jobs succeed, so GitHub never freezes a partially populated
release.

The final publisher will have job-scoped `contents: write` permission and will pass GitHub's token,
repository, and tag to `gh` through environment variables. GoReleaser will replace an existing
draft for the same tag when a complete workflow rerun is needed, rebuilding the pre-publication
state cleanly.

The repository's immutable-releases setting remains an explicit GitHub configuration step. The
workflow must be safe both before and after Dan enables that setting.

## Build reproducibility hardening

The Go build will use `-trimpath`, the source commit date in version metadata, and the commit
timestamp for output modification times. GoReleaser's Go module proxy mode will resolve the tagged
module through the public proxy and checksum database so the binary records verifiable module
information.

These controls do not by themselves prove that every archive and Linux package is byte-for-byte
reproducible. Local snapshots also ignore Go module proxy mode. The release checks therefore
describe the configuration as reproducibility-hardened and verifiable rather than claiming proven
reproducibility.

Release CI will pin Go 1.26.6 and GoReleaser v2.17.1. These are the latest stable upstream releases
available on 21 August 2026. Exact versions keep the build instructions stable; updating either pin
is a deliberate maintenance change.

## Release hooks

The release will not run `go mod tidy` or `go generate ./...` before building. `go mod tidy` can
mutate a tagged checkout, and this repository currently has no `go:generate` directives. CI will
run `go mod tidy -diff` so stale module metadata fails before a release tag is created.

## Testing and documentation

Go tests will parse the GoReleaser and GitHub Actions YAML and enforce the repository's release
policy: deterministic build inputs, draft replacement, draft asset upload,
provenance-before-publication ordering, publisher authorisation, module tidiness, and exact
toolchain pins. `goreleaser check`, a snapshot packaging smoke test, the Go test suite, linting, and
a build will validate the locally testable configuration. The release guide will explain the draft
workflow, clean-rerun policy, tagged proxy-build check, and separate GitHub immutable-releases
setting.
