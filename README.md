# Terraform Version Bump

> **Note:** This repository is an experiment for generative AI coding tools. It may contain bugs, incomplete features, or other issues. Use at your own discretion.

A CLI tool written in Go that updates Terraform module versions across multiple files using glob patterns. The tool matches modules by their source attribute, making it easy to update all instances of a particular module to a new version.

## Features

- Parse Terraform files using the official HashiCorp HCL library
- Update module versions by matching on module source
- Process multiple files using glob patterns
- **Batch updates** via YAML configuration files
- **Selective updates** with ignore patterns (wildcard support)
- **Version filtering** to skip specific versions or update only from specific versions
- Preserves formatting and comments in Terraform files
- Safe and reliable HCL parsing and writing
- Comprehensive test suite

## Installation

### Install with go install (recommended)

If you have Go installed (version 1.24 or later), this is the easiest and recommended method:

```bash
go install github.com/yesdevnull/tf-version-bump@latest
```

This installs the binary to your `$GOPATH/bin` directory (usually `~/go/bin`). Ensure this directory is in your `PATH`.

### Download pre-built binary with verification

For environments without Go, or when you need supply chain verification (particularly useful for CI/production), download a pre-built binary from the [GitHub Releases](https://github.com/yesdevnull/tf-version-bump/releases) page:

```bash
# Set the version you want to install (replace with desired version)
VERSION="1.0.0"

# Download the binary and verification files
curl -LO "https://github.com/yesdevnull/tf-version-bump/releases/download/v${VERSION}/tf-version-bump_${VERSION}_linux_x86_64.tar.gz"
curl -LO "https://github.com/yesdevnull/tf-version-bump/releases/download/v${VERSION}/tf-version-bump-v${VERSION}.checksums.txt"

# Verify the checksum
sha256sum -c "tf-version-bump-v${VERSION}.checksums.txt" --ignore-missing

# Extract and install
tar -xzf "tf-version-bump_${VERSION}_linux_x86_64.tar.gz"
sudo mv tf-version-bump /usr/local/bin/
```

#### Verify SLSA provenance (optional but recommended)

For enhanced supply chain security, verify the SLSA Level 3 provenance:

```bash
# Install slsa-verifier
go install github.com/slsa-framework/slsa-verifier/v2/cli/slsa-verifier@latest

# Download provenance
curl -LO "https://github.com/yesdevnull/tf-version-bump/releases/download/v${VERSION}/tf-version-bump-v${VERSION}.intoto.jsonl"

# Verify
slsa-verifier verify-artifact "tf-version-bump_${VERSION}_linux_x86_64.tar.gz" \
  --provenance-path "tf-version-bump-v${VERSION}.intoto.jsonl" \
  --source-uri github.com/yesdevnull/tf-version-bump \
  --source-tag "v${VERSION}"
```

#### Platform-specific downloads

| Platform | Architecture | Filename |
|----------|-------------|----------|
| Linux | x86_64 | `tf-version-bump_<version>_linux_x86_64.tar.gz` |
| Linux | arm64 | `tf-version-bump_<version>_linux_arm64.tar.gz` |
| macOS | x86_64 | `tf-version-bump_<version>_darwin_x86_64.tar.gz` |
| macOS | arm64 (Apple Silicon) | `tf-version-bump_<version>_darwin_arm64.tar.gz` |
| Windows | x86_64 | `tf-version-bump_<version>_windows_x86_64.zip` |
| Windows | arm64 | `tf-version-bump_<version>_windows_arm64.zip` |

### Build from source

```bash
git clone https://github.com/yesdevnull/tf-version-bump.git
cd tf-version-bump
go build -o tf-version-bump

# Run the locally built binary
./tf-version-bump --help
```

## Usage

The tool supports four modes of operation:

1. **Single Module Mode**: Update one module at a time via command-line flags
2. **Config File Mode**: Update multiple modules in one operation using a YAML configuration file
3. **Terraform Version Mode**: Update Terraform `required_version` in terraform blocks
4. **Provider Version Mode**: Update provider versions in terraform `required_providers` blocks

### Single Module Mode

Basic syntax:

```bash
tf-version-bump -pattern <glob-pattern> -module <module-source> -to <version>
```

**Note:** If you built from source, use `./tf-version-bump` instead of `tf-version-bump`.

#### Arguments

- `-pattern`: Glob pattern for Terraform files (e.g., `*.tf`, `modules/**/*.tf`)
- `-module`: Source of the module to update (e.g., `terraform-aws-modules/vpc/aws`)
- `-to`: Desired version number
- `-from`: (Optional) Version to update from (can be specified multiple times, e.g., `-from 3.0.0 -from '~> 3.0'`)
- `-ignore-version`: (Optional) Version(s) to skip (can be specified multiple times, e.g., `-ignore-version 3.0.0 -ignore-version '~> 3.0'`)
- `-ignore-modules`: (Optional) Comma-separated list of module names or patterns to ignore (e.g., `vpc,legacy-*,*-test`)
- `-force-add`: (Optional) Add version attribute to modules that don't have one (default: false, skip with warning)
- `-dry-run`: (Optional) Show what changes would be made without actually modifying files
- `-verbose`: (Optional) Show verbose output including skipped modules
- `-output`: (Optional) Output format: `text` (default) or `md` (Markdown). Controls whether strings are quoted with single quotes or backticks

#### Examples

Update all VPC modules from the Terraform AWS modules registry to version `5.0.0`:

```bash
tf-version-bump -pattern "*.tf" -module "terraform-aws-modules/vpc/aws" -to "5.0.0"
```

Update S3 bucket modules in a specific directory:

```bash
tf-version-bump -pattern "environments/prod/*.tf" -module "terraform-aws-modules/s3-bucket/aws" -to "4.1.2"
```

Update modules across subdirectories (recursive):

```bash
tf-version-bump -pattern "modules/**/*.tf" -module "terraform-aws-modules/security-group/aws" -to "4.9.0"
```

Update modules with subpaths in their source:

```bash
tf-version-bump -pattern "*.tf" -module "terraform-aws-modules/iam/aws//modules/iam-user" -to "5.2.0"
```

Update only modules currently at version `3.14.0` to version `5.0.0`:

```bash
tf-version-bump -pattern "*.tf" -module "terraform-aws-modules/vpc/aws" -to "5.0.0" -from "3.14.0"
```

Update modules from multiple specific versions (CLI supports multiple -from flags):

```bash
tf-version-bump -pattern "*.tf" -module "terraform-aws-modules/s3-bucket/aws" -to "4.0.0" -from "3.0.0" -from "~> 3.0"
```

This will update S3 bucket modules that are currently at version `3.0.0` OR `~> 3.0` to version `4.0.0`, while leaving modules at other versions (like `3.1.0`) unchanged.

Skip updating specific versions using ignore-version flag:

```bash
tf-version-bump -pattern "*.tf" -module "terraform-aws-modules/vpc/aws" -to "5.0.0" -ignore-version "3.14.0"
```

This will update all VPC modules to version `5.0.0` EXCEPT those currently at version `3.14.0`.

Skip multiple versions (can specify flag multiple times):

```bash
tf-version-bump -pattern "*.tf" -module "terraform-aws-modules/s3-bucket/aws" -to "4.0.0" -ignore-version "3.0.0" -ignore-version "~> 3.0"
```

This will update all S3 bucket modules to version `4.0.0` EXCEPT those currently at version `3.0.0` or `~> 3.0`.

Update all VPC modules except specific ones using ignore patterns:

```bash
tf-version-bump -pattern "*.tf" -module "terraform-aws-modules/vpc/aws" -to "5.0.0" -ignore-modules "legacy-vpc,test-*"
```

This will update all VPC modules to version 5.0.0 except:
- The module named exactly `legacy-vpc`
- Any modules starting with `test-` (like `test-vpc`, `test-network`, etc.)

Update Git-based modules:

```bash
tf-version-bump -pattern "*.tf" -module "git::https://github.com/example/terraform-module.git" -to "v1.2.3"
```

Preview changes without modifying files (dry-run):

```bash
tf-version-bump -pattern "**/*.tf" -module "terraform-aws-modules/vpc/aws" -to "5.0.0" -dry-run
```

Use Markdown output format (backticks instead of single quotes):

```bash
tf-version-bump -pattern "*.tf" -module "terraform-aws-modules/vpc/aws" -to "5.0.0" -output md
```

This will output messages like:
```
Found 3 file(s) matching pattern `*.tf`
✓ Updated module source `terraform-aws-modules/vpc/aws` to version `5.0.0` in main.tf
```

Instead of:
```
Found 3 file(s) matching pattern '*.tf'
✓ Updated module source 'terraform-aws-modules/vpc/aws' to version '5.0.0' in main.tf
```

**Note:** Local modules (sources starting with `./`, `../`, or `/`) are not supported and will be skipped with a warning. Version bumping is only supported for registry modules and remote sources (Git, HTTP, etc.).

### Config File Mode

For updating multiple modules at once, use a YAML configuration file:

```bash
tf-version-bump -pattern <glob-pattern> -config <config-file>
```

**Note:** If you built from source, use `./tf-version-bump` instead of `tf-version-bump`.

#### Arguments

- `-pattern`: Glob pattern for Terraform files (required)
- `-config`: Path to YAML configuration file (required)
- `-force-add`: (Optional) Add version attribute to modules that don't have one (default: false, skip with warning)
- `-dry-run`: (Optional) Show what changes would be made without actually modifying files
- `-output`: (Optional) Output format: `text` (default) or `md` (Markdown). Controls whether strings are quoted with single quotes or backticks

#### Config File Format

Create a YAML file with the following structure:

```yaml
modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
    from: "3.14.0"       # Optional: only update if current version is 3.14.0
    ignore_versions:     # Optional: versions to skip
      - "3.0.0"
      - "~> 3.0"
    ignore_modules:      # Optional: module names or patterns to ignore
      - "legacy-vpc"
      - "test-*"
  - source: "terraform-aws-modules/s3-bucket/aws"
    version: "4.0.0"
    from:                # Optional: update from multiple versions
      - "3.0.0"
      - "~> 3.0"
    ignore_versions:     # Optional: skip specific versions
      - "3.5.0"
  - source: "terraform-aws-modules/security-group/aws"
    version: "5.1.0"
    from: "4.0.0"        # Optional: only update from version 4.0.0
    ignore_modules:
      - "*-deprecated"
```

Each module entry supports the following fields:
- `source` (required): Module source identifier
- `version` (required): Target version to update to
- `from` (optional): Only update modules currently at this version (or any version in a list)
  - Can be a single string: `from: "3.14.0"`
  - Can be a list of versions: `from: ["3.0.0", "~> 3.0"]`
  - Modules will be updated if their current version matches any version in the list
- `ignore_versions` (optional): Skip modules currently at these version(s)
  - Can be a single string: `ignore_versions: "3.14.0"`
  - Can be a list of versions: `ignore_versions: ["3.0.0", "~> 3.0"]`
  - Modules will be skipped if their current version matches any version in the list
  - Takes precedence over `from` filter (if a version matches both, it will be skipped)
- `ignore_modules` (optional): List of module names or wildcard patterns to skip
  - Supports exact matches: `"vpc"` matches only a module named "vpc"
  - Supports wildcards with `*`:
    - Prefix: `"legacy-*"` matches `legacy-vpc`, `legacy-network`, etc.
    - Suffix: `"*-test"` matches `vpc-test`, `network-test`, etc.
    - Both: `"*-vpc-*"` matches `prod-vpc-test`, `staging-vpc-1`, etc.
    - Any: `"*"` matches all modules (effectively disables updates for this source)

**Note about local modules:** Local modules (sources starting with `./`, `../`, or `/`) are not supported and will be skipped with a warning. The tool only updates registry modules and remote sources.

#### Examples

Update modules using a basic config file:

```bash
tf-version-bump -pattern "*.tf" -config "config.yml"
```

Update modules in production environment:

```bash
tf-version-bump -pattern "environments/prod/**/*.tf" -config "config-production.yml"
```

Update all Terraform files recursively:

```bash
tf-version-bump -pattern "**/*.tf" -config "module-updates.yml"
```

#### Example: Skipping Specific Versions with ignore_versions

You can use `ignore_versions` to skip updating modules at specific versions while updating all others. This is useful when you want to keep certain versions pinned (e.g., for compatibility reasons) but update everything else.

**Example scenario:** Update all VPC modules to version `5.0.0` EXCEPT those at version `3.14.0` and `~> 3.0` (which should remain unchanged).

**Config file** (`skip-versions.yml`):
```yaml
modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
    ignore_versions:
      - "3.14.0"
      - "~> 3.0"
```

**Terraform file before** (`main.tf`):
```hcl
module "vpc_old" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"  # Will NOT be updated (ignored)
}

module "vpc_constraint" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 3.0"  # Will NOT be updated (ignored)
}

module "vpc_newer" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "4.0.0"  # Will be updated
}
```

**Run the update:**
```bash
tf-version-bump -pattern "main.tf" -config "skip-versions.yml"
```

**Terraform file after:**
```hcl
module "vpc_old" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"  # Unchanged (ignored)
}

module "vpc_constraint" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 3.0"  # Unchanged (ignored)
}

module "vpc_newer" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.0.0"  # Updated
}
```

#### Example: Selective Updates with Multiple From Versions

You can specify multiple "from" versions to selectively update only modules matching specific versions. This is useful when you want to upgrade modules from certain versions while leaving others untouched.

**Example scenario:** Update S3 bucket modules from versions `3.0.0` and `~> 3.0` to `4.0.0`, but leave modules at `3.1.0` unchanged.

**Config file** (`selective-update.yml`):
```yaml
modules:
  - source: "terraform-aws-modules/s3-bucket/aws"
    version: "4.0.0"
    from:
      - "3.0.0"
      - "~> 3.0"
```

**Terraform file before** (`main.tf`):
```hcl
module "s3_exact" {
  source  = "terraform-aws-modules/s3-bucket/aws"
  version = "3.0.0"  # Will be updated
}

module "s3_constraint" {
  source  = "terraform-aws-modules/s3-bucket/aws"
  version = "~> 3.0"  # Will be updated
}

module "s3_other" {
  source  = "terraform-aws-modules/s3-bucket/aws"
  version = "3.1.0"  # Will NOT be updated (doesn't match)
}
```

**Run the update:**
```bash
tf-version-bump -pattern "main.tf" -config "selective-update.yml"
```

**Terraform file after:**
```hcl
module "s3_exact" {
  source  = "terraform-aws-modules/s3-bucket/aws"
  version = "4.0.0"  # Updated
}

module "s3_constraint" {
  source  = "terraform-aws-modules/s3-bucket/aws"
  version = "4.0.0"  # Updated
}

module "s3_other" {
  source  = "terraform-aws-modules/s3-bucket/aws"
  version = "3.1.0"  # Unchanged
}
```

#### Example: Combining from and ignore_versions Filters

You can combine both `from` and `ignore_versions` filters for fine-grained control. The `ignore_versions` filter takes precedence - if a version matches both filters, it will be skipped.

**Example scenario:** Update VPC modules from versions `3.x` and `4.x` to `5.0.0`, but keep version `4.0.0` pinned for compatibility.

**Config file** (`combined-filters.yml`):
```yaml
modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
    from:
      - "3.14.0"
      - "4.0.0"
      - "4.5.0"
    ignore_versions:
      - "4.0.0"  # Keep this version pinned
```

**Result:** Modules at `3.14.0` and `4.5.0` will be updated to `5.0.0`, but modules at `4.0.0` will remain unchanged.

#### Example Config Files

See the `examples/` directory for sample configuration files:

- `config-basic.yml` - Simple configuration with a few modules
- `config-advanced.yml` - Advanced configuration showing various module types (subpaths, Git sources)
- `config-production.yml` - Production-ready configuration with common AWS modules
- `config-with-ignore.yml` - Examples of using the ignore_modules feature with various patterns

### Terraform Version Mode

Update the Terraform `required_version` in terraform blocks across your configuration files.

**Basic syntax:**

```bash
tf-version-bump -pattern <glob-pattern> -terraform-version <version>
```

**Arguments:**

- `-pattern`: Glob pattern for Terraform files (required)
- `-terraform-version`: Target Terraform version (e.g., `">= 1.5"`, `"~> 1.6"`)
- `-dry-run`: (Optional) Preview changes without modifying files
- `-output`: (Optional) Output format: `text` (default) or `md` (Markdown)

**Examples:**

Update all Terraform files to require Terraform >= 1.5:

```bash
tf-version-bump -pattern "*.tf" -terraform-version ">= 1.5"
```

Update Terraform version in a specific directory:

```bash
tf-version-bump -pattern "environments/prod/*.tf" -terraform-version "~> 1.6"
```

Preview changes before applying:

```bash
tf-version-bump -pattern "**/*.tf" -terraform-version ">= 1.5" -dry-run
```

**Example transformation:**

Before:
```hcl
terraform {
  required_version = ">= 1.0"
  
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}
```

After running: `tf-version-bump -pattern "*.tf" -terraform-version ">= 1.5"`

```hcl
terraform {
  required_version = ">= 1.5"
  
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}
```

**Notes:**

- Only updates the `required_version` attribute in terraform blocks
- Provider versions are not modified
- Preserves all formatting and comments
- If a file has multiple terraform blocks (unusual but valid), all will be updated

### Provider Version Mode

Update provider versions in terraform `required_providers` blocks across your configuration files.

**Basic syntax:**

```bash
tf-version-bump -pattern <glob-pattern> -provider <provider-name> -to <version>
```

**Arguments:**

- `-pattern`: Glob pattern for Terraform files (required)
- `-provider`: Provider name (e.g., `aws`, `azurerm`, `google`)
- `-to`: Target provider version (required)
- `-dry-run`: (Optional) Preview changes without modifying files
- `-output`: (Optional) Output format: `text` (default) or `md` (Markdown)

**Examples:**

Update AWS provider to version ~> 5.0:

```bash
tf-version-bump -pattern "*.tf" -provider aws -to "~> 5.0"
```

Update Azure provider in production environment:

```bash
tf-version-bump -pattern "environments/prod/**/*.tf" -provider azurerm -to "~> 3.5"
```

Preview changes for Google Cloud provider:

```bash
tf-version-bump -pattern "*.tf" -provider google -to "~> 5.0" -dry-run
```

**Example transformation:**

Before:
```hcl
terraform {
  required_version = ">= 1.0"
  
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
    azurerm {
      source  = "hashicorp/azurerm"
      version = "~> 3.0"
    }
  }
}
```

After running: `tf-version-bump -pattern "*.tf" -provider aws -to "~> 5.0"`

```hcl
terraform {
  required_version = ">= 1.0"
  
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    azurerm {
      source  = "hashicorp/azurerm"
      version = "~> 3.0"
    }
  }
}
```

**Attribute-based syntax example:**

Before:

```hcl
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 3.0"
    }
  }
}
```

After running: `tf-version-bump -pattern "*.tf" -provider aws -to "~> 5.0"`

```hcl
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 3.0"
    }
  }
}
```

**Notes:**

- Only updates the specified provider's version
- Other providers in the same required_providers block remain unchanged
- Terraform required_version is not modified
- Preserves all formatting and comments
- Supports both block-based syntax: `aws { source = "..." version = "..." }`
- Supports attribute-based syntax: `aws = { source = "..." version = "..." }`

## How it Works

1. The tool uses `filepath.Glob` to find all files matching the specified pattern
2. For each file, it:
   - Parses the HCL structure using `hashicorp/hcl/v2`
   - Searches for `module` blocks with the specified source attribute
   - Checks if the module name matches any ignore patterns and skips if matched
   - Skips local modules (sources starting with `./`, `../`, or `/`) with a warning
   - If the `-ignore-version` flag is specified, skips modules with matching current version (takes precedence)
   - If the `-from` flag is specified, only updates modules with matching current version
   - Updates the `version` attribute to the desired version
   - If a module doesn't have a version attribute, it prints a warning and skips it (no version will be added)
   - Writes the updated content back to the file with proper formatting
3. Reports the number of files successfully updated

### Local Modules

Local modules (those with sources starting with `./`, `../`, or `/`) are automatically skipped because they reference local filesystem paths and don't use version attributes in standard Terraform configurations.

**Example warning output:**
```
Warning: Module "local_vpc" in main.tf (source: "./modules/vpc") is a local module and cannot be version-bumped, skipping
```

### Modules Without Version Attributes

By default, if a registry module matching the source pattern doesn't have a version attribute, the tool will:
- Print a warning message to stderr indicating which module was skipped
- Continue processing other modules
- Not add a version attribute to that module

**Example warning output:**
```
Warning: Module "vpc" in main.tf (source: "terraform-aws-modules/vpc/aws") has no version attribute, skipping
```

#### Force-Adding Version Attributes

If you want to add version attributes to registry modules that don't have them, use the `-force-add` flag:

```bash
# Add version attribute to registry modules that don't have one
tf-version-bump -pattern "*.tf" -module "terraform-aws-modules/vpc/aws" -to "5.0.0" -force-add

# Force-add with config file
tf-version-bump -pattern "**/*.tf" -config "config.yml" -force-add
```

**Note:** This flag only affects registry modules and remote sources. Local modules are always skipped regardless of the `-force-add` flag.

## Example Terraform File

**Before:**

```hcl
module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"

  name = "my-vpc"
  cidr = "10.0.0.0/16"
}

module "another_vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.14.0"

  name = "another-vpc"
  cidr = "172.16.0.0/16"
}
```

**After running:** `tf-version-bump -pattern "*.tf" -module "terraform-aws-modules/vpc/aws" -to "5.0.0"`

```hcl
module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.0.0"

  name = "my-vpc"
  cidr = "10.0.0.0/16"
}

module "another_vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.0.0"

  name = "another-vpc"
  cidr = "172.16.0.0/16"
}
```

Note: Both modules are updated because they share the same source attribute, regardless of their module names.

## Testing

Run the tests:

```bash
go test -v -race -coverprofile=coverage.out -covermode=atomic ./...
```

## Releases

Pre-built binaries are available on the [GitHub Releases](https://github.com/yesdevnull/tf-version-bump/releases) page.

Each release includes:
- Binaries for Linux, macOS, and Windows (amd64/arm64)
- Linux packages (deb, rpm)
- SHA256 checksums
- SLSA Level 3 provenance attestations

For verification instructions and detailed release information, see [docs/RELEASING.md](docs/RELEASING.md).

## Security Considerations

### Best Practices

- **Always use version control**: This tool modifies files in place. Ensure your Terraform files are committed to Git before running updates.
- **Test before production**: Always test updates in a development environment first, especially when using config files with multiple module updates.
- **Review changes**: Use `git diff` after running the tool to review all modifications before committing.
- **Use dry-run mode**: Run with `-dry-run` flag first to preview changes: `tf-version-bump -pattern "*.tf" -module "..." -to "..." -dry-run`

### Known Limitations

- **Concurrent execution**: This tool does not implement file locking. Running multiple instances simultaneously on the same files may cause corruption. Use external coordination (e.g., CI/CD job locks) if needed.
- **Config file trust**: YAML configuration files should come from trusted sources only. While the tool validates required fields, extremely large or malicious YAML files could cause resource exhaustion.
- **File size**: The tool loads entire files into memory for parsing. Very large Terraform files (> 100MB) may cause performance issues, though typical Terraform files are much smaller.

### Unicode Support

The tool fully supports Unicode characters in:
- Module names (e.g., `module "vpc-主要"`)
- Module sources (e.g., `source = "registry.example.com/組織/module"`)
- Ignore patterns (e.g., `ignore_modules: ["vpc-主要", "test-🚀-*"]`)

### Permissions

The tool preserves original file permissions when updating files. It runs with the same permissions as the user executing it and does not require elevated privileges.

## Advanced Usage

### Looping Through Git Branches

You can use shell scripts to run `tf-version-bump` across multiple branches matching a filter. This is useful for updating module versions across feature branches, release branches, or any set of branches matching a pattern.

#### Basic Branch Loop

Loop through all branches matching a pattern and update modules:

```bash
#!/bin/bash

# Configuration
BRANCH_PATTERN="feature/*"
MODULE_SOURCE="terraform-aws-modules/vpc/aws"
TARGET_VERSION="5.0.0"
FILE_PATTERN="**/*.tf"

# Get current branch to return to later
ORIGINAL_BRANCH=$(git branch --show-current)

# Loop through branches matching the pattern
for branch in $(git branch --list "${BRANCH_PATTERN}" --format='%(refname:short)'); do
    echo "Processing branch: $branch"

    # Checkout the branch
    git checkout "$branch" || continue

    # Run tf-version-bump
    tf-version-bump -pattern "$FILE_PATTERN" -module "$MODULE_SOURCE" -to "$TARGET_VERSION"

    # Check if there are changes to commit
    if [[ -n $(git status --porcelain) ]]; then
        git add -A
        git commit -m "chore: bump $MODULE_SOURCE to $TARGET_VERSION"
        echo "  Committed changes on $branch"
    else
        echo "  No changes needed on $branch"
    fi
done

# Return to original branch
git checkout "$ORIGINAL_BRANCH"
echo "Done! Returned to $ORIGINAL_BRANCH"
```

#### Using Config Files Across Branches

For batch updates with a config file:

```bash
#!/bin/bash

BRANCH_PATTERN="release/*"
CONFIG_FILE="module-updates.yml"
FILE_PATTERN="**/*.tf"

ORIGINAL_BRANCH=$(git branch --show-current)

for branch in $(git branch --list "$BRANCH_PATTERN" --format='%(refname:short)'); do
    echo "Processing branch: $branch"

    git checkout "$branch" || continue

    tf-version-bump -pattern "$FILE_PATTERN" -config "$CONFIG_FILE"

    if [[ -n $(git status --porcelain) ]]; then
        git add -A
        git commit -m "chore: batch update module versions"
    fi
done

git checkout "$ORIGINAL_BRANCH"
```

#### Including Remote Branches

To include remote branches that haven't been checked out locally:

```bash
#!/bin/bash

BRANCH_PATTERN="feature/*"
MODULE_SOURCE="terraform-aws-modules/vpc/aws"
TARGET_VERSION="5.0.0"

# Fetch all remote branches first
git fetch --all

ORIGINAL_BRANCH=$(git branch --show-current)

# List remote branches matching pattern (strip 'origin/' prefix)
for branch in $(git branch -r --list "origin/${BRANCH_PATTERN}" --format='%(refname:short)' | sed 's|origin/||'); do
    echo "Processing branch: $branch"

    # Checkout the branch (create if it doesn't exist locally)
    if git show-ref --verify --quiet "refs/heads/$branch"; then
        git checkout "$branch" || continue
        git pull origin "$branch" || continue
    else
        git checkout -b "$branch" "origin/$branch" || continue
    fi

    tf-version-bump -pattern "**/*.tf" -module "$MODULE_SOURCE" -to "$TARGET_VERSION"

    if [[ -n $(git status --porcelain) ]]; then
        git add -A
        git commit -m "chore: bump $MODULE_SOURCE to $TARGET_VERSION"

        # Optionally push changes
        # git push origin "$branch"
    fi
done

git checkout "$ORIGINAL_BRANCH"
```

#### Dry Run Mode

Preview what changes would be made on each branch without modifying files:

```bash
#!/bin/bash

BRANCH_PATTERN="feature/*"
MODULE_SOURCE="terraform-aws-modules/vpc/aws"
TARGET_VERSION="5.0.0"

ORIGINAL_BRANCH=$(git branch --show-current)

for branch in $(git branch --list "$BRANCH_PATTERN" --format='%(refname:short)'); do
    echo "Processing branch: $branch"
    git checkout "$branch" || continue

    # Use -dry-run to preview changes without modifying files
    tf-version-bump -pattern "**/*.tf" -module "$MODULE_SOURCE" -to "$TARGET_VERSION" -dry-run
done

git checkout "$ORIGINAL_BRANCH"
```

#### Filtering by Recent Activity

Process only branches with recent commits.

**Note:** This script uses GNU `date` syntax and requires Linux. For macOS/BSD, you'll need to modify the date commands.

```bash
#!/bin/bash

BRANCH_PATTERN="feature/*"
DAYS_AGO=30

ORIGINAL_BRANCH=$(git branch --show-current)

# Get branches with commits in the last N days
for branch in $(git branch --list "$BRANCH_PATTERN" --format='%(refname:short)'); do
    # Check if branch has commits within the time window
    last_commit=$(git log -1 --format="%ci" "$branch" 2>/dev/null)
    if [[ -n "$last_commit" ]]; then
        commit_date=$(date -d "$last_commit" +%s)
        cutoff_date=$(date -d "$DAYS_AGO days ago" +%s)

        if [[ $commit_date -gt $cutoff_date ]]; then
            echo "Processing recent branch: $branch"
            git checkout "$branch" || continue

            tf-version-bump -pattern "**/*.tf" -module "terraform-aws-modules/vpc/aws" -to "5.0.0"

            if [[ -n $(git status --porcelain) ]]; then
                git add -A
                git commit -m "chore: bump module versions"
            fi
        fi
    fi
done

git checkout "$ORIGINAL_BRANCH"
```

#### Error Handling and Logging

Production-ready script with comprehensive error handling:

```bash
#!/bin/bash

BRANCH_PATTERN="${1:-feature/*}"
MODULE_SOURCE="${2:-terraform-aws-modules/vpc/aws}"
TARGET_VERSION="${3:-5.0.0}"
LOG_FILE="version-bump-$(date +%Y%m%d-%H%M%S).log"

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" | tee -a "$LOG_FILE"
}

ORIGINAL_BRANCH=$(git branch --show-current)
PROCESSED=0
UPDATED=0
FAILED=0

log "Starting branch loop for pattern: $BRANCH_PATTERN"
log "Module: $MODULE_SOURCE -> $TARGET_VERSION"

for branch in $(git branch --list "$BRANCH_PATTERN" --format='%(refname:short)'); do
    ((PROCESSED++))
    log "Processing: $branch"

    if ! git checkout "$branch" 2>>"$LOG_FILE"; then
        log "  ERROR: Failed to checkout $branch"
        ((FAILED++))
        continue
    fi

    if ! tf-version-bump -pattern "**/*.tf" -module "$MODULE_SOURCE" -to "$TARGET_VERSION" 2>>"$LOG_FILE"; then
        log "  ERROR: tf-version-bump failed on $branch"
        ((FAILED++))
        git checkout "$ORIGINAL_BRANCH" 2>/dev/null
        continue
    fi

    if [[ -n $(git status --porcelain) ]]; then
        git add -A
        git commit -m "chore: bump $MODULE_SOURCE to $TARGET_VERSION"
        ((UPDATED++))
        log "  SUCCESS: Committed changes"
    else
        log "  SKIPPED: No changes needed"
    fi
done

git checkout "$ORIGINAL_BRANCH"

log "Complete! Processed: $PROCESSED, Updated: $UPDATED, Failed: $FAILED"
log "Log saved to: $LOG_FILE"
```

Usage:
```bash
./update-branches.sh "feature/*" "terraform-aws-modules/vpc/aws" "5.0.0"
```
