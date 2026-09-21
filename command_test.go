package main

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseFlagsContract(t *testing.T) {
	args := []string{"tf-version-bump", "-pattern", "**/*.tf", "-module", "example/module", "-to", "2.0.0", "-from", "1.0.0", "-from", "1.5.0", "-ignore-version", "3.0.0", "-ignore-modules", "vpc, legacy-*", "-branch", " main ", "-config", "config.yml", "-validate-config", "validate.yml", "-force-add", "-dry-run", "-check", "-verbose", "-version", "-output", "md", "-terraform-version", ">= 1.5", "-provider", "aws", "-audit-file", "audit.json"}
	withFlagArgs(t, args, func() {
		got := parseFlags()
		want := &cliFlags{pattern: "**/*.tf", moduleSource: "example/module", toVersion: "2.0.0", fromVersions: stringSliceFlag{"1.0.0", "1.5.0"}, ignoreVersions: stringSliceFlag{"3.0.0"}, ignoreModules: "vpc, legacy-*", branch: "main", configFile: "config.yml", validationConfigFile: "validate.yml", forceAdd: true, dryRun: true, check: true, verbose: true, showVersion: true, output: "md", terraformVersion: ">= 1.5", providerName: "aws", auditFile: "audit.json"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("flags = %#v, want %#v", got, want)
		}
	})
}

func TestLoadModuleUpdatesContract(t *testing.T) {
	flags := &cliFlags{pattern: "*.tf", moduleSource: "example/module", toVersion: "2.0.0", fromVersions: stringSliceFlag{"1.0.0", "1.5.0"}, ignoreVersions: stringSliceFlag{"3.0.0", "~> 3.0"}, ignoreModules: "vpc, legacy-*"}
	got := loadModuleUpdates(flags)
	want := []ModuleUpdate{{Source: "example/module", Version: "2.0.0", From: FromVersions{"1.0.0", "1.5.0"}, IgnoreVersions: FromVersions{"3.0.0", "~> 3.0"}, IgnoreModules: []string{"vpc", "legacy-*"}, resolvedIgnoreModules: []string{"vpc", "legacy-*"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("updates = %#v, want %#v", got, want)
	}
}

func TestLoadModuleUpdatesResolvesBranchScopedIgnorePatterns(t *testing.T) {
	flags := &cliFlags{pattern: "*.tf", moduleSource: "example/module", toVersion: "2.0.0", ignoreModules: "legacy-*,release/*/vpc,main/database", branch: "release/2026-09"}
	got := loadModuleUpdates(flags)
	want := []ModuleUpdate{{
		Source: "example/module", Version: "2.0.0",
		IgnoreModules:         []string{"legacy-*", "release/*/vpc", "main/database"},
		resolvedIgnoreModules: []string{"legacy-*", "vpc"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("updates = %#v, want %#v", got, want)
	}
}

func TestParseFlagsRejectsDetachedHeadBranch(t *testing.T) {
	restore, _ := stubExit(t)
	t.Cleanup(restore)
	got := captureLog(t, func() {
		withFlagArgs(t, []string{"tf-version-bump", "-branch", " HEAD "}, func() { requireExitCall(t, func() { parseFlags() }) })
	})
	want := "Error: -branch must be a branch name, not 'HEAD'. Use 'git branch --show-current', which is empty on a detached checkout\n"
	if got != want {
		t.Fatalf("diagnostic = %q, want %q", got, want)
	}
}

func TestCommandRejectsRefBranch(t *testing.T) {
	dir := t.TempDir()
	moduleFile := writeTestFile(t, dir, "main.tf", "module \"vpc\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n")

	result := runMainCommand(t, []string{"tf-version-bump", "-pattern", moduleFile, "-module", "example/module", "-to", "2.0.0", "-branch", "refs/heads/main"})

	want := "Error: -branch must be a branch name, not the ref 'refs/heads/main'. Use 'git branch --show-current' rather than a full ref such as GITHUB_REF\n"
	if result.exitCode != 1 || result.diagnostics != want {
		t.Fatalf("result = %#v, want diagnostic %q and exit 1", result, want)
	}
}

func TestCommandRejectsMalformedIgnoreModulesEntry(t *testing.T) {
	dir := t.TempDir()
	moduleFile := writeTestFile(t, dir, "main.tf", "module \"vpc\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n")

	result := runMainCommand(t, []string{"tf-version-bump", "-pattern", moduleFile, "-module", "example/module", "-to", "2.0.0", "-ignore-modules", "main/"})

	want := "Error: 'main/' must be '<branch>/<module>' with no empty '/'-separated part\n"
	if result.exitCode != 1 || result.diagnostics != want {
		t.Fatalf("result = %#v, want diagnostic %q and exit 1", result, want)
	}
	if !strings.Contains(readTestFile(t, moduleFile), "version = \"1.0.0\"") {
		t.Fatalf("content = %q, want the file left unchanged", readTestFile(t, moduleFile))
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

func TestParseFlagsPreservesConfiguredUsageOutput(t *testing.T) {
	originalArgs := os.Args
	originalFlagSet := flag.CommandLine
	t.Cleanup(func() {
		os.Args = originalArgs
		flag.CommandLine = originalFlagSet
	})

	var usage bytes.Buffer
	configuredFlagSet := flag.NewFlagSet("tf-version-bump", flag.ContinueOnError)
	configuredFlagSet.SetOutput(&usage)
	flag.CommandLine = configuredFlagSet
	os.Args = []string{"tf-version-bump", "-version"}

	_ = parseFlags()
	flag.PrintDefaults()

	if !strings.Contains(usage.String(), "-check") {
		t.Fatalf("usage output = %q, want registered flags on the configured writer", usage.String())
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
	diag := captureLog(t, func() { requireExitCall(t, func() { _, _ = runCLIMode(nil, &cliFlags{providerName: "aws"}) }) })
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

func TestCommandValidatesConfigWithoutTerraformFiles(t *testing.T) {
	config := writeTestFile(t, t.TempDir(), "versions.yml", "providers:\n  - name: aws\n    version: '~> 5.0'\n")
	tests := []struct {
		name string
		args []string
	}{
		{name: "default output"},
		{name: "Markdown output", args: []string{"-output", "md"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"tf-version-bump", "-validate-config", config}, tt.args...)
			result := runMainCommand(t, args)

			want := "Config '" + config + "' is valid\n"
			if result.stdout != want || result.diagnostics != "" || result.exitCode != 0 {
				t.Fatalf("result = %#v, want stdout %q and exit 0", result, want)
			}
		})
	}
}

func TestCommandConfigValidationRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		wantDetail string
	}{
		{name: "malformed", content: "providers:\n  - name: [\n", wantDetail: "failed to parse YAML"},
		{name: "empty operations", content: "# no updates\n", wantDetail: "config contains no updates"},
		{name: "multiple documents", content: "terraform_version: '>= 1.5'\n---\nunknown: true\n", wantDetail: "multiple YAML documents are not supported"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := writeTestFile(t, t.TempDir(), "versions.yml", tt.content)
			result := runMainCommand(t, []string{"tf-version-bump", "-validate-config", config})

			if result.stdout != "" || result.exitCode != 1 || !strings.Contains(result.diagnostics, "Error validating config file: "+tt.wantDetail) {
				t.Fatalf("result = %#v, want validation error containing %q", result, tt.wantDetail)
			}
		})
	}
}

func TestCommandConfigValidationRejectsUpdateAndReportFlags(t *testing.T) {
	config := writeTestFile(t, t.TempDir(), "versions.yml", "terraform_version: '>= 1.5'\n")
	tests := []struct {
		name string
		args []string
	}{
		{name: "pattern", args: []string{"-pattern", "*.tf"}},
		{name: "config update", args: []string{"-config", config}},
		{name: "module", args: []string{"-module", "example/module"}},
		{name: "target version", args: []string{"-to", "2.0.0"}},
		{name: "source version", args: []string{"-from", "1.0.0"}},
		{name: "ignored version", args: []string{"-ignore-version", "1.0.0"}},
		{name: "ignored module", args: []string{"-ignore-modules", "legacy-*"}},
		{name: "branch", args: []string{"-branch", "main"}},
		{name: "Terraform version", args: []string{"-terraform-version", ">= 1.5"}},
		{name: "provider", args: []string{"-provider", "aws"}},
		{name: "force add", args: []string{"-force-add"}},
		{name: "dry run", args: []string{"-dry-run"}},
		{name: "check", args: []string{"-check"}},
		{name: "verbose", args: []string{"-verbose"}},
		{name: "report", args: []string{"-report-file", "report.json"}},
		{name: "audit", args: []string{"-audit-file", "audit.json"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"tf-version-bump", "-validate-config", config}, tt.args...)
			result := runMainCommand(t, args)

			want := "Error: Cannot use -validate-config with update or report flags\n"
			if result.stdout != "" || result.diagnostics != want || result.exitCode != 1 {
				t.Fatalf("result = %#v, want diagnostic %q and exit 1", result, want)
			}
		})
	}
}

func TestCommandCheckProposesModuleUpdateWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	input := "module \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n"
	file := writeTestFile(t, dir, "main.tf", input)

	result := runMainCommand(t, []string{
		"tf-version-bump", "-pattern", file, "-module", "example/module", "-to", "2.0.0", "-check",
	})

	wantStdout := "Found 1 file(s) matching pattern '" + file + "'\n" +
		"Running in check mode - no files will be modified\n" +
		"→ Would update module source 'example/module' to version '2.0.0' in " + file + "\n\n" +
		"Dry run: would update 1 file(s)\n"
	if result.stdout != wantStdout || result.diagnostics != "" || result.exitCode != 2 {
		t.Fatalf("result = %#v, want stdout %q and exit 2", result, wantStdout)
	}
	if got := readTestFile(t, file); got != input {
		t.Fatalf("check changed content to %q, want %q", got, input)
	}
}

func TestCommandCheckConfigReportsAllUpdateModesWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	input := `terraform {
  required_version = ">= 1.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}
module "example" {
  source  = "example/module"
  version = "1.0.0"
}
`
	file := writeTestFile(t, dir, "main.tf", input)
	config := writeTestFile(t, dir, "versions.yml", `terraform_version: ">= 1.5"
providers:
  - name: aws
    version: "~> 5.0"
modules:
  - source: example/module
    version: 2.0.0
`)

	result := runMainCommand(t, []string{
		"tf-version-bump", "-pattern", file, "-config", config, "-check",
	})

	for _, fragment := range []string{
		"Running in check mode - no files will be modified\n",
		"→ Would update Terraform required_version to '>= 1.5'",
		"→ Would update provider 'aws' to version '~> 5.0'",
		"→ Would update module source 'example/module' to version '2.0.0'",
	} {
		if !strings.Contains(result.stdout, fragment) {
			t.Errorf("stdout %q does not contain %q", result.stdout, fragment)
		}
	}
	if result.diagnostics != "" || result.exitCode != 2 {
		t.Fatalf("result = %#v, want no diagnostics and exit 2", result)
	}
	if got := readTestFile(t, file); got != input {
		t.Fatalf("check changed content to %q, want %q", got, input)
	}
}

func TestCommandCheckReturnsSuccessWhenCurrent(t *testing.T) {
	dir := t.TempDir()
	input := "module \"example\" {\n  source  = \"example/module\"\n  version = \"2.0.0\"\n}\n"
	file := writeTestFile(t, dir, "main.tf", input)

	result := runMainCommand(t, []string{
		"tf-version-bump", "-pattern", file, "-module", "example/module", "-to", "2.0.0", "-check",
	})

	wantStdout := "Found 1 file(s) matching pattern '" + file + "'\n" +
		"Running in check mode - no files will be modified\n\n" +
		"Dry run: would update 0 file(s)\n"
	if result.stdout != wantStdout || result.diagnostics != "" || result.exitCode != -1 {
		t.Fatalf("result = %#v, want stdout %q and normal return", result, wantStdout)
	}
	if got := readTestFile(t, file); got != input {
		t.Fatalf("check changed content to %q, want %q", got, input)
	}
}

func TestCommandCheckProcessingErrorWinsOverUpdatesRequired(t *testing.T) {
	dir := t.TempDir()
	bad := writeTestFile(t, dir, "bad.tf", "module {")
	goodInput := "module \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n"
	good := writeTestFile(t, dir, "good.tf", goodInput)

	result := runMainCommand(t, []string{
		"tf-version-bump", "-pattern", dir + "/*.tf", "-module", "example/module", "-to", "2.0.0", "-check",
	})

	// The summary reports the failures alongside the updates the run would make, so a check whose
	// answer is incomplete never reads as a clean "would update 1 file".
	wantStdout := "Found 2 file(s) matching pattern '" + dir + "/*.tf'\n" +
		"Running in check mode - no files will be modified\n" +
		"→ Would update module source 'example/module' to version '2.0.0' in " + good + "\n\n" +
		"Dry run: would update 1 file(s)\n1 update(s) failed; see the errors on stderr\n"
	if result.exitCode != 1 || result.stdout != wantStdout || !strings.Contains(result.diagnostics, "Error processing "+bad) || !strings.Contains(result.diagnostics, "Error: 1 module update error(s)") {
		t.Fatalf("result = %#v, want stdout %q, processing diagnostics and exit 1", result, wantStdout)
	}
	if got := readTestFile(t, good); got != goodInput {
		t.Fatalf("check changed valid content to %q, want %q", got, goodInput)
	}
}

func TestCommandCheckRejectsConflictingFlags(t *testing.T) {
	dir := t.TempDir()
	file := writeTestFile(t, dir, "main.tf", "module \"example\" {\n  source = \"example/module\"\n}\n")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "dry run", args: []string{"-dry-run"}, want: "Error: Cannot use -check with -dry-run\n"},
		{name: "report", args: []string{"-report-file", dir + "/report.json"}, want: "Error: Cannot use -check with -report-file\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := []string{"tf-version-bump", "-pattern", file, "-module", "example/module", "-to", "2.0.0", "-check"}
			result := runMainCommand(t, append(args, tt.args...))
			if result.stdout != "" || result.diagnostics != tt.want || result.exitCode != 1 {
				t.Fatalf("result = %#v, want diagnostic %q and exit 1", result, tt.want)
			}
		})
	}
}

func TestCommandCheckRejectsInvalidArgumentsWithErrorStatus(t *testing.T) {
	dir := t.TempDir()
	file := writeTestFile(t, dir, "main.tf", "module \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "unknown flag",
			args: []string{"-check", "-module", "example/module", "-to", "2.0.0", "-bogus"},
			want: "Error: flag provided but not defined: -bogus\n",
		},
		{
			name: "missing flag value",
			args: []string{"-check", "-module", "example/module", "-to", "2.0.0", "-pattern"},
			want: "Error: flag needs an argument: -pattern\n",
		},
		{
			name: "positional argument",
			args: []string{"-check", "-pattern", file, "-module", "example/module", "-to", "2.0.0", "trailing"},
			want: "Error: unexpected positional argument(s): trailing\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runMainCommand(t, append([]string{"tf-version-bump"}, tt.args...))
			if result.stdout != "" || result.diagnostics != tt.want || result.exitCode != 1 {
				t.Fatalf("result = %#v, want diagnostic %q and exit 1", result, tt.want)
			}
		})
	}
}

func TestRunConfigFileModeReturnsLoadErrorContract(t *testing.T) {
	_, err := runConfigFileMode(nil, &cliFlags{configFile: "does-not-exist"})
	if err == nil || !errors.Is(err, os.ErrNotExist) || !strings.HasPrefix(err.Error(), "Error loading config file:") {
		t.Fatalf("error = %v", err)
	}
}

func TestCommandConfigBranchScopedIgnoreModules(t *testing.T) {
	const terraform = "module \"shared-vpc\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n"
	const config = "modules:\n  - source: example/module\n    version: 2.0.0\n    ignore_modules:\n      - state/staging/example-thing/shared-vpc\n"

	tests := []struct {
		name, branch, wantVersion string
	}{
		{name: "scoped branch is ignored", branch: "state/staging/example-thing", wantVersion: "1.0.0"},
		{name: "other branch is updated", branch: "state/nonproduction/example-thing", wantVersion: "2.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			file := writeTestFile(t, dir, "main.tf", terraform)
			configFile := writeTestFile(t, dir, "versions.yml", config)

			result := runMainCommand(t, []string{"tf-version-bump", "-pattern", file, "-config", configFile, "-branch", tt.branch})

			if result.diagnostics != "" || result.exitCode != -1 {
				t.Fatalf("result = %#v, want a successful run", result)
			}
			if !strings.Contains(readTestFile(t, file), "version = \""+tt.wantVersion+"\"") {
				t.Fatalf("content = %q, want version %q", readTestFile(t, file), tt.wantVersion)
			}
		})
	}
}

func TestCommandBranchScopedIgnoreRequiresBranch(t *testing.T) {
	tests := []struct {
		name string
		args func(moduleFile, configFile string) []string
	}{
		{name: "config mode", args: func(moduleFile, configFile string) []string {
			return []string{"-pattern", moduleFile, "-config", configFile}
		}},
		{name: "direct mode", args: func(moduleFile, _ string) []string {
			return []string{"-pattern", moduleFile, "-module", "example/module", "-to", "2.0.0", "-ignore-modules", "main/vpc"}
		}},
		{name: "empty branch from a detached checkout", args: func(moduleFile, configFile string) []string {
			return []string{"-pattern", moduleFile, "-config", configFile, "-branch", "   "}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			moduleFile := writeTestFile(t, dir, "main.tf", "module \"vpc\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n")
			configFile := writeTestFile(t, dir, "versions.yml", "modules:\n  - source: example/module\n    version: 2.0.0\n    ignore_modules:\n      - main/vpc\n")

			result := runMainCommand(t, append([]string{"tf-version-bump"}, tt.args(moduleFile, configFile)...))

			want := "Error: ignore pattern 'main/vpc' is scoped to a branch, but -branch is missing or empty; a detached checkout has no current branch\n"
			if result.exitCode != 1 || result.diagnostics != want {
				t.Fatalf("result = %#v, want diagnostic %q and exit 1", result, want)
			}
			if !strings.Contains(readTestFile(t, moduleFile), "version = \"1.0.0\"") {
				t.Fatalf("content = %q, want the file left unchanged", readTestFile(t, moduleFile))
			}
		})
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
		selectionQuote string
		wantOperation  func(string) string
		wantSummary    string
	}{
		{
			name:           "module with from",
			input:          "module \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n",
			args:           []string{"-module", "example/module", "-to", "2.0.0", "-from", "1.0.0"},
			selectionQuote: "'",
			wantOperation: func(file string) string {
				return "→ Would update module source 'example/module' from version(s) [1.0.0] to '2.0.0' in " + file + "\n"
			},
			wantSummary: "Dry run: would update 1 file(s)\n",
		},
		{
			name:           "Terraform",
			input:          "terraform {\n  required_version = \">= 1.0\"\n}\n",
			args:           []string{"-terraform-version", ">= 1.5", "-output", "md"},
			selectionQuote: "`",
			wantOperation: func(file string) string {
				return "→ Would update Terraform required_version to `>= 1.5` in " + file + "\n"
			},
			wantSummary: "Dry run: would update Terraform version in 1 file(s)\n",
		},
		{
			name:           "provider",
			input:          "terraform {\n  required_providers {\n    aws = {\n      source  = \"hashicorp/aws\"\n      version = \"~> 4.0\"\n    }\n  }\n}\n",
			args:           []string{"-provider", "aws", "-to", "~> 5.0", "-output", "md"},
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
			result := runMainCommand(t, args)
			wantStdout := "Found 1 file(s) matching pattern " + tt.selectionQuote + file + tt.selectionQuote + "\n" +
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
	report := dir + "/report.json"

	result := runMainCommand(t, []string{
		"tf-version-bump", "-pattern", file, "-config", config, "-dry-run", "-output", "md", "-report-file", report,
	})
	wantStdout := "Found 1 file(s) matching pattern `" + file + "`\n" +
		"Running in dry-run mode - no files will be modified\n" +
		"→ Would update Terraform required_version to `>= 1.5` in " + file + "\n" +
		"→ Would update provider `aws` to version `~> 5.0` in " + file + "\n" +
		"→ Would update module source `example/module` to version `2.0.0` in " + file + "\n\n" +
		"==================================================\n" +
		"Config File Update Summary\n" +
		"==================================================\n" +
		"Terraform version: would update 1 file(s)\n" +
		"Providers: would apply 1 update(s)\n" +
		"Modules: would apply 1 update(s)\n"

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
	wantReport := "{\n  \"schema_version\": 2,\n  \"terraform_blocks_updated\": 0,\n  \"module_blocks_updated\": 0,\n  \"provider_blocks_updated\": 0\n}\n"
	if got := readTestFile(t, report); got != wantReport {
		t.Errorf("dry-run report = %q, want %q", got, wantReport)
	}
}

// A run applies a source's config entries in order, so a dry run must report every step a real run
// takes, including an entry whose from names an earlier entry's target.
func TestCommandConfigDryRunChainsEntriesForOneSource(t *testing.T) {
	dir := t.TempDir()
	input := "module \"vpc\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n"
	file := writeTestFile(t, dir, "main.tf", input)
	config := writeTestFile(t, dir, "versions.yml", "modules:\n  - source: example/module\n    version: 2.0.0\n    from: 1.0.0\n  - source: example/module\n    version: 3.0.0\n    from: 2.0.0\n")

	result := runMainCommand(t, []string{"tf-version-bump", "-pattern", file, "-config", config, "-dry-run"})

	wantStdout := "Found 1 file(s) matching pattern '" + file + "'\n" +
		"Running in dry-run mode - no files will be modified\n" +
		"→ Would update module source 'example/module' from version(s) [1.0.0] to '2.0.0' in " + file + "\n" +
		"→ Would update module source 'example/module' from version(s) [2.0.0] to '3.0.0' in " + file + "\n\n" +
		"==================================================\n" +
		"Config File Update Summary\n" +
		"==================================================\n" +
		"Modules: would apply 2 update(s)\n"
	if result.stdout != wantStdout || result.diagnostics != "" || result.exitCode != -1 {
		t.Fatalf("result = %#v, want stdout %q and normal return", result, wantStdout)
	}
	if got := readTestFile(t, file); got != input {
		t.Fatalf("dry run changed content to %q, want %q", got, input)
	}
}

// A failed write leaves the file on disk as it was, so later entries start from that content rather
// than the unsaved change: the chained entry is judged against the version the file still holds, and
// an entry for another source is still applied. The third entry fails to write for the same reason,
// so abandoning the file after the first failure would report one error instead of two.
func TestCommandConfigFailedWriteRestartsLaterEntriesFromDisk(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write read-only files")
	}
	dir := t.TempDir()
	input := "module \"vpc\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n\nmodule \"bucket\" {\n  source  = \"other/module\"\n  version = \"1.0.0\"\n}\n"
	file := writeTestFile(t, dir, "main.tf", input)
	if err := os.Chmod(file, 0o400); err != nil {
		t.Fatal(err)
	}
	config := writeTestFile(t, dir, "versions.yml", "modules:\n  - source: example/module\n    version: 2.0.0\n    from: 1.0.0\n  - source: example/module\n    version: 3.0.0\n    from: 2.0.0\n  - source: other/module\n    version: 2.0.0\n    from: 1.0.0\n")

	result := runMainCommand(t, []string{"tf-version-bump", "-pattern", file, "-config", config})

	writeFailure := "Error processing " + file + ": failed to write file: open " + file + ": permission denied\n"
	wantDiagnostics := writeFailure + writeFailure + "Error: 2 module update error(s)\n"
	if result.diagnostics != wantDiagnostics || result.exitCode != 1 || strings.Contains(result.stdout, "✓") {
		t.Fatalf("result = %#v, want diagnostics %q, no success lines and exit 1", result, wantDiagnostics)
	}
	if got := readTestFile(t, file); got != input {
		t.Fatalf("content = %q, want the unwritable file unchanged", got)
	}
}

// A run whose updates all failed must say so. Reporting that the config may be empty or match
// nothing sends the operator to the config when the config was right and the files were not.
// Each kind of configured update is pinned because only a run that failed on modules alone
// names the modules in its error.
func TestCommandConfigEveryUpdateFailedReportsTheFailures(t *testing.T) {
	tests := []struct{ name, config, errText string }{
		{"terraform", "terraform_version: \">= 1.5\"\n", "Error: 1 update error(s)"},
		{"provider", "providers:\n  - name: aws\n    version: \"~> 5.0\"\n", "Error: 1 update error(s)"},
		{"module", "modules:\n  - source: example/module\n    version: 2.0.0\n", "Error: 1 module update error(s)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			file := writeTestFile(t, dir, "main.tf", `module "broken" {`)
			config := writeTestFile(t, dir, "versions.yml", tt.config)

			result := runMainCommand(t, []string{"tf-version-bump", "-pattern", file, "-config", config})

			wantStdout := "Found 1 file(s) matching pattern '" + file + "'\n\n1 update(s) failed; see the errors on stderr\n"
			wantDiagnostics := "Error processing " + file + ": failed to parse HCL: " + file + ":1,17-18: Unclosed configuration block; There is no closing brace for this block before the end of the file. This may be caused by incorrect brace nesting elsewhere in this file.\n" + tt.errText + "\n"
			if result.stdout != wantStdout || result.diagnostics != wantDiagnostics || result.exitCode != 1 {
				t.Fatalf("result = %#v, want stdout %q, diagnostics %q and exit 1", result, wantStdout, wantDiagnostics)
			}
		})
	}
}

// A module the config deliberately excludes was neither already applied nor unmatched, so the
// no-op message names skipping too rather than offering the operator two causes that are both
// untrue of the run in front of them.
func TestCommandConfigFilteredModuleIsNotReportedAsApplied(t *testing.T) {
	dir := t.TempDir()
	input := "module \"legacy-vpc\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n"
	file := writeTestFile(t, dir, "main.tf", input)
	config := writeTestFile(t, dir, "versions.yml", "modules:\n  - source: example/module\n    version: 2.0.0\n    ignore_modules:\n      - \"legacy-*\"\n")

	result := runMainCommand(t, []string{"tf-version-bump", "-pattern", file, "-config", config})

	wantStdout := "Found 1 file(s) matching pattern '" + file + "'\n\nNo updates were performed. Every configured update is already applied, skipped or matched nothing; use -audit-file to see which.\n"
	if result.stdout != wantStdout || result.diagnostics != "" || result.exitCode != -1 {
		t.Fatalf("result = %#v, want stdout %q and normal return", result, wantStdout)
	}
	if got := readTestFile(t, file); got != input {
		t.Errorf("content = %q, want unchanged %q", got, input)
	}
}

// A config that declares nothing is named as such, rather than left to share the wording of a
// config whose updates are all already applied. A config that parses to empty values says it
// just as one holding only comments does, so the predicate both readers share is pinned
// against a parsed config and not only against the degenerate file that never reaches it.
func TestCommandConfigDeclaringNoUpdatesSaysSo(t *testing.T) {
	tests := map[string]string{
		"comments only": "# nothing to update\n",
		"empty values":  "modules: []\nproviders: []\nterraform_version: \"  \"\n",
	}
	for name, config := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			input := "module \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n"
			file := writeTestFile(t, dir, "main.tf", input)
			configFile := writeTestFile(t, dir, "versions.yml", config)

			result := runMainCommand(t, []string{"tf-version-bump", "-pattern", file, "-config", configFile})

			wantStdout := "Found 1 file(s) matching pattern '" + file + "'\n\nNo updates were performed. The config declares no updates.\n"
			if result.stdout != wantStdout || result.diagnostics != "" || result.exitCode != -1 {
				t.Fatalf("result = %#v, want stdout %q and normal return", result, wantStdout)
			}
			if got := readTestFile(t, file); got != input {
				t.Errorf("content = %q, want unchanged %q", got, input)
			}
		})
	}
}

// Every direct mode reports its failures in place of a success line counting no files, which
// claims a success the run never had.
func TestCommandEveryUpdateFailedReportsTheFailures(t *testing.T) {
	tests := []struct {
		name, errText string
		operation     []string
	}{
		{"module", "Error: 1 module update error(s)", []string{"-module", "example/module", "-to", "2.0.0"}},
		{"terraform", "Error: 1 Terraform version update error(s)", []string{"-terraform-version", ">= 1.5"}},
		{"provider", "Error: 1 provider update error(s)", []string{"-provider", "aws", "-to", "~> 5.0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			file := writeTestFile(t, dir, "main.tf", `module "broken" {`)

			result := runMainCommand(t, append([]string{"tf-version-bump", "-pattern", file}, tt.operation...))

			wantStdout := "Found 1 file(s) matching pattern '" + file + "'\n\n1 update(s) failed; see the errors on stderr\n"
			wantDiagnostics := "Error processing " + file + ": failed to parse HCL: " + file + ":1,17-18: Unclosed configuration block; There is no closing brace for this block before the end of the file. This may be caused by incorrect brace nesting elsewhere in this file.\n" + tt.errText + "\n"
			if result.stdout != wantStdout || result.diagnostics != wantDiagnostics || result.exitCode != 1 {
				t.Fatalf("result = %#v, want stdout %q, diagnostics %q and exit 1", result, wantStdout, wantDiagnostics)
			}
		})
	}
}

// Once a write keeps its backup, the file's content cannot be trusted, so every later pass in the
// run refuses it rather than applying further changes to a possibly damaged file.
func TestCommandConfigRefusesFileWhoseWriteKeptBackup(t *testing.T) {
	backups := useTempDir(t)
	dir := t.TempDir()
	input := "terraform {\n  required_version = \">= 1.0\"\n  required_providers {\n    aws = {\n      source  = \"hashicorp/aws\"\n      version = \"~> 4.0\"\n    }\n  }\n}\n\nmodule \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n"
	file := writeTestFile(t, dir, "main.tf", input)
	// A second file shows the refusal covers only the file whose write kept a backup. Every pass walks
	// the files in the sorted order the glob returns, so main.tf is the one whose write fails.
	other := writeTestFile(t, dir, "other.tf", input)
	config := writeTestFile(t, dir, "versions.yml", "terraform_version: \">= 1.5\"\nproviders:\n  - name: aws\n    version: \"~> 5.0\"\nmodules:\n  - source: example/module\n    version: 2.0.0\n")
	rewrite := &failingFile{failOn: map[string][]int{"WriteAt": {1, 2}}, partial: 1}
	failRewrite(t, rewrite)

	result := runMainCommand(t, []string{"tf-version-bump", "-pattern", filepath.Join(dir, "*.tf"), "-config", config})

	kept := backupsIn(t, backups)
	if len(kept) != 1 {
		t.Fatalf("backups = %v, want exactly one; result = %#v", kept, result)
	}
	failure := "Error processing " + file + ": failed to write file: injected failure; restoring the original also failed: injected failure; original content is in " + kept[0] + "\n"
	refused := "Error processing " + file + ": file left untrusted by an earlier failed write; original content is in " + kept[0] + "\n"
	wantDiagnostics := failure + refused + refused + "Error: 3 update error(s)\n"
	if result.diagnostics != wantDiagnostics || result.exitCode != 1 {
		t.Fatalf("result = %#v, want diagnostics %q and exit 1", result, wantDiagnostics)
	}
	if strings.Contains(result.stdout, file) || strings.Count(result.stdout, other) != 3 {
		t.Errorf("stdout = %q, want the three updates of the second file and nothing for the refused one", result.stdout)
	}
	// The refused file still holds what the failed write left, so no later pass wrote to it again.
	if got := readTestFile(t, file); got != input {
		t.Errorf("refused file = %q, want it left as the failed write did", got)
	}
	if got := readTestFile(t, other); strings.Contains(got, "1.0.0") || strings.Contains(got, "~> 4.0") || strings.Contains(got, ">= 1.0") {
		t.Errorf("second file = %q, want every version updated", got)
	}
	if got := readTestFile(t, kept[0]); got != input {
		t.Errorf("backup = %q, want the original %q", got, input)
	}
}

func TestCommandWritesExactUpdatedBlockCounts(t *testing.T) {
	dir := t.TempDir()
	file := writeTestFile(t, dir, "main.tf", `terraform {
	  required_version = ">= 1.0"
	  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}
terraform {
	  required_version = ">= 1.5"
	  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}
terraform {
	  required_providers {
	    aws = {
	      source  = "hashicorp/aws"
	      version = "~> 5.0"
	    }
	  }
	}
	terraform {
	  required_version = "${">= 2.0"}"
	  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}
module "first" {
  source  = "example/module"
  version = "1.0.0"
}
module "second" {
  source  = "example/module"
  version = "1.0.0"
}
module "current" {
  source  = "example/module"
  version = "2.0.0"
}
`)
	config := writeTestFile(t, dir, "updates.yml", `providers:
  - name: aws
    version: "~> 5.0"
modules:
  - source: example/module
    version: 2.0.0
terraform_version: ">= 2.0"
`)
	report := dir + "/report.json"

	result := runMainCommand(t, []string{
		"tf-version-bump", "-pattern", file, "-config", config, "-report-file", report,
	})

	if result.exitCode != -1 || result.diagnostics != "" {
		t.Fatalf("result = %#v", result)
	}
	want := "{\n  \"schema_version\": 2,\n  \"terraform_blocks_updated\": 3,\n  \"module_blocks_updated\": 2,\n  \"provider_blocks_updated\": 2\n}\n"
	if got := readTestFile(t, report); got != want {
		t.Fatalf("report = %q, want %q", got, want)
	}
}

func TestCommandReportCountsTerraformBlocksInDirectMode(t *testing.T) {
	dir := t.TempDir()
	file := writeTestFile(t, dir, "main.tf", `terraform {
  required_version = ">= 1.0"
}

terraform {
  required_version = "${">= 1.5"}"
}

terraform {
}
`)
	report := dir + "/report.json"

	result := runMainCommand(t, []string{
		"tf-version-bump", "-pattern", file, "-terraform-version", ">= 1.5", "-report-file", report,
	})

	if result.exitCode != -1 || result.diagnostics != "" {
		t.Fatalf("result = %#v", result)
	}
	want := "{\n  \"schema_version\": 2,\n  \"terraform_blocks_updated\": 2,\n  \"module_blocks_updated\": 0,\n  \"provider_blocks_updated\": 0\n}\n"
	if got := readTestFile(t, report); got != want {
		t.Fatalf("report = %q, want %q", got, want)
	}
}

func TestCommandReportCountsHardLinkedTerraformBlocksOnce(t *testing.T) {
	dir := t.TempDir()
	file := writeTestFile(t, dir, "a.tf", "terraform {\n  required_version = \">= 1.0\"\n}\n")
	linkedFile := dir + "/b.tf"
	if err := os.Link(file, linkedFile); err != nil {
		t.Skipf("cannot create hard link: %v", err)
	}
	report := dir + "/report.json"

	result := runMainCommand(t, []string{
		"tf-version-bump", "-pattern", dir + "/*.tf", "-terraform-version", ">= 1.5", "-report-file", report,
	})

	if result.exitCode != -1 || result.diagnostics != "" {
		t.Fatalf("result = %#v", result)
	}
	want := "{\n  \"schema_version\": 2,\n  \"terraform_blocks_updated\": 1,\n  \"module_blocks_updated\": 0,\n  \"provider_blocks_updated\": 0\n}\n"
	if got := readTestFile(t, report); got != want {
		t.Fatalf("report = %q, want %q", got, want)
	}
}

func TestCommandReportAggregatesDistinctFiles(t *testing.T) {
	dir := t.TempDir()
	firstFile := writeTestFile(t, dir, "first.tf", `terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}
module "first" {
  source  = "example/module"
  version = "1.0.0"
}
`)
	secondFile := writeTestFile(t, dir, "second.tf", `terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}
module "second" {
  source  = "example/module"
  version = "1.0.0"
}
module "third" {
  source  = "example/module"
  version = "1.0.0"
}
`)
	config := writeTestFile(t, dir, "updates.yml", `providers:
  - name: aws
    version: "~> 5.0"
modules:
  - source: example/module
    version: 2.0.0
`)
	report := dir + "/report.json"

	result := runMainCommand(t, []string{
		"tf-version-bump", "-pattern", dir + "/*.tf", "-config", config, "-report-file", report,
	})

	if result.exitCode != -1 || result.diagnostics != "" {
		t.Fatalf("result = %#v", result)
	}
	wantReport := "{\n  \"schema_version\": 2,\n  \"terraform_blocks_updated\": 0,\n  \"module_blocks_updated\": 3,\n  \"provider_blocks_updated\": 2\n}\n"
	if got := readTestFile(t, report); got != wantReport {
		t.Errorf("report = %q, want %q", got, wantReport)
	}
	for _, file := range []string{firstFile, secondFile} {
		content := readTestFile(t, file)
		if !strings.Contains(content, `version = "~> 5.0"`) || !strings.Contains(content, `version = "2.0.0"`) {
			t.Errorf("updated Terraform content for %s = %q", file, content)
		}
	}
}

func TestCommandReportCountsChangedBlockStyleProviders(t *testing.T) {
	dir := t.TempDir()
	file := writeTestFile(t, dir, "main.tf", `terraform {
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}
terraform {
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 4.1"
    }
  }
}
terraform {
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}
`)
	report := dir + "/report.json"

	result := runMainCommand(t, []string{
		"tf-version-bump", "-pattern", file, "-provider", "aws", "-to", "~> 5.0", "-report-file", report,
	})

	if result.exitCode != -1 || result.diagnostics != "" {
		t.Fatalf("result = %#v", result)
	}
	wantReport := "{\n  \"schema_version\": 2,\n  \"terraform_blocks_updated\": 0,\n  \"module_blocks_updated\": 0,\n  \"provider_blocks_updated\": 2\n}\n"
	if got := readTestFile(t, report); got != wantReport {
		t.Errorf("report = %q, want %q", got, wantReport)
	}
}

func TestCommandReportCountsForceAddedModuleBlock(t *testing.T) {
	dir := t.TempDir()
	file := writeTestFile(t, dir, "main.tf", `module "vpc" {
  source = "terraform-aws-modules/vpc/aws"
}
`)
	report := dir + "/report.json"

	result := runMainCommand(t, []string{
		"tf-version-bump", "-pattern", file,
		"-module", "terraform-aws-modules/vpc/aws", "-to", "5.0.0",
		"-force-add", "-report-file", report,
	})

	if result.exitCode != -1 || result.diagnostics != "" {
		t.Fatalf("result = %#v", result)
	}
	wantReport := "{\n  \"schema_version\": 2,\n  \"terraform_blocks_updated\": 0,\n  \"module_blocks_updated\": 1,\n  \"provider_blocks_updated\": 0\n}\n"
	if got := readTestFile(t, report); got != wantReport {
		t.Errorf("report = %q, want %q", got, wantReport)
	}
	wantTerraform := "module \"vpc\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"5.0.0\"\n}\n"
	if got := readTestFile(t, file); got != wantTerraform {
		t.Errorf("Terraform content = %q, want %q", got, wantTerraform)
	}
}

func TestProcessFilesSkipsReportBookkeepingWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	file := writeTestFile(t, dir, "main.tf", "module \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n")
	flags := &cliFlags{}
	updates := []ModuleUpdate{{Source: "example/module", Version: "2.0.0"}}

	var updatesApplied, updateErrors int
	captureStdout(t, func() {
		updatesApplied, updateErrors = processFiles([]string{file}, updates, flags)
	})

	if updatesApplied != 1 || updateErrors != 0 {
		t.Fatalf("processFiles() = (%d, %d), want (1, 0)", updatesApplied, updateErrors)
	}
	if flags.report.moduleBlockIDs != nil || flags.report.fileIdentities != nil {
		t.Fatalf("disabled report bookkeeping = %#v", flags.report)
	}
}

func TestProviderModesSkipReportBookkeepingWhenDisabled(t *testing.T) {
	for _, mode := range []string{"CLI", "config"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			file := writeTestFile(t, dir, "main.tf", `terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}
`)
			flags := &cliFlags{providerName: "aws", toVersion: "~> 5.0", output: "text"}
			if mode == "config" {
				flags = &cliFlags{configFile: writeTestFile(t, dir, "updates.yml", "providers:\n  - name: aws\n    version: '~> 5.0'\n"), output: "text"}
			}

			var runErr error
			captureStdout(t, func() {
				if mode == "CLI" {
					_, runErr = runCLIMode([]string{file}, flags)
				} else {
					_, runErr = runConfigFileMode([]string{file}, flags)
				}
			})

			if runErr != nil {
				t.Fatalf("provider mode error = %v", runErr)
			}
			if flags.report.providerBlockIDs != nil || flags.report.fileIdentities != nil {
				t.Fatalf("disabled report bookkeeping = %#v", flags.report)
			}
			if got := readTestFile(t, file); !strings.Contains(got, `version = "~> 5.0"`) {
				t.Fatalf("updated Terraform content = %q", got)
			}
		})
	}
}

func TestCommandReportCountsEachBlockOnce(t *testing.T) {
	dir := t.TempDir()
	file := writeTestFile(t, dir, "main.tf", `terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}
module "example" {
  source  = "example/module"
  version = "1.0.0"
}
`)
	config := writeTestFile(t, dir, "updates.yml", `providers:
  - name: aws
    version: "~> 5.0"
  - name: aws
    version: "~> 6.0"
modules:
  - source: example/module
    version: 2.0.0
  - source: example/module
    version: 3.0.0
`)
	report := dir + "/report.json"

	result := runMainCommand(t, []string{
		"tf-version-bump", "-pattern", file, "-config", config, "-report-file", report,
	})

	if result.exitCode != -1 || result.diagnostics != "" {
		t.Fatalf("result = %#v", result)
	}
	wantReport := "{\n  \"schema_version\": 2,\n  \"terraform_blocks_updated\": 0,\n  \"module_blocks_updated\": 1,\n  \"provider_blocks_updated\": 1\n}\n"
	if got := readTestFile(t, report); got != wantReport {
		t.Errorf("report = %q, want %q", got, wantReport)
	}
	content := readTestFile(t, file)
	if !strings.Contains(content, `version = "~> 6.0"`) || !strings.Contains(content, `version = "3.0.0"`) {
		t.Errorf("final Terraform content = %q", content)
	}
}

func TestCommandReportCountsHardLinkedBlocksOnce(t *testing.T) {
	dir := t.TempDir()
	file := writeTestFile(t, dir, "a.tf", `terraform {
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}
module "example" {
  source  = "example/module"
  version = "1.0.0"
}
`)
	linkedFile := dir + "/b.tf"
	if err := os.Link(file, linkedFile); err != nil {
		t.Skipf("cannot create hard link: %v", err)
	}
	config := writeTestFile(t, dir, "updates.yml", `providers:
  - name: aws
    version: "~> 5.0"
  - name: aws
    version: "~> 6.0"
modules:
  - source: example/module
    version: 2.0.0
  - source: example/module
    version: 3.0.0
`)
	report := dir + "/report.json"

	result := runMainCommand(t, []string{
		"tf-version-bump", "-pattern", dir + "/*.tf", "-config", config, "-report-file", report,
	})

	if result.exitCode != -1 || result.diagnostics != "" {
		t.Fatalf("result = %#v", result)
	}
	wantReport := "{\n  \"schema_version\": 2,\n  \"terraform_blocks_updated\": 0,\n  \"module_blocks_updated\": 1,\n  \"provider_blocks_updated\": 1\n}\n"
	if got := readTestFile(t, report); got != wantReport {
		t.Errorf("report = %q, want %q", got, wantReport)
	}
	if got := readTestFile(t, linkedFile); !strings.Contains(got, `version = "3.0.0"`) || !strings.Contains(got, `version = "~> 6.0"`) {
		t.Errorf("final Terraform content = %q", got)
	}
}

func TestCommandReplacesExistingReportAfterSuccessfulUpdate(t *testing.T) {
	dir := t.TempDir()
	file := writeTestFile(t, dir, "main.tf", "module \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n")
	report := writeTestFile(t, dir, "report.json", "stale report\n")

	result := runMainCommand(t, []string{
		"tf-version-bump", "-pattern", file, "-module", "example/module", "-to", "2.0.0", "-report-file", report,
	})

	if result.exitCode != -1 || result.diagnostics != "" {
		t.Fatalf("result = %#v", result)
	}
	wantReport := "{\n  \"schema_version\": 2,\n  \"terraform_blocks_updated\": 0,\n  \"module_blocks_updated\": 1,\n  \"provider_blocks_updated\": 0\n}\n"
	if got := readTestFile(t, report); got != wantReport {
		t.Errorf("report = %q, want %q", got, wantReport)
	}
}

func TestCommandRejectsReportInputCollision(t *testing.T) {
	t.Run("Terraform input", func(t *testing.T) {
		dir := t.TempDir()
		input := "module \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n"
		file := writeTestFile(t, dir, "main.tf", input)

		result := runMainCommand(t, []string{
			"tf-version-bump", "-pattern", file, "-module", "example/module", "-to", "2.0.0", "-report-file", file,
		})

		wantDiagnostic := "Error: report file must not overwrite input file: " + file + "\n"
		if result.exitCode != 1 || result.diagnostics != wantDiagnostic {
			t.Errorf("result = %#v, want diagnostic %q", result, wantDiagnostic)
		}
		if got := readTestFile(t, file); got != input {
			t.Errorf("Terraform input = %q, want unchanged %q", got, input)
		}
	})

	t.Run("config input", func(t *testing.T) {
		dir := t.TempDir()
		input := "module \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n"
		file := writeTestFile(t, dir, "main.tf", input)
		configContent := "modules:\n  - source: example/module\n    version: 2.0.0\n"
		config := writeTestFile(t, dir, "updates.yml", configContent)

		result := runMainCommand(t, []string{
			"tf-version-bump", "-pattern", file, "-config", config, "-report-file", config,
		})

		wantDiagnostic := "Error: report file must not overwrite input file: " + config + "\n"
		if result.exitCode != 1 || result.diagnostics != wantDiagnostic {
			t.Errorf("result = %#v, want diagnostic %q", result, wantDiagnostic)
		}
		if got := readTestFile(t, file); got != input {
			t.Errorf("Terraform input = %q, want unchanged %q", got, input)
		}
		if got := readTestFile(t, config); got != configContent {
			t.Errorf("config input = %q, want unchanged %q", got, configContent)
		}
	})

	t.Run("symlink alias", func(t *testing.T) {
		dir := t.TempDir()
		input := "module \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n"
		file := writeTestFile(t, dir, "main.tf", input)
		report := dir + "/report.json"
		if err := os.Symlink(file, report); err != nil {
			t.Skipf("cannot create symlink: %v", err)
		}

		result := runMainCommand(t, []string{
			"tf-version-bump", "-pattern", file, "-module", "example/module", "-to", "2.0.0", "-report-file", report,
		})

		wantDiagnostic := "Error: report file must not overwrite input file: " + report + "\n"
		if result.exitCode != 1 || result.diagnostics != wantDiagnostic {
			t.Errorf("result = %#v, want diagnostic %q", result, wantDiagnostic)
		}
		if got := readTestFile(t, file); got != input {
			t.Errorf("Terraform input = %q, want unchanged %q", got, input)
		}
	})
}

func TestCommandRejectsUnusableReportDestinationBeforeUpdating(t *testing.T) {
	dir := t.TempDir()
	input := "module \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n"
	file := writeTestFile(t, dir, "main.tf", input)
	report := dir + "/missing/report.json"

	result := runMainCommand(t, []string{
		"tf-version-bump", "-pattern", file, "-module", "example/module", "-to", "2.0.0", "-report-file", report,
	})

	if result.exitCode != 1 || !strings.HasPrefix(result.diagnostics, "Error preparing update report: ") {
		t.Errorf("result = %#v, want report preparation failure", result)
	}
	if got := readTestFile(t, file); got != input {
		t.Errorf("Terraform input = %q, want unchanged %q", got, input)
	}
	if _, err := os.Stat(report); !os.IsNotExist(err) {
		t.Errorf("report stat error = %v, want not exist", err)
	}
}

func TestCommandRejectsReportDirectoryBeforeUpdating(t *testing.T) {
	dir := t.TempDir()
	input := "module \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n"
	file := writeTestFile(t, dir, "main.tf", input)
	report := dir + "/report-target"
	if err := os.Mkdir(report, 0o755); err != nil {
		t.Fatalf("create report directory: %v", err)
	}

	result := runMainCommand(t, []string{
		"tf-version-bump", "-pattern", file, "-module", "example/module", "-to", "2.0.0", "-report-file", report,
	})

	if result.exitCode != 1 || !strings.HasPrefix(result.diagnostics, "Error preparing update report: ") {
		t.Errorf("result = %#v, want report preparation failure", result)
	}
	if got := readTestFile(t, file); got != input {
		t.Errorf("Terraform input = %q, want unchanged %q", got, input)
	}
}

func TestCommandDiscardsPreparedReportAfterUpdateFailure(t *testing.T) {
	dir := t.TempDir()
	file := writeTestFile(t, dir, "main.tf", "!!!\n")
	reportContent := "previous report\n"
	report := writeTestFile(t, dir, "report.json", reportContent)

	result := runMainCommand(t, []string{
		"tf-version-bump", "-pattern", file, "-module", "example/module", "-to", "2.0.0", "-report-file", report,
	})

	if result.exitCode != 1 || !strings.Contains(result.diagnostics, "Error: 1 module update error(s)") {
		t.Errorf("result = %#v, want module update failure", result)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read temporary directory: %v", err)
	}
	if len(entries) != 2 || entries[0].Name() != "main.tf" || entries[1].Name() != "report.json" {
		t.Errorf("temporary directory entries = %v, want main.tf and report.json", entries)
	}
	if got := readTestFile(t, report); got != reportContent {
		t.Errorf("report = %q, want preserved %q", got, reportContent)
	}
}

func TestCommandDoesNotPrepareReportBeforeRequiredFlagValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "module", args: []string{"-module", "example/module"}},
		{name: "provider", args: []string{"-provider", "aws"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			file := writeTestFile(t, dir, "main.tf", "module \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n")
			report := dir + "/report.json"
			args := append([]string{"tf-version-bump", "-pattern", file, "-report-file", report}, tt.args...)

			result := runMainCommand(t, args)

			if result.exitCode != 1 {
				t.Errorf("result = %#v, want exit code 1", result)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("read temporary directory: %v", err)
			}
			if len(entries) != 1 || entries[0].Name() != "main.tf" {
				t.Errorf("temporary directory entries = %v, want only main.tf", entries)
			}
		})
	}
}

func TestCommandReportOmitsEquivalentLiteralUpdates(t *testing.T) {
	dir := t.TempDir()
	input := `terraform {
  required_version = "\u003e= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "\u007e> 5.0"
    }
  }
}
module "current" {
  source  = "example/module"
  version = "\u0032.0.0"
}
`
	file := writeTestFile(t, dir, "main.tf", input)
	wantModTime := time.Unix(1, 0)
	if err := os.Chtimes(file, wantModTime, wantModTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	config := writeTestFile(t, dir, "updates.yml", `providers:
  - name: aws
    version: "~> 5.0"
modules:
  - source: example/module
    version: 2.0.0
terraform_version: ">= 1.5"
`)
	report := dir + "/report.json"

	result := runMainCommand(t, []string{
		"tf-version-bump", "-pattern", file, "-config", config, "-report-file", report,
	})

	wantStdout := "Found 1 file(s) matching pattern '" + file + "'\n\n" +
		"No updates were performed. Every configured update is already applied, skipped or matched nothing; use -audit-file to see which.\n"
	if result.exitCode != -1 || result.diagnostics != "" || result.stdout != wantStdout {
		t.Fatalf("result = %#v, want stdout %q", result, wantStdout)
	}
	wantReport := "{\n  \"schema_version\": 2,\n  \"terraform_blocks_updated\": 0,\n  \"module_blocks_updated\": 0,\n  \"provider_blocks_updated\": 0\n}\n"
	if got := readTestFile(t, report); got != wantReport {
		t.Fatalf("report = %q, want %q", got, wantReport)
	}
	if got := readTestFile(t, file); got != input {
		t.Errorf("content = %q, want unchanged %q", got, input)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.ModTime(); !got.Equal(wantModTime) {
		t.Errorf("modification time = %v, want unchanged %v", got, wantModTime)
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
				wantStdout += "==================================================\nConfig File Update Summary\n==================================================\nModules: 1 update(s) applied\n"
			}
			wantStdout += "1 update(s) failed; see the errors on stderr\n"
			wantDiag := "Error processing " + bad + ": failed to parse HCL: " + bad + ":1,1-2: Argument or block definition required; An argument or block definition is required here.\nError: 1 module update error(s)\n"
			wantHCL := "module \"example\" {\n  source  = \"example/module\"\n  version = \"2.0.0\"\n}\n"
			if r.stdout != wantStdout || r.diagnostics != wantDiag || r.exitCode != 1 || readTestFile(t, good) != wantHCL {
				t.Fatalf("result %#v content=%q", r, readTestFile(t, good))
			}
		})
	}
}

func TestCommandWritesConfigAudit(t *testing.T) {
	dir := t.TempDir()
	input := `terraform {
  required_version = ">= 1.9"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "4.2.0"
}
`
	file := writeTestFile(t, dir, "main.tf", input)
	config := writeTestFile(t, dir, "versions.yml", `terraform_version: ">= 1.10"
providers:
  - name: aws
    version: "~> 6.0"
modules:
  - source: terraform-aws-modules/vpc/aws
    version: 5.0.0
`)
	audit := filepath.Join(dir, "audit.json")

	result := runMainCommand(t, []string{"tf-version-bump", "-pattern", file, "-config", config, "-audit-file", audit})

	wantStdout := "Found 1 file(s) matching pattern '" + file + "'\n✓ Wrote audit to '" + audit + "'\n"
	if result.stdout != wantStdout || result.diagnostics != "" || result.exitCode != -1 {
		t.Fatalf("result = %#v, want stdout %q and a normal return", result, wantStdout)
	}
	wantAudit := `{
  "schema_version": 1,
  "terraform": [
    {
      "file": "` + file + `",
      "actual": ">= 1.9",
      "expected": ">= 1.10",
      "matches": false
    }
  ],
  "providers": [
    {
      "file": "` + file + `",
      "name": "aws",
      "actual": "~> 6.0",
      "expected": "~> 6.0",
      "matches": true
    }
  ],
  "modules": [
    {
      "file": "` + file + `",
      "name": "vpc",
      "source": "terraform-aws-modules/vpc/aws",
      "actual": "4.2.0",
      "expected": "5.0.0",
      "matches": false,
      "skip": null
    }
  ]
}
`
	if got := readTestFile(t, audit); got != wantAudit {
		t.Errorf("audit = %q, want %q", got, wantAudit)
	}
	if got := readTestFile(t, file); got != input {
		t.Errorf("audit changed Terraform content to %q", got)
	}
}

func TestCommandAuditRejectsConflictingFlags(t *testing.T) {
	dir := t.TempDir()
	file := writeTestFile(t, dir, "main.tf", "module \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n")
	config := writeTestFile(t, dir, "versions.yml", "modules:\n  - source: example/module\n    version: 2.0.0\n")
	audit := filepath.Join(dir, "audit.json")
	conflict := "Error: Cannot use -audit-file with -dry-run, -check, -report-file or -force-add\n"
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "without config", args: []string{"-module", "example/module", "-to", "2.0.0"}, want: "Error: -audit-file requires -config\n"},
		{name: "dry run", args: []string{"-config", config, "-dry-run"}, want: conflict},
		{name: "check", args: []string{"-config", config, "-check"}, want: conflict},
		{name: "report", args: []string{"-config", config, "-report-file", filepath.Join(dir, "report.json")}, want: conflict},
		{name: "force add", args: []string{"-config", config, "-force-add"}, want: conflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"tf-version-bump", "-pattern", file, "-audit-file", audit}, tt.args...)
			result := runMainCommand(t, args)
			if result.stdout != "" || result.diagnostics != tt.want || result.exitCode != 1 {
				t.Fatalf("result = %#v, want diagnostic %q and exit 1", result, tt.want)
			}
			if _, err := os.Stat(audit); !os.IsNotExist(err) {
				t.Fatalf("audit stat error = %v, want no audit", err)
			}
		})
	}
}

func TestCommandAuditRejectsInputCollision(t *testing.T) {
	dir := t.TempDir()
	input := "module \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n"
	file := writeTestFile(t, dir, "main.tf", input)
	configContent := "modules:\n  - source: example/module\n    version: 2.0.0\n"
	config := writeTestFile(t, dir, "versions.yml", configContent)

	for _, destination := range []string{file, config} {
		result := runMainCommand(t, []string{"tf-version-bump", "-pattern", file, "-config", config, "-audit-file", destination})

		wantDiagnostic := "Error: audit file must not overwrite input file: " + destination + "\n"
		if result.exitCode != 1 || result.diagnostics != wantDiagnostic {
			t.Errorf("result = %#v, want diagnostic %q", result, wantDiagnostic)
		}
	}
	if readTestFile(t, file) != input || readTestFile(t, config) != configContent {
		t.Error("a rejected audit changed an input file")
	}
}

func TestCommandAuditRejectsADirectoryDestination(t *testing.T) {
	dir := t.TempDir()
	file := writeTestFile(t, dir, "main.tf", "module \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n")
	config := writeTestFile(t, dir, "versions.yml", "modules:\n  - source: example/module\n    version: 2.0.0\n")

	result := runMainCommand(t, []string{"tf-version-bump", "-pattern", file, "-config", config, "-audit-file", dir})

	if result.exitCode != 1 || result.diagnostics != "Error preparing audit: destination is a directory: "+dir+"\n" {
		t.Errorf("result = %#v, want the audit preparation failure", result)
	}
}

func TestCommandAuditWritesNothingWhenAFileCannotBeParsed(t *testing.T) {
	dir := t.TempDir()
	bad := writeTestFile(t, dir, "bad.tf", "terraform {\n")
	writeTestFile(t, dir, "good.tf", "terraform {\n  required_version = \">= 1.10\"\n}\n")
	config := writeTestFile(t, dir, "versions.yml", "terraform_version: \">= 1.10\"\n")
	previous := "previous audit\n"
	audit := writeTestFile(t, dir, "audit.json", previous)

	result := runMainCommand(t, []string{"tf-version-bump", "-pattern", filepath.Join(dir, "*.tf"), "-config", config, "-audit-file", audit})

	if result.exitCode != 1 || !strings.HasPrefix(result.diagnostics, "Error auditing "+bad+": failed to parse HCL: ") {
		t.Errorf("result = %#v, want a parse failure naming %s", result, bad)
	}
	if got := readTestFile(t, audit); got != previous {
		t.Errorf("audit = %q, want the previous audit kept", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read directory: %v", err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if strings.Join(names, ",") != "audit.json,bad.tf,good.tf,versions.yml" {
		t.Errorf("directory entries = %v, want no temporary audit left behind", names)
	}
}

func TestCommandAuditReportsAnInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	file := writeTestFile(t, dir, "main.tf", "terraform {}\n")
	config := writeTestFile(t, dir, "versions.yml", "modules:\n  - version: 1.0.0\n")

	result := runMainCommand(t, []string{"tf-version-bump", "-pattern", file, "-config", config, "-audit-file", filepath.Join(dir, "audit.json")})

	want := "Error loading config file: module at index 0 is missing 'source' field\n"
	if result.exitCode != 1 || result.diagnostics != want {
		t.Errorf("result = %#v, want diagnostic %q", result, want)
	}
}
