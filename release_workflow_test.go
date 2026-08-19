package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestReleaseWorkflowReferencesSLSAGeneratorByExactSemanticVersionTag(t *testing.T) {
	workflowData, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatalf("failed to read release workflow: %v", err)
	}

	var workflow struct {
		Jobs map[string]struct {
			Uses string `yaml:"uses"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(workflowData, &workflow); err != nil {
		t.Fatalf("failed to parse release workflow: %v", err)
	}

	const generator = "slsa-framework/slsa-github-generator/.github/workflows/generator_generic_slsa3.yml@"
	reference := workflow.Jobs["assets-provenance"].Uses
	if !strings.HasPrefix(reference, generator) {
		t.Fatalf("assets-provenance uses %q, want the SLSA generic generator", reference)
	}

	version := strings.TrimPrefix(reference, generator)
	exactSemanticVersion := regexp.MustCompile(`^v[1-9]\d*\.\d+\.\d+(?:-rc\.\d+)?$`)
	testCases := []struct {
		name      string
		version   string
		supported bool
	}{
		{name: "workflow reference", version: version, supported: true},
		{name: "stable release", version: "v2.1.0", supported: true},
		{name: "release candidate", version: "v2.1.0-rc.1", supported: true},
		{name: "commit hash", version: "f7dd8c54c2067bafc12ca7a55595d5ee9b75204a", supported: false},
		{name: "short tag", version: "v2.1", supported: false},
		{name: "malformed release candidate", version: "v2.1.0-rc.", supported: false},
		{name: "unsupported prerelease", version: "v2.1.0-beta.1", supported: false},
		{name: "zero-leading major", version: "v02.1.0", supported: false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := exactSemanticVersion.MatchString(testCase.version); got != testCase.supported {
				t.Errorf("SLSA generator reference support for %q = %t, want %t", testCase.version, got, testCase.supported)
			}
		})
	}
}
