package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
	linkPattern := regexp.MustCompile(`\[[^]]+\]\(([^)]+)\)`)
	for _, document := range markdownTestFiles(t) {
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

func markdownTestFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == ".superpowers" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".md") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk Markdown files: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("found no Markdown files")
	}
	return files
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
