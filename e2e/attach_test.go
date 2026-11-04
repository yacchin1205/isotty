//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAttachSession takes a project through up, attach in tmux, a second
// terminal with --no-host, stop, up again with tmux turned off on the host,
// attach in a plain shell, and down.
func TestAttachSession(t *testing.T) {
	h := newHarness(t)

	out := h.isotty("up", "--no-attach")
	if !strings.Contains(out, "IsoTTY environment is ready.") || !strings.Contains(out, "Preparing tmux") {
		t.Fatalf("up output:\n%s", out)
	}
	h.waitFor("the workspace to sync", 60*time.Second, func() bool {
		_, err := h.vm("test -f /workspace/hello.txt")
		return err == nil
	})

	// The first attach creates the tmux session and lands in the workspace.
	first := h.attach()
	h.waitFor("the tmux session", 60*time.Second, func() bool {
		out, err := h.vm("tmux list-sessions -F '#{session_name} #{session_attached}'")
		return err == nil && strings.Contains(out, "isotty 1")
	})
	first.typeLine("touch /workspace/typed-in-tmux")
	h.waitFor("typed input to reach the tmux pane", 30*time.Second, func() bool {
		_, err := h.vm("test -f /workspace/typed-in-tmux")
		return err == nil
	})
	if out := h.mustVM("tmux display-message -p -t isotty '#{pane_current_path}'"); strings.TrimSpace(out) != "/workspace" {
		t.Fatalf("pane path = %q", out)
	}
	// The gateway attach puts BROWSER into the session environment.
	if out := h.mustVM("tmux show-environment -t isotty BROWSER"); strings.TrimSpace(out) != "BROWSER=/usr/local/bin/isotty-open" {
		t.Fatalf("session environment: %q", out)
	}

	// A second terminal joins as a grouped session with its own current
	// window and leaves no session behind when it detaches.
	second := h.attach("--no-host")
	var grouped string
	h.waitFor("the grouped session", 60*time.Second, func() bool {
		out, err := h.vm("tmux list-sessions -F '#{session_name} #{session_group} #{session_attached} #{destroy-unattached}'")
		if err != nil {
			return false
		}
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 4 && fields[0] != "isotty" && fields[1] == "isotty" && fields[2] == "1" && fields[3] == "on" {
				grouped = fields[0]
				return true
			}
		}
		return false
	})
	h.mustVM("tmux detach-client -s " + grouped)
	if err := second.wait(t, 30*time.Second); err != nil {
		t.Fatalf("second attach: %v", err)
	}
	if out := h.mustVM("tmux list-sessions -F '#{session_name} #{session_attached}'"); strings.TrimSpace(out) != "isotty 1" {
		t.Fatalf("sessions after the second terminal left:\n%s", out)
	}

	// Detaching ends the attach; the session and what runs in it stay.
	h.mustVM("tmux detach-client -s isotty")
	if err := first.wait(t, 30*time.Second); err != nil {
		t.Fatalf("first attach: %v", err)
	}
	if out := h.mustVM("tmux list-sessions -F '#{session_name} #{session_attached}'"); strings.TrimSpace(out) != "isotty 0" {
		t.Fatalf("sessions after detach:\n%s", out)
	}
	events := h.mustVM("sudo cat /var/log/isotty/events.jsonl")
	for _, want := range []string{`"attach-start"`, `"attach-end"`} {
		if strings.Count(events, want) != 2 {
			t.Fatalf("events should record both attaches:\n%s", events)
		}
	}

	h.isotty("stop")
	// A stopped VM loses its processes; the container does not, so end
	// them here as a reboot would.
	h.mustVM("tmux kill-server 2>/dev/null; true")

	// With tmux turned off on the host, attach is the plain login shell.
	if err := os.WriteFile(filepath.Join(h.home, "attach.yaml"), []byte("tmux: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.isotty("up", "--no-attach")
	plain := h.attach()
	plain.typeLine("touch /workspace/typed-in-plain-shell")
	h.waitFor("typed input to reach the plain shell", 60*time.Second, func() bool {
		_, err := h.vm("test -f /workspace/typed-in-plain-shell")
		return err == nil
	})
	if out, err := h.vm("tmux list-sessions"); err == nil {
		t.Fatalf("no tmux session should exist with tmux: false:\n%s", out)
	}
	plain.typeLine("exit")
	if err := plain.wait(t, 30*time.Second); err != nil {
		t.Fatalf("plain attach: %v", err)
	}

	h.isotty("down")
	if entries, _ := os.ReadDir(filepath.Join(h.root, "gcloud", "instances")); len(entries) != 0 {
		t.Fatalf("down should delete the instance; %d left", len(entries))
	}
}
