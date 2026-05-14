package isotty

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func requireExecutable(name string) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("%s is required but was not found in PATH", name)
	}
	return nil
}

func RunCommand(dir string, env []string, debug bool, name string, args ...string) error {
	if debug {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		cmd.Env = env
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return &CommandError{
			Name:   name,
			Args:   append([]string(nil), args...),
			Stdout: stdout.String(),
			Stderr: stderr.String(),
			Err:    err,
		}
	}
	return nil
}

func CaptureCommand(dir string, env []string, name string, args ...string) (string, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", &CommandError{
			Name:   name,
			Args:   append([]string(nil), args...),
			Stdout: stdout.String(),
			Stderr: stderr.String(),
			Err:    err,
		}
	}
	return stdout.String(), nil
}

func ExitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

type CommandError struct {
	Name   string
	Args   []string
	Stdout string
	Stderr string
	Err    error
}

func (e *CommandError) Error() string {
	var b strings.Builder
	b.WriteString("command failed: ")
	b.WriteString(e.Name)
	if len(e.Args) > 0 {
		b.WriteByte(' ')
		b.WriteString(strings.Join(e.Args, " "))
	}
	if e.Err != nil {
		b.WriteString(": ")
		b.WriteString(e.Err.Error())
	}
	if e.Stdout != "" {
		b.WriteString("\nstdout:\n")
		b.WriteString(e.Stdout)
	}
	if e.Stderr != "" {
		b.WriteString("\nstderr:\n")
		b.WriteString(e.Stderr)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (e *CommandError) Unwrap() error {
	return e.Err
}
