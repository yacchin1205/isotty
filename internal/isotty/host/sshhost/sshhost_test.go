package sshhost

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yazawa/isotty/internal/isotty/host/pin"
)

const hostKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"

// machine is a TCP server standing for the machine on the host's network:
// it answers each line with the line upper-cased.
func machine(t *testing.T) (string, int) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				scanner := bufio.NewScanner(conn)
				for scanner.Scan() {
					io.WriteString(conn, strings.ToUpper(scanner.Text())+"\n")
				}
			}()
		}
	}()
	addr := listener.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port
}

type recorder struct {
	mu     sync.Mutex
	events []Event
	done   chan struct{}
}

func (r *recorder) record(e Event) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
	if e.Operation == "close" {
		r.done <- struct{}{}
	}
}

func (r *recorder) wait(t *testing.T) []Event {
	t.Helper()
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		t.Fatal("no close event")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.events...)
}

func upgrade(t *testing.T, server *httptest.Server, name string, withUpgrade bool) (net.Conn, *bufio.Reader, string) {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	request := "GET /" + name + " HTTP/1.1\r\nHost: x\r\n"
	if withUpgrade {
		request += "Connection: Upgrade\r\nUpgrade: isotty-ssh\r\n"
	}
	io.WriteString(conn, request+"\r\n")
	reader := bufio.NewReader(conn)
	status, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil || line == "\r\n" {
			break
		}
	}
	return conn, reader, strings.TrimSpace(status)
}

// The relay carries a session to a machine of the config, and only to one
// of them, and says when it opened and how it ended.
func TestRelayCarriesASessionToANamedMachineOnly(t *testing.T) {
	address, port := machine(t)
	cfg := ProjectConfig{Hosts: map[string]Host{"pi": {Host: address, Port: port, User: "pi", HostKeys: []string{hostKey}}}}
	events := &recorder{done: make(chan struct{}, 4)}
	relay := NewRelay(cfg)
	server := httptest.NewServer(Handler(relay, events.record))
	defer server.Close()

	conn, reader, status := upgrade(t, server, "pi", true)
	if status != "HTTP/1.1 101 Switching Protocols" {
		t.Fatalf("status = %q", status)
	}
	io.WriteString(conn, "hello\n")
	if line, _ := reader.ReadString('\n'); line != "HELLO\n" {
		t.Fatalf("machine answered %q", line)
	}
	conn.(*net.TCPConn).CloseWrite()
	if rest, _ := io.ReadAll(reader); len(rest) != 0 {
		t.Fatalf("after the end: %q", rest)
	}
	got := events.wait(t)
	want := "pi@" + address + ":" + strconv.Itoa(port)
	if len(got) != 2 || got[0].Operation != "open" || got[1].Operation != "close" || got[1].Address != want ||
		got[1].Sent != 6 || got[1].Received != 6 || got[1].Result != "ok" {
		t.Fatalf("events = %+v", got)
	}

	for name, want := range map[string]string{"other": "404", "pi": "400"} {
		if _, _, status := upgrade(t, server, name, name != "pi"); !strings.Contains(status, want) {
			t.Fatalf("%s: status = %q", name, status)
		}
	}
}

func TestRelayReportsAMachineItCannotReach(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	events := &recorder{done: make(chan struct{}, 4)}
	server := httptest.NewServer(Handler(NewRelay(ProjectConfig{Hosts: map[string]Host{"pi": {Host: "127.0.0.1", Port: port, User: "pi", HostKeys: []string{hostKey}}}}), events.record))
	defer server.Close()
	if _, _, status := upgrade(t, server, "pi", true); !strings.Contains(status, "502") {
		t.Fatalf("status = %q", status)
	}
	if got := events.wait(t); len(got) != 1 || got[0].Operation != "close" || !strings.Contains(got[0].Result, "refused") {
		t.Fatalf("events = %+v", got)
	}
}

// A session ends with the attach: Close ends what the relay holds.
func TestRelayEndsItsSessionsWhenClosed(t *testing.T) {
	address, port := machine(t)
	events := &recorder{done: make(chan struct{}, 4)}
	relay := NewRelay(ProjectConfig{Hosts: map[string]Host{"pi": {Host: address, Port: port, User: "pi", HostKeys: []string{hostKey}}}})
	server := httptest.NewServer(Handler(relay, events.record))
	defer server.Close()
	conn, reader, _ := upgrade(t, server, "pi", true)
	io.WriteString(conn, "a\n")
	reader.ReadString('\n')
	relay.Close()
	if got := events.wait(t); got[len(got)-1].Result != "ended with the attach" {
		t.Fatalf("events = %+v", got)
	}
	if _, _, status := upgrade(t, server, "pi", true); !strings.Contains(status, "503") {
		t.Fatalf("a closed relay still connects: %q", status)
	}
}

// The VM's ssh reaches the relay through isotty-ssh as its ProxyCommand,
// which carries the session on its standard input and output.
func TestProxyCommandCarriesTheSession(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	address, port := machine(t)
	relay := NewRelay(ProjectConfig{Hosts: map[string]Host{"pi": {Host: address, Port: port, User: "pi", HostKeys: []string{hostKey}}}})
	mux := http.NewServeMux()
	mux.Handle("/ssh/", http.StripPrefix("/ssh", Handler(relay, func(Event) {})))
	server := httptest.NewServer(mux)
	defer server.Close()
	gatewayPort := server.Listener.Addr().(*net.TCPAddr).Port
	script := filepath.Join(t.TempDir(), "isotty-ssh")
	if err := os.WriteFile(script, []byte(strings.ReplaceAll(clientScript, "__ISOTTY_GATEWAY_PORT__", strconv.Itoa(gatewayPort))), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", script, "connect", "pi")
	cmd.Stdin = strings.NewReader("one\ntwo\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil || string(out) != "ONE\nTWO\n" {
		t.Fatalf("out = %q, err = %v, stderr = %s", out, err, stderr.String())
	}
	cmd = exec.Command("python3", script, "connect", "nas")
	stderr.Reset()
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil || !strings.Contains(stderr.String(), "isotty host ssh add") {
		t.Fatalf("a machine not named: err = %v, stderr = %s", err, stderr.String())
	}
}

func TestSetupInputHoldsTheKeysAndTheConfig(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(KeyDir(home, "abc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(KeyPath(home, "abc", "pi"), []byte("PRIVATE"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := ProjectConfig{Hosts: map[string]Host{"pi": {Host: "raspberrypi.local", Port: 2222, User: "pi", HostKeys: []string{hostKey}}}}
	input, err := SetupInput(cfg, home, "abc")
	if err != nil {
		t.Fatal(err)
	}
	zipped, err := gzip.NewReader(bytes.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	archive := tar.NewReader(zipped)
	files := map[string]string{}
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(archive)
		files[header.Name] = string(data)
		if header.Typeflag == tar.TypeReg && header.Mode != 0o600 {
			t.Fatalf("%s has mode %o", header.Name, header.Mode)
		}
	}
	if files["isotty-hosts/pi"] != "PRIVATE" || files["isotty-hosts/known_hosts"] != "isotty-host-pi "+hostKey+"\n" {
		t.Fatalf("files = %v", files)
	}
	for _, want := range []string{"Host pi\n", "HostName raspberrypi.local\n", "Port 2222\n", "User pi\n", "ProxyCommand /usr/local/bin/isotty-ssh connect pi\n", "HostKeyAlias isotty-host-pi\n", "StrictHostKeyChecking yes\n", "IdentitiesOnly yes\n", "HostKeyAlgorithms ssh-ed25519\n"} {
		if !strings.Contains(files["isotty-hosts.conf"], want) {
			t.Fatalf("config lacks %q:\n%s", want, files["isotty-hosts.conf"])
		}
	}
	if command := SetupCommand([]string{"claude"}); strings.Contains(command, "PRIVATE") || !strings.Contains(command, `tar -xzf - -C "$tmp"`) {
		t.Fatalf("command = %s", command)
	}
	if _, err := SetupInput(ProjectConfig{Hosts: map[string]Host{"nas": cfg.Hosts["pi"]}}, home, "abc"); err == nil || !strings.Contains(err.Error(), "isotty host ssh add nas") {
		t.Fatalf("a machine without a key: %v", err)
	}
}

// An old machine's RSA host key is verified with the SHA-1 algorithm it
// alone offers, which a current ssh no longer tries by itself.
func TestHostKeyAlgorithmsFollowTheRecordedKeys(t *testing.T) {
	h := Host{HostKeys: []string{"ecdsa-sha2-nistp256 AAAA", "ssh-rsa AAAA", "ecdsa-sha2-nistp256 BBBB"}}
	if got := h.HostKeyAlgorithms(); got != "ecdsa-sha2-nistp256,rsa-sha2-512,rsa-sha2-256,ssh-rsa" {
		t.Fatalf("algorithms = %s", got)
	}
}

// The synced file counts only as pinned: the VM can rewrite it to point
// the relay at another machine.
func TestTheSyncedConfigCountsOnlyAsPinned(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	cfg := ProjectConfig{Hosts: map[string]Host{"pi": {Host: "raspberrypi.local", Port: 22, User: "pi", HostKeys: []string{hostKey}}}}
	if err := Save(project, "abc", home, cfg); err != nil {
		t.Fatal(err)
	}
	synced, ok, err := LoadProjectConfig(project)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, err := VerifyPinned(home, "abc", synced); err != nil {
		t.Fatal(err)
	}
	edited := ProjectConfig{Hosts: map[string]Host{"pi": {Host: "router.local", Port: 22, User: "pi", HostKeys: []string{hostKey}}}}
	if _, err := VerifyPinned(home, "abc", edited); err == nil || !strings.Contains(err.Error(), "differs") {
		t.Fatalf("an edited file: %v", err)
	}
	for _, bad := range []string{
		"hosts:\n  Pi:\n    host: a\n    port: 22\n    user: pi\n    host_keys: [\"" + hostKey + "\"]\n",
		"hosts:\n  pi:\n    host: -oProxyCommand=x\n    port: 22\n    user: pi\n    host_keys: [\"" + hostKey + "\"]\n",
		"hosts:\n  pi:\n    host: a\n    port: 22\n    user: pi\n    host_keys: []\n",
		"hosts:\n  pi:\n    host: a\n    port: 0\n    user: pi\n    host_keys: [\"" + hostKey + "\"]\n",
	} {
		if _, err := ParseProjectConfig([]byte(bad)); err == nil {
			t.Fatalf("accepted:\n%s", bad)
		}
	}
	if err := Save(project, "abc", home, ProjectConfig{}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := LoadProjectConfig(project); ok {
		t.Fatal("no machine left the file")
	}
	if _, err := os.Stat(pin.Path(home, "abc", PinKind)); !os.IsNotExist(err) {
		t.Fatalf("no machine left the pin: %v", err)
	}
}
