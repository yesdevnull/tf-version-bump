package main

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseFlagsContract(t *testing.T) {
	args := []string{"tf-version-bump", "-pattern", "**/*.tf", "-module", "example/module", "-to", "2.0.0", "-from", "1.0.0", "-from", "1.5.0", "-ignore-version", "3.0.0", "-ignore-modules", "vpc, legacy-*", "-config", "config.yml", "-validate-config", "validate.yml", "-force-add", "-dry-run", "-check", "-verbose", "-version", "-output", "md", "-terraform-version", ">= 1.5", "-provider", "aws"}
	withFlagArgs(t, args, func() {
		got := parseFlags()
		want := &cliFlags{pattern: "**/*.tf", moduleSource: "example/module", toVersion: "2.0.0", fromVersions: stringSliceFlag{"1.0.0", "1.5.0"}, ignoreVersions: stringSliceFlag{"3.0.0"}, ignoreModules: "vpc, legacy-*", configFile: "config.yml", validationConfigFile: "validate.yml", forceAdd: true, dryRun: true, check: true, verbose: true, showVersion: true, output: "md", terraformVersion: ">= 1.5", providerName: "aws"}
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
		{name: "Terraform version", args: []string{"-terraform-version", ">= 1.5"}},
		{name: "provider", args: []string{"-provider", "aws"}},
		{name: "force add", args: []string{"-force-add"}},
		{name: "dry run", args: []string{"-dry-run"}},
		{name: "check", args: []string{"-check"}},
		{name: "verbose", args: []string{"-verbose"}},
		{name: "report", args: []string{"-report-file", "report.json"}},
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

	if result.exitCode != 1 || !strings.Contains(result.diagnostics, "Error processing "+bad) || !strings.Contains(result.diagnostics, "1 module update error(s)") {
		t.Fatalf("result = %#v, want processing diagnostics and exit 1", result)
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

	if result.exitCode != 1 || !strings.Contains(result.diagnostics, "1 module update error(s)") {
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
		"No updates were performed. Config file may be empty or contain no matching items.\n"
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
			wantDiag := "Error processing " + bad + ": failed to parse HCL: " + bad + ":1,1-2: Argument or block definition required; An argument or block definition is required here.\n1 module update error(s)\n"
			wantHCL := "module \"example\" {\n  source  = \"example/module\"\n  version = \"2.0.0\"\n}\n"
			if r.stdout != wantStdout || r.diagnostics != wantDiag || r.exitCode != 1 || readTestFile(t, good) != wantHCL {
				t.Fatalf("result %#v content=%q", r, readTestFile(t, good))
			}
		})
	}
}
