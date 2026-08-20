package main

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestFromVersionsRejectsNonStringSequenceItem(t *testing.T) {
	type fromVersionsRegressionDocument struct {
		From FromVersions `yaml:"from"`
	}

	var document fromVersionsRegressionDocument
	err := yaml.Unmarshal([]byte("from: [4.0.0, 4]\n"), &document)
	if err == nil || !strings.Contains(err.Error(), "array contains non-string values") {
		t.Fatalf("error = %v, want component %q", err, "array contains non-string values")
	}
}
