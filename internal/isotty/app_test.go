package isotty

import (
	"strings"
	"testing"

	vmcfg "github.com/yazawa/isotty/internal/isotty/vm"
)

func TestBuildAttachSSHArgs(t *testing.T) {
	conn := vmcfg.GCPConnection{
		InstanceName: "isotty-abc",
		ProjectID:    "demo-project",
		Zone:         "us-central1-f",
	}
	forwardCfg := ForwardConfig{
		Forwards: map[string]Forward{
			"web": {LocalPort: 8080, RemotePort: 8080},
		},
	}

	args := buildAttachSSHArgs(conn, "/workspace", forwardCfg)
	joined := strings.Join(args, " ")
	if strings.Count(joined, "--ssh-flag=-t") != 2 {
		t.Fatalf("args = %v, want two tty flags", args)
	}
	if !strings.Contains(joined, "cd /workspace && exec ${SHELL:-/bin/bash} -l") {
		t.Fatalf("args = %v, want workspace shell command", args)
	}
	if !strings.Contains(joined, "--ssh-flag=-L 127.0.0.1:8080:127.0.0.1:8080") {
		t.Fatalf("args = %v, want forward flag", args)
	}
}

func TestBuildAttachSSHArgsWithUser(t *testing.T) {
	conn := vmcfg.GCPConnection{
		InstanceName: "isotty-abc",
		ProjectID:    "demo-project",
		Zone:         "us-central1-f",
		User:         "testuser",
	}

	args := buildAttachSSHArgs(conn, "/workspace", ForwardConfig{Forwards: map[string]Forward{}})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "compute ssh testuser@isotty-abc") {
		t.Fatalf("args = %v, want user-qualified instance target", args)
	}
}
