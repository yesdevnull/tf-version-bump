package main

import "testing"

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
