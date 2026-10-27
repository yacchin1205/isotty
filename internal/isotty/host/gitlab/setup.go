package gitlab

import (
	_ "embed"
	"strconv"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
	runtimecfg "github.com/yazawa/isotty/internal/isotty/runtime"
)

//go:embed assets/isotty-gitlab.py
var clientScript string

//go:embed assets/SKILL.md
var skill string

const (
	ClientPath = "/usr/local/bin/isotty-gitlab"
	SkillName  = "isotty-gitlab"
)

// SetupCommand installs the VM-side client, git if the image lacks it, the
// sshd keepalive and, for each configured agent, the skill describing the
// client. It runs on every `isotty up`.
func SetupCommand(agents []string) string {
	parts := []string{
		"set -euo pipefail",
		"command -v git >/dev/null || (sudo apt-get update && sudo apt-get install -y git)",
		gateway.WriteFileCommand(ClientPath, render(clientScript), true),
		"sudo chmod 755 " + ClientPath,
	}
	parts = append(parts, gateway.KeepaliveCommands()...)
	parts = append(parts, gateway.SkillCommands(runtimecfg.AgentSkillDirs(agents), SkillName, render(skill))...)
	return strings.Join(parts, " && ")
}

func render(template string) string {
	return strings.ReplaceAll(template, "__ISOTTY_GATEWAY_PORT__", strconv.Itoa(gateway.VMPort))
}
