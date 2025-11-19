# Terraform Version Bump

A CLI tool written in Go that updates Terraform module versions across multiple files using glob patterns. The tool matches modules by their source attribute, making it easy to update all instances of a particular module to a new version.

## Features

- Parse Terraform files using the official HashiCorp HCL library
- Update module versions by matching on module source
- Process multiple files using glob patterns
- **Batch updates** via YAML configuration files
- **Git repository support** - Clone repos, filter branches, and update across multiple branches
- **Automated commits** with configurable author and commit message
- **SSH commit signing** support for secure, verified commits
- Preserves formatting and comments in Terraform files
- Safe and reliable HCL parsing and writing
- Comprehensive test suite

## Installation

### Prerequisites

- Go 1.24 or later

### Option 1: Install with go install

Install the latest version using `go install`:

```bash
go install github.com/yesdevnull/tf-version-bump@latest
```

This will install the `tf-version-bump` binary to your `$GOPATH/bin` directory (usually `~/go/bin`). Make sure this directory is in your `PATH`.

**Verify the installation:**

```bash
# Check if the binary is accessible
tf-version-bump --help

# If not in PATH, you can run it directly
$GOPATH/bin/tf-version-bump --help
# or typically
~/go/bin/tf-version-bump --help
```

### Option 2: Build from source

Alternatively, you can build from source:

```bash
git clone https://github.com/yesdevnull/tf-version-bump.git
cd tf-version-bump
go build -o tf-version-bump

# Run the locally built binary
./tf-version-bump --help
```

## Usage

The tool supports three modes of operation:

1. **Single Module Mode**: Update one module at a time via command-line flags
2. **Config File Mode**: Update multiple modules in one operation using a YAML configuration file
3. **Git Repository Mode**: Clone a repository, filter branches, and update modules across multiple branches with automated commits

### Single Module Mode

Basic syntax:

```bash
tf-version-bump -pattern <glob-pattern> -module <module-source> -version <version>
```

**Note:** If you built from source, use `./tf-version-bump` instead of `tf-version-bump`.

#### Arguments

- `-pattern`: Glob pattern for Terraform files (e.g., `*.tf`, `modules/**/*.tf`)
- `-module`: Source of the module to update (e.g., `terraform-aws-modules/vpc/aws`)
- `-version`: Desired version number
- `-force-add`: (Optional) Add version attribute to modules that don't have one (default: false, skip with warning)

#### Examples

Update all VPC modules from the Terraform AWS modules registry to version `5.0.0`:

```bash
tf-version-bump -pattern "*.tf" -module "terraform-aws-modules/vpc/aws" -version "5.0.0"
```

Update S3 bucket modules in a specific directory:

```bash
tf-version-bump -pattern "environments/prod/*.tf" -module "terraform-aws-modules/s3-bucket/aws" -version "4.1.2"
```

Update modules across subdirectories (recursive):

```bash
tf-version-bump -pattern "modules/**/*.tf" -module "terraform-aws-modules/security-group/aws" -version "4.9.0"
```

Update modules with subpaths in their source:

```bash
tf-version-bump -pattern "*.tf" -module "terraform-aws-modules/iam/aws//modules/iam-user" -version "5.2.0"
```

Update local modules:

```bash
tf-version-bump -pattern "*.tf" -module "./modules/my-module" -version "1.0.0"
```

Update Git-based modules:

```bash
tf-version-bump -pattern "*.tf" -module "git::https://github.com/example/terraform-module.git" -version "v1.2.3"
```

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

#### Config File Format

Create a YAML file with the following structure:

```yaml
modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
  - source: "terraform-aws-modules/s3-bucket/aws"
    version: "4.0.0"
  - source: "terraform-aws-modules/security-group/aws"
    version: "5.1.0"
```

**Note about local modules:** While local modules (e.g., `./modules/vpc` or `../shared-modules/s3`) typically don't use version attributes in standard Terraform configurations, this tool requires a version field for all modules in the config file. However, if a local module in your Terraform files doesn't have a version attribute, the tool will print a warning and skip it rather than adding a version attribute. This approach allows you to specify desired versions in your config while respecting Terraform's conventions for local modules.

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
- `config-advanced.yml` - Advanced configuration showing various module types (subpaths, local modules, Git sources)
- `config-production.yml` - Production-ready configuration with common AWS modules
- `config-git.yml` - Git repository configuration with branch filtering and automated commits

### Git Repository Mode

For advanced workflows, you can configure the tool to clone a Git repository, filter branches by pattern, and apply version updates across multiple branches with automated commits.

**Note:** If you built from source, use `./tf-version-bump` instead of `tf-version-bump`.

#### Git Config Format

Add a `git` section to your YAML configuration file:

```yaml
modules:
  - source: "terraform-aws-modules/vpc/aws"
    version: "5.0.0"
  - source: "terraform-aws-modules/s3-bucket/aws"
    version: "4.0.0"

git:
  # Repository URL to clone
  repository: "https://github.com/example/terraform-infrastructure.git"

  # Regex pattern to filter branches (e.g., "main", "release/.*", "feature/.*")
  branch_filter: "release/.*"

  # Git author information (required)
  author_name: "Terraform Bot"
  author_email: "terraform-bot@example.com"

  # Path to SSH key for signing commits (optional)
  signing_key: "/home/user/.ssh/id_ed25519"

  # Custom commit message (optional)
  commit_message: |
    chore: update terraform module versions

    Automated update of Terraform module versions.

  # Whether to push changes to remote (default: false)
  push: false
```

#### Git Configuration Options

- `repository` (required): Git repository URL to clone
- `branch_filter` (required): Regex pattern to match branch names (e.g., `release/.*` matches all release branches)
- `author_name` (required): Git commit author name
- `author_email` (required): Git commit author email
- `signing_key` (optional): Path to SSH private key for signing commits
- `commit_message` (optional): Custom commit message. If not provided, a default message will be generated
- `push` (optional): Whether to push changes to remote repository (default: `false`)

#### How Git Mode Works

When a `git` section is present in your config file:

1. **Clone**: The tool clones the specified repository to a temporary directory
2. **Filter Branches**: Lists all remote branches and filters them using the regex pattern
3. **Process Each Branch**: For each matching branch:
   - Checks out the branch
   - Finds files matching the glob pattern
   - Updates module versions as specified
   - Commits changes with the configured author
   - Optionally pushes to remote if `push: true`
4. **Report**: Displays a summary of successfully processed branches

#### Examples

Update all release branches in a repository:

```bash
tf-version-bump -pattern "**/*.tf" -config "config-git.yml"
```

**Example output:**

```
Cloning repository: https://github.com/example/terraform-infrastructure.git
...

Found 3 matching branch(es):
  - release/1.0
  - release/2.0
  - release/3.0

Processing branch: release/1.0
  Found 5 file(s) matching pattern
  ✓ Updated module 'terraform-aws-modules/vpc/aws' to version '5.0.0' in main.tf
  ✓ Updated module 'terraform-aws-modules/s3-bucket/aws' to version '4.0.0' in storage.tf
  Committed changes: a1b2c3d4
✓ Successfully processed branch: release/1.0

...

Completed processing 3/3 branch(es)
```

#### SSH Commit Signing

To sign commits with an SSH key, specify the path to your SSH private key in the `signing_key` field:

```yaml
git:
  signing_key: "/home/user/.ssh/id_ed25519"
```

**Note:** The current implementation uses go-git v5, which has limited SSH signing support. For full SSH signing capabilities, consider using the git CLI with `GIT_SSH_COMMAND` environment variable or upgrading to a newer version of go-git when available.

For production use with SSH signing, you may want to:
1. Configure your Git client to sign commits by default
2. Use SSH agent for key management
3. Verify signatures with `git log --show-signature`

#### Security Considerations

- **Authentication**: For private repositories, ensure you have proper authentication configured (SSH keys, tokens, etc.)
- **Push Permission**: When using `push: true`, ensure the authentication method has write access to the repository
- **Branch Protection**: Be aware of branch protection rules that may prevent direct pushes
- **Review Changes**: Consider setting `push: false` (default) and reviewing changes before pushing manually

## How it Works

1. The tool uses `filepath.Glob` to find all files matching the specified pattern
2. For each file, it:
   - Parses the HCL structure using `hashicorp/hcl/v2`
   - Searches for `module` blocks with the specified source attribute
   - Updates the `version` attribute to the desired version
   - If a module doesn't have a version attribute, it prints a warning and skips it (no version will be added)
   - Writes the updated content back to the file with proper formatting
3. Reports the number of files successfully updated

### Modules Without Version Attributes

By default, if a module matching the source pattern doesn't have a version attribute (common for local modules), the tool will:
- Print a warning message to stderr indicating which module was skipped
- Continue processing other modules
- Not add a version attribute to that module

This behavior ensures the tool doesn't make unintended changes to modules that don't typically use version attributes, such as local modules.

**Example warning output:**
```
Warning: Module "local_vpc" in main.tf (source: "./modules/vpc") has no version attribute, skipping
```

#### Force-Adding Version Attributes

If you want to add version attributes to modules that don't have them, use the `-force-add` flag:

```bash
# Add version attribute to local modules that don't have one
tf-version-bump -pattern "*.tf" -module "./modules/vpc" -version "1.0.0" -force-add

# Force-add with config file
tf-version-bump -pattern "**/*.tf" -config "config.yml" -force-add
```

**Note:** Use this flag cautiously, especially with local modules, as Terraform typically doesn't use version attributes for local module sources.

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

**After running:** `tf-version-bump -pattern "*.tf" -module "terraform-aws-modules/vpc/aws" -version "5.0.0"`

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

The project includes a comprehensive test suite covering various scenarios:

- Updating single and multiple modules
- Modules with and without version attributes
- Mixed modules with different sources
- Modules with subpaths in sources
- Config file parsing and validation
- Batch updates from configuration files
- Preserving formatting and comments
- Error handling for invalid HCL and missing files

Run the tests:

```bash
go test -v
```

## Dependencies

- `github.com/hashicorp/hcl/v2` - HCL parsing and writing
- `github.com/zclconf/go-cty` - Configuration type system for HCL
- `gopkg.in/yaml.v3` - YAML parsing for configuration files
- `github.com/go-git/go-git/v5` - Git operations (cloning, branching, committing)

## License

MIT

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.
