package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestFindMatchingFilesRecursiveAndSorted(t *testing.T) {
	tmpDir := t.TempDir()
	for _, name := range []string{"top.tf", filepath.Join("alpha", "a.tf"), filepath.Join("alpha", "nested", "n.tf"), filepath.Join("middle", "m.tf")} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(tmpDir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, tmpDir, name, "# test")
	}
	writeTestFile(t, tmpDir, "notes.md", "# test")
	pattern := filepath.Join(tmpDir, "**", "*.tf")
	flags := &cliFlags{pattern: pattern, output: "text"}
	want := []string{filepath.Join(tmpDir, "alpha", "a.tf"), filepath.Join(tmpDir, "alpha", "nested", "n.tf"), filepath.Join(tmpDir, "middle", "m.tf"), filepath.Join(tmpDir, "top.tf")}
	var got []string
	stdout, diagnostics, err := captureRunnerOutput(t, func() error { got = findMatchingFiles(flags); return nil })
	if err != nil || diagnostics != "" {
		t.Fatalf("selection error=%v diagnostics=%q", err, diagnostics)
	}
	if stdout != "Found 4 file(s) matching pattern '"+pattern+"'\n" {
		t.Errorf("stdout = %q", stdout)
	}
	if !slices.Equal(got, want) {
		t.Errorf("files = %v, want %v", got, want)
	}
}

func TestFindMatchingFilesHiddenPathPolicy(t *testing.T) {
	tmpDir := t.TempDir()
	rootHidden := writeTestFile(t, tmpDir, ".generated.tf", "# test")
	ordinary := writeTestFile(t, tmpDir, "main.tf", "# test")
	for _, dir := range []string{filepath.Join(tmpDir, ".terraform", "modules"), filepath.Join(tmpDir, ".git", "hooks"), filepath.Join(tmpDir, "nested", ".terraform")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	vendored := writeTestFile(t, filepath.Join(tmpDir, ".terraform", "modules"), "vpc.tf", "# test")
	writeTestFile(t, filepath.Join(tmpDir, ".git", "hooks"), "hook.tf", "# test")
	nested := writeTestFile(t, filepath.Join(tmpDir, "nested", ".terraform"), "deep.tf", "# test")
	_ = nested
	flags := &cliFlags{pattern: filepath.Join(tmpDir, "**", "*.tf"), output: "text"}
	var got []string
	stdout, diagnostics, err := captureRunnerOutput(t, func() error { got = findMatchingFiles(flags); return nil })
	if err != nil || diagnostics != "" || stdout != "Found 2 file(s) matching pattern '"+flags.pattern+"'\n" || !slices.Equal(got, []string{rootHidden, ordinary}) {
		t.Errorf("wildcard got=%v stdout=%q diagnostics=%q", got, stdout, diagnostics)
	}
	flags.pattern = filepath.Join(tmpDir, ".terraform", "**", "*.tf")
	stdout, diagnostics, err = captureRunnerOutput(t, func() error { got = findMatchingFiles(flags); return nil })
	if err != nil || diagnostics != "" || stdout != "Found 1 file(s) matching pattern '"+flags.pattern+"'\n" || !slices.Equal(got, []string{vendored}) {
		t.Errorf("explicit hidden got=%v stdout=%q diagnostics=%q", got, stdout, diagnostics)
	}
}

func TestFindMatchingFilesExcludesSymlinksAndDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	realDir := filepath.Join(tmpDir, "modules")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	realFile := writeTestFile(t, realDir, "main.tf", "# test")
	directoryLink := filepath.Join(tmpDir, "modules-link")
	if err := os.Symlink(realDir, directoryLink); err != nil {
		t.Fatal(err)
	}
	fileLink := filepath.Join(tmpDir, "main-link.tf")
	if err := os.Symlink(realFile, fileLink); err != nil {
		t.Fatal(err)
	}
	flags := &cliFlags{pattern: filepath.Join(tmpDir, "**"), output: "text"}
	var got []string
	stdout, diagnostics, err := captureRunnerOutput(t, func() error { got = findMatchingFiles(flags); return nil })
	if err != nil || diagnostics != "" || stdout != "Found 2 file(s) matching pattern '"+flags.pattern+"'\n" {
		t.Fatalf("got=%v stdout=%q diagnostics=%q", got, stdout, diagnostics)
	}
	want := []string{fileLink, realFile}
	if !slices.Equal(got, want) {
		t.Errorf("files = %v, want %v", got, want)
	}
}

func TestFindMatchingFilesRejectsInvalidSelection(t *testing.T) {
	tests := []struct{ name, pattern, want string }{
		{name: "missing pattern", want: "Error: -pattern flag is required\n"},
		{name: "invalid glob", pattern: "[", want: "Error matching pattern: syntax error in pattern\n"},
		{name: "no matches", pattern: filepath.Join("/tmp", "no-such-tf-version-bump-file-*.tf"), want: "No files matched pattern: /tmp/no-such-tf-version-bump-file-*.tf\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restore, _ := stubExit(t)
			defer restore()
			flags := &cliFlags{pattern: tt.pattern, output: "text"}
			logs := captureLog(t, func() { requireExitCall(t, 1, func() { findMatchingFiles(flags) }) })
			if logs != tt.want {
				t.Errorf("diagnostics = %q, want %q", logs, tt.want)
			}
		})
	}
}

func TestFindMatchingFilesReportsDryRun(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestFile(t, tmpDir, "main.tf", "# test")
	pattern := filepath.Join(tmpDir, "*.tf")
	flags := &cliFlags{pattern: pattern, output: "text", dryRun: true}
	stdout, diagnostics, err := captureRunnerOutput(t, func() error { findMatchingFiles(flags); return nil })
	want := "Found 1 file(s) matching pattern '" + pattern + "'\n" + "Running in dry-run mode - no files will be modified\n"
	if err != nil || diagnostics != "" || stdout != want {
		t.Errorf("stdout=%q diagnostics=%q err=%v, want stdout=%q", stdout, diagnostics, err, want)
	}
}
