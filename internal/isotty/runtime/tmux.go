package runtimecfg

// TmuxSetupCommand installs tmux when the image lacks it; `attach` runs its
// session in tmux unless the host turns that off.
func TmuxSetupCommand() string {
	return "command -v tmux >/dev/null 2>&1 || (sudo apt-get update -q && sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -q tmux)"
}
