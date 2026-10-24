package runtimecfg

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentAuthRoundTrip(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	archive := []byte("tar bytes")

	if _, ok, err := LoadAgentAuth(home, project, "claude"); err != nil || ok {
		t.Fatalf("LoadAgentAuth() before save = %v, %v", ok, err)
	}
	if err := SaveAgentAuth(home, project, "claude", archive); err != nil {
		t.Fatalf("SaveAgentAuth() error = %v", err)
	}
	for _, path := range []string{filepath.Join(home, agentAuthKeyFile), AgentAuthArchivePath(project, "claude")} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o, want 600", path, info.Mode().Perm())
		}
	}
	sealed, err := os.ReadFile(AgentAuthArchivePath(project, "claude"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, archive) {
		t.Fatal("archive is stored in plaintext")
	}

	loaded, ok, err := LoadAgentAuth(home, project, "claude")
	if err != nil || !ok || !bytes.Equal(loaded, archive) {
		t.Fatalf("LoadAgentAuth() = %q, %v, %v", loaded, ok, err)
	}

	if err := SaveAgentAuth(home, project, "claude", nil); err != nil {
		t.Fatalf("SaveAgentAuth(empty) error = %v", err)
	}
	if _, ok, err := LoadAgentAuth(home, project, "claude"); err != nil || ok {
		t.Fatalf("LoadAgentAuth() after empty save = %v, %v", ok, err)
	}
}

func TestAgentAuthNeedsItsKey(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	if err := SaveAgentAuth(home, project, "codex", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(home, agentAuthKeyFile)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadAgentAuth(home, project, "codex"); err == nil || !strings.Contains(err.Error(), "cannot be decrypted") {
		t.Fatalf("LoadAgentAuth() without key error = %v", err)
	}
}

func TestRemoveAgentAuth(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	for _, agent := range []string{"claude", "codex"} {
		if err := SaveAgentAuth(home, project, agent, []byte(agent)); err != nil {
			t.Fatal(err)
		}
	}
	if err := RemoveAgentAuth(project, []string{"claude"}); err != nil {
		t.Fatalf("RemoveAgentAuth() error = %v", err)
	}
	if _, ok, _ := LoadAgentAuth(home, project, "claude"); ok {
		t.Fatal("claude state should be gone")
	}
	if _, ok, _ := LoadAgentAuth(home, project, "codex"); !ok {
		t.Fatal("codex state should remain")
	}
	if err := RemoveAgentAuth(project, []string{"claude"}); err != nil {
		t.Fatalf("second RemoveAgentAuth() error = %v", err)
	}
}

func TestCaptureAgentAuthCommand(t *testing.T) {
	command := CaptureAgentAuthCommand("claude")
	for _, want := range []string{`".claude" ".claude.json"`, "tar -czf -", "base64 -w0", `[ $# -eq 0 ] ||`} {
		if !strings.Contains(command, want) {
			t.Fatalf("command missing %q: %s", want, command)
		}
	}
	if !strings.Contains(command, `tar -czf - --exclude=".claude/cache" "$@"`) {
		t.Fatalf("claude command missing excludes: %s", command)
	}
	codex := CaptureAgentAuthCommand("codex")
	if !strings.Contains(codex, `tar -czf - --exclude=".codex/.tmp" --exclude=".codex/cache" --exclude=".codex/plugins/cache" "$@"`) {
		t.Fatalf("codex command missing excludes: %s", codex)
	}
	if RestoreAgentAuthCommand() != "tar -xzf - -C ~" {
		t.Fatalf("RestoreAgentAuthCommand() = %q", RestoreAgentAuthCommand())
	}
}
