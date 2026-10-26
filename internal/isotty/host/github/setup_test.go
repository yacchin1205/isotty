package github

import (
	"strings"
	"testing"
)

func TestSetupCommand(t *testing.T) {
	command := SetupCommand(true)
	for _, want := range []string{"set -euo pipefail", "command -v git", "sudo tee " + ClientPath, "sudo chmod 755 " + ClientPath, "sshd_config.d/00-isotty.conf", "systemctl reload ssh", ClaudeSkillDir + "/SKILL.md"} {
		if !strings.Contains(command, want) {
			t.Fatalf("SetupCommand() missing %q:\n%s", want, command)
		}
	}
	if strings.Contains(SetupCommand(false), "SKILL.md") {
		t.Fatal("SetupCommand() should not install the skill without Claude")
	}
	rendered := render(clientScript)
	if !strings.Contains(rendered, "http://127.0.0.1:61001/github") || strings.Contains(rendered, "__ISOTTY_") {
		t.Fatalf("rendered client script not substituted:\n%s", rendered)
	}
	if strings.Contains(render(skill), "__ISOTTY_") {
		t.Fatal("rendered skill not substituted")
	}
}
