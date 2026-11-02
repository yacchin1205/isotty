package isotty

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yazawa/isotty/internal/isotty/host"
	runtimecfg "github.com/yazawa/isotty/internal/isotty/runtime"

	vmcfg "github.com/yazawa/isotty/internal/isotty/vm"
)

func TestBuildAttachSSHArgs(t *testing.T) {
	conn := vmcfg.GCPConnection{
		InstanceName:  "isotty-abc",
		ProjectID:     "demo-project",
		Zone:          "us-central1-f",
		SSHConfigPath: "/tmp/isotty/ssh/config",
	}
	forwardCfg := host.ForwardConfig{
		Forwards: map[string]host.Forward{
			"web": {LocalPort: 8080, RemotePort: 8080},
		},
	}

	args := buildAttachSSHArgs(conn, "/workspace", forwardCfg, nil)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-F /tmp/isotty/ssh/config -tt") {
		t.Fatalf("args = %v, want ssh config and tty flags", args)
	}
	if !strings.Contains(joined, "cd /workspace && exec ${SHELL:-/bin/bash} -l") {
		t.Fatalf("args = %v, want workspace shell command", args)
	}
	if !strings.Contains(joined, "-L 127.0.0.1:8080:127.0.0.1:8080") {
		t.Fatalf("args = %v, want forward flag", args)
	}
	if !strings.Contains(joined, "isotty-abc.us-central1-f.demo-project") {
		t.Fatalf("args = %v, want GCP ssh config host", args)
	}
	if !strings.Contains(joined, "-o ExitOnForwardFailure=yes -o ServerAliveInterval=15 -o ServerAliveCountMax=3") {
		t.Fatalf("args = %v, want forward failure and keepalive options", args)
	}
	if strings.Contains(joined, "-R ") {
		t.Fatalf("args = %v, want no reverse forward without ep_weave", args)
	}
}

func TestBuildAttachSSHArgsWithEpweaveGateway(t *testing.T) {
	conn := vmcfg.GCPConnection{
		InstanceName:  "isotty-abc",
		ProjectID:     "demo-project",
		Zone:          "us-central1-f",
		SSHConfigPath: "/tmp/isotty/ssh/config",
	}

	args := buildAttachSSHArgs(conn, "/workspace", host.ForwardConfig{Forwards: map[string]host.Forward{}}, &reverseForward{VMPort: 61001, HostPort: 54321})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-R 127.0.0.1:61001:127.0.0.1:54321") {
		t.Fatalf("args = %v, want reverse forward to the gateway", args)
	}
	if !strings.Contains(joined, "cd /workspace && BROWSER=/usr/local/bin/isotty-open exec ${SHELL:-/bin/bash} -l") {
		t.Fatalf("args = %v, want BROWSER set for the session shell", args)
	}
}

func TestBuildAttachSSHArgsWithUser(t *testing.T) {
	conn := vmcfg.GCPConnection{
		InstanceName:  "isotty-abc",
		ProjectID:     "demo-project",
		Zone:          "us-central1-f",
		User:          "testuser",
		SSHConfigPath: "/tmp/isotty/ssh/config",
	}

	args := buildAttachSSHArgs(conn, "/workspace", host.ForwardConfig{Forwards: map[string]host.Forward{}}, nil)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "testuser@isotty-abc.us-central1-f.demo-project") {
		t.Fatalf("args = %v, want user-qualified instance target", args)
	}
}

func TestGatewayHandlerServesNothingWithNoHost(t *testing.T) {
	record := func(runtimecfg.VMEventRecord) error { return nil }
	fail := func(err error) { t.Fatal(err) }

	_, services, err := (&App{}).gatewayHandler(true, "abc", nil, nil, nil, nil, nil, record, fail)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 0 {
		t.Fatalf("services = %v, want none so a second terminal can attach without the gateway", services)
	}

	mux, services, err := (&App{}).gatewayHandler(false, "abc", nil, nil, nil, nil, nil, record, fail)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(services, ",") != "browser open" {
		t.Fatalf("services = %v, want browser open", services)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/open", nil))
	if rec.Code == http.StatusNotFound {
		t.Fatal("/open must be served when the gateway is wanted")
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
