package main

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"
)

type versionSchema struct {
	Pattern string `json:"pattern"`
}

type configSchema struct {
	Required   []string `json:"required"`
	Properties struct {
		Modules struct {
			Items struct {
				Properties struct {
					Version versionSchema `json:"version"`
				} `json:"properties"`
			} `json:"items"`
		} `json:"modules"`
		TerraformVersion versionSchema `json:"terraform_version"`
		Providers        struct {
			Items struct {
				Required   []string `json:"required"`
				Properties struct {
					Version versionSchema `json:"version"`
				} `json:"properties"`
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

	if schema.Properties.TerraformVersion.Pattern == "" {
		t.Fatalf("terraform_version pattern is missing in schema")
	}

	providerPattern := schema.Properties.Providers.Items.Properties.Version.Pattern
	if providerPattern == "" {
		t.Fatalf("provider version pattern is missing in schema")
	}

	if !contains(schema.Properties.Providers.Items.Required, "name") {
		t.Errorf("provider schema should require 'name'")
	}
	if !contains(schema.Properties.Providers.Items.Required, "version") {
		t.Errorf("provider schema should require 'version'")
	}

	for _, field := range schema.Required {
		if field == "modules" {
			t.Errorf("modules should no longer be a required top-level field")
		}
	}
}

func TestConfigSchemaVersionPatternAllowsTerraformConstraints(t *testing.T) {
	schema := loadConfigSchema(t)

	modulePattern := schema.Properties.Modules.Items.Properties.Version.Pattern
	providerPattern := schema.Properties.Providers.Items.Properties.Version.Pattern
	terraformPattern := schema.Properties.TerraformVersion.Pattern

	if modulePattern == "" {
		t.Fatalf("module version pattern is missing in schema")
	}

	if modulePattern != providerPattern {
		t.Fatalf("provider version pattern should match module version pattern")
	}

	if modulePattern != terraformPattern {
		t.Fatalf("terraform version pattern should match module version pattern")
	}

	re, err := regexp.Compile(modulePattern)
	if err != nil {
		t.Fatalf("failed to compile module version pattern: %v", err)
	}

	validConstraints := []string{
		"1.2.3",
		"v1.0.0",
		"~> 3.0",
		"~>3.0.0-beta.1+build.5",
		">= 1.2, < 2.0",
		"!= 1.0.0",
		"<=1.4.0",
		">= 1.5 < 2.0",
	}

	for _, constraint := range validConstraints {
		if !re.MatchString(constraint) {
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
