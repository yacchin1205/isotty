package isotty

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yazawa/isotty/internal/isotty/host"
	vmcfg "github.com/yazawa/isotty/internal/isotty/vm"
)

// The daemon answers on its socket, takes a project up when asked, lets go
// of one whose state is gone, and stops when told.
func TestDaemonControl(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ISOTTY_HOME", home)
	var out bytes.Buffer
	served := make(chan error, 1)
	go func() { served <- (&App{stdout: &out, stderr: &out}).serveDaemon(home) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := daemonStatus(home); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the daemon did not answer:\n%s", out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := (&App{stdout: &out, stderr: &out}).serveDaemon(home); err == nil || !strings.Contains(err.Error(), "another") {
		t.Fatalf("a second daemon started: %v", err)
	}
	// A command of the same binary leaves it running.
	if err := (&App{stdout: &out, stderr: &out}).replaceOlderDaemon(home); err != nil {
		t.Fatal(err)
	}
	if _, err := daemonStatus(home); err != nil {
		t.Fatalf("the daemon of the same binary was replaced: %v", err)
	}
	if err := reloadProject(home, "gone"); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		statuses, err := daemonStatus(home)
		if err != nil {
			t.Fatal(err)
		}
		if len(statuses) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the daemon holds a project without state: %+v", statuses)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := (&App{stdout: &out, stderr: &out}).stopDaemon(home); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-served:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon did not stop")
	}
}

// The gateway's ssh ends when its forward cannot bind; it holds no terminal.
func TestDaemonSSHArgs(t *testing.T) {
	args := strings.Join(daemonSSHArgs(vmcfg.GCPConnection{InstanceName: "isotty-abc", Zone: "z", ProjectID: "p", SSHConfigPath: "/c"}, "-R", "127.0.0.1:61001:127.0.0.1:5000"), " ")
	want := "-F /c -T -o ExitOnForwardFailure=yes -o ServerAliveInterval=15 -o ServerAliveCountMax=3 -R 127.0.0.1:61001:127.0.0.1:5000 isotty-abc.z.p exec sleep infinity"
	if args != want {
		t.Fatalf("args = %s\nwant %s", args, want)
	}
}

func TestSameForwards(t *testing.T) {
	a := host.ForwardConfig{Forwards: map[string]host.Forward{"web": {LocalPort: 8080, RemotePort: 80}}}
	b := host.ForwardConfig{Forwards: map[string]host.Forward{"web": {LocalPort: 8080, RemotePort: 80}}}
	if !sameForwards(a, b) {
		t.Fatal("same forwards differ")
	}
	b.Forwards["api"] = host.Forward{LocalPort: 9000, RemotePort: 9000}
	if sameForwards(a, b) {
		t.Fatal("a new forward went unnoticed")
	}
}

// A relay that cannot be read is left out and named; the others are still
// read.
func TestLoadHostConfigNamesWhatItRefuses(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("ISOTTY_HOME", home)
	if err := writeFile(project+"/.isotty/github.yaml", "repos: [\n"); err != nil {
		t.Fatal(err)
	}
	cfg := loadHostConfig(project, State{ProjectPath: project, ProjectHash: "abc"}, home)
	if cfg.refused["GitHub"] == nil || !strings.Contains(cfg.describeRefused(), "GitHub not served") {
		t.Fatalf("refused: %v", cfg.refused)
	}
	if len(cfg.refused) != 1 || cfg.forwards.Forwards == nil {
		t.Fatalf("more than GitHub was refused: %v", cfg.refused)
	}
}

func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
