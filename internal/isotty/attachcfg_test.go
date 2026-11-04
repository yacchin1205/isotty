package isotty

import (
	"os"
	"strings"
	"testing"
)

func TestLoadAttachConfig(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadAttachConfig(dir)
	if err != nil || !cfg.UseTmux() {
		t.Fatalf("without a file: cfg %#v, err %v; want tmux", cfg, err)
	}
	if err := os.WriteFile(AttachConfigPath(dir), []byte("tmux: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadAttachConfig(dir)
	if err != nil || cfg.UseTmux() {
		t.Fatalf("tmux: false: cfg %#v, err %v; want a plain shell", cfg, err)
	}
	if err := os.WriteFile(AttachConfigPath(dir), []byte("tmux: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err = LoadAttachConfig(dir); err != nil || !cfg.UseTmux() {
		t.Fatalf("tmux: true: cfg %#v, err %v", cfg, err)
	}
	if err := os.WriteFile(AttachConfigPath(dir), []byte("tmux: false\nshell: zsh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAttachConfig(dir); err == nil || !strings.Contains(err.Error(), "shell") {
		t.Fatalf("unknown key error = %v", err)
	}
}
