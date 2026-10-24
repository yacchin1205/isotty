package epweave

import (
	_ "embed"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/gateway"
)

//go:embed assets/isotty-notes.py
var notesClientScript string

//go:embed assets/SKILL.md
var notesSkill string

const (
	ClientPath     = "/usr/local/bin/isotty-notes"
	ClaudeSkillDir = ".claude/skills/isotty-notes"
	// sshd keeps the first value it reads and includes sshd_config.d/*.conf
	// in lexical order; the cloud image's 50-cloudimg-settings.conf sets
	// ClientAliveInterval 120, so this file must sort before it.
	sshdConfigPath = "/etc/ssh/sshd_config.d/00-isotty.conf"
	// Until sshd notices the dead peer it keeps the session's -R listener,
	// and the VM client hangs on it instead of failing.
	sshdKeepalive = "ClientAliveInterval 15\nClientAliveCountMax 3\n"
)

// SetupCommand installs the VM-side client, the sshd keepalive and, for
// projects that run Claude, the skill describing the client. It runs on
// every `isotty up`, so enabling ep_weave on an existing VM works too.
func SetupCommand(cfg ProjectConfig, installClaudeSkill bool) string {
	parts := []string{
		"set -euo pipefail",
		writeFileCommand(ClientPath, render(notesClientScript, cfg), true),
		"sudo chmod 755 " + ClientPath,
		writeFileCommand(sshdConfigPath, sshdKeepalive, true),
		"sudo systemctl reload ssh",
	}
	if installClaudeSkill {
		parts = append(parts,
			"mkdir -p \"$HOME\"/"+ClaudeSkillDir,
			writeFileCommand("\"$HOME\"/"+ClaudeSkillDir+"/SKILL.md", render(notesSkill, cfg), false),
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

// writeFileCommand ships the content as base64 so no shell quoting applies to it.
func writeFileCommand(path, content string, sudo bool) string {
	tee := "tee"
	if sudo {
		tee = "sudo tee"
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	return fmt.Sprintf("printf %%s %s | base64 -d | %s %s >/dev/null", encoded, tee, path)
}
