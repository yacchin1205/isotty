package runtimecfg

import (
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
)

// NotifyCommand makes each configured agent ring the terminal bell when it
// wants the human: the bell character is the one signal that passes ssh and
// tmux to whatever terminal the human attached from, where it shows as the
// tab's badge, a bounce or a sound as that terminal has it. Left to itself
// each agent rings nothing in a VM. Claude Code's automatic choice asks the
// terminal program, which ssh does not forward, and on Apple Terminal reads
// the host's profile, which the VM cannot. Codex rings only while the
// terminal is unfocused and starts out focused; it learns otherwise from
// focus events, which tmux does not forward and Apple Terminal never sends.
// A value the human set in the agent's own settings, which travel with the
// saved login state, is kept.
func NotifyCommand(agents []string) string {
	if len(agents) == 0 {
		return ""
	}
	parts := append([]string{"set -euo pipefail"}, gateway.MergeScriptCommands()...)
	for _, agent := range agents {
		switch agent {
		case "claude":
			parts = append(parts, gateway.DefaultSettingCommand(agent, "preferredNotifChannel", `"terminal_bell"`))
		case "codex":
			parts = append(parts,
				gateway.DefaultSettingCommand(agent, "notification_method", `"bel"`),
				gateway.DefaultSettingCommand(agent, "notification_condition", `"always"`))
		}
	}
	return strings.Join(parts, " && ")
}
