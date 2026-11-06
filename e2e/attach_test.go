//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
)

// typedReaches types a command into the attach and waits for the file it
// creates: the proof that keystrokes reach a shell in the workspace, since
// the path is relative.
func typedReaches(t *testing.T, h *harness, s *session, command, file string) {
	t.Helper()
	s.typeLine(command)
	h.waitFor("the shell to run "+command, 60*time.Second, func() bool {
		_, err := h.vm("test -e /workspace/" + file)
		return err == nil
	})
}

// TestAttachKeepsTheSessionInTmux guards what the human relies on: what
// runs in the attach survives the attach, a second terminal shares it, and
// the next attach is the same session.
func TestAttachKeepsTheSessionInTmux(t *testing.T) {
	h := newHarness(t)
	h.isotty("up", "--no-attach")

	first := h.attach()
	typedReaches(t, h, first, "touch first-terminal", "first-terminal")
	// An open in the session reaches the host: the shell has BROWSER.
	typedReaches(t, h, first, `echo "$BROWSER" > browser`, "browser")
	if out := h.mustVM("cat /workspace/browser"); strings.TrimSpace(out) != "/usr/local/bin/isotty-open" {
		t.Fatalf("BROWSER in the session shell = %q", out)
	}
	// A link an agent prints reaches the terminal: tmux relays OSC 8 to it.
	if out := h.mustVM("tmux list-clients -t isotty -F '#{client_termfeatures}'"); !strings.Contains(out, "hyperlinks") {
		t.Fatalf("features of the attached terminal = %q, want hyperlinks", strings.TrimSpace(out))
	}

	// A second terminal works in the same session and leaves nothing
	// behind when it goes.
	second := h.attach("--no-host")
	typedReaches(t, h, second, "touch second-terminal", "second-terminal")
	sessions := h.mustVM("tmux list-sessions -F '#{session_name} #{session_group}'")
	if strings.Count(sessions, "isotty\n") != 2 || strings.Count(sessions, "\n") != 2 {
		t.Fatalf("the second terminal should be a session in the group of the first:\n%s", sessions)
	}
	second.typeLine("tmux detach")
	if err := second.wait(t, 30*time.Second); err != nil {
		t.Fatalf("second attach: %v", err)
	}
	if out := h.mustVM("tmux list-sessions -F '#{session_name}'"); strings.TrimSpace(out) != "isotty" {
		t.Fatalf("sessions after the second terminal left:\n%s", out)
	}

	// Detaching ends the attach but not what runs in it.
	first.typeLine("sleep 600")
	first.typeLine("")
	h.waitFor("the sleep to start", 30*time.Second, func() bool {
		_, err := h.vm("pgrep -u " + vmUser + " -x sleep")
		return err == nil
	})
	h.mustVM("tmux detach-client -s isotty")
	if err := first.wait(t, 30*time.Second); err != nil {
		t.Fatalf("first attach: %v", err)
	}
	h.mustVM("pgrep -u " + vmUser + " -x sleep")

	// The next attach is that session, with the sleep still in front.
	again := h.attach()
	h.waitFor("the attach to return to the session", 60*time.Second, func() bool {
		out, err := h.vm("tmux list-sessions -F '#{session_name} #{session_attached}'")
		return err == nil && strings.TrimSpace(out) == "isotty 1"
	})
	if out := h.mustVM("tmux display-message -p -t isotty '#{pane_current_command}'"); strings.TrimSpace(out) != "sleep" {
		t.Fatalf("the re-attached session shows %q, not the sleep", strings.TrimSpace(out))
	}
	again.typeLine("\x03")
	again.typeLine("tmux detach")
	if err := again.wait(t, 30*time.Second); err != nil {
		t.Fatalf("re-attach: %v", err)
	}

	h.isotty("down")
}

// TestAttachWithTmuxOffIsAPlainShell guards the way back: with tmux: false
// in the host's attach.yaml, attach is the login shell in the workspace it
// always was, and no tmux is involved.
func TestAttachWithTmuxOffIsAPlainShell(t *testing.T) {
	h := newHarness(t)
	if err := os.WriteFile(filepath.Join(h.home, "attach.yaml"), []byte("tmux: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.isotty("up", "--no-attach")

	plain := h.attach()
	typedReaches(t, h, plain, `echo "tmux=$TMUX" > plain`, "plain")
	if out := h.mustVM("cat /workspace/plain"); strings.TrimSpace(out) != "tmux=" {
		t.Fatalf("the plain shell is inside tmux: %q", out)
	}
	if out, err := h.vm("tmux list-sessions"); err == nil {
		t.Fatalf("no tmux session should exist:\n%s", out)
	}
	plain.typeLine("exit")
	if err := plain.wait(t, 30*time.Second); err != nil {
		t.Fatalf("plain attach: %v", err)
	}

	h.isotty("down")
}

// TestAttachWaitsOutASessionThatDropped guards the attach after the host
// changed networks: the session that dropped holds the gateway's VM port
// until sshd notices, and the attach says so and waits for the port instead
// of failing on the forward.
func TestAttachWaitsOutASessionThatDropped(t *testing.T) {
	h := newHarness(t)
	h.isotty("up", "--no-attach")

	gatewayPortHeld := func() bool {
		_, err := h.vm(gateway.PortFreeCommand(0))
		return err != nil
	}
	// To the VM, a dropped session is an ssh that still holds the port.
	dropped := exec.Command("ssh", "-p", h.port, "-i", filepath.Join(h.root, "key"),
		"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR",
		"-o", "ExitOnForwardFailure=yes", "-N", "-R", "127.0.0.1:61001:127.0.0.1:9", vmUser+"@127.0.0.1")
	if err := dropped.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dropped.Process.Kill() })
	h.waitFor("the dropped session to hold the gateway port", 30*time.Second, gatewayPortHeld)

	s := h.attach()
	h.waitFor("the attach to say it waits for the port", 60*time.Second, func() bool {
		return strings.Contains(s.output.String(), "VM port 61001 is still taken")
	})
	if _, err := h.vm("tmux has-session -t isotty"); err == nil {
		t.Fatal("the attach reached the session while the gateway port was held")
	}

	// sshd notices: the port is free, and the attach goes on to hold it.
	if err := dropped.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	h.waitFor("the attach to reach the session", 60*time.Second, func() bool {
		select {
		case err := <-s.done:
			t.Fatalf("the attach ended before reaching the session: %v\n%s", err, s.output.String())
		default:
		}
		out, err := h.vm("tmux list-sessions -F '#{session_name} #{session_attached}'")
		return err == nil && strings.TrimSpace(out) == "isotty 1"
	})
	if !gatewayPortHeld() {
		t.Fatal("the attach should forward the gateway on the port it waited for")
	}
	h.mustVM("tmux detach-client -s isotty")
	if err := s.wait(t, 30*time.Second); err != nil {
		t.Fatalf("attach: %v", err)
	}

	h.isotty("down")
}
