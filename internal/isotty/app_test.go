package isotty

import (
	"strings"
	"testing"
)

func TestBuildAttachSSHArgs(t *testing.T) {
	state := State{
		InstanceName: "isotty-abc",
		GCPProjectID: "demo-project",
		Zone:         "us-central1-f",
	}
	forwardCfg := ForwardConfig{
		Forwards: map[string]Forward{
			"web": {LocalPort: 8080, RemotePort: 8080},
		},
	}

	args := buildAttachSSHArgs(state, forwardCfg)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--ssh-flag=-t") {
		t.Fatalf("args = %v, want tty flag", args)
	}
	if !strings.Contains(joined, "cd /workspace && exec ${SHELL:-/bin/bash} -l") {
		t.Fatalf("args = %v, want workspace shell command", args)
	}
	if !strings.Contains(joined, "--ssh-flag=-L 127.0.0.1:8080:127.0.0.1:8080") {
		t.Fatalf("args = %v, want forward flag", args)
	}
}
