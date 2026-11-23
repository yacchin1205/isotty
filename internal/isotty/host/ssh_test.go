package host

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yazawa/isotty/internal/isotty/host/sshhost"
)

const githubHostKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"

// fakeSSH puts an ssh on PATH that records its arguments and its standard
// input, and, when the known-hosts file it is given is empty, writes the
// host key into it as ssh does once the human accepts it.
func fakeSSH(t *testing.T, fail bool) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	exit := "0"
	if fail {
		exit = "255"
	}
	script := `#!/bin/sh
for arg in "$@"; do printf '%s\n' "$arg" >> ` + log + `; done
cat >> ` + log + `.stdin
for arg in "$@"; do
  case "$arg" in
    UserKnownHostsFile=*) f="${arg#UserKnownHostsFile=}"; [ -s "$f" ] || echo "isotty-host ` + githubHostKey + `" >> "$f";;
  esac
done
exit ` + exit + `
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func TestSSHAddRecordsTheAcceptedHostKeyAndKeepsTheKeyOnTheHost(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	log := fakeSSH(t, false)
	var stdout, stderr bytes.Buffer
	if err := runSSH(project, "abc", home, []string{"add", "pi", "--host", "raspberrypi.local", "--user", "pi"}, &stdout, &stderr); err != nil {
		t.Fatalf("%v\n%s", err, stderr.String())
	}
	cfg, err := LoadSSHProjectConfig(project, "abc", home)
	if err != nil || cfg == nil {
		t.Fatal(cfg, err)
	}
	if h := cfg.Hosts["pi"]; h.Host != "raspberrypi.local" || h.Port != 22 || h.User != "pi" || len(h.HostKeys) != 1 || h.HostKeys[0] != githubHostKey {
		t.Fatalf("pinned %+v", h)
	}
	args, _ := os.ReadFile(log)
	input, _ := os.ReadFile(log + ".stdin")
	for _, want := range []string{"StrictHostKeyChecking=ask", "HostKeyAlias=isotty-host", "GlobalKnownHostsFile=none", "-l\npi\n", "--\nraspberrypi.local\n", "authorized_keys"} {
		if !strings.Contains(string(args), want) {
			t.Fatalf("ssh args lack %q:\n%s", want, args)
		}
	}
	if !strings.HasPrefix(string(input), "ssh-ed25519 ") || !strings.HasSuffix(string(input), " isotty abc pi\n") {
		t.Fatalf("ssh stdin = %q", input)
	}
	key, err := os.Stat(sshhost.KeyPath(home, "abc", "pi"))
	if err != nil || key.Mode().Perm() != 0o600 {
		t.Fatalf("key: %v %v", key, err)
	}
	if strings.Contains(string(mustRead(t, sshhost.ProjectConfigPath(project))), "PRIVATE KEY") || !strings.Contains(string(mustRead(t, sshhost.KeyPath(home, "abc", "pi"))), "BEGIN OPENSSH PRIVATE KEY") {
		t.Fatal("the private key is in the synced file")
	}
	if err := runSSH(project, "abc", home, []string{"add", "pi", "--host", "x", "--user", "pi"}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "already added") {
		t.Fatalf("adding again: %v", err)
	}
}

// An OpenSSH older than 6.5 has no ed25519; the key can be ECDSA for it.
func TestSSHAddMakesAnECDSAKeyForAnOldMachine(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	log := fakeSSH(t, false)
	var stdout, stderr bytes.Buffer
	if err := runSSH(project, "abc", home, []string{"add", "pi", "--host", "raspberrypi.local", "--user", "pi", "--key-type", "ecdsa"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if input := string(mustRead(t, log+".stdin")); !strings.HasPrefix(input, "ecdsa-sha2-nistp256 ") {
		t.Fatalf("ssh stdin = %q", input)
	}
	if err := runSSH(project, "abc", home, []string{"add", "nas", "--host", "nas.local", "--user", "pi", "--key-type", "rsa"}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "ed25519 or ecdsa") {
		t.Fatalf("an unknown key type: %v", err)
	}
}

func TestSSHAddKeepsNothingWhenTheLoginFails(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	fakeSSH(t, true)
	var stdout, stderr bytes.Buffer
	if err := runSSH(project, "abc", home, []string{"add", "pi", "--host", "raspberrypi.local", "--user", "pi"}, &stdout, &stderr); err == nil {
		t.Fatal("a failed login added the machine")
	}
	if cfg, err := LoadSSHProjectConfig(project, "abc", home); err != nil || cfg != nil {
		t.Fatal(cfg, err)
	}
	if _, err := os.Stat(sshhost.KeyPath(home, "abc", "pi")); !os.IsNotExist(err) {
		t.Fatalf("a key was kept: %v", err)
	}
}

func TestSSHRemoveTakesTheKeyOutUnderTheRecordedHostKey(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	fakeSSH(t, false)
	var stdout, stderr bytes.Buffer
	if err := runSSH(project, "abc", home, []string{"add", "pi", "--host", "raspberrypi.local", "--user", "pi"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	log := fakeSSH(t, false)
	if err := runSSH(project, "abc", home, []string{"remove", "pi"}, &stdout, &stderr); err != nil {
		t.Fatalf("%v\n%s", err, stderr.String())
	}
	args := string(mustRead(t, log))
	if !strings.Contains(args, "StrictHostKeyChecking=yes") || !strings.Contains(args, "HostKeyAlgorithms=ssh-ed25519\n") || !strings.Contains(args, "grep -vF -- AAAAC3NzaC1lZDI1NTE5") {
		t.Fatalf("ssh args:\n%s", args)
	}
	if cfg, err := LoadSSHProjectConfig(project, "abc", home); err != nil || cfg != nil {
		t.Fatal(cfg, err)
	}
	if _, err := os.Stat(sshhost.KeyPath(home, "abc", "pi")); !os.IsNotExist(err) {
		t.Fatalf("the key is still kept: %v", err)
	}
}

func TestSSHRemoveForceForgetsAMachineThatIsGone(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	fakeSSH(t, false)
	var stdout, stderr bytes.Buffer
	if err := runSSH(project, "abc", home, []string{"add", "pi", "--host", "raspberrypi.local", "--user", "pi"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	log := fakeSSH(t, true)
	if err := runSSH(project, "abc", home, []string{"remove", "pi"}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("an unreachable machine: %v", err)
	}
	os.Remove(log)
	if err := runSSH(project, "abc", home, []string{"remove", "pi", "--force"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("--force logged in")
	}
	if cfg, err := LoadSSHProjectConfig(project, "abc", home); err != nil || cfg != nil {
		t.Fatal(cfg, err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return data
}
