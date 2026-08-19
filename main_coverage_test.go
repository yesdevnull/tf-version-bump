package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

type exitCall struct {
	code int
}

func stubExit(t *testing.T) (restore func(), code *int) {
	t.Helper()
	hookMu.Lock()
	original := exitFunc
	exitCode := -1
	exitFunc = func(c int) {
		exitCode = c
		panic(exitCall{code: c})
	}
	return func() {
		exitFunc = original
		hookMu.Unlock()
	}, &exitCode
}

func withFlagArgs(t *testing.T, args []string, fn func()) {
	t.Helper()
	origArgs := os.Args
	origFlagSet := flag.CommandLine
	flag.CommandLine = flag.NewFlagSet(args[0], flag.ContinueOnError)
	flag.CommandLine.SetOutput(io.Discard)
	os.Args = args
	defer func() {
		flag.CommandLine = origFlagSet
		os.Args = origArgs
	}()
	fn()
}

type commandResult struct {
	stdout      string
	diagnostics string
	exitCode    int
}

func runMainCommand(t *testing.T, args []string) commandResult {
	t.Helper()

	restoreExit, code := stubExit(t)
	defer restoreExit()

	var diagnostics bytes.Buffer
	originalLogWriter := log.Writer()
	originalLogFlags := log.Flags()
	originalLogPrefix := log.Prefix()
	log.SetOutput(&diagnostics)
	log.SetFlags(0)
	log.SetPrefix("")
	defer func() {
		log.SetOutput(originalLogWriter)
		log.SetFlags(originalLogFlags)
		log.SetPrefix(originalLogPrefix)
	}()

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stdout pipe: %v", err)
	}
	defer func() {
		if err := stdoutReader.Close(); err != nil {
			t.Errorf("failed to close stdout reader: %v", err)
		}
	}()

	originalStdout := os.Stdout
	os.Stdout = stdoutWriter
	defer func() { os.Stdout = originalStdout }()

	var stdout bytes.Buffer
	stdoutDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&stdout, stdoutReader)
		close(stdoutDone)
	}()

	withFlagArgs(t, args, func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				if _, ok := recovered.(exitCall); !ok {
					panic(recovered)
				}
			}
		}()
		main()
	})

	if err := stdoutWriter.Close(); err != nil {
		t.Fatalf("failed to close stdout writer: %v", err)
	}
	<-stdoutDone

	return commandResult{
		stdout:      stdout.String(),
		diagnostics: diagnostics.String(),
		exitCode:    *code,
	}
}

func TestParseFlagsInvalidOutput(t *testing.T) {
	restoreExit, code := stubExit(t)
	defer restoreExit()
	log.SetOutput(io.Discard)

	withFlagArgs(t, []string{
		"tf-version-bump",
		"-pattern", "*.tf",
		"-module", "module/source",
		"-to", "1.0.0",
		"-output", "invalid",
	}, func() {
		defer func() { _ = recover() }()
		parseFlags()
	})

	if *code != 1 {
		t.Fatalf("expected exit code 1, got %d", *code)
	}
}

func TestLoadModuleUpdatesMissingRequired(t *testing.T) {
	restoreExit, code := stubExit(t)
	defer restoreExit()
	log.SetOutput(io.Discard)

	defer func() { _ = recover() }()
	loadModuleUpdates(&cliFlags{})
	if *code != 1 {
		t.Fatalf("expected exit code 1, got %d", *code)
	}
}

func TestMainVersionFlag(t *testing.T) {
	restoreExit, code := stubExit(t)
	defer restoreExit()
	log.SetOutput(io.Discard)

	var buf bytes.Buffer
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = origStdout }()

	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	withFlagArgs(t, []string{"tf-version-bump", "-version"}, func() {
		defer func() { _ = recover() }()
		main()
	})

	if err := w.Close(); err != nil {
		t.Fatalf("failed to close pipe writer: %v", err)
	}
	<-done
	os.Stdout = origStdout

	output := buf.String()
	if *code != 0 {
		t.Fatalf("expected exit code 0, got %d", *code)
	}

	if !strings.Contains(output, "tf-version-bump") {
		t.Fatalf("expected version output, got %q", output)
	}
}

func TestMainExecutionPath(t *testing.T) {
	log.SetOutput(io.Discard)
	restoreExit, code := stubExit(t)
	defer restoreExit()

	tmpDir := t.TempDir()
	tfFile := filepath.Join(tmpDir, "main.tf")
	if err := os.WriteFile(tfFile, []byte("module \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n"), 0o644); err != nil {
		t.Fatalf("failed to write terraform file: %v", err)
	}

	withFlagArgs(t, []string{
		"tf-version-bump",
		"-pattern", filepath.Join(tmpDir, "*.tf"),
		"-module", "example/module",
		"-to", "2.0.0",
		"-dry-run",
	}, func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(exitCall); ok {
					t.Fatalf("unexpected exit during main execution")
				}
				panic(r)
			}
		}()
		main()
	})

	if *code != -1 {
		t.Fatalf("unexpected exit code recorded: %d", *code)
	}
}

func TestMainConfigFilePath(t *testing.T) {
	log.SetOutput(io.Discard)
	restoreExit, code := stubExit(t)
	defer restoreExit()

	tmpDir := t.TempDir()
	tfFile := filepath.Join(tmpDir, "main.tf")
	if err := os.WriteFile(tfFile, []byte(`terraform { required_version = ">= 0.13" }`), 0o644); err != nil {
		t.Fatalf("failed to create terraform file: %v", err)
	}

	configFile := filepath.Join(tmpDir, "config.yml")
	if err := os.WriteFile(configFile, []byte(`terraform_version: ">= 1.2"`), 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	withFlagArgs(t, []string{
		"tf-version-bump",
		"-pattern", filepath.Join(tmpDir, "*.tf"),
		"-config", configFile,
	}, func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(exitCall); ok {
					t.Fatalf("unexpected exit during config main path")
				}
				panic(r)
			}
		}()
		main()
	})

	if *code != -1 {
		t.Fatalf("unexpected exit code recorded: %d", *code)
	}
}

func TestCommandCLIReportsAggregateFileFailure(t *testing.T) {
	tmpDir := t.TempDir()
	malformedFile := filepath.Join(tmpDir, "01-malformed.tf")
	if err := os.WriteFile(malformedFile, []byte("!!!\n"), 0o644); err != nil {
		t.Fatalf("failed to write malformed Terraform file: %v", err)
	}
	validFile := filepath.Join(tmpDir, "02-valid.tf")
	if err := os.WriteFile(validFile, []byte("module \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n"), 0o644); err != nil {
		t.Fatalf("failed to write valid Terraform file: %v", err)
	}

	result := runMainCommand(t, []string{
		"tf-version-bump",
		"-pattern", filepath.Join(tmpDir, "*.tf"),
		"-module", "example/module",
		"-to", "2.0.0",
	})

	wantStdout := "Found 2 file(s) matching pattern '" + filepath.Join(tmpDir, "*.tf") + "'\n" +
		"✓ Updated module source 'example/module' to version '2.0.0' in " + validFile + "\n" +
		"\nSuccessfully updated 1 file(s)\n"
	if result.stdout != wantStdout {
		t.Errorf("stdout = %q, want %q", result.stdout, wantStdout)
	}

	wantDiagnostic := "Error processing " + malformedFile + ": failed to parse HCL: " + malformedFile + ":1,1-2: Argument or block definition required; An argument or block definition is required here.\n" +
		"1 module update error(s)\n"
	if result.diagnostics != wantDiagnostic {
		t.Errorf("stderr = %q, want %q", result.diagnostics, wantDiagnostic)
	}
	if result.exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", result.exitCode)
	}

	contents, err := os.ReadFile(validFile)
	if err != nil {
		t.Fatalf("failed to read valid Terraform file: %v", err)
	}
	if !strings.Contains(string(contents), `version = "2.0.0"`) {
		t.Fatalf("expected later valid file to update, got %q", contents)
	}
}

func TestCommandConfigReportsAggregateFileFailure(t *testing.T) {
	tmpDir := t.TempDir()
	malformedFile := filepath.Join(tmpDir, "01-malformed.tf")
	if err := os.WriteFile(malformedFile, []byte("!!!\n"), 0o644); err != nil {
		t.Fatalf("failed to write malformed Terraform file: %v", err)
	}
	validFile := filepath.Join(tmpDir, "02-valid.tf")
	if err := os.WriteFile(validFile, []byte("module \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n"), 0o644); err != nil {
		t.Fatalf("failed to write valid Terraform file: %v", err)
	}
	configFile := filepath.Join(tmpDir, "updates.yml")
	if err := os.WriteFile(configFile, []byte("modules:\n  - source: example/module\n    version: 2.0.0\n"), 0o644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	result := runMainCommand(t, []string{
		"tf-version-bump",
		"-pattern", filepath.Join(tmpDir, "*.tf"),
		"-config", configFile,
	})

	wantStdout := "Found 2 file(s) matching pattern '" + filepath.Join(tmpDir, "*.tf") + "'\n" +
		"✓ Updated module source 'example/module' to version '2.0.0' in " + validFile + "\n" +
		"\n==================================================\n" +
		"Config File Update Summary\n" +
		"==================================================\n" +
		"Modules: 1 file(s) updated\n"
	if result.stdout != wantStdout {
		t.Errorf("stdout = %q, want %q", result.stdout, wantStdout)
	}

	wantDiagnostic := "Error processing " + malformedFile + ": failed to parse HCL: " + malformedFile + ":1,1-2: Argument or block definition required; An argument or block definition is required here.\n" +
		"1 module update error(s)\n"
	if result.diagnostics != wantDiagnostic {
		t.Errorf("stderr = %q, want %q", result.diagnostics, wantDiagnostic)
	}
	if result.exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", result.exitCode)
	}

	contents, err := os.ReadFile(validFile)
	if err != nil {
		t.Fatalf("failed to read valid Terraform file: %v", err)
	}
	if !strings.Contains(string(contents), `version = "2.0.0"`) {
		t.Fatalf("expected later valid file to update, got %q", contents)
	}
}

func TestValidateOperationModesFailures(t *testing.T) {
	log.SetOutput(io.Discard)

	tests := []struct {
		name  string
		flags *cliFlags
	}{
		{
			name: "config with other flags",
			flags: &cliFlags{
				configFile:   "config.yml",
				moduleSource: "source",
			},
		},
		{
			name:  "no modes set",
			flags: &cliFlags{},
		},
		{
			name: "multiple modes set",
			flags: &cliFlags{
				moduleSource:     "source",
				terraformVersion: ">= 1.5",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restoreExit, code := stubExit(t)
			defer restoreExit()
			defer func() { _ = recover() }()
			validateOperationModes(tt.flags)
			if *code != 1 {
				t.Fatalf("expected exit code 1, got %d", *code)
			}
		})
	}
}

func TestValidateOperationModesConfigOnly(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("validateOperationModes panicked: %v", r)
		}
	}()
	validateOperationModes(&cliFlags{configFile: "config.yml"})
}

func TestValidateOperationModesProviderOnly(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("validateOperationModes panicked: %v", r)
		}
	}()
	validateOperationModes(&cliFlags{providerName: "aws"})
}

func TestFindMatchingFilesFailures(t *testing.T) {
	log.SetOutput(io.Discard)

	t.Run("missing pattern", func(t *testing.T) {
		restoreExit, code := stubExit(t)
		defer restoreExit()
		defer func() { _ = recover() }()
		findMatchingFiles(&cliFlags{})
		if *code != 1 {
			t.Fatalf("expected exit code 1, got %d", *code)
		}
	})

	t.Run("bad glob", func(t *testing.T) {
		restoreExit, code := stubExit(t)
		defer restoreExit()
		defer func() { _ = recover() }()
		findMatchingFiles(&cliFlags{pattern: "["})
		if *code != 1 {
			t.Fatalf("expected exit code 1, got %d", *code)
		}
	})

	t.Run("no matches", func(t *testing.T) {
		restoreExit, code := stubExit(t)
		defer restoreExit()
		defer func() { _ = recover() }()
		findMatchingFiles(&cliFlags{pattern: filepath.Join(t.TempDir(), "*.tf")})
		if *code != 1 {
			t.Fatalf("expected exit code 1, got %d", *code)
		}
	})
}

func TestFindMatchingFilesDryRunMessage(t *testing.T) {
	tmpDir := t.TempDir()
	tfFile := filepath.Join(tmpDir, "main.tf")
	if err := os.WriteFile(tfFile, []byte("# test"), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	flags := &cliFlags{
		pattern: filepath.Join(tmpDir, "*.tf"),
		dryRun:  true,
		output:  "text",
	}

	files := findMatchingFiles(flags)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
}

func TestRunConfigFileModeReturnsLoadError(t *testing.T) {
	restoreExit, code := stubExit(t)
	defer restoreExit()
	log.SetOutput(io.Discard)

	var gotErr error
	var recovered interface{}
	func() {
		defer func() { recovered = recover() }()
		gotErr = runConfigFileMode(nil, &cliFlags{configFile: "does-not-exist"})
	}()

	if recovered != nil {
		t.Fatalf("runConfigFileMode exited instead of returning an error: %v", recovered)
	}
	if *code != -1 {
		t.Fatalf("runConfigFileMode exited with code %d", *code)
	}
	if gotErr == nil {
		t.Fatal("expected config load error")
	}
	if !strings.Contains(gotErr.Error(), "Error loading config file: failed to read config file:") {
		t.Fatalf("unexpected error: %v", gotErr)
	}
	if !errors.Is(gotErr, os.ErrNotExist) {
		t.Fatalf("expected wrapped file-not-found error, got: %v", gotErr)
	}
}

func TestRunCLIModeProviderMissingVersion(t *testing.T) {
	restoreExit, code := stubExit(t)
	defer restoreExit()
	log.SetOutput(io.Discard)

	defer func() { _ = recover() }()
	_ = runCLIMode(nil, &cliFlags{providerName: "aws"})
	if *code != 1 {
		t.Fatalf("expected exit code 1, got %d", *code)
	}
}

func TestUpdateTerraformVersionReadError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("Skipping read error test when running as root")
	}
	tmpDir := t.TempDir()
	file := filepath.Join(tmpDir, "unreadable.tf")
	if err := os.WriteFile(file, []byte("content"), 0o222); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	updated, err := updateTerraformVersion(file, "1.0.0", false)
	if err == nil || updated {
		t.Fatalf("expected read error, got updated=%v err=%v", updated, err)
	}
}

func TestUpdateTerraformVersionWriteError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("Skipping write error test when running as root")
	}
	tmpDir := t.TempDir()
	file := filepath.Join(tmpDir, "main.tf")
	content := `terraform { required_version = ">= 0.13" }`
	if err := os.WriteFile(file, []byte(content), 0o444); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	updated, err := updateTerraformVersion(file, ">= 1.0", false)
	if err == nil || updated {
		t.Fatalf("expected write error, got updated=%v err=%v", updated, err)
	}
}

func TestUpdateProviderVersionReadError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("Skipping read error test when running as root")
	}
	tmpDir := t.TempDir()
	file := filepath.Join(tmpDir, "provider.tf")
	if err := os.WriteFile(file, []byte("content"), 0o222); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	updated, err := updateProviderVersion(file, "aws", "1.0.0", false)
	if err == nil || updated {
		t.Fatalf("expected read error, got updated=%v err=%v", updated, err)
	}
}

func TestUpdateProviderVersionWriteError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("Skipping write error test when running as root")
	}
	tmpDir := t.TempDir()
	file := filepath.Join(tmpDir, "provider.tf")
	content := `terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}`
	if err := os.WriteFile(file, []byte(content), 0o444); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	updated, err := updateProviderVersion(file, "aws", "1.0.0", false)
	if err == nil || updated {
		t.Fatalf("expected write error, got updated=%v err=%v", updated, err)
	}
}

func TestUpdateProviderAttributeVersionVariants(t *testing.T) {
	tests := []struct {
		name         string
		setup        func(*hclwrite.Block)
		expectResult bool
		providerName string
	}{
		{
			name: "missing attribute",
			setup: func(block *hclwrite.Block) {
				// no attributes added
			},
			expectResult: false,
		},
		{
			name: "parse error",
			setup: func(block *hclwrite.Block) {
				block.Body().SetAttributeRaw("aws", hclwrite.Tokens{
					&hclwrite.Token{Type: hclsyntax.TokenOBrace, Bytes: []byte("{")},
				})
			},
			expectResult: false,
		},
		{
			name: "non object expression",
			setup: func(block *hclwrite.Block) {
				block.Body().SetAttributeValue("aws", cty.StringVal("literal"))
			},
			expectResult: false,
		},
		{
			name: "object without version",
			setup: func(block *hclwrite.Block) {
				block.Body().SetAttributeRaw("aws", hclwrite.Tokens{
					&hclwrite.Token{Type: hclsyntax.TokenOBrace, Bytes: []byte("{")},
					&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("source")},
					&hclwrite.Token{Type: hclsyntax.TokenEqual, Bytes: []byte("=")},
					&hclwrite.Token{Type: hclsyntax.TokenOQuote, Bytes: []byte("\"")},
					&hclwrite.Token{Type: hclsyntax.TokenQuotedLit, Bytes: []byte("hashicorp/aws")},
					&hclwrite.Token{Type: hclsyntax.TokenCQuote, Bytes: []byte("\"")},
					&hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n")},
					&hclwrite.Token{Type: hclsyntax.TokenCBrace, Bytes: []byte("}")},
				})
			},
			expectResult: false,
		},
		{
			name: "non traversal key",
			setup: func(block *hclwrite.Block) {
				block.Body().SetAttributeRaw("aws", hclwrite.Tokens{
					&hclwrite.Token{Type: hclsyntax.TokenOBrace, Bytes: []byte("{")},
					&hclwrite.Token{Type: hclsyntax.TokenOParen, Bytes: []byte("(")},
					&hclwrite.Token{Type: hclsyntax.TokenNumberLit, Bytes: []byte("1")},
					&hclwrite.Token{Type: hclsyntax.TokenPlus, Bytes: []byte("+")},
					&hclwrite.Token{Type: hclsyntax.TokenNumberLit, Bytes: []byte("1")},
					&hclwrite.Token{Type: hclsyntax.TokenCParen, Bytes: []byte(")")},
					&hclwrite.Token{Type: hclsyntax.TokenEqual, Bytes: []byte("=")},
					&hclwrite.Token{Type: hclsyntax.TokenNumberLit, Bytes: []byte("2")},
					&hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n")},
					&hclwrite.Token{Type: hclsyntax.TokenCBrace, Bytes: []byte("}")},
				})
			},
			expectResult: false,
		},
		{
			name: "additional attributes retained",
			setup: func(block *hclwrite.Block) {
				block.Body().SetAttributeRaw("aws", hclwrite.Tokens{
					&hclwrite.Token{Type: hclsyntax.TokenOBrace, Bytes: []byte("{")},
					&hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n")},
					&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("source")},
					&hclwrite.Token{Type: hclsyntax.TokenEqual, Bytes: []byte("=")},
					&hclwrite.Token{Type: hclsyntax.TokenOQuote, Bytes: []byte("\"")},
					&hclwrite.Token{Type: hclsyntax.TokenQuotedLit, Bytes: []byte("hashicorp/aws")},
					&hclwrite.Token{Type: hclsyntax.TokenCQuote, Bytes: []byte("\"")},
					&hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n")},
					&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("version")},
					&hclwrite.Token{Type: hclsyntax.TokenEqual, Bytes: []byte("=")},
					&hclwrite.Token{Type: hclsyntax.TokenOQuote, Bytes: []byte("\"")},
					&hclwrite.Token{Type: hclsyntax.TokenQuotedLit, Bytes: []byte("~> 4.0")},
					&hclwrite.Token{Type: hclsyntax.TokenCQuote, Bytes: []byte("\"")},
					&hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n")},
					&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("region")},
					&hclwrite.Token{Type: hclsyntax.TokenEqual, Bytes: []byte("=")},
					&hclwrite.Token{Type: hclsyntax.TokenOQuote, Bytes: []byte("\"")},
					&hclwrite.Token{Type: hclsyntax.TokenQuotedLit, Bytes: []byte("us-west-2")},
					&hclwrite.Token{Type: hclsyntax.TokenCQuote, Bytes: []byte("\"")},
					&hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n")},
					&hclwrite.Token{Type: hclsyntax.TokenCBrace, Bytes: []byte("}")},
				})
			},
			expectResult: true,
		},
		{
			name: "parse error on reconstruction due to invalid provider name",
			setup: func(block *hclwrite.Block) {
				block.Body().SetAttributeRaw("invalid provider", hclwrite.Tokens{
					&hclwrite.Token{Type: hclsyntax.TokenOBrace, Bytes: []byte("{")},
					&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("source")},
					&hclwrite.Token{Type: hclsyntax.TokenEqual, Bytes: []byte("=")},
					&hclwrite.Token{Type: hclsyntax.TokenOQuote, Bytes: []byte("\"")},
					&hclwrite.Token{Type: hclsyntax.TokenQuotedLit, Bytes: []byte("hashicorp/aws")},
					&hclwrite.Token{Type: hclsyntax.TokenCQuote, Bytes: []byte("\"")},
					&hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n")},
					&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("version")},
					&hclwrite.Token{Type: hclsyntax.TokenEqual, Bytes: []byte("=")},
					&hclwrite.Token{Type: hclsyntax.TokenOQuote, Bytes: []byte("\"")},
					&hclwrite.Token{Type: hclsyntax.TokenQuotedLit, Bytes: []byte("~> 4.0")},
					&hclwrite.Token{Type: hclsyntax.TokenCQuote, Bytes: []byte("\"")},
					&hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n")},
					&hclwrite.Token{Type: hclsyntax.TokenCBrace, Bytes: []byte("}")},
				})
			},
			expectResult: false,
			providerName: "invalid provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			block := hclwrite.NewBlock("required_providers", nil)
			tt.setup(block)
			name := tt.providerName
			if name == "" {
				name = "aws"
			}
			result := updateProviderAttributeVersion(block, name, "9.9.9")
			if result != tt.expectResult {
				t.Fatalf("expected %v, got %v", tt.expectResult, result)
			}
		})
	}
}

func TestUpdateModuleVersionVerboseIgnore(t *testing.T) {
	tmpDir := t.TempDir()
	tfFile := filepath.Join(tmpDir, "main.tf")
	content := `module "example" {
  source  = "example/module"
  version = "1.0.0"
}`
	if err := os.WriteFile(tfFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	updated, err := updateModuleVersion(tfFile, "example/module", "2.0.0", nil, []string{"1.0.0"}, nil, false, false, true, "text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated {
		t.Fatalf("expected no update due to ignore version")
	}
}
