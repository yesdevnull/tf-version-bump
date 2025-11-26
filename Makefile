.PHONY: help test test-verbose test-coverage coverage-html coverage-func clean build install

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
