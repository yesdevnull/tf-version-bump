package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

type workflowStep struct {
	Name string         `yaml:"name"`
	Uses string         `yaml:"uses"`
	Run  string         `yaml:"run"`
	With map[string]any `yaml:"with"`
	Env  map[string]any `yaml:"env"`
}

type workflowJob struct {
	Needs       any               `yaml:"needs"`
	Permissions map[string]string `yaml:"permissions"`
	Steps       []workflowStep    `yaml:"steps"`
	Uses        string            `yaml:"uses"`
	With        map[string]any    `yaml:"with"`
}

type githubWorkflow struct {
	On   map[string]map[string]any `yaml:"on"`
	Jobs map[string]workflowJob    `yaml:"jobs"`
}

func loadWorkflow(t *testing.T, filename string) githubWorkflow {
	t.Helper()
	workflowData, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("failed to read %s: %v", filename, err)
	}

	var workflow githubWorkflow
	if err := yaml.Unmarshal(workflowData, &workflow); err != nil {
		t.Fatalf("failed to parse %s: %v", filename, err)
	}
	return workflow
}

func TestGoReleaserBuildConfigurationIsDeterministicAndVerifiable(t *testing.T) {
	configData, err := os.ReadFile(".goreleaser.yaml")
	if err != nil {
		t.Fatalf("failed to read GoReleaser config: %v", err)
	}

	var config struct {
		Before struct {
			Hooks []string `yaml:"hooks"`
		} `yaml:"before"`
		Builds []struct {
			Flags        []string `yaml:"flags"`
			Ldflags      []string `yaml:"ldflags"`
			ModTimestamp string   `yaml:"mod_timestamp"`
		} `yaml:"builds"`
		GoMod struct {
			Proxy bool `yaml:"proxy"`
		} `yaml:"gomod"`
	}
	if err := yaml.Unmarshal(configData, &config); err != nil {
		t.Fatalf("failed to parse GoReleaser config: %v", err)
	}
	if len(config.Builds) != 1 {
		t.Fatalf("build count = %d, want 1", len(config.Builds))
	}

	build := config.Builds[0]
	if !slices.Contains(build.Flags, "-trimpath") {
		t.Errorf("build flags = %q, want -trimpath", build.Flags)
	}
	if build.ModTimestamp != "{{ .CommitTimestamp }}" {
		t.Errorf("mod_timestamp = %q, want commit timestamp", build.ModTimestamp)
	}
	ldflags := strings.Join(build.Ldflags, " ")
	if !strings.Contains(ldflags, "{{.CommitDate}}") && !strings.Contains(ldflags, "{{ .CommitDate }}") {
		t.Errorf("ldflags = %q, want commit date", build.Ldflags)
	}
	if strings.Contains(ldflags, "{{.Date}}") || strings.Contains(ldflags, "{{ .Date }}") {
		t.Errorf("ldflags = %q, must not use build time", build.Ldflags)
	}
	if !config.GoMod.Proxy {
		t.Error("gomod.proxy = false, want true")
	}
	if len(config.Before.Hooks) != 0 {
		t.Errorf("before hooks = %q, want none that can mutate tagged source", config.Before.Hooks)
	}
}

func TestCIRejectsUntidyModuleFiles(t *testing.T) {
	workflow := loadWorkflow(t, ".github/workflows/ci.yml")
	for _, step := range workflow.Jobs["test"].Steps {
		if step.Run == "go mod tidy -diff" {
			return
		}
	}
	t.Fatal("CI test job does not run go mod tidy -diff")
}

func TestRequiredStatusWorkflowsRunForEveryMainChange(t *testing.T) {
	for _, filename := range []string{".github/workflows/ci.yml", ".github/workflows/lint.yml"} {
		workflow := loadWorkflow(t, filename)
		for _, event := range []string{"push", "pull_request"} {
			trigger, ok := workflow.On[event]
			if !ok {
				t.Errorf("%s does not run on %s", filename, event)
				continue
			}
			for _, filter := range []string{"paths", "paths-ignore"} {
				if value, exists := trigger[filter]; exists {
					t.Errorf("%s %s %s = %#v, want filter omitted", filename, event, filter, value)
				}
			}
		}
	}
}

func TestGoReleaserCreatesReplaceableDraftRelease(t *testing.T) {
	configData, err := os.ReadFile(".goreleaser.yaml")
	if err != nil {
		t.Fatalf("failed to read GoReleaser config: %v", err)
	}

	var config struct {
		Release struct {
			Draft                bool `yaml:"draft"`
			ReplaceExistingDraft bool `yaml:"replace_existing_draft"`
		} `yaml:"release"`
	}
	if err := yaml.Unmarshal(configData, &config); err != nil {
		t.Fatalf("failed to parse GoReleaser config: %v", err)
	}
	if !config.Release.Draft {
		t.Error("release.draft = false, want true")
	}
	if !config.Release.ReplaceExistingDraft {
		t.Error("release.replace_existing_draft = false, want true")
	}
}

func TestReleaseWorkflowUploadsProvenanceToDraft(t *testing.T) {
	workflow := loadWorkflow(t, ".github/workflows/release.yml")

	draftRelease, ok := workflow.Jobs["assets-provenance"].With["draft-release"].(string)
	if !ok || draftRelease != "true" {
		t.Fatalf("assets-provenance draft-release = %#v, want string %q", workflow.Jobs["assets-provenance"].With["draft-release"], "true")
	}
}

func TestReleaseWorkflowPublishesCompleteDraft(t *testing.T) {
	workflow := loadWorkflow(t, ".github/workflows/release.yml")

	publish := workflow.Jobs["publish-release"]
	needs, ok := publish.Needs.([]any)
	if !ok {
		t.Fatalf("publish-release needs = %#v, want a job list", publish.Needs)
	}
	var dependencyNames []string
	for _, need := range needs {
		if name, ok := need.(string); ok {
			dependencyNames = append(dependencyNames, name)
		}
	}
	for _, required := range []string{"release", "assets-provenance"} {
		if !slices.Contains(dependencyNames, required) {
			t.Errorf("publish-release needs = %q, want %q", dependencyNames, required)
		}
	}
	if publish.Permissions["contents"] != "write" {
		t.Errorf("publish-release contents permission = %q, want write", publish.Permissions["contents"])
	}
	if len(publish.Steps) != 1 {
		t.Fatalf("publish-release step count = %d, want 1", len(publish.Steps))
	}
	step := publish.Steps[0]
	if step.Run != `gh release edit "$TAG" --draft=false` {
		t.Errorf("publish command = %q, want gh release edit with quoted tag", step.Run)
	}
	wantEnv := map[string]string{
		"GH_TOKEN": "${{ github.token }}",
		"GH_REPO":  "${{ github.repository }}",
		"TAG":      "${{ github.ref_name }}",
	}
	for name, want := range wantEnv {
		if got, ok := step.Env[name].(string); !ok || got != want {
			t.Errorf("publish %s = %#v, want %q", name, step.Env[name], want)
		}
	}
}

func TestReleaseWorkflowPinsBuildToolchain(t *testing.T) {
	workflow := loadWorkflow(t, ".github/workflows/release.yml")
	var goVersion, goreleaserVersion any
	for _, step := range workflow.Jobs["release"].Steps {
		switch {
		case strings.HasPrefix(step.Uses, "actions/setup-go@"):
			goVersion = step.With["go-version"]
		case strings.HasPrefix(step.Uses, "goreleaser/goreleaser-action@"):
			goreleaserVersion = step.With["version"]
		}
	}
	if goVersion != "1.26.6" {
		t.Errorf("release Go version = %#v, want %q", goVersion, "1.26.6")
	}
	if goreleaserVersion != "v2.17.1" {
		t.Errorf("release GoReleaser version = %#v, want %q", goreleaserVersion, "v2.17.1")
	}
}

func TestReleaseWorkflowReferencesSLSAGeneratorByExactSemanticVersionTag(t *testing.T) {
	workflow := loadWorkflow(t, ".github/workflows/release.yml")

	const generator = "slsa-framework/slsa-github-generator/.github/workflows/generator_generic_slsa3.yml@"
	reference := workflow.Jobs["assets-provenance"].Uses
	if !strings.HasPrefix(reference, generator) {
		t.Fatalf("assets-provenance uses %q, want the SLSA generic generator", reference)
	}

	version := strings.TrimPrefix(reference, generator)
	exactSemanticVersion := regexp.MustCompile(`^v[1-9]\d*\.\d+\.\d+(?:-rc\.\d+)?$`)
	if !exactSemanticVersion.MatchString(version) {
		t.Fatalf("SLSA generator version = %q, want an exact semantic-version tag", version)
	}
}

func TestUpdateActionsReleasePinUpdatesMaintainedFiles(t *testing.T) {
	repository := copyActionsReleasePinFixture(t)
	const newVersion = "v1.0.0-rc.11"
	const newDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	command := exec.Command("bash", "scripts/update-actions-release-pin.sh", newVersion, newDigest)
	command.Dir = repository
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("update release pin: %v\n%s", err, output)
	}
	if got, want := string(output), "Updated GitHub Actions example pin to v1.0.0-rc.11\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}

	for _, filename := range actionsReleasePinFiles() {
		contents := readTestFile(t, filepath.Join(repository, filename))
		if strings.Contains(contents, "v1.0.0-rc.10") || strings.Contains(contents, "532783cd3c6834a37616ed81ed76ef99ec343cc64d9664dd67c7eb325420c830") {
			t.Errorf("%s retains the previous release pin", filename)
		}
		if !strings.Contains(contents, newVersion) || !strings.Contains(contents, newDigest) {
			t.Errorf("%s does not contain the new release pin", filename)
		}
	}
}

func TestUpdateActionsReleasePinAcceptsStableRelease(t *testing.T) {
	repository := copyActionsReleasePinFixture(t)
	const newVersion = "v1.0.0"
	const newDigest = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	command := exec.Command("bash", "scripts/update-actions-release-pin.sh", newVersion, newDigest)
	command.Dir = repository
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("update to stable release: %v\n%s", err, output)
	}
	for _, filename := range actionsReleasePinFiles() {
		contents := readTestFile(t, filepath.Join(repository, filename))
		if strings.Contains(contents, "1.0.0-rc.10") {
			t.Errorf("%s retains the previous prerelease", filename)
		}
		if !strings.Contains(contents, newVersion) || !strings.Contains(contents, newDigest) {
			t.Errorf("%s does not contain the stable release pin", filename)
		}
	}
	afterFirstUpdate := readActionsReleasePinFiles(t, repository)
	command = exec.Command("bash", "scripts/update-actions-release-pin.sh", newVersion, newDigest)
	command.Dir = repository
	output, err = command.CombinedOutput()
	if err != nil {
		t.Fatalf("repeat stable release-pin update: %v\n%s", err, output)
	}
	if afterSecondUpdate := readActionsReleasePinFiles(t, repository); !reflect.DeepEqual(afterSecondUpdate, afterFirstUpdate) {
		t.Fatal("repeated stable release-pin update changed maintained files")
	}
}

func TestUpdateActionsReleasePinAcceptsBuildMetadata(t *testing.T) {
	repository := copyActionsReleasePinFixture(t)
	const newVersion = "v1.0.0+build.7"
	const newDigest = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

	command := exec.Command("bash", "scripts/update-actions-release-pin.sh", newVersion, newDigest)
	command.Dir = repository
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("update to release with build metadata: %v\n%s", err, output)
	}
	for _, filename := range actionsReleasePinFiles() {
		contents := readTestFile(t, filepath.Join(repository, filename))
		if !strings.Contains(contents, newVersion) || !strings.Contains(contents, newDigest) {
			t.Errorf("%s does not contain the release pin with build metadata", filename)
		}
	}
}

func TestUpdateActionsReleasePinIsIdempotent(t *testing.T) {
	repository := copyActionsReleasePinFixture(t)
	before := readActionsReleasePinFiles(t, repository)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, "bash", "scripts/update-actions-release-pin.sh", "v1.0.0-rc.10", "532783cd3c6834a37616ed81ed76ef99ec343cc64d9664dd67c7eb325420c830")
	command.Dir = repository
	command.WaitDelay = time.Second
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("already-current release-pin update did not terminate")
	}
	if err != nil {
		t.Fatalf("repeat release-pin update: %v\n%s", err, output)
	}
	if after := readActionsReleasePinFiles(t, repository); !reflect.DeepEqual(after, before) {
		t.Fatal("already-current release-pin update changed maintained files")
	}
}

func TestUpdateActionsReleasePinRejectsInvalidInputWithoutChanges(t *testing.T) {
	testCases := []struct {
		name string
		args []string
	}{
		{name: "missing arguments"},
		{name: "version without tag prefix", args: []string{"1.0.0-rc.11", strings.Repeat("a", 64)}},
		{name: "malformed version", args: []string{"v1.0", strings.Repeat("a", 64)}},
		{name: "leading zero in core version", args: []string{"v01.0.0", strings.Repeat("a", 64)}},
		{name: "leading zero in numeric prerelease", args: []string{"v1.0.0-01", strings.Repeat("a", 64)}},
		{name: "short digest", args: []string{"v1.0.0-rc.11", "abc123"}},
		{name: "extra argument", args: []string{"v1.0.0-rc.11", strings.Repeat("a", 64), "unexpected"}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			repository := copyActionsReleasePinFixture(t)
			before := readActionsReleasePinFiles(t, repository)
			command := exec.Command("bash", append([]string{"scripts/update-actions-release-pin.sh"}, testCase.args...)...)
			command.Dir = repository
			output, err := command.CombinedOutput()
			var exitError *exec.ExitError
			if !errors.As(err, &exitError) || exitError.ExitCode() != 2 {
				t.Fatalf("exit error = %v, output = %q, want status 2", err, output)
			}
			if !strings.Contains(string(output), "Usage: update-actions-release-pin.sh") {
				t.Fatalf("output = %q, want usage", output)
			}
			if after := readActionsReleasePinFiles(t, repository); !reflect.DeepEqual(after, before) {
				t.Fatal("invalid input changed maintained release-pin files")
			}
		})
	}
}

func TestUpdateActionsReleasePinHelp(t *testing.T) {
	repository := copyActionsReleasePinFixture(t)
	command := exec.Command("bash", "scripts/update-actions-release-pin.sh", "--help")
	command.Dir = repository
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("show updater help: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Usage: update-actions-release-pin.sh <version> <linux-x86-64-sha256>") {
		t.Fatalf("output = %q, want updater usage", output)
	}
}

func TestUpdateActionsReleasePinRejectsUnexpectedLayoutWithoutPartialChanges(t *testing.T) {
	repository := copyActionsReleasePinFixture(t)
	guide := filepath.Join(repository, "docs", "ADVANCED-USAGE.md")
	contents := readTestFile(t, guide)
	contents = strings.Replace(contents, "532783cd3c6834a37616ed81ed76ef99ec343cc64d9664dd67c7eb325420c830", strings.Repeat("b", 64), 1)
	if err := os.WriteFile(guide, []byte(contents), 0o644); err != nil {
		t.Fatalf("write mismatched guide fixture: %v", err)
	}
	before := readActionsReleasePinFiles(t, repository)

	command := exec.Command("bash", "scripts/update-actions-release-pin.sh", "v1.0.0-rc.11", strings.Repeat("a", 64))
	command.Dir = repository
	output, err := command.CombinedOutput()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 1 {
		t.Fatalf("exit error = %v, output = %q, want status 1", err, output)
	}
	if !strings.Contains(string(output), "docs/ADVANCED-USAGE.md") {
		t.Fatalf("output = %q, want mismatched filename", output)
	}
	if after := readActionsReleasePinFiles(t, repository); !reflect.DeepEqual(after, before) {
		t.Fatal("unexpected layout caused partial release-pin changes")
	}
}

func TestUpdateActionsReleasePinRejectsDivergentDuplicateFieldWithoutPartialChanges(t *testing.T) {
	repository := copyActionsReleasePinFixture(t)
	workflow := filepath.Join(repository, "examples", "github-actions", ".github", "workflows", "tf-version-bump-nonproduction.yml")
	contents := readTestFile(t, workflow)
	currentField := "      tf_version_bump_version: v1.0.0-rc.10"
	contents = strings.Replace(contents, currentField, currentField+"\n      tf_version_bump_version: v0.9.0", 1)
	if err := os.WriteFile(workflow, []byte(contents), 0o644); err != nil {
		t.Fatalf("write duplicate-field fixture: %v", err)
	}
	before := readActionsReleasePinFiles(t, repository)

	command := exec.Command("bash", "scripts/update-actions-release-pin.sh", "v1.0.0-rc.11", strings.Repeat("a", 64))
	command.Dir = repository
	output, err := command.CombinedOutput()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 1 {
		t.Fatalf("exit error = %v, output = %q, want status 1", err, output)
	}
	if !strings.Contains(string(output), "tf-version-bump-nonproduction.yml") {
		t.Fatalf("output = %q, want duplicate-field filename", output)
	}
	if after := readActionsReleasePinFiles(t, repository); !reflect.DeepEqual(after, before) {
		t.Fatal("divergent duplicate field caused partial release-pin changes")
	}
}

func TestUpdateActionsReleasePinRejectsUnwritableTargetWithoutPartialChanges(t *testing.T) {
	repository := copyActionsReleasePinFixture(t)
	workflow := filepath.Join(repository, "examples", "github-actions", ".github", "workflows", "tf-version-bump-nonproduction.yml")
	if err := os.Chmod(workflow, 0o444); err != nil {
		t.Fatalf("make target unwritable: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(workflow, 0o644); err != nil {
			t.Errorf("restore target permissions: %v", err)
		}
	})
	before := readActionsReleasePinFiles(t, repository)

	command := exec.Command("bash", "scripts/update-actions-release-pin.sh", "v1.0.0-rc.11", strings.Repeat("a", 64))
	command.Dir = repository
	output, err := command.CombinedOutput()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 1 {
		t.Fatalf("exit error = %v, output = %q, want status 1", err, output)
	}
	if !strings.Contains(string(output), "tf-version-bump-nonproduction.yml") {
		t.Fatalf("output = %q, want unwritable filename", output)
	}
	if after := readActionsReleasePinFiles(t, repository); !reflect.DeepEqual(after, before) {
		t.Fatal("unwritable target caused partial release-pin changes")
	}
}

func TestRequiredStatusWorkflowDocumentationMatchesTriggers(t *testing.T) {
	contents := readTestFile(t, "CLAUDE.md")
	for _, outdatedClaim := range []string{"skipping `**/*.md`", "only on Go/dependency file changes"} {
		if strings.Contains(contents, outdatedClaim) {
			t.Errorf("CLAUDE.md retains outdated workflow claim %q", outdatedClaim)
		}
	}
	if !strings.Contains(contents, "CI/Build and Lint run for every push and pull request targeting `main`") {
		t.Fatal("CLAUDE.md does not document unconditional required status workflows")
	}
}

func actionsReleasePinFiles() []string {
	return []string{
		"examples/github-actions/.github/workflows/tf-version-bump-production.yml",
		"examples/github-actions/.github/workflows/tf-version-bump-nonproduction.yml",
		"examples/github-actions/test.sh",
		"examples/github-actions/README.md",
		"docs/ADVANCED-USAGE.md",
	}
}

func readActionsReleasePinFiles(t *testing.T, repository string) map[string]string {
	t.Helper()
	contents := make(map[string]string)
	for _, filename := range actionsReleasePinFiles() {
		contents[filename] = readTestFile(t, filepath.Join(repository, filename))
	}
	return contents
}

func copyActionsReleasePinFixture(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	filenames := append(actionsReleasePinFiles(), "scripts/update-actions-release-pin.sh")
	for _, filename := range filenames {
		contents, err := os.ReadFile(filename)
		if err != nil {
			t.Fatalf("read fixture %s: %v", filename, err)
		}
		target := filepath.Join(repository, filename)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatalf("create fixture directory for %s: %v", filename, err)
		}
		if err := os.WriteFile(target, contents, 0o755); err != nil {
			t.Fatalf("write fixture %s: %v", filename, err)
		}
	}
	return repository
}
