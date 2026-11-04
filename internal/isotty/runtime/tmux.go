package runtimecfg

import (
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
)

const tmuxConfPath = "/etc/tmux.conf"

// tmuxConf is read by every tmux server in the VM before the human's own
// ~/.tmux.conf. tmux relays an OSC 8 hyperlink only to a terminal it knows
// takes them, which it tells from TERM_PROGRAM, and ssh does not carry that
// variable; so a link Codex marks up would stop at tmux. The client is
// always the human's terminal, which takes the sequence or ignores it.
const tmuxConf = `# Written by isotty up.
# Relay OSC 8 hyperlinks to the attached terminal: tmux would otherwise keep
# them, since ssh does not tell it which terminal program that is.
set -as terminal-features '*:hyperlinks'
`

// TmuxSetupCommand installs tmux when the image lacks it and writes the
// configuration above; `attach` runs its session in tmux unless the host
// turns that off.
func TmuxSetupCommand() string {
	return strings.Join([]string{
		"command -v tmux >/dev/null 2>&1 || (sudo apt-get update -q && sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -q tmux)",
		gateway.WriteFileCommand(tmuxConfPath, tmuxConf, true),
	}, " && ")
}
