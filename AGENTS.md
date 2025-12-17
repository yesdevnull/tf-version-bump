# AGENTS.md - Quick Start Guide for AI Agents

This is a quick reference guide for AI agents working with tf-version-bump. For comprehensive development documentation, see [CLAUDE.md](CLAUDE.md).

## What is tf-version-bump?

A CLI tool written in Go that updates Terraform module versions, Terraform required_version, and provider versions across multiple files using glob patterns.

**Key tech**: Go 1.24+, HashiCorp HCL library, YAML configs

## Quick Setup

```bash
# 1. Install dependencies
go mod download && go mod verify

# 2. Build
go build -o tf-version-bump

# 3. Test
go test -v -race -coverprofile=coverage.out -covermode=atomic ./...

# 4. Lint
golangci-lint run --timeout=5m
```

## Project Structure

```
tf-version-bump/
├── main.go                  # Core logic
├── config.go                # YAML config handling
├── *_test.go                # Comprehensive tests (14 test files)
├── schema/config-schema.json # YAML validation schema
├── examples/                # Sample configs and .tf files
├── .github/workflows/       # CI/CD pipelines
└── docs/                    # Additional documentation
```

## Core Files

| File | Purpose |
|------|---------|
| `main.go` | CLI parsing, HCL processing, version updates |
| `config.go` | YAML config loading and validation |
| `main_test.go` | Core functionality tests |
| `config_test.go` | Config validation tests |
| `schema/config-schema.json` | JSON Schema for YAML |

## Essential Commands

### Building
```bash
go build -o tf-version-bump                              # Local build
GOOS=linux GOARCH=amd64 go build -o tf-version-bump     # Cross-compile
```

### Testing
```bash
go test -v ./...                                          # Quick test
go test -v -race -coverprofile=coverage.out -covermode=atomic ./...  # Full CI test
make test-coverage                                        # With coverage report
```

### Linting
```bash
golangci-lint run --timeout=5m                           # Run linter
golangci-lint run --fix                                   # Auto-fix issues
```

### Running the Tool
```bash
# Single module update
./tf-version-bump -pattern "*.tf" -module "terraform-aws-modules/vpc/aws" -to "5.0.0"

# Batch update with config
./tf-version-bump -pattern "**/*.tf" -config examples/config-basic.yml

# Dry run
./tf-version-bump -pattern "*.tf" -module "..." -to "..." -dry-run

# Update Terraform version
./tf-version-bump -pattern "*.tf" -terraform-version ">= 1.5"

# Update provider version
./tf-version-bump -pattern "*.tf" -provider aws -to "~> 5.0"
```

## Key Architecture Patterns

### HCL Processing (main.go)
```go
// 1. Read file
src, err := os.ReadFile(filename)

// 2. Parse HCL
file, diags := hclwrite.ParseConfig(src, filename, hcl.Pos{Line: 1, Column: 1})

// 3. Navigate and modify
for _, block := range file.Body().Blocks() {
    if block.Type() == "module" {
        block.Body().SetAttributeValue("version", cty.StringVal(targetVersion))
    }
}

// 4. Write back
os.WriteFile(filename, file.Bytes(), fileInfo.Mode())
```

### Config Loading (config.go)
```go
// Strict YAML parsing with validation
config, err := loadConfig(filename)
// Custom FromVersions type accepts string or []string
// Whitespace trimming and validation
```

## Code Conventions

- **Package**: Single `main` package (flat structure)
- **Style**: Standard Go formatting (`go fmt`)
- **Errors**: Wrapped with `fmt.Errorf("context: %w", err)`
- **Tests**: Table-driven tests with `t.Run()` subtests
- **Temp files**: Always use `t.TempDir()` in tests

## Testing Patterns

```go
// Standard test structure
func TestExample(t *testing.T) {
    tmpDir := t.TempDir()
    testFile := filepath.Join(tmpDir, "test.tf")

    // Setup
    os.WriteFile(testFile, []byte(initial), 0644)

    // Execute
    err := processFile(testFile, ...)

    // Assert
    if err != nil {
        t.Fatalf("processFile failed: %v", err)
    }

    // Verify
    result, _ := os.ReadFile(testFile)
    if !strings.Contains(string(result), expected) {
        t.Errorf("Expected %s, got %s", expected, result)
    }
}
```

## Common Operations

### Adding a Feature
1. Update `main.go` or `config.go`
2. Add CLI flag in `parseFlags()` if needed
3. Add config field to `Config` struct if needed
4. Update `schema/config-schema.json` for YAML validation
5. Add tests in `*_test.go`
6. Update `README.md` with usage examples
7. Run full validation: `make test-coverage && golangci-lint run`

### Debugging
```bash
go test -v -run TestSpecific                  # Run specific test
go test -coverprofile=coverage.out           # Generate coverage
go tool cover -html=coverage.out             # View coverage HTML
make test-coverage                            # Coverage with report
```

## CI/CD

GitHub Actions runs on every push/PR:
- **Test**: Go 1.24 & 1.25, race detection, coverage upload
- **Build**: 6 platforms (Linux/macOS/Windows × amd64/arm64)
- **Lint**: golangci-lint with 11 enabled linters

## Version Filtering Logic

Priority order in `updateModuleVersion()`:
1. **Ignore patterns**: If module name matches `ignore_modules`, skip
2. **Ignore versions**: If current version in `ignore_versions`, skip (takes precedence)
3. **From filter**: If `from` is set and current version doesn't match, skip
4. **Update**: If all checks pass, update the version

## Quick Tips

✓ **Always read files before modifying** - Use Read tool first
✓ **Run tests with race detector** - `go test -race`
✓ **Use dry-run** - Preview changes before applying
✓ **Check coverage** - `make test-coverage`
✓ **Follow existing patterns** - Match the codebase style
✓ **Update docs** - Keep README.md and CLAUDE.md in sync

✗ **Never break HCL format** - Use `hclwrite` API only
✗ **Don't skip linting** - CI will fail
✗ **Don't change public API** - CLI flags are user-facing
✗ **Don't commit binaries** - They're gitignored

## Dependencies

```
github.com/hashicorp/hcl/v2  v2.24.0  # Official HCL parser
github.com/zclconf/go-cty    v1.17.0  # HCL type system
gopkg.in/yaml.v3             v3.0.1   # YAML parsing
```

## Resources

- **Comprehensive guide**: [CLAUDE.md](CLAUDE.md)
- **User documentation**: [README.md](README.md)
- **Release process**: [docs/RELEASING.md](docs/RELEASING.md)
- **Examples**: `examples/` directory
- **HCL docs**: https://github.com/hashicorp/hcl
- **Version constraints**: https://developer.hashicorp.com/terraform/language/expressions/version-constraints

---

**Need more details?** See [CLAUDE.md](CLAUDE.md) for comprehensive development documentation.
