package isotty

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/yazawa/isotty/internal/isotty/host"
	runtimecfg "github.com/yazawa/isotty/internal/isotty/runtime"

	vmcfg "github.com/yazawa/isotty/internal/isotty/vm"
)

// The terminal's ssh carries no forward: the daemon holds those.
func TestBuildAttachSSHArgs(t *testing.T) {
	conn := vmcfg.GCPConnection{
		InstanceName:  "isotty-abc",
		ProjectID:     "demo-project",
		Zone:          "us-central1-f",
		User:          "testuser",
		SSHConfigPath: "/tmp/isotty/ssh/config",
	}
	args := buildAttachSSHArgs(conn, "/workspace", attachShell{})
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-F /tmp/isotty/ssh/config -tt",
		"-o ServerAliveInterval=15 -o ServerAliveCountMax=3",
		"testuser@isotty-abc.us-central1-f.demo-project",
		"cd /workspace && BROWSER=/usr/local/bin/isotty-open exec ${SHELL:-/bin/bash} -l",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args = %v, want %s", args, want)
		}
	}
	if strings.Contains(joined, "-L ") || strings.Contains(joined, "-R ") {
		t.Fatalf("args = %v, want no forward", args)
	}
}

// Every terminal is alike: the first takes the session, one that attaches
// while another is attached joins it as a grouped session.
func TestAttachShellCommandInTmux(t *testing.T) {
	want := "if tmux has-session -t isotty 2>/dev/null && [ -n \"$(tmux list-clients -t isotty)\" ]; then " +
		"exec tmux new-session -t isotty -c /workspace ';' set-option destroy-unattached on; else " +
		"BROWSER=/usr/local/bin/isotty-open exec tmux new-session -A -s isotty -c /workspace ';' set-environment -t isotty BROWSER /usr/local/bin/isotty-open; fi"
	if got := attachShellCommand("/workspace", attachShell{Tmux: true}); got != want {
		t.Fatalf("command = %q\nwant %q", got, want)
	}
}

func TestAttachShellFor(t *testing.T) {
	dir := t.TempDir()
	shell, err := attachShellFor(dir, false)
	if err != nil || shell != (attachShell{Tmux: true}) {
		t.Fatalf("default = %#v, %v", shell, err)
	}
	if shell, err = attachShellFor(dir, true); err != nil || shell.Tmux {
		t.Fatalf("--no-tmux = %#v, %v", shell, err)
	}
	if err := os.WriteFile(AttachConfigPath(dir), []byte("tmux: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if shell, err = attachShellFor(dir, false); err != nil || shell.Tmux {
		t.Fatalf("tmux: false = %#v, %v", shell, err)
	}
}

func TestGatewayHandlerServesTheOpenerAndWakes(t *testing.T) {
	t.Setenv("ISOTTY_HOME", t.TempDir())
	record := func(runtimecfg.VMEventRecord) error { return nil }
	fail := func(err error) { t.Fatal(err) }
	mux, services, err := (&App{}).gatewayHandler(vmcfg.GCPConnection{}, "abc", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, record, fail)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(services, ",") != "browser open,wake" {
		t.Fatalf("services = %v, want browser open and wake", services)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/open", nil))
	if rec.Code == http.StatusNotFound {
		t.Fatal("/open must be served")
	}
}

func TestAddOpecoForwardUsesTheProjectPort(t *testing.T) {
	forwardCfg := host.ForwardConfig{Forwards: map[string]host.Forward{}}
	if err := addOpecoForward(forwardCfg, 0); err == nil {
		t.Fatal("a state without a QR port must fail the attach")
	}
	if err := addOpecoForward(forwardCfg, 61003); err != nil {
		t.Fatal(err)
	}
	if qr := forwardCfg.Forwards[opecoForwardName]; qr.LocalPort != 61003 || qr.RemotePort != 61003 {
		t.Fatalf("opeco-qr forward = %+v, want 61003 on both ends", qr)
	}
	if err := addOpecoForward(forwardCfg, 61003); err == nil {
		t.Fatal("a project forward named opeco-qr must be rejected")
	}
}

// A live attach never lets go of the gateway's VM port, so the wait for it
// ends by saying how to join that attach; a port that came free is no error.
