package gitlab

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadHostConfig(t *testing.T) {
	home := t.TempDir()
	path := HostConfigPath(home, "abc")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHostConfig(home, "abc"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing host config error = %v", err)
	}
	for _, bad := range []string{
		"instances: []\n",
		"instances:\n  - url: https://gitlab.example.org\n",
		"instances:\n  - url: gitlab.example.org\n    token: t\n",
		"instances:\n  - url: https://gitlab.example.org/?x=1\n    token: t\n",
		"instances:\n  - url: https://gitlab.example.org\n    token: t\n    extra: 1\n",
	} {
		if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadHostConfig(home, "abc"); err == nil {
			t.Fatalf("LoadHostConfig(%q) should fail", bad)
		}
	}
	if err := os.WriteFile(path, []byte("instances:\n  - url: https://gitlab.example.org:8443/\n    token: glpat-x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadHostConfig(home, "abc")
	if err != nil {
		t.Fatalf("LoadHostConfig() error = %v", err)
	}
	if cfg.Path() != path {
		t.Fatalf("Path() = %q, want %q", cfg.Path(), path)
	}
	instance, ok := cfg.Find("GitLab.example.org")
	if !ok || instance.Token != "glpat-x" {
		t.Fatalf("Find() = %#v, %v", instance, ok)
	}
	if _, ok := cfg.Find("other.example.org"); ok {
		t.Fatal("Find() matched an unlisted host")
	}
	if got := instance.apiURL("group/sub/project.name"); got != "https://gitlab.example.org:8443/api/v4/projects/group%2Fsub%2Fproject.name" {
		t.Fatalf("apiURL() = %q", got)
	}
}

func TestValidateRepo(t *testing.T) {
	for _, good := range []string{"gitlab.example.org/group/project", "gitlab.example.org/g/sub/sub2/p", "GitLab.Example.org/G/P.git-ish"} {
		if err := ValidateRepo(good); err != nil {
			t.Fatalf("ValidateRepo(%q) = %v", good, err)
		}
	}
	for _, bad := range []string{"group/project", "gitlab.example.org/project", "gitlab.example.org//p", "gitlab.example.org:10022/g/p", "https://gitlab.example.org/g/p", ""} {
		if err := ValidateRepo(bad); err == nil {
			t.Fatalf("ValidateRepo(%q) should fail", bad)
		}
	}
	host, path, err := Split("gitlab.example.org/g/sub/p")
	if err != nil || host != "gitlab.example.org" || path != "g/sub/p" {
		t.Fatalf("Split() = %q, %q, %v", host, path, err)
	}
}
