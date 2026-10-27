package github

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseProjectConfig(t *testing.T) {
	cfg, err := Kind.ParseProjectConfig([]byte("repos:\n  yacchin1205/isotty: write\n  Some-Org/lib: read\n"))
	if err != nil {
		t.Fatalf("ParseProjectConfig() error = %v", err)
	}
	if access, ok := cfg.Access("some-org/LIB"); !ok || access != AccessRead {
		t.Fatalf("Access() = %q, %v; want read (case-insensitive)", access, ok)
	}
	if _, err := Kind.ParseProjectConfig([]byte("repos:\n  not-a-repo: write\n")); err == nil || !strings.Contains(err.Error(), "expected owner/name") {
		t.Fatalf("ParseProjectConfig(not-a-repo) error = %v", err)
	}
}

func TestLoadHostConfig(t *testing.T) {
	home := t.TempDir()
	if _, err := LoadHostConfig(home); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing host config error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "github.yaml"), []byte("token: ''\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHostConfig(home); err == nil || !strings.Contains(err.Error(), "does not define token") {
		t.Fatalf("empty token error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "github.yaml"), []byte("token: ghp_x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err := LoadHostConfig(home); err != nil || cfg.Token != "ghp_x" {
		t.Fatalf("LoadHostConfig() = %#v, %v", cfg, err)
	}
}
