package main

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestParseFlagsContract(t *testing.T) {
	args := []string{"tf-version-bump", "-pattern", "**/*.tf", "-module", "example/module", "-to", "2.0.0", "-from", "1.0.0", "-from", "1.5.0", "-ignore-version", "3.0.0", "-ignore-modules", "vpc, legacy-*", "-config", "config.yml", "-force-add", "-dry-run", "-verbose", "-version", "-output", "md", "-terraform-version", ">= 1.5", "-provider", "aws"}
	withFlagArgs(t, args, func() {
		got := parseFlags()
		want := &cliFlags{pattern: "**/*.tf", moduleSource: "example/module", toVersion: "2.0.0", fromVersions: stringSliceFlag{"1.0.0", "1.5.0"}, ignoreVersions: stringSliceFlag{"3.0.0"}, ignoreModules: "vpc, legacy-*", configFile: "config.yml", forceAdd: true, dryRun: true, verbose: true, showVersion: true, output: "md", terraformVersion: ">= 1.5", providerName: "aws"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("flags = %#v, want %#v", got, want)
		}
	})
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
		withFlagArgs(t, []string{"tf-version-bump", "-output", "invalid"}, func() { requireExitCall(t, func() { parseFlags() }) })
	})
	if got != "Error: Invalid output format 'invalid'. Must be 'text' or 'md'\n" {
		t.Fatalf("diagnostic: %q", got)
	}
}

func TestValidateOperationModesContract(t *testing.T) {
	tests := []struct {
		name  string
		flags *cliFlags
		want  string
	}{
		{"config mixed", &cliFlags{configFile: "x", moduleSource: "m"}, "Error: Cannot use -config with other operation flags (-module, -to, -terraform-version, -provider, -from, -ignore-version, -ignore-modules)\n"},
		{"no operation", &cliFlags{}, "Usage:\n"},
		{"multiple operations", &cliFlags{moduleSource: "m", terraformVersion: "x"}, "Error: Cannot use -module, -terraform-version, and -provider flags together. Choose one operation mode or use a config file.\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restore, _ := stubExit(t)
			t.Cleanup(restore)
			var out, diagnostic string
			if tt.name == "no operation" {
				out = captureStdout(t, func() {
					withFlagArgs(t, []string{"tf-version-bump"}, func() { requireExitCall(t, func() { validateOperationModes(tt.flags) }) })
				})
			} else {
				diagnostic = captureLog(t, func() { requireExitCall(t, func() { validateOperationModes(tt.flags) }) })
			}
			if tt.name == "no operation" && !strings.HasPrefix(out, tt.want) {
				t.Fatalf("output %q", out)
			}
			if tt.name != "no operation" && diagnostic != tt.want {
				t.Fatalf("diagnostic %q", diagnostic)
			}
		})
	}
}

func TestLoadModuleUpdatesRequiresFlags(t *testing.T) {
	restore, _ := stubExit(t)
	t.Cleanup(restore)
	out := captureStdout(t, func() {
		withFlagArgs(t, []string{"tf-version-bump"}, func() { requireExitCall(t, func() { loadModuleUpdates(&cliFlags{}) }) })
	})
	if !strings.HasPrefix(out, "Usage:\n") {
		t.Fatalf("output %q", out)
	}
}

func TestRunCLIModeRequiresProviderVersion(t *testing.T) {
	restore, _ := stubExit(t)
	t.Cleanup(restore)
	diag := captureLog(t, func() { requireExitCall(t, func() { _ = runCLIMode(nil, &cliFlags{providerName: "aws"}) }) })
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

func TestCommandDryRunOutputContract(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		args           []string
		output         string
		selectionQuote string
		wantOperation  func(string) string
		wantSummary    string
	}{
		{
			name:  "module with from",
			input: "module \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n",
			args:  []string{"-module", "example/module", "-to", "2.0.0", "-from", "1.0.0"},
			wantOperation: func(file string) string {
				return "→ Would update module source 'example/module' from version(s) [1.0.0] to '2.0.0' in " + file + "\n"
			},
			wantSummary: "Dry run: would update 1 file(s)\n",
		},
		{
			name:           "Terraform",
			input:          "terraform {\n  required_version = \">= 1.0\"\n}\n",
			args:           []string{"-terraform-version", ">= 1.5"},
			output:         "md",
			selectionQuote: "`",
			wantOperation: func(file string) string {
				return "→ Would update Terraform required_version to `>= 1.5` in " + file + "\n"
			},
			wantSummary: "Dry run: would update Terraform version in 1 file(s)\n",
		},
		{
			name:           "provider",
			input:          "terraform {\n  required_providers {\n    aws = {\n      source  = \"hashicorp/aws\"\n      version = \"~> 4.0\"\n    }\n  }\n}\n",
			args:           []string{"-provider", "aws", "-to", "~> 5.0"},
			output:         "md",
			selectionQuote: "`",
			wantOperation: func(file string) string {
				return "→ Would update provider `aws` to version `~> 5.0` in " + file + "\n"
			},
			wantSummary: "Dry run: would update `aws` provider version in 1 file(s)\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := writeTestFile(t, t.TempDir(), "main.tf", tt.input)
			args := append([]string{"tf-version-bump", "-pattern", file, "-dry-run"}, tt.args...)
			if tt.output != "" {
				args = append(args, "-output", tt.output)
			}
			result := runMainCommand(t, args)
			selectionQuote := tt.selectionQuote
			if selectionQuote == "" {
				selectionQuote = "'"
			}
			wantStdout := "Found 1 file(s) matching pattern " + selectionQuote + file + selectionQuote + "\n" +
				"Running in dry-run mode - no files will be modified\n" +
				tt.wantOperation(file) + "\n" + tt.wantSummary

			if result.stdout != wantStdout {
				t.Errorf("stdout = %q, want %q", result.stdout, wantStdout)
			}
			if result.diagnostics != "" {
				t.Errorf("diagnostics = %q, want empty", result.diagnostics)
			}
			if result.exitCode != -1 {
				t.Errorf("exit code = %d, want normal return", result.exitCode)
			}
			if got := readTestFile(t, file); got != tt.input {
				t.Errorf("dry run content = %q, want %q", got, tt.input)
			}
		})
	}
}

func TestCommandConfigDryRunOutputContract(t *testing.T) {
	dir := t.TempDir()
	input := "terraform {\n  required_version = \">= 1.0\"\n  required_providers {\n    aws = {\n      source  = \"hashicorp/aws\"\n      version = \"~> 4.0\"\n    }\n  }\n}\nmodule \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n"
	file := writeTestFile(t, dir, "main.tf", input)
	config := writeTestFile(t, dir, "updates.yml", "terraform_version: \">= 1.5\"\nproviders:\n  - name: aws\n    version: \"~> 5.0\"\nmodules:\n  - source: example/module\n    version: 2.0.0\n")

	result := runMainCommand(t, []string{
		"tf-version-bump", "-pattern", file, "-config", config, "-dry-run", "-output", "md",
	})
	wantStdout := "Found 1 file(s) matching pattern `" + file + "`\n" +
		"Running in dry-run mode - no files will be modified\n" +
		"→ Would update Terraform required_version to `>= 1.5` in " + file + "\n" +
		"→ Would update provider `aws` to version `~> 5.0` in " + file + "\n" +
		"→ Would update module source `example/module` to version `2.0.0` in " + file + "\n\n" +
		"==================================================\n" +
		"Config File Update Summary\n" +
		"==================================================\n" +
		"Terraform version: 1 file(s) updated\n" +
		"Providers: 1 update(s) applied\n" +
		"Modules: 1 file(s) updated\n"

	if result.stdout != wantStdout {
		t.Errorf("stdout = %q, want %q", result.stdout, wantStdout)
	}
	if result.diagnostics != "" {
		t.Errorf("diagnostics = %q, want empty", result.diagnostics)
	}
	if result.exitCode != -1 {
		t.Errorf("exit code = %d, want normal return", result.exitCode)
	}
	if got := readTestFile(t, file); got != input {
		t.Errorf("config dry run content = %q, want %q", got, input)
	}
}

func TestCommandReportsAggregateFileFailure(t *testing.T) {
	for _, mode := range []string{"CLI", "config"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			bad := writeTestFile(t, dir, "01.tf", "!!!\n")
			good := writeTestFile(t, dir, "02.tf", "module \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n")
			args := []string{"tf-version-bump", "-pattern", dir + "/*.tf", "-module", "example/module", "-to", "2.0.0"}
			if mode == "config" {
				cfg := writeTestFile(t, dir, "updates.yml", "modules:\n  - source: example/module\n    version: 2.0.0\n")
				args = []string{"tf-version-bump", "-pattern", dir + "/*.tf", "-config", cfg}
			}
			r := runMainCommand(t, args)
			wantStdout := "Found 2 file(s) matching pattern '" + dir + "/*.tf'\n✓ Updated module source 'example/module' to version '2.0.0' in " + good + "\n\n"
			if mode == "CLI" {
				wantStdout += "Successfully updated 1 file(s)\n"
			} else {
				wantStdout += "==================================================\nConfig File Update Summary\n==================================================\nModules: 1 file(s) updated\n"
			}
			wantDiag := "Error processing " + bad + ": failed to parse HCL: " + bad + ":1,1-2: Argument or block definition required; An argument or block definition is required here.\n1 module update error(s)\n"
			wantHCL := "module \"example\" {\n  source  = \"example/module\"\n  version = \"2.0.0\"\n}\n"
			if r.stdout != wantStdout || r.diagnostics != wantDiag || r.exitCode != 1 || readTestFile(t, good) != wantHCL {
				t.Fatalf("result %#v content=%q", r, readTestFile(t, good))
			}
		})
	}
}
