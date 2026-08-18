# Usage reference

`tf-version-bump` selects Terraform files with a glob, parses them as HCL, applies one kind of
update (or an aggregate YAML config), and writes each changed file in place.

For a short introduction, start with the [README](../README.md#quick-start).

## Command modes

The command supports four mutually exclusive entry points:

```text
tf-version-bump -pattern <glob> -module <source> -to <version>
tf-version-bump -pattern <glob> -terraform-version <constraint>
tf-version-bump -pattern <glob> -provider <name> -to <constraint>
tf-version-bump -pattern <glob> -config <file>
```

`-config` can combine module, Terraform, and provider updates internally. It cannot be combined
with the three direct operation flags or their module filters.

## Flag reference

| Flag | Applies to | Description |
|------|------------|-------------|
| `-pattern <glob>` | All update modes | Files to process. Required. Quote it to prevent shell expansion. |
| `-module <source>` | Direct module mode | Literal module source to match. |
| `-to <version>` | Module and provider modes | Replacement version string or constraint. |
| `-from <version>` | Direct module mode | Update only this exact current-version string. Repeatable. |
| `-ignore-version <version>` | Direct module mode | Skip this exact current-version string. Repeatable. |
| `-ignore-modules <patterns>` | Direct module mode | Comma-separated module block labels; `*` is a wildcard. |
| `-config <file>` | Config mode | YAML file containing one or more update groups. |
| `-terraform-version <constraint>` | Direct Terraform mode | Value to set as `required_version`. |
| `-provider <name>` | Direct provider mode | Local provider name within `required_providers`. |
| `-force-add` | Module updates | Add a missing module `version` attribute. Local modules remain excluded. |
| `-dry-run` | All update modes | Report changes without writing files. |
| `-verbose` | Module updates | Report modules skipped by name or version filters. |
| `-output <format>` | All update modes | `text` (default) uses single quotes; `md` uses backticks in messages. |
| `-version` | Standalone | Print version, commit, and build date metadata, then exit. |

`-output md` changes quoting in human-readable messages; it does not emit a structured Markdown
document or machine-readable result.

## Module updates

```bash
tf-version-bump \
  -pattern "modules/**/*.tf" \
  -module "terraform-aws-modules/vpc/aws" \
  -to "5.0.0"
```

The command examines top-level `module` blocks in every selected file. A block is eligible when
its `source` value exactly equals the requested source. Matching is not based on the block label,
so all of these blocks are updated together:

```hcl
module "production_vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "4.0.0"
}

module "test_vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "4.1.0"
}
```

The tool is designed for literal source and version strings. It does not evaluate HCL
expressions, Terraform variables, semantic versions, or version constraints.

### Version filters

Repeat `-from` to form an allow-list of exact current values:

```bash
tf-version-bump \
  -pattern "**/*.tf" \
  -module "terraform-aws-modules/vpc/aws" \
  -to "5.0.0" \
  -from "4.0.0" \
  -from "~> 4.0"
```

Repeat `-ignore-version` to exclude exact current values:

```bash
tf-version-bump \
  -pattern "**/*.tf" \
  -module "terraform-aws-modules/vpc/aws" \
  -to "5.0.0" \
  -ignore-version "4.0.0" \
  -ignore-version "~> 4.0"
```

`ignore-version` takes precedence when a value appears in both sets. Constraint-looking strings
are still compared literally: `~> 4.0` matches `~> 4.0`, not every release in the 4.x series.

### Module-name filters

`-ignore-modules` matches module block labels, not source addresses:

```bash
tf-version-bump \
  -pattern "**/*.tf" \
  -module "terraform-aws-modules/vpc/aws" \
  -to "5.0.0" \
  -ignore-modules "legacy-vpc,test-*,*-deprecated"
```

The only special character is `*`, which matches zero or more characters. Matching is
case-sensitive. An exact name contains no wildcard.

### Missing versions and local modules

A matching remote or registry module without `version` is skipped with a warning unless
`-force-add` is supplied. When the attribute is added, it is written through `hclwrite` and may
not appear at the same position you would have chosen manually.

Sources beginning with `./`, `../`, or `/` are treated as local modules and always skipped. They
remain skipped with `-force-add` because Terraform does not support a registry-style version
constraint for a local source.

Module processing follows this order:

1. Require an exact source match.
2. Skip local sources.
3. Apply module-name exclusions.
4. Skip a missing version unless `-force-add` is enabled.
5. Apply `ignore-version` exclusions.
6. Apply the `from` allow-list.
7. Set the requested version.

## Terraform version updates

```bash
tf-version-bump -pattern "**/*.tf" -terraform-version ">= 1.9, < 2.0"
```

Every top-level `terraform` block in a selected file receives the requested
`required_version`. A missing attribute is added, but a missing `terraform` block is not created.
Provider constraints are not changed in this mode.

Before:

```hcl
terraform {
  required_version = ">= 1.5"
}
```

After:

```hcl
terraform {
  required_version = ">= 1.9, < 2.0"
}
```

## Provider version updates

```bash
tf-version-bump -pattern "**/*.tf" -provider aws -to "~> 6.0"
```

The provider name is the local key under `required_providers`, such as `aws`, rather than the
full source address `hashicorp/aws`. Only that key is changed.

The normal Terraform attribute syntax is supported:

```hcl
terraform {
  required_providers {
    aws = {
      source                = "hashicorp/aws"
      version               = "~> 6.0"
      configuration_aliases = [aws.alternate]
    }
  }
}
```

The updater changes an existing `version` entry and preserves the other object expressions.
It does not add a missing version to attribute-style provider objects.

The tool also recognises block-style provider entries and adds or replaces their version:

```hcl
terraform {
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}
```

Block-style entries are supported by the tool for existing configurations, although the
attribute-style object is Terraform's conventional `required_providers` form.

## Config mode

```bash
tf-version-bump -pattern "**/*.tf" -config versions.yml
```

Config mode applies updates in this order for each selected set of files:

1. Terraform `required_version`
2. Providers, in YAML order
3. Modules, in YAML order

Use `-force-add`, `-dry-run`, `-verbose`, or `-output md` with config mode when required. See
[Configuration](CONFIGURATION.md) for the complete YAML contract.

## File selection

Patterns use [`doublestar`](https://github.com/bmatcuk/doublestar) semantics and are evaluated
relative to the current working directory unless an absolute or prefixed path is supplied.

| Pattern | Matches |
|---------|---------|
| `*.tf` | Terraform files in the current directory |
| `modules/*.tf` | Terraform files directly under `modules` |
| `modules/**/*.tf` | Terraform files under `modules` at any depth, including directly under it |
| `**/*.tf` | Terraform files at the current root and in any visible subdirectory |
| `{dev,prod}/**/*.tf` | Terraform files under `dev` or `prod` |
| `env-[0-9]/*.tf` | One numeric environment suffix |

Wildcard traversal does not enter directories whose names begin with `.`, so a broad pattern
skips `.terraform`, `.git`, and other dot-directories. Naming the directory before the wildcard
makes the intent explicit and permits it:

```bash
tf-version-bump -pattern ".terraform/**/*.tf" -module "example/module" -to "2.0.0"
```

Updating `.terraform` is normally a mistake because `terraform init` manages its contents.

Directory symlinks are not followed. Only files are returned, and matches are sorted
lexicographically before processing. Literal braces must be escaped when brace expansion would
otherwise interpret them.

An invalid pattern or a pattern with no matching files is a fatal command error.

## Output and error behaviour

- Per-file success messages and summaries go to standard output.
- Local modules and matching modules without versions produce warnings on standard error.
- `-verbose` adds explanations for name and version filter skips.
- `-dry-run` parses every selected file and reports proposed updates without writing.
- Parse, stat, read, and write errors for an individual file are logged and processing continues
  with later files or updates.

Because individual processing errors are logged rather than returned as a final failure, read
the complete output in automation; a zero exit status does not by itself prove that every file
was updated successfully.

## File-writing behaviour

Changed files are serialised through `hclwrite.Format`. Comments and the surrounding HCL
structure are retained, but whitespace can be normalised across the changed file. The original
permission bits are reused when the file is written.

Writes are not transactional and there is no file locking. Do not run multiple instances against
the same files. Keep the files under version control, use `-dry-run`, and review the resulting
diff.

The parser reads each file into memory. This is reasonable for ordinary Terraform files but is
not designed for exceptionally large generated configurations.

## Next steps

- [Configuration reference](CONFIGURATION.md)
- [Branch automation](ADVANCED-USAGE.md)
- [Examples](../examples/README.md)
