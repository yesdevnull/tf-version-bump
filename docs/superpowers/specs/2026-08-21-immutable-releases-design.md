# Immutable Releases and Reproducible Builds Design

## Goal

Prepare the release pipeline for GitHub immutable releases while making GoReleaser outputs as
reproducible and verifiable as the current Go toolchain permits.

## Release publication

GoReleaser will create a draft GitHub release and upload its archives, packages, and checksum
manifest. The SLSA generator will attach provenance to that same draft. A final workflow job will
publish the draft only after both jobs succeed, so GitHub never freezes a partially populated
release.

The repository's immutable-releases setting remains an explicit GitHub configuration step. The
workflow must be safe both before and after Dan enables that setting.

## Build reproducibility

The Go build will use `-trimpath`, the source commit date in version metadata, and the commit
timestamp for output modification times. GoReleaser's Go module proxy mode will resolve the tagged
module through the public proxy and checksum database so the binary records verifiable module
information.

Release CI will pin Go 1.26.6 and GoReleaser v2.17.1. These are the latest stable upstream releases
available on 21 August 2026. Exact versions keep the build instructions stable; updating either pin
is a deliberate maintenance change.

## Release hooks

The release will not run `go mod tidy` or `go generate ./...` before building. `go mod tidy` can
mutate a tagged checkout, and this repository currently has no `go:generate` directives. Dependency
and source validation belongs in CI before a release tag is created.

## Testing and documentation

Go tests will parse the GoReleaser and GitHub Actions YAML and enforce the repository's release
policy: reproducible build inputs, draft asset upload, provenance-before-publication ordering, and
exact toolchain pins. `goreleaser check`, a snapshot release, the Go test suite, linting, and a build
will verify the complete change. The release guide will explain the draft workflow and the separate
GitHub immutable-releases setting.

