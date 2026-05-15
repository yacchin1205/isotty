package isotty

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var defaultIgnores = []string{
	".env",
	".env.*",
	".npmrc",
	".pypirc",
	"*.pem",
	"*.key",
	".ssh/",
	".aws/",
	".gcloud/",
	".azure/",
	".kube/",
	".docker/",
	"node_modules/",
	".venv/",
}

func recreateMutagenSession(state State, debug bool) error {
	if err := terminateMutagenSession(state); err != nil {
		return err
	}

	if err := runMutagenCommand(state.MutagenEnv(), debug, buildMutagenCreateArgs(state)...); err != nil {
		return err
	}
	return flushMutagenSession(state)
}

func terminateMutagenSession(state State) error {
	args := []string{"sync", "terminate", "--label-selector", state.MutagenLabelSelector()}
	output, err := captureMutagenCommand(state.MutagenEnv(), args...)
	if err == nil {
		_ = output
		return nil
	}

	lower := strings.ToLower(err.Error())
	if mutagenExitCode(err) == 1 && (strings.Contains(lower, "not found") || strings.Contains(lower, "did not match any sessions")) {
		return nil
	}
	return err
}

func flushMutagenSession(state State) error {
	args := []string{"sync", "flush", state.SessionName}
	_, err := captureMutagenCommand(state.MutagenEnv(), args...)
	return err
}

func describeMutagenSession(state State) (string, error) {
	return captureMutagenCommand(state.MutagenEnv(), "sync", "list", "-l", state.SessionName)
}

func buildMutagenCreateArgs(state State) []string {
	args := []string{
		"sync", "create",
		"--name", state.SessionName,
		"--no-global-configuration",
		"--mode", state.SyncMode,
	}
	for _, label := range state.MutagenLabels() {
		args = append(args, "--label", label)
	}
	for _, ignore := range defaultIgnores {
		args = append(args, "--ignore", ignore)
	}
	args = append(args, state.ProjectPath, state.RemoteEndpoint())
	return args
}

func ensureSSHWrappers(state State) error {
	sshPath, err := requirePath("ssh")
	if err != nil {
		return err
	}
	scpPath, err := requirePath("scp")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(state.SSHWrapperDir, 0o755); err != nil {
		return fmt.Errorf("create ssh wrapper directory: %w", err)
	}

	sshScript := fmt.Sprintf("#!/bin/sh\nexec %s -F %s \"$@\"\n", shellQuote(sshPath), shellQuote(state.SSHConfigPath))
	if err := os.WriteFile(filepath.Join(state.SSHWrapperDir, "ssh"), []byte(sshScript), 0o755); err != nil {
		return fmt.Errorf("write ssh wrapper: %w", err)
	}

	scpScript := fmt.Sprintf("#!/bin/sh\nexec %s -F %s \"$@\"\n", shellQuote(scpPath), shellQuote(state.SSHConfigPath))
	if err := os.WriteFile(filepath.Join(state.SSHWrapperDir, "scp"), []byte(scpScript), 0o755); err != nil {
		return fmt.Errorf("write scp wrapper: %w", err)
	}

	if err := os.MkdirAll(state.MutagenDataDirectory, 0o755); err != nil {
		return fmt.Errorf("create mutagen data directory: %w", err)
	}
	return nil
}

func requirePath(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s is required but was not found in PATH", name)
	}
	return path, nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func runMutagenCommand(env []string, debug bool, args ...string) error {
	if debug {
		cmd := exec.Command("mutagen", args...)
		cmd.Env = env
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd := exec.Command("mutagen", args...)
	cmd.Env = env
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf(
			"command failed: mutagen %s: %w\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "),
			err,
			strings.TrimRight(stdout.String(), "\n"),
			strings.TrimRight(stderr.String(), "\n"),
		)
	}
	return nil
}

func captureMutagenCommand(env []string, args ...string) (string, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd := exec.Command("mutagen", args...)
	cmd.Env = env
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf(
			"command failed: mutagen %s: %w\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "),
			err,
			strings.TrimRight(stdout.String(), "\n"),
			strings.TrimRight(stderr.String(), "\n"),
		)
	}
	return stdout.String(), nil
}

func mutagenExitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}
