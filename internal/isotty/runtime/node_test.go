package runtimecfg

import "testing"

func TestSetNodeVersion(t *testing.T) {
	projectDir := t.TempDir()

	if err := SetNodeVersion(projectDir, "22"); err != nil {
		t.Fatalf("SetNodeVersion() error = %v", err)
	}

	version, err := NodeVersion(projectDir)
	if err != nil {
		t.Fatalf("NodeVersion() error = %v", err)
	}
	if version != "22" {
		t.Fatalf("version = %q, want 22", version)
	}
}

func TestSetNodeVersionRejectsInvalidValue(t *testing.T) {
	projectDir := t.TempDir()

	if err := SetNodeVersion(projectDir, "22;echo pwned"); err == nil {
		t.Fatal("SetNodeVersion() should reject invalid value")
	}
}
