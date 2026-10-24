// Package main is tf-version-bump, a CLI that updates Terraform module versions, the Terraform
// required_version and provider versions across files matched by a glob.
//
// A run performs one operation given by flags (-module, -terraform-version or -provider), applies
// every update in a YAML config (-config), validates a config (-validate-config) or compares files
// with a config without changing them (-audit-file).
//
// Files are parsed and rewritten with hclwrite, so comments and structure survive; changed files
// are normalised by hclwrite.Format.
package main

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	tfaddr "github.com/hashicorp/terraform-registry-address"
	"github.com/zclconf/go-cty/cty"
)

// visibleDirFS hides dot-directories from directory listings, so a '**' wildcard never descends
// into trees such as .terraform or .git. A dot-directory named in the non-glob base path is still
// reachable, because the base is resolved before the glob.
type visibleDirFS struct {
	fs.ReadDirFS
}

func (f visibleDirFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := f.ReadDirFS.ReadDir(name)
	if err != nil {
		return nil, err
	}

	visible := entries[:0]
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		visible = append(visible, entry)
	}
	return visible, nil
}

// Build information set by ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// Process and filesystem hooks, swapped by tests to observe exits and inject failures.
var (
	hookMu   sync.Mutex // guards the hooks below
	exitFunc = os.Exit
	fatalf   = func(format string, v ...any) {
		log.Printf(format, v...)
		exitFunc(1)
	}
	// renameReportFile moves a written report onto its destination, the last step of publishing it.
	renameReportFile = os.Rename
	// removeReportFile deletes a report an earlier run left where this run's report was to go.
	removeReportFile = os.Remove
	// openFileForRewrite opens a Terraform file to rewrite it in place: without O_CREATE, so a file
	// deleted since its parse is not recreated, and without O_TRUNC, so a failed write can be undone.
	openFileForRewrite = func(name string) (rewritableFile, error) {
		file, err := os.OpenFile(name, os.O_RDWR, 0)
		if err != nil {
			return nil, err
		}
		return file, nil
	}
	// createBackupFile creates the copy of a Terraform file's original bytes kept while it is
	// rewritten. It lives in the system temporary directory, out of reach of a Terraform glob
	// unless that directory is itself inside the tree being globbed.
	createBackupFile = func(pattern string) (backupFile, error) {
		file, err := os.CreateTemp("", pattern)
		if err != nil {
			return nil, err
		}
		return file, nil
	}
)

// stringSliceFlag collects a flag that may be given more than once.
type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSliceFlag) Set(value string) error {
	*s = append(*s, value)
	return nil
}

// quote wraps a user-facing value for the output format: 'vpc' for text, `vpc` for Markdown.
func quote(s, format string) string {
	if format == "md" {
		return "`" + s + "`"
	}
	return "'" + s + "'"
}

// updateVerb returns the line prefix and verb for an update line: "✓ Updated", or "→ Would update"
// in a dry run.
func updateVerb(dryRun bool) (prefix, action string) {
	if dryRun {
		return "→", "Would update"
	}
	return "✓", "Updated"
}

// cliFlags holds the parsed command-line flags and, with -report-file, the report being built.
type cliFlags struct {
	pattern              string
	moduleSource         string
	toVersion            string
	fromVersions         stringSliceFlag
	ignoreVersions       stringSliceFlag
	ignoreModules        string
	branch               string
	configFile           string
	validationConfigFile string
	forceAdd             bool
	dryRun               bool
	check                bool
	verbose              bool
	showVersion          bool
	output               string
	terraformVersion     string
	providerName         string
	reportFile           string
	auditFile            string
	report               updateReport
}

// updateReport is the -report-file document: exact counts of the distinct blocks a run changed.
type updateReport struct {
	SchemaVersion          int `json:"schema_version"`
	TerraformBlocksUpdated int `json:"terraform_blocks_updated"`
	ModuleBlocksUpdated    int `json:"module_blocks_updated"`
	ProviderBlocksUpdated  int `json:"provider_blocks_updated"`

	terraformBlockIDs map[string]struct{}
	moduleBlockIDs    map[string]struct{}
	fileIdentities    []fs.FileInfo
}

// preparedReportFile is a JSON output opened as a temporary file beside its destination, so a
// run that fails leaves no partial document there.
type preparedReportFile struct {
	destination string
	file        *os.File
}

// reportRecorder returns the report to record changed blocks in, or nil without -report-file.
func (flags *cliFlags) reportRecorder() *updateReport {
	if flags.reportFile == "" {
		return nil
	}
	return &flags.report
}

func (report *updateReport) recordTerraformBlocks(filename string, blockIndexes []int) {
	if report.terraformBlockIDs == nil {
		report.terraformBlockIDs = make(map[string]struct{})
	}
	report.TerraformBlocksUpdated += report.recordNewBlocks(filename, blockIndexes, report.terraformBlockIDs)
}

func (report *updateReport) recordModuleBlocks(filename string, blockIndexes []int) {
	if report.moduleBlockIDs == nil {
		report.moduleBlockIDs = make(map[string]struct{})
	}
	report.ModuleBlocksUpdated += report.recordNewBlocks(filename, blockIndexes, report.moduleBlockIDs)
}

// recordNewBlocks adds the file's blocks to seen and returns how many were not there already. A
// module block can be changed by more than one config entry, and the same file can be selected
// through more than one path, so the count is of distinct blocks.
func (report *updateReport) recordNewBlocks(filename string, blockIndexes []int, seen map[string]struct{}) int {
	fileID := report.fileIdentity(filename)
	var recorded int
	for _, blockIndex := range blockIndexes {
		blockID := fmt.Sprintf("%s\x00%d", fileID, blockIndex)
		if _, ok := seen[blockID]; ok {
			continue
		}
		seen[blockID] = struct{}{}
		recorded++
	}
	return recorded
}

// recordProviderBlocks needs no de-duplication: a run names each provider once, and a second path
// to the same file is read already at the target.
func (report *updateReport) recordProviderBlocks(blockLocations []string) {
	report.ProviderBlocksUpdated += len(blockLocations)
}

// fileIdentity names a file by inode when it can be stat'ed, so hard links and symlinks to one
// file share an identity, and by cleaned path otherwise.
func (report *updateReport) fileIdentity(filename string) string {
	fileInfo, err := os.Stat(filename)
	if err == nil {
		for index, existingIdentity := range report.fileIdentities {
			if os.SameFile(fileInfo, existingIdentity) {
				return fmt.Sprintf("file:%d", index)
			}
		}
		report.fileIdentities = append(report.fileIdentities, fileInfo)
		return fmt.Sprintf("file:%d", len(report.fileIdentities)-1)
	}
	return "path:" + canonicalFileIdentity(filename)
}

func canonicalFileIdentity(filename string) string {
	absolute, err := filepath.Abs(filename)
	if err == nil {
		filename = absolute
	}
	resolved, err := filepath.EvalSymlinks(filename)
	if err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(filename)
}

// parseFlags parses the command line and validates the flags that stand on their own.
func parseFlags() *cliFlags {
	flags := &cliFlags{}
	usageOutput := flag.CommandLine.Output()
	flagSet := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	flag.CommandLine = flagSet

	flagSet.StringVar(&flags.pattern, "pattern", "", "Glob pattern for Terraform files; '**' matches any depth (e.g., '*.tf' or 'modules/**/*.tf')")
	flagSet.StringVar(&flags.moduleSource, "module", "", "Source of the module to update (e.g., 'terraform-aws-modules/vpc/aws')")
	flagSet.StringVar(&flags.toVersion, "to", "", "Desired version number")
	flagSet.Var(&flags.fromVersions, "from", "Optional: version to update from (can be specified multiple times, e.g., -from 3.0.0 -from '~> 3.0')")
	flagSet.Var(&flags.ignoreVersions, "ignore-version", "Optional: version(s) to skip (can be specified multiple times, e.g., -ignore-version 3.0.0 -ignore-version '~> 3.0')")
	flagSet.StringVar(&flags.ignoreModules, "ignore-modules", "", "Optional: comma-separated list of module names or patterns to ignore; a '<branch>/<module>' pattern requires -branch (e.g., 'vpc,legacy-*,release/*/vpc')")
	flagSet.StringVar(&flags.branch, "branch", "", "Optional: current branch name, required by branch-scoped ignore patterns (e.g., 'release/2026-09')")
	flagSet.StringVar(&flags.configFile, "config", "", "Path to YAML config file with multiple module updates")
	flagSet.StringVar(&flags.validationConfigFile, "validate-config", "", "Validate a YAML config file without updating Terraform files")
	flagSet.BoolVar(&flags.forceAdd, "force-add", false, "Add a missing version attribute to registry modules (default: skip with warning)")
	flagSet.BoolVar(&flags.dryRun, "dry-run", false, "Show what changes would be made without actually modifying files")
	flagSet.BoolVar(&flags.check, "check", false, "Exit 2 when updates are required without modifying files")
	flagSet.BoolVar(&flags.verbose, "verbose", false, "Show verbose output including skipped modules")
	flagSet.BoolVar(&flags.showVersion, "version", false, "Print version information and exit")
	flagSet.StringVar(&flags.output, "output", "text", "Output format: 'text' (default) or 'md' (Markdown)")
	flagSet.StringVar(&flags.terraformVersion, "terraform-version", "", "Update Terraform required_version in terraform blocks")
	flagSet.StringVar(&flags.providerName, "provider", "", "Provider name to update (e.g., 'aws', 'azurerm')")
	flagSet.StringVar(&flags.reportFile, "report-file", "", "Write exact updated Terraform, module, and provider block counts as JSON")
	flagSet.StringVar(&flags.auditFile, "audit-file", "", "Write each configured version value's current and expected version as JSON without changing files (config mode)")
	if err := flagSet.Parse(os.Args[1:]); err != nil {
		if stderrors.Is(err, flag.ErrHelp) {
			flagSet.SetOutput(usageOutput)
			if _, writeErr := fmt.Fprintf(usageOutput, "Usage of %s:\n", os.Args[0]); writeErr != nil {
				fatalf("Error writing help: %v", writeErr)
				return flags
			}
			flagSet.PrintDefaults()
			exitFunc(0)
			return flags
		}
		fatalf("Error: %v", err)
		return flags
	}
	flagSet.SetOutput(usageOutput)
	if flagSet.NArg() > 0 {
		fatalf("Error: unexpected positional argument(s): %s", strings.Join(flagSet.Args(), " "))
		return flags
	}

	if flags.output != "text" && flags.output != "md" {
		fatalf("Error: Invalid output format '%s'. Must be 'text' or 'md'", flags.output)
	}

	flags.branch = strings.TrimSpace(flags.branch)
	// Neither names a branch, so branch-scoped ignore patterns would silently be dropped:
	// 'git rev-parse --abbrev-ref HEAD' prints HEAD on a detached checkout, and GITHUB_REF holds
	// a full ref such as 'refs/heads/main'.
	if flags.branch == "HEAD" {
		fatalf("Error: -branch must be a branch name, not 'HEAD'. Use 'git branch --show-current', which is empty on a detached checkout")
	}
	if strings.HasPrefix(flags.branch, "refs/") {
		fatalf("Error: -branch must be a branch name, not the ref '%s'. Use 'git branch --show-current' rather than a full ref such as GITHUB_REF", flags.branch)
	}

	return flags
}

// loadModuleUpdates builds the single module update that -module describes.
func loadModuleUpdates(flags *cliFlags) []ModuleUpdate {
	if flags.pattern == "" || flags.moduleSource == "" || flags.toVersion == "" {
		fmt.Println("Usage:")
		fmt.Println("  Single module:  tf-version-bump -pattern <glob> -module <source> -to <version> [-from <version>]... [-ignore-version <version>]... [-ignore-modules <patterns>] [-branch <name>]")
		fmt.Println("  Config file:    tf-version-bump -pattern <glob> -config <config-file>")
		flag.PrintDefaults()
		exitFunc(1)
	}

	var ignorePatterns []string
	if flags.ignoreModules != "" {
		ignorePatterns = trimNonEmptyStrings(strings.Split(flags.ignoreModules, ","))
	}

	updates := []ModuleUpdate{{
		Source:         flags.moduleSource,
		Version:        flags.toVersion,
		From:           FromVersions(flags.fromVersions),
		IgnoreVersions: FromVersions(flags.ignoreVersions),
		IgnoreModules:  ignorePatterns,
	}}
	if err := resolveBranchIgnoreModules(updates, flags.branch); err != nil {
		fatalf("Error: %v", err)
		return nil
	}

	return updates
}

// resolveBranchIgnoreModules records, for each entry, the ignore_modules patterns that apply to
// branch. A branch-scoped pattern cannot be evaluated without a branch, so it is an error rather
// than a silent no-op that would bump a module the configuration excludes.
func resolveBranchIgnoreModules(updates []ModuleUpdate, branch string) error {
	for i := range updates {
		if len(updates[i].IgnoreModules) == 0 {
			continue
		}

		resolved := make([]string, 0, len(updates[i].IgnoreModules))
		for _, entry := range updates[i].IgnoreModules {
			branchPattern, modulePattern, err := splitIgnoreModuleEntry(entry)
			switch {
			case err != nil:
				return err
			case branchPattern == "":
				resolved = append(resolved, modulePattern)
			case branch == "":
				return fmt.Errorf("ignore pattern '%s' is scoped to a branch, but -branch is missing or empty; a detached checkout has no current branch", entry)
			case matchPattern(branch, branchPattern):
				resolved = append(resolved, modulePattern)
			}
		}
		updates[i].resolvedIgnoreModules = resolved
	}

	return nil
}

// loadResolvedConfig is the single entry point for reading a config into an executable form. The
// update and audit paths share it so their module filtering cannot drift apart.
func loadResolvedConfig(configFile, branch string) (*Config, error) {
	config, err := loadConfig(configFile)
	if err != nil {
		return nil, fmt.Errorf("Error loading config file: %w", err)
	}
	if err := resolveBranchIgnoreModules(config.Modules, branch); err != nil {
		return nil, fmt.Errorf("Error: %w", err)
	}
	return config, nil
}

// processFiles applies the module updates to every file in order, parsing each file once so a
// later update meets the changes earlier ones made, in a dry run as in a real run. A file that
// cannot be read or parsed counts one error per update. A failed write counts one error and the
// file is read again for the updates after it, which a file whose write kept its backup refuses.
func processFiles(files []string, updates []ModuleUpdate, flags *cliFlags) (totalUpdates, totalSkips, totalErrors int) {
	for _, file := range files {
		parsed, readErr := readTerraformFile(file)
		// Entries for one source are applied to the same file in turn, so an unpinned module is
		// skipped once per entry. The count speaks for modules, so the blocks are unioned.
		skippedBlocks := make(map[int]struct{})
		for i := range updates {
			update := &updates[i]
			if readErr != nil {
				log.Printf("Error processing %s: %v", file, readErr)
				totalErrors++
				continue
			}
			updated, changedBlocks, blocksSkipped, err := applyModuleVersion(parsed, update.Source, update.Version, update.From, update.IgnoreVersions, update.resolvedIgnoreModules, flags.forceAdd, flags.dryRun, flags.verbose, flags.output)
			for _, blockIndex := range blocksSkipped {
				skippedBlocks[blockIndex] = struct{}{}
			}
			if err != nil {
				log.Printf("Error processing %s: %v", file, err)
				totalErrors++
				// Re-read rather than keep the unsaved change: a restored file gives later updates
				// its original content, and a file whose backup was kept is refused.
				parsed, readErr = readTerraformFile(file)
				continue
			}
			if !updated {
				continue
			}
			if report := flags.reportRecorder(); report != nil && !flags.dryRun && len(changedBlocks) > 0 {
				report.recordModuleBlocks(file, changedBlocks)
			}
			prefix, action := updateVerb(flags.dryRun)
			if len(update.From) > 0 {
				fmt.Printf("%s %s module source %s from version(s) %v to %s in %s\n", prefix, action, quote(update.Source, flags.output), update.From, quote(update.Version, flags.output), file)
			} else {
				fmt.Printf("%s %s module source %s to version %s in %s\n", prefix, action, quote(update.Source, flags.output), quote(update.Version, flags.output), file)
			}
			totalUpdates++
		}
		totalSkips += len(skippedBlocks)
	}
	return totalUpdates, totalSkips, totalErrors
}

// failureNote reports the updates that failed, so a summary of what succeeded is never mistaken
// for the whole run. Every counted failure is logged where it happens, so the count is also the
// number of diagnostics. It names stderr because the summary goes to stdout.
func failureNote(errorCount int) string {
	if errorCount == 0 {
		return ""
	}
	return fmt.Sprintf("%d update(s) failed; see the errors on stderr\n", errorCount)
}

// skipNote reports the entries the run matched but left unpinned, each of which was warned about
// where it happened. A local module is warned about but not counted: it can never carry a version.
func skipNote(skipCount int, kind string) string {
	if skipCount == 0 {
		return ""
	}
	return fmt.Sprintf("%d %s(s) skipped; see the warnings on stderr\n", skipCount, kind)
}

// printRunSummary prints a direct run's summary line, then its failure and skip notes. A run that
// updated nothing explains why instead of claiming a clean zero, because a mistyped source, a glob
// over the wrong tree and an already-current file all look alike from here; a run that updated
// nothing and failed prints only the notes.
func printRunSummary(line string, totalUpdates, errorCount int, skipped string, dryRun bool) {
	fmt.Println()
	switch {
	case totalUpdates > 0:
		fmt.Println(line)
	case errorCount > 0:
		// The failure note is the whole summary: a nothing-to-do line would read as a clean result.
	case dryRun:
		fmt.Println("No updates would be performed. Nothing matched, or every match was already at the target version or skipped.")
	default:
		fmt.Println("No updates were performed. Nothing matched, or every match was already at the target version or skipped.")
	}
	fmt.Print(failureNote(errorCount))
	fmt.Print(skipped)
}

// printSummary prints the summary of a direct module run, which counts files.
func printSummary(totalUpdates, errorCount, skipCount int, dryRun bool) {
	line := fmt.Sprintf("Successfully updated %d file(s)", totalUpdates)
	if dryRun {
		line = fmt.Sprintf("Dry run: would update %d file(s)", totalUpdates)
	}
	printRunSummary(line, totalUpdates, errorCount, skipNote(skipCount, "module"), dryRun)
}

func main() {
	flags := parseFlags()

	if flags.showVersion {
		fmt.Printf("tf-version-bump %s\n", version)
		fmt.Printf("  commit: %s\n", commit)
		fmt.Printf("  built:  %s\n", date)
		exitFunc(0)
	}

	validateOperationModes(flags)
	if flags.validationConfigFile != "" {
		if err := validateConfigFile(flags.validationConfigFile); err != nil {
			fatalf("Error validating config file: %v", err)
		}
		fmt.Printf("Config '%s' is valid\n", flags.validationConfigFile)
		exitFunc(0)
	}
	if flags.check {
		flags.dryRun = true
	}

	files := findMatchingFiles(flags)
	validateRequiredOperationFlags(flags)

	if flags.auditFile != "" {
		if err := runAuditMode(files, flags); err != nil {
			fatalf("%v", err)
		}
		return
	}

	totalUpdates, err := runUpdateMode(files, flags)
	if err != nil {
		fatalf("%v", err)
	}
	if flags.check && totalUpdates > 0 {
		exitFunc(2)
	}
}

// commandInputFiles lists every file the command reads: the selected Terraform files and, in
// config mode, the config. An output destination must not overwrite any of them.
func commandInputFiles(files []string, configFile string) []string {
	if configFile == "" {
		return files
	}
	return append(append([]string(nil), files...), configFile)
}

// runUpdateMode applies the selected updates and publishes any requested update report,
// returning the update-operation total that check mode turns into its exit status.
func runUpdateMode(files []string, flags *cliFlags) (int, error) {
	inputFiles := commandInputFiles(files, flags.configFile)
	preparedReport, err := prepareJSONOutput(updateReportOutput, flags.reportFile, inputFiles)
	if err != nil {
		return 0, err
	}

	var totalUpdates int
	if flags.configFile != "" {
		totalUpdates, err = runConfigFileMode(files, flags)
	} else {
		totalUpdates, err = runCLIMode(files, flags)
	}
	if err == nil && preparedReport != nil {
		flags.report.SchemaVersion = 2
		if publishErr := preparedReport.publish(&flags.report); publishErr != nil {
			err = fmt.Errorf("Error writing update report: %w", publishErr)
		}
	}
	if err == nil {
		return totalUpdates, nil
	}
	if preparedReport != nil {
		if discardErr := preparedReport.discard(); discardErr != nil {
			err = fmt.Errorf("%w; failed to discard prepared report: %w", err, discardErr)
		}
		// A report an earlier run left at the destination describes the tree as it was then. A
		// failed run that changed the tree, including one whose publish failed, removes it so a
		// consumer that skips the exit status cannot act on stale counts. A run refused for its
		// config, a run whose every file failed and a dry run leave tree and report untouched. A
		// file left untrusted by a write counts as changed even though no update is counted for it.
		if (totalUpdates > 0 && !flags.dryRun) || anyUntrusted() {
			if removeErr := preparedReport.removeDestination(); removeErr != nil {
				err = fmt.Errorf("%w; failed to remove the stale report: %w", err, removeErr)
			}
		}
	}
	return totalUpdates, err
}

// validateRequiredOperationFlags rejects a direct operation missing a flag it needs. It runs after
// file selection, so the usage message follows the file count as it always has.
func validateRequiredOperationFlags(flags *cliFlags) {
	if flags.moduleSource != "" {
		_ = loadModuleUpdates(flags)
	}
	if flags.providerName != "" && flags.toVersion == "" {
		fatalf("Error: -to flag is required when using -provider")
	}
}

// jsonOutput describes a JSON document the command publishes through a temporary file once its
// work succeeds, and how diagnostics name it.
type jsonOutput struct {
	fileLabel     string // the destination, as in "report file must not overwrite input file"
	documentLabel string // the document, as in "Error preparing update report"
	tempPattern   string
}

var (
	updateReportOutput = jsonOutput{fileLabel: "report file", documentLabel: "update report", tempPattern: ".tf-version-bump-report-*"}
	auditOutput        = jsonOutput{fileLabel: "audit file", documentLabel: "audit", tempPattern: ".tf-version-bump-audit-*"}
)

func prepareJSONOutput(output jsonOutput, destination string, inputFiles []string) (*preparedReportFile, error) {
	if destination == "" {
		return nil, nil
	}
	if err := validateOutputDoesNotOverwriteInput(output, destination, inputFiles); err != nil {
		return nil, err
	}

	file, err := os.CreateTemp(filepath.Dir(destination), output.tempPattern)
	if err != nil {
		return nil, fmt.Errorf("Error preparing %s: %w", output.documentLabel, err)
	}
	return &preparedReportFile{destination: destination, file: file}, nil
}

// publish encodes the document into the temporary file, syncs it and moves it onto the
// destination, so the destination only ever holds a complete document.
func (prepared *preparedReportFile) publish(document any) error {
	var data bytes.Buffer
	encoder := json.NewEncoder(&data)
	// Without this, constraints such as ">= 1.5" would be written with Unicode escapes.
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		_ = prepared.discard()
		return fmt.Errorf("create report: %w", err)
	}
	if _, err := prepared.file.Write(data.Bytes()); err != nil {
		_ = prepared.discard()
		return err
	}
	if err := prepared.file.Sync(); err != nil {
		_ = prepared.discard()
		return err
	}
	temporaryName := prepared.file.Name()
	// Closing consumes the handle whether or not it succeeds, so the caller's discard must not
	// close it again and report a failure the run did not have.
	closeErr := prepared.file.Close()
	prepared.file = nil
	if closeErr != nil {
		_ = os.Remove(temporaryName)
		return closeErr
	}
	if err := renameReportFile(temporaryName, prepared.destination); err != nil {
		_ = os.Remove(temporaryName)
		return err
	}
	return nil
}

// removeDestination deletes a report an earlier run left at the destination. The caller decides
// when: only a failed run that changed files, which runUpdateMode explains. Audit mode never calls
// it, because a failed audit changes nothing on disk and the previous audit is still true.
func (prepared *preparedReportFile) removeDestination() error {
	if prepared == nil {
		return nil
	}
	if err := removeReportFile(prepared.destination); err != nil && !stderrors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// discard closes and removes the temporary file, if one is still open.
func (prepared *preparedReportFile) discard() error {
	if prepared == nil || prepared.file == nil {
		return nil
	}
	temporaryName := prepared.file.Name()
	closeErr := prepared.file.Close()
	prepared.file = nil
	removeErr := os.Remove(temporaryName)
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}

// validateOutputDoesNotOverwriteInput rejects a destination that is one of the files the command
// reads, by path or, when the destination exists, by file identity.
func validateOutputDoesNotOverwriteInput(output jsonOutput, destination string, inputFiles []string) error {
	if destination == "" {
		return nil
	}

	destinationPath, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("Error resolving %s: %w", output.fileLabel, err)
	}
	destinationInfo, destinationStatErr := os.Stat(destinationPath)
	if destinationStatErr != nil && !os.IsNotExist(destinationStatErr) {
		return fmt.Errorf("Error inspecting %s: %w", output.fileLabel, destinationStatErr)
	}
	if destinationStatErr == nil && destinationInfo.IsDir() {
		return fmt.Errorf("Error preparing %s: destination is a directory: %s", output.documentLabel, destination)
	}

	for _, inputFile := range inputFiles {
		inputPath, absErr := filepath.Abs(inputFile)
		if absErr != nil {
			return fmt.Errorf("Error resolving input file %s: %w", inputFile, absErr)
		}
		if filepath.Clean(destinationPath) == filepath.Clean(inputPath) {
			return fmt.Errorf("Error: %s must not overwrite input file: %s", output.fileLabel, destination)
		}
		if destinationStatErr != nil {
			continue
		}
		inputInfo, inputStatErr := os.Stat(inputPath)
		if inputStatErr != nil {
			return fmt.Errorf("Error inspecting input file %s: %w", inputFile, inputStatErr)
		}
		if os.SameFile(destinationInfo, inputInfo) {
			return fmt.Errorf("Error: %s must not overwrite input file: %s", output.fileLabel, destination)
		}
	}

	return nil
}

// validateOperationModes rejects flag combinations that mix or omit operation modes.
func validateOperationModes(flags *cliFlags) {
	if flags.validationConfigFile != "" {
		if configValidationHasConflicts(flags) {
			fatalf("Error: Cannot use -validate-config with update or report flags")
		}
		return
	}
	if flags.auditFile != "" {
		validateAuditMode(flags)
	}
	if flags.check && flags.dryRun {
		fatalf("Error: Cannot use -check with -dry-run")
	}
	if flags.check && flags.reportFile != "" {
		fatalf("Error: Cannot use -check with -report-file")
	}

	if flags.configFile != "" {
		validateConfigUpdateMode(flags)
		return
	}

	modesSet := 0
	if flags.moduleSource != "" {
		modesSet++
	}
	if flags.terraformVersion != "" {
		modesSet++
	}
	if flags.providerName != "" {
		modesSet++
	}

	if modesSet == 0 {
		fmt.Println("Usage:")
		fmt.Println("  Module update:     tf-version-bump -pattern <glob> -module <source> -to <version>")
		fmt.Println("  Config file:       tf-version-bump -pattern <glob> -config <config-file>")
		fmt.Println("  Config validation: tf-version-bump -validate-config <config-file>")
		fmt.Println("  Terraform version: tf-version-bump -pattern <glob> -terraform-version <version>")
		fmt.Println("  Provider version:  tf-version-bump -pattern <glob> -provider <name> -to <version>")
		flag.PrintDefaults()
		exitFunc(1)
	}

	if modesSet > 1 {
		fatalf("Error: Cannot use -module, -terraform-version, and -provider flags together. Choose one operation mode or use a config file.")
	}
}

// validateConfigUpdateMode keeps -config exclusive with the direct operation flags.
func validateConfigUpdateMode(flags *cliFlags) {
	if flags.moduleSource != "" || flags.terraformVersion != "" || flags.providerName != "" ||
		flags.toVersion != "" || len(flags.fromVersions) > 0 || len(flags.ignoreVersions) > 0 || flags.ignoreModules != "" {
		fatalf("Error: Cannot use -config with other operation flags (-module, -to, -terraform-version, -provider, -from, -ignore-version, -ignore-modules)")
	}
}

// validateAuditMode keeps -audit-file read-only: it compares files with a config and never
// combines with flags that preview, check, report or add versions.
func validateAuditMode(flags *cliFlags) {
	if flags.configFile == "" {
		fatalf("Error: -audit-file requires -config")
	}
	if flags.dryRun || flags.check || flags.reportFile != "" || flags.forceAdd {
		fatalf("Error: Cannot use -audit-file with -dry-run, -check, -report-file or -force-add")
	}
}

// configValidationHasConflicts reports whether any update or report flag accompanies
// -validate-config, which validates a config on its own. The check is split in two to keep each
// half under the cyclomatic limit as flags are added.
func configValidationHasConflicts(flags *cliFlags) bool {
	return configValidationHasOperationFlags(flags) || configValidationHasBehaviourFlags(flags)
}

// configValidationHasOperationFlags covers the flags naming what to update and where to write.
func configValidationHasOperationFlags(flags *cliFlags) bool {
	return flags.pattern != "" || flags.configFile != "" || flags.moduleSource != "" ||
		flags.toVersion != "" || flags.terraformVersion != "" || flags.providerName != "" ||
		flags.reportFile != "" || flags.auditFile != ""
}

// configValidationHasBehaviourFlags covers the filters and switches that change how a run behaves.
func configValidationHasBehaviourFlags(flags *cliFlags) bool {
	return len(flags.fromVersions) > 0 || len(flags.ignoreVersions) > 0 || flags.ignoreModules != "" ||
		flags.branch != "" || flags.forceAdd || flags.dryRun || flags.check || flags.verbose
}

// findMatchingFiles returns the sorted files matching -pattern, or exits when nothing matches.
func findMatchingFiles(flags *cliFlags) []string {
	if flags.pattern == "" {
		fatalf("Error: -pattern flag is required")
	}

	// doublestar rather than filepath.Glob, because filepath.Glob treats '**' as a plain '*'.
	// visibleDirFS keeps wildcards out of dot-directories; WithNoFollow stops a directory symlink
	// matching the same file twice or cycling; WithFilesOnly keeps '**' from returning directories.
	pattern := filepath.ToSlash(filepath.Clean(flags.pattern))
	base, globPattern := doublestar.SplitPattern(pattern)
	fileSystem := os.DirFS(base)
	if readDirFS, ok := fileSystem.(fs.ReadDirFS); ok {
		fileSystem = visibleDirFS{ReadDirFS: readDirFS}
	}

	matches, err := doublestar.Glob(
		fileSystem,
		globPattern,
		doublestar.WithNoFollow(),
		doublestar.WithFilesOnly(),
	)
	if err != nil {
		fatalf("Error matching pattern: %v", err)
	}

	files := make([]string, 0, len(matches))
	for _, match := range matches {
		filename := filepath.Join(base, filepath.FromSlash(match))
		info, lstatErr := os.Lstat(filename)
		if lstatErr == nil && info.Mode()&os.ModeSymlink != 0 {
			target, statErr := os.Stat(filename)
			if statErr == nil && target.IsDir() {
				continue
			}
		}
		files = append(files, filename)
	}

	// doublestar returns traversal order, and file order is user-visible in the per-file output.
	slices.Sort(files)

	if len(files) == 0 {
		fatalf("No files matched pattern: %s", flags.pattern)
	}

	fmt.Printf("Found %d file(s) matching pattern %s\n", len(files), quote(flags.pattern, flags.output))

	if flags.check {
		fmt.Println("Running in check mode - no files will be modified")
	} else if flags.dryRun {
		fmt.Println("Running in dry-run mode - no files will be modified")
	}

	return files
}

// runConfigFileMode applies every update in the config, in the order Terraform version, providers,
// modules, and returns the update-operation total.
func runConfigFileMode(files []string, flags *cliFlags) (int, error) {
	config, err := loadResolvedConfig(flags.configFile, flags.branch)
	if err != nil {
		return 0, err
	}
	// -validate-config rejects a config that asks for nothing, so an update run does too, or a
	// file truncated by a bad merge would be a cheerful no-op. The refusal is here rather than in
	// the loader, which audit mode shares and which must keep auditing such a config.
	if config.declaresNoUpdates() {
		return 0, fmt.Errorf("Error: %w", errConfigDeclaresNoUpdates)
	}

	var outcome configOutcome
	if config.TerraformVersion != "" {
		outcome.terraformUpdates, outcome.terraformErrors = processTerraformVersion(files, config.TerraformVersion, flags.dryRun, flags.output, flags.reportRecorder())
	}
	for _, provider := range config.Providers {
		updates, skips, errs := processProviderVersion(files, provider.Name, provider.Version, flags.dryRun, flags.output, flags.reportRecorder())
		outcome.providerUpdates += updates
		outcome.providerSkips += skips
		outcome.providerErrors += errs
	}
	if len(config.Modules) > 0 {
		outcome.moduleUpdates, outcome.moduleSkips, outcome.moduleErrors = processFiles(files, config.Modules, flags)
	}

	printConfigSummary(outcome, flags.dryRun)
	switch {
	case outcome.onlyModuleErrors():
		return outcome.updates(), fmt.Errorf("Error: %d module update error(s)", outcome.moduleErrors)
	case outcome.errors() > 0:
		return outcome.updates(), fmt.Errorf("Error: %d update error(s)", outcome.errors())
	}
	return outcome.updates(), nil
}

// runCLIMode applies the one direct operation the flags name and returns its update total.
func runCLIMode(files []string, flags *cliFlags) (int, error) {
	switch {
	case flags.terraformVersion != "":
		totalUpdates, totalErrors := processTerraformVersion(files, flags.terraformVersion, flags.dryRun, flags.output, flags.reportRecorder())
		printTerraformSummary(totalUpdates, totalErrors, flags.dryRun)
		if totalErrors > 0 {
			return totalUpdates, fmt.Errorf("Error: %d Terraform version update error(s)", totalErrors)
		}
		return totalUpdates, nil
	case flags.providerName != "":
		if flags.toVersion == "" {
			fatalf("Error: -to flag is required when using -provider")
		}
		totalUpdates, totalSkips, totalErrors := processProviderVersion(files, flags.providerName, flags.toVersion, flags.dryRun, flags.output, flags.reportRecorder())
		printProviderSummary(flags.providerName, totalUpdates, totalErrors, totalSkips, flags.dryRun, flags.output)
		if totalErrors > 0 {
			return totalUpdates, fmt.Errorf("Error: %d provider update error(s)", totalErrors)
		}
		return totalUpdates, nil
	default:
		totalUpdates, totalSkips, totalErrors := processFiles(files, loadModuleUpdates(flags), flags)
		printSummary(totalUpdates, totalErrors, totalSkips, flags.dryRun)
		if totalErrors > 0 {
			return totalUpdates, fmt.Errorf("Error: %d module update error(s)", totalErrors)
		}
		return totalUpdates, nil
	}
}

// configOutcome counts what one config-mode run applied, or would apply in a dry run, and what it
// failed on. Without the failures a run whose every update failed would read like one with
// nothing to do, sending the operator to the config when the files were at fault.
type configOutcome struct {
	terraformUpdates, terraformErrors int
	providerUpdates, providerErrors   int
	moduleUpdates, moduleErrors       int
	moduleSkips, providerSkips        int
}

func (o configOutcome) updates() int {
	return o.terraformUpdates + o.providerUpdates + o.moduleUpdates
}

func (o configOutcome) errors() int {
	return o.terraformErrors + o.providerErrors + o.moduleErrors
}

// onlyModuleErrors reports whether every failure was a module update, so the run's error can name
// modules rather than blame the whole config.
func (o configOutcome) onlyModuleErrors() bool {
	return o.terraformErrors == 0 && o.providerErrors == 0 && o.moduleErrors > 0
}

// printConfigSummary prints a config-mode run's summary, then its failure and skip notes. The
// no-op wording points at -audit-file, which can say which updates were already applied, skipped
// or matched nothing; a run that updated nothing and failed prints only the notes.
func printConfigSummary(outcome configOutcome, dryRun bool) {
	switch {
	case outcome.updates() > 0:
		fmt.Println("\n" + strings.Repeat("=", 50))
		fmt.Println("Config File Update Summary")
		fmt.Println(strings.Repeat("=", 50))
		printConfigCount(outcome.terraformUpdates, dryRun, "Terraform version: would update %d file(s)", "Terraform version: %d file(s) updated")
		printConfigCount(outcome.providerUpdates, dryRun, "Providers: would apply %d update(s)", "Providers: %d update(s) applied")
		printConfigCount(outcome.moduleUpdates, dryRun, "Modules: would apply %d update(s)", "Modules: %d update(s) applied")
	case outcome.errors() > 0:
		// The failure note is the whole summary: a nothing-to-do line would blame the config.
		fmt.Println()
	case dryRun:
		fmt.Println("\nNo updates would be performed. Every configured update is already applied, skipped or matched nothing; use -audit-file to see which.")
	default:
		fmt.Println("\nNo updates were performed. Every configured update is already applied, skipped or matched nothing; use -audit-file to see which.")
	}
	fmt.Print(failureNote(outcome.errors()))
	fmt.Print(skipNote(outcome.providerSkips, "provider"))
	fmt.Print(skipNote(outcome.moduleSkips, "module"))
}

// printConfigCount prints one line of the config summary when its count is non-zero.
func printConfigCount(count int, dryRun bool, dryRunFormat, format string) {
	if count == 0 {
		return
	}
	if dryRun {
		format = dryRunFormat
	}
	fmt.Printf(format+"\n", count)
}

// printTerraformSummary prints the summary of a direct Terraform version run. A required_version
// is set whether or not one is there, so nothing is ever skipped.
func printTerraformSummary(totalUpdates, errorCount int, dryRun bool) {
	line := fmt.Sprintf("Successfully updated Terraform version in %d file(s)", totalUpdates)
	if dryRun {
		line = fmt.Sprintf("Dry run: would update Terraform version in %d file(s)", totalUpdates)
	}
	printRunSummary(line, totalUpdates, errorCount, "", dryRun)
}

// printProviderSummary prints the summary of a direct provider run.
func printProviderSummary(providerName string, totalUpdates, errorCount, skipCount int, dryRun bool, outputFormat string) {
	line := fmt.Sprintf("Successfully updated %s provider version in %d file(s)", quote(providerName, outputFormat), totalUpdates)
	if dryRun {
		line = fmt.Sprintf("Dry run: would update %s provider version in %d file(s)", quote(providerName, outputFormat), totalUpdates)
	}
	printRunSummary(line, totalUpdates, errorCount, skipNote(skipCount, "provider"), dryRun)
}

// processTerraformVersion sets required_version in every file's terraform blocks and returns the
// files updated, or that a dry run would update, and the files that failed.
func processTerraformVersion(files []string, version string, dryRun bool, outputFormat string, report *updateReport) (totalUpdates, totalErrors int) {
	for _, file := range files {
		updated, changedBlocks, err := updateTerraformVersionWithCount(file, version, dryRun)
		if err != nil {
			log.Printf("Error processing %s: %v", file, err)
			totalErrors++
			continue
		}
		if !updated {
			continue
		}
		if report != nil && !dryRun && len(changedBlocks) > 0 {
			report.recordTerraformBlocks(file, changedBlocks)
		}
		prefix, action := updateVerb(dryRun)
		fmt.Printf("%s %s Terraform required_version to %s in %s\n", prefix, action, quote(version, outputFormat), file)
		totalUpdates++
	}
	return totalUpdates, totalErrors
}

// processProviderVersion sets one provider's version in every file's required_providers and
// returns the files updated, the entries matched but left as they were, and the files that failed.
func processProviderVersion(files []string, providerName, version string, dryRun bool, outputFormat string, report *updateReport) (totalUpdates, totalSkips, totalErrors int) {
	for _, file := range files {
		updated, changedBlocks, skipReasons, err := updateProviderVersionWithCount(file, providerName, version, dryRun)
		for _, reason := range skipReasons {
			fmt.Fprintf(os.Stderr, "Warning: Provider %s in %s %s, skipping\n", quote(providerName, outputFormat), file, reason)
		}
		totalSkips += len(skipReasons)
		if err != nil {
			log.Printf("Error processing %s: %v", file, err)
			totalErrors++
			continue
		}
		if !updated {
			continue
		}
		if report != nil && !dryRun && len(changedBlocks) > 0 {
			report.recordProviderBlocks(changedBlocks)
		}
		prefix, action := updateVerb(dryRun)
		fmt.Printf("%s %s provider %s to version %s in %s\n", prefix, action, quote(providerName, outputFormat), quote(version, outputFormat), file)
		totalUpdates++
	}
	return totalUpdates, totalSkips, totalErrors
}

// updateTerraformVersionWithCount sets required_version in each terraform block of the file that
// does not already hold it, adding the attribute where it is missing, and returns the indexes of
// the blocks it changed.
func updateTerraformVersionWithCount(filename, version string, dryRun bool) (updated bool, changedBlocks []int, err error) {
	file, err := readTerraformFile(filename)
	if err != nil {
		return false, nil, err
	}

	for blockIndex, block := range file.hcl.Body().Blocks() {
		if block.Type() != "terraform" {
			continue
		}
		current := block.Body().GetAttribute("required_version")
		if current != nil && attributeHasStringValue(current, version) {
			continue
		}
		block.Body().SetAttributeValue("required_version", cty.StringVal(version))
		updated = true
		changedBlocks = append(changedBlocks, blockIndex)
	}

	if updated && !dryRun {
		if err := file.write(); err != nil {
			return false, nil, err
		}
	}

	return updated, changedBlocks, nil
}

// updateProviderVersionWithCount sets a provider's version in each required_providers of the
// file, in both the block form (aws { version = "..." }) and the object form
// (aws = { version = "..." }). changedBlocks locates each changed declaration; skipReasons
// describes each declaration the run matched but would not change, in the words of its warning.
func updateProviderVersionWithCount(filename, providerName, version string, dryRun bool) (updated bool, changedBlocks, skipReasons []string, err error) {
	file, err := readTerraformFile(filename)
	if err != nil {
		return false, nil, nil, err
	}

	for blockIndex, block := range file.hcl.Body().Blocks() {
		blockUpdated, blockChanges, blockSkipReasons := updateProviderTerraformBlockResult(block, providerName, version)
		updated = updated || blockUpdated
		skipReasons = append(skipReasons, blockSkipReasons...)
		for _, blockChange := range blockChanges {
			changedBlocks = append(changedBlocks, fmt.Sprintf("%d/%s", blockIndex, blockChange))
		}
	}

	if updated && !dryRun {
		if err := file.write(); err != nil {
			return false, nil, skipReasons, err
		}
	}

	return updated, changedBlocks, skipReasons, nil
}

// updateProviderTerraformBlockResult updates the provider in each required_providers block of a
// terraform block. A required_providers that declares the provider in block form is not also
// searched for the object form.
func updateProviderTerraformBlockResult(block *hclwrite.Block, providerName, version string) (updated bool, changedBlocks, skipReasons []string) {
	if block.Type() != "terraform" {
		return false, nil, nil
	}

	for nestedIndex, nestedBlock := range block.Body().Blocks() {
		if nestedBlock.Type() != "required_providers" {
			continue
		}
		if blockSyntaxChanges := updateProviderBlockSyntaxResult(nestedBlock, providerName, version); len(blockSyntaxChanges) > 0 {
			updated = true
			for _, blockSyntaxIndex := range blockSyntaxChanges {
				changedBlocks = append(changedBlocks, fmt.Sprintf("%d/block/%d", nestedIndex, blockSyntaxIndex))
			}
			continue
		}
		attributeUpdated, skipReason := updateProviderAttributeVersionResult(nestedBlock, providerName, version)
		if skipReason != "" {
			skipReasons = append(skipReasons, skipReason)
		}
		if attributeUpdated {
			updated = true
			changedBlocks = append(changedBlocks, fmt.Sprintf("%d/attribute/%s", nestedIndex, providerName))
		}
	}

	return updated, changedBlocks, skipReasons
}

// updateProviderBlockSyntaxResult sets the version in each block-form declaration of the provider
// that does not already hold it, and returns the indexes of the blocks it changed.
func updateProviderBlockSyntaxResult(nestedBlock *hclwrite.Block, providerName, version string) (changedBlocks []int) {
	for providerIndex, providerBlock := range nestedBlock.Body().Blocks() {
		if providerBlock.Type() != providerName {
			continue
		}
		current := providerBlock.Body().GetAttribute("version")
		if current != nil && attributeHasStringValue(current, version) {
			continue
		}
		providerBlock.Body().SetAttributeValue("version", cty.StringVal(version))
		changedBlocks = append(changedBlocks, providerIndex)
	}
	return changedBlocks
}

// updateProviderAttributeVersionResult replaces the version in an object-form declaration of the
// provider. Only the version expression's bytes are replaced, so other items such as
// configuration_aliases remain. skipReason describes a declaration the run matched but would not
// change: one with no version item, which updates do not add, or one that is a bare version
// string, which updates do not change.
func updateProviderAttributeVersionResult(nestedBlock *hclwrite.Block, providerName, newVersion string) (updated bool, skipReason string) {
	objExpr, expression, ok, present := providerAttributeObject(nestedBlock, providerName)
	if !ok {
		if !present {
			return false, ""
		}
		return false, "is a version string rather than an object or block, which updates do not change"
	}

	updatedExpression, hasVersion, changed := replaceProviderObjectVersion(objExpr, expression, newVersion)
	if !hasVersion {
		return false, "has no version argument and updates cannot add one"
	}
	if !changed {
		return false, ""
	}

	// hclwrite builds tokens only from a parsed body, so the expression is parsed as an attribute.
	newAttribute := append([]byte(providerName+" = "), updatedExpression...)
	parsed, diags := hclwrite.ParseConfig(newAttribute, "inline", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return false, ""
	}
	newAttr := parsed.Body().GetAttribute(providerName)
	if newAttr == nil {
		return false, ""
	}
	nestedBlock.Body().SetAttributeRaw(providerName, newAttr.Expr().BuildTokens(nil))
	return true, ""
}

// providerAttributeObject returns the provider's object-form declaration and its source bytes.
// present separates a declaration that is not an object, such as a bare version string, from a
// provider that is not declared in attribute form at all.
func providerAttributeObject(nestedBlock *hclwrite.Block, providerName string) (objExpr *hclsyntax.ObjectConsExpr, expression []byte, ok, present bool) {
	attr, exists := nestedBlock.Body().Attributes()[providerName]
	if !exists {
		return nil, nil, false, false
	}

	expression = attr.Expr().BuildTokens(nil).Bytes()
	expr, diags := hclsyntax.ParseExpression(expression, "inline", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, nil, false, true
	}

	objExpr, ok = expr.(*hclsyntax.ObjectConsExpr)
	return objExpr, expression, ok, true
}

// replaceProviderObjectVersion returns expression with each version item's value replaced by
// newVersion. Items are visited last to first so earlier byte offsets stay valid as the bytes
// change. hasVersion reports whether the object has a version item; changed whether any was
// not already at newVersion.
func replaceProviderObjectVersion(objExpr *hclsyntax.ObjectConsExpr, expression []byte, newVersion string) (updated []byte, hasVersion, changed bool) {
	updated = slices.Clone(expression)
	newValue := hclwrite.TokensForValue(cty.StringVal(newVersion)).Bytes()

	for index := len(objExpr.Items) - 1; index >= 0; index-- {
		item := objExpr.Items[index]
		if key, ok := providerObjectItemKey(item); !ok || key != "version" {
			continue
		}
		start, end, ok := objectItemValueRange(item, expression)
		if !ok {
			return nil, false, false
		}
		hasVersion = true
		if expressionHasStringValue(expression[start:end], newVersion) {
			continue
		}
		updated = slices.Concat(updated[:start], newValue, updated[end:])
		changed = true
	}

	return updated, hasVersion, changed
}

// objectItemValueRange returns the byte range of an object item's value within the expression it
// was parsed from, or false for a range the expression does not contain.
func objectItemValueRange(item hclsyntax.ObjectConsItem, expression []byte) (start, end int, ok bool) {
	valueRange := item.ValueExpr.Range()
	start, end = valueRange.Start.Byte, valueRange.End.Byte
	return start, end, start >= 0 && start <= end && end <= len(expression)
}

// expressionHasStringValue reports whether expression evaluates to value as an update would write
// it. cty strings are NFC-normalised, so value is normalised the same way; otherwise a version
// written from a decomposed string would never read back as applied.
func expressionHasStringValue(expression []byte, value string) bool {
	expr, diags := hclsyntax.ParseExpression(expression, "inline", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return false
	}
	expressionValue, diags := expr.Value(&hcl.EvalContext{})
	return !diags.HasErrors() && expressionValue.IsKnown() && !expressionValue.IsNull() &&
		expressionValue.Type().Equals(cty.String) && expressionValue.Equals(cty.StringVal(value)).True()
}

// providerObjectItemKey returns the bare identifier an object item is keyed by, such as the
// "version" in version = "...".
func providerObjectItemKey(item hclsyntax.ObjectConsItem) (string, bool) {
	keyExpr, ok := item.KeyExpr.(*hclsyntax.ObjectConsKeyExpr)
	if !ok {
		return "", false
	}

	traversal, ok := keyExpr.Wrapped.(*hclsyntax.ScopeTraversalExpr)
	if !ok || len(traversal.Traversal) == 0 {
		return "", false
	}

	rootName, ok := traversal.Traversal[0].(hcl.TraverseRoot)
	if !ok {
		return "", false
	}

	return rootName.Name, true
}

// updateModuleVersionWithCount reads the file and applies one module update to it; see
// applyModuleVersion. Most module tests call this wrapper.
func updateModuleVersionWithCount(filename, moduleSource, version string, fromVersions, ignoreVersions, ignorePatterns []string, forceAdd, dryRun, verbose bool, outputFormat string) (updated bool, changedBlocks, skippedBlocks []int, err error) {
	file, err := readTerraformFile(filename)
	if err != nil {
		return false, nil, nil, err
	}
	return applyModuleVersion(file, moduleSource, version, fromVersions, ignoreVersions, ignorePatterns, forceAdd, dryRun, verbose, outputFormat)
}

// terraformFile is a Terraform file parsed once, with the identity the untrusted-file check
// compares.
type terraformFile struct {
	name string
	info os.FileInfo
	hcl  *hclwrite.File
}

// rewritableFile is an open Terraform file that a write replaces in place.
type rewritableFile interface {
	io.Reader
	io.WriterAt
	Truncate(size int64) error
	Sync() error
	Close() error
}

// backupFile holds a Terraform file's original bytes while the file is rewritten.
type backupFile interface {
	io.Writer
	Sync() error
	Close() error
	Name() string
}

// untrustedFile is a Terraform file whose failed write kept a backup, so its content cannot be trusted.
type untrustedFile struct {
	info   os.FileInfo
	backup string
}

var (
	untrustedMu    sync.Mutex
	untrustedFiles []untrustedFile // refused by readTerraformFile for the rest of the run
)

// anyUntrusted reports whether a write left a file whose content cannot be confirmed. No update is
// counted for such a file, so the update total alone cannot say whether the run changed the tree.
func anyUntrusted() bool {
	untrustedMu.Lock()
	defer untrustedMu.Unlock()
	return len(untrustedFiles) > 0
}

func markUntrusted(info os.FileInfo, backup string) {
	untrustedMu.Lock()
	defer untrustedMu.Unlock()
	untrustedFiles = append(untrustedFiles, untrustedFile{info: info, backup: backup})
}

// untrustedBackup returns the backup holding an untrusted file's original bytes. It compares file
// identities, so the same file reached through a symlink or a hard link is found too.
func untrustedBackup(info os.FileInfo) (backup string, untrusted bool) {
	untrustedMu.Lock()
	defer untrustedMu.Unlock()
	for _, file := range untrustedFiles {
		if os.SameFile(info, file.info) {
			return file.backup, true
		}
	}
	return "", false
}

// readTerraformFile stats, reads and parses a Terraform file, refusing one an earlier failed write
// left untrusted.
func readTerraformFile(filename string) (*terraformFile, error) {
	fileInfo, err := os.Stat(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}
	if backup, untrusted := untrustedBackup(fileInfo); untrusted {
		return nil, fmt.Errorf("file left untrusted by an earlier failed write; original content is in %s", backup)
	}

	src, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	file, diags := hclwrite.ParseConfig(src, filename, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, fmt.Errorf("failed to parse HCL: %s", diags.Error())
	}

	return &terraformFile{name: filename, info: fileInfo, hcl: file}, nil
}

// write formats the file and rewrites it in place, so its permission bits, owner and links are
// untouched. The original bytes are backed up first: a failed rewrite is undone and the backup
// removed, and when the file's state cannot be confirmed the backup is kept and the error names it.
func (file *terraformFile) write() error {
	handle, err := openFileForRewrite(file.name)
	if err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}
	original, err := io.ReadAll(handle)
	if err != nil {
		_ = handle.Close()
		return fmt.Errorf("failed to write file: %w", err)
	}
	backup, err := writeBackup(file.name, original)
	if err != nil {
		_ = handle.Close()
		return fmt.Errorf("failed to write file: cannot back up %s: %w", file.name, err)
	}
	outcome, err := rewriteContents(handle, original, hclwrite.Format(file.hcl.Bytes()))
	if err != nil {
		_ = handle.Close()
		if outcome == rewriteUncertain {
			return file.keepBackup(backup, err)
		}
		removeBackup(backup)
		return fmt.Errorf("failed to write file: %w", err)
	}
	// The rewrite is synced, but a failed close leaves its state unconfirmed. Restoring would mean
	// reopening the file, so, as gofmt does, the backup is kept instead.
	if err := handle.Close(); err != nil {
		return file.keepBackup(backup, err)
	}
	removeBackup(backup)
	return nil
}

// keepBackup reports a write whose file may not hold its original bytes, naming the backup that
// does, and marks the file untrusted so the rest of the run refuses it.
func (file *terraformFile) keepBackup(backup string, err error) error {
	markUntrusted(file.info, backup)
	return fmt.Errorf("failed to write file: %w; original content is in %s", err, backup)
}

// writeBackup copies a Terraform file's original bytes into the system temporary directory and
// returns the copy's path. A copy that fails part way is removed.
func writeBackup(name string, original []byte) (string, error) {
	backup, err := createBackupFile("tf-version-bump-backup-" + filepath.Base(name) + "-*")
	if err != nil {
		return "", err
	}
	path := backup.Name()
	_, err = backup.Write(original)
	if err == nil {
		err = backup.Sync()
	}
	if closeErr := backup.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		removeBackup(path)
		return "", err
	}
	return path, nil
}

// rewriteOutcome says what a rewrite left in the file, so a caller never has to read it back.
type rewriteOutcome int

const (
	// rewriteComplete means the file holds the new bytes.
	rewriteComplete rewriteOutcome = iota
	// rewriteUndone means the rewrite failed and the file holds its original bytes.
	rewriteUndone
	// rewriteUncertain means the rewrite failed and the file may hold neither version in full.
	rewriteUncertain
)

// rewriteContents replaces the file's bytes with formatted. When that fails it writes the original
// bytes back, unless nothing reached the file, and the outcome says which of the three cases happened.
func rewriteContents(file rewritableFile, original, formatted []byte) (rewriteOutcome, error) {
	written, err := file.WriteAt(formatted, 0)
	if err != nil && written == 0 {
		// Nothing reached the file, so it still holds its original bytes.
		return rewriteUndone, err
	}
	if err == nil {
		err = truncateAndSync(file, len(formatted))
	}
	if err == nil {
		return rewriteComplete, nil
	}
	if restoreErr := writeContents(file, original); restoreErr != nil {
		return rewriteUncertain, fmt.Errorf("%w; restoring the original also failed: %w", err, restoreErr)
	}
	return rewriteUndone, fmt.Errorf("%w; original content restored", err)
}

// writeContents writes data over the start of the file, cuts the file to its length and syncs it.
func writeContents(file rewritableFile, data []byte) error {
	if _, err := file.WriteAt(data, 0); err != nil {
		return err
	}
	return truncateAndSync(file, len(data))
}

func truncateAndSync(file rewritableFile, size int) error {
	if err := file.Truncate(int64(size)); err != nil {
		return err
	}
	return file.Sync()
}

// removeBackup deletes a backup that is no longer needed. The Terraform file holds the content it
// should by then, so a failure is only a warning.
func removeBackup(backup string) {
	if err := os.Remove(backup); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not remove backup %s: %v\n", backup, err)
	}
}

// applyModuleVersion applies one module update to a parsed file, changing it in memory so a later
// update to the same file meets this one's result, and writes the file unless dryRun is set.
// changedBlocks indexes the module blocks whose version changed; skippedBlocks indexes those left
// unpinned by a skip the run warns about.
func applyModuleVersion(file *terraformFile, moduleSource, version string, fromVersions, ignoreVersions, ignorePatterns []string, forceAdd, dryRun, verbose bool, outputFormat string) (updated bool, changedBlocks, skippedBlocks []int, err error) {
	opts := moduleUpdateOptions{
		filename:       file.name,
		moduleSource:   moduleSource,
		version:        version,
		fromVersions:   fromVersions,
		ignoreVersions: ignoreVersions,
		ignorePatterns: ignorePatterns,
		forceAdd:       forceAdd,
		verbose:        verbose,
		outputFormat:   outputFormat,
	}

	for blockIndex, block := range file.hcl.Body().Blocks() {
		blockUpdated, blockSkipped := updateModuleBlockResult(block, &opts)
		if blockSkipped {
			skippedBlocks = append(skippedBlocks, blockIndex)
		}
		if blockUpdated {
			updated = true
			changedBlocks = append(changedBlocks, blockIndex)
		}
	}

	if updated && !dryRun {
		if err := file.write(); err != nil {
			return false, nil, skippedBlocks, err
		}
	}

	return updated, changedBlocks, skippedBlocks, nil
}

// moduleUpdateOptions carries one module update's target and filters through the per-block work.
type moduleUpdateOptions struct {
	filename       string
	moduleSource   string
	version        string
	fromVersions   []string
	ignoreVersions []string
	ignorePatterns []string
	forceAdd       bool
	verbose        bool
	outputFormat   string
}

// updateModuleBlockResult applies the update to one block when its source matches. Precedence:
// a local source, then ignore_modules, then a missing version (added only with -force-add on a
// registry source), then ignore_versions, then from. skipped reports a skip that leaves the
// block unpinned or defeats an explicit -force-add, which the run counts; a block excluded by a
// filter, a local module and a block already at the target were skipped as asked, so it does not.
func updateModuleBlockResult(block *hclwrite.Block, opts *moduleUpdateOptions) (updated, skipped bool) {
	if block.Type() != "module" {
		return false, false
	}

	moduleName := moduleBlockName(block)
	sourceValue, ok := moduleSourceValue(block)
	if !ok || sourceValue != opts.moduleSource {
		return false, false
	}

	if isLocalModule(sourceValue) {
		opts.warnSkip("is a local module and cannot be version-bumped", moduleName)
		return false, false
	}

	if shouldIgnoreModule(moduleName, opts.ignorePatterns) {
		if opts.verbose {
			fmt.Printf("  ⊗ Skipped module %s in %s (matches ignore pattern)\n", quote(moduleName, opts.outputFormat), opts.filename)
		}
		return false, false
	}

	versionAttr := block.Body().GetAttribute("version")
	switch {
	case versionAttr == nil && !opts.forceAdd:
		opts.warnSkip("has no version attribute", moduleName)
		return false, true
	case versionAttr == nil && !isRegistryModule(sourceValue):
		opts.warnSkip("is not a registry module and cannot use a version attribute", moduleName)
		return false, true
	case versionAttr != nil && shouldSkipModuleVersion(moduleName, attributeStringValue(versionAttr), opts):
		return false, false
	case versionAttr != nil && attributeHasStringValue(versionAttr, opts.version):
		return false, false
	}

	block.Body().SetAttributeValue("version", cty.StringVal(opts.version))
	return true, false
}

// warnSkip prints the warning for a matched module the update leaves alone.
func (opts *moduleUpdateOptions) warnSkip(reason, moduleName string) {
	fmt.Fprintf(os.Stderr, "Warning: Module %s in %s (source: %s) %s, skipping\n",
		quote(moduleName, opts.outputFormat), opts.filename, quote(opts.moduleSource, opts.outputFormat), reason)
}

func moduleBlockName(block *hclwrite.Block) string {
	if len(block.Labels()) == 0 {
		return ""
	}
	return block.Labels()[0]
}

func moduleSourceValue(block *hclwrite.Block) (string, bool) {
	sourceAttr := block.Body().GetAttribute("source")
	if sourceAttr == nil {
		return "", false
	}
	return attributeStringValue(sourceAttr), true
}

// attributeStringValue returns an attribute's value as written, without its quotes.
func attributeStringValue(attr *hclwrite.Attribute) string {
	return trimQuotes(strings.TrimSpace(string(attr.Expr().BuildTokens(nil).Bytes())))
}

// attributeHasStringValue reports whether the attribute already evaluates to value, which is the
// updater's no-op test.
func attributeHasStringValue(attr *hclwrite.Attribute, value string) bool {
	return expressionHasStringValue(attr.Expr().BuildTokens(nil).Bytes(), value)
}

// moduleVersionFilter names the config filter that stops an update from currentVersion:
// "ignore_versions" when it lists the version, "from" when it is set and does not, or "" when
// neither applies. ignore_versions takes precedence over from.
func moduleVersionFilter(currentVersion string, ignoreVersions, fromVersions []string) string {
	if slices.Contains(ignoreVersions, currentVersion) {
		return "ignore_versions"
	}
	if len(fromVersions) > 0 && !slices.Contains(fromVersions, currentVersion) {
		return "from"
	}
	return ""
}

// shouldSkipModuleVersion reports whether a version filter excludes the block, printing why with
// -verbose.
func shouldSkipModuleVersion(moduleName, currentVersion string, opts *moduleUpdateOptions) bool {
	switch moduleVersionFilter(currentVersion, opts.ignoreVersions, opts.fromVersions) {
	case "ignore_versions":
		if opts.verbose {
			fmt.Printf("  ⊗ Skipped module %s in %s (current version %s matches 'ignore-version' filter %v)\n", quote(moduleName, opts.outputFormat), opts.filename, quote(currentVersion, opts.outputFormat), opts.ignoreVersions)
		}
		return true
	case "from":
		if opts.verbose {
			fmt.Printf("  ⊗ Skipped module %s in %s (current version %s does not match any 'from' filter %v)\n", quote(moduleName, opts.outputFormat), opts.filename, quote(currentVersion, opts.outputFormat), opts.fromVersions)
		}
		return true
	}
	return false
}

// isLocalModule reports whether a module source is a local path (./, ../ or /), which Terraform
// gives no version attribute.
func isLocalModule(source string) bool {
	return strings.HasPrefix(source, "./") ||
		strings.HasPrefix(source, "../") ||
		strings.HasPrefix(source, "/")
}

// isRegistryModule reports whether a source is a registry address, the only kind of source that
// may carry a version constraint.
func isRegistryModule(source string) bool {
	_, err := tfaddr.ParseModuleSource(source)
	return err == nil
}

// shouldIgnoreModule reports whether a module name matches any ignore pattern. A module block
// always has a name label, so an empty name means malformed HCL and never matches.
func shouldIgnoreModule(moduleName string, patterns []string) bool {
	if moduleName == "" {
		return false
	}
	return slices.ContainsFunc(patterns, func(pattern string) bool {
		return matchPattern(moduleName, pattern)
	})
}

// matchPattern reports whether name matches pattern, in which '*' matches zero or more characters:
// "vpc" exactly, "legacy-*" a prefix, "*-test" a suffix, "*-vpc-*" a substring. Consecutive
// wildcards act as one.
func matchPattern(name, pattern string) bool {
	if !strings.Contains(pattern, "*") {
		return name == pattern
	}

	parts := strings.Split(pattern, "*")
	first, last := parts[0], parts[len(parts)-1]
	if !strings.HasPrefix(name, first) || !strings.HasSuffix(name, last) || len(name) < len(first)+len(last) {
		return false
	}

	// The middle parts must appear in order between the prefix and the suffix. Taking each at its
	// first occurrence is safe, because a later occurrence leaves less room for the rest.
	middle := name[len(first) : len(name)-len(last)]
	for _, part := range parts[1 : len(parts)-1] {
		index := strings.Index(middle, part)
		if index < 0 {
			return false
		}
		middle = middle[index+len(part):]
	}
	return true
}

// trimQuotes removes matching surrounding single or double quotes, if any.
func trimQuotes(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}
