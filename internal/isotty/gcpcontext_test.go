package isotty

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGCloud puts a gcloud on PATH whose active configuration names
// gcloud-project and gcloud-zone, and points ISOTTY_HOME at a new directory.
func fakeGCloud(t *testing.T) string {
	t.Helper()
	bin, home := t.TempDir(), t.TempDir()
	script := `#!/bin/sh
case "$3" in
  project) echo gcloud-project;;
  compute/zone) echo gcloud-zone;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "gcloud"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ISOTTY_HOME", home)
	t.Setenv("ISOTTY_GCP_PROJECT", "")
	t.Setenv("ISOTTY_GCP_ZONE", "")
	return home
}

func TestGCPContextComesFromGCloudTheFirstTime(t *testing.T) {
	home := fakeGCloud(t)
	ctx, err := resolveGCPContext(home, "abc")
	if err != nil || ctx != (gcpContext{Project: "gcloud-project", Zone: "gcloud-zone"}) {
		t.Fatal(ctx, err)
	}
}

// After `down` the state is gone; the project comes back where it was, not
// where gcloud now points, unless the environment names another place.
func TestGCPContextIsRememberedAfterDown(t *testing.T) {
	home := fakeGCloud(t)
	if err := saveGCPContext(home, "abc", gcpContext{Project: "last-project", Zone: "last-zone"}); err != nil {
		t.Fatal(err)
	}
	ctx, err := resolveGCPContext(home, "abc")
	if err != nil || ctx != (gcpContext{Project: "last-project", Zone: "last-zone"}) {
		t.Fatal(ctx, err)
	}
	t.Setenv("ISOTTY_GCP_ZONE", "env-zone")
	ctx, err = resolveGCPContext(home, "abc")
	if err != nil || ctx != (gcpContext{Project: "last-project", Zone: "env-zone"}) {
		t.Fatal(ctx, err)
	}
	if ctx, err := resolveGCPContext(home, "other"); err != nil || ctx.Project != "gcloud-project" {
		t.Fatal("another project took this one's context:", ctx, err)
	}
}

// A state written before the context was remembered still names where the
// VM is.
func TestGCPContextOfAnUpVMIsItsState(t *testing.T) {
	home := fakeGCloud(t)
	if err := SaveState(State{ProjectHash: "abc", GCPProjectID: "state-project", Zone: "state-zone"}); err != nil {
		t.Fatal(err)
	}
	ctx, err := resolveGCPContext(home, "abc")
	if err != nil || ctx != (gcpContext{Project: "state-project", Zone: "state-zone"}) {
		t.Fatal(ctx, err)
	}
}

func TestUpRefusesToMoveAVMThatIsUp(t *testing.T) {
	home := fakeGCloud(t)
	project := t.TempDir()
	hash := hashProjectPath(project)
	if err := SaveState(State{ProjectPath: project, ProjectHash: hash, GCPProjectID: "state-project", Zone: "state-zone"}); err != nil {
		t.Fatal(err)
	}
	cfg := Config{ProjectPath: project, ProjectHash: hash, HomeDir: home, GCPProjectID: "state-project", Zone: "env-zone"}
	_, err := (&App{}).ensureEnvironment(cfg, "two-way-safe", false)
	if err == nil || !strings.Contains(err.Error(), "isotty down") {
		t.Fatalf("moving an up VM: %v", err)
	}
	if _, err := os.Stat(gcpContextPath(home, hash)); !os.IsNotExist(err) {
		t.Fatalf("the refused context was remembered: %v", err)
	}
}
