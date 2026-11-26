// Package opeco puts opeco into the VM for the configured agents and gives
// the host side what that needs: the per-project QR port. The QR page
// opens on the host through `isotty-open`, which the browser package
// installs as the VM's BROWSER.
package opeco

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/browser"
	"github.com/yazawa/isotty/internal/isotty/host/gateway"
)

// minimumVersion is the oldest opeco-link release whose CLI offers what
// the setup relies on: `--qr-port`, `BROWSER`, hook tools that take the
// words for an agent without a session, and `session_dir`. The VM gets the
// latest release, which is the one opeco keeps compatible with its service.
const minimumVersion = "2026.11.1"

// SessionDir keeps the VM's one opeco session when the host asks for it
// (persist in ~/.isotty/opeco.yaml): on the VM's disk, outside the synced
// workspace and the agents' saved homes, so it lives and dies with the VM.
const SessionDir = "/var/lib/isotty/opeco"

// SessionClientPath tells the session that the VM starts or stops, or
// closes it; see assets/isotty-opeco-session.sh.
const SessionClientPath = "/usr/local/bin/isotty-opeco-session"

// unpersist closes the kept session and removes what keeping it installed,
// for a VM whose host no longer asks for it.
const unpersist = `if [ -x "$session_client" ]; then "$session_client" close; fi
rm -f "$HOME/.opeco/config.json"
sudo rm -rf "$session_dir" "$session_client"
if [ -f "$boot_unit" ]; then sudo systemctl disable isotty-opeco-boot.service; sudo rm -f "$boot_unit"; sudo systemctl daemon-reload; fi`

// pairingReminder is what an agent is told as a request arrives while it
// has no opeco session. It excuses only a quick question: asked to judge by
// how long the work will take, agents rate their own work short and skip
// pairing for nearly every request.
const pairingReminder = "opeco.link: no session is running. Unless this request is only a quick question, call session_create now, while the person is here to pair. Do not ask again once they have declined."

// pairingHooks is the hook IsoTTY adds beside the ones opeco publishes.
// opeco's own stay silent without a session, since pairing unasked is not
// for everyone who uses opeco; in an IsoTTY VM the agent is left to work
// while the person is away, so it is. The reminder comes with the request
// because pairing takes the person, and that is when they are there.
func pairingHooks(agent string) string {
	type hook struct {
		Type    string            `json:"type"`
		Server  string            `json:"server"`
		Tool    string            `json:"tool"`
		Input   map[string]string `json:"input"`
		Timeout int               `json:"timeout"`
	}
	type group struct {
		Hooks []hook `json:"hooks"`
	}
	data, err := json.Marshal(map[string]map[string][]group{
		"hooks": {"UserPromptSubmit": {{Hooks: []hook{{
			Type:    "mcp_tool",
			Server:  "opeco-link",
			Tool:    agent + "_hook_check",
			Input:   map[string]string{"event": "UserPromptSubmit", "without_session": pairingReminder},
			Timeout: 3,
		}}}}},
	})
	if err != nil {
		panic(err)
	}
	return string(data)
}

//go:embed assets/opeco-setup.sh
var setupScript string

//go:embed assets/opeco-remove.sh
var removeScript string

//go:embed assets/isotty-opeco-session.sh
var sessionScript string

// Active reports whether the VM gets opeco: unless it is turned off, once
// there is an agent to use it.
func Active(agents []string, settings Settings) bool {
	return len(agents) > 0 && settings.Enabled()
}

// SetupCommand installs the latest opeco release, registers it as the
// `opeco-link` MCP server of each configured agent with the agent hooks that
// opeco publishes, IsoTTY's pairing reminder and `isotty-open` as its
// BROWSER, and installs opeco's skill. qrPort is the port the project state holds for opeco. The script
// travels base64-encoded so no shell quoting applies to it, after the
// commands that put the merge script it runs in the VM.
func SetupCommand(agents []string, qrPort int, persist bool) string {
	flag := func(agent string) string {
		if slices.Contains(agents, agent) {
			return "1"
		}
		return "0"
	}
	script := strings.NewReplacer(
		"__OPECO_MINIMUM_VERSION__", minimumVersion,
		"__OPECO_QR_PORT__", strconv.Itoa(qrPort),
		"__OPEN_CLIENT__", browser.ClientPath,
		"__INSTALL_CLAUDE__", flag("claude"),
		"__INSTALL_CODEX__", flag("codex"),
		"__CLAUDE_PAIRING_HOOKS__", base64.StdEncoding.EncodeToString([]byte(pairingHooks("claude"))),
		"__CODEX_PAIRING_HOOKS__", base64.StdEncoding.EncodeToString([]byte(pairingHooks("codex"))),
		"__MERGE_SCRIPT__", gateway.MergeScriptPath,
		"__PERSIST__", boolFlag(persist),
		"__SESSION_SCRIPT__", base64.StdEncoding.EncodeToString([]byte(renderSession(sessionScript))),
		"__UNPERSIST__", unpersist,
		"__SESSION_DIR__", SessionDir,
		"__SESSION_CLIENT__", SessionClientPath,
	).Replace(setupScript)
	return runWithMergeScript(script)
}

// RemoveCommand takes out of the VM what SetupCommand put there, for either
// agent: the saved state of an agent brings the registration back into a
// new VM, so turning opeco off means removing it on every `up`.
func RemoveCommand() string {
	return runWithMergeScript(strings.NewReplacer(
		"__MERGE_SCRIPT__", gateway.MergeScriptPath,
		"__UNPERSIST__", unpersist,
		"__SESSION_DIR__", SessionDir,
		"__SESSION_CLIENT__", SessionClientPath,
	).Replace(removeScript))
}

func renderSession(template string) string {
	return strings.NewReplacer("__SESSION_DIR__", SessionDir, "__EVENT_LOG__", gateway.VMEventLogPath).Replace(template)
}

func boolFlag(on bool) string {
	if on {
		return "1"
	}
	return "0"
}

// SessionCommand runs isotty-opeco-session as the VM user, when the VM has
// it: status with the text, or close.
func SessionCommand(args ...string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'"'"'`) + "'"
	}
	return fmt.Sprintf("if [ -x %s ]; then %s %s; fi", SessionClientPath, SessionClientPath, strings.Join(quoted, " "))
}

func runWithMergeScript(script string) string {
	run := fmt.Sprintf("printf %%s %s | base64 -d | bash", base64.StdEncoding.EncodeToString([]byte(script)))
	return strings.Join(append(gateway.MergeScriptCommands(), run), " && ")
}
