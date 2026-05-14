package isotty

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAddAndRemoveRuntimeAptPackages(t *testing.T) {
	projectDir := t.TempDir()

	if err := AddRuntimeAptPackages(projectDir, []string{"ripgrep", "jq"}); err != nil {
		t.Fatalf("AddRuntimeAptPackages() error = %v", err)
	}
	if err := AddRuntimeAptPackages(projectDir, []string{"jq", "fd-find"}); err != nil {
		t.Fatalf("AddRuntimeAptPackages() second error = %v", err)
	}

	packages, err := ListRuntimeAptPackages(projectDir)
	if err != nil {
		t.Fatalf("ListRuntimeAptPackages() error = %v", err)
	}
	if len(packages) != 3 {
		t.Fatalf("len(packages) = %d, want 3", len(packages))
	}
	if packages[0] != "ripgrep" || packages[1] != "jq" || packages[2] != "fd-find" {
		t.Fatalf("packages = %v, want [ripgrep jq fd-find]", packages)
	}

	if err := RemoveRuntimeAptPackages(projectDir, []string{"jq", "missing"}); err != nil {
		t.Fatalf("RemoveRuntimeAptPackages() error = %v", err)
	}
	packages, err = ListRuntimeAptPackages(projectDir)
	if err != nil {
		t.Fatalf("ListRuntimeAptPackages() after remove error = %v", err)
	}
	if len(packages) != 2 || packages[0] != "ripgrep" || packages[1] != "fd-find" {
		t.Fatalf("packages = %v, want [ripgrep fd-find]", packages)
	}
}

func TestRemoveRuntimeAptPackagesDeletesFileWhenEmpty(t *testing.T) {
	projectDir := t.TempDir()
	if err := AddRuntimeAptPackages(projectDir, []string{"ripgrep"}); err != nil {
		t.Fatalf("AddRuntimeAptPackages() error = %v", err)
	}

	if err := RemoveRuntimeAptPackages(projectDir, []string{"ripgrep"}); err != nil {
		t.Fatalf("RemoveRuntimeAptPackages() error = %v", err)
	}

	if _, err := os.Stat(aptPackagesPath(projectDir)); !os.IsNotExist(err) {
		t.Fatalf("apt config should be removed, stat err = %v", err)
	}
}

func TestSetRuntimeNodeVersion(t *testing.T) {
	projectDir := t.TempDir()

	if err := SetRuntimeNodeVersion(projectDir, "22"); err != nil {
		t.Fatalf("SetRuntimeNodeVersion() error = %v", err)
	}

	version, err := RuntimeNodeVersion(projectDir)
	if err != nil {
		t.Fatalf("RuntimeNodeVersion() error = %v", err)
	}
	if version != "22" {
		t.Fatalf("version = %q, want 22", version)
	}
}

func TestSetRuntimeNodeVersionRejectsInvalidValue(t *testing.T) {
	projectDir := t.TempDir()

	if err := SetRuntimeNodeVersion(projectDir, "22;echo pwned"); err == nil {
		t.Fatal("SetRuntimeNodeVersion() should reject invalid value")
	}
}

func TestAddAndRemoveRuntimeAgents(t *testing.T) {
	projectDir := t.TempDir()

	if err := AddRuntimeAgents(projectDir, []string{"codex"}); err != nil {
		t.Fatalf("AddRuntimeAgents() error = %v", err)
	}
	if err := AddRuntimeAgents(projectDir, []string{"claude", "codex"}); err != nil {
		t.Fatalf("AddRuntimeAgents() second error = %v", err)
	}

	agents, err := ListRuntimeAgents(projectDir)
	if err != nil {
		t.Fatalf("ListRuntimeAgents() error = %v", err)
	}
	if len(agents) != 2 || agents[0] != "claude" || agents[1] != "codex" {
		t.Fatalf("agents = %v, want [claude codex]", agents)
	}

	if err := RemoveRuntimeAgents(projectDir, []string{"codex"}); err != nil {
		t.Fatalf("RemoveRuntimeAgents() error = %v", err)
	}
	agents, err = ListRuntimeAgents(projectDir)
	if err != nil {
		t.Fatalf("ListRuntimeAgents() after remove error = %v", err)
	}
	if len(agents) != 1 || agents[0] != "claude" {
		t.Fatalf("agents = %v, want [claude]", agents)
	}
}

func TestRemoveRuntimeAgentsDeletesFileWhenEmpty(t *testing.T) {
	projectDir := t.TempDir()
	if err := AddRuntimeAgents(projectDir, []string{"codex"}); err != nil {
		t.Fatalf("AddRuntimeAgents() error = %v", err)
	}

	if err := RemoveRuntimeAgents(projectDir, []string{"codex"}); err != nil {
		t.Fatalf("RemoveRuntimeAgents() error = %v", err)
	}

	if _, err := os.Stat(agentConfigPath(projectDir)); !os.IsNotExist(err) {
		t.Fatalf("agent config should be removed, stat err = %v", err)
	}
}

func TestRuntimeConfigFilesAreProjectLocal(t *testing.T) {
	projectDir := t.TempDir()
	if err := SetRuntimeNodeVersion(projectDir, "22"); err != nil {
		t.Fatalf("SetRuntimeNodeVersion() error = %v", err)
	}
	if err := AddRuntimeAptPackages(projectDir, []string{"ripgrep"}); err != nil {
		t.Fatalf("AddRuntimeAptPackages() error = %v", err)
	}
	if err := AddRuntimeAgents(projectDir, []string{"codex"}); err != nil {
		t.Fatalf("AddRuntimeAgents() error = %v", err)
	}

	for _, path := range []string{
		nodeVersionPath(projectDir),
		aptPackagesPath(projectDir),
		agentConfigPath(projectDir),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", filepath.Base(path), err)
		}
	}
}
