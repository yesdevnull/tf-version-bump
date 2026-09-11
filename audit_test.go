package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func auditValue(value string) *string {
	return &value
}

// auditJSON renders audit values the way -audit-file writes them, so failures show readable
// documents instead of pointer addresses.
func auditJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal audit value: %v", err)
	}
	return string(data)
}

func TestBuildAudit_RecordsTerraformRequiredVersions(t *testing.T) {
	versions := writeTestFile(t, t.TempDir(), "versions.tf", `terraform {
  required_version = ">= 1.10"
}

terraform {
  required_version = "~>1.10"
}

terraform {
  required_version = var.minimum_terraform
}

terraform {
  backend "local" {}
}
`)

	audit, err := buildAudit([]string{versions}, &Config{TerraformVersion: ">= 1.10"})
	if err != nil {
		t.Fatalf("buildAudit: %v", err)
	}

	want := []terraformAuditEntry{
		{File: versions, Actual: auditValue(">= 1.10"), Expected: ">= 1.10", Matches: true},
		{File: versions, Actual: auditValue("~>1.10"), Expected: ">= 1.10", Matches: false},
		{File: versions, Actual: auditValue("var.minimum_terraform"), Expected: ">= 1.10", Matches: false},
		{File: versions, Actual: nil, Expected: ">= 1.10", Matches: false},
	}
	if got, wantJSON := auditJSON(t, audit.Terraform), auditJSON(t, want); got != wantJSON {
		t.Fatalf("terraform = %s, want %s", got, wantJSON)
	}
}

func TestBuildAudit_OmitsTerraformVersionsTheConfigDoesNotSet(t *testing.T) {
	versions := writeTestFile(t, t.TempDir(), "versions.tf", "terraform {\n  required_version = \">= 1.10\"\n}\n")

	audit, err := buildAudit([]string{versions}, &Config{})
	if err != nil {
		t.Fatalf("buildAudit: %v", err)
	}

	if got := auditJSON(t, audit); got != `{"schema_version":1,"terraform":[],"providers":[]}` {
		t.Fatalf("audit = %s, want empty sections", got)
	}
}

func TestBuildAudit_RecordsProvidersInEitherSyntax(t *testing.T) {
	versions := writeTestFile(t, t.TempDir(), "versions.tf", `terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    google = {
      source = "hashicorp/google"
    }
    random = {
      source  = "hashicorp/random"
      version = "3.6.0"
    }
  }
}

terraform {
  required_providers {
    aws {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
    google {
      source = "hashicorp/google"
    }
  }
}
`)
	config := &Config{Providers: []ProviderUpdate{
		{Name: "aws", Version: "~> 6.0"},
		{Name: "google", Version: "~> 5.0"},
		{Name: "azurerm", Version: "~> 4.0"},
	}}

	audit, err := buildAudit([]string{versions}, config)
	if err != nil {
		t.Fatalf("buildAudit: %v", err)
	}

	want := []providerAuditEntry{
		{File: versions, Name: "aws", Actual: auditValue("~> 5.0"), Expected: "~> 6.0", Matches: false},
		{File: versions, Name: "google", Actual: nil, Expected: "~> 5.0", Matches: false},
		{File: versions, Name: "aws", Actual: auditValue("~> 6.0"), Expected: "~> 6.0", Matches: true},
		{File: versions, Name: "google", Actual: nil, Expected: "~> 5.0", Matches: false},
	}
	if got, wantJSON := auditJSON(t, audit.Providers), auditJSON(t, want); got != wantJSON {
		t.Fatalf("providers = %s, want %s", got, wantJSON)
	}
}

func TestBuildAudit_StopsAtTheFirstUnparseableFile(t *testing.T) {
	dir := t.TempDir()
	bad := writeTestFile(t, dir, "bad.tf", "terraform {\n")
	good := writeTestFile(t, dir, "good.tf", "terraform {\n  required_version = \">= 1.10\"\n}\n")

	audit, err := buildAudit([]string{bad, good}, &Config{TerraformVersion: ">= 1.10"})

	if audit != nil || err == nil || !strings.HasPrefix(err.Error(), "Error auditing "+bad+": failed to parse HCL: ") {
		t.Fatalf("audit = %v, err = %v; want no audit and a parse error naming %s", audit, err, bad)
	}
}

func TestBuildAudit_ReportsAnUnreadableFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.tf")

	audit, err := buildAudit([]string{missing}, &Config{})

	if audit != nil || err == nil || !strings.HasPrefix(err.Error(), "Error auditing "+missing+": failed to read file: ") {
		t.Fatalf("audit = %v, err = %v; want no audit and a read error naming %s", audit, err, missing)
	}
}
