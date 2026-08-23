package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type capturedStreams struct {
	stdout string
	stderr string
}

type moduleCase struct {
	name           string
	input          string
	source         string
	version        string
	from           []string
	ignoreVersions []string
	ignoreModules  []string
	forceAdd       bool
	dryRun         bool
	verbose        bool
	wantUpdated    bool
	wantContent    string
}

func captureStdoutAndStderr(t *testing.T, fn func()) capturedStreams {
	t.Helper()
	testOutputMu.Lock()
	defer testOutputMu.Unlock()
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = stdoutWriter.Close()
		_ = stdoutReader.Close()
	})
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stderr pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = stderrWriter.Close()
		_ = stderrReader.Close()
	})
	originalStdout, originalStderr := os.Stdout, os.Stderr
	restore := func() { os.Stdout, os.Stderr = originalStdout, originalStderr }
	t.Cleanup(restore)
	defer restore()
	os.Stdout, os.Stderr = stdoutWriter, stderrWriter
	stdout, stdoutDone := startPipeDrain(stdoutReader)
	stderr, stderrDone := startPipeDrain(stderrReader)
	fn()
	restore()
	if err := stdoutWriter.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	if err := stderrWriter.Close(); err != nil {
		t.Fatalf("close stderr writer: %v", err)
	}
	<-stdoutDone
	<-stderrDone
	if err := stdoutReader.Close(); err != nil {
		t.Fatalf("close stdout reader: %v", err)
	}
	if err := stderrReader.Close(); err != nil {
		t.Fatalf("close stderr reader: %v", err)
	}
	return capturedStreams{stdout: stdout.String(), stderr: stderr.String()}
}

func TestUpdateModuleVersionContract(t *testing.T) {
	const registry = "terraform-aws-modules/vpc/aws"
	cases := []moduleCase{
		{"matching registry module updates", "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n}\n", registry, "5.0.0", nil, nil, nil, false, false, false, true, "module \"vpc\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"5.0.0\"\n}\n"},
		{"non-matching source unchanged", "module \"vpc\" {\n  source = \"terraform-aws-modules/s3-bucket/aws\"\n  version = \"3.14.0\"\n}\n", registry, "5.0.0", nil, nil, nil, false, false, false, false, "module \"vpc\" {\n  source = \"terraform-aws-modules/s3-bucket/aws\"\n  version = \"3.14.0\"\n}\n"},
		{"force-add missing version", "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n}\n", registry, "5.0.0", nil, nil, nil, true, false, false, true, "module \"vpc\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"5.0.0\"\n}\n"},
		{"existing Git version remains updateable", "module \"vpc\" {\n  source = \"git::https://github.com/example/vpc.git\"\n  version = \"1.0.0\"\n}\n", "git::https://github.com/example/vpc.git", "2.0.0", nil, nil, nil, false, false, false, true, "module \"vpc\" {\n  source  = \"git::https://github.com/example/vpc.git\"\n  version = \"2.0.0\"\n}\n"},
		{"force-add does not block an existing HTTP version", "module \"vpc\" {\n  source = \"https://example.com/vpc.zip\"\n  version = \"1.0.0\"\n}\n", "https://example.com/vpc.zip", "2.0.0", nil, nil, nil, true, false, false, true, "module \"vpc\" {\n  source  = \"https://example.com/vpc.zip\"\n  version = \"2.0.0\"\n}\n"},
		{"relative local source skipped", "module \"vpc\" {\n  source = \"./modules/vpc\"\n}\n", "./modules/vpc", "5.0.0", nil, nil, nil, true, false, false, false, "module \"vpc\" {\n  source = \"./modules/vpc\"\n}\n"},
		{"matching from filter updates", "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n}\n", registry, "5.0.0", []string{"3.14.0"}, nil, nil, false, false, false, true, "module \"vpc\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"5.0.0\"\n}\n"},
		{"updates every eligible block with the same source", "module \"primary\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n}\n\nmodule \"secondary\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"4.0.0\"\n}\n", registry, "5.0.0", nil, nil, nil, false, false, false, true, "module \"primary\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"5.0.0\"\n}\n\nmodule \"secondary\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"5.0.0\"\n}\n"},
		{"second from entry matches", "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n}\n", registry, "5.0.0", []string{"4.0.0", "3.14.0"}, nil, nil, false, false, false, true, "module \"vpc\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"5.0.0\"\n}\n"},
		{"second ignore version entry matches", "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n}\n", registry, "5.0.0", nil, []string{"4.0.0", "3.14.0"}, nil, false, false, false, false, "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n}\n"},
		{"non-matching from preserves version", "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n}\n", registry, "5.0.0", []string{"4.0.0"}, nil, nil, false, false, true, false, "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n}\n"},
		{"matching ignore version preserves version", "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n}\n", registry, "5.0.0", nil, []string{"3.14.0"}, nil, false, false, false, false, "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n}\n"},
		{"mixed ignored and eligible modules", "module \"ignored\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n}\n\nmodule \"eligible\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"4.0.0\"\n}\n", registry, "5.0.0", nil, []string{"3.14.0"}, nil, false, false, false, true, "module \"ignored\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n}\n\nmodule \"eligible\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"5.0.0\"\n}\n"},
		{"ignore takes precedence over from", "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n}\n", registry, "5.0.0", []string{"4.0.0"}, []string{"3.14.0"}, nil, false, false, true, false, "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n}\n"},
		{"module name ignore preserves unchanged content", "module \"legacy-vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n}\n", registry, "5.0.0", nil, nil, []string{"legacy-*"}, false, false, true, false, "module \"legacy-vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file := writeTestFile(t, t.TempDir(), "main.tf", tc.input)
			var updated bool
			var err error
			output := captureStdoutAndStderr(t, func() {
				updated, err = updateModuleVersion(file, tc.source, tc.version, tc.from, tc.ignoreVersions, tc.ignoreModules, tc.forceAdd, tc.dryRun, tc.verbose, "text")
			})
			if err != nil {
				t.Fatalf("updateModuleVersion: %v", err)
			}
			if updated != tc.wantUpdated {
				t.Fatalf("updated = %v, want %v", updated, tc.wantUpdated)
			}
			if got := readTestFile(t, file); got != tc.wantContent {
				t.Errorf("content = %q, want %q", got, tc.wantContent)
			}
			switch {
			case tc.verbose:
				want := wantModuleVerboseDiagnostic(&tc, file)
				if output.stdout != want || output.stderr != "" {
					t.Errorf("streams = stdout %q stderr %q, want stdout %q and empty stderr", output.stdout, output.stderr, want)
				}
			case strings.Contains(tc.name, "local source"):
				want := fmt.Sprintf("Warning: Module 'vpc' in %s (source: './modules/vpc') is a local module and cannot be version-bumped, skipping\n", file)
				if output.stdout != "" || output.stderr != want {
					t.Errorf("streams = stdout %q stderr %q, want stdout empty stderr %q", output.stdout, output.stderr, want)
				}
			case output.stdout != "" || output.stderr != "":
				t.Errorf("unexpected output: stdout=%q stderr=%q", output.stdout, output.stderr)
			}
		})
	}
}

func wantModuleVerboseDiagnostic(tc *moduleCase, file string) string {
	switch {
	case len(tc.ignoreVersions) > 0:
		return fmt.Sprintf("  ⊗ Skipped module 'vpc' in %s (current version '3.14.0' matches 'ignore-version' filter [3.14.0])\n", file)
	case len(tc.from) > 0:
		return fmt.Sprintf("  ⊗ Skipped module 'vpc' in %s (current version '3.14.0' does not match any 'from' filter [4.0.0])\n", file)
	case len(tc.ignoreModules) > 0:
		return fmt.Sprintf("  ⊗ Skipped module 'legacy-vpc' in %s (matches ignore pattern)\n", file)
	default:
		return ""
	}
}

func TestUpdateModuleVersionWarnsWhenVersionMissing(t *testing.T) {
	input := "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n}\n"
	file := writeTestFile(t, t.TempDir(), "main.tf", input)
	want := fmt.Sprintf("Warning: Module 'vpc' in %s (source: 'terraform-aws-modules/vpc/aws') has no version attribute, skipping\n", file)
	var updated bool
	var err error
	stderr := captureStderr(t, func() {
		updated, err = updateModuleVersion(file, "terraform-aws-modules/vpc/aws", "5.0.0", nil, nil, nil, false, false, false, "text")
	})
	if err != nil || updated {
		t.Fatalf("updated=%v err=%v", updated, err)
	}
	if stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
	if got := readTestFile(t, file); got != input {
		t.Errorf("content changed: %q", got)
	}
}

func TestUpdateModuleVersionForceAddRequiresRegistrySource(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{name: "Git URL", source: "git::https://github.com/example/vpc.git"},
		{name: "GitHub shorthand", source: "github.com/example/vpc"},
		{name: "HTTP URL", source: "https://example.com/vpc.zip"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := fmt.Sprintf("module \"vpc\" {\n  source = %q\n}\n", tt.source)
			file := writeTestFile(t, t.TempDir(), "main.tf", input)
			wantWarning := fmt.Sprintf("Warning: Module 'vpc' in %s (source: '%s') is not a registry module and cannot use a version attribute, skipping\n", file, tt.source)

			var updated bool
			var err error
			stderr := captureStderr(t, func() {
				updated, err = updateModuleVersion(file, tt.source, "2.0.0", nil, nil, nil, true, false, false, "text")
			})

			if err != nil || updated {
				t.Fatalf("updated=%v err=%v", updated, err)
			}
			if stderr != wantWarning {
				t.Errorf("stderr = %q, want %q", stderr, wantWarning)
			}
			if got := readTestFile(t, file); got != input {
				t.Errorf("content = %q, want unchanged %q", got, input)
			}
		})
	}
}

func TestUpdateModuleVersionForceAddSupportsPrivateRegistrySource(t *testing.T) {
	input := "module \"vpc\" {\n  source = \"app.terraform.io/example/vpc/aws\"\n}\n"
	file := writeTestFile(t, t.TempDir(), "main.tf", input)

	updated, err := updateModuleVersion(file, "app.terraform.io/example/vpc/aws", "2.0.0", nil, nil, nil, true, false, false, "text")
	if err != nil || !updated {
		t.Fatalf("updated=%v err=%v", updated, err)
	}
	want := "module \"vpc\" {\n  source  = \"app.terraform.io/example/vpc/aws\"\n  version = \"2.0.0\"\n}\n"
	if got := readTestFile(t, file); got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}

func TestUpdateModuleVersionPreservesHCL(t *testing.T) {
	input := "# This is a comment\nmodule \"vpc\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n\n  # Another comment\n  name = \"my-vpc\"\n  cidr = var.vpc_cidr\n}\n\nmodule \"other\" {\n  source = \"terraform-aws-modules/s3-bucket/aws\"\n  version = \"1.0.0\"\n}\n"
	want := "# This is a comment\nmodule \"vpc\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"5.0.0\"\n\n  # Another comment\n  name = \"my-vpc\"\n  cidr = var.vpc_cidr\n}\n\nmodule \"other\" {\n  source  = \"terraform-aws-modules/s3-bucket/aws\"\n  version = \"1.0.0\"\n}\n"
	file := writeTestFile(t, t.TempDir(), "main.tf", input)
	updated, err := updateModuleVersion(file, "terraform-aws-modules/vpc/aws", "5.0.0", nil, nil, nil, false, false, false, "text")
	if err != nil || !updated {
		t.Fatalf("updated=%v err=%v", updated, err)
	}
	if got := readTestFile(t, file); got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}

func TestUpdateModuleVersionPreservesPermissions(t *testing.T) {
	file := writeTestFile(t, t.TempDir(), "main.tf", "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"1.0.0\"\n}\n")
	if err := os.Chmod(file, 0o640); err != nil {
		t.Fatal(err)
	}
	updated, err := updateModuleVersion(file, "terraform-aws-modules/vpc/aws", "2.0.0", nil, nil, nil, false, false, false, "text")
	if err != nil || !updated {
		t.Fatalf("updated=%v err=%v", updated, err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Errorf("mode = %o, want 640", got)
	}
}

func TestUpdateModuleVersionMatchingVersionDoesNotWrite(t *testing.T) {
	input := `module "vpc" {
source="terraform-aws-modules/vpc/aws"
version="5.0.0"
}
`
	filename := writeTestFile(t, t.TempDir(), "main.tf", input)
	wantModTime := time.Unix(1, 0)
	if err := os.Chtimes(filename, wantModTime, wantModTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	updated, err := updateModuleVersion(filename, "terraform-aws-modules/vpc/aws", "5.0.0", nil, nil, nil, false, false, false, "text")
	if err != nil {
		t.Fatalf("updateModuleVersion returned error: %v", err)
	}
	if updated {
		t.Fatal("updated = true, want false")
	}
	if got := readTestFile(t, filename); got != input {
		t.Errorf("content = %q, want unchanged %q", got, input)
	}
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.ModTime(); !got.Equal(wantModTime) {
		t.Errorf("modification time = %v, want unchanged %v", got, wantModTime)
	}
}

func TestUpdateModuleVersionDryRunContract(t *testing.T) {
	input := "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"1.0.0\"\n}\n"
	file := writeTestFile(t, t.TempDir(), "main.tf", input)
	var updated bool
	var err error
	output := captureStdoutAndStderr(t, func() {
		updated, err = updateModuleVersion(file, "terraform-aws-modules/vpc/aws", "2.0.0", nil, nil, nil, false, true, false, "text")
	})
	if err != nil || !updated {
		t.Fatalf("updated=%v err=%v", updated, err)
	}
	if output.stdout != "" || output.stderr != "" {
		t.Errorf("output = stdout %q stderr %q, want both empty", output.stdout, output.stderr)
	}
	if got := readTestFile(t, file); got != input {
		t.Errorf("dry run changed content")
	}
}

func TestIsLocalModuleContract(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		want         bool
	}{{"relative", "./modules/vpc", true}, {"parent-relative", "../shared", true}, {"absolute", "/opt/module", true}, {"registry", "terraform-aws-modules/vpc/aws", false}, {"git", "git::https://github.com/example/vpc.git", false}} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isLocalModule(tc.source); got != tc.want {
				t.Errorf("isLocalModule(%q)=%v, want %v", tc.source, got, tc.want)
			}
		})
	}
}

func TestUpdateModuleVersionErrors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		updated, err := updateModuleVersion(filepath.Join(t.TempDir(), "missing.tf"), "x", "1.0.0", nil, nil, nil, false, false, false, "text")
		if updated || err == nil || !strings.Contains(err.Error(), "failed to stat file:") {
			t.Errorf("updated=%v err=%v", updated, err)
		}
	})
	t.Run("directory read error", func(t *testing.T) {
		directory := t.TempDir()
		updated, err := updateModuleVersion(directory, "x", "1.0.0", nil, nil, nil, false, false, false, "text")
		if updated || err == nil || !strings.Contains(err.Error(), "failed to read file:") {
			t.Errorf("updated=%v err=%v", updated, err)
		}
	})
	t.Run("malformed HCL", func(t *testing.T) {
		file := writeTestFile(t, t.TempDir(), "bad.tf", "module \\\"vpc\\\" {")
		updated, err := updateModuleVersion(file, "x", "1.0.0", nil, nil, nil, false, false, false, "text")
		if updated || err == nil || !strings.Contains(err.Error(), "failed to parse HCL:") {
			t.Errorf("updated=%v err=%v", updated, err)
		}
	})
	t.Run("write error", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root can write read-only files")
		}
		file := writeTestFile(t, t.TempDir(), "readonly.tf", "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"1.0.0\"\n}\n")
		if err := os.Chmod(file, 0o400); err != nil {
			t.Fatal(err)
		}
		updated, err := updateModuleVersion(file, "terraform-aws-modules/vpc/aws", "2.0.0", nil, nil, nil, false, false, false, "text")
		if updated || err == nil || !strings.Contains(err.Error(), "failed to write file:") {
			t.Errorf("updated=%v err=%v", updated, err)
		}
	})
}
