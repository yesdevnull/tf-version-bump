# Configuration

A YAML config lets one `tf-version-bump` run update Terraform requirements, providers, and modules across the same selected files.

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

At least one of `terraform_version`, `providers`, or `modules` must be present: `-validate-config` and an update run both reject a config that asks for nothing, so a file truncated by a bad merge fails rather than passing as a no-op. `-audit-file` is the exception and audits such a config, writing an audit with empty sections, because an audit of a config that asks for nothing is a true answer. Unknown fields are rejected by the runtime YAML decoder. Leading and trailing whitespace is removed from names, sources, and version strings; empty items in module filter lists are discarded.

The repository's [JSON Schema](../schema/config-schema.json) provides editor completion and validates Terraform-style version-constraint syntax. The CLI's YAML loader does not execute that JSON Schema, so use an editor or separate schema validator when you need schema enforcement. The maintained configurations under [`examples/`](../examples/README.md#yaml-configurations) include the schema declaration shown above and can be copied as editor-enabled starting points.

## Validate without updating

Validate the runtime YAML contract without selecting, parsing, or changing Terraform files:

```bash
tf-version-bump -validate-config versions.yml
```

The command rejects malformed YAML, multiple YAML documents, unknown fields, missing required entry fields, a provider `name` listed more than once, and configs without any Terraform, provider, or module updates. It trims and validates values in the same way as update mode. It does not execute the JSON Schema or validate Terraform version-constraint syntax. Validation is standalone and cannot be combined with update, check, or report flags.

## Top-level fields

| Field | Type | Purpose |
|-------|------|---------|
| `terraform_version` | string | Value assigned to `required_version` in existing `terraform` blocks |
| `providers` | list | Provider version updates |
| `modules` | list | Module version updates |

When more than one group is present, the command applies Terraform, provider, then module updates. Entries within a list retain YAML order.

## Terraform version

```yaml
terraform_version: ">= 1.9, < 2.0"
```

The value is set in every existing top-level `terraform` block in every selected file. A missing `required_version` attribute is added; a missing block is not.

## Providers

Each provider entry requires a local provider `name` and target `version`:

```yaml
providers:
  - name: "aws"
    version: "~> 6.0"
  - name: "google"
    version: ">= 6.0, < 7.0"
```

Each `name` may appear only once. A provider entry has no filters, so a second entry for the same name could only contradict the first; the loader rejects it, and `-validate-config` reports the repeated entry. The JSON Schema cannot express this rule, so only the runtime check enforces it.

`name` is the key under `required_providers`, not the provider source address. In this example, the first entry targets `aws`, not `hashicorp/aws`:

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

See [Provider version updates](USAGE.md#provider-version-updates) for syntax and insertion behaviour.

## Modules

Each module entry requires:

- `source`: the exact literal module source to match
- `version`: the replacement version string or constraint

It can also include:

- `from`: one exact current-version string or a list of them
- `ignore_versions`: one exact current-version string or a list of them
- `ignore_modules`: a list of module block labels or `*` patterns, each optionally scoped to a branch

### Basic update

```yaml
modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
```

Every non-local module with that exact source is updated when it already has a `version` attribute, whether its value is a literal or an expression such as `var.module_version`, which is replaced with the target string. See [Module updates](USAGE.md#module-updates). `-force-add` can add a missing attribute only when the source is a registry module.

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

The strings are not interpreted. The last item matches only a module whose version attribute is literally `~> 4.0`; it does not represent every 4.x release.

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

An ignored version is never updated by that entry. `ignore_versions` takes precedence over `from` when the same value appears in both.

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

Patterns are case-sensitive and apply to the label in `module "label"`. `*` matches zero or more characters. A value without `*` is an exact match.

### Branch-scoped module names

An entry can be limited to particular branches by prefixing it with a branch pattern. Terraform module names cannot contain `/`, so the final `/` separates the branch pattern from the module pattern:

```yaml
modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
    ignore_modules:
      - "legacy-vpc"                              # every branch
      - "state/staging/example-thing/shared-vpc"  # one branch
      - "state/staging/*/shared-*"                # branch and module wildcards
```

The branch pattern uses the same wildcard rules as the module pattern, so `*` spans `/` instead of stopping at a path segment. A trailing `/*` is therefore the module pattern rather than a branch glob: every module on any `state/staging/…` branch is `state/staging/*/*`. No `/`-separated part may be empty; `state/staging/`, `/vpc`, and `state//vpc` are rejected when the config is loaded or validated.

Supply the branch with the `-branch` flag, which the command never infers from Git. It takes the short branch name, rather than a full ref such as `refs/heads/…`. A remote-tracking name such as `origin/main` is accepted, because a local branch may be named that way, but it is rarely the branch you mean:

```bash
tf-version-bump -pattern "**/*.tf" -config versions.yml -branch "$(git branch --show-current)"
```

`git branch --show-current` is deliberate: it prints nothing on a detached checkout, so a config containing a branch-scoped entry stops with this error:

```text
Error: ignore pattern 'state/staging/example-thing/shared-vpc' is scoped to a branch, but -branch is missing or empty; a detached checkout has no current branch
```

`git rev-parse --abbrev-ref HEAD` prints `HEAD` there, which names no branch, so a literal `-branch HEAD` is rejected, as is any value beginning `refs/` such as `$GITHUB_REF`.

A config containing a branch-scoped entry fails when `-branch` is missing, rather than silently dropping the exclusion and updating a module the config set out to protect. Configs that use only unscoped entries do not need `-branch`.

`-audit-file` resolves these entries identically, so the audit and an update agree on which modules are excluded. See [the version audit](USAGE.md#machine-readable-version-audit).

### Filter precedence

For a module whose source matches the entry:

1. Local sources are skipped.
2. `ignore_modules` is applied, after branch-scoped entries are resolved against `-branch`.
3. A missing version is skipped unless the command uses `-force-add` and the source is a registry module.
4. `ignore_versions` is applied.
5. `from` is applied.
6. The target `version` is written.

When `-force-add` handles a missing version, there is no current value to compare with `from` or `ignore_versions`, so the target is added after the name and registry-source checks. Terraform does not support a `version` argument for Git or other non-registry module sources.

### Several entries for one source

The same `source` can appear in more than one entry, each with its own filters. That moves two version lines of a module separately, and can exclude a module name from one line only:

```yaml
modules:
  # Keep 5.x constraints and pins on the latest 5.x line.
  - source: "terraform-aws-modules/vpc/aws"
    version: "~> 5.21"
    from:
      - "~> 5.0"
      - "5.1.0"

  # Move 4.x constraints to the latest 4.x line, except the legacy VPC.
  - source: "terraform-aws-modules/vpc/aws"
    version: "~> 4.6"
    from: "~> 4.0"
    ignore_modules:
      - "legacy_vpc"
```

A block at `~> 5.0` or `5.1.0` moves to `~> 5.21`, and a block at `~> 4.0` moves to `~> 4.6` unless it is named `legacy_vpc`. A block at any other version, such as `3.19.0`, matches neither `from` list and is left alone. The [`same-source-ranges` scenario](../examples/README.md#runnable-scenarios) runs this config.

A run applies the entries in YAML order, and each entry finds the versions the entries before it set. An entry whose `from` lists an earlier entry's target therefore moves the same block again in the same run: with `3.0.0` → `4.0.0` followed by `4.0.0` → `5.0.0`, a block at `3.0.0` ends at `5.0.0`. Listed the other way round, the same two entries take one run per step. `-dry-run`, `-check` and `-audit-file` follow the same order as a real run. Keep each target out of the other entries' `from` lists unless you want that chain.

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
- `-branch` supplies the branch name that branch-scoped `ignore_modules` entries are matched against.
- `-check` previews like `-dry-run` but exits 2 when updates are required.
- `-report-file` writes exact changed-block counts as JSON after a successful update.
- `-audit-file` writes each configured value's current and expected version as JSON instead of updating; it cannot be combined with `-dry-run`, `-check`, `-report-file` or `-force-add`.

Direct operation flags and filters cannot accompany `-config`: `-module`, `-provider`, `-terraform-version`, `-to`, `-from`, `-ignore-version`, and `-ignore-modules` are rejected.

## Example files

The [`examples` directory](../examples/README.md) contains configs for:

- Basic module batches
- Multiple `from` values
- Module-name exclusions
- Combined Terraform, provider, and module updates
- A larger production-style module list

Use those values as syntax examples, not as recommendations for current module or provider versions. Choose versions appropriate to your own configuration.
