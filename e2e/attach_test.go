//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	// A comment the human leaves in the viewer on the host reaches the VM
	// through the sync, where the agent reads it with the installed client.
	review := filepath.Join(h.project, ".isotty", "reviews", "browser.jsonl")
	if err := os.MkdirAll(filepath.Dir(review), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(review, []byte(`{"id":"r1","at":"2026-11-08T10:00:00Z","anchor":{"lines":[1,1],"quote":"/usr/local/bin/isotty-open"},"body":"Looks right.","file_sha256":"sha256:old","resolved":false}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.waitFor("the sync to carry the comment into the VM", 60*time.Second, func() bool {
		out, err := h.vm("cd /workspace && isotty-open reviews")
		return err == nil && strings.Contains(out, "browser\n  L1  2026-11-08T10:00:00Z  \"/usr/local/bin/isotty-open\"  [file changed since; quoted text now at L1]\n    Looks right.")
	})
	// A link an agent prints reaches the terminal: tmux relays OSC 8 to it.
	if out := h.mustVM("tmux list-clients -t isotty -F '#{client_termfeatures}'"); !strings.Contains(out, "hyperlinks") {
		t.Fatalf("features of the attached terminal = %q, want hyperlinks", strings.TrimSpace(out))
	}

	// A second terminal works in the same session and leaves nothing
	// behind when it goes.
	second := h.attach()
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

// TestDaemonHoldsTheGatewayWithoutATerminal guards the host daemon: it
// holds the gateway once `up` has handed it the project, with no terminal
// open; it waits out a connection that dropped and still holds the
// gateway's VM port until sshd notices, instead of failing on the forward;
// and an attach starts it when it is not running.
func TestDaemonHoldsTheGatewayWithoutATerminal(t *testing.T) {
	h := newHarness(t)
	h.isotty("up", "--no-attach")
	h.waitForGateway()

	h.isotty("daemon", "stop")
	h.waitFor("the gateway to go with the daemon", 60*time.Second, func() bool { return !h.gatewayHeld() })
	// To the VM, a dropped connection is an ssh that still holds the port.
	dropped := exec.Command("ssh", "-p", h.port, "-i", filepath.Join(h.root, "key"),
		"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR",
		"-o", "ExitOnForwardFailure=yes", "-N", "-R", "127.0.0.1:61001:127.0.0.1:9", vmUser+"@127.0.0.1")
	if err := dropped.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dropped.Process.Kill() })
	h.waitFor("the dropped connection to hold the gateway port", 30*time.Second, h.gatewayHeld)

	// The attach starts the daemon, which waits for the port.
	s := h.attach()
	h.waitFor("the daemon to wait for the VM port", 60*time.Second, func() bool {
		return strings.Contains(h.daemonStatus(), "waiting for the VM port")
	})
	// sshd notices: the port is free, and the daemon goes on to hold it.
	if err := dropped.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	h.waitForGateway()
	h.mustVM("tmux detach-client -s isotty")
	if err := s.wait(t, 30*time.Second); err != nil {
		t.Fatalf("attach: %v", err)
	}

	h.isotty("down")
}

// TestAttachReconnectsAfterADrop guards an attach through a network that is
// gone for a while: the session that ran for minutes drops, the tries that
// follow fail while the VM cannot be reached, and a later one returns to
// the same tmux session, while the daemon holds the gateway again. The timings are
// the real ones, so it takes some four minutes.
func TestAttachReconnectsAfterADrop(t *testing.T) {
	h := newHarness(t)
	h.isotty("up", "--no-attach")

	s := h.attach()
	typedReaches(t, h, s, "touch before-drop", "before-drop")
	// Only a session that ran three minutes is attached again.
	time.Sleep(3*time.Minute + 5*time.Second)

	// The network goes: the VM stops taking the host's key, and the
	// connection of the session is cut.
	h.mustVM("mv ~/.ssh/authorized_keys ~/.ssh/authorized_keys.away")
	if out, err := exec.Command("docker", "exec", h.container, "pkill", "-KILL", "-f", "^sshd: "+vmUser).CombinedOutput(); err != nil {
		t.Fatalf("cut the session: %v\n%s", err, out)
	}
	h.waitFor("the first try to fail", 60*time.Second, func() bool {
		return strings.Contains(s.output.String(), "(2/5)")
	})
	// The network comes back before the next try.
	h.mustVM("mv ~/.ssh/authorized_keys.away ~/.ssh/authorized_keys")

	h.waitFor("the attach to return to the session", 90*time.Second, func() bool {
		select {
		case err := <-s.done:
			t.Fatalf("the attach ended instead of attaching again: %v\n%s", err, s.output.String())
		default:
		}
		out, err := h.vm("tmux list-sessions -F '#{session_name} #{session_attached}'")
		return err == nil && strings.TrimSpace(out) == "isotty 1"
	})
	out := s.output.String()
	for _, want := range []string{"attaching again in 5 seconds (1/5)", "Attaching again failed", "attaching again in 30 seconds (2/5)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the attach did not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "(3/5)") {
		t.Fatalf("tried more than it needed:\n%s", out)
	}
	// The daemon's connection was cut with the terminal's and comes back
	// on its own.
	h.waitForGateway()
	typedReaches(t, h, s, "touch after-drop", "after-drop")

	h.mustVM("tmux detach-client -s isotty")
	if err := s.wait(t, 30*time.Second); err != nil {
		t.Fatalf("attach: %v", err)
	}
	h.isotty("down")
}
