package main

import (
	"errors"
	"os"
	"strings"
	"testing"
)

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
