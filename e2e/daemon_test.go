//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// What the host daemon and the wakes it serves guarantee, through the
// lifecycle of a VM that comes and goes: docs/FORWARD.md "The Host Daemon"
// and docs/WAKE.md.

// upWithAnAgent brings the project up with Claude configured, which is what
// the idle stop and the wake client come with, and lets it take wakes.
func upWithAnAgent(t *testing.T, h *harness) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(h.project, ".isotty", "agent.yaml"), []byte("agents:\n  claude: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.isotty("schedule", "enable")
	h.isotty("up", "--no-attach")
	h.waitForGateway()
	// A stand-in for the agent's CLI, ahead of the real one on PATH, that
	// says how it was started.
	h.mustVM(`printf '#!/bin/sh\necho "$@" >> /tmp/agent-started\nsleep 3600\n' | sudo tee /usr/local/bin/claude >/dev/null && sudo chmod 755 /usr/local/bin/claude`)
}

// projectHash is the project's, as the host keeps its state under it.
func (h *harness) projectHash() string {
	h.t.Helper()
	matches, err := filepath.Glob(filepath.Join(h.home, "projects", "*.json"))
	if err != nil {
		h.t.Fatal(err)
	}
	for _, m := range matches {
		if name := strings.TrimSuffix(filepath.Base(m), ".json"); !strings.Contains(name, ".") {
			return name
		}
	}
	h.t.Fatal("no project state")
	return ""
}

// askWake asks the gateway for a wake, as isotty-wake does for the agent.
func (h *harness) askWake(body map[string]any) string {
	h.t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		h.t.Fatal(err)
	}
	out := h.mustVM("curl -sS -X POST -H 'Content-Type: application/json' --data '" + string(data) + "' http://127.0.0.1:61001/wake/wakes")
	var wake struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &wake); err != nil || wake.ID == "" {
		h.t.Fatalf("the gateway did not keep the wake: %s", out)
	}
	return wake.ID
}

func (h *harness) wakes(hash string) string {
	data, err := os.ReadFile(filepath.Join(h.home, "projects", hash+".wakes.json"))
	if err != nil {
		return ""
	}
	return string(data)
}

// stopVM stops the VM as an idle stop does, as far as the host can tell: GCP
// reports it stopped, and its connections are gone.
func (h *harness) stopVM() {
	h.t.Helper()
	here, err := os.Getwd()
	if err != nil {
		h.t.Fatal(err)
	}
	// The fake gcloud by its path: exec finds a bare name on this process's
	// PATH, not on the one given to the command.
	stop := exec.Command(filepath.Join(here, "fake-gcloud", "gcloud"), "compute", "instances", "stop", h.instance(), "--project", "e2e-project", "--zone", "e2e-zone")
	stop.Env = h.env
	if out, err := stop.CombinedOutput(); err != nil {
		h.t.Fatalf("stop the VM: %v\n%s", err, out)
	}
	if out, err := exec.Command("docker", "exec", h.container, "pkill", "-KILL", "-f", "^sshd: "+vmUser).CombinedOutput(); err != nil {
		h.t.Fatalf("cut the VM's connections: %v\n%s", err, out)
	}
	h.waitFor("the daemon to see the VM stopped", 90*time.Second, func() bool {
		return strings.Contains(h.daemonStatus(), "waiting for the VM to run")
	})
}

func (h *harness) instance() string {
	h.t.Helper()
	data, err := os.ReadFile(filepath.Join(h.home, "projects", h.projectHash()+".json"))
	if err != nil {
		h.t.Fatal(err)
	}
	var state struct {
		InstanceName string `json:"instance_name"`
	}
	if err := json.Unmarshal(data, &state); err != nil || state.InstanceName == "" {
		h.t.Fatalf("no instance in the state: %s", data)
	}
	return state.InstanceName
}

// TestWakeStartsTheStoppedVMAndResumesTheConversation guards the wake: an
// agent asks for one, the VM stops, and at the time the daemon starts it
// as `up` does, holds its gateway again and resumes the conversation in a
// new tmux window with the agent's own command; the wake is then gone, and
// the VM's event log has it. `down` then lets the project go.
func TestWakeStartsTheStoppedVMAndResumesTheConversation(t *testing.T) {
	h := newHarness(t)
	upWithAnAgent(t, h)
	hash := h.projectHash()
	h.askWake(map[string]any{"agent": "claude", "session": "conv-1", "after_seconds": 45, "prompt": "go on"})
	h.stopVM()

	h.waitFor("the wake to resume the conversation", 5*time.Minute, func() bool {
		out, err := h.vm("cat /tmp/agent-started")
		return err == nil && strings.Contains(out, "--resume conv-1 go on")
	})
	if calls := h.gcloudCalls(); !strings.Contains(calls, "compute instances start") {
		t.Fatalf("the daemon did not start the VM:\n%s", calls)
	}
	h.waitForGateway()
	if windows := h.mustVM("tmux list-windows -t isotty -F '#{pane_start_command}'"); !strings.Contains(windows, "claude --resume conv-1") {
		t.Fatalf("the conversation is not in the session's windows:\n%s", windows)
	}
	h.waitFor("the delivered wake to be forgotten", 60*time.Second, func() bool {
		return !strings.Contains(h.wakes(hash), "conv-1")
	})
	if log := h.mustVM("sudo cat /var/log/isotty/events.jsonl"); !strings.Contains(log, `"type":"wake-request"`) || !strings.Contains(log, `"type":"wake"`) {
		t.Fatalf("the event log lacks the wake:\n%s", log)
	}

	// `down` lets the project go, and its wakes with it.
	h.askWake(map[string]any{"agent": "claude", "session": "conv-2", "after_seconds": 3600, "prompt": "later"})
	h.isotty("down")
	h.waitFor("the daemon to let the project go", 60*time.Second, func() bool {
		return strings.Contains(h.daemonStatus(), "The daemon holds no project")
	})
	if wakes := h.wakes(hash); wakes != "" {
		t.Fatalf("wakes left after down:\n%s", wakes)
	}
}

// TestRecurringWakeSkipsABusyConversation guards the recurring wake: a time
// at which its conversation still has work is skipped and the wake goes on
// to its next time, and once the conversation waits for input the next
// time types the prompt into its pane.
func TestRecurringWakeSkipsABusyConversation(t *testing.T) {
	h := newHarness(t)
	upWithAnAgent(t, h)
	hash := h.projectHash()
	// The conversation's agent, in a pane of the session, as the idle hooks
	// record it: working.
	h.mustVM("tmux new-session -d -s isotty -c /workspace 'exec sleep 3600'")
	pid := strings.TrimSpace(h.mustVM("tmux list-panes -t isotty -F '#{pane_pid}'"))
	start := strings.Fields(h.mustVM("cat /proc/" + pid + "/stat | sed 's/.*) //'"))[19]
	recordState := func(state string) {
		h.mustVM(fmt.Sprintf(`printf '{"agent":"claude","session":"conv-r","state":"%s","pid":%s,"pid_start":"%s","at":0}' > /var/lib/isotty/idle/claude-conv-r.json`, state, pid, start))
	}
	recordState("working")
	id := h.askWake(map[string]any{"agent": "claude", "session": "conv-r", "cron": "* * * * *", "prompt": "check again"})

	h.waitFor("a time to be skipped", 3*time.Minute, func() bool {
		log, err := h.vm("sudo cat /var/log/isotty/events.jsonl")
		return err == nil && strings.Contains(log, "skipped: the conversation still has work")
	})
	if !strings.Contains(h.wakes(hash), id) {
		t.Fatalf("a skipped recurring wake was forgotten:\n%s", h.wakes(hash))
	}

	recordState("idle")
	h.waitFor("the next time to type the prompt", 3*time.Minute, func() bool {
		out, err := h.vm("tmux capture-pane -p -t isotty")
		return err == nil && strings.Contains(out, "check again")
	})
	if !strings.Contains(h.wakes(hash), id) {
		t.Fatalf("a delivered recurring wake was forgotten:\n%s", h.wakes(hash))
	}
	h.isotty("down")
}

// TestDaemonStopsServingOnlyTheRelayWhosePinNoLongerMatches guards the pins
// under the daemon: a synced file that no longer matches its pin takes that
// relay out, says so, and leaves the gateway and the other relays up.
func TestDaemonStopsServingOnlyTheRelayWhosePinNoLongerMatches(t *testing.T) {
	h := newHarness(t)
	// A registry the host can reach, as the pin requires.
	registry := h.container + "-registry"
	hostDocker(t, "run", "-d", "--rm", "--name", registry, "-p", "127.0.0.1:0:5000", "registry:2")
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", registry).Run() })
	address := hostDocker(t, "port", registry, "5000/tcp")
	repo := "127.0.0.1:" + address[strings.LastIndex(address, ":")+1:] + "/e2e/app"
	h.waitFor("the registry to answer", 60*time.Second, func() bool {
		cmd := exec.Command(h.bin, "host", "docker", "add", "--repo", repo)
		cmd.Dir, cmd.Env = h.project, h.env
		return cmd.Run() == nil
	})
	h.isotty("up", "--no-attach")
	h.waitForGateway()
	if status := h.daemonStatus(); !strings.Contains(status, "Docker") {
		t.Fatalf("the pinned relay is not served:\n%s", status)
	}
	// The agent, by mistake, widens the synced file.
	path := filepath.Join(h.project, ".isotty", "docker.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(data), repo, repo+"-other")), 0o644); err != nil {
		t.Fatal(err)
	}
	h.isotty("daemon", "restart")
	h.waitForGateway()
	status := h.daemonStatus()
	if !strings.Contains(status, "Docker not served") || !strings.Contains(status, "browser open") {
		t.Fatalf("the daemon should refuse Docker alone:\n%s", status)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	h.isotty("down")
}

// TestUpReplacesADaemonOfAnotherBinary guards the replacement: a binary put
// in the place of the one the daemon runs changes nothing a running process
// would notice, so `up` replaces a daemon that runs another binary.
func TestUpReplacesADaemonOfAnotherBinary(t *testing.T) {
	h := newHarness(t)
	h.isotty("up", "--no-attach")
	h.waitForGateway()
	// Another binary in the same place: the same build with one byte more.
	data, err := os.ReadFile(h.bin)
	if err != nil {
		t.Fatal(err)
	}
	next := h.bin + ".next"
	if err := os.WriteFile(next, append(data, 0), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, h.bin); err != nil {
		t.Fatal(err)
	}
	h.isotty("up", "--no-attach")
	h.waitForGateway()
	log, err := os.ReadFile(filepath.Join(h.home, "daemon.log"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(log), "daemon started"); n != 2 {
		t.Fatalf("the daemon started %d times, want 2:\n%s", n, log)
	}
	h.isotty("down")
}
