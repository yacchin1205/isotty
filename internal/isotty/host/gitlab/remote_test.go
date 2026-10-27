package gitlab

import (
	"os/exec"
	"strings"
	"testing"
)

func TestParseRemoteURL(t *testing.T) {
	for url, want := range map[string]string{
		"ssh://git@gitlab.example.org:10022/group/project.git":     "gitlab.example.org/group/project",
		"https://gitlab.example.org/group/sub/project.git":         "gitlab.example.org/group/sub/project",
		"https://oauth2:tok@gitlab.example.org:8443/group/project": "gitlab.example.org/group/project",
		"git@gitlab.example.org:group/project.git":                 "gitlab.example.org/group/project",
		"git@gitlab.example.org:group/sub/project/":                "gitlab.example.org/group/sub/project",
		"git@github.com:owner/name.git":                            "github.com/owner/name",
	} {
		if got, err := ParseRemoteURL(url); err != nil || got != want {
			t.Fatalf("ParseRemoteURL(%q) = %q, %v; want %q", url, got, err, want)
		}
	}
	for _, bad := range []string{"https://gitlab.example.org/project.git", "/local/path/repo.git", "gitlab.example.org", "file:///tmp/repo"} {
		if got, err := ParseRemoteURL(bad); err == nil {
			t.Fatalf("ParseRemoteURL(%q) = %q, want error", bad, got)
		}
	}
}

func TestResolveRemote(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", "ssh://git@gitlab.example.org:10022/group/project.git"},
		{"remote", "add", "local", "/tmp/elsewhere.git"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if repo, err := ResolveRemote(dir, "origin"); err != nil || repo != "gitlab.example.org/group/project" {
		t.Fatalf("ResolveRemote(origin) = %q, %v", repo, err)
	}
	if _, err := ResolveRemote(dir, "local"); err == nil || !strings.Contains(err.Error(), "not a git remote URL") {
		t.Fatalf("ResolveRemote(local) error = %v", err)
	}
	if _, err := ResolveRemote(dir, "missing"); err == nil || !strings.Contains(err.Error(), `remote "missing"`) {
		t.Fatalf("ResolveRemote(missing) error = %v", err)
	}
}
