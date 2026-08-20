package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
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
	if !exactSemanticVersion.MatchString(version) {
		t.Fatalf("SLSA generator version = %q, want an exact semantic-version tag", version)
	}
}
