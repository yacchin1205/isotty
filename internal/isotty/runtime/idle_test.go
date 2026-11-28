package runtimecfg

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

// idleVM is isotty-idle written against a directory of the test's, with a
// systemctl that records being asked to power off.
type idleVM struct {
	t                                        *testing.T
	script, states, config, lastWork, events string
	poweroffs                                string
	env                                      []string
}

func newIdleVM(t *testing.T, seconds int) *idleVM {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not available")
	}
	dir := t.TempDir()
	vm := &idleVM{t: t, script: filepath.Join(dir, "isotty-idle"), states: filepath.Join(dir, "idle"),
		config: filepath.Join(dir, "idle-stop.json"), lastWork: filepath.Join(dir, "idle-last-work.json"),
		events: filepath.Join(dir, "log", "events.jsonl"), poweroffs: filepath.Join(dir, "poweroffs")}
	script := strings.NewReplacer(
		"__ISOTTY_IDLE_STATE__", vm.states,
		"__ISOTTY_IDLE_CONFIG__", vm.config,
		"__ISOTTY_IDLE_LAST_WORK__", vm.lastWork,
		"__ISOTTY_EVENTS__", vm.events,
		// Booted long ago: a runner may have booted minutes before the test.
		"__ISOTTY_KERNEL_STAT__", filepath.Join(dir, "stat"),
		"__ISOTTY_OPECO_SESSION__", filepath.Join(dir, "opeco-session"),
		"__ISOTTY_OPECO_DIR__", dir,
	).Replace(idleClient)
	bin := filepath.Join(dir, "bin")
	for path, content := range map[string]string{
		vm.script:                       script,
		filepath.Join(dir, "stat"):      "cpu 0 0 0 0\nbtime 1000000000\n",
		filepath.Join(bin, "systemctl"): "#!/bin/sh\necho \"$@\" >> " + vm.poweroffs + "\n",
		// sudo -u <owner> -H runs the rest as the owner; here, as the test.
		filepath.Join(bin, "sudo"): "#!/bin/sh\nshift 3\nexec \"$@\"\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(vm.states, 0o755); err != nil {
		t.Fatal(err)
	}
	vm.env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	vm.setSeconds(seconds)
	return vm
}

func (vm *idleVM) setSeconds(seconds int) {
	vm.t.Helper()
	if err := os.WriteFile(vm.config, []byte(fmt.Sprintf("{\"seconds\": %d}\n", seconds)), 0o644); err != nil {
		vm.t.Fatal(err)
	}
}

// hook runs the hook as an agent may, through a shell: `&& :` keeps the
// shell from replacing itself with python3.
func (vm *idleVM) hook(agent string, event map[string]any) string {
	vm.t.Helper()
	input, err := json.Marshal(event)
	if err != nil {
		vm.t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", "python3 "+vm.script+" hook "+agent+" && :")
	cmd.Env = vm.env
	cmd.Stdin = strings.NewReader(string(input))
	out, err := cmd.Output()
	if err != nil {
		vm.t.Fatalf("hook %v: %v", event, err)
	}
	return string(out)
}

func (vm *idleVM) check() {
	vm.t.Helper()
	cmd := exec.Command("python3", vm.script, "check")
	cmd.Env = vm.env
	if out, err := cmd.CombinedOutput(); err != nil {
		vm.t.Fatalf("check: %v\n%s", err, out)
	}
}

func (vm *idleVM) poweredOff() bool {
	_, err := os.Stat(vm.poweroffs)
	return err == nil
}

func (vm *idleVM) state(agent, session string) map[string]any {
	vm.t.Helper()
	data, err := os.ReadFile(filepath.Join(vm.states, agent+"-"+session+".json"))
	if err != nil {
		return nil
	}
	var state map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		vm.t.Fatal(err)
	}
	return state
}

// age moves every time isotty-idle counts back by d, as if that long had
// passed.
func (vm *idleVM) age(d time.Duration) {
	vm.t.Helper()
	info, err := os.Stat(vm.config)
	if err != nil {
		vm.t.Fatal(err)
	}
	past := info.ModTime().Add(-d)
	if err := os.Chtimes(vm.config, past, past); err != nil {
		vm.t.Fatal(err)
	}
	entries, err := os.ReadDir(vm.states)
	if err != nil {
		vm.t.Fatal(err)
	}
	paths := []string{vm.lastWork}
	for _, e := range entries {
		paths = append(paths, filepath.Join(vm.states, e.Name()))
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			vm.t.Fatal(err)
		}
		var state map[string]any
		if err := json.Unmarshal(data, &state); err != nil {
			vm.t.Fatal(err)
		}
		state["at"] = state["at"].(float64) - d.Seconds()
		data, _ = json.Marshal(state)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			vm.t.Fatal(err)
		}
	}
}

func event(name, session string, fields map[string]any) map[string]any {
	e := map[string]any{"hook_event_name": name, "session_id": session}
	for k, v := range fields {
		e[k] = v
	}
	return e
}

// The VM keeps running while a session works or will be woken, and powers
// off once no session has had work for the configured time, saying why.
func TestIdleStopFollowsTheAgentsWork(t *testing.T) {
	vm := newIdleVM(t, 1800)
	vm.hook("claude", event("UserPromptSubmit", "s1", nil))
	if got := vm.state("claude", "s1"); got["state"] != "working" {
		t.Fatalf("after a prompt: %v", got)
	}
	vm.age(time.Hour)
	vm.check()
	if vm.poweredOff() {
		t.Fatal("powered off while a turn ran")
	}

	// A turn that leaves a scheduled prompt still has work.
	vm.hook("claude", event("Stop", "s1", map[string]any{"background_tasks": []any{}, "session_crons": []any{map[string]any{"schedule": "*/5 * * * *"}}}))
	if got := vm.state("claude", "s1"); got["state"] != "waiting" {
		t.Fatalf("after a turn with a cron: %v", got)
	}
	vm.age(time.Hour)
	vm.check()
	if vm.poweredOff() {
		t.Fatal("powered off while a scheduled prompt waited")
	}

	vm.hook("claude", event("Stop", "s1", map[string]any{"background_tasks": []any{}, "session_crons": []any{}}))
	vm.age(29 * time.Minute)
	vm.check()
	if vm.poweredOff() {
		t.Fatal("powered off before the time without work")
	}
	vm.age(2 * time.Minute)
	vm.check()
	if !vm.poweredOff() {
		t.Fatal("did not power off after the time without work")
	}
	log, err := os.ReadFile(vm.events)
	if err != nil || !strings.Contains(string(log), `"type":"idle-stop"`) || !strings.Contains(string(log), "no agent work for 31 minutes") {
		t.Fatalf("event log: %s %v", log, err)
	}
}

// A turn the human interrupts ends without Stop; the idle prompt or Codex's
// Interrupt ends its work, but not the work of a session that waits for a
// scheduled prompt.
func TestIdleStopSeesAnInterruptedTurnEnd(t *testing.T) {
	vm := newIdleVM(t, 1800)
	vm.hook("claude", event("UserPromptSubmit", "s1", nil))
	vm.hook("claude", event("Notification", "s1", map[string]any{"notification_type": "idle_prompt"}))
	if got := vm.state("claude", "s1"); got["state"] != "idle" {
		t.Fatalf("after the idle prompt: %v", got)
	}
	vm.hook("claude", event("Stop", "s2", map[string]any{"session_crons": []any{map[string]any{"schedule": "0 * * * *"}}}))
	vm.hook("claude", event("Notification", "s2", nil))
	if got := vm.state("claude", "s2"); got["state"] != "waiting" {
		t.Fatalf("the idle prompt ended a wait for a scheduled prompt: %v", got)
	}
	vm.hook("codex", event("UserPromptSubmit", "t1", nil))
	vm.hook("codex", event("Interrupt", "t1", nil))
	if got := vm.state("codex", "t1"); got["state"] != "idle" {
		t.Fatalf("after Codex's interrupt: %v", got)
	}
}

// Codex reads a Stop hook's stdout as JSON; a session that ends leaves no
// state, nor does one whose agent is gone.
func TestIdleStopForgetsSessionsThatEnded(t *testing.T) {
	vm := newIdleVM(t, 1800)
	vm.hook("codex", event("UserPromptSubmit", "t1", nil))
	if out := vm.hook("codex", event("Stop", "t1", nil)); strings.TrimSpace(out) != "{}" {
		t.Fatalf("Stop answered %q", out)
	}
	vm.hook("codex", event("SessionEnd", "t1", nil))
	if got := vm.state("codex", "t1"); got != nil {
		t.Fatalf("an ended session is left: %v", got)
	}

	gone := exec.Command("true")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"agent": "claude", "session": "dead", "state": "working", "pid": gone.ProcessState.Pid(), "pid_start": "0", "at": float64(time.Now().Unix())})
	if err := os.WriteFile(filepath.Join(vm.states, "claude-dead.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	vm.age(time.Hour)
	vm.check()
	if got := vm.state("claude", "dead"); got != nil {
		t.Fatalf("a session whose agent is gone is left: %v", got)
	}
	if !vm.poweredOff() {
		t.Fatal("a session whose agent is gone kept the VM up")
	}
}

func TestIdleStopZeroNeverStops(t *testing.T) {
	vm := newIdleVM(t, 0)
	vm.age(24 * time.Hour)
	vm.check()
	if vm.poweredOff() {
		t.Fatal("powered off with the stop turned off")
	}
}

// The hooks are the command's own, so another up replaces them and leaves
// the human's alone.
func TestIdleHooks(t *testing.T) {
	for agent, want := range map[string][]string{
		"claude": {"UserPromptSubmit", "Stop", "SessionEnd", `"matcher":"idle_prompt"`, ".claude/settings.json"},
		"codex":  {"UserPromptSubmit", "Stop", "SessionEnd", "Interrupt", ".codex/hooks.json"},
	} {
		file, hooks := idleHooks(agent)
		got := file + string(hooks)
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("%s hooks lack %s: %s", agent, w, got)
			}
		}
		if !strings.Contains(string(hooks), `"command":"`+IdleClientPath+" hook "+agent+`"`) {
			t.Errorf("%s hooks: %s", agent, hooks)
		}
	}
	command := IdleStopCommand([]string{"claude"}, 30*time.Minute, "/usr/local/bin/isotty-opeco-session", "/var/lib/isotty/opeco")
	for _, w := range []string{"enable --now isotty-idle.timer", "hooks \"$HOME\"/.claude/settings.json", "'" + IdleClientPath + " hook claude'"} {
		if !strings.Contains(command, w) {
			t.Errorf("command lacks %s", w)
		}
	}
}

// A VM that keeps an opeco session says on its card why it powers off.
func TestIdleStopTellsTheOpecoSession(t *testing.T) {
	vm := newIdleVM(t, 1800)
	told := filepath.Join(filepath.Dir(vm.script), "told")
	if err := os.WriteFile(filepath.Join(filepath.Dir(vm.script), "opeco-session"), []byte("#!/bin/sh\necho \"$@\" > "+told+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	vm.age(time.Hour)
	vm.check()
	data, err := os.ReadFile(told)
	if err != nil || !strings.HasPrefix(string(data), "notify VM powered off: no agent work for") {
		t.Fatalf("told %q %v", data, err)
	}
	if !vm.poweredOff() {
		t.Fatal("did not power off")
	}
}

// An opeco session that cannot be told holds up nothing: the VM powers off.
func TestIdleStopGoesOnWhenOpecoFails(t *testing.T) {
	vm := newIdleVM(t, 1800)
	if err := os.WriteFile(filepath.Join(filepath.Dir(vm.script), "opeco-session"), []byte("#!/bin/sh\necho offline >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	vm.age(time.Hour)
	vm.check()
	if !vm.poweredOff() {
		t.Fatal("a failing opeco session held the power-off up")
	}
}
