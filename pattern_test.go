package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestMatchPatternContract(t *testing.T) {
	tests := []struct {
		name, input, pattern string
		want                 bool
	}{
		{name: "exact", input: "vpc", pattern: "vpc", want: true},
		{name: "different literal", input: "vpc", pattern: "s3", want: false},
		{name: "wildcard matches empty", input: "", pattern: "*", want: true},
		{name: "prefix", input: "legacy-vpc", pattern: "legacy-*", want: true},
		{name: "suffix", input: "vpc-test", pattern: "*-test", want: true},
		{name: "contains", input: "prod-vpc-test", pattern: "*-vpc-*", want: true},
		{name: "ordered middles", input: "aws-prod-vpc-au", pattern: "aws-*-vpc-*", want: true},
		{name: "middles out of order", input: "aws-vpc-prod-au", pattern: "aws-*-prod-vpc-*", want: false},
		{name: "missing middle", input: "aws-prod-s3-au", pattern: "aws-*-vpc-*", want: false},
		{name: "overlap too short", input: "abc", pattern: "abc*abc", want: false},
		{name: "overlap minimum", input: "abcabc", pattern: "abc*abc", want: true},
		{name: "zero-width middle", input: "module-test", pattern: "module*-test", want: true},
		{name: "repeated part", input: "a-b-a-b", pattern: "a-*-a-*", want: true},
		{name: "Unicode", input: "módulo-vpc-produção", pattern: "módulo-*-produção", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchPattern(tt.input, tt.pattern); got != tt.want {
				t.Errorf("matchPattern(%q, %q) = %v, want %v", tt.input, tt.pattern, got, tt.want)
			}
		})
	}
}

func TestShouldIgnoreModuleContract(t *testing.T) {
	tests := []struct {
		name     string
		module   string
		patterns []string
		want     bool
	}{
		{name: "empty module name is never ignored", module: "", patterns: []string{"*"}, want: false},
		{name: "empty patterns do not ignore", module: "vpc", patterns: nil, want: false},
		{name: "second pattern matches", module: "legacy-vpc", patterns: []string{"s3", "legacy-*"}, want: true},
		{name: "no pattern matches", module: "vpc", patterns: []string{"s3", "database-*"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldIgnoreModule(tt.module, tt.patterns); got != tt.want {
				t.Errorf("shouldIgnoreModule(%q, %v) = %v, want %v", tt.module, tt.patterns, got, tt.want)
			}
		})
	}
}

func TestSplitIgnoreModuleEntryContract(t *testing.T) {
	tests := []struct {
		name, entry, wantBranch, wantModule, wantError string
	}{
		{name: "unscoped name", entry: "vpc", wantModule: "vpc"},
		{name: "unscoped wildcard", entry: "legacy-*", wantModule: "legacy-*"},
		{name: "single-segment branch", entry: "main/vpc", wantBranch: "main", wantModule: "vpc"},
		{name: "multi-segment branch", entry: "state/staging/example-thing/shared-vpc", wantBranch: "state/staging/example-thing", wantModule: "shared-vpc"},
		{name: "branch wildcard", entry: "release/*/legacy-vpc", wantBranch: "release/*", wantModule: "legacy-vpc"},
		{name: "surrounding whitespace trimmed", entry: "main / vpc", wantBranch: "main", wantModule: "vpc"},
		{name: "missing module pattern", entry: "state/staging/", wantError: "must be '<branch>/<module>'"},
		{name: "missing branch pattern", entry: "/vpc", wantError: "must be '<branch>/<module>'"},
		{name: "empty middle segment", entry: "state//vpc", wantError: "must be '<branch>/<module>'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			branch, module, err := splitIgnoreModuleEntry(tt.entry)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("splitIgnoreModuleEntry(%q) error = %v, want error containing %q", tt.entry, err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("splitIgnoreModuleEntry(%q) returned error: %v", tt.entry, err)
			}
			if branch != tt.wantBranch || module != tt.wantModule {
				t.Errorf("splitIgnoreModuleEntry(%q) = (%q, %q), want (%q, %q)", tt.entry, branch, module, tt.wantBranch, tt.wantModule)
			}
		})
	}
}

func TestResolveBranchIgnoreModulesContract(t *testing.T) {
	tests := []struct {
		name      string
		patterns  []string
		branch    string
		want      []string
		wantError string
	}{
		{name: "no patterns are left untouched", patterns: nil, branch: "main", want: nil},
		{name: "unscoped patterns apply to every branch", patterns: []string{"vpc", "legacy-*"}, branch: "", want: []string{"vpc", "legacy-*"}},
		{name: "matching branch keeps the module pattern", patterns: []string{"state/staging/example-thing/shared-vpc"}, branch: "state/staging/example-thing", want: []string{"shared-vpc"}},
		{name: "other branch drops the module pattern", patterns: []string{"state/staging/example-thing/shared-vpc"}, branch: "state/production/example-thing", want: []string{}},
		{name: "branch wildcard spans separators", patterns: []string{"state/*/shared-*"}, branch: "state/staging/example-thing", want: []string{"shared-*"}},
		{name: "scoped and unscoped patterns combine", patterns: []string{"legacy-*", "main/vpc", "release/*/vpc"}, branch: "main", want: []string{"legacy-*", "vpc"}},
		{name: "scoped pattern without a branch is rejected", patterns: []string{"main/vpc"}, branch: "", wantError: "ignore pattern 'main/vpc' is scoped to a branch, so the -branch flag is required"},
		{name: "malformed pattern is rejected", patterns: []string{"main/"}, branch: "main", wantError: "must be '<branch>/<module>'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updates := []ModuleUpdate{{Source: "example/module", Version: "2.0.0", IgnoreModules: tt.patterns}}
			err := resolveBranchIgnoreModules(updates, tt.branch)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("resolveBranchIgnoreModules(%v, %q) error = %v, want error containing %q", tt.patterns, tt.branch, err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveBranchIgnoreModules(%v, %q) returned error: %v", tt.patterns, tt.branch, err)
			}
			if !reflect.DeepEqual(updates[0].IgnoreModules, tt.want) {
				t.Errorf("ignore modules = %#v, want %#v", updates[0].IgnoreModules, tt.want)
			}
		})
	}
}
