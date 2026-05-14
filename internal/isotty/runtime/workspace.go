package runtimecfg

func WorkspaceBootstrapPrefix(workspacePath string) []string {
	return []string{
		"set -euo pipefail",
		"export DEBIAN_FRONTEND=noninteractive",
		"sudo mkdir -p " + workspacePath,
		"sudo chown \"$USER\":\"$(id -gn)\" " + workspacePath,
	}
}
