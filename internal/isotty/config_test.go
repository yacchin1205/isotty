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
	if err := validateSyncMode(developmentSyncMode); err != nil {
		t.Fatalf("development mode should be valid: %v", err)
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
