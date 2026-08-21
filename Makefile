.PHONY: help test test-verbose test-coverage coverage-html coverage-func clean build install docs-check branch-automation-test test-github-actions actionlint

TEST_GIT ?= git

# Default target
help:
	@echo "Available targets:"
	@echo "  test           - Run all tests"
	@echo "  test-verbose   - Run tests with verbose output"
	@echo "  test-coverage  - Run tests with coverage report"
	@echo "  coverage-html  - Generate HTML coverage report"
	@echo "  coverage-func  - Show function-level coverage"
	@echo "  clean          - Clean build artifacts and coverage files"
	@echo "  build          - Build the binary"
	@echo "  install        - Install the binary"
	@echo "  docs-check     - Check documentation links, schema, and example configs"
	@echo "  branch-automation-test - Alias for test-github-actions"
	@echo "  test-github-actions - Run GitHub Actions example checks (harness + actionlint)"
	@echo "  actionlint     - Lint GitHub Actions workflows"

# Run tests
test:
	go test -v ./...

# Run tests with verbose output and race detection
test-verbose:
	go test -v -race ./...

# Run tests with coverage
test-coverage:
	go test -v -race -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -func=coverage.out

# Generate HTML coverage report
coverage-html: test-coverage
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Show function-level coverage
coverage-func:
	@if [ ! -f coverage.out ]; then \
		echo "No coverage file found. Run 'make test-coverage' first."; \
		exit 1; \
	fi
	go tool cover -func=coverage.out

# Clean build artifacts and coverage files
clean:
	rm -f coverage.out coverage.html
	rm -f tf-version-bump
	go clean

# Build the binary
build:
	go build -v -o tf-version-bump .

# Install the binary
install:
	go install -v .

# Check user documentation, its local links, the config schema, and maintained YAML examples.
docs-check:
	go test -v -run 'Test(ConfigSchema|Documentation|ExampleConfigs)' ./...

# Alias kept for muscle memory: identical to test-github-actions, which also runs actionlint.
branch-automation-test: test-github-actions

# The primary example harness uses Docker only as local Terraform test infrastructure, then lints the copied workflow tree.
test-github-actions:
	TEST_GIT="$(TEST_GIT)" examples/github-actions/test.sh
	@temporary_directory=$$(mktemp -d); \
	trap 'rm -rf "$$temporary_directory"' EXIT; \
	cp -R examples/github-actions/.github "$$temporary_directory/.github"; \
	"$(TEST_GIT)" -C "$$temporary_directory" init --quiet; \
	cd "$$temporary_directory"; \
	"$(CURDIR)/scripts/run-actionlint.sh" .github/workflows/*.yml

# Lint GitHub Actions workflows with the pinned launcher
actionlint:
	scripts/run-actionlint.sh
