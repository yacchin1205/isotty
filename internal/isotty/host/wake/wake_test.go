package wake

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A wake is kept in time order and forgotten once delivered.
func TestStoreKeepsWakesInTimeOrder(t *testing.T) {
	home := t.TempDir()
	now := time.Now().Truncate(time.Second)
	late, err := Add(home, "abc", Request{Agent: "claude", Session: "s1", At: now.Add(2 * time.Hour), Prompt: "late"})
	if err != nil {
		t.Fatal(err)
	}
	early, err := Add(home, "abc", Request{Agent: "claude", Session: "s2", At: now.Add(time.Hour), Prompt: "early"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Load(home, "abc")
	if err != nil || len(got) != 2 || got[0].ID != early.ID || got[1].ID != late.ID {
		t.Fatalf("wakes: %+v %v", got, err)
	}
	if _, _, err := Remove(home, "abc", early.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := Load(home, "abc"); len(got) != 1 || got[0].ID != late.ID {
		t.Fatalf("after remove: %+v", got)
	}
	if _, err := Add(home, "abc", Request{Agent: "codex", Session: "s", At: now, Prompt: "p"}); err != nil {
		t.Fatalf("a codex wake was refused: %v", err)
	}
	if _, err := Add(home, "abc", Request{Agent: "gemini", Session: "s", At: now, Prompt: "p"}); err == nil {
		t.Fatal("a wake of an agent isotty cannot resume was kept")
	}
}

// A project takes wakes only once the human allowed them on the host.
func TestHandlerKeepsAndListsWakes(t *testing.T) {
	home := t.TempDir()
	var recorded []Request
	server := httptest.NewServer(Handler(home, "abc", func(_ string, r Request) error { recorded = append(recorded, r); return nil }))
	defer server.Close()
	body := `{"agent":"claude","session":"s1","after_seconds":3600,"prompt":"go on"}`
	resp, err := http.Post(server.URL+"/wakes", "application/json", strings.NewReader(body))
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a wake was taken without consent: %v %v", resp, err)
	}
	if err := Enable(home, "abc", "/projects/a"); err != nil {
		t.Fatal(err)
	}
	resp, err = http.Post(server.URL+"/wakes", "application/json", strings.NewReader(body))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("post: %v %v", resp, err)
	}
	var kept Request
	if err := json.NewDecoder(resp.Body).Decode(&kept); err != nil || kept.ID == "" || time.Until(kept.At) < 59*time.Minute {
		t.Fatalf("kept: %+v %v", kept, err)
	}
	if len(recorded) != 1 || recorded[0].ID != kept.ID {
		t.Fatalf("recorded: %+v", recorded)
	}
	resp, err = http.Post(server.URL+"/wakes", "application/json", strings.NewReader(`{"agent":"claude","session":"s1","after_seconds":0,"prompt":"x"}`))
	if err != nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("no time: %v %v", resp, err)
	}
	resp, err = http.Post(server.URL+"/wakes", "application/json", strings.NewReader(`{"agent":"claude","session":"s1","cron":"0 6 * * *","prompt":"daily"}`))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("cron: %v %v", resp, err)
	}
	var daily Request
	if err := json.NewDecoder(resp.Body).Decode(&daily); err != nil || daily.Cron != "0 6 * * *" || daily.At.Hour() != 6 || daily.At.Minute() != 0 {
		t.Fatalf("daily: %+v %v", daily, err)
	}
	resp, err = http.Post(server.URL+"/wakes", "application/json", strings.NewReader(`{"agent":"claude","session":"s1","cron":"0 6 * * *","after_seconds":60,"prompt":"x"}`))
	if err != nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("both: %v %v", resp, err)
	}
	resp, err = http.Get(server.URL + "/wakes")
	if err != nil {
		t.Fatal(err)
	}
	var listed []Request
	if err := json.NewDecoder(resp.Body).Decode(&listed); err != nil || len(listed) != 2 {
		t.Fatalf("list: %+v %v", listed, err)
	}
	req, _ := http.NewRequest(http.MethodDelete, server.URL+"/wakes/"+daily.ID, nil)
	if resp, err = http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel: %v %v", resp, err)
	}
	if got, _ := Load(home, "abc"); len(got) != 1 || got[0].ID != kept.ID {
		t.Fatalf("after cancel: %+v", got)
	}
	if last := recorded[len(recorded)-1]; last.ID != daily.ID {
		t.Fatalf("the cancel was not recorded: %+v", recorded)
	}
	if resp, err = http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cancel of a wake that is gone: %v %v", resp, err)
	}
}

// The client asks for its own conversation: the session isotty-idle knows
// for the agent process it runs under.
func TestClientAsksForItsOwnSession(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not available")
	}
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		w.Write([]byte(`{"id":"w1","at":"2026-11-25T15:00:00Z"}`))
	}))
	defer server.Close()
	dir := t.TempDir()
	states := filepath.Join(dir, "idle")
	if err := os.Mkdir(states, 0o755); err != nil {
		t.Fatal(err)
	}
	// This test process stands for the agent the client runs under.
	state, _ := json.Marshal(map[string]any{"agent": "claude", "session": "conv-1", "state": "working", "pid": os.Getpid(), "pid_start": "x", "at": 0})
	if err := os.WriteFile(filepath.Join(states, "claude-conv-1.json"), state, 0o644); err != nil {
		t.Fatal(err)
	}
	script := strings.NewReplacer("http://127.0.0.1:__ISOTTY_GATEWAY_PORT__/wake", server.URL, "__ISOTTY_IDLE_STATE__", states).Replace(clientScript)
	path := filepath.Join(dir, "isotty-wake")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd := exec.Command("sh", "-c", "python3 "+path+" in 1h 'check CI and go on' && :")
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if got["session"] != "conv-1" || got["agent"] != "claude" || got["after_seconds"] != float64(3600) || got["prompt"] != "check CI and go on" {
		t.Fatalf("asked: %v", got)
	}
	if !strings.Contains(out.String(), "wake w1") {
		t.Fatalf("output: %s", out.String())
	}
}

func TestConsents(t *testing.T) {
	home := t.TempDir()
	for hash, path := range map[string]string{"bbb": "/projects/b", "aaa": "/projects/a"} {
		if err := Enable(home, hash, path); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Add(home, "bbb", Request{Agent: "claude", Session: "s", At: time.Now(), Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	consents, err := Consents(home)
	if err != nil || len(consents) != 2 || consents[0] != (Consent{ProjectPath: "/projects/a", ProjectHash: "aaa"}) {
		t.Fatalf("consents: %+v %v", consents, err)
	}
	if err := Disable(home, "bbb"); err != nil {
		t.Fatal(err)
	}
	if enabled, _ := Enabled(home, "bbb"); enabled {
		t.Fatal("still enabled after disable")
	}
	if wakes, _ := Load(home, "bbb"); len(wakes) != 0 {
		t.Fatalf("wakes kept after disable: %+v", wakes)
	}
}

// No wake types into a conversation that still has work, or waits for an
// approval: the keys would answer it in the human's place.
func TestDeliverSkipsABusyConversation(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not available")
	}
	dir := t.TempDir()
	states := filepath.Join(dir, "idle")
	if err := os.Mkdir(states, 0o755); err != nil {
		t.Fatal(err)
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", os.Getpid()))
	if err != nil {
		t.Skip("no /proc")
	}
	fields := strings.Fields(string(stat[strings.LastIndex(string(stat), ")")+2:]))
	state, _ := json.Marshal(map[string]any{"agent": "claude", "session": "conv-1", "state": "working", "pid": os.Getpid(), "pid_start": fields[19], "at": 0})
	if err := os.WriteFile(filepath.Join(states, "claude-conv-1.json"), state, 0o644); err != nil {
		t.Fatal(err)
	}
	script := strings.NewReplacer("__ISOTTY_IDLE_STATE__", states).Replace(clientScript)
	path := filepath.Join(dir, "isotty-wake")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("python3", path, "deliver", "claude", "conv-1", base64.StdEncoding.EncodeToString([]byte("go on"))).CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 75 || !strings.Contains(string(out), "still has work") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// A conversation whose agent is gone is resumed with that agent's own
// command, Codex's as Claude's, and told that the human may not be
// watching.
func TestDeliverResumesWithTheAgentsOwnCommand(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not available")
	}
	for agent, want := range map[string]string{
		"claude": "claude --resume conv-1 'go on (isotty wake: the human is likely not watching this chat.",
		"codex":  "codex resume --no-daemon conv-1 'go on (isotty wake: the human is likely not watching this chat.",
	} {
		dir := t.TempDir()
		bin := filepath.Join(dir, "bin")
		if err := os.Mkdir(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		calls := filepath.Join(dir, "calls")
		if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> "+calls+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		script := strings.NewReplacer("__ISOTTY_IDLE_STATE__", filepath.Join(dir, "none"), "__ISOTTY_OPEN_CLIENT__", "/usr/local/bin/isotty-open").Replace(clientScript)
		path := filepath.Join(dir, "isotty-wake")
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("python3", path, "deliver", agent, "conv-1", base64.StdEncoding.EncodeToString([]byte("go on")))
		cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", agent, err, out)
		}
		data, _ := os.ReadFile(calls)
		if !strings.Contains(string(data), "new-window") || !strings.Contains(string(data), "bash -lc "+want) {
			t.Errorf("%s: tmux was asked\n%s\nwant bash -lc %s", agent, data, want)
		}
	}
}

// A wake shorter than one and a half times the idle stop is refused before
// it reaches the host: it would race the power-off.
func TestClientRefusesAWakeShorterThanTheIdleStop(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not available")
	}
	dir := t.TempDir()
	config := filepath.Join(dir, "idle-stop.json")
	if err := os.WriteFile(config, []byte(`{"seconds": 1800}`), 0o644); err != nil {
		t.Fatal(err)
	}
	script := strings.NewReplacer("__ISOTTY_IDLE_CONFIG__", config, "__ISOTTY_IDLE_STATE__", filepath.Join(dir, "none"),
		"http://127.0.0.1:__ISOTTY_GATEWAY_PORT__/wake", "http://127.0.0.1:9/wake").Replace(clientScript)
	path := filepath.Join(dir, "isotty-wake")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("python3", path, "in", "30m", "go on").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "too short for a wake") || !strings.Contains(string(out), "45 minutes") {
		t.Fatalf("30m: %v\n%s", err, out)
	}
	// 45 minutes passes the check and goes on to look for its session.
	out, err = exec.Command("python3", path, "in", "45m", "go on").CombinedOutput()
	if err == nil || strings.Contains(string(out), "too short") || !strings.Contains(string(out), "not run by an agent session") {
		t.Fatalf("45m: %v\n%s", err, out)
	}
}
