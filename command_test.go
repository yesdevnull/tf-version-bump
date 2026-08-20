package main

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestStringSliceFlagContract(t *testing.T) {
	var got stringSliceFlag
	if err := got.Set("3.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := got.Set("~> 3.0"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual([]string(got), []string{"3.0.0", "~> 3.0"}) || got.String() != "3.0.0,~> 3.0" {
		t.Fatalf("unexpected flag: %#v %q", got, got.String())
	}
}

func TestParseFlagsContract(t *testing.T) {
	args := []string{"tf-version-bump", "-pattern", "**/*.tf", "-module", "example/module", "-to", "2.0.0", "-from", "1.0.0", "-from", "1.5.0", "-ignore-version", "3.0.0", "-ignore-modules", "vpc, legacy-*", "-config", "", "-force-add", "-dry-run", "-verbose", "-output", "md"}
	withFlagArgs(t, args, func() {
		got := parseFlags()
		want := &cliFlags{pattern: "**/*.tf", moduleSource: "example/module", toVersion: "2.0.0", fromVersions: stringSliceFlag{"1.0.0", "1.5.0"}, ignoreVersions: stringSliceFlag{"3.0.0"}, ignoreModules: "vpc, legacy-*", forceAdd: true, dryRun: true, verbose: true, output: "md"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("flags = %#v, want %#v", got, want)
		}
	})
}

func TestQuoteContract(t *testing.T) {
	if got := quote("example/module", "text"); got != "'example/module'" {
		t.Fatal(got)
	}
	if got := quote("example/module", "md"); got != "`example/module`" {
		t.Fatal(got)
	}
}

func TestLoadModuleUpdatesContract(t *testing.T) {
	flags := &cliFlags{pattern: "*.tf", moduleSource: "example/module", toVersion: "2.0.0", fromVersions: stringSliceFlag{"1.0.0", "1.5.0"}, ignoreVersions: stringSliceFlag{"3.0.0", "~> 3.0"}, ignoreModules: "vpc, legacy-*"}
	got := loadModuleUpdates(flags)
	want := []ModuleUpdate{{Source: "example/module", Version: "2.0.0", From: FromVersions{"1.0.0", "1.5.0"}, IgnoreVersions: FromVersions{"3.0.0", "~> 3.0"}, IgnoreModules: []string{"vpc", "legacy-*"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("updates = %#v, want %#v", got, want)
	}
}

func TestParseFlagsRejectsInvalidOutput(t *testing.T) {
	restore, _ := stubExit(t)
	t.Cleanup(restore)
	got := captureLog(t, func() {
		withFlagArgs(t, []string{"tf-version-bump", "-output", "invalid"}, func() { requireExitCall(t, 1, func() { parseFlags() }) })
	})
	if got != "Error: Invalid output format 'invalid'. Must be 'text' or 'md'\n" {
		t.Fatalf("diagnostic: %q", got)
	}
}

func TestValidateOperationModesContract(t *testing.T) {
	for _, flags := range []*cliFlags{{configFile: "config.yml"}, {moduleSource: "source"}, {terraformVersion: ">= 1.5"}, {providerName: "aws"}} {
		validateOperationModes(flags)
	}
	tests := []struct {
		name  string
		flags *cliFlags
		want  string
	}{
		{"config mixed", &cliFlags{configFile: "x", moduleSource: "m"}, "Error: Cannot use -config with other operation flags"},
		{"no operation", &cliFlags{}, "Usage:\n"},
		{"multiple operations", &cliFlags{moduleSource: "m", terraformVersion: "x"}, "Error: Cannot use -module, -terraform-version, and -provider flags together."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restore, code := stubExit(t)
			t.Cleanup(restore)
			var out, diagnostic string
			if tt.name == "no operation" {
				out = captureStdout(t, func() {
					withFlagArgs(t, []string{"tf-version-bump"}, func() { requireExitCall(t, 1, func() { validateOperationModes(tt.flags) }) })
				})
			} else {
				diagnostic = captureLog(t, func() { requireExitCall(t, 1, func() { validateOperationModes(tt.flags) }) })
			}
			if *code != 1 {
				t.Fatal(*code)
			}
			if tt.name == "no operation" && !strings.HasPrefix(out, tt.want) {
				t.Fatalf("output %q", out)
			}
			if tt.name != "no operation" && !strings.HasPrefix(diagnostic, tt.want) {
				t.Fatalf("diagnostic %q", diagnostic)
			}
			if tt.name != "no operation" && diagnostic == "" {
				t.Fatal("missing diagnostic")
			}
		})
	}
}

func TestLoadModuleUpdatesRequiresFlags(t *testing.T) {
	restore, _ := stubExit(t)
	t.Cleanup(restore)
	out := captureStdout(t, func() {
		withFlagArgs(t, []string{"tf-version-bump"}, func() { requireExitCall(t, 1, func() { loadModuleUpdates(&cliFlags{}) }) })
	})
	if !strings.HasPrefix(out, "Usage:\n") {
		t.Fatalf("output %q", out)
	}
}

func TestRunCLIModeRequiresProviderVersion(t *testing.T) {
	restore, _ := stubExit(t)
	t.Cleanup(restore)
	diag := captureLog(t, func() { requireExitCall(t, 1, func() { _ = runCLIMode(nil, &cliFlags{providerName: "aws"}) }) })
	if diag != "Error: -to flag is required when using -provider\n" {
		t.Fatalf("diagnostics: %q", diag)
	}
}

func TestCommandVersion(t *testing.T) {
	oldV, oldC, oldD := version, commit, date
	t.Cleanup(func() { version, commit, date = oldV, oldC, oldD })
	version, commit, date = "1.2.3", "abc123", "2026-08-20"
	result := runMainCommand(t, []string{"tf-version-bump", "-version"})
	if result.stdout != "tf-version-bump 1.2.3\n  commit: abc123\n  built:  2026-08-20\n" || result.diagnostics != "" || result.exitCode != 0 {
		t.Fatalf("result %#v", result)
	}
}

func TestRunConfigFileModeReturnsLoadErrorContract(t *testing.T) {
	err := runConfigFileMode(nil, &cliFlags{configFile: "does-not-exist"})
	if err == nil || !errors.Is(err, os.ErrNotExist) || !strings.HasPrefix(err.Error(), "Error loading config file:") {
		t.Fatalf("error = %v", err)
	}
}

func TestCommandNoMatchingModuleIsSuccess(t *testing.T) {
	dir := t.TempDir()
	file := writeTestFile(t, dir, "main.tf", "module \"x\" {\n  source = \"other/module\"\n  version = \"1.0.0\"\n}\n")
	result := runMainCommand(t, []string{"tf-version-bump", "-pattern", file, "-module", "example/module", "-to", "2.0.0"})
	if result.stdout != "Found 1 file(s) matching pattern '"+file+"'\n\nSuccessfully updated 0 file(s)\n" || result.diagnostics != "" || result.exitCode != -1 || readTestFile(t, file) != "module \"x\" {\n  source = \"other/module\"\n  version = \"1.0.0\"\n}\n" {
		t.Fatalf("result %#v content %q", result, readTestFile(t, file))
	}
}

func TestRunCLIModeMarkdownOutput(t *testing.T) {
	dir := t.TempDir()
	file := writeTestFile(t, dir, "main.tf", "module \"x\" {\n  source = \"example/module\"\n  version = \"1.0.0\"\n}\n")
	r := runMainCommand(t, []string{"tf-version-bump", "-pattern", file, "-module", "example/module", "-to", "2.0.0", "-output", "md"})
	want := "Found 1 file(s) matching pattern `" + file + "`\n✓ Updated module source `example/module` to version `2.0.0` in " + file + "\n\nSuccessfully updated 1 file(s)\n"
	if r.stdout != want || r.diagnostics != "" || r.exitCode != -1 {
		t.Fatalf("result %#v", r)
	}
}

func TestCommandReportsAggregateFileFailure(t *testing.T) {
	for _, mode := range []string{"CLI", "config"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			bad := writeTestFile(t, dir, "01.tf", "!!!\n")
			good := writeTestFile(t, dir, "02.tf", "module \"x\" {\n  source = \"example/module\"\n  version = \"1.0.0\"\n}\n")
			args := []string{"tf-version-bump", "-pattern", dir + "/*.tf", "-module", "example/module", "-to", "2.0.0"}
			if mode == "config" {
				cfg := writeTestFile(t, dir, "updates.yml", "modules:\n  - source: example/module\n    version: 2.0.0\n")
				args = []string{"tf-version-bump", "-pattern", dir + "/*.tf", "-config", cfg}
			}
			r := runMainCommand(t, args)
			if r.exitCode != 1 || !strings.Contains(r.diagnostics, bad) || !strings.Contains(r.diagnostics, "1 module update error(s)\n") || !strings.Contains(r.stdout, good) || !strings.Contains(readTestFile(t, good), `version = "2.0.0"`) {
				t.Fatalf("result %#v", r)
			}
		})
	}
}
