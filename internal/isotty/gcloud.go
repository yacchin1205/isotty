package isotty

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func createInstance(cfg Config, instanceName string, debug bool) error {
	labels := fmt.Sprintf("app=isotty,project_hash=%s,backend=vm", cfg.ProjectHash)
	return RunCommand("", os.Environ(), debug, "gcloud",
		"compute", "instances", "create", instanceName,
		"--quiet",
		"--project", cfg.GCPProjectID,
		"--zone", cfg.Zone,
		"--machine-type", cfg.MachineType,
		"--boot-disk-size", cfg.BootDiskSize,
		"--image-family", cfg.ImageFamily,
		"--image-project", cfg.ImageProject,
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

func bootstrapWorkspace(state State, debug bool) error {
	commandParts := []string{
		"set -euo pipefail",
		"export DEBIAN_FRONTEND=noninteractive",
		"sudo mkdir -p /workspace",
		"sudo chown \"$USER\":\"$(id -gn)\" /workspace",
	}
	if len(state.AptPackages) > 0 || needsNodeRuntime(state) {
		commandParts = append(commandParts, "sudo apt-get update")
	}
	if len(state.AptPackages) > 0 {
		commandParts = append(commandParts, fmt.Sprintf("sudo apt-get install -y %s", shellJoin(state.AptPackages)))
	}
	if needsNodeRuntime(state) {
		commandParts = append(commandParts, buildNodeInstallScript(state))
	}
	if len(state.Agents) > 0 {
		agentCommand, err := buildAgentInstallScript(state)
		if err != nil {
			return err
		}
		commandParts = append(commandParts, agentCommand)
	}
	command := strings.Join(commandParts, " && ")
	return RunCommand("", os.Environ(), debug, "gcloud",
		"compute", "ssh", state.InstanceName,
		"--project", state.GCPProjectID,
		"--zone", state.Zone,
		"--command", command,
	)
}

func shellJoin(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, strconv.Quote(value))
	}
	return strings.Join(quoted, " ")
}

func refreshSSHConfig(state State, debug bool) error {
	if err := os.MkdirAll(filepath.Dir(state.SSHConfigPath), 0o755); err != nil {
		return fmt.Errorf("create ssh config directory: %w", err)
	}
	return RunCommand("", os.Environ(), debug, "gcloud",
		"compute", "config-ssh",
		"--quiet",
		"--project", state.GCPProjectID,
		"--ssh-config-file", state.SSHConfigPath,
	)
}
