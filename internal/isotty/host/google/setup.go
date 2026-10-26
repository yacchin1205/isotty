package google

import (
	_ "embed"
	"strconv"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
)

//go:embed assets/isotty-google.py
var clientScript string

//go:embed assets/SKILL.md
var skill string

const (
	ClientPath     = "/usr/local/bin/isotty-google"
	ClaudeSkillDir = ".claude/skills/isotty-google"
)

// SetupCommand installs the VM-side client, the sshd keepalive and, for
// projects that run Claude, the skill describing the client. It runs on
// every `isotty up`.
func SetupCommand(installClaudeSkill bool) string {
	parts := []string{
		"set -euo pipefail",
		gateway.WriteFileCommand(ClientPath, render(clientScript), true),
		"sudo chmod 755 " + ClientPath,
	}
	parts = append(parts, gateway.KeepaliveCommands()...)
	if installClaudeSkill {
		parts = append(parts,
			"mkdir -p \"$HOME\"/"+ClaudeSkillDir,
			gateway.WriteFileCommand("\"$HOME\"/"+ClaudeSkillDir+"/SKILL.md", render(skill), false),
		)
	}
	return strings.Join(parts, " && ")
}

func render(template string) string {
	return strings.ReplaceAll(template, "__ISOTTY_GATEWAY_PORT__", strconv.Itoa(gateway.VMPort))
}
