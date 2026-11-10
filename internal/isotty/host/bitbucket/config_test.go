package bitbucket

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseProjectConfig(t *testing.T) {
	cfg, err := Kind.ParseProjectConfig([]byte("repos:\n  my-team/app: write\n  Other-Team/lib: read\n"))
	if err != nil {
		t.Fatal(err)
	}
	if access, ok := cfg.Access("other-team/LIB"); !ok || access != AccessRead {
		t.Fatalf("Access(other-team/LIB) = %q, %v", access, ok)
	}
	for _, bad := range []string{"repos:\n  app: write\n", "repos:\n  team/app/extra: read\n", "repos:\n  team/app: owner\n", "repos: {}\n"} {
		if _, err := Kind.ParseProjectConfig([]byte(bad)); err == nil {
			t.Fatalf("parsed %q", bad)
		}
	}
}

func TestLoadHostConfig(t *testing.T) {
	home := t.TempDir()
	if _, err := LoadHostConfig(home); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing file error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "bitbucket.yaml"), []byte("token: ATATT3x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadHostConfig(home)
	if err != nil || cfg.Token != "ATATT3x" {
		t.Fatalf("LoadHostConfig = %+v, %v", cfg, err)
	}
	if err := os.WriteFile(filepath.Join(home, "bitbucket.yaml"), []byte("token: ''\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHostConfig(home); err == nil || !strings.Contains(err.Error(), "does not define token") {
		t.Fatalf("empty token error = %v", err)
	}
}

func TestSaveHostConfig(t *testing.T) {
	home := filepath.Join(t.TempDir(), "isotty")
	if err := SaveHostConfig(home, HostConfig{Token: "ATATT3x"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(HostConfigPath(home))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode = %v, %v", info.Mode(), err)
	}
	if cfg, err := LoadHostConfig(home); err != nil || cfg.Token != "ATATT3x" {
		t.Fatalf("LoadHostConfig = %+v, %v", cfg, err)
	}
}
