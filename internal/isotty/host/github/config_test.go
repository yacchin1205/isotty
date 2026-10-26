package github

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseProjectConfig(t *testing.T) {
	cfg, err := ParseProjectConfig([]byte("repos:\n  yacchin1205/isotty: write\n  Some-Org/lib: read\n"))
	if err != nil {
		t.Fatalf("ParseProjectConfig() error = %v", err)
	}
	if access, ok := cfg.Access("some-org/LIB"); !ok || access != AccessRead {
		t.Fatalf("Access() = %q, %v; want read (case-insensitive)", access, ok)
	}
	if _, ok := cfg.Access("yacchin1205/other"); ok {
		t.Fatal("Access() found an unpinned repository")
	}
	if got := strings.Join(cfg.SortedRepos(), ","); got != "Some-Org/lib,yacchin1205/isotty" {
		t.Fatalf("SortedRepos() = %q", got)
	}

	for _, bad := range []string{
		"repos: {}\n",
		"repos:\n  not-a-repo: write\n",
		"repos:\n  a/b: admin\n",
		"repos:\n  a/b: write\nextra: 1\n",
		"repos:\n  a/b: write\n  A/B: read\n",
	} {
		if _, err := ParseProjectConfig([]byte(bad)); err == nil {
			t.Fatalf("ParseProjectConfig(%q) should fail", bad)
		}
	}
}

func TestProjectConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, enabled, err := LoadProjectConfig(dir); err != nil || enabled {
		t.Fatalf("LoadProjectConfig() = enabled %v, err %v; want disabled", enabled, err)
	}
	want := ProjectConfig{Repos: map[string]Access{"a/b": AccessWrite, "c/d": AccessRead}}
	if err := SaveProjectConfig(dir, want); err != nil {
		t.Fatalf("SaveProjectConfig() error = %v", err)
	}
	got, enabled, err := LoadProjectConfig(dir)
	if err != nil || !enabled || got.Repos["a/b"] != AccessWrite || got.Repos["c/d"] != AccessRead || len(got.Repos) != 2 {
		t.Fatalf("LoadProjectConfig() = %#v, %v, %v", got, enabled, err)
	}
	if err := RemoveProjectConfig(dir); err != nil {
		t.Fatalf("RemoveProjectConfig() error = %v", err)
	}
	if err := RemoveProjectConfig(dir); err != nil {
		t.Fatalf("second RemoveProjectConfig() error = %v", err)
	}
}

func TestPutAndDeleteIgnoreCase(t *testing.T) {
	var cfg ProjectConfig
	cfg.Put("Some-Org/Lib", AccessRead)
	cfg.Put("some-org/lib", AccessWrite)
	if len(cfg.Repos) != 1 || cfg.Repos["some-org/lib"] != AccessWrite {
		t.Fatalf("Put() left %#v; want one entry at write", cfg.Repos)
	}
	if !cfg.Delete("SOME-ORG/LIB") || len(cfg.Repos) != 0 {
		t.Fatalf("Delete() left %#v", cfg.Repos)
	}
	if cfg.Delete("some-org/lib") {
		t.Fatal("Delete() reported an unpinned repository as removed")
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
