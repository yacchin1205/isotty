// Package opeco puts opeco into the VM for the configured agents and gives
// the host side what that needs: the per-project QR port. The QR page
// opens on the host through `isotty-open`, which the browser package
// installs as the VM's BROWSER.
package opeco

import (
	_ "embed"
	"encoding/base64"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/browser"
)

// minimumVersion is the oldest opeco-link release whose CLI offers what
// the setup relies on: `--qr-port` and `BROWSER`. The VM gets the latest
// release, which is the one opeco keeps compatible with its service.
const minimumVersion = "2026.10.2"

//go:embed assets/opeco-setup.sh
var setupScript string

//go:embed assets/opeco-merge.py
var mergeScript string

// Active reports whether the VM gets opeco: always, once there is an agent
// to use it.
func Active(agents []string) bool {
	return len(agents) > 0
}

// SetupCommand installs the latest opeco release, registers it as the
// `opeco-link` MCP server of each configured agent with the agent hooks that
// opeco publishes and `isotty-open` as its BROWSER, and installs opeco's
// skill. qrPort is the port the project state holds for opeco. The script
// travels base64-encoded so no shell quoting applies to it.
func SetupCommand(agents []string, qrPort int) string {
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
		"__MERGE_SCRIPT_B64__", base64.StdEncoding.EncodeToString([]byte(mergeScript)),
	).Replace(setupScript)
	return fmt.Sprintf("printf %%s %s | base64 -d | bash", base64.StdEncoding.EncodeToString([]byte(script)))
}
