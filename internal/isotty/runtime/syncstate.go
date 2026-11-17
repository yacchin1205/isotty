package runtimecfg

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
)

// SyncStatePath is where the VM is told how the sync of /workspace stands,
// outside what is synced.
const SyncStatePath = "/var/lib/isotty/sync.json"

// SyncInterval is how often the attach that holds the gateway writes the
// state; a state not written for three intervals is taken to mean that no
// such attach runs.
const SyncInterval = 30 * time.Second

const (
	SyncClientPath = "/usr/local/bin/isotty-sync"
	syncSkillName  = "isotty-sync"
)

//go:embed assets/isotty-sync.py
var syncClient string

//go:embed assets/isotty-sync-skill.md
var syncSkill string

// SyncStateCommand writes the state its stdin carries, whole or not at all.
func SyncStateCommand() string {
	next := SyncStatePath + ".next"
	return fmt.Sprintf("sudo mkdir -p %s && sudo sh -c 'cat > %s && chmod 644 %s && mv %s %s'", path.Dir(SyncStatePath), next, next, next, SyncStatePath)
}

// SyncClientCommand installs isotty-sync, its skill and, for each agent, the
// prompt hook through which the agent hears that the sync is not done.
func SyncClientCommand(agents []string) string {
	parts := append([]string{"set -euo pipefail"}, gateway.MergeScriptCommands()...)
	parts = append(parts, gateway.WriteFileCommand(SyncClientPath, renderSyncClient(syncClient), true), "sudo chmod 755 "+SyncClientPath)
	parts = append(parts, gateway.SkillCommands(AgentSkillDirs(agents), syncSkillName, syncSkill)...)
	if len(agents) == 0 {
		return strings.Join(parts, " && ")
	}
	hook := syncHookCommand()
	settings := map[string]string{"claude": `"$HOME"/.claude/settings.json`, "codex": `"$HOME"/.codex/hooks.json`}
	hooks := fmt.Sprintf(`printf %%s %s | base64 -d > /tmp/isotty-sync-hooks.json`, base64.StdEncoding.EncodeToString(syncHooks(hook)))
	parts = append(parts, hooks)
	for _, agent := range agents {
		file, ok := settings[agent]
		if !ok {
			panic(fmt.Sprintf("unsupported agent %q", agent))
		}
		parts = append(parts, fmt.Sprintf("mkdir -p \"$(dirname %s)\" && python3 %s hooks %s /tmp/isotty-sync-hooks.json '%s'", file, gateway.MergeScriptPath, file, hook))
	}
	return strings.Join(append(parts, "rm /tmp/isotty-sync-hooks.json"), " && ")
}

func syncHookCommand() string {
	return SyncClientPath + " hook"
}

// syncHooks is the hook in the shape Claude Code and Codex both read; the
// merge script knows it as its own by the command.
func syncHooks(command string) []byte {
	type hook struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	type group struct {
		Hooks []hook `json:"hooks"`
	}
	data, err := json.Marshal(map[string]map[string][]group{
		"hooks": {"UserPromptSubmit": {{Hooks: []hook{{Type: "command", Command: command, Timeout: 5}}}}},
	})
	if err != nil {
		panic(err)
	}
	return data
}

func renderSyncClient(template string) string {
	interval := int(SyncInterval.Seconds())
	return strings.NewReplacer(
		"__ISOTTY_SYNC_STATE__", SyncStatePath,
		"__ISOTTY_SYNC_INTERVAL__", strconv.Itoa(interval),
		"__ISOTTY_SYNC_STALE__", strconv.Itoa(3*interval),
	).Replace(template)
}
