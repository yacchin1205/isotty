package bitbucket

import (
	"os/exec"
	"strings"
	"testing"
)

func TestParseRemoteURL(t *testing.T) {
	for url, want := range map[string]string{
		"https://bitbucket.org/my-team/app.git":              "my-team/app",
		"https://someone@bitbucket.org/my-team/app.git":      "my-team/app",
		"https://x-token-auth@bitbucket.org/my-team/app":     "my-team/app",
		"git@bitbucket.org:my-team/app.git":                  "my-team/app",
		"ssh://git@bitbucket.org/my-team/app.git":            "my-team/app",
		"ssh://git@altssh.bitbucket.org:443/my-team/app.git": "my-team/app",
		"https://bitbucket.org/My-Team/App/":                 "My-Team/App",
	} {
		got, err := ParseRemoteURL(url)
		if err != nil || got != want {
			t.Fatalf("ParseRemoteURL(%s) = %q, %v; want %q", url, got, err, want)
		}
	}
	for _, url := range []string{
		"https://github.com/me/project.git",
		"https://bitbucket.example.org/scm/proj/app.git",
		"https://bitbucket.org/my-team",
		"https://bitbucket.org/my-team/app/src/main",
		"/srv/git/app.git",
	} {
		if got, err := ParseRemoteURL(url); err == nil {
			t.Fatalf("ParseRemoteURL(%s) = %q, want an error", url, got)
		}
	}
}

func TestResolveRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", dir},
		{"-C", dir, "remote", "add", "origin", "https://someone@bitbucket.org/my-team/app.git"},
		{"-C", dir, "remote", "add", "elsewhere", "https://github.com/me/project.git"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if repo, err := ResolveRemote(dir, "origin"); err != nil || repo != "my-team/app" {
		t.Fatalf("ResolveRemote(origin) = %q, %v", repo, err)
	}
	if _, err := ResolveRemote(dir, "elsewhere"); err == nil || !strings.Contains(err.Error(), "not a bitbucket.org repository URL") {
		t.Fatalf("ResolveRemote(elsewhere) error = %v", err)
	}
	if _, err := ResolveRemote(dir, "missing"); err == nil || !strings.Contains(err.Error(), `remote "missing"`) {
		t.Fatalf("ResolveRemote(missing) error = %v", err)
	}
}
