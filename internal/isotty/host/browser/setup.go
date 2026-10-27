package browser

import (
	_ "embed"
	"strconv"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
	runtimecfg "github.com/yazawa/isotty/internal/isotty/runtime"
)

//go:embed assets/isotty-open.py
var clientScript string

//go:embed assets/SKILL.md
var skill string

const (
	ClientPath = "/usr/local/bin/isotty-open"
	SkillName  = "isotty-open"
)

// SetupCommand installs `isotty-open` and the skill for each configured
// agent. It runs on every `isotty up`; `isotty attach` names the client as
// BROWSER in the session it opens.
func SetupCommand(agents []string) string {
	parts := []string{
		"set -euo pipefail",
		gateway.WriteFileCommand(ClientPath, render(clientScript), true),
		"sudo chmod 755 " + ClientPath,
	}
	parts = append(parts, gateway.SkillCommands(runtimecfg.AgentSkillDirs(agents), SkillName, skill)...)
	return strings.Join(parts, " && ")
}

func render(template string) string {
	return strings.ReplaceAll(template, "__ISOTTY_GATEWAY_PORT__", strconv.Itoa(gateway.VMPort))
}
