.PHONY: help test test-verbose test-coverage coverage-html coverage-func clean build install docs-check branch-automation-test test-github-actions actionlint shellcheck

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
	@echo "  docs-check     - Check documentation, schema, configs, and runnable examples"
	@echo "  branch-automation-test - Alias for test-github-actions"
	@echo "  test-github-actions - Run the GitHub Actions example harness"
	@echo "  actionlint     - Lint this repository's and the example's GitHub Actions workflows"
	@echo "  shellcheck     - Lint every tracked shell script"

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

# Check user documentation, its local links, the config schema, and maintained examples.
docs-check:
	go test -count=1 -v -run 'Test(ConfigSchema|Documentation|ExampleConfigs)' ./...

# Alias of test-github-actions.
branch-automation-test: test-github-actions

# The primary example harness uses Docker only as local Terraform test infrastructure.
test-github-actions:
	TEST_GIT="$(TEST_GIT)" examples/github-actions/test.sh

# Lint this repository's workflows, then the example's, both with the pinned launcher. The example's
# callers use ./.github/workflows/tf-version-bump-reusable.yml, which resolves only from a
# repository root, so its workflow tree is linted from a temporary repository copy.
actionlint:
	scripts/run-actionlint.sh
	@set -e; \
	temporary_directory=$$(mktemp -d); \
	trap 'rm -rf "$$temporary_directory"' EXIT; \
	cp -R examples/github-actions/.github "$$temporary_directory/.github"; \
	"$(TEST_GIT)" -C "$$temporary_directory" init --quiet; \
	cd "$$temporary_directory"; \
	"$(CURDIR)/scripts/run-actionlint.sh" .github/workflows/*.yml

# Lint every tracked shell script. CI pins the shellcheck version; see .github/workflows/lint.yml.
shellcheck:
	scripts/run-shellcheck.sh
