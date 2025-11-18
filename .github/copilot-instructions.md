# Copilot Coding Agent Instructions

## Repository Overview

**tf-version-bump** is a CLI tool written in Go that updates Terraform module versions across multiple files using glob patterns. The tool matches modules by their source attribute, making it easy to update all instances of a particular module to a new version.

- **Repository Size**: ~6.3 MB (small repository)
- **Language**: Go 1.24+ (currently using Go 1.24.10)
- **Lines of Code**: ~420 lines total (119 in main.go, 300 in main_test.go)
- **Key Dependencies**: 
  - `github.com/hashicorp/hcl/v2` v2.24.0 - Official HashiCorp HCL parser/writer
  - `github.com/zclconf/go-cty` v1.17.0 - Configuration type system for HCL

## Project Structure

### Root Directory Files
```
.github/              # GitHub Actions workflows and configurations
├── workflows/
│   └── ci.yml       # CI pipeline (test, build, lint)
└── dependabot.yml   # Dependency update configuration
.gitignore           # Git ignore patterns (binaries, IDE files, OS files)
.golangci.yml        # Linter configuration
README.md            # Project documentation with usage examples
go.mod               # Go module definition (Go 1.24 required)
go.sum               # Dependency checksums
main.go              # Main application code (119 lines)
main_test.go         # Comprehensive test suite (300 lines)
examples/            # Test Terraform files for validation
├── main.tf
├── modules.tf
├── complex.tf
├── heavily_commented.tf
└── unusual_formatting.tf
```

### Key Files

**main.go** - Contains:
- `main()` function: CLI argument parsing using flag package
- `updateModuleVersion(filename, moduleSource, version)`: Core function that parses HCL, finds modules, updates versions
- `trimQuotes(s)`: Helper to remove quotes from strings

**main_test.go** - Contains comprehensive tests covering:
- Unit tests for trimQuotes helper
- Integration tests for updateModuleVersion with various scenarios
- Tests for formatting preservation, error handling, edge cases

## Build and Test Instructions

### Prerequisites
- Go 1.24 or later (go.mod specifies `go 1.24`)
- All commands should be run from the repository root

### Dependency Management

**ALWAYS run dependency download and verification before building or testing:**
```bash
go mod download
go mod verify
```

These commands:
- Download required dependencies to `~/go/pkg/mod/`
- Verify integrity using go.sum checksums
- Take ~5-10 seconds on first run
- Are cached for subsequent runs

Note: `go build` and `go test` will auto-download dependencies if not present, but explicit download is recommended for CI/CD reliability.

### Building

**Standard build (creates binary in current directory):**
```bash
go build -o tf-version-bump
```
- Takes ~0.5-1 second
- Creates executable `tf-version-bump` in current directory
- Binary is gitignored (see .gitignore line 18)

**Cross-platform builds** (as used in CI):
```bash
# Linux
GOOS=linux GOARCH=amd64 go build -o tf-version-bump-linux-amd64
GOOS=linux GOARCH=arm64 go build -o tf-version-bump-linux-arm64

# macOS
GOOS=darwin GOARCH=amd64 go build -o tf-version-bump-darwin-amd64
GOOS=darwin GOARCH=arm64 go build -o tf-version-bump-darwin-arm64

# Windows
GOOS=windows GOARCH=amd64 go build -o tf-version-bump-windows-amd64.exe
GOOS=windows GOARCH=arm64 go build -o tf-version-bump-windows-arm64.exe
```
- All cross-platform builds work without additional setup
- Each build takes ~0.5-1 second

**Clean build** (remove cached artifacts):
```bash
go clean
go build -o tf-version-bump
```

### Testing

**Standard test run:**
```bash
go test -v ./...
```
- Takes ~5 seconds
- Runs all tests in verbose mode
- All tests should pass

**Test with race detector and coverage** (as used in CI):
```bash
go test -v -race -coverprofile=coverage.out -covermode=atomic ./...
```
- Takes ~5-10 seconds
- Enables race condition detection
- Generates coverage.out file (gitignored)
- Current coverage: 50.0% of statements
- This is the REQUIRED test command for CI validation

**Quick test without verbosity:**
```bash
go test ./...
```
- Takes ~5 seconds
- Less output, faster for quick checks

### Linting

**Install golangci-lint** (required before first lint):
```bash
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin v2.5.0
```
- Installs to `~/go/bin/golangci-lint`
- Version v2.5.0 is specified in CI workflow
- Takes ~30-60 seconds

**Run linter:**
```bash
$(go env GOPATH)/bin/golangci-lint run --timeout=5m
```
OR if `~/go/bin` is in PATH:
```bash
golangci-lint run --timeout=5m
```
- Takes ~10-30 seconds
- Timeout is 5 minutes (configured in .golangci.yml and CI)
- Should report "0 issues" on clean code

**Linter Configuration** (.golangci.yml):
- Enabled linters: errcheck, govet, ineffassign, staticcheck, unused, gocritic, gocyclo, misspell, unconvert, unparam, whitespace
- Cyclomatic complexity threshold: 15
- Tests are linted (run.tests: true)
- No maximum same issues limit (issues.max-same-issues: 0)

## CI/CD Pipeline

The repository uses GitHub Actions with three jobs that run on push to main/master or on pull requests:

### 1. Test Job
- Runs on: Ubuntu Latest
- Matrix: Go versions 1.24 and 1.25
- Steps:
  1. Checkout code
  2. Setup Go with matrix version
  3. Cache Go modules (uses go.sum hash)
  4. `go mod download`
  5. `go mod verify`
  6. `go test -v -race -coverprofile=coverage.out -covermode=atomic ./...`
  7. Upload coverage to Codecov (only on Go 1.25)

### 2. Build Job
- Runs on: Ubuntu Latest
- Depends on: Test job completion
- Go version: 1.25
- Builds for 6 platforms: Linux (amd64, arm64), macOS (amd64, arm64), Windows (amd64, arm64)
- Uploads artifacts with 30-day retention

### 3. Lint Job
- Runs on: Ubuntu Latest (runs in parallel with test/build)
- Go version: 1.25
- Installs golangci-lint v2.5.0
- Runs: `golangci-lint run --timeout=5m`

**All three jobs must pass for CI to succeed.**

## Development Workflow

### Making Code Changes

1. **Before making changes**, verify current state:
   ```bash
   go mod download && go mod verify
   go test -v -race -coverprofile=coverage.out -covermode=atomic ./...
   $(go env GOPATH)/bin/golangci-lint run --timeout=5m
   ```

2. **Make your code changes** in main.go or main_test.go

3. **Build to check for compilation errors:**
   ```bash
   go build -o tf-version-bump
   ```

4. **Test your changes:**
   ```bash
   go test -v -race -coverprofile=coverage.out -covermode=atomic ./...
   ```

5. **Lint your changes:**
   ```bash
   $(go env GOPATH)/bin/golangci-lint run --timeout=5m
   ```

6. **Test the binary manually** (optional but recommended):
   ```bash
   ./tf-version-bump -pattern "examples/*.tf" -module "terraform-aws-modules/vpc/aws" -version "5.0.0"
   git checkout examples/  # Restore example files
   ```

### Adding Tests

When adding new functionality:
- Add corresponding test cases to main_test.go
- Follow existing test patterns (table-driven tests)
- Ensure tests are deterministic and don't require external files beyond temp files
- Tests should clean up after themselves

### Common Gotchas

1. **Binary in repository**: The `tf-version-bump` binary is gitignored. If you accidentally commit it, run `git rm tf-version-bump`.

2. **Race detector**: Always run tests with `-race` flag before committing, as CI requires it.

3. **Coverage file**: `coverage.out` is gitignored. Don't commit it.

4. **Go module cache**: Dependencies are cached in `~/go/pkg/mod/`. If you have issues, try `go clean -modcache` then `go mod download`.

5. **golangci-lint version**: CI uses v2.5.0. Always install this specific version to match CI behavior.

6. **Glob patterns**: The tool uses `filepath.Glob`, which doesn't support `**` for recursive globbing on all systems. Use explicit patterns like `modules/**/*.tf` with caution.

7. **HCL formatting**: The tool uses `hclwrite.Format()` which may change whitespace. This is expected behavior.

## Architecture Notes

### How the Tool Works

1. **Input**: Takes three flags: `-pattern` (glob), `-module` (source), `-version` (target version)
2. **File Discovery**: Uses `filepath.Glob()` to find matching .tf files
3. **Parsing**: For each file, uses `hclwrite.ParseConfig()` to parse HCL
4. **Matching**: Iterates through blocks, finds `module` blocks with matching `source` attribute
5. **Updating**: Uses `block.Body().SetAttributeValue("version", cty.StringVal(version))` to update/add version
6. **Writing**: Formats with `hclwrite.Format()` and writes back to file with 0644 permissions

### Code Organization

- Main logic is in `updateModuleVersion()` function (~50 lines)
- CLI flag parsing and file iteration in `main()` (~40 lines)
- Helper function `trimQuotes()` handles quote removal (~10 lines)
- Test file has ~300 lines with comprehensive coverage of edge cases

### Testing Strategy

Tests use:
- Temporary files created with `os.CreateTemp()`
- Table-driven test structure with `tests := []struct{...}`
- Subtests with `t.Run()` for better organization
- File cleanup with `defer os.Remove()`

## Quick Reference Commands

```bash
# Full validation sequence (run before committing)
go mod download && go mod verify
go test -v -race -coverprofile=coverage.out -covermode=atomic ./...
$(go env GOPATH)/bin/golangci-lint run --timeout=5m
go build -o tf-version-bump

# Quick check
go test ./... && go build

# Clean and rebuild
go clean && go build -o tf-version-bump

# Run the tool
./tf-version-bump -pattern "*.tf" -module "terraform-aws-modules/vpc/aws" -version "5.0.0"
```

## Important Notes

- **Always trust these instructions first**. Only search or explore the codebase if information here is incomplete or incorrect.
- **Do not modify example files** in the `examples/` directory unless specifically required for testing. These are reference files.
- **Run the full test suite** with race detector before finalizing changes. This matches CI requirements exactly.
- **Version requirements**: Go 1.24+ is required (specified in go.mod). CI tests on 1.24 and 1.25.
- **No external dependencies** for running the tool itself - all dependencies are vendored through go.mod.
- **Dependabot is active**: Weekly automated PRs for Go modules and GitHub Actions updates.
