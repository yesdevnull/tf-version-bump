# Terraform Version Bump

A CLI tool written in Go that updates Terraform module versions across multiple files using glob patterns.

## Features

- Parse Terraform files using the official HashiCorp HCL library
- Update module versions by module name
- Process multiple files using glob patterns
- Preserves formatting and comments in Terraform files
- Safe and reliable HCL parsing and writing

## Installation

### Prerequisites

- Go 1.24.7 or later

### Build from source

```bash
git clone https://github.com/yesdevnull/tf-version-bump.git
cd tf-version-bump
go build -o tf-version-bump
```

## Usage

```bash
./tf-version-bump -pattern <glob-pattern> -module <module-name> -version <version>
```

### Arguments

- `-pattern`: Glob pattern for Terraform files (e.g., `*.tf`, `modules/**/*.tf`)
- `-module`: Name of the module to update
- `-version`: Desired version number

### Examples

Update all `vpc` modules to version `5.0.0` in the current directory:

```bash
./tf-version-bump -pattern "*.tf" -module "vpc" -version "5.0.0"
```

Update `s3_bucket` modules in a specific directory:

```bash
./tf-version-bump -pattern "environments/prod/*.tf" -module "s3_bucket" -version "4.1.2"
```

Update modules across subdirectories:

```bash
./tf-version-bump -pattern "modules/**/*.tf" -module "security_group" -version "4.9.0"
```

## How it Works

1. The tool uses `filepath.Glob` to find all files matching the specified pattern
2. For each file, it:
   - Parses the HCL structure using `hashicorp/hcl/v2`
   - Searches for `module` blocks with the specified name
   - Updates the `version` attribute to the desired version
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
```

**After running:** `./tf-version-bump -pattern "*.tf" -module "vpc" -version "5.0.0"`

```hcl
module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.0.0"

  name = "my-vpc"
  cidr = "10.0.0.0/16"
}
```

## Dependencies

- `github.com/hashicorp/hcl/v2` - HCL parsing and writing
- `github.com/zclconf/go-cty` - Configuration type system for HCL

## License

MIT

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.
