package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

type exitCall struct {
	code int
}

func bashPath(t *testing.T) string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is required to run shell-script tests")
	}
	return bash
}

func stubExit(t *testing.T) (restore func(), code *int) {
	t.Helper()
	hookMu.Lock()
	original := exitFunc
	exitCode := -1
	exitFunc = func(c int) {
		exitCode = c
		panic(exitCall{code: c})
	}
	return func() {
		exitFunc = original
		hookMu.Unlock()
	}, &exitCode
}

func withFlagArgs(t *testing.T, args []string, fn func()) {
	t.Helper()
	origArgs := os.Args
	origFlagSet := flag.CommandLine
	flag.CommandLine = flag.NewFlagSet(args[0], flag.ContinueOnError)
	flag.CommandLine.SetOutput(io.Discard)
	os.Args = args
	defer func() {
		flag.CommandLine = origFlagSet
		os.Args = origArgs
	}()
	fn()
}

type commandResult struct {
	stdout      string
	diagnostics string
	// warnings holds what the command wrote straight to stderr rather than through the logger,
	// which is where every 'Warning:' goes. Capturing it keeps test output free of application
	// output and lets a test assert the warning a counted skip reports.
	warnings string
	exitCode int
}

//nolint:unparam // The adapter preserves the production call shape used by focused tests.
func updateModuleVersion(filename, moduleSource, version string, fromVersions, ignoreVersions, ignorePatterns []string, forceAdd, dryRun, verbose bool, outputFormat string) (bool, error) {
	updated, _, _, err := updateModuleVersionWithCount(filename, moduleSource, version, fromVersions, ignoreVersions, ignorePatterns, forceAdd, dryRun, verbose, outputFormat)
	return updated, err
}

func updateProviderVersion(filename, providerName, version string, dryRun bool) (bool, error) {
	updated, _, _, err := updateProviderVersionWithCount(filename, providerName, version, dryRun)
	return updated, err
}

func updateTerraformVersion(filename, version string, dryRun bool) (bool, error) {
	updated, _, err := updateTerraformVersionWithCount(filename, version, dryRun)
	return updated, err
}

// testOutputMu serialises the tests that redirect the process streams or the standard logger.
var testOutputMu sync.Mutex

// redirectStream points stream at a pipe until collect is called, which restores the stream and
// returns everything written to it. The caller must hold testOutputMu.
func redirectStream(t *testing.T, stream **os.File) (collect func() string) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	original := *stream
	*stream = writer

	var output bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, reader)
		close(done)
	}()

	collected := false
	collect = func() string {
		if collected {
			return output.String()
		}
		collected = true
		*stream = original
		if err := writer.Close(); err != nil {
			t.Errorf("close pipe writer: %v", err)
		}
		<-done
		if err := reader.Close(); err != nil {
			t.Errorf("close pipe reader: %v", err)
		}
		return output.String()
	}
	t.Cleanup(func() { collect() })
	return collect
}

// redirectLog sends the standard logger to a buffer, without flags or prefix, until collect is
// called, which restores the logger and returns what it wrote. The caller must hold testOutputMu.
func redirectLog(t *testing.T) (collect func() string) {
	t.Helper()
	var output bytes.Buffer
	writer, flags, prefix := log.Writer(), log.Flags(), log.Prefix()
	log.SetOutput(&output)
	log.SetFlags(0)
	log.SetPrefix("")

	restored := false
	collect = func() string {
		if !restored {
			restored = true
			log.SetOutput(writer)
			log.SetFlags(flags)
			log.SetPrefix(prefix)
		}
		return output.String()
	}
	t.Cleanup(func() { collect() })
	return collect
}

// runMainCommand runs main with args, capturing its streams and the exit code it asked for.
func runMainCommand(t *testing.T, args []string) commandResult {
	t.Helper()
	testOutputMu.Lock()
	defer testOutputMu.Unlock()

	restoreExit, code := stubExit(t)
	defer restoreExit()
	diagnostics := redirectLog(t)
	stdout := redirectStream(t, &os.Stdout)
	warnings := redirectStream(t, &os.Stderr)

	withFlagArgs(t, args, func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				if _, ok := recovered.(exitCall); !ok {
					panic(recovered)
				}
			}
		}()
		main()
	})

	return commandResult{
		stdout:      stdout(),
		diagnostics: diagnostics(),
		warnings:    warnings(),
		exitCode:    *code,
	}
}

// captureRunnerOutput runs a mode runner, capturing its stdout and logged diagnostics.
func captureRunnerOutput(t *testing.T, run func() error) (stdout, diagnostic string, runnerErr error) {
	t.Helper()
	testOutputMu.Lock()
	defer testOutputMu.Unlock()

	collectStdout := redirectStream(t, &os.Stdout)
	collectLog := redirectLog(t)
	runnerErr = run()
	return collectStdout(), collectLog(), runnerErr
}

func requireExitCall(t *testing.T, fn func()) {
	t.Helper()

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		fn()
	}()

	call, ok := recovered.(exitCall)
	if !ok {
		t.Fatalf("recovered value = %#v, want exitCall", recovered)
	}
	if call.code != 1 {
		t.Fatalf("exit code = %d, want 1", call.code)
	}
}

func writeTestFile(t *testing.T, dir, name, contents string) string {
	t.Helper()
	filename := filepath.Join(dir, name)
	if err := os.WriteFile(filename, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", filename, err)
	}
	return filename
}

func readTestFile(t *testing.T, filename string) string {
	t.Helper()
	contents, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("read %s: %v", filename, err)
	}
	return string(contents)
}

func captureStream(t *testing.T, stream **os.File, fn func()) string {
	t.Helper()
	testOutputMu.Lock()
	defer testOutputMu.Unlock()

	collect := redirectStream(t, stream)
	fn()
	return collect()
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	return captureStream(t, &os.Stdout, fn)
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	return captureStream(t, &os.Stderr, fn)
}

func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	testOutputMu.Lock()
	defer testOutputMu.Unlock()

	collect := redirectLog(t)
	fn()
	return collect()
}

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

// failReportPublish makes publishing a prepared report fail at the point it is moved onto its
// destination, which is the last step and the one that can leave a previous report in place.
func failReportPublish(t *testing.T, failure error) {
	t.Helper()
	hookMu.Lock()
	original := renameReportFile
	renameReportFile = func(string, string) error { return failure }
	hookMu.Unlock()
	t.Cleanup(func() {
		hookMu.Lock()
		renameReportFile = original
		hookMu.Unlock()
	})
}

// failReportRemoval makes removing a report left at the destination fail, which is the second
// thing to go wrong in a run that had already failed.
func failReportRemoval(t *testing.T, failure error) {
	t.Helper()
	hookMu.Lock()
	original := removeReportFile
	removeReportFile = func(string) error { return failure }
	hookMu.Unlock()
	t.Cleanup(func() {
		hookMu.Lock()
		removeReportFile = original
		hookMu.Unlock()
	})
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
		// A write that keeps its backup marks the file untrusted for the rest of the run.
		untrustedMu.Lock()
		untrustedFiles = nil
		untrustedMu.Unlock()
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
