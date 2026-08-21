package main

import (
	"bytes"
	"flag"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type exitCall struct {
	code int
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
	exitCode    int
}

//nolint:unparam // The adapter preserves the production call shape used by focused tests.
func updateModuleVersion(filename, moduleSource, version string, fromVersions, ignoreVersions, ignorePatterns []string, forceAdd, dryRun, verbose bool, outputFormat string) (bool, error) {
	updated, _, err := updateModuleVersionWithCount(filename, moduleSource, version, fromVersions, ignoreVersions, ignorePatterns, forceAdd, dryRun, verbose, outputFormat)
	return updated, err
}

func updateProviderVersion(filename, providerName, version string, dryRun bool) (bool, error) {
	updated, _, err := updateProviderVersionWithCount(filename, providerName, version, dryRun)
	return updated, err
}

var testOutputMu sync.Mutex

func startPipeDrain(reader *os.File) (output *bytes.Buffer, done <-chan struct{}) {
	output = new(bytes.Buffer)
	doneChannel := make(chan struct{})
	go func() {
		_, _ = io.Copy(output, reader)
		close(doneChannel)
	}()
	done = doneChannel
	return output, done
}

func runMainCommand(t *testing.T, args []string) commandResult {
	t.Helper()
	testOutputMu.Lock()
	defer testOutputMu.Unlock()

	restoreExit, code := stubExit(t)
	defer restoreExit()

	var diagnostics bytes.Buffer
	originalLogWriter := log.Writer()
	originalLogFlags := log.Flags()
	originalLogPrefix := log.Prefix()
	log.SetOutput(&diagnostics)
	log.SetFlags(0)
	log.SetPrefix("")
	defer func() {
		log.SetOutput(originalLogWriter)
		log.SetFlags(originalLogFlags)
		log.SetPrefix(originalLogPrefix)
	}()

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stdout pipe: %v", err)
	}
	defer func() {
		if err := stdoutReader.Close(); err != nil {
			t.Errorf("failed to close stdout reader: %v", err)
		}
	}()
	defer func() { _ = stdoutWriter.Close() }()

	originalStdout := os.Stdout
	os.Stdout = stdoutWriter
	defer func() { os.Stdout = originalStdout }()

	var stdout bytes.Buffer
	stdoutDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&stdout, stdoutReader)
		close(stdoutDone)
	}()

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

	if err := stdoutWriter.Close(); err != nil {
		t.Fatalf("failed to close stdout writer: %v", err)
	}
	<-stdoutDone

	return commandResult{
		stdout:      stdout.String(),
		diagnostics: diagnostics.String(),
		exitCode:    *code,
	}
}

func captureRunnerOutput(t *testing.T, run func() error) (stdout, diagnostic string, runnerErr error) {
	t.Helper()
	testOutputMu.Lock()
	defer testOutputMu.Unlock()

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stdout pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = stdoutWriter.Close()
		_ = stdoutReader.Close()
	})
	defer func() {
		if err := stdoutReader.Close(); err != nil {
			t.Errorf("failed to close stdout reader: %v", err)
		}
	}()

	originalStdout := os.Stdout
	restore := func() { os.Stdout = originalStdout }
	t.Cleanup(restore)
	defer restore()
	os.Stdout = stdoutWriter

	originalLogOutput := log.Writer()
	originalLogFlags := log.Flags()
	var logOutput bytes.Buffer
	log.SetOutput(&logOutput)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(originalLogOutput)
		log.SetFlags(originalLogFlags)
	}()

	stdoutBuffer, stdoutDone := startPipeDrain(stdoutReader)
	runnerErr = run()

	if err := stdoutWriter.Close(); err != nil {
		t.Fatalf("failed to close stdout writer: %v", err)
	}
	<-stdoutDone

	return stdoutBuffer.String(), logOutput.String(), runnerErr
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

func captureStream(t *testing.T, stream **os.File, name string, fn func()) string {
	t.Helper()
	testOutputMu.Lock()
	defer testOutputMu.Unlock()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create %s pipe: %v", name, err)
	}
	original := *stream
	restore := func() { *stream = original }
	t.Cleanup(func() {
		restore()
		_ = writer.Close()
		_ = reader.Close()
	})
	defer restore()
	*stream = writer

	output, outputDone := startPipeDrain(reader)
	fn()
	restore()
	if err := writer.Close(); err != nil {
		t.Fatalf("close %s writer: %v", name, err)
	}
	<-outputDone
	return output.String()
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	return captureStream(t, &os.Stdout, "stdout", fn)
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	return captureStream(t, &os.Stderr, "stderr", fn)
}

func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	testOutputMu.Lock()
	defer testOutputMu.Unlock()

	var output bytes.Buffer
	originalWriter := log.Writer()
	originalFlags := log.Flags()
	originalPrefix := log.Prefix()
	restore := func() {
		log.SetOutput(originalWriter)
		log.SetFlags(originalFlags)
		log.SetPrefix(originalPrefix)
	}
	t.Cleanup(restore)
	defer restore()
	log.SetOutput(&output)
	log.SetFlags(0)
	log.SetPrefix("")
	fn()
	return output.String()
}
