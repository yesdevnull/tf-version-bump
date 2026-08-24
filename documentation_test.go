package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"unicode"
)

func TestExampleConfigsMatchDocumentedContract(t *testing.T) {
	configFiles := append(globTestFiles(t, "examples/config-*.yml"), globTestFiles(t, "examples/github-actions/.github/tf-version-bump/*.yml")...)
	configFiles = append(configFiles, globTestFiles(t, "examples/scenarios/*/config.yml")...)
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

func TestDocumentationExampleScenariosRunSuccessfully(t *testing.T) {
	assertBashExampleSucceeds(t, "examples/run-scenarios.sh", "Example scenarios passed\n")
}

func TestDocumentationPreCommitHookRunsSuccessfully(t *testing.T) {
	assertBashExampleSucceeds(t, "examples/pre-commit-hook_test.sh", "Pre-commit hook examples passed\n")
}

func TestDocumentationPreCommitHarnessReportsMissingCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("restricted PATH test uses Unix symbolic links")
	}
	binDirectory := t.TempDir()
	for _, dependency := range []string{"chmod", "cmp", "cp", "dirname", "env", "git", "go", "grep", "mkdir", "mktemp", "rm", "sed"} {
		path, err := exec.LookPath(dependency)
		if err != nil {
			t.Skipf("%s is required to test a restricted PATH", dependency)
		}
		if err := os.Symlink(path, filepath.Join(binDirectory, dependency)); err != nil {
			t.Fatalf("link %s into restricted PATH: %v", dependency, err)
		}
	}

	command := exec.Command(bashPath(t), "examples/pre-commit-hook_test.sh")
	command.Env = append(os.Environ(), "PATH="+binDirectory)
	output, err := command.CombinedOutput()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 1 {
		t.Fatalf("exit error = %v, output = %q, want status 1", err, output)
	}
	if got, want := string(output), "Pre-commit hook test failure: required command not found: cat\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func assertBashExampleSucceeds(t *testing.T, script, expectedOutput string) {
	t.Helper()
	command := exec.Command(bashPath(t), script)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run %s: %v\n%s", script, err, output)
	}
	if got := string(output); got != expectedOutput {
		t.Fatalf("%s output = %q, want %q", script, got, expectedOutput)
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
		for _, line := range markdownLinesOutsideTopLevelFences(string(contents)) {
			for _, match := range linkPattern.FindAllStringSubmatch(line, -1) {
				brokenLink, err := documentationLinkError(document, match[1], anchorCache)
				if err != nil {
					return nil, err
				}
				if brokenLink != "" {
					brokenLinks = append(brokenLinks, brokenLink)
				}
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
	headingPattern := regexp.MustCompile(`^#{1,6}[ \t]+(.+?)[ \t]*#*[ \t]*$`)
	for _, line := range markdownLinesOutsideTopLevelFences(contents) {
		match := headingPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
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

func markdownLinesOutsideTopLevelFences(contents string) []string {
	var rendered []string
	var fenceCharacter byte
	var fenceLength int

	for _, line := range strings.Split(contents, "\n") {
		character, length, remainder, isFence := markdownFence(line)
		if fenceCharacter == 0 {
			if isFence {
				fenceCharacter = character
				fenceLength = length
				continue
			}
			rendered = append(rendered, line)
			continue
		}

		if isFence && character == fenceCharacter && length >= fenceLength && strings.Trim(remainder, " \t") == "" {
			fenceCharacter = 0
			fenceLength = 0
		}
	}

	return rendered
}

func markdownFence(line string) (character byte, length int, remainder string, ok bool) {
	line = strings.TrimSuffix(line, "\r")
	indent := 0
	for indent < len(line) && line[indent] == ' ' {
		indent++
	}
	if indent > 3 || indent == len(line) {
		return 0, 0, "", false
	}

	character = line[indent]
	if character != '`' && character != '~' {
		return 0, 0, "", false
	}
	end := indent
	for end < len(line) && line[end] == character {
		end++
	}
	length = end - indent
	if length < 3 {
		return 0, 0, "", false
	}
	remainder = line[end:]
	if character == '`' && strings.ContainsRune(remainder, '`') {
		return 0, 0, "", false
	}
	return character, length, remainder, true
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

func TestDocumentationLocalLinksIgnoreFencedCode(t *testing.T) {
	directory := t.TempDir()
	writeTestFile(t, directory, "target.md", "# Target\n")
	document := writeTestFile(t, directory, "guide.md", "```markdown\n[backtick](missing-backtick.md)\n```\n\n~~~~markdown\n[tilde](missing-tilde.md)\n~~~~\n\n[rendered](target.md)\n")

	brokenLinks, err := documentationLinkErrors([]string{document})
	if err != nil {
		t.Fatal(err)
	}
	if len(brokenLinks) != 0 {
		t.Fatalf("broken links = %q, want none", brokenLinks)
	}
}

func TestDocumentationLocalLinksDoNotSpanFencedCode(t *testing.T) {
	document := writeTestFile(t, t.TempDir(), "guide.md", "[not a link\n```text\ncode\n```\n](missing.md)\n")

	brokenLinks, err := documentationLinkErrors([]string{document})
	if err != nil {
		t.Fatal(err)
	}
	if len(brokenLinks) != 0 {
		t.Fatalf("broken links = %q, want none", brokenLinks)
	}
}

func TestMarkdownHeadingAnchorsIgnoreFencedCode(t *testing.T) {
	contents := "```sh\n# Backtick only\n# Repeat\n```\n\n~~~sh\n# Tilde only\n~~~\n\n# Repeat\n## Repeat\n"

	anchors := markdownHeadingAnchors(contents)
	want := map[string]struct{}{"repeat": {}, "repeat-1": {}}
	if len(anchors) != len(want) {
		t.Fatalf("anchors = %q, want %q", anchors, want)
	}
	for anchor := range want {
		if _, ok := anchors[anchor]; !ok {
			t.Errorf("anchors = %q, want %q", anchors, want)
		}
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
