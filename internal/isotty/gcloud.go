package isotty

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func createInstance(cfg Config, instanceName string) error {
	labels := fmt.Sprintf("app=isotty,project_hash=%s,backend=vm", cfg.ProjectHash)
	return RunInteractiveCommand("", os.Environ(), "gcloud",
		"compute", "instances", "create", instanceName,
		"--quiet",
		"--project", cfg.GCPProjectID,
		"--zone", cfg.Zone,
		"--machine-type", defaultMachineType,
		"--boot-disk-size", defaultDiskSize,
		"--image-family", defaultImageFamily,
		"--image-project", defaultImageProject,
		"--labels", labels,
	)
}

func gcloudInstanceExists(projectID, zone, instanceName string) (bool, error) {
	_, err := CaptureCommand("", os.Environ(), "gcloud",
		"compute", "instances", "describe", instanceName,
		"--project", projectID,
		"--zone", zone,
		"--format=value(name)",
	)
	if err == nil {
		return true, nil
	}
	if ExitCode(err) == 1 && strings.Contains(err.Error(), "was not found") {
		return false, nil
	}
	return false, err
}

func waitForSSH(state State) error {
	deadline := time.Now().Add(2 * time.Minute)
	var lastErr error
	for time.Now().Before(deadline) {
		_, err := CaptureCommand("", os.Environ(), "gcloud",
			"compute", "ssh", state.InstanceName,
			"--project", state.GCPProjectID,
			"--zone", state.Zone,
			"--command", "true",
		)
		if err == nil {
			return nil
		}
		lastErr = err
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("wait for SSH: %w", lastErr)
}

func bootstrapWorkspace(state State) error {
	command := "sudo mkdir -p /workspace && sudo chown \"$USER\":\"$(id -gn)\" /workspace"
	return RunInteractiveCommand("", os.Environ(), "gcloud",
		"compute", "ssh", state.InstanceName,
		"--project", state.GCPProjectID,
		"--zone", state.Zone,
		"--command", command,
	)
}

func refreshSSHConfig(state State) error {
	if err := os.MkdirAll(filepath.Dir(state.SSHConfigPath), 0o755); err != nil {
		return fmt.Errorf("create ssh config directory: %w", err)
	}
	return RunInteractiveCommand("", os.Environ(), "gcloud",
		"compute", "config-ssh",
		"--quiet",
		"--project", state.GCPProjectID,
		"--ssh-config-file", state.SSHConfigPath,
	)
}
