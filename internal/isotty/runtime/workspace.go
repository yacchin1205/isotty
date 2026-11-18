package runtimecfg

import "fmt"

// WorkspaceCheck holds when the workspace exists and the login user owns it.
func WorkspaceCheck(workspacePath string) string {
	return fmt.Sprintf(`[ -d %s ] && [ "$(stat -c %%U %s)" = "$USER" ]`, workspacePath, workspacePath)
}

func WorkspaceCommand(workspacePath string) string {
	return fmt.Sprintf(`set -euo pipefail && sudo mkdir -p %s && sudo chown "$USER":"$(id -gn)" %s`, workspacePath, workspacePath)
}
