package runtimecfg

import (
	"strings"
	"testing"
)

func TestNotifyCommandRingsTheBellForEachAgent(t *testing.T) {
	if NotifyCommand(nil) != "" {
		t.Fatal("nothing to configure without an agent")
	}
	command := NotifyCommand([]string{"claude", "codex"})
	for _, want := range []string{
		`json-default "$HOME"/.claude/settings.json preferredNotifChannel '"terminal_bell"'`,
		`toml-default "$HOME"/.codex/config.toml tui notification_method '"bel"'`,
		`toml-default "$HOME"/.codex/config.toml tui notification_condition '"always"'`,
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("NotifyCommand() missing %q:\n%s", want, command)
		}
	}
	if only := NotifyCommand([]string{"claude"}); strings.Contains(only, ".codex") {
		t.Fatalf("an agent that is not configured must not get a config file:\n%s", only)
	}
}
