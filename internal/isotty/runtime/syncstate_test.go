package runtimecfg

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// syncClient runs isotty-sync as `up` installs it, reading state.
func runSyncClient(t *testing.T, state string, args ...string) (string, error) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "isotty-sync")
	client := strings.ReplaceAll(renderSyncClient(syncClient), SyncStatePath, state)
	if err := os.WriteFile(script, []byte(client), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("python3", append([]string{"-I", script}, args...)...).CombinedOutput()
	return string(out), err
}

func TestSyncClient(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	state := filepath.Join(t.TempDir(), "sync.json")
	write := func(content string, age time.Duration) {
		if err := os.WriteFile(state, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		if err := os.Chtimes(state, at, at); err != nil {
			t.Fatal(err)
		}
	}

	if out, err := runSyncClient(t, state, "status"); err != nil || out != "no sync state has been written yet\n" {
		t.Fatalf("status without a state = %q, %v", out, err)
	}
	if out, err := runSyncClient(t, state, "hook"); err != nil || out != "" {
		t.Fatalf("hook without a state = %q, %v", out, err)
	}

	write(`{"status":"staging-beta","done":false,"received_files":3,"expected_files":10}`, 0)
	if out, err := runSyncClient(t, state, "status"); err != nil || out != "syncing: staging-beta (3/10 files staged)\n" {
		t.Fatalf("status while staging = %q, %v", out, err)
	}
	if out, err := runSyncClient(t, state, "hook"); err != nil || !strings.Contains(out, `"additionalContext": "isotty: /workspace is not fully synced from the host yet (syncing: staging-beta (3/10 files staged))`) {
		t.Fatalf("hook while staging = %q, %v", out, err)
	}

	write(`{"status":"scanning","done":false}`, 2*time.Minute)
	if out, err := runSyncClient(t, state, "status"); err != nil || !strings.Contains(out, "syncing: scanning; not updated for 1") || !strings.Contains(out, "the host is not attached") {
		t.Fatalf("status of an old state = %q, %v", out, err)
	}

	write(`{"status":"watching","done":true}`, 2*time.Minute)
	if out, err := runSyncClient(t, state, "wait"); err != nil || out != "done\n" {
		t.Fatalf("wait when done = %q, %v", out, err)
	}
	if out, err := runSyncClient(t, state, "hook"); err != nil || out != "" {
		t.Fatalf("hook when done = %q, %v", out, err)
	}
}

func TestSyncClientCommand(t *testing.T) {
	command := SyncClientCommand([]string{"claude", "codex"})
	for _, want := range []string{"sudo chmod 755 " + SyncClientPath, ".claude/skills/isotty-sync/SKILL.md", ".codex/skills/isotty-sync/SKILL.md", `hooks "$HOME"/.claude/settings.json /tmp/isotty-sync-hooks.json '/usr/local/bin/isotty-sync hook'`, `hooks "$HOME"/.codex/hooks.json /tmp/isotty-sync-hooks.json '/usr/local/bin/isotty-sync hook'`} {
		if !strings.Contains(command, want) {
			t.Fatalf("SyncClientCommand lacks %q:\n%s", want, command)
		}
	}
	if command := SyncClientCommand(nil); strings.Contains(command, "SKILL.md") || strings.Contains(command, "hooks") {
		t.Fatalf("SyncClientCommand without agents:\n%s", command)
	}
	if rendered := renderSyncClient(syncClient); strings.Contains(rendered, "__ISOTTY_") || !strings.Contains(rendered, "STALE = int('90')") {
		t.Fatalf("rendered client not substituted")
	}
}
