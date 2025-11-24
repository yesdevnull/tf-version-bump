# Terraform Version Bump

> **Note:** This repository is an experiment for generative AI coding tools. It may contain bugs, incomplete features, or other issues. Use at your own discretion.

A CLI tool written in Go that updates Terraform module versions across multiple files using glob patterns. The tool matches modules by their source attribute, making it easy to update all instances of a particular module to a new version.

## Features

- Parse Terraform files using the official HashiCorp HCL library
- Update module versions by matching on module source
- Process multiple files using glob patterns
- **Batch updates** via YAML configuration files
- Preserves formatting and comments in Terraform files
- Safe and reliable HCL parsing and writing
- Comprehensive test suite

## Installation

### Option 1: Install with go install

The easiest method if you have Go installed:

```bash
go install github.com/yesdevnull/tf-version-bump@latest
```

This installs the binary to your `$GOPATH/bin` directory (usually `~/go/bin`). Ensure this directory is in your `PATH`.

### Option 2: Download pre-built binary (recommended for CI/production)

Download a pre-built binary from the [GitHub Releases](https://github.com/yesdevnull/tf-version-bump/releases) page with verification:

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

### Option 3: Build from source

```bash
git clone https://github.com/yesdevnull/tf-version-bump.git
cd tf-version-bump
go build -o tf-version-bump

# Run the locally built binary
./tf-version-bump --help
```

## Usage

The tool supports two modes of operation:

1. **Single Module Mode**: Update one module at a time via command-line flags
2. **Config File Mode**: Update multiple modules in one operation using a YAML configuration file

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
- `-from`: (Optional) Only update modules with this current version (e.g., `4.0.0`)
- `-force-add`: (Optional) Add version attribute to modules that don't have one (default: false, skip with warning)
- `-dry-run`: (Optional) Show what changes would be made without actually modifying files

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

Update Git-based modules:

```bash
tf-version-bump -pattern "*.tf" -module "git::https://github.com/example/terraform-module.git" -to "v1.2.3"
```

Preview changes without modifying files (dry-run):

```bash
tf-version-bump -pattern "**/*.tf" -module "terraform-aws-modules/vpc/aws" -to "5.0.0" -dry-run
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

#### Config File Format

Create a YAML file with the following structure:

```yaml
modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
    from: "3.14.0"  # Optional: only update if current version is 3.14.0
  - source: "terraform-aws-modules/s3-bucket/aws"
    version: "4.0.0"
  - source: "terraform-aws-modules/security-group/aws"
    version: "5.1.0"
    from: "4.0.0"   # Optional: only update from version 4.0.0
```

Each module entry supports the following fields:
- `source` (required): Module source identifier
- `version` (required): Target version to update to
- `from` (optional): Only update modules currently at this version

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

#### Example Config Files

See the `examples/` directory for sample configuration files:

- `config-basic.yml` - Simple configuration with a few modules
- `config-advanced.yml` - Advanced configuration showing various module types (subpaths, Git sources)
- `config-production.yml` - Production-ready configuration with common AWS modules

## How it Works

1. The tool uses `filepath.Glob` to find all files matching the specified pattern
2. For each file, it:
   - Parses the HCL structure using `hashicorp/hcl/v2`
   - Searches for `module` blocks with the specified source attribute
   - Skips local modules (sources starting with `./`, `../`, or `/`) with a warning
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
