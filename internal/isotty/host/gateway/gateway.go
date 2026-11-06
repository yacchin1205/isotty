// Package gateway serves host-side services to the VM for the duration of an
// attach session. The server listens on a host loopback port and `ssh -R`
// exposes it on the VM's loopback at VMPort; each service mounts under its
// own path prefix.
package gateway

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path"
	"time"
)

// VMPort is where the VM reaches the gateway. It sits above the Linux
// ephemeral range so it collides neither with services a project runs in the
// VM nor with outgoing connections.
const VMPort = 61001

type Gateway struct {
	listener net.Listener
	server   *http.Server
	served   chan error
}

func Serve(handler http.Handler) (*Gateway, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for host gateway: %w", err)
	}
	gateway := &Gateway{
		listener: listener,
		// The VM is not trusted; a connection that never sends its headers
		// must not hold a goroutine for the rest of the session.
		server: &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second},
		served: make(chan error, 1),
	}
	go func() {
		gateway.served <- gateway.server.Serve(listener)
	}()
	return gateway, nil
}

func (g *Gateway) Port() int {
	return g.listener.Addr().(*net.TCPAddr).Port
}

func (g *Gateway) Close() error {
	if err := g.server.Close(); err != nil {
		return fmt.Errorf("close host gateway: %w", err)
	}
	if err := <-g.served; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("host gateway: %w", err)
	}
	return nil
}

// WriteJSON answers a gateway request; every service answers in JSON, errors
// included, so the VM clients have one shape to parse.
func WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		panic(err)
	}
}

func WriteError(w http.ResponseWriter, status int, message string) {
	WriteJSON(w, status, map[string]string{"error": message})
}

const (
	// sshd keeps the first value it reads and includes sshd_config.d/*.conf
	// in lexical order; the cloud image's 50-cloudimg-settings.conf sets
	// ClientAliveInterval 120, so this file must sort before it.
	sshdConfigPath = "/etc/ssh/sshd_config.d/00-isotty.conf"
	// Until sshd notices the dead peer it keeps the session's -R listener,
	// and the VM clients hang on it instead of failing.
	sshdKeepalive = "ClientAliveInterval 15\nClientAliveCountMax 3\n"
)

// KeepaliveCommands make a dead attach session release the gateway's VM
// port within about 45 seconds; every service's setup runs them.
func KeepaliveCommands() []string {
	return []string{
		WriteFileCommand(sshdConfigPath, sshdKeepalive, true),
		"sudo systemctl reload ssh",
	}
}

// PortHeld is the exit code of PortFreeCommand when the port stayed taken.
const PortHeld = 75

// PortWait covers the time sshd takes to notice a dead session under
// KeepaliveCommands, with a margin.
const PortWait = 90 * time.Second

// portFree binds the port the way sshd does for `ssh -R`, so it succeeds
// exactly when an attach's reverse forward would.
const portFree = `import errno, socket, sys, time
port, wait, held = int(sys.argv[1]), float(sys.argv[2]), int(sys.argv[3])
deadline = time.monotonic() + wait
while True:
    with socket.socket() as s:
        s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        try:
            s.bind(("127.0.0.1", port))
            sys.exit(0)
        except OSError as e:
            if e.errno != errno.EADDRINUSE:
                raise
    if time.monotonic() >= deadline:
        sys.exit(held)
    time.sleep(0.5)
`

// PortFreeCommand succeeds once VMPort is free in the VM and exits with
// PortHeld when it is still taken after wait. A session whose host went
// away keeps the port until sshd notices, and the next attach cannot bind it
// until then.
func PortFreeCommand(wait time.Duration) string {
	return portFreeCommand(VMPort, wait)
}

func portFreeCommand(port int, wait time.Duration) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(portFree))
	return fmt.Sprintf("printf %%s %s | base64 -d | python3 - %d %g %d", encoded, port, wait.Seconds(), PortHeld)
}

// WriteFileCommand ships the content as base64 so no shell quoting applies to it.
func WriteFileCommand(path, content string, sudo bool) string {
	tee := "tee"
	if sudo {
		tee = "sudo tee"
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	return fmt.Sprintf("printf %%s %s | base64 -d | %s %s >/dev/null", encoded, tee, path)
}

//go:embed assets/mcp-merge.py
var mergeScript string

// MergeScript merges an MCP server into an agent's config; see its usage line.
func MergeScript() string {
	return mergeScript
}

// MergeScriptPath is where the merge script lives in the VM.
const MergeScriptPath = "/usr/local/share/isotty/mcp-merge.py"

// MergeScriptCommands put the merge script in the VM with the TOML writer
// it needs; the commands below, runtime.NotifyCommand and the opeco setup
// run it.
func MergeScriptCommands() []string {
	return []string{
		"dpkg -s python3-tomli-w >/dev/null 2>&1 || (sudo apt-get update -q && sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -q python3-tomli-w)",
		"sudo mkdir -p " + path.Dir(MergeScriptPath),
		WriteFileCommand(MergeScriptPath, mergeScript, true),
	}
}

// DefaultSettingCommand sets a key of an agent's config file unless the
// file already has it, so a value the human chose stays theirs.
func DefaultSettingCommand(agent, key, valueJSON string) string {
	switch agent {
	case "claude":
		return fmt.Sprintf("python3 %s json-default \"$HOME\"/.claude/settings.json %s '%s'", MergeScriptPath, key, valueJSON)
	case "codex":
		return fmt.Sprintf("python3 %s toml-default \"$HOME\"/.codex/config.toml tui %s '%s'", MergeScriptPath, key, valueJSON)
	}
	panic("unsupported agent " + agent)
}

// MCPServerCommands register a streamable HTTP MCP server under `name` with
// each configured agent: Claude in ~/.claude.json, Codex in
// ~/.codex/config.toml. Codex's per-tool timeout is raised from its 60
// seconds to an hour, since a tool may wait for a human; Claude Code's
// default is longer still.
func MCPServerCommands(agents []string, name, url string) []string {
	parts := MergeScriptCommands()
	for _, agent := range agents {
		switch agent {
		case "claude":
			parts = append(parts, fmt.Sprintf("python3 %s mcp-json-url \"$HOME\"/.claude.json %s %s", MergeScriptPath, name, url))
		case "codex":
			parts = append(parts, fmt.Sprintf("python3 %s mcp-toml-url \"$HOME\"/.codex/config.toml %s %s '{\"tool_timeout_sec\": 3600}'", MergeScriptPath, name, url))
		}
	}
	return parts
}

// SkillCommands install a skill for every agent whose skill directory is
// given (see runtime.AgentSkillDirs); each gets its own copy of SKILL.md.
func SkillCommands(skillDirs []string, name, content string) []string {
	var parts []string
	for _, dir := range skillDirs {
		target := "\"$HOME\"/" + dir + "/" + name
		parts = append(parts, "mkdir -p "+target, WriteFileCommand(target+"/SKILL.md", content, false))
	}
	return parts
}
