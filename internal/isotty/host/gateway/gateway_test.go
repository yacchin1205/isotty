package gateway

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"testing"
	"time"
)

func TestServeAndClose(t *testing.T) {
	gateway, err := Serve(http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", gateway.Port()))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if err := gateway.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

// TestPortFreeCommandWaitsForAListenerToGo guards what an attach asks the VM
// before it forwards the gateway: a port something listens on is held, and
// the wait ends as soon as the listener is gone.
func TestPortFreeCommandWaitsForAListenerToGo(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	exitCode := func(wait time.Duration) int {
		out, err := exec.Command("sh", "-c", portFreeCommand(port, wait)).CombinedOutput()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return 0
	}

	if code := exitCode(0); code != PortHeld {
		t.Fatalf("exit code with a listener on the port = %d, want %d", code, PortHeld)
	}
	if code := exitCode(time.Second); code != PortHeld {
		t.Fatalf("exit code after waiting out a listener that stays = %d, want %d", code, PortHeld)
	}
	time.AfterFunc(time.Second, func() { listener.Close() })
	started := time.Now()
	if code := exitCode(time.Minute); code != 0 {
		t.Fatalf("exit code once the listener is gone = %d, want 0", code)
	}
	if waited := time.Since(started); waited > 30*time.Second {
		t.Fatalf("the wait took %s although the listener went after a second", waited)
	}
}
