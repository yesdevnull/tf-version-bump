package main

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"
)

type versionSchema struct {
	Pattern string `json:"pattern"`
	OneOf   []struct {
		Pattern string `json:"pattern"`
	} `json:"oneOf"`
}

type configSchema struct {
	Required    []string          `json:"required"`
	AnyOf       []json.RawMessage `json:"anyOf"`
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

func TestConfigSchemaIncludesProviderAndTerraformOptions(t *testing.T) {
	schema := loadConfigSchema(t)

	if schema.Definitions.VersionConstraint.Pattern == "" && len(schema.Definitions.VersionConstraint.OneOf) == 0 {
		t.Fatalf("version constraint pattern definition is missing in schema")
	}

	providerVersion, ok := schema.Properties.Providers.Items.Properties["version"]
	if !ok {
		t.Fatalf("provider version schema is missing")
	}
	if !referencesVersionConstraint(t, providerVersion) {
		t.Fatalf("provider version should reference the shared version constraint definition")
	}

	if !contains(schema.Properties.Providers.Items.Required, "name") {
		t.Errorf("provider schema should require 'name'")
	}
	if !contains(schema.Properties.Providers.Items.Required, "version") {
		t.Errorf("provider schema should require 'version'")
	}

	requiredAnyOf := requiredOptionsFromAnyOf(t, schema.AnyOf)
	for _, key := range []string{"modules", "providers", "terraform_version"} {
		if !requiredAnyOf[key] {
			t.Fatalf("schema anyOf should require at least one of modules/providers/terraform_version, missing %s", key)
		}
	}

	if len(schema.Properties.Modules.Items.Properties) == 0 {
		t.Fatalf("module properties are missing from schema")
	}
	if moduleVersion, ok := schema.Properties.Modules.Items.Properties["version"]; ok {
		if !referencesVersionConstraint(t, moduleVersion) {
			t.Fatalf("module version should reference the shared version constraint definition")
		}
	} else {
		t.Fatalf("module version schema is missing")
	}

	if !referencesVersionConstraint(t, schema.Properties.TerraformVersion) {
		t.Fatalf("terraform_version should reference the shared version constraint definition")
	}

	for _, field := range schema.Required {
		if field == "modules" {
			t.Errorf("modules should no longer be a required top-level field")
		}
	}
}

func TestConfigSchemaVersionPatternAllowsTerraformConstraints(t *testing.T) {
	schema := loadConfigSchema(t)

	regexes := compileConstraintRegexps(t, schema.Definitions.VersionConstraint)

	validConstraints := []string{
		"1.2.3",
		"v1.0.0",
		"~> 3.0",
		"~>3.0.0-beta.1+build.5",
		">= 1.2, < 2.0",
		"!= 1.0.0",
		"<=1.4.0",
		">= 1.5, < 2.0",
	}

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
}

func contains(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
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

func requiredOptionsFromAnyOf(t *testing.T, clauses []json.RawMessage) map[string]bool {
	t.Helper()

	required := map[string]bool{}

	for _, clause := range clauses {
		var node map[string]any
		if err := json.Unmarshal(clause, &node); err != nil {
			t.Fatalf("failed to parse anyOf clause: %v", err)
		}

		reqList, ok := node["required"].([]any)
		if !ok {
			continue
		}

		for _, item := range reqList {
			if name, ok := item.(string); ok {
				required[name] = true
			}
		}
	}

	return required
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
