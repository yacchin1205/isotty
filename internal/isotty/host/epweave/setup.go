package epweave

import (
	_ "embed"
	"strconv"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
)

//go:embed assets/isotty-notes.py
var notesClientScript string

//go:embed assets/SKILL.md
var notesSkill string

const (
	ClientPath     = "/usr/local/bin/isotty-notes"
	ClaudeSkillDir = ".claude/skills/isotty-notes"
)

// SetupCommand installs the VM-side client, the sshd keepalive and, for
// projects that run Claude, the skill describing the client. It runs on
// every `isotty up`, so enabling ep_weave on an existing VM works too.
func SetupCommand(cfg ProjectConfig, installClaudeSkill bool) string {
	parts := []string{
		"set -euo pipefail",
		gateway.WriteFileCommand(ClientPath, render(notesClientScript, cfg), true),
		"sudo chmod 755 " + ClientPath,
	}
	parts = append(parts, gateway.KeepaliveCommands()...)
	if installClaudeSkill {
		parts = append(parts,
			"mkdir -p \"$HOME\"/"+ClaudeSkillDir,
			gateway.WriteFileCommand("\"$HOME\"/"+ClaudeSkillDir+"/SKILL.md", render(notesSkill, cfg), false),
		)
	}
	return strings.Join(parts, " && ")
}

func render(template string, cfg ProjectConfig) string {
	return strings.NewReplacer(
		"__ISOTTY_GATEWAY_PORT__", strconv.Itoa(gateway.VMPort),
		"__ISOTTY_EPWEAVE_PREFIX__", cfg.TitlePrefix(),
	).Replace(template)
}
