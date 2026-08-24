package main

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

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
