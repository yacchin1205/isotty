package wake

import (
	_ "embed"
	"strconv"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/browser"
	"github.com/yazawa/isotty/internal/isotty/host/gateway"
	runtimecfg "github.com/yazawa/isotty/internal/isotty/runtime"
)

//go:embed assets/isotty-wake.py
var clientScript string

//go:embed assets/SKILL.md
var skill string

const (
	ClientPath = "/usr/local/bin/isotty-wake"
	SkillName  = "isotty-wake"
)

// SetupCommand installs the VM-side client and, for each configured agent,
// its skill. It runs on every `isotty up`.
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
	return strings.NewReplacer(
		"__ISOTTY_GATEWAY_PORT__", strconv.Itoa(gateway.VMPort),
		"__ISOTTY_IDLE_STATE__", runtimecfg.IdleStateDir,
		"__ISOTTY_OPEN_CLIENT__", browser.ClientPath,
	).Replace(template)
}
