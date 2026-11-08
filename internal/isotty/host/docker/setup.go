package docker

import (
	_ "embed"
	"strconv"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
	runtimecfg "github.com/yazawa/isotty/internal/isotty/runtime"
)

//go:embed assets/isotty-docker.py
var clientScript string

//go:embed assets/SKILL.md
var skill string

const (
	ClientPath = "/usr/local/bin/isotty-docker"
	SkillName  = "isotty-docker"
)

// SetupCommand installs the VM-side client, the sshd keepalive and, for
// each configured agent, the skill describing the client. It runs on
// every `isotty up`. Docker itself is the project's `docker` service.
func SetupCommand(agents []string) string {
	parts := []string{
		"set -euo pipefail",
		gateway.WriteFileCommand(ClientPath, strings.ReplaceAll(clientScript, "__ISOTTY_GATEWAY_PORT__", strconv.Itoa(gateway.VMPort)), true),
		"sudo chmod 755 " + ClientPath,
	}
	parts = append(parts, gateway.KeepaliveCommands()...)
	parts = append(parts, gateway.SkillCommands(runtimecfg.AgentSkillDirs(agents), SkillName, skill)...)
	return strings.Join(parts, " && ")
}
