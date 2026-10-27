package forge

import (
	"errors"
	"strings"
	"testing"

	"github.com/yazawa/isotty/internal/isotty/host/pin"
)

var testKind = Kind{Name: "test", Title: "Test", ValidateRepo: func(repo string) error {
	if !strings.Contains(repo, "/") {
		return errors.New("expected a/b")
	}
	return nil
}}

func TestAccessCovers(t *testing.T) {
	for _, c := range []struct {
		have, need Access
		want       bool
	}{
		{AccessRead, AccessRead, true}, {AccessRead, AccessComment, false}, {AccessComment, AccessComment, true},
		{AccessComment, AccessWrite, false}, {AccessWrite, AccessComment, true}, {AccessWrite, AccessWrite, true},
	} {
		if got := c.have.Covers(c.need); got != c.want {
			t.Fatalf("%s.Covers(%s) = %v", c.have, c.need, got)
		}
	}
	if err := ValidateAccess("admin"); err == nil {
		t.Fatal("ValidateAccess(admin) should fail")
	}
}

func TestParseProjectConfig(t *testing.T) {
	cfg, err := testKind.ParseProjectConfig([]byte("repos:\n  a/b: write\n  Some-Org/lib: read\n"))
	if err != nil {
		t.Fatalf("ParseProjectConfig() error = %v", err)
	}
	if access, ok := cfg.Access("some-org/LIB"); !ok || access != AccessRead {
		t.Fatalf("Access() = %q, %v; want read (case-insensitive)", access, ok)
	}
	if _, ok := cfg.Access("a/other"); ok {
		t.Fatal("Access() found an unpinned repository")
	}
	if got := strings.Join(cfg.SortedRepos(), ","); got != "Some-Org/lib,a/b" {
		t.Fatalf("SortedRepos() = %q", got)
	}
	for _, bad := range []string{
		"repos: {}\n",
		"repos:\n  not-a-repo: write\n",
		"repos:\n  a/b: admin\n",
		"repos:\n  a/b: write\nextra: 1\n",
		"repos:\n  a/b: write\n  A/B: read\n",
	} {
		if _, err := testKind.ParseProjectConfig([]byte(bad)); err == nil {
			t.Fatalf("ParseProjectConfig(%q) should fail", bad)
		}
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

func TestSaveAndLoadPinned(t *testing.T) {
	dir, home := t.TempDir(), t.TempDir()
	if cfg, err := testKind.LoadPinned(dir, "abc", home); err != nil || cfg != nil {
		t.Fatalf("LoadPinned() without config = %v, %v", cfg, err)
	}
	want := ProjectConfig{Repos: map[string]Access{"a/b": AccessWrite, "c/d": AccessRead}}
	if err := testKind.Save(dir, "abc", home, want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := testKind.LoadPinned(dir, "abc", home)
	if err != nil || got.Repos["a/b"] != AccessWrite || got.Repos["c/d"] != AccessRead || len(got.Repos) != 2 {
		t.Fatalf("LoadPinned() = %#v, %v", got, err)
	}
	pinned, err := testKind.LoadPin(home, "abc")
	if err != nil || len(pinned.Repos) != 2 {
		t.Fatalf("LoadPin() = %#v, %v", pinned, err)
	}

	widened := ProjectConfig{Repos: map[string]Access{"a/b": AccessWrite, "c/d": AccessWrite}}
	if err := testKind.SaveProjectConfig(dir, widened); err != nil {
		t.Fatal(err)
	}
	if _, err := testKind.LoadPinned(dir, "abc", home); err == nil || !strings.Contains(err.Error(), "c/d:write") || !strings.Contains(err.Error(), "c/d:read") {
		t.Fatalf("widened error = %v", err)
	}
	if err := pin.Remove(home, "abc", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := testKind.LoadPinned(dir, "abc", home); err == nil || !strings.Contains(err.Error(), "not pinned") {
		t.Fatalf("unpinned error = %v", err)
	}

	if err := testKind.Save(dir, "abc", home, ProjectConfig{}); err != nil {
		t.Fatalf("Save(empty) error = %v", err)
	}
	if cfg, err := testKind.LoadPinned(dir, "abc", home); err != nil || cfg != nil {
		t.Fatalf("LoadPinned() after clear = %v, %v", cfg, err)
	}
}
