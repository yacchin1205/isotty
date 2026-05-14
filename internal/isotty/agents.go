package isotty

import (
	"fmt"
	"strings"
)

const defaultNodeMajorVersion = "24"

var agentPackages = map[string]string{
	"claude": "@anthropic-ai/claude-code",
	"codex":  "@openai/codex",
}

func needsNodeRuntime(state State) bool {
	return state.NodeVersion != "" || len(state.Agents) > 0
}

func resolvedNodeVersion(state State) string {
	if state.NodeVersion != "" {
		return state.NodeVersion
	}
	return defaultNodeMajorVersion
}

func buildNodeInstallScript(state State) string {
	return fmt.Sprintf(`NODE_MAJOR=%s
if [ ! -f /etc/apt/sources.list.d/nodesource.list ] || ! grep -q "node_${NODE_MAJOR}\.x" /etc/apt/sources.list.d/nodesource.list; then
  curl -fsSL "https://deb.nodesource.com/setup_${NODE_MAJOR}.x" -o /tmp/nodesource_setup.sh
  sudo -E bash /tmp/nodesource_setup.sh
fi
sudo apt-get install -y nodejs`, shellJoin([]string{resolvedNodeVersion(state)}))
}

func buildAgentInstallScript(state State) (string, error) {
	if len(state.Agents) == 0 {
		return "", nil
	}

	packages := make([]string, 0, len(state.Agents))
	for _, agent := range state.Agents {
		pkg, ok := agentPackages[agent]
		if !ok {
			return "", fmt.Errorf("unsupported agent %q", agent)
		}
		packages = append(packages, pkg)
	}

	return fmt.Sprintf("sudo env PATH=/usr/local/bin:$PATH npm install -g %s", shellJoin(packages)), nil
}

func bootstrapLabel(state State) string {
	parts := []string{"Bootstrapping workspace"}
	if len(state.AptPackages) > 0 {
		parts = append(parts, "installing packages")
	}
	if needsNodeRuntime(state) {
		parts = append(parts, "installing Node.js")
	}
	if len(state.Agents) > 0 {
		parts = append(parts, "installing agents")
	}
	return strings.Join(parts, " and ")
}
