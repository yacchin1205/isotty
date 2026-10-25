package runtimecfg

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHomeFilesArchive(t *testing.T) {
	home := t.TempDir()
	if _, ok, err := HomeFilesArchive(home); err != nil || ok {
		t.Fatalf("HomeFilesArchive() without the directory = %v, %v", ok, err)
	}
	root := HomeFilesDir(home)
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := HomeFilesArchive(home); err != nil || ok {
		t.Fatalf("HomeFilesArchive() with an empty directory = %v, %v", ok, err)
	}
	if err := os.Mkdir(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CODE_POLICY.md"), []byte("policy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "CLAUDE.md"), []byte("read it"), 0o600); err != nil {
		t.Fatal(err)
	}

	archive, ok, err := HomeFilesArchive(home)
	if err != nil || !ok {
		t.Fatalf("HomeFilesArchive() = %v, %v", ok, err)
	}
	got := map[string]string{}
	modes := map[string]int64{}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		got[header.Name] = string(body)
		modes[header.Name] = header.Mode
	}
	want := map[string]string{".claude/": "", ".claude/CLAUDE.md": "read it", "CODE_POLICY.md": "policy"}
	if len(got) != len(want) {
		t.Fatalf("archive entries = %v, want %v", got, want)
	}
	for name, body := range want {
		if got[name] != body {
			t.Fatalf("entry %s = %q, want %q", name, got[name], body)
		}
	}
	if modes[".claude/CLAUDE.md"] != 0o600 || modes["CODE_POLICY.md"] != 0o644 {
		t.Fatalf("modes = %v", modes)
	}
}

func TestHomeFilesArchiveRejectsSymlinks(t *testing.T) {
	home := t.TempDir()
	root := HomeFilesDir(home)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "CODE_POLICY.md")
	if err := os.WriteFile(target, []byte("policy"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "CODE_POLICY.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	_, _, err := HomeFilesArchive(home)
	if err == nil || !strings.Contains(err.Error(), link) {
		t.Fatalf("HomeFilesArchive() error = %v, want one naming %s", err, link)
	}

	linkedHome := t.TempDir()
	if err := os.Symlink(root, HomeFilesDir(linkedHome)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := HomeFilesArchive(linkedHome); err == nil {
		t.Fatal("HomeFilesArchive() accepted a linked directory")
	}
}
