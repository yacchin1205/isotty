package isotty

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCommandSuccess(t *testing.T) {
	err := RunCommand("", os.Environ(), false, "sh", "-c", "printf ok")
	if err != nil {
		t.Fatalf("RunCommand() error = %v", err)
	}
}

func TestRunCommandFailureReturnsCommandError(t *testing.T) {
	err := RunCommand("", os.Environ(), false, "sh", "-c", "printf out; printf err >&2; exit 7")
	if err == nil {
		t.Fatal("RunCommand() error = nil, want CommandError")
	}

	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("error type = %T, want *CommandError", err)
	}
	if commandErr.Name != "sh" {
		t.Fatalf("Name = %q", commandErr.Name)
	}
	if commandErr.Stdout != "out" {
		t.Fatalf("Stdout = %q", commandErr.Stdout)
	}
	if commandErr.Stderr != "err" {
		t.Fatalf("Stderr = %q", commandErr.Stderr)
	}
	if ExitCode(commandErr.Err) != 7 {
		t.Fatalf("ExitCode = %d, want 7", ExitCode(commandErr.Err))
	}
}

func TestCaptureCommandSuccess(t *testing.T) {
	output, err := CaptureCommand("", os.Environ(), "sh", "-c", "printf captured")
	if err != nil {
		t.Fatalf("CaptureCommand() error = %v", err)
	}
	if output != "captured" {
		t.Fatalf("output = %q", output)
	}
}

func TestCaptureCommandFailureReturnsCommandError(t *testing.T) {
	_, err := CaptureCommand("", os.Environ(), "sh", "-c", "printf bad >&2; exit 9")
	if err == nil {
		t.Fatal("CaptureCommand() error = nil, want CommandError")
	}

	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("error type = %T, want *CommandError", err)
	}
	if commandErr.Stderr != "bad" {
		t.Fatalf("Stderr = %q", commandErr.Stderr)
	}
	if ExitCode(commandErr.Err) != 9 {
		t.Fatalf("ExitCode = %d, want 9", ExitCode(commandErr.Err))
	}
}

func TestCommandErrorError(t *testing.T) {
	err := &CommandError{
		Name:   "demo",
		Args:   []string{"a", "b"},
		Stdout: "out",
		Stderr: "err",
		Err:    errors.New("boom"),
	}
	message := err.Error()
	if !strings.Contains(message, "command failed: demo a b: boom") {
		t.Fatalf("Error() = %q", message)
	}
	if !strings.Contains(message, "\nstdout:\nout") {
		t.Fatalf("Error() = %q", message)
	}
	if !strings.Contains(message, "\nstderr:\nerr") {
		t.Fatalf("Error() = %q", message)
	}
}

func TestRequireExecutable(t *testing.T) {
	tempDir := t.TempDir()
	scriptPath := filepath.Join(tempDir, "demo-bin")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", tempDir+string(os.PathListSeparator)+originalPath)

	if err := requireExecutable("demo-bin"); err != nil {
		t.Fatalf("requireExecutable() error = %v", err)
	}
	if err := requireExecutable("missing-demo-bin"); err == nil {
		t.Fatal("requireExecutable() error = nil, want missing executable error")
	}
}
