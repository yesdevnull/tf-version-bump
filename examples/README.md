# Examples

This directory contains sample YAML configurations, Terraform fixtures, and a maintained script
for automating updates across Git branches.

Version numbers in these files demonstrate syntax only. They are not recommendations and may not
be current releases of the referenced modules or providers.

## YAML configurations

| File | Demonstrates |
|------|--------------|
| [`config-basic.yml`](config-basic.yml) | A small batch of module updates |
| [`config-advanced.yml`](config-advanced.yml) | Registry modules, submodules, and a Git source |
| [`config-multiple-from.yml`](config-multiple-from.yml) | Exact current-version allow-lists |
| [`config-with-ignore.yml`](config-with-ignore.yml) | Module block-name exclusions |
| [`config-terraform-providers.yml`](config-terraform-providers.yml) | Terraform, provider, and module updates together |
| [`config-production.yml`](config-production.yml) | A larger illustrative module batch |

Preview any config from the repository root:

```bash
go run . -pattern "examples/*.tf" -config examples/config-basic.yml -dry-run
```

The sample Terraform files do not contain every source listed by every config, so a preview can
legitimately report fewer updates than the config contains.

For the YAML contract and filter precedence, see the
[configuration reference](../docs/CONFIGURATION.md).

## Terraform files

| File | Purpose |
|------|---------|
| [`main.tf`](main.tf) | Basic modules and resources |
| [`modules.tf`](modules.tf) | Additional module examples |
| [`complex.tf`](complex.tf) | More involved HCL structures |
| [`heavily_commented.tf`](heavily_commented.tf) | Comment retention |
| [`unusual_formatting.tf`](unusual_formatting.tf) | Formatting behaviour when a file is rewritten |

Copy fixtures to a temporary directory before running without `-dry-run` if you want to preserve
the checked-in examples.

## Branch automation

[`update-branches.sh`](update-branches.sh) applies one module update or YAML config across Git
branches, creating a commit on each branch without pushing it. Pass `--sign-commits` to ask Git to
sign each update commit with its configured signing key. Without the flag, the script does not
request signing, which allows it to run in CI without access to a signing key.

Start with its help and a dry run:

```bash
examples/update-branches.sh --help

examples/update-branches.sh \
  --repository /path/to/terraform-repository \
  --branch-pattern 'release/*' \
  --module 'terraform-aws-modules/vpc/aws' \
  --to '5.0.0' \
  --dry-run
```

The script refuses dirty worktrees, restores the starting branch after successful runs, can fetch
remote-only branches, and never pushes. See [Advanced usage](../docs/ADVANCED-USAGE.md) before
using write mode.

Contributors can run its end-to-end checks with:

```bash
examples/update-branches_test.sh
```

The checks require `ssh-keygen`, build and exercise the real `tf-version-bump` binary in temporary
Git repositories, and create an ephemeral signing key. They do not contact a remote service.
