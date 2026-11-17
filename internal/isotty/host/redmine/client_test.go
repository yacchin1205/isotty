package redmine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestClient runs the VM client against the relay, with the fakes, the
// way `isotty up` installs it: the gateway port in the script is the test
// relay's port.
func TestClient(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	fakeTools(t)
	tr := newTestRelay(t, infraPins)
	port := strings.TrimPrefix(tr.server.URL, "http://127.0.0.1:")
	if _, err := strconv.Atoi(port); err != nil {
		t.Fatalf("test relay URL %s has no port", tr.server.URL)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "isotty-redmine")
	if err := os.WriteFile(script, []byte(strings.ReplaceAll(clientScript, "__ISOTTY_GATEWAY_PORT__", port)), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, int) {
		cmd := exec.Command("python3", append([]string{"-I", script}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		code := 0
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return string(out), code
	}

	if out, code := run("status"); code != 0 || !strings.Contains(out, "infra\tInfrastructure") {
		t.Fatalf("status: %d\n%s", code, out)
	}
	if out, code := run("issue", "list"); code != 0 || !strings.Contains(out, "| 101 | - | New | - | [name]さんがログインできない | "+person(15)+" |") {
		t.Fatalf("issue list: %d\n%s", code, out)
	}
	last := tr.redmine.calls[len(tr.redmine.calls)-1]
	if !strings.Contains(last, "status_id=open") || !strings.Contains(last, "project_id=infra") {
		t.Fatalf("issue list upstream call: %s", last)
	}
	out, code := run("issue", "view", "101")
	for _, want := range []string{"# #101 [name]さんがログインできない", "- author: " + person(12), "- assigned: " + person(15), "[email] に連絡するか [phone] に電話。[name]さんが対応する。トークンは [secret]", "### " + person(20) + " on 2026-11-10T00:00:00Z", "[name]さんの件は確認済み。", "- 5: log.txt (text/plain, 0 bytes)"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("issue view lacks %q: %d\n%s", want, code, out)
		}
	}
	if strings.Contains(out, "山田") || strings.Contains(out, token) {
		t.Fatalf("issue view leaks:\n%s", out)
	}
	if out, code := run("issue", "related", "101"); code != 0 || !strings.Contains(out, "#101\t-\t[name]さんがログインできない") {
		t.Fatalf("issue related: %d\n%s", code, out)
	}
	if out, code := run("wiki", "list"); code != 0 || !strings.Contains(out, "Setup\tv3\t") {
		t.Fatalf("wiki list: %d\n%s", code, out)
	}
	if out, code := run("wiki", "view", "Setup"); code != 0 || !strings.Contains(out, "# Setup (v3, "+person(12)+", -)") || !strings.Contains(out, "[name]さん（[email]）に聞く。") {
		t.Fatalf("wiki view: %d\n%s", code, out)
	}
	if out, code := run("attachment", "get", "5", "--issue", "101"); code != 0 || !strings.Contains(out, "log.txt: ") {
		t.Fatalf("attachment get: %d\n%s", code, out)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "log.txt")); err != nil || string(data) != "[name]さん <[email]> が再起動した\n" {
		t.Fatalf("downloaded attachment: %q, %v", data, err)
	}
	if out, code := run("attachment", "get", "6", "--issue", "101"); code != 0 || !strings.Contains(out, "spec.pdf.txt: ") || !strings.Contains(out, "the text read on the host") {
		t.Fatalf("pdf attachment get: %d\n%s", code, out)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "spec.pdf.txt")); err != nil || string(data) != "[name]さんの資料\fpage 2: [name]さん [phone]\n" {
		t.Fatalf("downloaded pdf text: %q, %v", data, err)
	}
	if out, code := run("issue", "view", "202"); code == 0 || !strings.Contains(out, "belongs to a project that is not pinned") {
		t.Fatalf("issue of another project: %d\n%s", code, out)
	}
	if out, code := run("issue", "list", "--project", "other"); code == 0 || !strings.Contains(out, "not among the projects pinned") {
		t.Fatalf("unpinned project: %d\n%s", code, out)
	}
}
