package main

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestUpdateProviderVersionContract(t *testing.T) {
	tests := []struct {
		name, input, want string
		provider, version string
		wantUpdated       bool
	}{
		{
			name: "legacy provider block syntax", provider: "aws", version: "~> 5.0",
			input: `terraform {
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}
`,
			want: `terraform {
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}
`, wantUpdated: true,
		},
		{
			name: "attribute syntax preserves configuration aliases", provider: "aws", version: "~> 5.0",
			input: `terraform {
  required_providers {
    aws = {
      source                = "hashicorp/aws"
      version               = "~> 4.0"
      configuration_aliases = [aws.alternate]
    }
  }
}
`,
			want: `terraform {
  required_providers {
    aws = {
      source                = "hashicorp/aws"
      version               = "~> 5.0"
      configuration_aliases = [aws.alternate]
    }
  }
}
`, wantUpdated: true,
		},
		{
			name: "mixed providers only named provider changes", provider: "azurerm", version: "~> 3.5",
			input: `terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 3.0"
    }
  }
}
`,
			want: `terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 3.5"
    }
  }
}
`, wantUpdated: true,
		},
		{
			name: "mixed syntax across terraform blocks", provider: "aws", version: "~> 5.0",
			input: `terraform {
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}

terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 4.1"
    }
  }
}
`,
			want: `terraform {
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}
`, wantUpdated: true,
		},
		{
			name: "attribute provider without version is unchanged", provider: "aws", version: "~> 5.0",
			input: `terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}
`,
			want: `terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}
`, wantUpdated: false,
		},
		{
			name: "block provider without version adds version", provider: "aws", version: "~> 5.0",
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
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}
`, wantUpdated: true,
		},
		{
			name: "missing provider unchanged", provider: "google", version: "~> 6.0",
			input: `terraform {
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}
`,
			want: `terraform {
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filename := writeTestFile(t, t.TempDir(), "main.tf", tt.input)
			updated, err := updateProviderVersion(filename, tt.provider, tt.version, false)
			if err != nil {
				t.Fatalf("updateProviderVersion returned error: %v", err)
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

func TestUpdateProviderVersionDryRunContract(t *testing.T) {
	input := "terraform {\n  required_providers {\n    aws {\n      source = \"hashicorp/aws\"\n      version = \"~> 4.0\"\n    }\n  }\n}\n"
	filename := writeTestFile(t, t.TempDir(), "main.tf", input)
	updated, err := updateProviderVersion(filename, "aws", "~> 5.0", true)
	if err != nil {
		t.Fatalf("updateProviderVersion returned error: %v", err)
	}
	if !updated {
		t.Fatal("updated = false, want true")
	}
	if got := readTestFile(t, filename); got != input {
		t.Fatalf("dry run mutated file: got %q, want %q", got, input)
	}
}

func TestUpdateProviderVersionErrors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		_, err := updateProviderVersion(t.TempDir()+"/missing.tf", "aws", "~> 5.0", false)
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("error = %v, want os.ErrNotExist", err)
		}
	})
	t.Run("directory read error", func(t *testing.T) {
		updated, err := updateProviderVersion(t.TempDir(), "aws", "~> 5.0", false)
		if updated || err == nil || !strings.Contains(err.Error(), "failed to read file:") {
			t.Fatalf("updated=%v err=%v, want failed to read file", updated, err)
		}
	})
	t.Run("malformed HCL", func(t *testing.T) {
		filename := writeTestFile(t, t.TempDir(), "invalid.tf", "terraform {\n")
		_, err := updateProviderVersion(filename, "aws", "~> 5.0", false)
		if err == nil || !strings.Contains(err.Error(), "failed to parse HCL:") {
			t.Fatalf("error = %v, want failed to parse HCL", err)
		}
	})
	t.Run("read-only file", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root can write read-only files")
		}
		filename := writeTestFile(t, t.TempDir(), "readonly.tf", "terraform {\n  required_providers {\n    aws {\n      source = \"hashicorp/aws\"\n      version = \"~> 4.0\"\n    }\n  }\n}\n")
		if err := os.Chmod(filename, 0o400); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		_, err := updateProviderVersion(filename, "aws", "~> 5.0", false)
		if err == nil || !strings.Contains(err.Error(), "failed to write file:") {
			t.Fatalf("error = %v, want failed to write file", err)
		}
	})
}
