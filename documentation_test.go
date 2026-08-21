package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tfaddr "github.com/hashicorp/terraform-registry-address"
)

func TestExampleConfigsMatchDocumentedContract(t *testing.T) {
	configFiles := append(globTestFiles(t, "examples/config-*.yml"), globTestFiles(t, "examples/github-actions/.github/tf-version-bump/*.yml")...)
	constraintPatterns := compileConstraintRegexps(t, loadConfigSchema(t).Definitions.VersionConstraint)

	for _, filename := range configFiles {
		config, err := loadConfig(filename)
		if err != nil {
			t.Fatalf("load %s: %v", filename, err)
		}
		for _, module := range config.Modules {
			if _, err := tfaddr.ParseModuleSource(module.Source); err != nil {
				t.Errorf("%s uses non-registry module source %q: %v", filename, module.Source, err)
			}
			assertDocumentedVersionConstraint(t, filename, "module version", module.Version, constraintPatterns)
			for _, version := range module.From {
				assertDocumentedVersionConstraint(t, filename, "module from filter", version, constraintPatterns)
			}
			for _, version := range module.IgnoreVersions {
				assertDocumentedVersionConstraint(t, filename, "module ignore_versions filter", version, constraintPatterns)
			}
		}
		if config.TerraformVersion != "" {
			assertDocumentedVersionConstraint(t, filename, "Terraform version", config.TerraformVersion, constraintPatterns)
		}
		for _, provider := range config.Providers {
			assertDocumentedVersionConstraint(t, filename, "provider version", provider.Version, constraintPatterns)
		}
	}
}

func assertDocumentedVersionConstraint(t *testing.T, filename, field, value string, patterns []*regexp.Regexp) {
	t.Helper()
	for _, pattern := range patterns {
		if pattern.MatchString(value) {
			return
		}
	}
	t.Errorf("%s has %s %q that does not match the JSON Schema", filename, field, value)
}

func TestDocumentationLocalLinksResolve(t *testing.T) {
	documents := []string{"README.md", "CLAUDE.md", "AGENTS.md", "examples/README.md"}
	documents = append(documents, globTestFiles(t, "docs/*.md")...)
	documents = append(documents, globTestFiles(t, "examples/*/README.md")...)

	linkPattern := regexp.MustCompile(`\[[^]]+\]\(([^)]+)\)`)
	for _, document := range documents {
		contents, err := os.ReadFile(document)
		if err != nil {
			t.Fatalf("read %s: %v", document, err)
		}
		for _, match := range linkPattern.FindAllStringSubmatch(string(contents), -1) {
			target := strings.SplitN(match[1], "#", 2)[0]
			if target == "" || strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(document), target))
			if _, err := os.Stat(resolved); err != nil {
				t.Errorf("%s links to %q: %v", document, target, err)
			}
		}
	}
}

func globTestFiles(t *testing.T, pattern string) []string {
	t.Helper()
	files, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob %q: %v", pattern, err)
	}
	if len(files) == 0 {
		t.Fatalf("glob %q matched no files", pattern)
	}
	return files
}
