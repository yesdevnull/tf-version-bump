# CLAUDE.md - AI Assistant Guide for tf-version-bump

This document provides comprehensive guidance for AI assistants (like Claude) working with the tf-version-bump codebase. It covers the project structure, development workflows, code conventions, and key architectural decisions.

## Table of Contents

1. [Project Overview](#project-overview)
2. [Codebase Structure](#codebase-structure)
3. [Development Workflows](#development-workflows)
4. [Code Architecture](#code-architecture)
5. [Key Conventions](#key-conventions)
6. [Testing Guidelines](#testing-guidelines)
7. [Common Operations](#common-operations)
8. [Important Files Reference](#important-files-reference)

---

## Project Overview

**tf-version-bump** is a CLI tool written in Go that automates updating Terraform module versions, Terraform required_version, and provider versions across multiple files using glob patterns.

### Purpose

- **Primary**: Update Terraform module versions by matching module source attributes
- **Secondary**: Update Terraform required_version in terraform blocks
- **Tertiary**: Update provider versions in required_providers blocks
- **Key benefit**: Safe HCL parsing using official HashiCorp libraries while preserving formatting and comments

### Experimental Status

This repository is an experiment for generative AI coding tools. It may contain bugs or incomplete features. Always maintain version control and test changes thoroughly.

### Core Technologies

- **Language**: Go 1.24+
- **HCL Parsing**: `github.com/hashicorp/hcl/v2` (official HashiCorp library)
- **Config Files**: YAML with strict validation (`gopkg.in/yaml.v3`)
- **Build**: Standard Go toolchain + GoReleaser for releases
- **CI/CD**: GitHub Actions
- **Linting**: golangci-lint

---

## Codebase Structure

```
tf-version-bump/
├── main.go                          # Main entry point, CLI parsing, core logic (900+ lines)
├── config.go                        # YAML config loading and validation (187 lines)
├── *_test.go                        # Comprehensive test suite (50+ test files)
│
├── schema/
│   └── config-schema.json           # JSON Schema for YAML validation
│
├── examples/                        # Example Terraform files and configs
│   ├── *.tf                         # Sample Terraform files (various complexity levels)
│   └── config-*.yml                 # Example YAML configurations
│
├── .github/
│   ├── workflows/
│   │   ├── ci.yml                   # Main CI: test + build (Go 1.24, 1.25)
│   │   ├── lint.yml                 # golangci-lint checks
│   │   ├── codeql.yml               # Security analysis
│   │   └── release.yml              # GoReleaser with SLSA provenance
│   ├── dependabot.yml               # Automated dependency updates
│   └── copilot-instructions.md      # Detailed AI assistant instructions
│
├── docs/
│   └── RELEASING.md                 # Release process documentation
│
├── Makefile                         # Development commands (test, build, coverage)
├── .goreleaser.yaml                 # Multi-platform build configuration
├── .golangci.yml                    # Linter configuration
├── go.mod                           # Go module dependencies
├── README.md                        # User-facing documentation
└── AGENTS.md                        # Comprehensive AI agent guide

```

### File Organization

- **Single package design**: All Go code is in the `main` package
- **Test files**: Separated by concern (e.g., `config_test.go`, `chaos_test.go`, `coverage_test.go`)
- **No subdirectories**: Simple flat structure for a CLI tool
- **Examples directory**: Contains both `.tf` and `.yml` files for testing and documentation

---

## Development Workflows

### Building

```bash
# Build locally (creates ./tf-version-bump binary)
go build -o tf-version-bump .

# Or use Makefile
make build

# Install to $GOPATH/bin
go install .
# or
make install
```

### Testing

```bash
# Run all tests
go test -v ./...
# or
make test

# Run tests with race detection
go test -v -race ./...
# or
make test-verbose

# Generate coverage report
go test -v -race -coverprofile=coverage.out -covermode=atomic ./...
go tool cover -func=coverage.out
# or
make test-coverage

# Generate HTML coverage report
make coverage-html  # Creates coverage.html
```

### Linting

```bash
# Install golangci-lint (if not already installed)
# See: https://golangci-lint.run/usage/install/

# Run linter
golangci-lint run

# Run linter with auto-fix
golangci-lint run --fix
```

The project uses `.golangci.yml` for configuration. CI runs linting automatically.

### Running the Tool

```bash
# After building locally
./tf-version-bump --help

# Update a single module
./tf-version-bump -pattern "*.tf" -module "terraform-aws-modules/vpc/aws" -to "5.0.0"

# Use a config file for batch updates
./tf-version-bump -pattern "**/*.tf" -config examples/config-basic.yml

# Dry run to preview changes
./tf-version-bump -pattern "*.tf" -module "terraform-aws-modules/vpc/aws" -to "5.0.0" -dry-run

# Update Terraform required_version
./tf-version-bump -pattern "*.tf" -terraform-version ">= 1.5"

# Update provider version
./tf-version-bump -pattern "*.tf" -provider aws -to "~> 5.0"
```

### Release Process

Releases are automated via GitHub Actions and GoReleaser:

1. **Create a tag**: `git tag -a v1.0.0 -m "Release v1.0.0"`
2. **Push the tag**: `git push origin v1.0.0`
3. **GitHub Actions**: Automatically builds binaries, creates checksums, generates SLSA provenance
4. **Artifacts**: Linux, macOS, Windows (amd64/arm64), deb/rpm packages

See `docs/RELEASING.md` for detailed release documentation.

---

## Code Architecture

### Main Components

#### 1. CLI Flag Parsing (`main.go`)

- **Type**: `cliFlags` struct holds all command-line arguments
- **Custom types**: `stringSliceFlag` allows multiple `-from` and `-ignore-version` flags
- **Validation**: Flags are validated in `parseFlags()` and `main()`
- **Modes**: Single module, config file, Terraform version, provider version

#### 2. Config File Loading (`config.go`)

- **Type**: `Config` struct represents YAML structure
- **Features**:
  - Strict YAML parsing with `KnownFields(true)`
  - Custom `FromVersions` type to accept both string and []string
  - Whitespace trimming and empty value filtering
  - Comprehensive validation of required fields

**Config Structure**:
```go
type Config struct {
    TerraformVersion string           // Optional: Terraform required_version
    Providers        []ProviderUpdate // Optional: Provider updates
    Modules          []ModuleUpdate   // Optional: Module updates
}

type ModuleUpdate struct {
    Source         string       // Required: Module source
    Version        string       // Required: Target version
    From           FromVersions // Optional: Update only from these versions
    IgnoreVersions FromVersions // Optional: Skip these versions
    IgnoreModules  []string     // Optional: Name/pattern ignore list
}
```

#### 3. HCL File Processing (`main.go`)

**Key Functions**:

- `processFile()`: Main file processing loop
- `updateModuleVersion()`: Updates a single module block
- `updateTerraformVersion()`: Updates terraform required_version
- `updateProviderVersion()`: Updates provider versions

**HCL Manipulation Pattern**:
```go
// 1. Read file
src, err := os.ReadFile(filename)

// 2. Parse HCL
file, diags := hclwrite.ParseConfig(src, filename, hcl.Pos{Line: 1, Column: 1})

// 3. Navigate blocks
for _, block := range file.Body().Blocks() {
    if block.Type() == "module" {
        // Find and update version attribute
        body := block.Body()
        body.SetAttributeValue("version", cty.StringVal(targetVersion))
    }
}

// 4. Write back
os.WriteFile(filename, file.Bytes(), fileInfo.Mode())
```

#### 4. Pattern Matching

- **Glob patterns**: `filepath.Glob()` for file matching (supports `**/*.tf`)
- **Module ignore patterns**: Custom wildcard matching with `*` support
  - Exact match: `"vpc"` matches only "vpc"
  - Prefix: `"legacy-*"` matches "legacy-vpc", "legacy-network"
  - Suffix: `"*-test"` matches "vpc-test", "network-test"
  - Contains: `"*-vpc-*"` matches "prod-vpc-test", "staging-vpc-1"

### Error Handling Strategy

1. **File-level errors**: Log and continue to next file
2. **Warnings**: Printed to stderr for:
   - Local modules (skipped by design)
   - Modules without version attributes (unless `-force-add`)
   - Modules matching ignore patterns
3. **Fatal errors**: Only for invalid flags or config file parsing
4. **Safe defaults**: Conservative behavior (skip rather than guess)

### Version Filtering Logic

**Priority order** (in `updateModuleVersion()`):

1. **Ignore patterns first**: If module name matches `ignore_modules`, skip
2. **Ignore versions**: If current version in `ignore_versions`, skip (takes precedence)
3. **From filter**: If `from` is set and current version doesn't match, skip
4. **Update**: If all checks pass, update the version

---

## Key Conventions

### Code Style

1. **Go standard formatting**: Use `go fmt` (enforced by CI)
2. **Comprehensive comments**: All exported functions have doc comments
3. **Error wrapping**: Use `fmt.Errorf("context: %w", err)` for error chains
4. **Package-level doc**: `main.go` starts with package documentation

### Naming Conventions

- **Files**: `snake_case` with descriptive suffixes (`_test.go`, `_integration_test.go`)
- **Functions**: `camelCase` for unexported, `PascalCase` for exported
- **Variables**: Descriptive names, avoid single letters except in short loops
- **Constants**: Not used extensively; build info uses `var` for ldflags

### Testing Patterns

#### Test File Organization

- **Unit tests**: `*_test.go` files test specific functions
- **Integration tests**: `*_integration_test.go` test full workflows
- **Coverage tests**: `coverage_test.go` ensures edge cases are tested
- **Chaos tests**: `chaos_test.go`, `chaos_advanced_test.go` test error conditions

#### Test Naming

```go
// Pattern: Test<FunctionName>_<Scenario>
func TestLoadConfig_ValidYAML(t *testing.T)
func TestLoadConfig_InvalidYAML(t *testing.T)
func TestUpdateModuleVersion_WithFromFilter(t *testing.T)
```

#### Test Structure

```go
func TestExample(t *testing.T) {
    // 1. Setup: Create temp files, prepare test data

    // 2. Execute: Call the function being tested

    // 3. Assert: Check results with descriptive error messages

    // 4. Cleanup: Defer cleanup or use t.Cleanup()
}
```

#### Temporary Files

```go
// Always use t.TempDir() for test files
tmpDir := t.TempDir()
testFile := filepath.Join(tmpDir, "test.tf")
```

### Output Formatting

- **Quote function**: Use `quote(s, format)` for consistent string quoting
  - `"text"` format: single quotes `'vpc'`
  - `"md"` format: backticks `` `vpc` ``
- **Success messages**: Prefix with `✓` (U+2713)
- **Warnings**: Prefix with `Warning:` to stderr
- **Dry-run**: Prefix with `[DRY-RUN]`

### File Handling

1. **Preserve permissions**: Use `fileInfo.Mode()` when writing
2. **Atomic operations**: Not implemented (see Known Limitations in README)
3. **Unicode support**: Full UTF-8 support for module names and sources
4. **Line endings**: Preserve original line endings

---

## Testing Guidelines

### Running Specific Tests

```bash
# Run a specific test file
go test -v -run TestLoadConfig

# Run a specific test function
go test -v -run TestLoadConfig_ValidYAML

# Run tests matching a pattern
go test -v -run "TestUpdate.*"

# Run with race detection (important for concurrency)
go test -v -race -run TestExample
```

### Coverage Goals

- **Current coverage**: ~90%+ (check with `make test-coverage`)
- **Critical paths**: 100% coverage for config parsing, version updating
- **Edge cases**: Extensive tests for unusual HCL formatting, Unicode, errors

### Test Data

- **Location**: `examples/` directory contains reusable test data
- **Creating test files**: Use `hclwrite.Format()` to ensure valid HCL syntax
- **Config files**: YAML examples in `examples/config-*.yml`

### Common Test Patterns

```go
// 1. Testing file updates
func TestUpdateModule(t *testing.T) {
    tmpDir := t.TempDir()
    testFile := filepath.Join(tmpDir, "test.tf")

    // Write initial content
    initial := `module "vpc" { source = "aws/vpc" version = "1.0.0" }`
    os.WriteFile(testFile, []byte(initial), 0644)

    // Run update
    err := processFile(testFile, ...)
    if err != nil {
        t.Fatalf("processFile failed: %v", err)
    }

    // Read and verify result
    result, _ := os.ReadFile(testFile)
    if !strings.Contains(string(result), `version = "2.0.0"`) {
        t.Errorf("Version not updated correctly")
    }
}

// 2. Testing config loading
func TestConfigLoad(t *testing.T) {
    tmpDir := t.TempDir()
    configFile := filepath.Join(tmpDir, "config.yml")

    yamlContent := `
modules:
  - source: "aws/vpc"
    version: "2.0.0"
`
    os.WriteFile(configFile, []byte(yamlContent), 0644)

    config, err := loadConfig(configFile)
    if err != nil {
        t.Fatalf("loadConfig failed: %v", err)
    }

    if len(config.Modules) != 1 {
        t.Errorf("Expected 1 module, got %d", len(config.Modules))
    }
}

// 3. Testing error conditions
func TestInvalidInput(t *testing.T) {
    _, err := loadConfig("nonexistent.yml")
    if err == nil {
        t.Error("Expected error for nonexistent file")
    }
}
```

---

## Common Operations

### Making Code Changes

When modifying the codebase:

1. **Read existing code first**: Always use Read tool before suggesting changes
2. **Understand the context**: Check related test files
3. **Maintain compatibility**: Don't break existing CLI flags or config format
4. **Update tests**: Add/modify tests for any functional changes
5. **Run tests locally**: `make test-coverage` before committing
6. **Check linting**: Run `golangci-lint run`

### Adding New Features

**Typical workflow**:

1. **Design phase**:
   - Review existing patterns in `main.go` and `config.go`
   - Check if similar features exist (e.g., module filtering)
   - Update `config-schema.json` if adding config fields

2. **Implementation**:
   - Add CLI flag in `parseFlags()` if needed
   - Add config field to `Config` struct if needed
   - Implement core logic following existing patterns
   - Use `hclwrite` API for HCL manipulation

3. **Testing**:
   - Create test file: `<feature>_test.go`
   - Add unit tests for new functions
   - Add integration tests for end-to-end workflows
   - Test edge cases and error conditions

4. **Documentation**:
   - Update README.md with usage examples
   - Add example config files to `examples/`
   - Update this CLAUDE.md if architectural changes

### Debugging

**Common debugging techniques**:

```bash
# Print verbose test output
go test -v -run TestExample

# See full error details
go test -v -run TestExample 2>&1 | less

# Print coverage by function
make test-coverage

# Check which tests cover a specific line
go test -coverprofile=coverage.out
go tool cover -html=coverage.out
```

**Adding debug output** (for development only, remove before commit):

```go
// Temporary debugging
fmt.Fprintf(os.Stderr, "DEBUG: variable = %+v\n", variable)
```

### Git Workflow

```bash
# Development branch naming
git checkout -b feature/description
git checkout -b fix/issue-number

# Commit messages
git commit -m "feat: add new filter option"
git commit -m "fix: handle empty version attributes"
git commit -m "test: add coverage for edge cases"
git commit -m "docs: update README with new examples"

# Before pushing
make test-coverage
golangci-lint run
```

---

## Important Files Reference

### Core Source Files

| File | Purpose | Key Functions |
|------|---------|---------------|
| `main.go` | Main entry point, CLI logic, HCL processing | `main()`, `processFile()`, `updateModuleVersion()`, `updateTerraformVersion()`, `updateProviderVersion()` |
| `config.go` | YAML config loading and validation | `loadConfig()`, `UnmarshalYAML()` for `FromVersions` |

### Test Files (Selected)

| File | Coverage |
|------|----------|
| `main_test.go` | Core functionality tests |
| `config_test.go` | Config loading and validation |
| `config_schema_test.go` | JSON Schema validation |
| `integration_config_test.go` | End-to-end config workflows |
| `chaos_test.go` | Error condition testing |
| `coverage_test.go` | Edge case coverage |
| `validation_test.go` | Input validation tests |

### Configuration Files

| File | Purpose |
|------|---------|
| `.github/workflows/ci.yml` | Main CI pipeline (test + build) |
| `.github/workflows/lint.yml` | Linting checks |
| `.github/workflows/release.yml` | Release automation with GoReleaser |
| `.goreleaser.yaml` | Multi-platform build config |
| `.golangci.yml` | Linter configuration |
| `Makefile` | Development commands |
| `schema/config-schema.json` | JSON Schema for YAML validation |

### Documentation

| File | Audience |
|------|----------|
| `README.md` | End users (comprehensive CLI documentation) |
| `AGENTS.md` | AI agents (detailed technical guide) |
| `CLAUDE.md` | AI assistants (this file - development guide) |
| `docs/RELEASING.md` | Maintainers (release process) |
| `.github/copilot-instructions.md` | AI coding assistants (build/test instructions) |

### Example Files

All files in `examples/` directory:

- **Terraform files**: Various complexity levels (simple, complex, heavily commented, unusual formatting)
- **Config files**: YAML examples for different use cases
  - `config-basic.yml`: Simple module updates
  - `config-advanced.yml`: Advanced features (subpaths, Git sources)
  - `config-production.yml`: Real-world AWS modules
  - `config-with-ignore.yml`: Ignore patterns demonstration
  - `config-terraform-providers.yml`: Terraform and provider version updates

---

## Key Architectural Decisions

### Why Single Package?

- **Simplicity**: CLI tool doesn't need complex package hierarchy
- **Fast compilation**: No package dependency graph
- **Easy testing**: All code visible to tests in same package

### Why HCL Write API?

- **Official library**: HashiCorp's own implementation
- **Format preservation**: Maintains formatting and comments
- **Safety**: Prevents syntax errors
- **Limitation**: Can only modify attributes, not restructure blocks

### Why YAML for Config?

- **Human-readable**: Easy to write and review
- **Schema validation**: JSON Schema provides IDE support and validation
- **Flexible types**: Supports both string and array for `from` field
- **Widespread**: Well-known format in DevOps ecosystem

### Why No Database/State?

- **Stateless design**: Each run is independent
- **Git-based workflow**: Changes tracked in version control
- **Simplicity**: No persistence layer needed for file transformations

### Known Limitations (from README)

1. **No file locking**: Don't run multiple instances on same files
2. **No atomic writes**: File corruption possible on system crash (rare)
3. **Memory-based**: Large files (>100MB) may cause issues (unlikely in practice)
4. **Local modules**: Intentionally skipped (no version attribute in Terraform)

---

## Version Constraint Support

The tool fully supports Terraform version constraint syntax:

- **Exact versions**: `"1.0.0"`, `"5.2.1"`
- **SemVer constraints**: `"~> 3.0"`, `">= 4.0"`, `"< 2.0"`
- **Chained constraints**: `">= 1.5, < 2.0"`
- **Pre-release versions**: `"1.0.0-beta"`, `"2.0.0-rc.1"`
- **Build metadata**: `"1.0.0+build.123"`

**JSON Schema** (`schema/config-schema.json`) enforces valid version constraint format with regex pattern.

---

## Working with AI Assistants

### Best Practices for AI Agents

When working with this codebase:

1. **Always read files before modifying**: Use Read tool first
2. **Understand the test suite**: Check `*_test.go` files to understand expected behavior
3. **Preserve existing patterns**: Follow established code conventions
4. **Test thoroughly**: Run `make test-coverage` after changes
5. **Update documentation**: Keep README.md, CLAUDE.md, and AGENTS.md in sync
6. **Validate changes**: Use dry-run mode to preview tool behavior

### Common Pitfalls to Avoid

1. **Breaking HCL format**: Always use `hclwrite` API, never string manipulation
2. **Ignoring test failures**: Fix tests or update them if behavior changed intentionally
3. **Incomplete error handling**: Match existing error handling patterns
4. **Changing public API**: CLI flags and config format are user-facing contracts
5. **Skipping linting**: CI will fail if linter finds issues

### Helpful Commands for AI Agents

```bash
# Quick validation workflow
make test && golangci-lint run

# Check if change affected coverage
make test-coverage

# Verify tool still works end-to-end
./tf-version-bump -pattern "examples/*.tf" -config examples/config-basic.yml -dry-run

# Check JSON Schema validity
# (Requires jsonschema npm package or online validator)
```

---

## Additional Resources

- **Official HCL docs**: https://github.com/hashicorp/hcl
- **Terraform version constraints**: https://developer.hashicorp.com/terraform/language/expressions/version-constraints
- **GoReleaser docs**: https://goreleaser.com/
- **golangci-lint**: https://golangci-lint.run/

---

## Changelog and Updates

**Last updated**: 2025-12-17

This document should be updated when:
- Major architectural changes are made
- New features are added that change workflows
- Testing patterns evolve
- Build/release process changes

For detailed code changes, see git history and GitHub releases.
