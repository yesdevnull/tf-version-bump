# Usage reference

`tf-version-bump` selects Terraform files with a glob, parses them as HCL, applies one kind of update (or an aggregate YAML config), and writes each changed file in place.

For a short introduction, start with the [README](../README.md#quick-start).

## Command modes

The command supports five mutually exclusive entry points:

```text
tf-version-bump -pattern <glob> -module <source> -to <version>
tf-version-bump -pattern <glob> -terraform-version <constraint>
tf-version-bump -pattern <glob> -provider <name> -to <constraint>
tf-version-bump -pattern <glob> -config <file>
tf-version-bump -validate-config <file>
```

`-config` can combine module, Terraform, and provider updates internally. It cannot be combined with the three direct operation flags or their module filters. `-validate-config` checks only the YAML runtime contract and cannot be combined with update or report flags.

`-audit-file` is a config-mode option rather than another mode: it compares the selected files with the config and writes the result without changing any Terraform file.

An eligible module, provider, or Terraform version that already evaluates to its requested constant string is a no-op: the command does not rewrite the file or count it as an update.

## Flag reference

| Flag | Applies to | Description |
|------|------------|-------------|
| `-pattern <glob>` | All update modes | Files to process. Required. Quote it to prevent shell expansion. |
| `-module <source>` | Direct module mode | Literal module source to match. |
| `-to <version>` | Module and provider modes | Replacement version string or constraint. |
| `-from <version>` | Direct module mode | Update only this exact current-version string. Repeatable. |
| `-ignore-version <version>` | Direct module mode | Skip this exact current-version string. Repeatable. |
| `-ignore-modules <patterns>` | Direct module mode | Comma-separated module block labels; `*` is a wildcard. |
| `-branch <name>` | Module updates and audit | Short branch name used to resolve branch-scoped ignore patterns. |
| `-config <file>` | Config mode | YAML file containing one or more update groups. |
| `-validate-config <file>` | Standalone | Validate a non-empty YAML update config without selecting Terraform files. |
| `-terraform-version <constraint>` | Direct Terraform mode | Value to set as `required_version`. |
| `-provider <name>` | Direct provider mode | Local provider name within `required_providers`. |
| `-force-add` | Module updates | Add a missing module `version` attribute to registry modules. |
| `-dry-run` | All update modes | Report changes without writing files. |
| `-check` | All update modes | Report changes without writing files; exit 2 when updates are required. |
| `-verbose` | Module updates | Report modules skipped by name or version filters. |
| `-output <format>` | All update modes | `text` (default) uses single quotes; `md` uses backticks in messages. |
| `-report-file <path>` | All update modes | Write exact updated Terraform, module, and provider block counts as JSON. |
| `-audit-file <path>` | Config mode | Write every configured version value's current and expected version as JSON, without changing files. |
| `-version` | Standalone | Print version, commit, and build date metadata, then exit. |

`-output md` changes quoting in human-readable update messages; it does not emit a structured Markdown document or machine-readable result. Standalone validation keeps its fixed `Config '<path>' is valid` success message in every output format.

### Machine-readable update report

Use `-report-file` when automation needs exact block counts independently of the human-readable summary:

```bash
tf-version-bump \
  -pattern "**/*.tf" \
  -config versions.yml \
  -report-file update-report.json
```

The report has this stable shape:

```json
{
  "schema_version": 2,
  "terraform_blocks_updated": 1,
  "module_blocks_updated": 4,
  "provider_blocks_updated": 2
}
```

Counts represent unique individual Terraform, module, and provider blocks whose version value changed across the complete command. Repeated config entries that update the same block count it once. Blocks already at the requested version are excluded. Dry runs write zero counts because they do not change files. The report is written only after the update operation completes without errors. Its destination is validated before Terraform files are modified and cannot be one of the selected Terraform or YAML config inputs. Changed-file counts are outside this report; automation can derive them from its version-control diff.

### Machine-readable version audit

Use `-audit-file` with a config to record how far the selected files are from it, without changing them:

```bash
tf-version-bump \
  -pattern "**/*.tf" \
  -config versions.yml \
  -audit-file audit.json
```

The audit lists every value the config targets that the files declare:

```json
{
  "schema_version": 1,
  "terraform": [
    {"file": "versions.tf", "actual": ">= 1.10", "expected": ">= 1.10", "matches": true}
  ],
  "providers": [
    {"file": "versions.tf", "name": "aws", "actual": "~> 5.0", "expected": "~> 6.0", "matches": false}
  ],
  "modules": [
    {"file": "main.tf", "name": "vpc", "source": "terraform-aws-modules/vpc/aws",
     "actual": "4.2.0", "expected": "5.0.0", "matches": false, "skip": null},
    {"file": "main.tf", "name": "legacy_vpc", "source": "terraform-aws-modules/vpc/aws",
     "actual": "3.19.0", "expected": "5.0.0", "matches": false,
     "skip": {"filter": "ignore_modules", "values": ["legacy_*"]}}
  ]
}
```

- `terraform` has one entry per `terraform` block when the config sets `terraform_version`.
- `providers` has one entry per `required_providers` declaration, in either syntax, whose local name the config lists.
- `modules` has one entry per module block and config entry with an equal `source`, so a block that two entries target appears twice.
- `actual` is the value the entry would find, without its quotes, or `null` when the declaration has no version. That is the value as written, except that a run applies a source's module entries in order: once an earlier entry would rewrite a block, later entries for that block find the earlier entry's `version`, and `matches` and `skip` are judged against it. A non-literal expression appears as its source text.
- `matches` is true when the value already evaluates to the expected string, so an update would never change it. A value can also stay unchanged without matching: a skipped module, or an object-syntax provider without `version`, which updates do not add.
- `skip` names the first filter that would stop an update, in the order the updater applies them: `local_source`, `ignore_modules`, `ignore_versions`, then `from`. A module without a `version` is never skipped by a version filter.
- `skip.values` lists every configured value for the filter as written, so branch-scoped `ignore_modules` entries appear in full, including those scoped to other branches.

The audit applies `ignore_modules` exactly as the updater does, including branch scoping, so both agree on which modules are excluded. Pass `-branch` whenever the config has a branch-scoped entry; without it the audit fails with the same error as an update rather than silently reporting the module as out of date.

Config entries that no selected file uses produce no entries. The audit is written only after every selected file parses; otherwise the command exits 1 and leaves any existing audit untouched. A written audit exits 0 whatever it contains. The destination is validated like `-report-file`'s, and `-audit-file` cannot be combined with `-dry-run`, `-check`, `-report-file` or `-force-add`.

## Module updates

```bash
tf-version-bump \
  -pattern "modules/**/*.tf" \
  -module "terraform-aws-modules/vpc/aws" \
  -to "5.0.0"
```

The command examines top-level `module` blocks in every selected file. A block is eligible when its `source` value exactly equals the requested source. Matching is not based on the block label, so all of these blocks are updated together:

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

Source and version-filter matching use literal strings; the tool does not interpret semantic versions or version constraints. For idempotency only, a version expression that HCL can evaluate as a wholly known constant string is compared with the requested value before rewriting. Variables, function calls, and other context-dependent expressions are not evaluated and are replaced with the requested literal string when their block is otherwise eligible.

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

`ignore-version` takes precedence when a value appears in both sets. Constraint-looking strings are still compared literally: `~> 4.0` matches `~> 4.0`, not every release in the 4.x series.

### Module-name filters

`-ignore-modules` matches module block labels, not source addresses:

```bash
tf-version-bump \
  -pattern "**/*.tf" \
  -module "terraform-aws-modules/vpc/aws" \
  -to "5.0.0" \
  -ignore-modules "legacy-vpc,test-*,*-deprecated"
```

The only special character is `*`, which matches zero or more characters. Matching is case-sensitive. An exact name contains no wildcard.

### Branch-scoped module-name filters

An ignore pattern can be limited to particular branches by prefixing it with a branch pattern. Terraform module names cannot contain `/`, so the final `/` separates the two patterns: everything before it is the branch pattern and everything after it is the module pattern.

```bash
tf-version-bump \
  -pattern "**/*.tf" \
  -module "terraform-aws-modules/vpc/aws" \
  -to "5.0.0" \
  -ignore-modules "legacy-vpc,state/staging/example-thing/shared-vpc" \
  -branch "state/staging/example-thing"
```

| Pattern | Ignores |
|---------|---------|
| `shared-vpc` | That module on every branch |
| `state/staging/example-thing/shared-vpc` | That module only on `state/staging/example-thing` |
| `release/*/legacy-vpc` | `legacy-vpc` on any branch starting `release/` |
| `state/staging/*/*` | Every module on any `state/staging/…` branch |

The branch pattern uses the same wildcard rules as the module pattern, so `*` spans `/` rather than stopping at a path segment. `release/*` therefore matches `release/2026-09` and `release/2026-09/hotfix` alike. No `/`-separated part may be empty, so `state/staging/`, `/vpc`, and `state//vpc` are rejected.

`-ignore-modules` is split on commas before any `/` is considered, so it cannot express a branch pattern containing `,`. Use `ignore_modules` in a config file for such a branch, or put `*` in place of the comma, accepting that it also matches other characters.

A trailing `/*` is the module pattern, not a branch glob. `state/staging/*` means every module on a branch named exactly `state/staging`, which cannot exist alongside any `state/staging/<name>` branch, because Git stores refs as directories. Under a `state/<environment>/<name>` scheme, use `state/staging/*/*`.

The command does not inspect Git, so supply `-branch` yourself. It takes the short branch name, as `git branch --show-current` prints it, rather than a full ref such as `refs/heads/…`. A remote-tracking name such as `origin/main` is accepted, because a local branch may be named that way, but it is rarely the branch you mean:

```bash
tf-version-bump -pattern "**/*.tf" -config versions.yml -branch "$(git branch --show-current)"
```

Prefer that over `git rev-parse --abbrev-ref HEAD`, which prints `HEAD` on a detached checkout — the usual CI case. `git branch --show-current` prints nothing when detached, so a command with a branch-scoped pattern stops with this error rather than matching nothing and updating the module anyway:

```text
Error: ignore pattern 'state/staging/example-thing/shared-vpc' is scoped to a branch, but -branch is missing or empty; a detached checkout has no current branch
```

A literal `-branch HEAD`, and any value beginning `refs/`, are rejected for the same reason: both name no branch, so every scoped pattern would be dropped without warning. `refs/` covers `-branch "$GITHUB_REF"`, which holds `refs/heads/<name>` in GitHub Actions. A value starting `origin/` is not rejected, because a local branch may legitimately be named that way.

A branch-scoped pattern without `-branch` is an error rather than a silent no-op, because silently dropping the exclusion would update a module the configuration set out to protect. Unscoped patterns never require `-branch`.

### Missing versions and module sources

A matching registry module without `version` is skipped with a warning unless `-force-add` is supplied. When the attribute is added, it is written through `hclwrite` and may not appear at the same position you would have chosen manually.

Sources beginning with `./`, `../`, or `/` are treated as local modules and always skipped. Non-registry remote sources such as Git URLs are also skipped when `-force-add` would otherwise add a missing version. Terraform permits the `version` argument only for registry modules; Git sources select revisions with a `ref` query parameter in `source`.

Module processing follows this order:

1. Require an exact source match.
2. Skip local sources.
3. Apply module-name exclusions, after branch-scoped patterns are resolved against `-branch`.
4. Skip a missing version unless `-force-add` is enabled and the source is a registry module.
5. Apply `ignore-version` exclusions.
6. Apply the `from` allow-list.
7. Set the requested version.

## Terraform version updates

```bash
tf-version-bump -pattern "**/*.tf" -terraform-version ">= 1.9, < 2.0"
```

Every top-level `terraform` block in a selected file receives the requested `required_version`. A missing attribute is added, but a missing `terraform` block is not created. Provider constraints are not changed in this mode.

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

The provider name is the local key under `required_providers`, such as `aws`, rather than the full source address `hashicorp/aws`. Only that key is changed.

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

The updater changes an existing `version` entry and preserves the other object expressions. It does not add a missing version to attribute-style provider objects.

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

Block-style entries are supported by the tool for existing configurations, although the attribute-style object is Terraform's conventional `required_providers` form.

## Config mode

```bash
tf-version-bump -pattern "**/*.tf" -config versions.yml
```

Config mode applies updates in this order for each selected set of files:

1. Terraform `required_version`
2. Providers, in YAML order
3. Modules, in YAML order

Use `-force-add`, `-dry-run`, `-check`, `-verbose`, `-branch`, or `-output md` with config mode when required. Add `-audit-file` to compare the files with the config instead of updating them. See [Configuration](CONFIGURATION.md) for the complete YAML contract.

Config summaries count module entry/file applications as `update(s)`, not distinct files. A file matched by two module entries therefore contributes two module updates.

Module entries apply in YAML order, so several entries for one source can move the same block more than once in a run; dry runs, checks and audits follow the same order. See [Several entries for one source](CONFIGURATION.md#several-entries-for-one-source).

## File selection

Patterns use [`doublestar`](https://github.com/bmatcuk/doublestar) semantics and are evaluated relative to the current working directory unless an absolute or prefixed path is supplied.

| Pattern | Matches |
|---------|---------|
| `*.tf` | Terraform files in the current directory |
| `modules/*.tf` | Terraform files directly under `modules` |
| `modules/**/*.tf` | Terraform files under `modules` at any depth, including directly under it |
| `**/*.tf` | Terraform files at the current root and in any visible subdirectory |
| `{dev,prod}/**/*.tf` | Terraform files under `dev` or `prod` |
| `env-[0-9]/*.tf` | One numeric environment suffix |

Wildcard traversal does not enter directories whose names begin with `.`, so a broad pattern skips `.terraform`, `.git`, and other dot-directories. Naming the directory before the wildcard makes the intent explicit and permits it:

```bash
tf-version-bump -pattern ".terraform/**/*.tf" -module "example/module" -to "2.0.0"
```

Updating `.terraform` is normally a mistake because `terraform init` manages its contents.

Directory symlinks are not followed. Only files are returned, and matches are sorted lexicographically before processing. Literal braces must be escaped when brace expansion would otherwise interpret them.

An invalid pattern or a pattern with no matching files is a fatal command error.

## Output and error behaviour

- Per-file success messages and summaries go to standard output.
- Local modules and matching modules without versions produce warnings on standard error. When a file's backup cannot be removed after a successful write, a `Warning: could not remove backup <path>: <err>` line also appears on standard error.
- `-verbose` adds explanations for name and version filter skips.
- `-dry-run` parses every selected file and reports proposed updates without writing.
- `-check` performs the same no-write preview, exits 0 when no eligible version value would change, and exits 2 after a successful run that found updates. Errors exit 1 and take precedence over status 2.
- `-audit-file` writes its audit and exits 0 whatever the audit contains; a selected file that cannot be read or parsed exits 1 without writing it.
- Parse, stat, read, and write errors for an individual file are logged and processing continues with later files or updates.

File-level errors do not stop processing: the command writes every diagnostic and continues with later files or configured updates. After processing, and after any summary, it exits non-zero if any selected operation encountered a file-level error.

`-check` cannot be combined with `-dry-run` because their exit contracts differ. It also rejects `-report-file` so the check promise covers every output file, not only Terraform inputs. Status 0 does not prove that every requested source or block exists: filters, ignored modules, unmatched targets, and missing module versions skipped without `-force-add` can leave no eligible update.

## File-writing behaviour

Changed files are serialised through `hclwrite.Format`. Comments and the surrounding HCL structure are retained, but whitespace can be normalised across the changed file. Files are rewritten in place, so their permission bits, owner and hard links are untouched, and a symlinked file is updated through its link.

Before rewriting a file, the tool copies its original bytes to a file named `tf-version-bump-backup-<file>-<random>` in the system temporary directory (`TMPDIR`, or `TMP` or `TEMP` on Windows), which must be writable and have room for the copy. If the rewrite fails, the original bytes are written back, the backup is removed and the error ends with `original content restored`. If the original cannot be written back, or the file cannot be closed after its rewrite, the backup is kept and the error ends with `original content is in <backup>`. The backup holds the bytes from before the run, so compare it against the Terraform file rather than copying it back unseen: a failed write-back leaves a file that may mix old and new bytes, which the backup replaces safely, while a file whose close failed most likely holds the completed update, which the backup would undo. The Terraform file is then refused for the rest of the run, so every later operation on it reports an error. Any other write error — from opening, reading, backing up, or when the rewrite makes no changes — leaves the file exactly as it was with no backup remaining, and the run carries on using it. Pointing the temporary directory inside the Terraform tree lets a later pattern select a leftover backup as an input. A crash or power loss part way through a write can still leave a mixed file, and the backup may not survive a reboot.

There is no file locking. Do not run multiple instances against the same files. Keep the files under version control, use `-dry-run`, and review the resulting diff.

The parser reads each file into memory. This is reasonable for ordinary Terraform files but is not designed for exceptionally large generated configurations.

## Next steps

- [Configuration reference](CONFIGURATION.md)
- [Branch automation](ADVANCED-USAGE.md)
- [Examples](../examples/README.md)
