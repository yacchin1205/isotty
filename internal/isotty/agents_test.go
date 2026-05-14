package isotty

import (
	"strings"
	"testing"
)

func TestBuildAgentInstallScript(t *testing.T) {
	state := State{
		Agents: []string{"claude", "codex"},
	}

	command, err := buildAgentInstallScript(state)
	if err != nil {
		t.Fatalf("buildAgentInstallScript() error = %v", err)
	}
	if !strings.Contains(command, "@anthropic-ai/claude-code") {
		t.Fatalf("command = %q, want claude package", command)
	}
	if !strings.Contains(command, "@openai/codex") {
		t.Fatalf("command = %q, want codex package", command)
	}
}

func TestResolvedNodeVersion(t *testing.T) {
	if version := resolvedNodeVersion(State{}); version != defaultNodeMajorVersion {
		t.Fatalf("version = %q, want %q", version, defaultNodeMajorVersion)
	}
	if version := resolvedNodeVersion(State{NodeVersion: "22"}); version != "22" {
		t.Fatalf("version = %q, want 22", version)
	}
}

func TestBuildNodeInstallScript(t *testing.T) {
	script := buildNodeInstallScript(State{NodeVersion: "22"})
	if !strings.Contains(script, "https://deb.nodesource.com/setup_${NODE_MAJOR}.x") {
		t.Fatalf("script = %q, want NodeSource setup URL", script)
	}
	if !strings.Contains(script, "sudo apt-get install -y nodejs") {
		t.Fatalf("script = %q, want apt nodejs install", script)
	}
}
