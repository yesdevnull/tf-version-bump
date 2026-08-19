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
	exactSemanticVersion := regexp.MustCompile(`^v\d+\.\d+\.\d+(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?$`)
	if !exactSemanticVersion.MatchString(version) {
		t.Fatalf("SLSA generator reference %q is unsupported; use an exact semantic version tag such as v2.1.0", version)
	}
}
