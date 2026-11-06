package opeco

import (
	"encoding/base64"
	"os"
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
	command := SetupCommand([]string{"claude"}, 61002)
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
