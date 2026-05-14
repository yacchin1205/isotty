package isotty

import "testing"

func TestRemoteEndpoint(t *testing.T) {
	state := State{
		InstanceName: "isotty-abc123",
		Zone:         "asia-northeast1-b",
		GCPProjectID: "demo-project",
	}
	state.populateDerivedFields("/tmp/isotty-home")

	got := state.RemoteEndpoint()
	want := "isotty-abc123.asia-northeast1-b.demo-project:/workspace"
	if got != want {
		t.Fatalf("RemoteEndpoint() = %q, want %q", got, want)
	}
}

func TestPopulateDerivedFields(t *testing.T) {
	state := State{
		ProjectHash: "abc12345def0",
	}
	state.populateDerivedFields("/tmp/isotty-home")

	if state.SessionName != "isotty-abc12345def0" {
		t.Fatalf("SessionName = %q", state.SessionName)
	}
	if state.RemoteWorkspacePath != "/workspace" {
		t.Fatalf("RemoteWorkspacePath = %q", state.RemoteWorkspacePath)
	}
	if state.MutagenDataDirectory != "/tmp/isotty-home/mutagen" {
		t.Fatalf("MutagenDataDirectory = %q", state.MutagenDataDirectory)
	}
}
