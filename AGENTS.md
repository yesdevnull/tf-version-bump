# AI Agents Guide for tf-version-bump

This document provides comprehensive information for AI agents and developers working with the **tf-version-bump** codebase. It covers the project structure, build processes, code conventions, and architectural patterns.

## 1. Project Purpose and Overview

**tf-version-bump** is a command-line tool written in Go that automates updating Terraform module versions across multiple files. It uses glob patterns to locate files and intelligently updates module version attributes while preserving formatting and comments.

### Key Capabilities

- Parse and modify Terraform HCL files using the official HashiCorp HCL library
- Update module versions by matching module source attributes
- Process multiple files and modules in a single operation
- Support batch updates via YAML configuration files
- Preserve all formatting, comments, and HCL structure
- Handle complex scenarios: subpaths in sources, various module types, conditional updates
- Safe error handling with warnings for unsupported modules (local modules, modules without versions)

### Supported Module Types

- **Registry modules**: `terraform-aws-modules/vpc/aws`, `terraform-aws-modules/s3-bucket/aws`, etc.
- **Modules with subpaths**: `terraform-aws-modules/iam/aws//modules/iam-user`
- **Git-based modules**: `git::https://github.com/example/terraform-module.git`
- **Local modules**: Detected and skipped with warnings (by design)
- **Selective updates**: Ignore specific modules by name or pattern using wildcard matching

### License

MIT License (Copyright 2025 yesdevnull)

---

## 2. Directory Structure and Key Files

```
tf-version-bump/
├── .github/
│   ├── workflows/
│   │   ├── ci.yml              # Main CI pipeline (test, build)
│   │   ├── lint.yml            # Linting workflow with golangci-lint
│   │   └── codeql.yml          # Security analysis with CodeQL
│   ├── dependabot.yml          # Automated dependency updates (Go modules, Actions)
│   └── copilot-instructions.md # Detailed build/test instructions
│
├── examples/
│   ├── config-basic.yml        # Basic batch config example
│   ├── config-advanced.yml     # Advanced config with various module types
│   ├── config-production.yml   # Production-ready config with AWS modules
│   ├── main.tf                 # Simple Terraform file example
│   ├── complex.tf              # Complex file with comments and varied formatting
│   ├── modules.tf              # File with multiple modules
│   ├── heavily_commented.tf    # File to test comment preservation
│   └── unusual_formatting.tf   # File with edge-case formatting
│
├── .gitignore                  # Standard Go project ignores
├── .golangci.yml               # Linter configuration
├── go.mod                       # Go module definition (Go 1.24+)
├── go.sum                       # Dependency checksums
├── LICENSE                      # MIT License
├── README.md                    # User-facing documentation
├── AGENTS.md                    # This file - AI agent guide
│
├── main.go                      # Main application code
├── main_test.go                 # Main tests
├── config_test.go               # Configuration tests
│
└── dist/                        # Build artifacts (gitignored)
```

### Critical Files Summary

| File | Purpose | Key Content |
|------|---------|-------------|
| `main.go` | Core application logic | CLI parsing, module version updating, HCL manipulation |
| `main_test.go` | Unit/integration tests | Tests for updateModuleVersion, edge cases, formatting |
| `config_test.go` | Configuration tests | Tests for YAML loading, validation, error cases |
| `.golangci.yml` | Linter configuration | 11 enabled linters, complexity threshold 15 |
| `.github/workflows/ci.yml` | Test and build pipeline | Tests on Go 1.24 and 1.25, builds for 6 platforms |
| `.github/workflows/lint.yml` | Code quality | golangci-lint v2.5.0 |
| `examples/` | Reference configurations | Real-world usage patterns and edge cases |

---

## 3. Programming Language and Frameworks

### Go Version

- **Required**: Go 1.24 or later
- **Specified in**: `go.mod` with `go 1.24`
- **CI/CD tests on**: Go 1.24 and 1.25

### Key Dependencies

| Package | Version | Purpose |
|---------|---------|---------|
| `github.com/hashicorp/hcl/v2` | v2.24.0 | Official HCL parser and writer for safe Terraform file manipulation |
| `github.com/zclconf/go-cty` | v1.17.0 | Configuration type system for HCL |
| `gopkg.in/yaml.v3` | v3.0.1 | YAML parsing for batch configuration files |

### Standard Library Usage

- `flag` - Command-line argument parsing
- `os` - File I/O operations
- `filepath` - Glob pattern matching and file path handling
- `strings` - String manipulation and parsing
- `fmt` - Formatted output
- `log` - Error and status logging

---

## 4. Build, Test, and Lint Commands

### Prerequisites

Before any operation, download and verify dependencies:

```bash
go mod download
go mod verify
```

These commands:
- Download dependencies to `~/go/pkg/mod/`
- Verify integrity against `go.sum`
- Take ~5-10 seconds on first run
- Are cached for subsequent runs

### Building

**Standard build** (creates binary in current directory):

```bash
go build -o tf-version-bump
```

**Cross-platform builds** (as used in CI/CD):

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

**Clean build**:

```bash
go clean
go build -o tf-version-bump
```

### Testing

**Standard test run**:

```bash
go test -v ./...
```

**Full test run with race detector and coverage** (REQUIRED for CI):

```bash
go test -v -race -coverprofile=coverage.out -covermode=atomic ./...
```

- Takes ~5-10 seconds
- Enables race condition detection
- Generates `coverage.out` file (gitignored)
- Current coverage: ~50% of statements

**Quick test without verbosity**:

```bash
go test ./...
```

### Linting

**Install golangci-lint** (one-time setup):

```bash
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin v2.5.0
```

**Run linter**:

```bash
golangci-lint run --timeout=5m
```

Or if `~/go/bin` is in PATH:

```bash
$(go env GOPATH)/bin/golangci-lint run --timeout=5m
```

**Linter Configuration** (`.golangci.yml`):

- **Enabled linters**: errcheck, govet, ineffassign, staticcheck, unused, gocritic, gocyclo, misspell, unconvert, unparam, whitespace
- **Cyclomatic complexity threshold**: 15
- **Test linting**: Enabled (run.tests: true)
- **Issue limit**: No maximum (issues.max-same-issues: 0)
- **Timeout**: 5 minutes

### Complete Validation Sequence

Run this before committing changes:

```bash
go mod download && go mod verify
go test -v -race -coverprofile=coverage.out -covermode=atomic ./...
golangci-lint run --timeout=5m
go build -o tf-version-bump
```

---

## 5. Code Style and Conventions

### Go Conventions

The codebase follows standard Go conventions:

- **Package documentation**: All packages have documentation comments
- **Function documentation**: All public functions documented with purpose, parameters, returns, and examples
- **Error handling**: Explicit error handling with `if err != nil` pattern
- **Naming**: Clear, descriptive names for variables and functions
- **Comments**: Inline comments for complex logic, block comments for sections

### Code Organization

#### main.go Structure

```go
package main

// ModuleUpdate struct - represents a single module update
type ModuleUpdate struct {
    Source  string   // Module source (YAML: "source")
    Version string   // Target version (YAML: "version")
    From    string   // Optional: current version filter (YAML: "from")
    Ignore  []string // Optional: module names/patterns to ignore (YAML: "ignore")
}

// Config struct - represents YAML configuration file structure
type Config struct {
    Modules []ModuleUpdate
}

// Main Functions:
// - main()                  - CLI argument parsing, orchestration
// - loadConfig()            - Load and validate YAML configuration
// - updateModuleVersion()   - Core: parse HCL, update versions, write file
// - shouldIgnoreModule()    - Check if module name matches ignore patterns
// - matchPattern()          - Wildcard pattern matching (supports *)
// - isLocalModule()         - Detect local vs remote modules
// - trimQuotes()            - Remove surrounding quotes from strings
```

#### Naming Conventions

- **Variables**: camelCase for local variables
- **Functions**: PascalCase for exported, lowercase for unexported
- **Constants**: UPPER_CASE for constants (none in this project)
- **Structs**: PascalCase

#### HCL Processing Pattern

```go
// Pattern used in updateModuleVersion():
1. Read file with os.ReadFile()
2. Parse with hclwrite.ParseConfig()
3. Iterate through file.Body().Blocks()
4. Find "module" blocks
5. Get "source" attribute and compare
6. Update or add "version" attribute with SetAttributeValue()
7. Format with hclwrite.Format()
8. Write back with os.WriteFile()
```

### Testing Conventions

**Table-driven tests** (standard Go pattern):

```go
tests := []struct {
    name string
    input string
    expected string
    // ... other fields
}{
    {
        name: "test case 1",
        input: "...",
        expected: "...",
    },
    // ... more test cases
}

for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
        // test logic using tt.field
    })
}
```

**Test Patterns**:
- Unit tests for helper functions (trimQuotes)
- Integration tests for main functionality (updateModuleVersion)
- Table-driven tests for multiple scenarios
- Subtests with `t.Run()` for organization
- Temporary files with `t.TempDir()` and `os.CreateTemp()`
- Assertions by checking return values and file content

---

## 6. Key Architectural Patterns

### CLI Design Pattern

The tool uses Go's built-in `flag` package for argument parsing with two operation modes:

**Mode 1: Single Module Update** (command-line flags)

```bash
tf-version-bump \
    -pattern "*.tf" \
    -module "terraform-aws-modules/vpc/aws" \
    -to "5.0.0" \
    [-from "4.0.0"] \
    [-ignore "legacy-*,test-*"] \
    [-force-add]
```

**Mode 2: Batch Configuration** (YAML file)

```bash
tf-version-bump \
    -pattern "**/*.tf" \
    -config "updates.yml" \
    [-force-add]
```

### File Processing Pipeline

```
Input (Glob Pattern)
    ↓
filepath.Glob() → Files matching pattern
    ↓
For each file:
    ↓
    os.ReadFile() → Read content
    ↓
    hclwrite.ParseConfig() → Parse HCL structure
    ↓
    Iterate blocks, find "module" blocks
    ↓
    Match module source
    ↓
    Update/Add version attribute
    ↓
    hclwrite.Format() → Format output
    ↓
    os.WriteFile() → Write back to disk
    ↓
Output: Updated file, Update count, Warnings
```

### Module Matching Strategy

1. **Module name extraction**: Get module name from block labels
2. **Ignore pattern matching**: Skip if module name matches any ignore pattern (exact or wildcard)
3. **Source extraction**: Get source attribute from module block
4. **Quote trimming**: Remove surrounding quotes
5. **Local module detection**: Skip if source starts with `./`, `../`, or `/`
6. **Version filtering**: If `-from` specified, only update if current version matches
7. **Version attribute handling**:
   - If exists: update value
   - If missing: warn (or add with `-force-add`)

### Error Handling Approach

- **File errors**: Return with wrapped error (using `fmt.Errorf`)
- **Parse errors**: Check HCL diagnostics, return with human-readable message
- **Warnings**: Print to stderr for non-fatal issues (local modules, missing versions)
- **Validation**: Config file validation happens at load time

### Data Flow

```
CLI Arguments → Determine Mode → Load Config/Build Updates → 
    Find Files → Process Each File → Update Version Attributes → 
    Write Back → Report Results
```

---

## 7. Important Configuration Files

### .golangci.yml

Linter configuration with 11 enabled linters:

```yaml
version: "2"
run:
  timeout: 5m
  tests: true
linters:
  enable:
    - errcheck           # Unchecked errors
    - govet              # Go vet
    - ineffassign        # Unused assignments
    - staticcheck        # Static analysis
    - unused             # Unused code
    - gocritic           # Critical Go style issues
    - gocyclo            # Cyclomatic complexity
    - misspell           # Spelling errors
    - unconvert          # Unnecessary type conversions
    - unparam            # Unused parameters
    - whitespace         # Whitespace issues
linters-settings:
  gocyclo:
    min-complexity: 15
  gocritic:
    enabled-tags:
      - diagnostic
      - style
      - performance
issues:
  max-same-issues: 0
```

### .github/workflows/ci.yml

CI/CD pipeline that:
- Runs on push to main and pull requests
- Tests on Go 1.24 and 1.25 (matrix strategy)
- Caches Go modules
- Downloads and verifies dependencies
- Runs tests with race detector and coverage
- Uploads coverage to Codecov
- Builds for 6 platforms (Linux, macOS, Windows × amd64, arm64)
- Uploads build artifacts

### .github/workflows/lint.yml

Linting workflow:
- Triggers only on Go file changes and dependency files
- Installs golangci-lint v2.5.0
- Uses SHA-pinned GitHub Actions for security
- Runs with 5-minute timeout

### .github/workflows/codeql.yml

Security analysis:
- CodeQL workflow for Go security scanning
- Runs on pushes and pull requests
- Tests Go files, go.mod, and go.sum changes
- Uses pinned versions of GitHub Actions

### .github/dependabot.yml

Automated dependency management:
- **Go modules**: Weekly updates (Mondays)
- **GitHub Actions**: Weekly updates (Mondays)
- Labels PRs with "dependencies" and specific type labels
- Limits to 5 open PRs per ecosystem
- Prefixes commits with "deps" or "ci"

### .gitignore

Standard Go project ignores:

```
*.exe, *.dll, *.so, *.dylib    # Compiled binaries
*.test                          # Test binaries
*.out                           # Coverage output
go.work                         # Go workspace
tf-version-bump, tf-version-bump-* # Binaries (this tool)
.idea/, .vscode/                # IDE files
*.swp, *.swo, *~                # Editor temp files
.DS_Store, Thumbs.db            # OS files
```

---

## 8. Existing Documentation

### README.md

Comprehensive user-facing documentation covering:

- **Installation**: go install method and build from source
- **Usage**: Single module mode and batch config mode with examples
- **How it works**: Step-by-step explanation of processing pipeline
- **Examples**: Real-world use cases (VPC modules, subpaths, Git sources)
- **Local modules**: Explanation of why they're skipped
- **Version attributes**: How modules without versions are handled
- **Force-add flag**: How to add version attributes
- **Before/after examples**: Visual comparison of file changes
- **Testing**: Test command for verification

### copilot-instructions.md

Detailed instructions for developers covering:

- **Repository overview**: Size, language, LOC, dependencies
- **Project structure**: File organization and key files
- **Build instructions**: Prerequisites, dependency management, cross-platform builds
- **Testing**: Various test commands with timing
- **Linting**: Installation and execution
- **CI/CD pipeline**: Three jobs (test, build, lint) with details
- **Development workflow**: Making changes, testing, common gotchas
- **Architecture notes**: How the tool works, code organization
- **Quick reference**: Common command sequences

### Example Configuration Files

**config-basic.yml**: Simple batch configuration with 3 modules
**config-advanced.yml**: Advanced examples including:
- Registry modules
- Modules with subpaths
- Local modules (for reference)
- Git source modules

**config-production.yml**: Production-ready config with 10 AWS modules
**config-with-ignore.yml**: Examples demonstrating the ignore feature with various wildcard patterns

### Example Terraform Files

**main.tf**: Basic example with VPC and security group modules
**complex.tf**: Complex file with comments and inline comments to test preservation
**modules.tf**: Multiple module blocks for testing
**heavily_commented.tf**: File to verify comment preservation
**unusual_formatting.tf**: Edge cases in formatting

---

## 9. Development Workflow for AI Agents

### Understanding the Codebase

When approaching this project:

1. **Start with main.go**: Understand the CLI interface and orchestration
2. **Review ModuleUpdate and Config structs**: Understand data structures
3. **Study updateModuleVersion()**: Core logic for HCL parsing and modification
4. **Examine tests**: See how the tool handles various scenarios

### Common Tasks

#### Adding a New Command-Line Flag

1. Add flag in `main()` with `flag.String()`, `flag.Bool()`, etc.
2. Parse the flag
3. Validate mutually exclusive flags
4. Use the flag value in processing

#### Modifying Module Version Update Logic

1. The core logic is in `updateModuleVersion()`
2. Key decision points:
   - Module source matching (line 229)
   - Version attribute checking (line 231)
   - Force-add logic (lines 232-243)
   - Version filtering with `-from` (lines 244-254)
3. Update relevant test cases in `main_test.go`

#### Adding Tests

1. Use table-driven test structure
2. Create temporary files for file operations
3. Check both return values and file content
4. Clean up resources (t.TempDir() handles this automatically)

### Testing Your Changes

Before submitting changes:

```bash
# 1. Download and verify dependencies
go mod download && go mod verify

# 2. Run tests with full validation
go test -v -race -coverprofile=coverage.out -covermode=atomic ./...

# 3. Run linter
golangci-lint run --timeout=5m

# 4. Build
go build -o tf-version-bump

# 5. Manual testing (optional)
./tf-version-bump -pattern "examples/*.tf" -module "terraform-aws-modules/vpc/aws" -to "5.0.0"
git checkout examples/  # Restore example files
```

### Important Constraints

- **Go version**: Code must work with Go 1.24 and 1.25
- **Race detector**: All code must pass race detector (`-race` flag)
- **Linting**: All code must pass golangci-lint with current config
- **Backwards compatibility**: Consider impact on existing users
- **Dependencies**: Keep dependency list minimal (currently only 2 external packages)

---

## 10. Code Patterns Reference

### HCL Parsing Pattern

```go
// Read file
src, err := os.ReadFile(filename)
if err != nil {
    return false, fmt.Errorf("failed to read file: %w", err)
}

// Parse HCL
file, diags := hclwrite.ParseConfig(src, filename, hcl.Pos{Line: 1, Column: 1})
if diags.HasErrors() {
    return false, fmt.Errorf("failed to parse HCL: %s", diags.Error())
}

// Iterate blocks
for _, block := range file.Body().Blocks() {
    if block.Type() == "module" {
        // Process module block
    }
}

// Write back
output := hclwrite.Format(file.Bytes())
if err := os.WriteFile(filename, output, 0644); err != nil {
    return false, fmt.Errorf("failed to write file: %w", err)
}
```

### String Value Extraction

```go
// Extract attribute value and remove quotes
attr := block.Body().GetAttribute("source")
if attr != nil {
    tokens := attr.Expr().BuildTokens(nil)
    value := string(tokens.Bytes())
    value = trimQuotes(strings.TrimSpace(value))
}
```

### Version Attribute Update/Addition

```go
// SetAttributeValue both updates existing and adds new attributes
block.Body().SetAttributeValue("version", cty.StringVal(newVersion))
```

### File Glob Matching

```go
// Find all files matching pattern
files, err := filepath.Glob(pattern)
if err != nil {
    log.Fatalf("Error matching pattern: %v", err)
}

if len(files) == 0 {
    log.Fatalf("No files matched pattern: %s", pattern)
}
```

---

## 11. Quick Reference

### Essential Commands

```bash
# Full validation (run before committing)
go mod download && go mod verify && \
go test -v -race -coverprofile=coverage.out -covermode=atomic ./... && \
golangci-lint run --timeout=5m && \
go build -o tf-version-bump

# Quick check
go test ./... && go build -o tf-version-bump

# Run linter only
golangci-lint run --timeout=5m

# Build for multiple platforms
for GOOS in linux darwin windows; do
    for GOARCH in amd64 arm64; do
        GOOS=$GOOS GOARCH=$GOARCH go build -o "tf-version-bump-$GOOS-$GOARCH"
    done
done

# Run the tool
./tf-version-bump -pattern "*.tf" -module "terraform-aws-modules/vpc/aws" -to "5.0.0"

# Run with config file
./tf-version-bump -pattern "**/*.tf" -config "updates.yml"

# Force-add versions
./tf-version-bump -pattern "*.tf" -module "terraform-aws-modules/vpc/aws" -to "5.0.0" -force-add

# Ignore specific modules using patterns
./tf-version-bump -pattern "*.tf" -module "terraform-aws-modules/vpc/aws" -to "5.0.0" -ignore "legacy-vpc,test-*"
```

### Ignore Pattern Matching

The tool supports wildcard pattern matching for ignoring specific modules:

- **Exact match**: `"vpc"` matches only module named "vpc"
- **Prefix wildcard**: `"legacy-*"` matches "legacy-vpc", "legacy-network", etc.
- **Suffix wildcard**: `"*-test"` matches "vpc-test", "network-test", etc.
- **Both sides**: `"*-vpc-*"` matches "prod-vpc-test", "staging-vpc-1", etc.
- **Match all**: `"*"` matches all modules

Pattern matching is implemented in the `matchPattern()` function which:
- Handles exact matches when no wildcards present
- Checks prefix/suffix for single wildcard
- Processes multiple wildcards by splitting and matching parts in order
```

### Key Files to Know

| File | Purpose | When to Edit |
|------|---------|--------------|
| main.go | Core logic | Adding features, fixing bugs |
| main_test.go | Function tests | Adding tests, testing new features |
| config_test.go | Config tests | Changing YAML structure |
| .golangci.yml | Linter config | Adjusting code quality thresholds |
| examples/config-*.yml | Reference configs | Updating example patterns |
| README.md | User docs | Updating usage examples |
| .github/workflows/* | CI/CD | Adding test platforms, changing tools |

### Common Error Messages

| Error | Cause | Fix |
|-------|-------|-----|
| "No files matched pattern" | Glob pattern has no matches | Verify glob pattern is correct |
| "failed to parse HCL" | Invalid Terraform syntax in file | Check file for HCL syntax errors |
| "Module has no version attribute" | Module doesn't have version field | Use `-force-add` flag to add it |
| "is a local module and cannot be version-bumped" | Source starts with `./, ../`, or `/` | This is by design; local modules are skipped |
| "missing 'source' field" in config | YAML config incomplete | Add `source:` field to module entry |

---

## 12. Performance Considerations

### Execution Performance

- **Glob matching**: Uses `filepath.Glob()` - fast for reasonable patterns
- **File I/O**: Reads entire file into memory - fine for typical Terraform files (usually < 100KB)
- **HCL parsing**: Fast for typical modules (< 50 blocks)
- **Typical operation**: 100s of files processed in seconds

### Memory Usage

- **Per file**: ~1-2x file size during processing
- **Configuration**: Entire config file loaded into memory (typically < 10KB)
- **No goroutines**: Single-threaded, sequential processing

### Optimization Opportunities (Not Currently Done)

- Could use goroutines for parallel file processing
- Could parse once and process multiple updates
- Could use streaming for very large files

---

## 13. Dependency Management

### Direct Dependencies (in go.mod)

```
github.com/hashicorp/hcl/v2 v2.24.0
github.com/zclconf/go-cty v1.17.0
gopkg.in/yaml.v3 v3.0.1
```

### Indirect Dependencies

Managed automatically by `go mod` - includes:
- HCL-related dependencies (levenshtein, go-textseg)
- YAML parsing support
- Text utilities and Go standard library dependencies

### Dependency Updates

**Automated via Dependabot**:
- Weekly checks for Go module updates (Mondays)
- Weekly checks for GitHub Actions updates (Mondays)
- Creates PRs with prefix "deps" for Go modules
- Creates PRs with prefix "ci" for GitHub Actions
- Limited to 5 open PRs per ecosystem

### Manual Dependency Updates

```bash
# Check for available updates
go list -u -m all

# Update all dependencies
go get -u ./...

# Update a specific dependency
go get -u github.com/hashicorp/hcl/v2

# Verify dependencies
go mod verify

# Clean up unused dependencies
go mod tidy
```

---

## 14. Common Gotchas and Tips

### For AI Agents Making Changes

1. **Always run full validation**: Race detector catches concurrency bugs
2. **Test with actual examples**: Use files in `examples/` for manual testing
3. **Restore examples after testing**: `git checkout examples/`
4. **Check error messages**: The tool provides helpful warnings to stderr
5. **Remember quote handling**: HCL attributes include quotes in token output
6. **Version filtering is exact**: `-from` requires exact version match

### For Development

1. **Binary gitignored**: Don't commit `tf-version-bump` binary
2. **Coverage file gitignored**: `coverage.out` won't be tracked
3. **Go module cache**: Delete with `go clean -modcache` if issues
4. **Linter version matters**: CI uses v2.5.0; match this version locally
5. **Glob patterns**: `**` doesn't work on all systems; use explicit patterns
6. **HCL formatting**: `hclwrite.Format()` may change whitespace (expected)
7. **Error wrapping**: Use `fmt.Errorf()` with `%w` verb for error chains

### Debugging Tips

```bash
# Verbose testing with output
go test -v -run TestFunctionName

# Run single test case
go test -v -run TestFunctionName/specific_test_case

# Generate coverage report
go test -coverprofile=coverage.html && go tool cover -html=coverage.html

# Check what linter would do
golangci-lint run --print-issued-lines=true -v

# Trace file operations with strace (Linux)
strace -e openat ./tf-version-bump -pattern "*.tf" -module "test" -to "1.0"
```

---

## 15. Future Enhancement Possibilities

### Potential Features (Not Currently Implemented)

- **Dry-run mode**: Preview changes without writing
- **Parallel processing**: Use goroutines for multiple files
- **Module discovery**: Find all modules used in codebase
- **Version constraint support**: Update to versions matching constraints (e.g., `>= 5.0.0`)
- **Git integration**: Auto-commit updated files
- **Terraform validation**: Run `terraform validate` after updates
- **Interactive mode**: CLI interface for selecting modules to update
- **Version pinning**: Specify compatible version ranges

### Areas for Potential Improvement

- Currently no support for terraform files that use HCL expressions in version attribute
- Could cache parsed configurations for batch operations
- Could support custom output formats (JSON, etc.)
- Could add configuration file schema validation with JSON Schema

---

## Conclusion

This codebase is a well-structured, focused tool for a specific Terraform automation task. It follows Go conventions, includes comprehensive tests and documentation, and is designed for both direct use and integration into CI/CD pipelines.

For AI agents: Focus on the patterns established in `main.go`, study the test cases in `main_test.go` and `config_test.go`, and always validate changes with the full test suite before considering them complete.

