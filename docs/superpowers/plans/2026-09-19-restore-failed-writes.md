# Restore Failed Writes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Terraform file whose write fails is either restored byte-for-byte or has its original kept in a named backup and is refused for the rest of the run, instead of being left truncated (issue #151).

**Architecture:** `terraformFile.write` stops using `os.WriteFile`. It opens the file without `O_CREATE` or `O_TRUNC`, reads its bytes, copies them to a backup in `os.TempDir()`, rewrites the file in place, and on failure writes the original bytes back. Two unexported hooks (`openFileForRewrite`, `createBackupFile`) let tests wrap real files that fail a chosen call. A run-wide set of untrusted files, checked by `readTerraformFile` with `os.SameFile`, refuses a file whose backup was kept.

**Tech Stack:** Go 1.25+, standard library only (`os`, `io`, `fmt`, `sync`); tests use `testing`, `t.Setenv`, `t.TempDir`. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-09-19-restore-failed-writes-design.md` (read it before starting; this plan implements it).

## Global Constraints

- Branch: `fix/151-restore-failed-writes` in `/Users/dan/Code/tf-version-bump`. All Go code stays in the single flat `main` package.
- Commit with `/Users/dan/.claude/bin/claude-git -C /Users/dan/Code/tf-version-bump commit -F <message file>`; write each message file under `/private/tmp/claude-501/-Users-dan-Code-tf-version-bump/c2a38027-e2dd-4db5-9bcc-05f7352d90d9/scratchpad/`. Stage named paths only, never `git add -A`. Never skip hooks.
- Every commit message ends with these two lines: `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>` and `Claude-Session: https://claude.ai/code/session_01AjGBAkcw44iiq19Rp9Fmjo`.
- Error prefix `failed to write file:` is unchanged. A read-only file must still fail with exactly `failed to write file: open <name>: permission denied`.
- Backup name pattern: `"tf-version-bump-backup-" + filepath.Base(name) + "-*"` passed to `os.CreateTemp("", …)`.
- Untrusted-file read error, exactly: `file left untrusted by an earlier failed write; original content is in <backup>`.
- Removal warning to stderr, exactly: `Warning: could not remove backup <backup>: <err>` followed by a newline.
- Tests: TDD; table-driven with `t.Run` where it fits; names `Test<Function>_<Scenario>`; assert every diagnostic; test output must be pristine; no test runs in parallel.
- Total coverage stays at or above 90%. `golangci-lint` v2.12 must pass (enabled: errcheck, govet, ineffassign, staticcheck, unused, gocritic, gocyclo ≤ 15, misspell, unconvert, unparam, whitespace).
- Australian/British spelling in comments and prose. Each Markdown paragraph and list item on one line (`TestDocumentationProseIsNotHardWrapped`).
- Comments explain what and why, never what changed.

## File Structure

- `main.go` — the hooks (top-level `var` block with `hookMu`), the `rewritableFile` and `backupFile` interfaces, `terraformFile` and its `write`, `writeBackup`, `rewriteContents`, `truncateAndSync`, `writeContents`, `removeBackup`, the untrusted-file set, and `readTerraformFile`. All of it sits beside the existing `terraformFile` code (around `main.go:1296`).
- `test_helpers_test.go` — the shared test double `failingFile`, the hook stubs `failRewrite` and `failBackup`, and the temporary-directory helpers `setTempDir`, `useTempDir` and `backupsIn`. Shared because both `module_update_test.go` and `command_test.go` use them.
- `module_update_test.go` — the `TestTerraformFileWrite_*` unit tests, beside `TestUpdateModuleVersionErrors`.
- `command_test.go` — one command-level test for the untrusted-file refusal across passes, and a reworded comment.
- `CLAUDE.md`, `AGENTS.md`, `docs/USAGE.md` — documentation of the new guarantee.

---

### Task 1: Rewrite in place from a backup, restoring on failure

**Files:**
- Modify: `main.go` (hook `var` block near line 65; `terraformFile`, `readTerraformFile` and `write` near lines 1296-1328)
- Modify: `test_helpers_test.go` (append helpers; add imports)
- Test: `module_update_test.go` (append tests after `TestUpdateModuleVersionErrors`; add imports)

**Interfaces:**
- Consumes: `updateModuleVersion(filename, moduleSource, version string, fromVersions, ignoreVersions, ignorePatterns []string, forceAdd, dryRun, verbose bool, outputFormat string) (bool, error)` from `test_helpers_test.go`; `writeTestFile`, `readTestFile`.
- Produces: `type rewritableFile interface { io.Reader; io.WriterAt; Truncate(size int64) error; Sync() error; Close() error }`; `type backupFile interface { io.Writer; Sync() error; Close() error; Name() string }`; `var openFileForRewrite func(name string) (rewritableFile, error)`; `var createBackupFile func(pattern string) (backupFile, error)`; `func writeBackup(name string, original []byte) (string, error)`; `func rewriteContents(file rewritableFile, original, formatted []byte) (restored bool, err error)`; `func (file *terraformFile) keepBackup(backup string, err error) error`; `func removeBackup(backup string)`. Test side: `type failingFile struct { *os.File; failOn map[string][]int; partial int; afterClose func(); calls map[string]int }`, `var errInjected`, `func failRewrite(t *testing.T, target *failingFile)`, `func failBackup(t *testing.T, target *failingFile)`, `func setTempDir(t *testing.T, dir string)`, `func useTempDir(t *testing.T) string`, `func backupsIn(t *testing.T, dir string) []string`, `func moduleAt(version string) string`, `const vpcSource`.

- [ ] **Step 1: Add the test helpers**

Add `"errors"`, `"slices"`, `"strings"` and `"testing"` to the imports of `test_helpers_test.go` if absent (`testing` and `path/filepath` are already there), then append:

```go
var errInjected = errors.New("injected failure")

// failingFile passes every call through to a real file except the calls chosen to fail, so a test
// can make a write fail part way and still assert on the bytes that reached the disk.
type failingFile struct {
	*os.File
	failOn     map[string][]int // method name → the 1-based calls of it that fail
	partial    int              // bytes a failing WriteAt stores before it fails
	afterClose func()           // runs after a successful Close
	calls      map[string]int
}

func (f *failingFile) fails(method string) bool {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[method]++
	return slices.Contains(f.failOn[method], f.calls[method])
}

func (f *failingFile) Read(p []byte) (int, error) {
	if f.fails("Read") {
		return 0, errInjected
	}
	return f.File.Read(p)
}

func (f *failingFile) Write(p []byte) (int, error) {
	if f.fails("Write") {
		return 0, errInjected
	}
	return f.File.Write(p)
}

func (f *failingFile) WriteAt(p []byte, off int64) (int, error) {
	if !f.fails("WriteAt") {
		return f.File.WriteAt(p, off)
	}
	written, err := f.File.WriteAt(p[:min(f.partial, len(p))], off)
	if err != nil {
		return written, err
	}
	return written, errInjected
}

func (f *failingFile) Truncate(size int64) error {
	if f.fails("Truncate") {
		return errInjected
	}
	return f.File.Truncate(size)
}

func (f *failingFile) Sync() error {
	if f.fails("Sync") {
		return errInjected
	}
	return f.File.Sync()
}

func (f *failingFile) Close() error {
	if f.fails("Close") {
		_ = f.File.Close()
		return errInjected
	}
	if err := f.File.Close(); err != nil {
		return err
	}
	if f.afterClose != nil {
		f.afterClose()
	}
	return nil
}

// failRewrite makes every Terraform file opened for rewriting behave as target describes. The lock
// is held only while swapping the hook, because runMainCommand holds hookMu for a whole run.
func failRewrite(t *testing.T, target *failingFile) {
	t.Helper()
	hookMu.Lock()
	original := openFileForRewrite
	openFileForRewrite = func(name string) (rewritableFile, error) {
		file, err := original(name)
		if err != nil {
			return nil, err
		}
		target.File = file.(*os.File)
		return target, nil
	}
	hookMu.Unlock()
	t.Cleanup(func() {
		hookMu.Lock()
		openFileForRewrite = original
		hookMu.Unlock()
	})
}

// failBackup makes every backup a write creates behave as target describes.
func failBackup(t *testing.T, target *failingFile) {
	t.Helper()
	hookMu.Lock()
	original := createBackupFile
	createBackupFile = func(pattern string) (backupFile, error) {
		file, err := original(pattern)
		if err != nil {
			return nil, err
		}
		target.File = file.(*os.File)
		return target, nil
	}
	hookMu.Unlock()
	t.Cleanup(func() {
		hookMu.Lock()
		createBackupFile = original
		hookMu.Unlock()
	})
}

// setTempDir points os.TempDir at dir for the test. Windows reads TMP and TEMP instead of TMPDIR.
func setTempDir(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, dir)
	}
}

// useTempDir points os.TempDir at a fresh directory, so the test sees every backup a write leaves.
func useTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	setTempDir(t, dir)
	return dir
}

// backupsIn lists the backups a write left in dir.
func backupsIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var backups []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "tf-version-bump-backup-") {
			backups = append(backups, filepath.Join(dir, entry.Name()))
		}
	}
	return backups
}
```

- [ ] **Step 2: Write the failing unit tests**

Add `"errors"` and `"io/fs"` to the imports of `module_update_test.go`, then append after `TestUpdateModuleVersionErrors`:

```go
const vpcSource = "terraform-aws-modules/vpc/aws"

// moduleAt is a formatted Terraform file pinning the VPC module at version.
func moduleAt(version string) string {
	return "module \"vpc\" {\n  source  = \"" + vpcSource + "\"\n  version = \"" + version + "\"\n}\n"
}

func TestTerraformFileWrite_SucceedsWithoutLeavingBackup(t *testing.T) {
	backups := useTempDir(t)
	file := writeTestFile(t, t.TempDir(), "main.tf", moduleAt("1.0.0"))

	updated, err := updateModuleVersion(file, vpcSource, "2.0.0", nil, nil, nil, false, false, false, "text")

	if !updated || err != nil {
		t.Fatalf("updated=%v err=%v", updated, err)
	}
	if got := readTestFile(t, file); got != moduleAt("2.0.0") {
		t.Errorf("content = %q, want %q", got, moduleAt("2.0.0"))
	}
	if kept := backupsIn(t, backups); len(kept) != 0 {
		t.Errorf("backups = %v, want none", kept)
	}
}

// Every failure here leaves the file holding its original bytes, so the backup is removed.
func TestTerraformFileWrite_RestoresOriginalAfterFailedRewrite(t *testing.T) {
	const restored = "failed to write file: injected failure; original content restored"
	tests := []struct {
		name    string
		from    string // a longer original makes a complete WriteAt leave a stale tail
		failOn  map[string][]int
		partial int
		wantErr string
	}{
		{name: "write fails part way", from: "1.0.0", failOn: map[string][]int{"WriteAt": {1}}, partial: len(moduleAt("2.0.0")) - 1, wantErr: restored},
		{name: "truncate fails after shorter content", from: "10.0.0", failOn: map[string][]int{"Truncate": {1}}, wantErr: restored},
		{name: "sync fails after shorter content", from: "10.0.0", failOn: map[string][]int{"Sync": {1}}, wantErr: restored},
		{name: "write fails before any byte", from: "1.0.0", failOn: map[string][]int{"WriteAt": {1}}, wantErr: "failed to write file: injected failure"},
		{name: "read fails before backup", from: "1.0.0", failOn: map[string][]int{"Read": {1}}, wantErr: "failed to write file: injected failure"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backups := useTempDir(t)
			file := writeTestFile(t, t.TempDir(), "main.tf", moduleAt(tt.from))
			failRewrite(t, &failingFile{failOn: tt.failOn, partial: tt.partial})

			updated, err := updateModuleVersion(file, vpcSource, "2.0.0", nil, nil, nil, false, false, false, "text")

			if updated || err == nil || err.Error() != tt.wantErr {
				t.Fatalf("updated=%v err=%v, want error %q", updated, err, tt.wantErr)
			}
			if got := readTestFile(t, file); got != moduleAt(tt.from) {
				t.Errorf("content = %q, want the original %q", got, moduleAt(tt.from))
			}
			if kept := backupsIn(t, backups); len(kept) != 0 {
				t.Errorf("backups = %v, want none", kept)
			}
		})
	}
}

func TestTerraformFileWrite_FailsBeforeRewriteWhenBackupFails(t *testing.T) {
	t.Run("temporary directory missing", func(t *testing.T) {
		file := writeTestFile(t, t.TempDir(), "main.tf", moduleAt("1.0.0"))
		setTempDir(t, filepath.Join(t.TempDir(), "missing"))

		_, err := updateModuleVersion(file, vpcSource, "2.0.0", nil, nil, nil, false, false, false, "text")

		// The error quotes CreateTemp's random name and the platform's wording, so only its prefix is exact.
		wantPrefix := "failed to write file: cannot back up " + file + ": "
		if err == nil || !strings.HasPrefix(err.Error(), wantPrefix) || !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("err = %v, want prefix %q wrapping fs.ErrNotExist", err, wantPrefix)
		}
		if got := readTestFile(t, file); got != moduleAt("1.0.0") {
			t.Errorf("content = %q, want the original", got)
		}
	})
	for _, method := range []string{"Write", "Sync", "Close"} {
		t.Run("backup "+method+" fails", func(t *testing.T) {
			backups := useTempDir(t)
			file := writeTestFile(t, t.TempDir(), "main.tf", moduleAt("1.0.0"))
			failBackup(t, &failingFile{failOn: map[string][]int{method: {1}}})

			_, err := updateModuleVersion(file, vpcSource, "2.0.0", nil, nil, nil, false, false, false, "text")

			want := "failed to write file: cannot back up " + file + ": injected failure"
			if err == nil || err.Error() != want {
				t.Fatalf("err = %v, want %q", err, want)
			}
			if got := readTestFile(t, file); got != moduleAt("1.0.0") {
				t.Errorf("content = %q, want the original", got)
			}
			if kept := backupsIn(t, backups); len(kept) != 0 {
				t.Errorf("backups = %v, want the partial backup removed", kept)
			}
		})
	}
}

// When the file cannot be shown to hold either version, the backup is kept and the error names it.
func TestTerraformFileWrite_KeepsBackupWhenFileStateIsUncertain(t *testing.T) {
	tests := []struct {
		name        string
		failOn      map[string][]int
		wantCause   string
		wantContent string // empty when the partial restore leaves no meaningful content to assert
	}{
		{name: "restore fails", failOn: map[string][]int{"WriteAt": {1, 2}}, wantCause: "injected failure; restoring the original also failed: injected failure"},
		{name: "close fails after rewrite", failOn: map[string][]int{"Close": {1}}, wantCause: "injected failure", wantContent: moduleAt("2.0.0")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backups := useTempDir(t)
			file := writeTestFile(t, t.TempDir(), "main.tf", moduleAt("1.0.0"))
			failRewrite(t, &failingFile{failOn: tt.failOn, partial: len(moduleAt("2.0.0")) - 1})

			_, err := updateModuleVersion(file, vpcSource, "2.0.0", nil, nil, nil, false, false, false, "text")

			kept := backupsIn(t, backups)
			if len(kept) != 1 {
				t.Fatalf("backups = %v, want exactly one; err = %v", kept, err)
			}
			want := "failed to write file: " + tt.wantCause + "; original content is in " + kept[0]
			if err == nil || err.Error() != want {
				t.Fatalf("err = %v, want %q", err, want)
			}
			if got := readTestFile(t, kept[0]); got != moduleAt("1.0.0") {
				t.Errorf("backup = %q, want the original", got)
			}
			if tt.wantContent != "" {
				if got := readTestFile(t, file); got != tt.wantContent {
					t.Errorf("content = %q, want %q", got, tt.wantContent)
				}
			}
		})
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test -run 'TestTerraformFileWrite' ./...`

Expected: build failure, `undefined: openFileForRewrite`, `undefined: rewritableFile`, `undefined: createBackupFile`, `undefined: backupFile`.

- [ ] **Step 4: Add the hooks and interfaces**

In `main.go`, extend the `var` block that declares `hookMu`, `exitFunc` and `fatalf` (near line 65) so it reads:

```go
var (
	hookMu   sync.Mutex // guards test hook variables
	exitFunc = os.Exit
	fatalf   = func(format string, v ...interface{}) {
		log.Printf(format, v...)
		exitFunc(1)
	}
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
	// rewritten. It lives in the system temporary directory, so no Terraform glob can select it.
	createBackupFile = func(pattern string) (backupFile, error) {
		file, err := os.CreateTemp("", pattern)
		if err != nil {
			return nil, err
		}
		return file, nil
	}
)
```

- [ ] **Step 5: Replace the write**

In `main.go`, replace the `terraformFile` type, `readTerraformFile` and `write` (currently `main.go:1296-1328`) with:

```go
// terraformFile is a Terraform file parsed once.
type terraformFile struct {
	name string
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

func readTerraformFile(filename string) (*terraformFile, error) {
	if _, err := os.Stat(filename); err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	src, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	file, diags := hclwrite.ParseConfig(src, filename, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, fmt.Errorf("failed to parse HCL: %s", diags.Error())
	}

	return &terraformFile{name: filename, hcl: file}, nil
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
	restored, err := rewriteContents(handle, original, hclwrite.Format(file.hcl.Bytes()))
	if err != nil {
		_ = handle.Close()
		if !restored {
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

// keepBackup reports a write whose file may not hold its original bytes, naming the backup that does.
func (file *terraformFile) keepBackup(backup string, err error) error {
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
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

// rewriteContents replaces the file's bytes with formatted. When that fails it writes the original
// bytes back; restored reports whether the file then holds them, and the error says which happened.
func rewriteContents(file rewritableFile, original, formatted []byte) (restored bool, err error) {
	written, err := file.WriteAt(formatted, 0)
	if err != nil && written == 0 {
		// Nothing reached the file, so it still holds its original bytes.
		return true, err
	}
	if err == nil {
		err = truncateAndSync(file, len(formatted))
	}
	if err == nil {
		return false, nil
	}
	if restoreErr := writeContents(file, original); restoreErr != nil {
		return false, fmt.Errorf("%w; restoring the original also failed: %w", err, restoreErr)
	}
	return true, fmt.Errorf("%w; original content restored", err)
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

// removeBackup deletes a backup that is no longer needed.
func removeBackup(backup string) {
	_ = os.Remove(backup)
}
```

- [ ] **Step 6: Run the new tests to verify they pass**

Run: `go test -run 'TestTerraformFileWrite' -v ./...`

Expected: PASS for every subtest, with no other output.

- [ ] **Step 7: Run the whole suite and the linter**

Run: `go test -race ./...`

Expected: `ok  	github.com/yesdevnull/tf-version-bump`. The read-only tests (`TestUpdateModuleVersionErrors/write_error`, `TestCommandConfigFailedWriteRestartsLaterEntriesFromDisk` and the ones in `terraform_version_test.go` and `provider_update_test.go`) pass unchanged, because opening a 0o400 file for writing fails before any backup exists.

Run: `golangci-lint run --timeout=5m`

Expected: `0 issues.`

- [ ] **Step 8: Commit**

Write the message file `commit-msg.md` in the scratchpad:

```text
fix: restore a Terraform file whose write fails

os.WriteFile truncated the file before writing, so a write that failed
part way left it empty or cut short. The write now backs the original
bytes up to the system temporary directory, rewrites the file in place
and writes the original back if the rewrite fails. When the file's state
cannot be confirmed, the backup is kept and the error names it.

Rewriting in place keeps the file's inode, so hard links, symlinks,
owner and permission bits behave as before, and a read-only file still
fails at the open.

Refs #151

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01AjGBAkcw44iiq19Rp9Fmjo
```

Run: `git -C /Users/dan/Code/tf-version-bump add main.go test_helpers_test.go module_update_test.go`

Run: `/Users/dan/.claude/bin/claude-git -C /Users/dan/Code/tf-version-bump commit -F /private/tmp/claude-501/-Users-dan-Code-tf-version-bump/c2a38027-e2dd-4db5-9bcc-05f7352d90d9/scratchpad/commit-msg.md`

---

### Task 2: Refuse a file whose write kept its backup

**Files:**
- Modify: `main.go` (`terraformFile`, `readTerraformFile`, `keepBackup`; add the untrusted-file set beside them; the comment in `processFiles` near line 387)
- Modify: `test_helpers_test.go` (`failRewrite` cleanup)
- Test: `module_update_test.go` (`TestTerraformFileWrite_KeepsBackupWhenFileStateIsUncertain`), `command_test.go` (new test)

**Interfaces:**
- Consumes: from Task 1, `keepBackup`, `failRewrite`, `failingFile`, `useTempDir`, `backupsIn`, `moduleAt`, `vpcSource`; `runMainCommand(t, args) commandResult` with fields `stdout`, `diagnostics`, `exitCode`.
- Produces: `terraformFile.info os.FileInfo`; `type untrustedFile struct { info os.FileInfo; backup string }`; `var untrustedMu sync.Mutex`; `var untrustedFiles []untrustedFile`; `func markUntrusted(info os.FileInfo, backup string)`; `func untrustedBackup(info os.FileInfo) (backup string, untrusted bool)`.

- [ ] **Step 1: Extend the unit test to require the refusal**

In `module_update_test.go`, in `TestTerraformFileWrite_KeepsBackupWhenFileStateIsUncertain`, replace the line that writes the test file:

```go
			file := writeTestFile(t, t.TempDir(), "main.tf", moduleAt("1.0.0"))
```

with the following, which also makes a hard link so the refusal is checked through both names:

```go
			dir := t.TempDir()
			file := writeTestFile(t, dir, "main.tf", moduleAt("1.0.0"))
			names := []string{file}
			if linked := filepath.Join(dir, "linked.tf"); os.Link(file, linked) == nil {
				names = append(names, linked)
			}
```

Then append inside the subtest, after the `wantContent` check:

```go
			wantRefusal := "file left untrusted by an earlier failed write; original content is in " + kept[0]
			for _, name := range names {
				if _, readErr := readTerraformFile(name); readErr == nil || readErr.Error() != wantRefusal {
					t.Errorf("readTerraformFile(%s) err = %v, want %q", name, readErr, wantRefusal)
				}
			}
```

- [ ] **Step 2: Write the failing command test**

In `command_test.go`, add after `TestCommandConfigFailedWriteRestartsLaterEntriesFromDisk`:

```go
// Once a write keeps its backup, the file's content cannot be trusted, so every later pass in the
// run refuses it rather than applying further changes to a possibly damaged file.
func TestCommandConfigRefusesFileWhoseWriteKeptBackup(t *testing.T) {
	backups := useTempDir(t)
	dir := t.TempDir()
	input := "terraform {\n  required_version = \">= 1.0\"\n  required_providers {\n    aws = {\n      source  = \"hashicorp/aws\"\n      version = \"~> 4.0\"\n    }\n  }\n}\n\nmodule \"example\" {\n  source  = \"example/module\"\n  version = \"1.0.0\"\n}\n"
	file := writeTestFile(t, dir, "main.tf", input)
	config := writeTestFile(t, dir, "versions.yml", "terraform_version: \">= 1.5\"\nproviders:\n  - name: aws\n    version: \"~> 5.0\"\nmodules:\n  - source: example/module\n    version: 2.0.0\n")
	rewrite := &failingFile{failOn: map[string][]int{"WriteAt": {1, 2}}, partial: 1}
	failRewrite(t, rewrite)

	result := runMainCommand(t, []string{"tf-version-bump", "-pattern", file, "-config", config})

	kept := backupsIn(t, backups)
	if len(kept) != 1 {
		t.Fatalf("backups = %v, want exactly one; result = %#v", kept, result)
	}
	failure := "Error processing " + file + ": failed to write file: injected failure; restoring the original also failed: injected failure; original content is in " + kept[0] + "\n"
	refused := "Error processing " + file + ": file left untrusted by an earlier failed write; original content is in " + kept[0] + "\n"
	wantDiagnostics := failure + refused + refused + "3 update error(s)\n"
	if result.diagnostics != wantDiagnostics || result.exitCode != 1 || strings.Contains(result.stdout, "✓") {
		t.Fatalf("result = %#v, want diagnostics %q, no success lines and exit 1", result, wantDiagnostics)
	}
	if got := rewrite.calls["WriteAt"]; got != 2 {
		t.Errorf("WriteAt calls = %d, want only the failed rewrite and its restore", got)
	}
	if got := readTestFile(t, kept[0]); got != input {
		t.Errorf("backup = %q, want the original %q", got, input)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test -run 'TestTerraformFileWrite_KeepsBackupWhenFileStateIsUncertain|TestCommandConfigRefusesFileWhoseWriteKeptBackup' ./...`

Expected: FAIL. `readTerraformFile` returns no error for the kept file, and the command test's provider pass re-reads and rewrites the file, so it prints `✓` and `WriteAt` is called more than twice.

- [ ] **Step 4: Add the untrusted-file set and use it**

In `main.go`, change `terraformFile` to carry the file's identity:

```go
// terraformFile is a Terraform file parsed once, with the identity the untrusted-file check compares.
type terraformFile struct {
	name string
	info os.FileInfo
	hcl  *hclwrite.File
}
```

Add, directly after the `backupFile` interface:

```go
// untrustedFile is a Terraform file whose failed write kept a backup, so its content cannot be trusted.
type untrustedFile struct {
	info   os.FileInfo
	backup string
}

var (
	untrustedMu    sync.Mutex
	untrustedFiles []untrustedFile // refused by readTerraformFile for the rest of the run
)

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
```

Replace the start and the return of `readTerraformFile`:

```go
func readTerraformFile(filename string) (*terraformFile, error) {
	fileInfo, err := os.Stat(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}
	if backup, untrusted := untrustedBackup(fileInfo); untrusted {
		return nil, fmt.Errorf("file left untrusted by an earlier failed write; original content is in %s", backup)
	}
```

and

```go
	return &terraformFile{name: filename, info: fileInfo, hcl: file}, nil
```

Replace `keepBackup` with:

```go
// keepBackup reports a write whose file may not hold its original bytes, naming the backup that
// does, and marks the file untrusted so the rest of the run refuses it.
func (file *terraformFile) keepBackup(backup string, err error) error {
	markUntrusted(file.info, backup)
	return fmt.Errorf("failed to write file: %w; original content is in %s", err, backup)
}
```

In `processFiles`, replace the comment

```go
				// A failed write may not have reached the file, so later updates start from what is
				// on disk rather than from the in-memory change it did not save.
```

with

```go
				// Read the file again rather than keep the unsaved change: a restored file gives later
				// updates its original content, and a file whose backup was kept is refused.
```

- [ ] **Step 5: Clear the set after each test that can fill it**

In `test_helpers_test.go`, replace the `t.Cleanup` in `failRewrite` with:

```go
	t.Cleanup(func() {
		hookMu.Lock()
		openFileForRewrite = original
		hookMu.Unlock()
		// A write that keeps its backup marks the file untrusted for the rest of the run.
		untrustedMu.Lock()
		untrustedFiles = nil
		untrustedMu.Unlock()
	})
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -run 'TestTerraformFileWrite|TestCommandConfigRefusesFileWhoseWriteKeptBackup' -v ./...`

Expected: PASS. If the hard link cannot be created on the platform, the subtest checks the one name only.

- [ ] **Step 7: Run the whole suite and the linter**

Run: `go test -race ./...`

Expected: `ok  	github.com/yesdevnull/tf-version-bump`.

Run: `golangci-lint run --timeout=5m`

Expected: `0 issues.`

- [ ] **Step 8: Commit**

Message file `commit-msg.md` in the scratchpad:

```text
fix: refuse a Terraform file whose write kept its backup

A kept backup means the file may hold a mixture of old and new bytes.
Every update pass re-reads files through readTerraformFile, so it now
refuses a file marked untrusted and names the backup, and each later
entry counts as an error instead of rewriting a damaged file. Files are
compared with os.SameFile, so a symlink or hard link to the same file is
refused too.

Refs #151

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01AjGBAkcw44iiq19Rp9Fmjo
```

Run: `git -C /Users/dan/Code/tf-version-bump add main.go test_helpers_test.go module_update_test.go command_test.go`

Run: `/Users/dan/.claude/bin/claude-git -C /Users/dan/Code/tf-version-bump commit -F /private/tmp/claude-501/-Users-dan-Code-tf-version-bump/c2a38027-e2dd-4db5-9bcc-05f7352d90d9/scratchpad/commit-msg.md`

---

### Task 3: Warn when a backup cannot be removed

**Files:**
- Modify: `main.go` (`removeBackup`)
- Test: `module_update_test.go` (new test; add `"runtime"` to imports)

**Interfaces:**
- Consumes: from Task 1, `failRewrite`, `failingFile.afterClose`, `useTempDir`, `backupsIn`, `moduleAt`, `vpcSource`; `captureStderr(t, fn) string`.
- Produces: `removeBackup` prints the warning.

- [ ] **Step 1: Write the failing test**

Append to `module_update_test.go`:

```go
func TestTerraformFileWrite_WarnsWhenBackupCannotBeRemoved(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a read-only directory does not block removing a file on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	backups := useTempDir(t)
	file := writeTestFile(t, t.TempDir(), "main.tf", moduleAt("1.0.0"))
	// Closing the rewritten file makes the backup's directory read-only, so the removal that follows fails.
	failRewrite(t, &failingFile{afterClose: func() {
		if err := os.Chmod(backups, 0o500); err != nil {
			t.Fatal(err)
		}
	}})
	t.Cleanup(func() {
		if err := os.Chmod(backups, 0o700); err != nil {
			t.Error(err)
		}
	})

	var updated bool
	var err error
	stderr := captureStderr(t, func() {
		updated, err = updateModuleVersion(file, vpcSource, "2.0.0", nil, nil, nil, false, false, false, "text")
	})

	if !updated || err != nil {
		t.Fatalf("updated=%v err=%v, want a successful write", updated, err)
	}
	kept := backupsIn(t, backups)
	if len(kept) != 1 {
		t.Fatalf("backups = %v, want the one that could not be removed", kept)
	}
	want := "Warning: could not remove backup " + kept[0] + ": remove " + kept[0] + ": permission denied\n"
	if stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
	if got := readTestFile(t, file); got != moduleAt("2.0.0") {
		t.Errorf("content = %q, want %q", got, moduleAt("2.0.0"))
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -run 'TestTerraformFileWrite_WarnsWhenBackupCannotBeRemoved' ./...`

Expected: FAIL with `stderr = "", want "Warning: could not remove backup …"`.

- [ ] **Step 3: Print the warning**

In `main.go`, replace `removeBackup` with:

```go
// removeBackup deletes a backup that is no longer needed. The Terraform file is already correct, so
// a failure is only a warning.
func removeBackup(backup string) {
	if err := os.Remove(backup); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not remove backup %s: %v\n", backup, err)
	}
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -run 'TestTerraformFileWrite' -v ./...`

Expected: PASS.

- [ ] **Step 5: Run the whole suite and the linter**

Run: `go test -race ./...` — expected `ok`. Run: `golangci-lint run --timeout=5m` — expected `0 issues.`

- [ ] **Step 6: Commit**

Message file `commit-msg.md` in the scratchpad:

```text
fix: warn when a write's backup cannot be removed

The Terraform file is already written correctly by then, so the write
still succeeds, but a leftover backup is named on stderr so it can be
cleaned up.

Refs #151

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01AjGBAkcw44iiq19Rp9Fmjo
```

Run: `git -C /Users/dan/Code/tf-version-bump add main.go module_update_test.go`

Run: `/Users/dan/.claude/bin/claude-git -C /Users/dan/Code/tf-version-bump commit -F /private/tmp/claude-501/-Users-dan-Code-tf-version-bump/c2a38027-e2dd-4db5-9bcc-05f7352d90d9/scratchpad/commit-msg.md`

---

### Task 4: Document the new write guarantee

**Files:**
- Modify: `CLAUDE.md` (Gotchas; Update flow paragraph; Standard hclwrite pattern; Errors and output)
- Modify: `AGENTS.md` (HCL Processing outline)
- Modify: `docs/USAGE.md` ("File-writing behaviour")
- Modify: `command_test.go` (comment on `TestCommandConfigFailedWriteRestartsLaterEntriesFromDisk`)

**Interfaces:**
- Consumes: the behaviour from Tasks 1-3. No code interfaces.
- Produces: documentation only.

- [ ] **Step 1: Update CLAUDE.md**

Replace the gotcha paragraph beginning `**Don't run concurrent instances over the same files.**` with:

```markdown
**Don't run concurrent instances over the same files.** There is no file locking. A write copies the file's original bytes to a `tf-version-bump-backup-*` file in the system temporary directory, then rewrites the file in place: a failed rewrite is undone and the backup removed, and when the file's state cannot be confirmed the backup is kept, the error names it and `readTerraformFile` refuses the file for the rest of the run. A crash or power loss part way through a write can still leave a mixed file, and the backup may not survive a reboot. Files are processed in memory, so very large files (>100MB) are impractical.
```

In the Update flow paragraph, replace the sentence `A file that cannot be read or parsed counts once per entry; each failed write counts once, and the file is read again for later entries.` with:

```markdown
A file that cannot be read or parsed counts once per entry; each failed write counts once, and the file is read again for later entries, which a file whose write kept its backup refuses.
```

In "Errors and output", replace `Warnings go to stderr prefixed `Warning:` for local modules, missing version attributes without `-force-add`, and non-registry sources where `-force-add` cannot add a version.` with:

```markdown
Warnings go to stderr prefixed `Warning:` for local modules, missing version attributes without `-force-add`, non-registry sources where `-force-add` cannot add a version, and a write's backup that could not be removed.
```

In "Standard hclwrite pattern", replace the two comments so the lines read:

```go
file, err := readTerraformFile(filename) // stat, read and parse; refuses a file a failed write left untrusted
```

```go
    err = file.write() // hclwrite.Format, then an in-place rewrite undone from a backup if it fails
```

- [ ] **Step 2: Update AGENTS.md**

In the "HCL Processing (main.go)" code block, replace `// 1. Stat, read and parse; the file keeps its mode for the write` with `// 1. Stat, read and parse; a file a failed write left untrusted is refused`, and replace `// 3. Format and write back with the original permission bits` with `// 3. Format and rewrite in place; a failed rewrite is undone from a backup`.

- [ ] **Step 3: Update docs/USAGE.md**

In "File-writing behaviour", replace the sentence `The original permission bits are reused when the file is written.` with `Files are rewritten in place, so their permission bits, owner and hard links are untouched, and a symlinked file is updated through its link.`

Replace the paragraph `Writes are not transactional and there is no file locking. Do not run multiple instances against the same files. Keep the files under version control, use `-dry-run`, and review the resulting diff.` with these two paragraphs:

```markdown
Before rewriting a file, the tool copies its original bytes to a file named `tf-version-bump-backup-<file>-<random>` in the system temporary directory (`TMPDIR`, or `TMP` or `TEMP` on Windows), which must be writable and have room for the copy. If the rewrite fails, the original bytes are written back, the backup is removed and the error ends with `original content restored`. If the original cannot be written back, or the file cannot be closed after its rewrite, the backup is kept and the error ends with `original content is in <backup>`: copy that file over the Terraform file to recover it. The Terraform file is then refused for the rest of the run, so every later operation on it reports an error. Pointing the temporary directory inside the Terraform tree lets a later pattern select a leftover backup as an input. A crash or power loss part way through a write can still leave a mixed file, and the backup may not survive a reboot.

There is no file locking. Do not run multiple instances against the same files. Keep the files under version control, use `-dry-run`, and review the resulting diff.
```

- [ ] **Step 4: Reword the command test's comment**

In `command_test.go`, replace the first sentence of the comment on `TestCommandConfigFailedWriteRestartsLaterEntriesFromDisk`, `A failed write may not have reached the file, so later entries start from the file on disk rather than the unsaved change:`, keeping the comment's line wrapping style, with `A failed write leaves the file on disk as it was, so later entries start from that content rather than the unsaved change:`. Leave the rest of the comment and the test unchanged.

- [ ] **Step 5: Run the documentation checks and the suite**

Run: `make docs-check`

Expected: every check passes.

Run: `go test -race ./...`

Expected: `ok`.

- [ ] **Step 6: Commit**

Message file `commit-msg.md` in the scratchpad:

```text
docs: describe how a failed write is undone

Writes are no longer plain truncating rewrites, so the gotchas, the
agent primers and the usage guide now describe the backup, the restore,
the refusal of a file whose backup was kept, and what a crash can still
leave behind.

Refs #151

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01AjGBAkcw44iiq19Rp9Fmjo
```

Run: `git -C /Users/dan/Code/tf-version-bump add CLAUDE.md AGENTS.md docs/USAGE.md command_test.go`

Run: `/Users/dan/.claude/bin/claude-git -C /Users/dan/Code/tf-version-bump commit -F /private/tmp/claude-501/-Users-dan-Code-tf-version-bump/c2a38027-e2dd-4db5-9bcc-05f7352d90d9/scratchpad/commit-msg.md`

---

### Task 5: Full validation, test cleanup and review

**Files:**
- No planned changes; the cleanup pass may remove tests.

**Interfaces:**
- Consumes: the finished branch.
- Produces: a validated branch ready for the finishing-a-development-branch step.

- [ ] **Step 1: Run the full CI validation**

Run each, in order, from `/Users/dan/Code/tf-version-bump`:

```bash
go mod download && go mod verify
go test -v -race -coverprofile=coverage.out -covermode=atomic ./...
go tool cover -func=coverage.out
golangci-lint version
golangci-lint run --timeout=5m
make actionlint
make shellcheck
make docs-check
go build -o tf-version-bump .
```

Expected: every test passes with no leaked output; the `total:` line of `go tool cover -func` is at least 90.0%; `golangci-lint version` reports 2.12 and `run` reports `0 issues.`; actionlint, shellcheck and docs-check pass; the build succeeds. Delete the built `tf-version-bump` binary and `coverage.out` afterwards with `make clean`.

- [ ] **Step 2: Run the test-cleanup pass in a separate subagent**

Per the global CLAUDE.md, the implementer does not clean up its own tests. Dispatch a fresh subagent with the `test-cleanup` skill over the branch's test changes (`git diff main...HEAD -- '*_test.go'`), noting that the spec expects the three existing permission-preservation tests to be reviewed now that the write never changes permission bits. Apply what it removes only if the suite still passes and coverage stays at or above 90%, then commit with a `test:` message ending in the two attribution lines.

- [ ] **Step 3: Request a code review**

Use superpowers:requesting-code-review (or `/par`) against `main...HEAD`, pointing reviewers at the spec. Address findings, then hand over to superpowers:finishing-a-development-branch. Do not push or comment on issue #151 without Dan's confirmation.
