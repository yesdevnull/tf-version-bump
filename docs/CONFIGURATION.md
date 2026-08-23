# Configuration

A YAML config lets one `tf-version-bump` run update Terraform requirements, providers, and
modules across the same selected files.

```bash
tf-version-bump -pattern "**/*.tf" -config versions.yml
```

## Complete example

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/yesdevnull/tf-version-bump/main/schema/config-schema.json

terraform_version: ">= 1.9, < 2.0"

providers:
  - name: "aws"
    version: "~> 6.0"
  - name: "azurerm"
    version: "~> 4.0"

modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
    from:
      - "4.0.0"
      - "~> 4.0"
    ignore_versions:
      - "4.0.0-bespoke"
    ignore_modules:
      - "test-*"
      - "*-deprecated"

  - source: "terraform-aws-modules/s3-bucket/aws"
    version: "4.0.0"
```

At least one of `terraform_version`, `providers`, or `modules` should be present. Unknown fields
are rejected by the runtime YAML decoder. Leading and trailing whitespace is removed from names,
sources, and version strings; empty items in module filter lists are discarded.

The repository's [JSON Schema](../schema/config-schema.json) provides editor completion and
validates Terraform-style version-constraint syntax. The CLI's YAML loader does not execute that
JSON Schema, so use an editor or separate schema validator when you need schema enforcement. The
maintained configurations under [`examples/`](../examples/README.md#yaml-configurations) include
the schema declaration shown above and can be copied as editor-enabled starting points.

## Validate without updating

Validate the runtime YAML contract without selecting, parsing, or changing Terraform files:

```bash
tf-version-bump -validate-config versions.yml
```

The command rejects malformed YAML, multiple YAML documents, unknown fields, missing required
entry fields, and configs without any Terraform, provider, or module updates. It trims and
validates values in the same way as update mode. It does not execute the JSON Schema or validate
Terraform version-constraint syntax. Validation is standalone and cannot be combined with update,
check, or report flags.

## Top-level fields

| Field | Type | Purpose |
|-------|------|---------|
| `terraform_version` | string | Value assigned to `required_version` in existing `terraform` blocks |
| `providers` | list | Provider version updates |
| `modules` | list | Module version updates |

When more than one group is present, the command applies Terraform, provider, then module updates.
Entries within a list retain YAML order.

## Terraform version

```yaml
terraform_version: ">= 1.9, < 2.0"
```

The value is set in every existing top-level `terraform` block in every selected file. A missing
`required_version` attribute is added; a missing block is not.

## Providers

Each provider entry requires a local provider `name` and target `version`:

```yaml
providers:
  - name: "aws"
    version: "~> 6.0"
  - name: "google"
    version: ">= 6.0, < 7.0"
```

`name` is the key under `required_providers`, not the provider source address. In this example,
the first entry targets `aws`, not `hashicorp/aws`:

```hcl
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}
```

See [Provider version updates](USAGE.md#provider-version-updates) for syntax and insertion
behaviour.

## Modules

Each module entry requires:

- `source`: the exact literal module source to match
- `version`: the replacement version string or constraint

It can also include:

- `from`: one exact current-version string or a list of them
- `ignore_versions`: one exact current-version string or a list of them
- `ignore_modules`: a list of module block labels or `*` patterns

### Basic update

```yaml
modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
```

Every non-local module with that exact source is updated when it already has a literal `version`
attribute. `-force-add` can add a missing attribute only when the source is a registry module.

### One source version

The scalar form updates only a literal current value:

```yaml
modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
    from: "4.0.0"
```

### Several source versions

The list form is an OR condition:

```yaml
modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
    from:
      - "4.0.0"
      - "4.1.0"
      - "~> 4.0"
```

The strings are not interpreted. The last item matches only a module whose version attribute is
literally `~> 4.0`; it does not represent every 4.x release.

### Excluded versions

`ignore_versions` accepts the same scalar or list shapes:

```yaml
modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
    ignore_versions:
      - "4.0.0-bespoke"
      - "~> 3.0"
```

An ignored version is never updated by that entry. `ignore_versions` takes precedence over
`from` when the same value appears in both.

### Excluded module names

`ignore_modules` always uses a YAML list:

```yaml
modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
    ignore_modules:
      - "legacy-vpc"
      - "test-*"
      - "*-deprecated"
      - "*-temporary-*"
```

Patterns are case-sensitive and apply to the label in `module "label"`. `*` matches zero or more
characters. A value without `*` is an exact match.

### Filter precedence

For a module whose source matches the entry:

1. Local sources are skipped.
2. `ignore_modules` is applied.
3. A missing version is skipped unless the command uses `-force-add` and the source is a registry
   module.
4. `ignore_versions` is applied.
5. `from` is applied.
6. The target `version` is written.

When `-force-add` handles a missing version, there is no current value to compare with `from` or
`ignore_versions`, so the target is added after the name and registry-source checks. Terraform
does not support a `version` argument for Git or other non-registry module sources.

## Config-mode flags

These global flags can accompany `-config`:

```bash
tf-version-bump \
  -pattern "**/*.tf" \
  -config versions.yml \
  -dry-run \
  -verbose \
  -output md
```

- `-dry-run` prevents all file writes.
- `-verbose` explains module skips caused by module or version filters.
- `-output md` uses backticks instead of single quotes in messages.
- `-force-add` adds missing version attributes to matching registry modules.

Direct operation flags and filters cannot accompany `-config`: `-module`, `-provider`,
`-terraform-version`, `-to`, `-from`, `-ignore-version`, and `-ignore-modules` are rejected.

## Example files

The [`examples` directory](../examples/README.md) contains configs for:

- Basic module batches
- Multiple `from` values
- Module-name exclusions
- Combined Terraform, provider, and module updates
- A larger production-style module list

Use those values as syntax examples, not as recommendations for current module or provider
versions. Choose versions appropriate to your own configuration.
