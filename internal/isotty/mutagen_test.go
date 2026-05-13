package isotty

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestBuildMutagenCreateArgs(t *testing.T) {
	state := State{
		ProjectPath:         filepath.Clean("/tmp/project"),
		ProjectHash:         "abc12345def0",
		Backend:             "gcp-vm",
		SessionName:         "isotty-abc12345def0",
		SyncMode:            defaultSyncMode,
		InstanceName:        "isotty-abc12345def0",
		GCPProjectID:        "demo-project",
		Zone:                "us-central1-f",
		RemoteWorkspacePath: "/workspace",
	}

	args := buildMutagenCreateArgs(state)

	if !slices.Contains(args, "--mode") {
		t.Fatal("expected --mode flag in mutagen create args")
	}
	if slices.Contains(args, "--sync-mode") {
		t.Fatal("did not expect legacy --sync-mode flag in mutagen create args")
	}
	if !slices.Contains(args, "--no-global-configuration") {
		t.Fatal("expected --no-global-configuration flag in mutagen create args")
	}
	if !slices.Contains(args, "--label") {
		t.Fatal("expected labels in mutagen create args")
	}
	if got := args[len(args)-1]; got != "isotty-abc12345def0.us-central1-f.demo-project:/workspace" {
		t.Fatalf("unexpected remote endpoint: %q", got)
	}
}

func TestMutagenLabelSelector(t *testing.T) {
	state := State{
		ProjectHash: "abc12345def0",
		Backend:     "gcp-vm",
		SyncMode:    defaultSyncMode,
	}

	got := state.MutagenLabelSelector()
	want := "app=isotty,project_hash=abc12345def0,backend=gcp-vm,sync_mode=one-way-safe"
	if got != want {
		t.Fatalf("MutagenLabelSelector() = %q, want %q", got, want)
	}
}
