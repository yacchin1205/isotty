//go:build e2e

package e2e

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/yazawa/isotty/internal/isotty/opeco"
)

const vmHome = "/home/" + vmUser

// opecoTraces are the places `up` leaves opeco in the VM; each command
// succeeds while its trace is there.
var opecoTraces = []string{
	"test -x /usr/local/bin/opeco",
	"grep -q opeco-link " + vmHome + "/.claude.json",
	"grep -q opeco-link " + vmHome + "/.claude/settings.json",
	"test -f " + vmHome + "/.claude/skills/opeco-link/SKILL.md",
	"grep -q opeco-link " + vmHome + "/.codex/config.toml",
	"grep -q opeco-link " + vmHome + "/.codex/hooks.json",
	"test -f " + vmHome + "/.codex/skills/opeco-link/SKILL.md",
}

// hookSays asks the opeco in the VM what the agent's settings have its
// runtime ask as a request arrives, and returns what the agent is told. No
// session is running: nothing in the test pairs.
func (h *harness) hookSays(settings string) string {
	h.t.Helper()
	var parsed struct {
		Hooks struct {
			UserPromptSubmit []struct {
				Hooks []struct {
					Tool  string          `json:"tool"`
					Input json.RawMessage `json:"input"`
				} `json:"hooks"`
			}
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(h.mustVM("cat "+settings)), &parsed); err != nil {
		h.t.Fatal(err)
	}
	if len(parsed.Hooks.UserPromptSubmit) != 1 || len(parsed.Hooks.UserPromptSubmit[0].Hooks) != 1 {
		h.t.Fatalf("%s: hooks as a request arrives = %+v, want one", settings, parsed.Hooks.UserPromptSubmit)
	}
	hook := parsed.Hooks.UserPromptSubmit[0].Hooks[0]
	calls := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"e2e","version":"0"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"` + hook.Tool + `","arguments":` + string(hook.Input) + `}}
`
	// opeco answers on stdout and leaves when its stdin closes, so the
	// input stays open long enough for the answer.
	out := h.mustVM("(echo " + base64.StdEncoding.EncodeToString([]byte(calls)) + " | base64 -d; sleep 2) | /usr/local/bin/opeco --no-browser mcp")
	for _, line := range strings.Split(out, "\n") {
		var answer struct {
			ID     int `json:"id"`
			Result struct {
				StructuredContent struct {
					HookSpecificOutput struct {
						AdditionalContext string `json:"additionalContext"`
					} `json:"hookSpecificOutput"`
				} `json:"structuredContent"`
			} `json:"result"`
		}
		if json.Unmarshal([]byte(line), &answer) == nil && answer.ID == 2 {
			return answer.Result.StructuredContent.HookSpecificOutput.AdditionalContext
		}
	}
	h.t.Fatalf("%s: opeco did not answer the hook:\n%s", settings, out)
	return ""
}

// holdBusyQRPorts tells the test's isotty home that the QR ports in use on
// this host belong to other projects, which they do when a developer has
// one attached, so `up` gives this project a port its attach can bind.
func (h *harness) holdBusyQRPorts() {
	h.t.Helper()
	if err := os.MkdirAll(filepath.Join(h.home, "projects"), 0o755); err != nil {
		h.t.Fatal(err)
	}
	for port := opeco.ChooseQRPort(nil); ; port++ {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			listener.Close()
			return
		}
		state := fmt.Sprintf(`{"project_hash": "held-%d", "opeco_qr_port": %d}`, port, port)
		if err := os.WriteFile(filepath.Join(h.home, "projects", fmt.Sprintf("held-%d.json", port)), []byte(state), 0o600); err != nil {
			h.t.Fatal(err)
		}
	}
}

// attachBinds reports whether an attach holds the host port: it attaches,
// tries the port and detaches.
func (h *harness) attachBinds(port string) bool {
	h.t.Helper()
	s := h.attach()
	h.waitFor("the attach to reach the session", 60*time.Second, func() bool {
		select {
		case err := <-s.done:
			h.t.Fatalf("the attach ended before reaching the session: %v\n%s", err, s.output.String())
		default:
		}
		out, err := h.vm("tmux list-sessions -F '#{session_name} #{session_attached}'")
		return err == nil && strings.TrimSpace(out) == "isotty 1"
	})
	conn, dialErr := net.DialTimeout("tcp", "127.0.0.1:"+port, 5*time.Second)
	if dialErr == nil {
		conn.Close()
	}
	h.mustVM("tmux detach-client -s isotty")
	if err := s.wait(h.t, 30*time.Second); err != nil {
		h.t.Fatalf("attach: %v", err)
	}
	return dialErr == nil
}

// TestOpecoDisableTakesItOutOfTheVM guards the switch: opeco comes with an
// agent unless the project turns it off, and with it the reminder to pair
// that an agent without a session gets as a request arrives. Turning it off
// takes away what an earlier up installed, in the VM and on the host's side of the attach,
// while what else is in the agents' settings stays. Turning it on again
// brings it back.
func TestOpecoDisableTakesItOutOfTheVM(t *testing.T) {
	h := newHarness(t)
	if err := os.WriteFile(filepath.Join(h.project, ".isotty", "agent.yaml"), []byte("agents:\n  claude: {}\n  codex: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	h.holdBusyQRPorts()

	up := h.isotty("up", "--no-attach")
	match := regexp.MustCompile(`QR page on port (\d+)`).FindStringSubmatch(up)
	if match == nil {
		t.Fatalf("up did not install opeco:\n%s", up)
	}
	qrPort := match[1]
	for _, trace := range opecoTraces {
		h.mustVM(trace)
	}
	for _, settings := range []string{vmHome + "/.claude/settings.json", vmHome + "/.codex/hooks.json"} {
		if said := h.hookSays(settings); !strings.Contains(said, "session_create") {
			t.Fatalf("%s: an agent without a session is told %q as a request arrives, want to be told to pair", settings, said)
		}
	}
	if !h.attachBinds(qrPort) {
		t.Fatalf("the attach should forward the QR port %s", qrPort)
	}
	// A server of the human's own, beside opeco's.
	h.mustVM("python3 /usr/local/share/isotty/mcp-merge.py mcp-json " + vmHome + "/.claude.json mine '{}' /bin/true")

	h.isotty("host", "opeco", "disable")
	h.isotty("up", "--no-attach")
	for _, trace := range opecoTraces {
		if _, err := h.vm(trace); err == nil {
			t.Fatalf("opeco is disabled but this still holds in the VM: %s", trace)
		}
	}
	h.mustVM(`grep -q '"mine"' ` + vmHome + "/.claude.json")
	if h.attachBinds(qrPort) {
		t.Fatalf("the attach still forwards the QR port %s", qrPort)
	}

	h.isotty("host", "opeco", "enable")
	h.isotty("up", "--no-attach")
	for _, trace := range opecoTraces {
		h.mustVM(trace)
	}

	h.isotty("down")
}
