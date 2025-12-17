package main

import (
	"bytes"
	"flag"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

type exitCall struct {
	code int
}

func stubExit(t *testing.T) (func(), *int) {
	t.Helper()
	hookMu.Lock()
	original := exitFunc
	code := -1
	exitFunc = func(c int) {
		code = c
		panic(exitCall{code: c})
	}
	return func() {
		exitFunc = original
		hookMu.Unlock()
	}, &code
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
		if *code != 1 {
			t.Fatalf("expected exit code 1, got %d", *code)
		}
	})
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
	if err := os.WriteFile(tfFile, []byte(`module "example" { source = "example/module" version = "1.0.0" }`), 0644); err != nil {
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
	if err := os.WriteFile(tfFile, []byte(`terraform { required_version = ">= 0.13" }`), 0644); err != nil {
		t.Fatalf("failed to create terraform file: %v", err)
	}

	configFile := filepath.Join(tmpDir, "config.yml")
	if err := os.WriteFile(configFile, []byte(`terraform_version: ">= 1.2"`), 0644); err != nil {
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
	if err := os.WriteFile(tfFile, []byte("# test"), 0644); err != nil {
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

func TestRunConfigFileModeLoadError(t *testing.T) {
	restoreExit, code := stubExit(t)
	defer restoreExit()
	log.SetOutput(io.Discard)

	defer func() { _ = recover() }()
	runConfigFileMode(nil, &cliFlags{configFile: "does-not-exist"})
	if *code != 1 {
		t.Fatalf("expected exit code 1, got %d", *code)
	}
}

func TestRunCLIModeProviderMissingVersion(t *testing.T) {
	restoreExit, code := stubExit(t)
	defer restoreExit()
	log.SetOutput(io.Discard)

	defer func() { _ = recover() }()
	runCLIMode(nil, &cliFlags{providerName: "aws"})
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
	if err := os.WriteFile(file, []byte("content"), 0222); err != nil {
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
	if err := os.WriteFile(file, []byte(content), 0444); err != nil {
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
	if err := os.WriteFile(file, []byte("content"), 0222); err != nil {
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
	if err := os.WriteFile(file, []byte(content), 0444); err != nil {
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
			name: "parse error on reconstruction",
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

func TestUpdateProviderAttributeVersionGuardBranches(t *testing.T) {
	newBlock := func() *hclwrite.Block {
		block := hclwrite.NewBlock("required_providers", nil)
		block.Body().SetAttributeRaw("aws", hclwrite.Tokens{
			&hclwrite.Token{Type: hclsyntax.TokenOBrace, Bytes: []byte("{")},
			&hclwrite.Token{Type: hclsyntax.TokenCBrace, Bytes: []byte("}")},
		})
		return block
	}

	setParseExpression := func(fn func([]byte, string, hcl.Pos) (hclsyntax.Expression, hcl.Diagnostics)) func() {
		hookMu.Lock()
		prev := parseExpression
		parseExpression = fn
		return func() {
			parseExpression = prev
			hookMu.Unlock()
		}
	}

	t.Run("non object key expression", func(t *testing.T) {
		restore := setParseExpression(func([]byte, string, hcl.Pos) (hclsyntax.Expression, hcl.Diagnostics) {
			obj := &hclsyntax.ObjectConsExpr{
				Items: []hclsyntax.ObjectConsItem{
					{
						KeyExpr:   &hclsyntax.TemplateExpr{},
						ValueExpr: &hclsyntax.TemplateExpr{},
					},
				},
			}
			return obj, hcl.Diagnostics{}
		})
		t.Cleanup(restore)
		if updateProviderAttributeVersion(newBlock(), "aws", "1.0.0") {
			t.Fatalf("expected false result")
		}
	})

	t.Run("empty traversal", func(t *testing.T) {
		restore := setParseExpression(func([]byte, string, hcl.Pos) (hclsyntax.Expression, hcl.Diagnostics) {
			obj := &hclsyntax.ObjectConsExpr{
				Items: []hclsyntax.ObjectConsItem{
					{
						KeyExpr: &hclsyntax.ObjectConsKeyExpr{
							Wrapped: &hclsyntax.ScopeTraversalExpr{
								Traversal: hcl.Traversal{},
							},
						},
						ValueExpr: &hclsyntax.TemplateExpr{},
					},
				},
			}
			return obj, hcl.Diagnostics{}
		})
		t.Cleanup(restore)
		if updateProviderAttributeVersion(newBlock(), "aws", "1.0.0") {
			t.Fatalf("expected false result")
		}
	})

	t.Run("non root traversal", func(t *testing.T) {
		restore := setParseExpression(func([]byte, string, hcl.Pos) (hclsyntax.Expression, hcl.Diagnostics) {
			obj := &hclsyntax.ObjectConsExpr{
				Items: []hclsyntax.ObjectConsItem{
					{
						KeyExpr: &hclsyntax.ObjectConsKeyExpr{
							Wrapped: &hclsyntax.ScopeTraversalExpr{
								Traversal: hcl.Traversal{
									hcl.TraverseAttr{Name: "attr"},
								},
							},
						},
						ValueExpr: &hclsyntax.TemplateExpr{},
					},
				},
			}
			return obj, hcl.Diagnostics{}
		})
		t.Cleanup(restore)
		if updateProviderAttributeVersion(newBlock(), "aws", "1.0.0") {
			t.Fatalf("expected false result")
		}
	})

	t.Run("literal value branch", func(t *testing.T) {
		restore := setParseExpression(func([]byte, string, hcl.Pos) (hclsyntax.Expression, hcl.Diagnostics) {
			obj := &hclsyntax.ObjectConsExpr{
				Items: []hclsyntax.ObjectConsItem{
					{
						KeyExpr: &hclsyntax.ObjectConsKeyExpr{
							Wrapped: &hclsyntax.ScopeTraversalExpr{
								Traversal: hcl.Traversal{
									hcl.TraverseRoot{Name: "version"},
								},
							},
						},
						ValueExpr: &hclsyntax.LiteralValueExpr{Val: cty.StringVal("old")},
					},
				},
			}
			return obj, hcl.Diagnostics{}
		})
		t.Cleanup(restore)
		if !updateProviderAttributeVersion(newBlock(), "aws", "2.0.0") {
			t.Fatalf("expected update to succeed")
		}
	})

	t.Run("default branch value", func(t *testing.T) {
		restore := setParseExpression(func([]byte, string, hcl.Pos) (hclsyntax.Expression, hcl.Diagnostics) {
			obj := &hclsyntax.ObjectConsExpr{
				Items: []hclsyntax.ObjectConsItem{
					{
						KeyExpr: &hclsyntax.ObjectConsKeyExpr{
							Wrapped: &hclsyntax.ScopeTraversalExpr{
								Traversal: hcl.Traversal{
									hcl.TraverseRoot{Name: "source"},
								},
							},
						},
						ValueExpr: &hclsyntax.ScopeTraversalExpr{
							Traversal: hcl.Traversal{
								hcl.TraverseRoot{Name: "var"},
								hcl.TraverseAttr{Name: "source"},
							},
						},
					},
				},
			}
			return obj, hcl.Diagnostics{}
		})
		t.Cleanup(restore)
		if updateProviderAttributeVersion(newBlock(), "aws", "2.0.0") {
			t.Fatalf("expected update to fail due to missing version")
		}
	})
}

func TestUpdateModuleVersionVerboseIgnore(t *testing.T) {
	tmpDir := t.TempDir()
	tfFile := filepath.Join(tmpDir, "main.tf")
	content := `module "example" {
  source  = "example/module"
  version = "1.0.0"
}`
	if err := os.WriteFile(tfFile, []byte(content), 0644); err != nil {
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
