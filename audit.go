package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// auditReport is the -audit-file document: each configured version value the selected files
// declare, beside the value the config expects.
type auditReport struct {
	SchemaVersion int                   `json:"schema_version"`
	Terraform     []terraformAuditEntry `json:"terraform"`
	Providers     []providerAuditEntry  `json:"providers"`
	Modules       []moduleAuditEntry    `json:"modules"`
}

type terraformAuditEntry struct {
	File     string  `json:"file"`
	Actual   *string `json:"actual"`
	Expected string  `json:"expected"`
	Matches  bool    `json:"matches"`
}

type providerAuditEntry struct {
	File     string  `json:"file"`
	Name     string  `json:"name"`
	Actual   *string `json:"actual"`
	Expected string  `json:"expected"`
	Matches  bool    `json:"matches"`
}

type moduleAuditEntry struct {
	File     string           `json:"file"`
	Name     string           `json:"name"`
	Source   string           `json:"source"`
	Actual   *string          `json:"actual"`
	Expected string           `json:"expected"`
	Matches  bool             `json:"matches"`
	Skip     *moduleAuditSkip `json:"skip"`
}

// moduleAuditSkip names the first config filter that stops the updater changing a block.
type moduleAuditSkip struct {
	Filter string   `json:"filter"`
	Values []string `json:"values"`
}

// buildAudit reads each selected file once and records every version value the config targets
// in it. It never writes files and stops at the first file it cannot read or parse.
func buildAudit(files []string, config *Config) (*auditReport, error) {
	audit := &auditReport{SchemaVersion: 1, Terraform: []terraformAuditEntry{}, Providers: []providerAuditEntry{}, Modules: []moduleAuditEntry{}}
	for _, filename := range files {
		file, err := parseAuditedFile(filename)
		if err != nil {
			return nil, err
		}
		for _, block := range file.Body().Blocks() {
			switch block.Type() {
			case "terraform":
				audit.recordTerraformBlock(filename, block, config)
			case "module":
				audit.recordModuleBlock(filename, block, config.Modules)
			}
		}
	}
	return audit, nil
}

func parseAuditedFile(filename string) (*hclwrite.File, error) {
	src, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("Error auditing %s: failed to read file: %w", filename, err) //nolint:staticcheck // User-facing CLI diagnostic.
	}
	file, diags := hclwrite.ParseConfig(src, filename, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, fmt.Errorf("Error auditing %s: failed to parse HCL: %s", filename, diags.Error()) //nolint:staticcheck // User-facing CLI diagnostic.
	}
	return file, nil
}

// recordTerraformBlock records the block's required_version when the config sets one, and each
// required_providers declaration of a configured provider.
func (audit *auditReport) recordTerraformBlock(filename string, block *hclwrite.Block, config *Config) {
	if config.TerraformVersion != "" {
		actual, matches := auditedAttribute(block.Body().GetAttribute("required_version"), config.TerraformVersion)
		audit.Terraform = append(audit.Terraform, terraformAuditEntry{File: filename, Actual: actual, Expected: config.TerraformVersion, Matches: matches})
	}
	for _, nestedBlock := range block.Body().Blocks() {
		if nestedBlock.Type() != "required_providers" {
			continue
		}
		for _, provider := range config.Providers {
			audit.recordRequiredProvider(filename, nestedBlock, provider)
		}
	}
}

// recordRequiredProvider records a provider declared in block syntax, object syntax or both,
// because the updater reads either.
func (audit *auditReport) recordRequiredProvider(filename string, requiredProviders *hclwrite.Block, provider ProviderUpdate) {
	for _, providerBlock := range requiredProviders.Body().Blocks() {
		if providerBlock.Type() == provider.Name {
			actual, matches := auditedAttribute(providerBlock.Body().GetAttribute("version"), provider.Version)
			audit.Providers = append(audit.Providers, providerAuditEntry{File: filename, Name: provider.Name, Actual: actual, Expected: provider.Version, Matches: matches})
		}
	}
	if objExpr, expression, ok := providerAttributeObject(requiredProviders, provider.Name); ok {
		actual, matches := auditedObjectVersion(objExpr, expression, provider.Version)
		audit.Providers = append(audit.Providers, providerAuditEntry{File: filename, Name: provider.Name, Actual: actual, Expected: provider.Version, Matches: matches})
	}
}

// recordModuleBlock records the block once for each config entry with an equal source.
func (audit *auditReport) recordModuleBlock(filename string, block *hclwrite.Block, updates []ModuleUpdate) {
	source, ok := moduleSourceValue(block)
	if !ok {
		return
	}
	name := moduleBlockName(block)
	versionAttribute := block.Body().GetAttribute("version")
	// Index rather than copy: gocritic's hugeParam rejects passing a ModuleUpdate by value.
	for i := range updates {
		update := &updates[i]
		if update.Source != source {
			continue
		}
		actual, matches := auditedAttribute(versionAttribute, update.Version)
		audit.Modules = append(audit.Modules, moduleAuditEntry{
			File: filename, Name: name, Source: source, Actual: actual, Expected: update.Version, Matches: matches,
			Skip: moduleAuditSkipFor(name, source, actual, update),
		})
	}
}

// moduleAuditSkipFor applies the updater's precedence: a local source, then ignore_modules, then
// the version filters, which a block without a version never meets.
func moduleAuditSkipFor(name, source string, actual *string, update *ModuleUpdate) *moduleAuditSkip {
	switch {
	case isLocalModule(source):
		return &moduleAuditSkip{Filter: "local_source", Values: []string{}}
	case shouldIgnoreModule(name, update.IgnoreModules):
		return &moduleAuditSkip{Filter: "ignore_modules", Values: update.IgnoreModules}
	case actual == nil:
		return nil
	}
	switch moduleVersionFilter(*actual, update.IgnoreVersions, update.From) {
	case "ignore_versions":
		return &moduleAuditSkip{Filter: "ignore_versions", Values: update.IgnoreVersions}
	case "from":
		return &moduleAuditSkip{Filter: "from", Values: update.From}
	}
	return nil
}

// auditedAttribute returns an attribute's value as written, without its quotes, and whether it
// already evaluates to expected, which is the updater's no-op test. A missing attribute has no
// value.
func auditedAttribute(attr *hclwrite.Attribute, expected string) (actual *string, matches bool) {
	if attr == nil {
		return nil, false
	}
	value := attributeStringValue(attr)
	return &value, attributeHasStringValue(attr, expected)
}

// auditedObjectVersion reads the version item of an object-syntax provider declaration.
func auditedObjectVersion(objExpr *hclsyntax.ObjectConsExpr, expression []byte, expected string) (actual *string, matches bool) {
	for _, item := range objExpr.Items {
		keyName, ok := providerObjectItemKey(item)
		if !ok || keyName != "version" {
			continue
		}
		valueRange := item.ValueExpr.Range()
		if valueRange.Start.Byte < 0 || valueRange.End.Byte > len(expression) || valueRange.Start.Byte > valueRange.End.Byte {
			continue
		}
		valueExpression := expression[valueRange.Start.Byte:valueRange.End.Byte]
		value := trimQuotes(strings.TrimSpace(string(valueExpression)))
		return &value, expressionHasStringValue(valueExpression, expected)
	}
	return nil, false
}
