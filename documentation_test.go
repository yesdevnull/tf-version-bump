package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode"
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
	brokenLinks, err := documentationLinkErrors(markdownTestFiles(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, brokenLink := range brokenLinks {
		t.Error(brokenLink)
	}
}

func documentationLinkErrors(documents []string) ([]string, error) {
	var brokenLinks []string
	anchorCache := make(map[string]map[string]struct{})
	linkPattern := regexp.MustCompile(`\[[^]]+\]\(([^)]+)\)`)
	for _, document := range documents {
		contents, err := os.ReadFile(document)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", document, err)
		}
		for _, match := range linkPattern.FindAllStringSubmatch(string(contents), -1) {
			brokenLink, err := documentationLinkError(document, match[1], anchorCache)
			if err != nil {
				return nil, err
			}
			if brokenLink != "" {
				brokenLinks = append(brokenLinks, brokenLink)
			}
		}
	}
	return brokenLinks, nil
}

func documentationLinkError(document, link string, anchorCache map[string]map[string]struct{}) (string, error) {
	target, fragment, hasFragment := strings.Cut(link, "#")
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "mailto:") {
		return "", nil
	}
	resolved := document
	if target != "" {
		resolved = filepath.Clean(filepath.Join(filepath.Dir(document), target))
		if _, err := os.Stat(resolved); err != nil {
			return fmt.Sprintf("%s links to %q: %v", document, target, err), nil
		}
	}
	if !hasFragment || fragment == "" || !strings.EqualFold(filepath.Ext(resolved), ".md") {
		return "", nil
	}
	anchors, ok := anchorCache[resolved]
	if !ok {
		targetContents, err := os.ReadFile(resolved)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", resolved, err)
		}
		anchors = markdownHeadingAnchors(string(targetContents))
		anchorCache[resolved] = anchors
	}
	if _, ok := anchors[fragment]; !ok {
		return fmt.Sprintf("%s links to missing heading %q in %s", document, "#"+fragment, resolved), nil
	}
	return "", nil
}

func markdownHeadingAnchors(contents string) map[string]struct{} {
	anchors := make(map[string]struct{})
	headingPattern := regexp.MustCompile(`(?m)^#{1,6}[ \t]+(.+?)[ \t]*#*[ \t]*$`)
	for _, match := range headingPattern.FindAllStringSubmatch(contents, -1) {
		base := markdownHeadingAnchor(match[1])
		anchor := base
		for suffix := 1; ; suffix++ {
			if _, exists := anchors[anchor]; !exists {
				break
			}
			anchor = fmt.Sprintf("%s-%d", base, suffix)
		}
		anchors[anchor] = struct{}{}
	}
	return anchors
}

func markdownHeadingAnchor(heading string) string {
	var anchor strings.Builder
	for _, character := range strings.ToLower(heading) {
		switch {
		case unicode.IsLetter(character), unicode.IsNumber(character), character == '-', character == '_':
			anchor.WriteRune(character)
		case unicode.IsSpace(character):
			anchor.WriteByte('-')
		}
	}
	return anchor.String()
}

func TestDocumentationLocalLinksRejectMissingAnchor(t *testing.T) {
	document := writeTestFile(t, t.TempDir(), "guide.md", "# Existing Heading\n\n[valid](#existing-heading)\n[missing](#missing-heading)\n")

	brokenLinks, err := documentationLinkErrors([]string{document})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{fmt.Sprintf("%s links to missing heading %q in %s", document, "#missing-heading", document)}
	if !slices.Equal(brokenLinks, want) {
		t.Fatalf("broken links = %q, want %q", brokenLinks, want)
	}
}

func TestDocumentationLocalLinksAcceptDuplicateHeadingAnchor(t *testing.T) {
	directory := t.TempDir()
	writeTestFile(t, directory, "target.md", "# `Repeat`!\n\n## Repeat\n")
	document := writeTestFile(t, directory, "guide.md", "[second heading](target.md#repeat-1)\n")

	brokenLinks, err := documentationLinkErrors([]string{document})
	if err != nil {
		t.Fatal(err)
	}
	if len(brokenLinks) != 0 {
		t.Fatalf("broken links = %q, want none", brokenLinks)
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
