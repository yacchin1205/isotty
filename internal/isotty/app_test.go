package isotty

import (
	"strings"
	"testing"

	"github.com/yazawa/isotty/internal/isotty/opeco"
	vmcfg "github.com/yazawa/isotty/internal/isotty/vm"
)

func TestBuildAttachSSHArgs(t *testing.T) {
	conn := vmcfg.GCPConnection{
		InstanceName:  "isotty-abc",
		ProjectID:     "demo-project",
		Zone:          "us-central1-f",
		SSHConfigPath: "/tmp/isotty/ssh/config",
	}
	forwardCfg := ForwardConfig{
		Forwards: map[string]Forward{
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

	args := buildAttachSSHArgs(conn, "/workspace", ForwardConfig{Forwards: map[string]Forward{}}, &reverseForward{VMPort: 61001, HostPort: 54321})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-R 127.0.0.1:61001:127.0.0.1:54321") {
		t.Fatalf("args = %v, want reverse forward to the gateway", args)
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

	args := buildAttachSSHArgs(conn, "/workspace", ForwardConfig{Forwards: map[string]Forward{}}, nil)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "testuser@isotty-abc.us-central1-f.demo-project") {
		t.Fatalf("args = %v, want user-qualified instance target", args)
	}
}

func TestAddOpecoForwardUsesTheProjectPort(t *testing.T) {
	home := t.TempDir()
	forwardCfg := ForwardConfig{Forwards: map[string]Forward{}}
	if err := addOpecoForward(forwardCfg, []string{"claude"}, home, "abc"); err == nil {
		t.Fatal("attach without a port allocated by up must fail")
	}
	port, err := opeco.AllocateQRPort(home, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if err := addOpecoForward(forwardCfg, []string{"claude"}, home, "abc"); err != nil {
		t.Fatal(err)
	}
	if qr := forwardCfg.Forwards[opecoForwardName]; qr.LocalPort != port || qr.RemotePort != port {
		t.Fatalf("opeco-qr forward = %+v, want %d on both ends", qr, port)
	}
	if err := addOpecoForward(forwardCfg, []string{"claude"}, home, "abc"); err == nil {
		t.Fatal("a project forward named opeco-qr must be rejected")
	}

	none := ForwardConfig{Forwards: map[string]Forward{}}
	if err := addOpecoForward(none, nil, home, "abc"); err != nil || len(none.Forwards) != 0 {
		t.Fatalf("forwards without agents = %v, err = %v", none.Forwards, err)
	}
}
