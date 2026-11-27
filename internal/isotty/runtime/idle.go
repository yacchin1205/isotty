package runtimecfg

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
)

// The VM powers itself off once no agent has had work for a while, so a VM
// the human walked away from stops costing compute time without the host,
// which may be asleep or gone, having to be there. A guest power-off leaves
// the instance as `isotty stop` does, with its disk; the next `up` starts
// it and makes the sync session again.
const (
	IdleClientPath  = "/usr/local/bin/isotty-idle"
	IdleStateDir    = "/var/lib/isotty/idle"
	IdleConfigPath  = "/etc/isotty/idle-stop.json"
	idleLastWork    = "/var/lib/isotty/idle-last-work.json"
	idleServicePath = "/etc/systemd/system/isotty-idle.service"
	idleTimerPath   = "/etc/systemd/system/isotty-idle.timer"
)

//go:embed assets/isotty-idle.py
var idleClient string

const idleService = `# Written by isotty up.
[Unit]
Description=Power the VM off once no agent has had work for a while (isotty)

[Service]
Type=oneshot
ExecStart=` + IdleClientPath + ` check
`

const idleTimer = `# Written by isotty up.
[Unit]
Description=Check every minute whether an agent has had work (isotty)

[Timer]
OnBootSec=1min
OnUnitActiveSec=1min
AccuracySec=10s

[Install]
WantedBy=timers.target
`

// IdleStopCommand installs isotty-idle, its timer, the time without work
// after which the VM powers off (0 never), and each agent's hooks that tell
// isotty-idle when a session has work. Writing the time counts as work, so
// the VM does not power off right after an up.
func IdleStopCommand(agents []string, after time.Duration, opecoSession, opecoDir string) string {
	config, err := json.Marshal(map[string]int{"seconds": int(after.Seconds())})
	if err != nil {
		panic(err)
	}
	parts := append([]string{"set -euo pipefail"}, gateway.MergeScriptCommands()...)
	parts = append(parts,
		gateway.WriteFileCommand(IdleClientPath, renderIdleClient(idleClient, opecoSession, opecoDir), true), "sudo chmod 755 "+IdleClientPath,
		fmt.Sprintf(`sudo install -d -m 755 -o "$(id -un)" -g "$(id -gn)" %s`, IdleStateDir),
		"sudo mkdir -p /etc/isotty",
		gateway.WriteFileCommand(IdleConfigPath, string(config)+"\n", true),
		gateway.WriteFileCommand(idleServicePath, idleService, true),
		gateway.WriteFileCommand(idleTimerPath, idleTimer, true),
		"sudo systemctl daemon-reload",
		"sudo systemctl enable --now isotty-idle.timer",
	)
	for _, agent := range agents {
		file, hooks := idleHooks(agent)
		tmp := "/tmp/isotty-idle-hooks-" + agent + ".json"
		parts = append(parts,
			fmt.Sprintf(`printf %%s %s | base64 -d > %s`, base64.StdEncoding.EncodeToString(hooks), tmp),
			fmt.Sprintf("mkdir -p \"$(dirname %s)\" && python3 %s hooks %s %s '%s'", file, gateway.MergeScriptPath, file, tmp, idleHookCommand(agent)),
			"rm "+tmp)
	}
	return strings.Join(parts, " && ")
}

func idleHookCommand(agent string) string {
	return IdleClientPath + " hook " + agent
}

// idleHooks are the events after which a session's work changes, in the
// shape Claude Code and Codex both read, and the file each reads them from.
// A turn the human interrupts ends without Stop; Claude Code's idle prompt
// and Codex's Interrupt say so.
func idleHooks(agent string) (string, []byte) {
	type hook struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	type group struct {
		Matcher string `json:"matcher,omitempty"`
		Hooks   []hook `json:"hooks"`
	}
	// Codex allows Interrupt and SessionEnd hooks three seconds at most.
	command := []hook{{Type: "command", Command: idleHookCommand(agent), Timeout: 3}}
	events := map[string][]group{
		"UserPromptSubmit": {{Hooks: command}},
		"Stop":             {{Hooks: command}},
		"SessionEnd":       {{Hooks: command}},
	}
	var file string
	switch agent {
	case "claude":
		file = `"$HOME"/.claude/settings.json`
		events["Notification"] = []group{{Matcher: "idle_prompt", Hooks: command}}
	case "codex":
		file = `"$HOME"/.codex/hooks.json`
		events["Interrupt"] = []group{{Hooks: command}}
	default:
		panic(fmt.Sprintf("unsupported agent %q", agent))
	}
	data, err := json.Marshal(map[string]map[string][]group{"hooks": events})
	if err != nil {
		panic(err)
	}
	return file, data
}

func renderIdleClient(template, opecoSession, opecoDir string) string {
	return strings.NewReplacer(
		"__ISOTTY_IDLE_STATE__", IdleStateDir,
		"__ISOTTY_IDLE_CONFIG__", IdleConfigPath,
		"__ISOTTY_IDLE_LAST_WORK__", idleLastWork,
		"__ISOTTY_EVENTS__", vmEventLogPath,
		"__ISOTTY_KERNEL_STAT__", "/proc/stat",
		"__ISOTTY_OPECO_SESSION__", opecoSession,
		"__ISOTTY_OPECO_DIR__", opecoDir,
	).Replace(template)
}
