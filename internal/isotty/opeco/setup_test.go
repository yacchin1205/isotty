package opeco

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestOpecoActiveNeedsAnAgent(t *testing.T) {
	if Active(nil) {
		t.Fatal("opeco should be inactive without agents")
	}
	if !Active([]string{"claude"}) {
		t.Fatal("opeco should be active with an agent")
	}
}

func TestOpecoSetupCommand(t *testing.T) {
	command := SetupCommand([]string{"claude"}, 61002)
	prefix, suffix := "printf %s ", " | base64 -d | bash"
	if !strings.HasPrefix(command, prefix) || !strings.HasSuffix(command, suffix) {
		t.Fatalf("command = %q", command)
	}
	script, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(command, prefix), suffix))
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
