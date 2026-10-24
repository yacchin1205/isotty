package isotty

import (
	"strings"
	"testing"

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

	args := buildAttachSSHArgs(conn, "/workspace", ForwardConfig{Forwards: map[string]Forward{}}, &epweaveForward{VMPort: 61001, HostPort: 54321})
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
