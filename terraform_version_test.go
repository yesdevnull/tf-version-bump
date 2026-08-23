package main

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestUpdateTerraformVersionWithCountIdentifiesChangedBlocks(t *testing.T) {
	input := `terraform {
  required_version = ">= 1.0"
}

terraform {
  required_version = "${">= 1.5"}"
}

terraform {
}
`
	filename := writeTestFile(t, t.TempDir(), "main.tf", input)

	updated, changedBlocks, err := updateTerraformVersionWithCount(filename, ">= 1.5", true)
	if err != nil {
		t.Fatalf("updateTerraformVersionWithCount returned error: %v", err)
	}
	if !updated {
		t.Fatal("updated = false, want true")
	}
	if want := []int{0, 2}; !reflect.DeepEqual(changedBlocks, want) {
		t.Fatalf("changed blocks = %v, want %v", changedBlocks, want)
	}
	if got := readTestFile(t, filename); got != input {
		t.Fatalf("dry run content = %q, want unchanged %q", got, input)
	}
}

func TestUpdateTerraformVersionContract(t *testing.T) {
	tests := []struct {
		name, input, want string
		wantUpdated       bool
	}{
		{
			name: "updates required version and preserves providers",
			input: `terraform {
  required_version = ">= 1.0"

  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}
`,
			want: `terraform {
  required_version = ">= 1.5"

  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}
`,
			wantUpdated: true,
		},
		{
			name: "updates every terraform block",
			input: `terraform {
  required_version = ">= 1.0"
}

terraform {
  required_version = ">= 1.1"
}
`,
			want: `terraform {
  required_version = ">= 1.5"
}

terraform {
  required_version = ">= 1.5"
}
`,
			wantUpdated: true,
		},
		{
			name: "leaves file without terraform block unchanged",
			input: `module "vpc" {
  source = "terraform-aws-modules/vpc/aws"
}
`,
			want: `module "vpc" {
  source = "terraform-aws-modules/vpc/aws"
}
`,
		},
		{
			name: "updates differing block when another already matches",
			input: `terraform {
  required_version = ">= 1.5"
}

terraform {
  required_version = ">= 1.0"
}
`,
			want: `terraform {
  required_version = ">= 1.5"
}

terraform {
  required_version = ">= 1.5"
}
`,
			wantUpdated: true,
		},
		{
			name: "adds required version to terraform block without one",
			input: `terraform {
  required_providers {
    aws {
      source = "hashicorp/aws"
    }
  }
}
`,
			want: `terraform {
  required_providers {
    aws {
      source = "hashicorp/aws"
    }
  }
  required_version = ">= 1.5"
}
`,
			wantUpdated: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filename := writeTestFile(t, t.TempDir(), "main.tf", tt.input)
			updated, err := updateTerraformVersion(filename, ">= 1.5", false)
			if err != nil {
				t.Fatalf("updateTerraformVersion returned error: %v", err)
			}
			if updated != tt.wantUpdated {
				t.Fatalf("updated = %t, want %t", updated, tt.wantUpdated)
			}
			if got := readTestFile(t, filename); got != tt.want {
				t.Errorf("content mismatch:\n--- got ---\n%s--- want ---\n%s", got, tt.want)
			}
		})
	}
}

func TestUpdateTerraformVersionRepairsMatchingNonStringExpression(t *testing.T) {
	filename := writeTestFile(t, t.TempDir(), "main.tf", "terraform {\n  required_version = 1.5\n}\n")

	updated, err := updateTerraformVersion(filename, "1.5", false)
	if err != nil {
		t.Fatalf("updateTerraformVersion returned error: %v", err)
	}
	if !updated {
		t.Fatal("updated = false, want true")
	}
	want := "terraform {\n  required_version = \"1.5\"\n}\n"
	if got := readTestFile(t, filename); got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}

func TestUpdateTerraformVersionMatchingVersionDoesNotWrite(t *testing.T) {
	input := `terraform { required_version = ">= 1.5" }
`
	filename := writeTestFile(t, t.TempDir(), "main.tf", input)
	wantModTime := time.Unix(1, 0)
	if err := os.Chtimes(filename, wantModTime, wantModTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	updated, err := updateTerraformVersion(filename, ">= 1.5", false)
	if err != nil {
		t.Fatalf("updateTerraformVersion returned error: %v", err)
	}
	if updated {
		t.Fatal("updated = true, want false")
	}
	if got := readTestFile(t, filename); got != input {
		t.Errorf("content = %q, want unchanged %q", got, input)
	}
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.ModTime(); !got.Equal(wantModTime) {
		t.Errorf("modification time = %v, want unchanged %v", got, wantModTime)
	}
}

func TestUpdateTerraformVersionPreservesPermissions(t *testing.T) {
	filename := writeTestFile(t, t.TempDir(), "main.tf", "terraform {\n  required_version = \">= 1.0\"\n}\n")
	if err := os.Chmod(filename, 0o640); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	updated, err := updateTerraformVersion(filename, ">= 1.5", false)
	if err != nil {
		t.Fatalf("updateTerraformVersion returned error: %v", err)
	}
	if !updated {
		t.Fatal("updated = false, want true")
	}
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Errorf("permissions = %o, want 640", got)
	}
}

func TestUpdateTerraformVersionDryRunContract(t *testing.T) {
	input := "terraform {\n  required_version = \">= 1.0\"\n}\n"
	filename := writeTestFile(t, t.TempDir(), "main.tf", input)

	updated, err := updateTerraformVersion(filename, ">= 1.5", true)
	if err != nil {
		t.Fatalf("updateTerraformVersion returned error: %v", err)
	}
	if !updated {
		t.Fatal("updated = false, want true")
	}
	if got := readTestFile(t, filename); got != input {
		t.Fatalf("dry run mutated file: got %q, want %q", got, input)
	}
}

func TestUpdateTerraformVersionErrors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		_, err := updateTerraformVersion(t.TempDir()+"/missing.tf", ">= 1.5", false)
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("error = %v, want os.ErrNotExist", err)
		}
	})

	t.Run("directory read error", func(t *testing.T) {
		updated, err := updateTerraformVersion(t.TempDir(), ">= 1.5", false)
		if updated || err == nil || !strings.Contains(err.Error(), "failed to read file:") {
			t.Fatalf("updated=%v err=%v, want failed to read file", updated, err)
		}
	})

	t.Run("malformed HCL", func(t *testing.T) {
		filename := writeTestFile(t, t.TempDir(), "invalid.tf", "terraform {\n")
		_, err := updateTerraformVersion(filename, ">= 1.5", false)
		if err == nil || !strings.Contains(err.Error(), "failed to parse HCL:") {
			t.Fatalf("error = %v, want failed to parse HCL", err)
		}
	})

	t.Run("read-only file", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root can write read-only files")
		}
		filename := writeTestFile(t, t.TempDir(), "readonly.tf", "terraform {\n  required_version = \">= 1.0\"\n}\n")
		if err := os.Chmod(filename, 0o400); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		_, err := updateTerraformVersion(filename, ">= 1.5", false)
		if err == nil || !strings.Contains(err.Error(), "failed to write file:") {
			t.Fatalf("error = %v, want failed to write file", err)
		}
	})
}
