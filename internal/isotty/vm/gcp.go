package vmcfg

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultGCPMachineType  = "e2-standard-4"
	defaultGCPDiskSize     = "50GB"
	defaultGCPImageProject = "ubuntu-os-cloud"
	defaultGCPImageFamily  = "ubuntu-2404-lts-amd64"
)

type GCPConfig struct {
	MachineType  *string `json:"machine_type"`
	BootDiskSize *string `json:"boot_disk_size"`
	ImageFamily  *string `json:"image_family"`
	ImageProject *string `json:"image_project"`
}

type GCPInstanceSpec struct {
	ProjectID    string
	Zone         string
	ProjectHash  string
	MachineType  string
	BootDiskSize string
	ImageFamily  string
	ImageProject string
}

type GCPConnection struct {
	ProjectID     string
	Zone          string
	InstanceName  string
	SSHConfigPath string
}

func normalizeGCPVMConfig(cfg VMConfig) (VMConfig, error) {
	gcp := cfg.GCP
	if gcp.MachineType == nil {
		gcp.MachineType = stringPointer(defaultGCPMachineType)
	} else if *gcp.MachineType == "" {
		return VMConfig{}, fmt.Errorf("vm config contains empty gcp.machine_type")
	}
	if gcp.BootDiskSize == nil {
		gcp.BootDiskSize = stringPointer(defaultGCPDiskSize)
	} else if *gcp.BootDiskSize == "" {
		return VMConfig{}, fmt.Errorf("vm config contains empty gcp.boot_disk_size")
	}
	if gcp.ImageFamily == nil {
		gcp.ImageFamily = stringPointer(defaultGCPImageFamily)
	} else if *gcp.ImageFamily == "" {
		return VMConfig{}, fmt.Errorf("vm config contains empty gcp.image_family")
	}
	if gcp.ImageProject == nil {
		gcp.ImageProject = stringPointer(defaultGCPImageProject)
	} else if *gcp.ImageProject == "" {
		return VMConfig{}, fmt.Errorf("vm config contains empty gcp.image_project")
	}
	cfg.GCP = gcp
	return cfg, nil
}

func SetGCPVMConfig(projectPath string, cfg GCPConfig) error {
	return saveVMConfig(projectPath, VMConfig{
		Provider: "gcp",
		GCP:      cfg,
	})
}

func RunGCP(projectPath string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("vm gcp requires a subcommand: show or set")
	}

	switch args[0] {
	case "show":
		return runGCPShow(projectPath, args[1:], stdout, stderr)
	case "set":
		return runGCPSet(projectPath, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown vm gcp subcommand %q", args[0])
	}
}

var runGcloud = func(debug bool, args ...string) error {
	if debug {
		cmd := exec.Command("gcloud", args...)
		cmd.Env = os.Environ()
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	var stdout strings.Builder
	var stderr strings.Builder

	cmd := exec.Command("gcloud", args...)
	cmd.Env = os.Environ()
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf(
			"command failed: gcloud %s: %w\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "),
			err,
			strings.TrimRight(stdout.String(), "\n"),
			strings.TrimRight(stderr.String(), "\n"),
		)
	}
	return nil
}

var captureGcloud = func(args ...string) (string, error) {
	var stdout strings.Builder
	var stderr strings.Builder

	cmd := exec.Command("gcloud", args...)
	cmd.Env = os.Environ()
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf(
			"command failed: gcloud %s: %w\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "),
			err,
			strings.TrimRight(stdout.String(), "\n"),
			strings.TrimRight(stderr.String(), "\n"),
		)
	}
	return stdout.String(), nil
}

var gcloudExitCode = func(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func CreateGCPInstance(spec GCPInstanceSpec, instanceName string, debug bool) error {
	labels := fmt.Sprintf("app=isotty,project_hash=%s,backend=vm", spec.ProjectHash)
	return runGcloud(debug,
		"compute", "instances", "create", instanceName,
		"--quiet",
		"--project", spec.ProjectID,
		"--zone", spec.Zone,
		"--machine-type", spec.MachineType,
		"--boot-disk-size", spec.BootDiskSize,
		"--image-family", spec.ImageFamily,
		"--image-project", spec.ImageProject,
		"--labels", labels,
	)
}

func GCPInstanceExists(projectID, zone, instanceName string) (bool, error) {
	_, err := captureGcloud(
		"compute", "instances", "describe", instanceName,
		"--project", projectID,
		"--zone", zone,
		"--format=value(name)",
	)
	if err == nil {
		return true, nil
	}
	if gcloudExitCode(err) == 1 && strings.Contains(err.Error(), "was not found") {
		return false, nil
	}
	return false, err
}

func WaitForGCPSSH(conn GCPConnection) error {
	deadline := time.Now().Add(2 * time.Minute)
	var lastErr error
	for time.Now().Before(deadline) {
		_, err := captureGcloud(
			"compute", "ssh", conn.InstanceName,
			"--project", conn.ProjectID,
			"--zone", conn.Zone,
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

func RunGCPRemoteCommand(conn GCPConnection, command string, debug bool) error {
	return runGcloud(debug,
		"compute", "ssh", conn.InstanceName,
		"--project", conn.ProjectID,
		"--zone", conn.Zone,
		"--command", command,
	)
}

func RunGCPInteractiveSSH(args ...string) error {
	cmd := exec.Command("gcloud", args...)
	cmd.Env = os.Environ()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func CaptureGCPRemoteCommand(conn GCPConnection, command string) (string, error) {
	return captureGcloud(
		"compute", "ssh", conn.InstanceName,
		"--project", conn.ProjectID,
		"--zone", conn.Zone,
		"--command", command,
	)
}

func GCPCommandExitCode(err error) int {
	return gcloudExitCode(err)
}

func DeleteGCPInstance(conn GCPConnection, debug bool) error {
	return runGcloud(debug,
		"compute", "instances", "delete", conn.InstanceName,
		"--quiet",
		"--project", conn.ProjectID,
		"--zone", conn.Zone,
	)
}

func RefreshGCPSSHConfig(conn GCPConnection, debug bool) error {
	if err := os.MkdirAll(filepath.Dir(conn.SSHConfigPath), 0o755); err != nil {
		return fmt.Errorf("create ssh config directory: %w", err)
	}
	return runGcloud(debug,
		"compute", "config-ssh",
		"--quiet",
		"--project", conn.ProjectID,
		"--ssh-config-file", conn.SSHConfigPath,
	)
}

func runGCPShow(projectPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("vm gcp show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("vm gcp show does not accept arguments")
	}
	vmConfig, err := Load(projectPath)
	if err != nil {
		return err
	}
	cfg := vmConfig.GCP
	fmt.Fprintf(stdout, "machine_type: %s\n", *cfg.MachineType)
	fmt.Fprintf(stdout, "boot_disk_size: %s\n", *cfg.BootDiskSize)
	fmt.Fprintf(stdout, "image_family: %s\n", *cfg.ImageFamily)
	fmt.Fprintf(stdout, "image_project: %s\n", *cfg.ImageProject)
	return nil
}

func runGCPSet(projectPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("vm gcp set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	machineType := fs.String("machine-type", "", "GCP machine type")
	bootDiskSize := fs.String("boot-disk-size", "", "GCP boot disk size")
	imageFamily := fs.String("image-family", "", "GCP image family")
	imageProject := fs.String("image-project", "", "GCP image project")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("vm gcp set does not accept positional arguments")
	}

	updates := GCPConfig{}
	if flagProvided(args, "--machine-type") {
		updates.MachineType = machineType
	}
	if flagProvided(args, "--boot-disk-size") {
		updates.BootDiskSize = bootDiskSize
	}
	if flagProvided(args, "--image-family") {
		updates.ImageFamily = imageFamily
	}
	if flagProvided(args, "--image-project") {
		updates.ImageProject = imageProject
	}
	if updates.MachineType == nil && updates.BootDiskSize == nil && updates.ImageFamily == nil && updates.ImageProject == nil {
		return errors.New("vm gcp set requires at least one flag")
	}

	vmConfig, err := Load(projectPath)
	if err != nil {
		return err
	}
	current := vmConfig.GCP
	if updates.MachineType != nil {
		current.MachineType = updates.MachineType
	}
	if updates.BootDiskSize != nil {
		current.BootDiskSize = updates.BootDiskSize
	}
	if updates.ImageFamily != nil {
		current.ImageFamily = updates.ImageFamily
	}
	if updates.ImageProject != nil {
		current.ImageProject = updates.ImageProject
	}
	vmConfig.GCP = current
	if err := saveVMConfig(projectPath, vmConfig); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Updated GCP VM config")
	return nil
}

func flagProvided(args []string, name string) bool {
	for i := 0; i < len(args); i++ {
		if args[i] == name {
			return true
		}
		if len(args[i]) > len(name) && strings.HasPrefix(args[i], name+"=") {
			return true
		}
	}
	return false
}

func stringPointer(value string) *string { return &value }
