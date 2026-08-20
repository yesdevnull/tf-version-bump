package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateModuleVersionContract(t *testing.T) {
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
	const registry = "terraform-aws-modules/vpc/aws"
	cases := []moduleCase{
		{"matching registry module updates", "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n}\n", registry, "5.0.0", nil, nil, nil, false, false, false, true, "module \"vpc\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"5.0.0\"\n}\n"},
		{"non-matching source unchanged", "module \"vpc\" {\n  source = \"terraform-aws-modules/s3-bucket/aws\"\n  version = \"3.14.0\"\n}\n", registry, "5.0.0", nil, nil, nil, false, false, false, false, "module \"vpc\" {\n  source = \"terraform-aws-modules/s3-bucket/aws\"\n  version = \"3.14.0\"\n}\n"},
		{"force-add missing version", "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n}\n", registry, "5.0.0", nil, nil, nil, true, false, false, true, "module \"vpc\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"5.0.0\"\n}\n"},
		{"relative local source skipped", "module \"vpc\" {\n  source = \"./modules/vpc\"\n}\n", "./modules/vpc", "5.0.0", nil, nil, nil, true, false, false, false, "module \"vpc\" {\n  source = \"./modules/vpc\"\n}\n"},
		{"matching from filter updates", "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n}\n", registry, "5.0.0", []string{"3.14.0"}, nil, nil, false, false, false, true, "module \"vpc\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"5.0.0\"\n}\n"},
		{"non-matching from preserves version", "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n}\n", registry, "5.0.0", []string{"4.0.0"}, nil, nil, false, false, false, false, "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n}\n"},
		{"matching ignore version preserves version", "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n}\n", registry, "5.0.0", nil, []string{"3.14.0"}, nil, false, false, false, false, "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n}\n"},
		{"mixed ignored and eligible modules", "module \"ignored\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n}\n\nmodule \"eligible\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"4.0.0\"\n}\n", registry, "5.0.0", nil, []string{"3.14.0"}, nil, false, false, false, true, "module \"ignored\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n}\n\nmodule \"eligible\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"5.0.0\"\n}\n"},
		{"ignore takes precedence over from", "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n}\n", registry, "5.0.0", []string{"4.0.0"}, []string{"3.14.0"}, nil, false, false, true, false, "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file := writeTestFile(t, t.TempDir(), "main.tf", tc.input)
			var updated bool
			var err error
			var stdout string
			var stderr string
			if tc.verbose {
				stdout = captureStdout(t, func() {
					updated, err = updateModuleVersion(file, tc.source, tc.version, tc.from, tc.ignoreVersions, tc.ignoreModules, tc.forceAdd, tc.dryRun, tc.verbose, "text")
				})
			} else if strings.Contains(tc.name, "local source") {
				stderr = captureStderr(t, func() {
					updated, err = updateModuleVersion(file, tc.source, tc.version, tc.from, tc.ignoreVersions, tc.ignoreModules, tc.forceAdd, tc.dryRun, tc.verbose, "text")
				})
			} else {
				updated, err = updateModuleVersion(file, tc.source, tc.version, tc.from, tc.ignoreVersions, tc.ignoreModules, tc.forceAdd, tc.dryRun, tc.verbose, "text")
			}
			if err != nil {
				t.Fatalf("updateModuleVersion: %v", err)
			}
			if updated != tc.wantUpdated {
				t.Fatalf("updated = %v, want %v", updated, tc.wantUpdated)
			}
			if got := readTestFile(t, file); got != tc.wantContent {
				t.Errorf("content = %q, want %q", got, tc.wantContent)
			}
			if tc.verbose && (!strings.Contains(stdout, "matches 'ignore-version' filter") || strings.Contains(stdout, "does not match any 'from' filter")) {
				t.Errorf("unexpected precedence output: %s", stdout)
			}
			if strings.Contains(tc.name, "local source") {
				want := fmt.Sprintf("Warning: Module 'vpc' in %s (source: './modules/vpc') is a local module and cannot be version-bumped, skipping\n", file)
				if stderr != want {
					t.Errorf("stderr = %q, want %q", stderr, want)
				}
			}
		})
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

func TestUpdateModuleVersionPreservesHCL(t *testing.T) {
	input := "# This is a comment\nmodule \"vpc\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"3.14.0\"\n\n  # Another comment\n  name = \"my-vpc\"\n  cidr = \"10.0.0.0/16\"\n}\n\nmodule \"other\" {\n  source = \"terraform-aws-modules/s3-bucket/aws\"\n  version = \"1.0.0\"\n}\n"
	want := "# This is a comment\nmodule \"vpc\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"5.0.0\"\n\n  # Another comment\n  name = \"my-vpc\"\n  cidr = \"10.0.0.0/16\"\n}\n\nmodule \"other\" {\n  source  = \"terraform-aws-modules/s3-bucket/aws\"\n  version = \"1.0.0\"\n}\n"
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

func TestUpdateModuleVersionDryRunContract(t *testing.T) {
	input := "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n  version = \"1.0.0\"\n}\n"
	file := writeTestFile(t, t.TempDir(), "main.tf", input)
	var updated bool
	var err error
	output := captureStdout(t, func() {
		updated, err = updateModuleVersion(file, "terraform-aws-modules/vpc/aws", "2.0.0", nil, nil, nil, false, true, false, "text")
	})
	if err != nil || !updated {
		t.Fatalf("updated=%v err=%v", updated, err)
	}
	if output != "" {
		t.Errorf("output = %q, want empty", output)
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

func TestContainsVersionContract(t *testing.T) {
	for _, tc := range []struct {
		name, version string
		versions      []string
		want          bool
	}{{"empty", "1.0.0", nil, false}, {"exact", "1.0.0", []string{"1.0.0"}, true}, {"non-match", "1.0.0", []string{"2.0.0"}, false}} {
		t.Run(tc.name, func(t *testing.T) {
			if got := containsVersion(tc.versions, tc.version); got != tc.want {
				t.Errorf("containsVersion=%v, want %v", got, tc.want)
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
