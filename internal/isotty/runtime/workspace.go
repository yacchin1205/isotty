package runtimecfg

func WorkspaceBootstrapPrefix() []string {
	return []string{
		"set -euo pipefail",
		"export DEBIAN_FRONTEND=noninteractive",
		"sudo mkdir -p /workspace",
		"sudo chown \"$USER\":\"$(id -gn)\" /workspace",
	}
}
