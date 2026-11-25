package opeco

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpecoActiveNeedsAnAgent(t *testing.T) {
	if Active(nil, Settings{}) {
		t.Fatal("opeco should be inactive without agents")
	}
	if !Active([]string{"claude"}, Settings{}) {
		t.Fatal("opeco should be active with an agent")
	}
}

// Either file turns opeco off, and the other cannot turn it back on: the
// project's is synced into the VM, where the agent can rewrite it.
func TestOpecoIsOffWhenEitherFileSaysSo(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		user, project *bool
		want          bool
	}{
		{nil, nil, true},
		{&on, &on, true},
		{nil, &off, false},
		{&off, nil, false},
		{&off, &on, false},
		{&on, &off, false},
	} {
		settings := Settings{User: Config{Enabled: tc.user}, Project: Config{Enabled: tc.project}}
		if got := Active([]string{"claude"}, settings); got != tc.want {
			t.Fatalf("user %v, project %v: active = %v", tc.user, tc.project, got)
		}
	}
}

// The files are read from where the human and `host opeco disable` write
// them, and a key that is not a setting is a mistake to say, not to skip.
func TestOpecoSettingsComeFromBothFiles(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	settings, err := LoadSettings(home, project)
	if err != nil || !settings.Enabled() {
		t.Fatalf("without files: %+v, %v", settings, err)
	}
	if err := DisableProject(project); err != nil {
		t.Fatal(err)
	}
	if settings, err = LoadSettings(home, project); err != nil || settings.Project.On() || !settings.User.On() {
		t.Fatalf("after disabling the project: %+v, %v", settings, err)
	}
	if err := EnableProject(project); err != nil {
		t.Fatal(err)
	}
	if err := EnableProject(project); err == nil {
		t.Fatal("enabling a project that is not disabled should say so")
	}
	if err := os.WriteFile(UserConfigPath(home), []byte("enabled: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if settings, err = LoadSettings(home, project); err != nil || settings.User.On() || !settings.Project.On() {
		t.Fatalf("after disabling the host user: %+v, %v", settings, err)
	}
	if err := os.WriteFile(UserConfigPath(home), []byte("enable: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSettings(home, project); err == nil {
		t.Fatal("a misspelled key should fail, not leave opeco on")
	}
}

func TestOpecoRemoveCommand(t *testing.T) {
	command := RemoveCommand()
	parts := strings.Split(command, " && ")
	if !strings.Contains(command, " | sudo tee /usr/local/share/isotty/mcp-merge.py ") {
		t.Fatalf("command = %q, want the merge script put in the VM first", command)
	}
	run := parts[len(parts)-1]
	script, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(run, "printf %s "), " | base64 -d | bash"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), `merge="/usr/local/share/isotty/mcp-merge.py"`) || strings.Contains(string(script), "__") {
		t.Fatalf("script:\n%s", script)
	}
}

func TestOpecoSetupCommand(t *testing.T) {
	command := SetupCommand([]string{"claude"}, 61002, false)
	parts := strings.Split(command, " && ")
	if !strings.Contains(command, "python3-tomli-w") || !strings.Contains(command, " | sudo tee /usr/local/share/isotty/mcp-merge.py ") {
		t.Fatalf("command = %q, want the merge script put in the VM first", command)
	}
	run := parts[len(parts)-1]
	prefix, suffix := "printf %s ", " | base64 -d | bash"
	if !strings.HasPrefix(run, prefix) || !strings.HasSuffix(run, suffix) {
		t.Fatalf("command = %q", command)
	}
	script, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(run, prefix), suffix))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`minimum_version="` + minimumVersion + `"`,
		"/releases/latest",
		`qr_port="61002"`,
		`install_claude="1"`,
		`install_codex="0"`,
		"sha256sum --ignore-missing -c checksums.txt",
		"sudo install -m 0755",
		`env="{\"BROWSER\": \"$open_client\"}"`,
		`merge="/usr/local/share/isotty/mcp-merge.py"`,
		`install_skill "$HOME/.claude/skills"`,
		`install_skill "$HOME/.codex/skills"`,
	} {
		if !strings.Contains(string(script), want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(string(script), "__") {
		t.Fatalf("script has an unrendered placeholder:\n%s", script)
	}
}

// The reminder reaches an agent as the hook its runtime runs when a request
// arrives: opeco's tool for that agent, with the words to say while no
// session is running.
func TestPairingHooksPassTheReminderToTheAgentsHookTool(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		var parsed struct {
			Hooks map[string][]struct {
				Hooks []struct {
					Type, Server, Tool string
					Input              map[string]string
				}
			}
		}
		if err := json.Unmarshal([]byte(pairingHooks(agent)), &parsed); err != nil {
			t.Fatal(err)
		}
		if len(parsed.Hooks) != 1 || len(parsed.Hooks["UserPromptSubmit"]) != 1 || len(parsed.Hooks["UserPromptSubmit"][0].Hooks) != 1 {
			t.Fatalf("%s: hooks = %+v, want one hook on UserPromptSubmit alone", agent, parsed.Hooks)
		}
		hook := parsed.Hooks["UserPromptSubmit"][0].Hooks[0]
		if hook.Type != "mcp_tool" || hook.Server != "opeco-link" || hook.Tool != agent+"_hook_check" {
			t.Fatalf("%s: hook = %+v", agent, hook)
		}
		if hook.Input["event"] != "UserPromptSubmit" || !strings.Contains(hook.Input["without_session"], "session_create") {
			t.Fatalf("%s: input = %+v", agent, hook.Input)
		}
	}
}

// The session is kept by default; either file turns that off, and neither
// turns it back on over the other.
func TestPersistIsTheDefault(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	if settings, err := LoadSettings(home, project); err != nil || !settings.Persist() {
		t.Fatalf("default: %+v %v", settings, err)
	}
	if err := os.MkdirAll(filepath.Dir(ProjectConfigPath(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ProjectConfigPath(project), []byte("persist: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if settings, err := LoadSettings(home, project); err != nil || settings.Persist() {
		t.Fatalf("project off: %+v %v", settings, err)
	}
	if err := os.WriteFile(UserConfigPath(home), []byte("persist: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if settings, _ := LoadSettings(home, project); settings.Persist() {
		t.Fatal("the host's file turned back on what the project's turned off")
	}
	if err := os.Remove(ProjectConfigPath(project)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(UserConfigPath(home), []byte("persist: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if settings, _ := LoadSettings(home, project); settings.Persist() {
		t.Fatal("the host's off was not taken")
	}
}

func TestSetupKeepsTheSessionOnlyWhenAsked(t *testing.T) {
	decoded := func(command string) string {
		t.Helper()
		parts := strings.Split(command, "printf %s ")
		script, err := base64.StdEncoding.DecodeString(strings.Fields(parts[len(parts)-1])[0])
		if err != nil {
			t.Fatal(err)
		}
		return string(script)
	}
	on := decoded(SetupCommand([]string{"claude"}, 61002, true))
	for _, want := range []string{`persist="1"`, `session_dir="` + SessionDir + `"`, `"$HOME/.opeco/config.json"`, "isotty-opeco-boot.service"} {
		if !strings.Contains(on, want) {
			t.Errorf("setup with persist lacks %s", want)
		}
	}
	off := decoded(SetupCommand([]string{"claude"}, 61002, false))
	if !strings.Contains(off, `persist="0"`) || !strings.Contains(off, `"$session_client" close`) {
		t.Errorf("setup without persist does not take the session away:\n%s", off)
	}
	if !strings.Contains(decoded(RemoveCommand()), `"$session_client" close`) {
		t.Error("remove leaves the session")
	}
}

// The client does nothing without a session, and records instead of failing
// when opeco cannot send.
func TestSessionClient(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	log := filepath.Join(dir, "events.jsonl")
	called := filepath.Join(dir, "called")
	for name, content := range map[string]string{
		"opeco": "#!/bin/sh\necho \"$@\" >> " + called + "\necho 'session_expired' >&2\nexit 1\n",
		"sudo":  "#!/bin/sh\nexec \"$@\"\n",
	} {
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sessions := filepath.Join(dir, "sessions")
	script := strings.NewReplacer("__SESSION_DIR__", sessions, "__EVENT_LOG__", log).Replace(sessionScript)
	path := filepath.Join(dir, "isotty-opeco-session")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func() {
		t.Helper()
		cmd := exec.Command(path, "status", "VM started")
		cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	run()
	if _, err := os.Stat(called); err == nil {
		t.Fatal("opeco ran without a session")
	}
	if err := os.MkdirAll(filepath.Join(sessions, "opeco-session-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessions, "opeco-session-1", "state.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	run()
	if data, _ := os.ReadFile(called); string(data) != "status VM started\n" {
		t.Fatalf("opeco got %q", data)
	}
	if data, err := os.ReadFile(log); err != nil || !strings.Contains(string(data), `"type":"opeco-status"`) || !strings.Contains(string(data), "session_expired") {
		t.Fatalf("event log: %s %v", data, err)
	}
}
