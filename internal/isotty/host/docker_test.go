package host

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yazawa/isotty/internal/isotty/host/docker"
)

// Pinning an image repository writes the synced file and the host's pin
// together, takes the repository by name only, and at the two levels the
// relay knows: read to pull, write to push as well.
func TestDockerPinsARepositoryForPullingOrPushing(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	reachable := checkRegistry
	checkRegistry = func(string) error { return nil }
	t.Cleanup(func() { checkRegistry = reachable })
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := runDocker(project, "abc", home, args, &out, &out)
		return out.String(), err
	}
	if out, err := run("add", "--repo", "registry.example.org/team/base"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	cfg, err := docker.Kind.LoadPinned(project, "abc", home)
	if err != nil || cfg == nil || cfg.Repos["registry.example.org/team/base"] != docker.AccessRead {
		t.Fatalf("pinned config = %+v, %v", cfg, err)
	}
	if _, err := os.Stat(filepath.Join(project, ".isotty", "docker.yaml")); err != nil {
		t.Fatal(err)
	}
	if out, err := run("add", "--repo", "registry.example.org/team/app", "--allow", "write"); err != nil {
		t.Fatalf("add for pushing: %v\n%s", err, out)
	}
	cfg, err = docker.Kind.LoadPinned(project, "abc", home)
	if err != nil || cfg == nil || cfg.Repos["registry.example.org/team/app"] != docker.AccessWrite || cfg.Repos["registry.example.org/team/base"] != docker.AccessRead {
		t.Fatalf("pinned config = %+v, %v", cfg, err)
	}
	for _, args := range [][]string{
		{"add", "--repo", "registry.example.org/team/tools", "--allow", "admin"},
		{"add", "--repo", "registry.example.org/team/tools", "--allow", "comment"},
		{"add", "--repo", "alpine"},
		{"add", "--remote", "origin"},
		{"add"},
	} {
		if out, err := run(args...); err == nil {
			t.Fatalf("%v was accepted:\n%s", args, out)
		}
	}
	if out, err := run("remove", "--repo", "registry.example.org/team/base", "--repo", "registry.example.org/team/app"); err != nil || !strings.Contains(out, "No repository is pinned") {
		t.Fatalf("remove: %v\n%s", err, out)
	}
	if cfg, err := docker.Kind.LoadPinned(project, "abc", home); err != nil || cfg != nil {
		t.Fatalf("after remove: %+v, %v", cfg, err)
	}
}

// A repository is pinned only when this host can reach its registry: a
// mistyped host is refused on the host instead of surfacing in the VM at
// the first pull.
func TestDockerRefusesARepositoryWhoseRegistryIsOutOfReach(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	registry := listener.Addr().String()
	var out bytes.Buffer
	if err := runDocker(project, "abc", home, []string{"add", "--repo", registry + "/team/base"}, &out, &out); err != nil {
		t.Fatalf("add with the registry listening: %v\n%s", err, out.String())
	}
	listener.Close()
	err = runDocker(project, "abc", home, []string{"add", "--repo", registry + "/team/app"}, &out, &out)
	if err == nil || !strings.Contains(err.Error(), "is not reachable from this host") {
		t.Fatalf("add with the registry gone = %v, want it refused as out of reach", err)
	}
	cfg, err := docker.Kind.LoadPinned(project, "abc", home)
	if err != nil || cfg == nil || len(cfg.Repos) != 1 {
		t.Fatalf("pinned config = %+v, %v, want only the repository that was reachable", cfg, err)
	}
}
