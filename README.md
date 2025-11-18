# Terraform Version Bump

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

### Prerequisites

- Go 1.24 or later

### Install with go install

The easiest way to install the tool is using `go install`:

```bash
go install github.com/yesdevnull/tf-version-bump@latest
```

This will install the `tf-version-bump` binary to your `$GOPATH/bin` directory (usually `~/go/bin`). Make sure this directory is in your `PATH`.

### Build from source

Alternatively, you can build from source:

```bash
git clone https://github.com/yesdevnull/tf-version-bump.git
cd tf-version-bump
go build -o tf-version-bump
```

## Usage

The tool supports two modes of operation:

1. **Single Module Mode**: Update one module at a time
2. **Config File Mode**: Update multiple modules using a YAML configuration file

### Single Module Mode

```bash
./tf-version-bump -pattern <glob-pattern> -module <module-source> -version <version>
```

#### Arguments

- `-pattern`: Glob pattern for Terraform files (e.g., `*.tf`, `modules/**/*.tf`)
- `-module`: Source of the module to update (e.g., `terraform-aws-modules/vpc/aws`)
- `-version`: Desired version number

#### Examples

Update all VPC modules from the Terraform AWS modules registry to version `5.0.0`:

```bash
./tf-version-bump -pattern "*.tf" -module "terraform-aws-modules/vpc/aws" -version "5.0.0"
```

Update S3 bucket modules in a specific directory:

```bash
./tf-version-bump -pattern "environments/prod/*.tf" -module "terraform-aws-modules/s3-bucket/aws" -version "4.1.2"
```

Update modules across subdirectories:

```bash
./tf-version-bump -pattern "modules/**/*.tf" -module "terraform-aws-modules/security-group/aws" -version "4.9.0"
```

Update modules with subpaths in their source:

```bash
./tf-version-bump -pattern "*.tf" -module "terraform-aws-modules/iam/aws//modules/iam-user" -version "5.2.0"
```

Update local modules:

```bash
./tf-version-bump -pattern "*.tf" -module "./modules/my-module" -version "1.0.0"
```

### Config File Mode

For updating multiple modules at once, use a YAML configuration file:

```bash
./tf-version-bump -pattern <glob-pattern> -config <config-file>
```

#### Arguments

- `-pattern`: Glob pattern for Terraform files (required)
- `-config`: Path to YAML configuration file

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

#### Examples

Update modules using a basic config file:

```bash
./tf-version-bump -pattern "*.tf" -config "config.yml"
```

Update modules in production environment:

```bash
./tf-version-bump -pattern "environments/prod/**/*.tf" -config "config-production.yml"
```

Update all Terraform files recursively:

```bash
./tf-version-bump -pattern "**/*.tf" -config "module-updates.yml"
```

#### Example Config Files

See the `examples/` directory for sample configuration files:

- `config-basic.yml` - Simple configuration with a few modules
- `config-advanced.yml` - Advanced configuration showing various module types (subpaths, local modules, Git sources)
- `config-production.yml` - Production-ready configuration with common AWS modules

## How it Works

1. The tool uses `filepath.Glob` to find all files matching the specified pattern
2. For each file, it:
   - Parses the HCL structure using `hashicorp/hcl/v2`
   - Searches for `module` blocks with the specified source attribute
   - Updates the `version` attribute to the desired version
   - If a module doesn't have a version attribute, it adds one
   - Writes the updated content back to the file with proper formatting
3. Reports the number of files successfully updated

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

**After running:** `./tf-version-bump -pattern "*.tf" -module "terraform-aws-modules/vpc/aws" -version "5.0.0"`

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

## License

MIT

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.
