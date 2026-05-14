package isotty

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHashProjectPathIsStable(t *testing.T) {
	path := filepath.Clean("/tmp/project")
	first := hashProjectPath(path)
	second := hashProjectPath(path)

	if first != second {
		t.Fatalf("hash should be stable: %q != %q", first, second)
	}
	if len(first) != 12 {
		t.Fatalf("hash length = %d, want 12", len(first))
	}
}

func TestValidateSyncMode(t *testing.T) {
	if err := validateSyncMode(defaultSyncMode); err != nil {
		t.Fatalf("default mode should be valid: %v", err)
	}
	if err := validateSyncMode(oneWaySafeSyncMode); err != nil {
		t.Fatalf("one-way-safe mode should be valid: %v", err)
	}
	if err := validateSyncMode("two-way-resolved"); err == nil {
		t.Fatal("unexpected validation success for unsupported mode")
	}
}

func TestLoadAptPackages(t *testing.T) {
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".isotty")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	content := "# comment\nripgrep\n\njq\nripgrep\n"
	if err := os.WriteFile(filepath.Join(configDir, "apt.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write apt.txt: %v", err)
	}

	packages, err := loadAptPackages(projectDir)
	if err != nil {
		t.Fatalf("loadAptPackages() error = %v", err)
	}
	if len(packages) != 2 {
		t.Fatalf("len(packages) = %d, want 2", len(packages))
	}
	if packages[0] != "ripgrep" || packages[1] != "jq" {
		t.Fatalf("packages = %v, want [ripgrep jq]", packages)
	}
}

func TestLoadNodeVersion(t *testing.T) {
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".isotty")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "node.txt"), []byte("22\n"), 0o644); err != nil {
		t.Fatalf("write node.txt: %v", err)
	}

	version, err := loadNodeVersion(projectDir)
	if err != nil {
		t.Fatalf("loadNodeVersion() error = %v", err)
	}
	if version != "22" {
		t.Fatalf("version = %q, want 22", version)
	}
}

func TestLoadNodeVersionFailsOnEmptyFile(t *testing.T) {
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".isotty")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "node.txt"), []byte("\n"), 0o644); err != nil {
		t.Fatalf("write node.txt: %v", err)
	}

	_, err := loadNodeVersion(projectDir)
	if err == nil {
		t.Fatal("loadNodeVersion() should fail on empty node.txt")
	}
}

func TestLoadNodeVersionFailsOnInvalidValue(t *testing.T) {
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".isotty")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "node.txt"), []byte("22$(touch /tmp/pwned)\n"), 0o644); err != nil {
		t.Fatalf("write node.txt: %v", err)
	}

	_, err := loadNodeVersion(projectDir)
	if err == nil {
		t.Fatal("loadNodeVersion() should fail on invalid node.txt")
	}
}

func TestLoadAgents(t *testing.T) {
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".isotty")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	content := "agents:\n  codex: {}\n  claude: {}\n"
	if err := os.WriteFile(filepath.Join(configDir, "agent.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write agent.yaml: %v", err)
	}

	agents, err := loadAgents(projectDir)
	if err != nil {
		t.Fatalf("loadAgents() error = %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("len(agents) = %d, want 2", len(agents))
	}
	if agents[0] != "claude" || agents[1] != "codex" {
		t.Fatalf("agents = %v, want [claude codex]", agents)
	}
}

func TestLoadAgentsFailsOnUnsupportedAgent(t *testing.T) {
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".isotty")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	content := "agents:\n  unknown: {}\n"
	if err := os.WriteFile(filepath.Join(configDir, "agent.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write agent.yaml: %v", err)
	}

	_, err := loadAgents(projectDir)
	if err == nil {
		t.Fatal("loadAgents() should fail on unsupported agent")
	}
}

func TestLoadVMConfig(t *testing.T) {
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".isotty")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	content := "provider: gcp\ngcp:\n  machine_type: e2-standard-8\n  boot_disk_size: 200GB\n  image_family: ubuntu-2404-lts-amd64\n  image_project: ubuntu-os-cloud\n"
	if err := os.WriteFile(filepath.Join(configDir, "vm.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write vm.yaml: %v", err)
	}

	cfg, err := loadVMConfig(projectDir)
	if err != nil {
		t.Fatalf("loadVMConfig() error = %v", err)
	}
	if cfg.MachineType == nil || *cfg.MachineType != "e2-standard-8" {
		t.Fatalf("MachineType = %v, want e2-standard-8", cfg.MachineType)
	}
	if cfg.BootDiskSize == nil || *cfg.BootDiskSize != "200GB" {
		t.Fatalf("BootDiskSize = %v, want 200GB", cfg.BootDiskSize)
	}
	if cfg.ImageFamily == nil || *cfg.ImageFamily != "ubuntu-2404-lts-amd64" {
		t.Fatalf("ImageFamily = %v", cfg.ImageFamily)
	}
	if cfg.ImageProject == nil || *cfg.ImageProject != "ubuntu-os-cloud" {
		t.Fatalf("ImageProject = %v", cfg.ImageProject)
	}
}

func TestLoadVMConfigFailsOnEmptyMachineType(t *testing.T) {
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".isotty")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	content := "gcp:\n  machine_type: \"\"\n"
	if err := os.WriteFile(filepath.Join(configDir, "vm.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write vm.yaml: %v", err)
	}

	_, err := loadVMConfig(projectDir)
	if err == nil {
		t.Fatal("loadVMConfig() should fail on empty gcp.machine_type")
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

	_, err := loadVMConfig(projectDir)
	if err == nil {
		t.Fatal("loadVMConfig() should fail on empty provider")
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

	_, err := loadVMConfig(projectDir)
	if err == nil {
		t.Fatal("loadVMConfig() should fail on unsupported provider")
	}
}
