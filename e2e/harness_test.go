//go:build e2e

// Package e2e runs the built isotty against a fake gcloud and an sshd
// container standing in for the VM. Run with ISOTTY_E2E=1 and
// `go test -tags e2e ./e2e/`; it needs docker, ssh, ssh-keygen, script and
// mutagen on the host.
package e2e

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const vmUser = "isotty"

type harness struct {
	t         *testing.T
	root      string
	bin       string
	container string
	port      string
	home      string
	project   string
	env       []string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	if os.Getenv("ISOTTY_E2E") != "1" {
		t.Skip("set ISOTTY_E2E=1 to run the end-to-end tests")
	}
	for _, name := range []string{"docker", "ssh", "ssh-keygen", "script", "mutagen"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Fatalf("%s is required for the end-to-end tests", name)
		}
	}
	// The mutagen daemon listens on a Unix socket under its data directory,
	// and macOS caps socket paths well below what t.TempDir() gives.
	root, err := os.MkdirTemp("/tmp", "isotty-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !t.Failed() {
			os.RemoveAll(root)
		}
	})
	here, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, root: root, bin: filepath.Join(root, "isotty"), home: filepath.Join(root, "home"), project: filepath.Join(root, "project")}

	h.run(filepath.Dir(here), "go", "build", "-o", h.bin, "./cmd/isotty")
	h.run(here, "docker", "build", "-q", "-t", "isotty-e2e-vm", "./vm")
	h.run(root, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(root, "key"))
	publicKey, err := os.ReadFile(filepath.Join(root, "key.pub"))
	if err != nil {
		t.Fatal(err)
	}
	h.container = fmt.Sprintf("isotty-e2e-%d", os.Getpid())
	h.run(root, "docker", "run", "-d", "--rm", "--name", h.container, "-p", "127.0.0.1:0:22", "-e", "AUTHORIZED_KEY="+strings.TrimSpace(string(publicKey)), "isotty-e2e-vm")
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", h.container).Run()
		stop := exec.Command("mutagen", "daemon", "stop")
		stop.Env = append(os.Environ(), "MUTAGEN_DATA_DIRECTORY="+filepath.Join(h.home, "mutagen"))
		_ = stop.Run()
	})
	address := strings.TrimSpace(h.run(root, "docker", "port", h.container, "22/tcp"))
	h.port = address[strings.LastIndex(address, ":")+1:]

	for _, dir := range []string{filepath.Join(h.project, ".isotty"), h.home} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h.env = append(h.env,
		"PATH="+filepath.Join(here, "fake-gcloud")+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+os.Getenv("HOME"),
		"ISOTTY_HOME="+h.home,
		"ISOTTY_GCP_PROJECT=e2e-project",
		"ISOTTY_GCP_ZONE=e2e-zone",
		"ISOTTY_FAKE_GCLOUD_STATE="+filepath.Join(root, "gcloud"),
		"ISOTTY_FAKE_VM_HOST=127.0.0.1",
		"ISOTTY_FAKE_VM_PORT="+h.port,
		"ISOTTY_FAKE_VM_USER="+vmUser,
		"ISOTTY_FAKE_VM_KEY="+filepath.Join(root, "key"),
	)
	h.waitFor("sshd in the container", 60*time.Second, func() bool {
		return exec.Command("ssh", "-p", h.port, "-i", filepath.Join(root, "key"), "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR", vmUser+"@127.0.0.1", "true").Run() == nil
	})
	return h
}

// run executes a host command and fails the test when it fails.
func (h *harness) run(dir string, name string, args ...string) string {
	h.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

// isotty runs the built binary in the project directory with the test's
// environment.
func (h *harness) isotty(args ...string) string {
	h.t.Helper()
	cmd := exec.Command(h.bin, args...)
	cmd.Dir = h.project
	cmd.Env = h.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("isotty %s: %v\n%s\n%s", strings.Join(args, " "), err, out, h.gcloudCalls())
	}
	return string(out)
}

func (h *harness) gcloudCalls() string {
	data, _ := os.ReadFile(filepath.Join(h.root, "gcloud", "calls.log"))
	return "gcloud calls:\n" + string(data)
}

// vm runs a shell command in the container as the VM user.
func (h *harness) vm(command string) (string, error) {
	out, err := exec.Command("docker", "exec", "-u", vmUser, h.container, "sh", "-c", command).CombinedOutput()
	return string(out), err
}

func (h *harness) mustVM(command string) string {
	h.t.Helper()
	out, err := h.vm(command)
	if err != nil {
		h.t.Fatalf("in the VM: %s: %v\n%s", command, err, out)
	}
	return out
}

func (h *harness) waitFor(what string, timeout time.Duration, ok func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if ok() {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// session is an `isotty attach` running under a pseudo-terminal from
// `script`, with its stdin in the test's hands.
type session struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	output terminalOutput
	done   chan error
}

// terminalOutput is what the attach has written so far; a test reads it
// while the attach still writes.
type terminalOutput struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (o *terminalOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.Write(p)
}

func (o *terminalOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.String()
}

func (h *harness) attach(args ...string) *session {
	h.t.Helper()
	var cmd *exec.Cmd
	attach := append([]string{h.bin, "attach"}, args...)
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("script", append([]string{"-q", "/dev/null"}, attach...)...)
	} else {
		cmd = exec.Command("script", "-q", "-e", "-c", strings.Join(attach, " "), "/dev/null")
	}
	cmd.Dir = h.project
	cmd.Env = append(h.env, "TERM=xterm")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		h.t.Fatal(err)
	}
	s := &session{cmd: cmd, stdin: stdin, done: make(chan error, 1)}
	cmd.Stdout = &s.output
	cmd.Stderr = &s.output
	if err := cmd.Start(); err != nil {
		h.t.Fatal(err)
	}
	go func() { s.done <- cmd.Wait() }()
	h.t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
		}
	})
	return s
}

func (s *session) typeLine(line string) {
	_, _ = io.WriteString(s.stdin, line+"\r")
}

// wait returns once the attach has ended, with its exit error.
func (s *session) wait(t *testing.T, timeout time.Duration) error {
	t.Helper()
	select {
	case err := <-s.done:
		if err != nil {
			t.Logf("attach output:\n%s", s.output.String())
		}
		return err
	case <-time.After(timeout):
		t.Fatalf("the attach did not end within %s\n%s", timeout, s.output.String())
		return nil
	}
}
