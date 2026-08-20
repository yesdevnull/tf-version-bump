package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type fromDocument struct {
	From FromVersions `yaml:"from"`
}

func TestFromVersionsUnmarshalYAML(t *testing.T) {
	tests := []struct {
		name, yaml, errString string
		want                  FromVersions
	}{
		{name: "scalar", yaml: "from: 4.0.0\n", want: FromVersions{"4.0.0"}},
		{name: "array", yaml: "from: [4.0.0, \"~> 3.0\"]\n", want: FromVersions{"4.0.0", "~> 3.0"}},
		{name: "empty scalar", yaml: "from: \"\"\n", want: FromVersions{}},
		{name: "number", yaml: "from: 4\n", errString: "must be a string or array of strings"},
		{name: "array with number", yaml: "from: [4.0.0, 4]\n", errString: "array contains non-string values"},
		{name: "mapping", yaml: "from: {old: 4.0.0}\n", errString: "must be either a string or an array of strings"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got fromDocument
			err := yaml.NewDecoder(strings.NewReader(tt.yaml)).Decode(&got)
			if tt.errString != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errString) {
					t.Fatalf("error = %v, want component %q", err, tt.errString)
				}
				return
			}
			if err != nil {
				t.Fatalf("decode failed: %v", err)
			}
			if !slices.Equal(got.From, tt.want) {
				t.Errorf("from = %#v, want %#v", got.From, tt.want)
			}
		})
	}
}

func TestLoadConfigSanitisesAllOperations(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "config.yml")
	data := `terraform_version: "  >= 1.6  "
providers:
  - name: " aws "
    version: " ~> 5.0 "
modules:
  - source: " terraform-aws-modules/vpc/aws "
    version: " 5.0.0 "
    from: " 4.0.0 "
    ignore_versions: [" 3.0.0 ", " ~> 3.0 "]
    ignore_modules: [" legacy-* ", " ", " *-test "]
`
	if err := os.WriteFile(configFile, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	want := &Config{
		TerraformVersion: ">= 1.6",
		Providers:        []ProviderUpdate{{Name: "aws", Version: "~> 5.0"}},
		Modules: []ModuleUpdate{{
			Source: "terraform-aws-modules/vpc/aws", Version: "5.0.0",
			From: FromVersions{"4.0.0"}, IgnoreVersions: FromVersions{"3.0.0", "~> 3.0"},
			IgnoreModules: []string{"legacy-*", "*-test"},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("config = %#v, want %#v", got, want)
	}
}

func TestLoadConfigRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name, data, want string
		exact            bool
	}{
		{name: "unknown top-level field", data: "unknown: true\nmodules: []\n", want: "field unknown not found"},
		{name: "malformed YAML", data: "modules:\n  - source: \"unterminated\n", want: "failed to parse YAML"},
		{name: "module missing source", data: "modules:\n  - version: 5.0.0\n", want: "module at index 0 is missing 'source' field", exact: true},
		{name: "module missing version", data: "modules:\n  - source: example/module\n", want: "module at index 0 is missing 'version' field", exact: true},
		{name: "provider missing name", data: "providers:\n  - version: 5.0.0\n", want: "provider at index 0 is missing 'name' field", exact: true},
		{name: "provider missing version", data: "providers:\n  - name: aws\n", want: "provider at index 0 is missing 'version' field", exact: true},
		{name: "invalid from mapping", data: "modules:\n  - source: example/module\n    version: 5.0.0\n    from: {old: 4.0.0}\n", want: "must be either a string or an array of strings"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configFile := filepath.Join(t.TempDir(), "config.yml")
			if err := os.WriteFile(configFile, []byte(tt.data), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := loadConfig(configFile)
			if err == nil || (tt.exact && err.Error() != tt.want) || (!tt.exact && !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("error = %v, want component %q", err, tt.want)
			}
		})
	}
}

func TestLoadConfigReadError(t *testing.T) {
	_, err := loadConfig(filepath.Join(t.TempDir(), "missing.yml"))
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want os.ErrNotExist", err)
	}
	if !strings.Contains(err.Error(), "failed to read config file:") {
		t.Errorf("error = %v, want read wrapper", err)
	}
}

func TestLoadConfigEmptyDocument(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "empty.yml")
	if err := os.WriteFile(configFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	if !reflect.DeepEqual(got, &Config{}) {
		t.Errorf("config = %#v, want empty config", got)
	}
}
