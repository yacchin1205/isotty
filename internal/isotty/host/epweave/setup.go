package epweave

import (
	_ "embed"
	"strconv"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
	runtimecfg "github.com/yazawa/isotty/internal/isotty/runtime"
)

//go:embed assets/isotty-notes.py
var notesClientScript string

//go:embed assets/SKILL.md
var notesSkill string

const (
	ClientPath = "/usr/local/bin/isotty-notes"
	SkillName  = "isotty-notes"
)

// SetupCommand installs the VM-side client, the sshd keepalive and, for
// each configured agent, the skill describing the client. It runs on
// every `isotty up`, so enabling ep_weave on an existing VM works too.
func SetupCommand(cfg ProjectConfig, agents []string) string {
	parts := []string{
		"set -euo pipefail",
		gateway.WriteFileCommand(ClientPath, render(notesClientScript, cfg), true),
		"sudo chmod 755 " + ClientPath,
	}
	parts = append(parts, gateway.KeepaliveCommands()...)
	parts = append(parts, gateway.SkillCommands(runtimecfg.AgentSkillDirs(agents), SkillName, render(notesSkill, cfg))...)
	return strings.Join(parts, " && ")
}

func render(template string, cfg ProjectConfig) string {
	return strings.NewReplacer(
		"__ISOTTY_GATEWAY_PORT__", strconv.Itoa(gateway.VMPort),
		"__ISOTTY_EPWEAVE_PREFIX__", cfg.TitlePrefix(),
	).Replace(template)
}
