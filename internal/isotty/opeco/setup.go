// Package opeco puts opeco into the VM for the configured agents and gives
// the host side what that needs: the per-project QR port and the gateway
// service that opens the QR page in the host browser.
package opeco

import (
	_ "embed"
	"encoding/base64"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/gateway"
)

// minimumVersion is the oldest opeco-link release whose CLI offers what
// the setup relies on: `--qr-port` and `BROWSER`. The VM gets the latest
// release, which is the one opeco keeps compatible with its service.
const minimumVersion = "2026.10.2"

//go:embed assets/opeco-setup.sh
var setupScript string

//go:embed assets/opeco-merge.py
var mergeScript string

//go:embed assets/isotty-open.py
var openClientScript string

// Active reports whether the VM gets opeco: always, once there is an agent
// to use it.
func Active(agents []string) bool {
	return len(agents) > 0
}

// SetupCommand installs the latest opeco release, registers it as the
// `opeco-link` MCP server of each configured agent with the agent hooks that
// opeco publishes, installs opeco's skill for Claude, and installs
// `isotty-open`, which opeco runs as its BROWSER so the QR page opens on the
// host. qrPort is the port the project state holds for opeco. The script
// travels base64-encoded so no shell quoting applies to it.
func SetupCommand(agents []string, qrPort int) string {
	flag := func(agent string) string {
		if slices.Contains(agents, agent) {
			return "1"
		}
		return "0"
	}
	openClient := strings.ReplaceAll(openClientScript, "__ISOTTY_GATEWAY_PORT__", strconv.Itoa(gateway.VMPort))
	script := strings.NewReplacer(
		"__OPECO_MINIMUM_VERSION__", minimumVersion,
		"__OPECO_QR_PORT__", strconv.Itoa(qrPort),
		"__INSTALL_CLAUDE__", flag("claude"),
		"__INSTALL_CODEX__", flag("codex"),
		"__MERGE_SCRIPT_B64__", base64.StdEncoding.EncodeToString([]byte(mergeScript)),
		"__OPEN_CLIENT_B64__", base64.StdEncoding.EncodeToString([]byte(openClient)),
	).Replace(setupScript)
	return fmt.Sprintf("printf %%s %s | base64 -d | bash", base64.StdEncoding.EncodeToString([]byte(script)))
}
