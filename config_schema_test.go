package main

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"
)

type versionSchema struct {
	Type    string `json:"type"`
	Pattern string `json:"pattern"`
	OneOf   []struct {
		Pattern string `json:"pattern"`
	} `json:"oneOf"`
}

type configSchema struct {
	Definitions struct {
		VersionConstraint versionSchema `json:"versionConstraint"`
	} `json:"definitions"`
	Properties struct {
		Modules struct {
			Items struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"items"`
		} `json:"modules"`
		TerraformVersion json.RawMessage `json:"terraform_version"`
		Providers        json.RawMessage `json:"providers"`
	} `json:"properties"`
}

func loadConfigSchema(t *testing.T) configSchema {
	t.Helper()

	data, err := os.ReadFile("schema/config-schema.json")
	if err != nil {
		t.Fatalf("failed to read config schema: %v", err)
	}

	var schema configSchema
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("failed to parse config schema: %v", err)
	}

	return schema
}

func TestConfigSchemaExposesConfigurationOptions(t *testing.T) {
	schema := loadConfigSchema(t)

	if schema.Definitions.VersionConstraint.Type != "string" {
		t.Fatalf("version constraint definition type = %q, want string", schema.Definitions.VersionConstraint.Type)
	}
	if len(schema.Properties.TerraformVersion) == 0 {
		t.Fatal("terraform_version schema is missing")
	}
	if !referencesVersionConstraint(t, schema.Properties.TerraformVersion) {
		t.Fatal("terraform_version should reference the shared version constraint definition")
	}
	if schemaNodeType(t, schema.Properties.Providers) != "array" {
		t.Fatal("providers should be an array")
	}
	for _, field := range []string{"ignore_versions", "ignore_modules"} {
		if _, ok := schema.Properties.Modules.Items.Properties[field]; !ok {
			t.Fatalf("module %s schema is missing", field)
		}
	}
	if schemaNodeType(t, schema.Properties.Modules.Items.Properties["ignore_modules"]) != "array" {
		t.Fatal("ignore_modules should be an array")
	}
	if !hasOneOfShape(t, schema.Properties.Modules.Items.Properties["from"], "string", "array") {
		t.Fatal("from should allow scalar and array shapes")
	}
	if !hasOneOfShape(t, schema.Properties.Modules.Items.Properties["ignore_versions"], "string", "array") {
		t.Fatal("ignore_versions should allow scalar and array shapes")
	}
}

func TestConfigSchemaVersionPatternAllowsTerraformConstraints(t *testing.T) {
	schema := loadConfigSchema(t)

	regexes := compileConstraintRegexps(t, schema.Definitions.VersionConstraint)

	validConstraints := []string{"1.2.3", "~> 3.0", ">= 1.5, < 2.0"}

	for _, constraint := range validConstraints {
		matched := false
		for _, re := range regexes {
			if re.MatchString(constraint) {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("expected schema pattern to accept %q", constraint)
		}
	}

	for _, re := range regexes {
		if re.MatchString("") {
			t.Error("expected schema pattern to reject an empty version")
		}
	}
}

func schemaNodeType(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var node map[string]any
	if err := json.Unmarshal(raw, &node); err != nil {
		t.Fatalf("failed to parse schema node: %v", err)
	}
	typeName, _ := node["type"].(string)
	return typeName
}

func hasOneOfShape(t *testing.T, raw json.RawMessage, shapes ...string) bool {
	t.Helper()
	var node struct {
		OneOf []json.RawMessage `json:"oneOf"`
	}
	if err := json.Unmarshal(raw, &node); err != nil {
		t.Fatalf("failed to parse oneOf schema: %v", err)
	}
	found := map[string]bool{}
	for _, option := range node.OneOf {
		var entry map[string]any
		if err := json.Unmarshal(option, &entry); err != nil {
			t.Fatalf("failed to parse oneOf option: %v", err)
		}
		if typeName, ok := entry["type"].(string); ok {
			found[typeName] = true
			continue
		}
		if allOf, ok := entry["allOf"].([]any); ok && len(allOf) > 0 && referencesVersionConstraint(t, option) {
			found["string"] = true
		}
	}
	for _, shape := range shapes {
		if !found[shape] {
			return false
		}
	}
	return true
}

func referencesVersionConstraint(t *testing.T, raw json.RawMessage) bool {
	t.Helper()

	if len(raw) == 0 {
		return false
	}

	var node map[string]any
	if err := json.Unmarshal(raw, &node); err != nil {
		t.Fatalf("failed to parse schema node: %v", err)
	}

	if ref, ok := node["$ref"].(string); ok && ref == "#/definitions/versionConstraint" {
		return true
	}

	allOf, ok := node["allOf"].([]any)
	if !ok {
		return false
	}

	for _, entry := range allOf {
		if entryMap, ok := entry.(map[string]any); ok {
			if ref, ok := entryMap["$ref"].(string); ok && ref == "#/definitions/versionConstraint" {
				return true
			}
		}
	}

	return false
}

func compileConstraintRegexps(t *testing.T, schema versionSchema) []*regexp.Regexp {
	t.Helper()

	patterns := make([]string, 0, 1+len(schema.OneOf))

	if schema.Pattern != "" {
		patterns = append(patterns, schema.Pattern)
	}

	for _, option := range schema.OneOf {
		if option.Pattern != "" {
			patterns = append(patterns, option.Pattern)
		}
	}

	if len(patterns) == 0 {
		t.Fatalf("no patterns found in version constraint schema")
	}

	regexes := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			t.Fatalf("failed to compile pattern %q: %v", pattern, err)
		}
		regexes = append(regexes, re)
	}

	return regexes
}
