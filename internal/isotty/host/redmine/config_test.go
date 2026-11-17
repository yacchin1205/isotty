package redmine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseProjectConfig(t *testing.T) {
	cfg, err := ParseProjectConfig([]byte("projects:\n  - infra\n  - help-desk\n"))
	if err != nil || !cfg.Pinned("infra") || cfg.Pinned("other") {
		t.Fatalf("ParseProjectConfig = %+v, %v", cfg, err)
	}
	for _, bad := range []string{"projects: []\n", "projects:\n  - Infra\n", "projects:\n  - infra\n  - infra\n", "repos:\n  - infra\n"} {
		if _, err := ParseProjectConfig([]byte(bad)); err == nil {
			t.Fatalf("parsed %q", bad)
		}
	}
}

func TestHostConfig(t *testing.T) {
	home := t.TempDir()
	if _, err := LoadHostConfig(home); err == nil || !strings.Contains(err.Error(), "login <url>") {
		t.Fatalf("missing file error = %v", err)
	}
	if err := SaveHostConfig(home, HostConfig{URL: "https://redmine.example.org/", APIKey: "k"}); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(HostConfigPath(home))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode())
	}
	cfg, err := LoadHostConfig(home)
	if err != nil || cfg.URL != "https://redmine.example.org" || cfg.APIKey != "k" {
		t.Fatalf("LoadHostConfig = %+v, %v", cfg, err)
	}
}

func TestSalt(t *testing.T) {
	path := SaltPath(t.TempDir(), "hash")
	first, err := LoadSalt(path)
	if err != nil || len(first) != saltSize {
		t.Fatalf("LoadSalt = %x, %v", first, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("salt file = %v, %v", info, err)
	}
	again, err := LoadSalt(path)
	if err != nil || string(again) != string(first) {
		t.Fatalf("LoadSalt again = %x, %v", again, err)
	}
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSalt(path); err == nil || !strings.Contains(err.Error(), "not a salt") {
		t.Fatalf("short salt = %v", err)
	}
}

func TestPinnedProjects(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	if cfg, err := LoadPinned(project, "hash", home); err != nil || cfg != nil {
		t.Fatalf("LoadPinned without config = %+v, %v", cfg, err)
	}
	cfg := ProjectConfig{}
	cfg.Put("infra")
	cfg.Put("helpdesk")
	cfg.Put("infra")
	if err := Save(project, "hash", home, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadPinned(project, "hash", home)
	if err != nil || loaded == nil || strings.Join(loaded.Projects, ",") != "helpdesk,infra" {
		t.Fatalf("LoadPinned = %+v, %v", loaded, err)
	}
	// The synced file edited in the VM is refused until the host pins it.
	if err := os.WriteFile(ProjectConfigPath(project), []byte("projects:\n  - infra\n  - helpdesk\n  - secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPinned(project, "hash", home); err == nil || !strings.Contains(err.Error(), "differs from the projects pinned") {
		t.Fatalf("edited file error = %v", err)
	}
	if !cfg.Delete("helpdesk") || cfg.Delete("helpdesk") {
		t.Fatal("Delete")
	}
	if err := Save(project, "hash", home, ProjectConfig{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ProjectConfigPath(project)); !os.IsNotExist(err) {
		t.Fatal("empty config left the file")
	}
	if _, err := os.Stat(filepath.Join(home, "projects")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
