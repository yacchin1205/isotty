package isotty

import "testing"

func TestRemoteEndpoint(t *testing.T) {
	state := State{
		InstanceName:        "isotty-abc123",
		Zone:                "asia-northeast1-b",
		GCPProjectID:        "demo-project",
		RemoteWorkspacePath: "/workspace",
	}

	got := state.RemoteEndpoint()
	want := "isotty-abc123.asia-northeast1-b.demo-project:/workspace"
	if got != want {
		t.Fatalf("RemoteEndpoint() = %q, want %q", got, want)
	}
}
