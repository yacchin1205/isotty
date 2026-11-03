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

const mergeScriptPath = "/usr/local/share/isotty/mcp-merge.py"

// MCPServerCommands register a streamable HTTP MCP server under `name` with
// each configured agent: Claude in ~/.claude.json, Codex in
// ~/.codex/config.toml. Codex's per-tool timeout is raised from its 60
// seconds to an hour, since a tool may wait for a human; Claude Code's
// default is longer still.
func MCPServerCommands(agents []string, name, url string) []string {
	parts := []string{
		"sudo mkdir -p " + path.Dir(mergeScriptPath),
		WriteFileCommand(mergeScriptPath, mergeScript, true),
	}
	for _, agent := range agents {
		switch agent {
		case "claude":
			parts = append(parts, fmt.Sprintf("python3 %s mcp-json-url \"$HOME\"/.claude.json %s %s", mergeScriptPath, name, url))
		case "codex":
			parts = append(parts, fmt.Sprintf("python3 %s mcp-toml-url \"$HOME\"/.codex/config.toml %s %s %s 'tool_timeout_sec = 3600'", mergeScriptPath, name, name, url))
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
