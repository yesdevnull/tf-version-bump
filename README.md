# Terraform Version Bump

> [!WARNING]
> This repository is an experiment for generative AI coding tools. It may contain bugs,
> incomplete features, or other issues. Review every change before applying it to production.

`tf-version-bump` updates Terraform module versions, Terraform `required_version`
constraints, and provider versions across files selected by a glob pattern. It parses and
writes HCL with HashiCorp's `hclwrite` package instead of editing Terraform as plain text.

## What it can update

- Every module whose literal `source` matches a requested source
- `required_version` in existing `terraform` blocks
- Provider versions in `required_providers` blocks
- Any combination of those updates from one YAML config file

Changed files retain their comments and HCL structure, but are formatted by `hclwrite`; do
not expect byte-for-byte preservation of whitespace. Original file permissions are retained.

## Installation

### Go install

Go 1.26 or later is required:

```bash
go install github.com/yesdevnull/tf-version-bump@latest
```

Go installs the command into `GOBIN`, or into `GOPATH/bin` when `GOBIN` is unset. Ensure that
directory is on your `PATH`.

### Release artefacts

Pre-built archives and Linux packages are published on the
[GitHub Releases](https://github.com/yesdevnull/tf-version-bump/releases) page. Releases may
be marked as pre-releases, so choose the tag deliberately. Release assets include checksums
alongside builds for Linux, macOS, and Windows on amd64 and arm64. Releases that include SLSA
provenance publish a matching `.intoto.jsonl` asset.

See [Release process and verification](docs/RELEASING.md) for artefact names and verification
commands.

### Build from source

```bash
git clone https://github.com/yesdevnull/tf-version-bump.git
cd tf-version-bump
go build -o tf-version-bump .
./tf-version-bump -help
```

## Quick start

Quote glob patterns so your shell passes them to `tf-version-bump` unchanged.

### Update a module

```bash
tf-version-bump \
  -pattern "**/*.tf" \
  -module "terraform-aws-modules/vpc/aws" \
  -to "5.0.0"
```

Module sources are compared as exact strings. All eligible module blocks with a matching source
are updated, no matter what their block labels are. Eligibility depends on the current-version,
module-name, and missing-version controls described below.

### Update the required Terraform version

```bash
tf-version-bump -pattern "**/*.tf" -terraform-version ">= 1.9"
```

This sets `required_version` in every existing top-level `terraform` block in the matching
files. It does not create a missing `terraform` block.

### Update a provider

```bash
tf-version-bump -pattern "**/*.tf" -provider aws -to "~> 6.0"
```

Only the named provider is changed; other providers and `required_version` are left alone.

### Apply several updates from YAML

Create `versions.yml`:

```yaml
terraform_version: ">= 1.9"

providers:
  - name: "aws"
    version: "~> 6.0"

modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
  - source: "terraform-aws-modules/s3-bucket/aws"
    version: "4.0.0"
    from:
      - "3.0.0"
      - "~> 3.0"
```

Then apply it:

```bash
tf-version-bump -pattern "**/*.tf" -config versions.yml
```

Config mode is exclusive with `-module`, `-provider`, `-terraform-version`, `-to`, and the
module-filter flags. It can still be combined with global behaviour flags such as `-dry-run`,
`-force-add`, `-verbose`, `-output`, and `-report-file`.

## Preview and review

The command writes files in place. Start with a clean version-control worktree, preview the
operation, then inspect the real diff:

```bash
tf-version-bump \
  -pattern "**/*.tf" \
  -module "terraform-aws-modules/vpc/aws" \
  -to "5.0.0" \
  -dry-run
```

Remove `-dry-run` to write the files, then review them:

```bash
git diff -- '*.tf'
terraform fmt -check -recursive
terraform validate
```

`tf-version-bump` checks HCL syntax but cannot determine whether a new version is compatible
with your Terraform configuration. Use your normal validation and planning workflow before
deployment.

Automation can pass `-report-file update-report.json` to receive exact updated module and
provider block counts as JSON. See the [usage reference](docs/USAGE.md#machine-readable-update-report)
for the report contract.

## Common controls

### Select current versions

Repeat `-from` to update only modules whose current version string equals one of the supplied
values:

```bash
tf-version-bump \
  -pattern "**/*.tf" \
  -module "terraform-aws-modules/vpc/aws" \
  -to "5.0.0" \
  -from "4.0.0" \
  -from "~> 4.0"
```

These are exact string comparisons, not semantic-version or Terraform-constraint evaluation.
For example, `-from "~> 4.0"` matches the literal constraint `~> 4.0`; it does not match
`4.3.0`.

Use repeatable `-ignore-version` flags to exclude exact current-version strings. Exclusions
take precedence over `-from`.

### Ignore module block names

`-ignore-modules` accepts comma-separated block labels and `*` wildcards:

```bash
tf-version-bump \
  -pattern "**/*.tf" \
  -module "terraform-aws-modules/vpc/aws" \
  -to "5.0.0" \
  -ignore-modules "legacy-vpc,test-*,*-deprecated"
```

### Add a missing module version

Matching registry modules without a `version` attribute are skipped with a warning by default.
Use `-force-add` to add the attribute:

```bash
tf-version-bump \
  -pattern "**/*.tf" \
  -module "terraform-aws-modules/vpc/aws" \
  -to "5.0.0" \
  -force-add
```

Local and non-registry remote module sources are always skipped when their `version` attribute is
missing, including with `-force-add`. Terraform supports `version` only for registry modules; Git
and other remote sources select revisions through their source address.

## Glob patterns

- `*` matches within one path segment.
- `**` spans zero or more directories, so `**/*.tf` also matches a root-level `main.tf`.
- Wildcard traversal skips dot-directories such as `.terraform` and `.git`.
- A dot-directory named explicitly, such as `.terraform/**/*.tf`, can still match.
- Directory symlinks are not followed; results are processed in lexicographical order.
- Brace alternation such as `{dev,prod}` and character classes such as `[0-9]` are supported.

See [Usage reference](docs/USAGE.md#file-selection) for the complete matching behaviour.

## Documentation

| Guide | Contents |
|------|----------|
| [Usage reference](docs/USAGE.md) | Every CLI flag, update semantics, glob behaviour, output, and limitations |
| [Configuration](docs/CONFIGURATION.md) | Complete YAML format, filters, precedence, and examples |
| [Advanced usage](docs/ADVANCED-USAGE.md) | Automating updates across Git branches |
| [Examples](examples/README.md) | YAML samples, Terraform fixtures, and the branch automation script |
| [Release process](docs/RELEASING.md) | Building, publishing, and verifying release artefacts |

Run `tf-version-bump -help` for the command's built-in flag reference and
`tf-version-bump -version` for build metadata.

## Development

```bash
go test -v -race -coverprofile=coverage.out -covermode=atomic ./...
golangci-lint run --timeout=5m
```

See [CLAUDE.md](CLAUDE.md) and [AGENTS.md](AGENTS.md) for repository architecture and
contributor guidance.

## Licence

This project is available under the [MIT Licence](LICENSE).
