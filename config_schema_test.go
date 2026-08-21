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
	Required []string `json:"required"`
	AnyOf    []struct {
		Required []string `json:"required"`
	} `json:"anyOf"`
	Definitions struct {
		VersionConstraint versionSchema `json:"versionConstraint"`
	} `json:"definitions"`
	Properties struct {
		Modules struct {
			Type  string `json:"type"`
			Items struct {
				Type                 string                     `json:"type"`
				Required             []string                   `json:"required"`
				AdditionalProperties *bool                      `json:"additionalProperties"`
				Properties           map[string]json.RawMessage `json:"properties"`
			} `json:"items"`
		} `json:"modules"`
		TerraformVersion json.RawMessage `json:"terraform_version"`
		Providers        struct {
			Type  string `json:"type"`
			Items struct {
				Required             []string                   `json:"required"`
				AdditionalProperties *bool                      `json:"additionalProperties"`
				Properties           map[string]json.RawMessage `json:"properties"`
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
	assertVersionConstraintNode(t, "terraform_version", schema.Properties.TerraformVersion)
	if schema.Properties.Providers.Type != "array" {
		t.Fatal("providers should be an array")
	}
	if schema.Properties.Modules.Type != "array" {
		t.Fatal("modules should be an array")
	}
	if schema.Properties.Modules.Items.Type != "object" {
		t.Fatal("module items should be objects")
	}
	assertExactRequiredFields(t, "module", schema.Properties.Modules.Items.Required, "source", "version")
	if schema.Properties.Modules.Items.AdditionalProperties == nil || *schema.Properties.Modules.Items.AdditionalProperties {
		t.Fatal("module items should disallow additional properties")
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
	if len(schema.Required) != 0 {
		t.Fatalf("schema has unconditional required fields = %v, want none", schema.Required)
	}
	assertExactRequiredFields(t, "provider", schema.Properties.Providers.Items.Required, "name", "version")
	if schema.Properties.Providers.Items.AdditionalProperties == nil || *schema.Properties.Providers.Items.AdditionalProperties {
		t.Fatal("provider items should disallow additional properties")
	}

	providerVersion, ok := schema.Properties.Providers.Items.Properties["version"]
	if !ok {
		t.Fatal("provider version schema is missing")
	}
	assertVersionConstraintNode(t, "provider version", providerVersion)

	moduleVersion, ok := schema.Properties.Modules.Items.Properties["version"]
	if !ok {
		t.Fatal("module version schema is missing")
	}
	assertVersionConstraintNode(t, "module version", moduleVersion)

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

	validConstraints := []string{"1.2.3", "v1.0.0", "~> 3.0", "!= 1.0.0", "<=1.4.0", ">= 1.5, < 2.0", "~> 3.0.0-beta.1+build.5"}

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

	for _, constraint := range []string{"invalid", "1.2.3.4"} {
		for _, re := range regexes {
			if re.MatchString(constraint) {
				t.Errorf("expected schema pattern to reject %q", constraint)
			}
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
		shape, ok := schemaOptionShape(t, option)
		if !ok {
			return false
		}
		found[shape] = true
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

func schemaOptionShape(t *testing.T, raw json.RawMessage) (string, bool) {
	t.Helper()
	var entry map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatalf("failed to parse oneOf option: %v", err)
	}

	if _, ok := entry["type"]; ok {
		return schemaArrayOptionShape(entry)
	}
	return schemaScalarOptionShape(entry)
}

func schemaArrayOptionShape(entry map[string]json.RawMessage) (string, bool) {
	typeRaw, ok := entry["type"]
	var typeName string
	if !ok || json.Unmarshal(typeRaw, &typeName) != nil || typeName != "array" ||
		!schemaOptionKeysAllowed(entry, "type", "items", "minItems") {
		return "", false
	}

	items, ok := entry["items"]
	if !ok || !isExactVersionConstraintReference(items) {
		return "", false
	}
	var minItems int
	minItemsRaw, ok := entry["minItems"]
	if !ok || json.Unmarshal(minItemsRaw, &minItems) != nil || minItems != 1 {
		return "", false
	}
	return "array", true
}

func schemaScalarOptionShape(entry map[string]json.RawMessage) (string, bool) {
	if !schemaOptionKeysAllowed(entry, "allOf") {
		return "", false
	}
	allOf, ok := entry["allOf"]
	if !ok {
		return "", false
	}
	var assertions []json.RawMessage
	if json.Unmarshal(allOf, &assertions) != nil || len(assertions) == 0 {
		return "", false
	}
	refCount := 0
	for _, assertion := range assertions {
		var assertionObject map[string]json.RawMessage
		if json.Unmarshal(assertion, &assertionObject) != nil {
			return "", false
		}
		if assertionObject == nil {
			return "", false
		}
		if isExactVersionConstraintReference(assertion) {
			refCount++
			continue
		}
		if !schemaOptionKeysAllowed(assertionObject) {
			return "", false
		}
	}
	if refCount == 1 {
		return "string", true
	}
	return "", false
}

var schemaAnnotationKeys = map[string]struct{}{
	"$comment":    {},
	"default":     {},
	"description": {},
	"examples":    {},
	"readOnly":    {},
	"title":       {},
	"writeOnly":   {},
}

func schemaOptionKeysAllowed(node map[string]json.RawMessage, assertionKeys ...string) bool {
	allowed := make(map[string]struct{}, len(assertionKeys))
	for _, key := range assertionKeys {
		allowed[key] = struct{}{}
	}
	for key := range node {
		if _, ok := schemaAnnotationKeys[key]; ok {
			continue
		}
		if _, ok := allowed[key]; !ok {
			return false
		}
	}
	return true
}

func isExactVersionConstraintReference(raw json.RawMessage) bool {
	var node map[string]json.RawMessage
	if json.Unmarshal(raw, &node) != nil || len(node) != 1 {
		return false
	}
	ref, ok := node["$ref"]
	if !ok {
		return false
	}
	var reference string
	return json.Unmarshal(ref, &reference) == nil && reference == "#/definitions/versionConstraint"
}

func assertVersionConstraintNode(t *testing.T, name string, raw json.RawMessage) {
	t.Helper()
	shape, ok := schemaOptionShape(t, raw)
	if !ok || shape != "string" {
		t.Fatalf("%s should be exactly one version-constraint reference plus annotation-only metadata", name)
	}
}

func assertExactRequiredFields(t *testing.T, name string, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s required fields = %v, want exactly %v", name, got, want)
	}
	for _, field := range want {
		if !slices.Contains(got, field) {
			t.Fatalf("%s required fields = %v, want exactly %v", name, got, want)
		}
	}
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
