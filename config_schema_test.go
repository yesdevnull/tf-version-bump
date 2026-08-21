package main

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
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
	AnyOf []struct {
		Required []string `json:"required"`
	} `json:"anyOf"`
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
		Providers        struct {
			Type  string `json:"type"`
			Items struct {
				Required   []string                   `json:"required"`
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"items"`
		} `json:"providers"`
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
	if schema.Properties.Providers.Type != "array" {
		t.Fatal("providers should be an array")
	}
	assertDistinctSchemaContracts(t, &schema)
	for _, field := range []string{"ignore_versions", "ignore_modules"} {
		if _, ok := schema.Properties.Modules.Items.Properties[field]; !ok {
			t.Fatalf("module %s schema is missing", field)
		}
	}
	if schemaNodeType(t, schema.Properties.Modules.Items.Properties["ignore_modules"]) != "array" {
		t.Fatal("ignore_modules should be an array")
	}
	if !hasExactOneOfShapes(t, schema.Properties.Modules.Items.Properties["from"], "string", "array") {
		t.Fatal("from should allow exactly scalar and array shapes")
	}
	if !hasExactOneOfShapes(t, schema.Properties.Modules.Items.Properties["ignore_versions"], "string", "array") {
		t.Fatal("ignore_versions should allow exactly scalar and array shapes")
	}
}

func assertDistinctSchemaContracts(t *testing.T, schema *configSchema) {
	t.Helper()
	for _, field := range []string{"name", "version"} {
		if !slices.Contains(schema.Properties.Providers.Items.Required, field) {
			t.Fatalf("provider schema should require %q", field)
		}
	}

	providerVersion, ok := schema.Properties.Providers.Items.Properties["version"]
	if !ok || !referencesVersionConstraint(t, providerVersion) {
		t.Fatal("provider version should reference the shared version constraint definition")
	}

	moduleVersion, ok := schema.Properties.Modules.Items.Properties["version"]
	if !ok || !referencesVersionConstraint(t, moduleVersion) {
		t.Fatal("module version should reference the shared version constraint definition")
	}

	if len(schema.AnyOf) != 3 {
		t.Fatalf("schema anyOf clauses = %d, want exactly 3", len(schema.AnyOf))
	}
	requiredTopLevel := make(map[string]bool, len(schema.AnyOf))
	for _, clause := range schema.AnyOf {
		if len(clause.Required) != 1 {
			t.Fatalf("schema anyOf clause required fields = %v, want exactly one", clause.Required)
		}
		requiredTopLevel[clause.Required[0]] = true
	}
	for _, field := range []string{"modules", "providers", "terraform_version"} {
		if !requiredTopLevel[field] {
			t.Fatalf("schema anyOf should contain a singleton required clause for %q", field)
		}
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

func hasExactOneOfShapes(t *testing.T, raw json.RawMessage, shapes ...string) bool {
	t.Helper()
	var node struct {
		OneOf []json.RawMessage `json:"oneOf"`
	}
	if err := json.Unmarshal(raw, &node); err != nil {
		t.Fatalf("failed to parse oneOf schema: %v", err)
	}
	if len(node.OneOf) != len(shapes) {
		return false
	}
	found := map[string]bool{}
	for _, option := range node.OneOf {
		var entry struct {
			Type  string          `json:"type"`
			Items json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal(option, &entry); err != nil {
			t.Fatalf("failed to parse oneOf option: %v", err)
		}
		if entry.Type == "array" && len(entry.Items) > 0 && referencesVersionConstraint(t, entry.Items) {
			found["array"] = true
			continue
		}
		if referencesVersionConstraint(t, option) {
			found["string"] = true
			continue
		}
		return false
	}
	if len(found) != len(shapes) {
		return false
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
