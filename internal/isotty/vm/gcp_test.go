package vmcfg

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadVMConfigNormalizesGCPDefaults(t *testing.T) {
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".isotty")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	content := "provider: gcp\ngcp:\n  machine_type: e2-standard-8\n  boot_disk_size: 200GB\n  image_family: ubuntu-2404-lts-amd64\n  image_project: ubuntu-os-cloud\n"
	if err := os.WriteFile(filepath.Join(configDir, "vm.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write vm.yaml: %v", err)
	}

	cfg, err := Load(projectDir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.GCP.MachineType == nil || *cfg.GCP.MachineType != "e2-standard-8" {
		t.Fatalf("MachineType = %v, want e2-standard-8", cfg.GCP.MachineType)
	}
	if cfg.GCP.BootDiskSize == nil || *cfg.GCP.BootDiskSize != "200GB" {
		t.Fatalf("BootDiskSize = %v, want 200GB", cfg.GCP.BootDiskSize)
	}
	if cfg.GCP.ImageFamily == nil || *cfg.GCP.ImageFamily != "ubuntu-2404-lts-amd64" {
		t.Fatalf("ImageFamily = %v", cfg.GCP.ImageFamily)
	}
	if cfg.GCP.ImageProject == nil || *cfg.GCP.ImageProject != "ubuntu-os-cloud" {
		t.Fatalf("ImageProject = %v", cfg.GCP.ImageProject)
	}
}

func TestLoadVMConfigFailsOnEmptyMachineType(t *testing.T) {
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".isotty")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	content := "provider: gcp\ngcp:\n  machine_type: \"\"\n"
	if err := os.WriteFile(filepath.Join(configDir, "vm.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write vm.yaml: %v", err)
	}

	if _, err := Load(projectDir); err == nil {
		t.Fatal("Load() should fail on empty gcp.machine_type")
	}
}

func TestLoadVMConfigFailsOnMissingProvider(t *testing.T) {
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".isotty")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	content := "gcp:\n  machine_type: e2-standard-8\n"
	if err := os.WriteFile(filepath.Join(configDir, "vm.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write vm.yaml: %v", err)
	}

	if _, err := Load(projectDir); err == nil {
		t.Fatal("Load() should fail on missing provider")
	}
}

func TestLoadVMConfigFailsOnEmptyProvider(t *testing.T) {
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".isotty")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	content := "provider: \"\"\n"
	if err := os.WriteFile(filepath.Join(configDir, "vm.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write vm.yaml: %v", err)
	}

	if _, err := Load(projectDir); err == nil {
		t.Fatal("Load() should fail on empty provider")
	}
}

func TestLoadVMConfigFailsOnUnsupportedProvider(t *testing.T) {
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".isotty")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	content := "provider: aws\n"
	if err := os.WriteFile(filepath.Join(configDir, "vm.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write vm.yaml: %v", err)
	}

	if _, err := Load(projectDir); err == nil {
		t.Fatal("Load() should fail on unsupported provider")
	}
}

func TestLoadVMConfigPreservesExplicitValues(t *testing.T) {
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".isotty")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	content := "provider: gcp\ngcp:\n  machine_type: e2-standard-8\n"
	if err := os.WriteFile(filepath.Join(configDir, "vm.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write vm.yaml: %v", err)
	}

	cfg, err := Load(projectDir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Provider != "gcp" {
		t.Fatalf("cfg.Provider = %q, want gcp", cfg.Provider)
	}
	if cfg.GCP.MachineType == nil || *cfg.GCP.MachineType != "e2-standard-8" {
		t.Fatalf("cfg.GCP.MachineType = %v", cfg.GCP.MachineType)
	}
}

func TestLoadVMConfigDefaultsWhenFileMissing(t *testing.T) {
	projectDir := t.TempDir()

	cfg, err := Load(projectDir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Provider != "gcp" {
		t.Fatalf("cfg.Provider = %q, want gcp", cfg.Provider)
	}
	if cfg.GCP.MachineType == nil || *cfg.GCP.MachineType != defaultGCPMachineType {
		t.Fatalf("cfg.GCP.MachineType = %v, want %q", cfg.GCP.MachineType, defaultGCPMachineType)
	}
	if cfg.GCP.BootDiskSize == nil || *cfg.GCP.BootDiskSize != defaultGCPDiskSize {
		t.Fatalf("cfg.GCP.BootDiskSize = %v, want %q", cfg.GCP.BootDiskSize, defaultGCPDiskSize)
	}
	if cfg.GCP.ImageFamily == nil || *cfg.GCP.ImageFamily != defaultGCPImageFamily {
		t.Fatalf("cfg.GCP.ImageFamily = %v, want %q", cfg.GCP.ImageFamily, defaultGCPImageFamily)
	}
	if cfg.GCP.ImageProject == nil || *cfg.GCP.ImageProject != defaultGCPImageProject {
		t.Fatalf("cfg.GCP.ImageProject = %v, want %q", cfg.GCP.ImageProject, defaultGCPImageProject)
	}
}

func TestRunGCPShow(t *testing.T) {
	projectDir := t.TempDir()
	cfg := GCPConfig{
		MachineType:  stringPointer("e2-standard-8"),
		BootDiskSize: stringPointer("200GB"),
		ImageFamily:  stringPointer("ubuntu-2404-lts-amd64"),
		ImageProject: stringPointer("ubuntu-os-cloud"),
	}
	if err := SetGCPVMConfig(projectDir, cfg); err != nil {
		t.Fatalf("SetGCPVMConfig() error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := RunGCP(projectDir, []string{"show"}, &stdout, &stderr); err != nil {
		t.Fatalf("RunGCP(show) error = %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "machine_type: e2-standard-8") {
		t.Fatalf("output = %q, want machine_type", output)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunGCPSet(t *testing.T) {
	projectDir := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := RunGCP(projectDir, []string{"set", "--machine-type", "e2-standard-8", "--boot-disk-size", "200GB"}, &stdout, &stderr); err != nil {
		t.Fatalf("RunGCP(set) error = %v", err)
	}

	vmConfig, err := Load(projectDir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if *vmConfig.GCP.MachineType != "e2-standard-8" {
		t.Fatalf("MachineType = %q, want e2-standard-8", *vmConfig.GCP.MachineType)
	}
	if *vmConfig.GCP.BootDiskSize != "200GB" {
		t.Fatalf("BootDiskSize = %q, want 200GB", *vmConfig.GCP.BootDiskSize)
	}
}

func TestCreateInstanceBuildsExpectedCommand(t *testing.T) {
	var gotDebug bool
	var gotArgs []string
	originalRunGcloud := runGcloud
	runGcloud = func(debug bool, args ...string) error {
		gotDebug = debug
		gotArgs = append([]string(nil), args...)
		return nil
	}
	t.Cleanup(func() { runGcloud = originalRunGcloud })

	err := CreateGCPInstance(GCPInstanceSpec{
		ProjectID:    "demo-project",
		Zone:         "us-central1-f",
		ProjectHash:  "abc123",
		MachineType:  "e2-standard-4",
		BootDiskSize: "50GB",
		ImageFamily:  "ubuntu-2404-lts-amd64",
		ImageProject: "ubuntu-os-cloud",
	}, "isotty-abc123", true)
	if err != nil {
		t.Fatalf("CreateGCPInstance() error = %v", err)
	}
	if !gotDebug {
		t.Fatalf("got debug=%v", gotDebug)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "compute instances create isotty-abc123") {
		t.Fatalf("args = %v", gotArgs)
	}
	if !strings.Contains(joined, "--machine-type e2-standard-4") {
		t.Fatalf("args = %v", gotArgs)
	}
	if !strings.Contains(joined, "--labels app=isotty,project_hash=abc123,backend=vm") {
		t.Fatalf("args = %v", gotArgs)
	}
}

func TestInstanceExistsTreatsNotFoundAsFalse(t *testing.T) {
	originalCaptureGcloud := captureGcloud
	originalGcloudExitCode := gcloudExitCode
	captureGcloud = func(args ...string) (string, error) {
		return "", errors.New("instance was not found")
	}
	gcloudExitCode = func(err error) int { return 1 }
	t.Cleanup(func() {
		captureGcloud = originalCaptureGcloud
		gcloudExitCode = originalGcloudExitCode
	})

	exists, err := GCPInstanceExists("demo-project", "us-central1-f", "isotty-abc123")
	if err != nil {
		t.Fatalf("GCPInstanceExists() error = %v", err)
	}
	if exists {
		t.Fatal("GCPInstanceExists() = true, want false")
	}
}
