package github

import (
	"os/exec"
	"strings"
	"testing"
)

func TestParseRemoteURL(t *testing.T) {
	for _, url := range []string{
		"https://github.com/yacchin1205/isotty.git",
		"https://github.com/yacchin1205/isotty",
		"https://github.com/yacchin1205/isotty/",
		"https://user@github.com/yacchin1205/isotty.git",
		"git@github.com:yacchin1205/isotty.git",
		"git@github.com:yacchin1205/isotty",
		"ssh://git@github.com/yacchin1205/isotty.git",
	} {
		if repo, err := ParseRemoteURL(url); err != nil || repo != "yacchin1205/isotty" {
			t.Fatalf("ParseRemoteURL(%q) = %q, %v", url, repo, err)
		}
	}
	for _, url := range []string{
		"https://gitlab.com/yacchin1205/isotty.git",
		"https://github.com/yacchin1205",
		"https://github.com/yacchin1205/isotty/tree/main",
		"git@github.com:yacchin1205/isotty/extra",
	} {
		if _, err := ParseRemoteURL(url); err == nil {
			t.Fatalf("ParseRemoteURL(%q) should fail", url)
		}
	}
}

func TestResolveRemote(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", "git@github.com:yacchin1205/isotty.git"},
		{"remote", "add", "upstream", "https://example.com/x/y.git"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if repo, err := ResolveRemote(dir, "origin"); err != nil || repo != "yacchin1205/isotty" {
		t.Fatalf("ResolveRemote(origin) = %q, %v", repo, err)
	}
	if _, err := ResolveRemote(dir, "upstream"); err == nil || !strings.Contains(err.Error(), "not a github.com repository") {
		t.Fatalf("ResolveRemote(upstream) error = %v", err)
	}
	if _, err := ResolveRemote(dir, "missing"); err == nil || !strings.Contains(err.Error(), `remote "missing"`) {
		t.Fatalf("ResolveRemote(missing) error = %v", err)
	}
}
