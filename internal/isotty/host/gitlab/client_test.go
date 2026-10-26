package gitlab

import (
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yazawa/isotty/internal/isotty/host/forge"
)

func requireTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"python3", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available", tool)
		}
	}
}

func writeClient(t *testing.T, dir, relayURL string) string {
	t.Helper()
	port := strings.TrimPrefix(relayURL, "http://127.0.0.1:")
	script := filepath.Join(dir, "isotty-gitlab")
	if err := os.WriteFile(script, []byte(strings.ReplaceAll(clientScript, "__ISOTTY_GATEWAY_PORT__", port)), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

func gitIn(t *testing.T, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestClient runs the VM client against the relay, with a fake instance,
// the way `isotty up` installs it: the gateway port in the script is the
// test relay's port.
func TestClient(t *testing.T) {
	requireTools(t)
	var events []WriteEvent
	relay, upstream, host := newTestRelay(t, map[string]forge.Access{"group/sub/app": forge.AccessWrite}, func(e WriteEvent) error { events = append(events, e); return nil })
	jsonAnswer := func(body string) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
		}
	}
	upstream.answers["GET /api/v4/projects/group%2Fsub%2Fapp/merge_requests"] = jsonAnswer(`[{"iid":1,"state":"opened","draft":false,"source_branch":"feat","title":"Add feature"}]`)
	upstream.answers["POST /api/v4/projects/group%2Fsub%2Fapp/merge_requests"] = jsonAnswer(`{"iid":2,"web_url":"https://gitlab.example.org/group/sub/app/-/merge_requests/2"}`)
	upstream.answers["GET /api/v4/projects/group%2Fsub%2Fapp/pipelines"] = jsonAnswer(`[{"id":5,"status":"failed","ref":"feat","sha":"0123456789abcdef","source":"push","created_at":"2026-10-26T00:00:00Z"}]`)
	upstream.answers["GET /api/v4/projects/group%2Fsub%2Fapp/pipelines/5/jobs"] = jsonAnswer(`[{"id":50,"name":"test","status":"failed","stage":"test"}]`)
	upstream.answers["GET /api/v4/projects/group%2Fsub%2Fapp/jobs/50/trace"] = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("FAIL: TestX\n"))
	}

	dir := t.TempDir()
	script := writeClient(t, dir, relay.URL)
	work := filepath.Join(dir, "work")
	gitIn(t, "init", "-q", "-b", "main", work)
	gitIn(t, "-C", work, "remote", "add", "origin", "ssh://git@"+host+":10022/group/sub/app.git")
	run := func(stdin string, args ...string) (string, int) {
		cmd := exec.Command("python3", append([]string{"-I", script}, args...)...)
		cmd.Dir = work
		cmd.Stdin = strings.NewReader(stdin)
		out, _ := cmd.CombinedOutput()
		return string(out), cmd.ProcessState.ExitCode()
	}

	if out, code := run("", "status"); code != 0 || !strings.Contains(out, host+"/group/sub/app\twrite") {
		t.Fatalf("status: %d\n%s", code, out)
	}
	if out, code := run("", "mr", "list"); code != 0 || !strings.Contains(out, "!1\topened\tfeat\tAdd feature") {
		t.Fatalf("mr list: %d\n%s", code, out)
	}
	if out, code := run("why\n", "mr", "create", "--title", "T", "--target", "main", "--source", "feat"); code != 0 || !strings.Contains(out, "!2 https://gitlab.example.org/group/sub/app/-/merge_requests/2") {
		t.Fatalf("mr create: %d\n%s", code, out)
	}
	if c := upstream.last(); !strings.Contains(c.Body, `"source_branch": "feat"`) || !strings.Contains(c.Body, `"description": "why\n"`) {
		t.Fatalf("mr create upstream body = %s", c.Body)
	}
	if out, code := run("", "pipeline", "list"); code != 0 || !strings.Contains(out, "5\tfailed\tfeat\t0123456789ab") {
		t.Fatalf("pipeline list: %d\n%s", code, out)
	}
	if out, code := run("", "pipeline", "log", "5"); code != 0 || !strings.Contains(out, "===== job 50 test") || !strings.Contains(out, "FAIL: TestX") {
		t.Fatalf("pipeline log: %d\n%s", code, out)
	}
	if out, code := run("", "mr", "list", "--project", host+"/other/repo"); code == 0 || !strings.Contains(out, "not among the repositories pinned") {
		t.Fatalf("unpinned project: %d\n%s", code, out)
	}
	if out, code := run("", "push", "-u", "https://"+host+"/other/repo.git", "HEAD"); code == 0 || !strings.Contains(out, "403") || !strings.Contains(out, "isotty-gitlab status") {
		t.Fatalf("push to unpinned project: %d\n%s", code, out)
	}
	if len(events) != 1 || events[0].Operation != "mr-create" || events[0].Target != "2" {
		t.Fatalf("events = %+v", events)
	}
}

// TestClientPushThroughRelay pushes with the real git client through the
// relay to a real `git http-backend` standing in for the instance, so the
// smart HTTP forwarding is checked against git itself rather than a fake.
func TestClientPushThroughRelay(t *testing.T) {
	requireTools(t)
	execPath, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Fatal(err)
	}
	backend := filepath.Join(strings.TrimSpace(string(execPath)), "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		t.Skip("git http-backend not available")
	}

	root := t.TempDir()
	bare := filepath.Join(root, "group", "app.git")
	gitIn(t, "init", "-q", "--bare", "-b", "main", bare)
	gitIn(t, "-C", bare, "config", "http.receivepack", "true")
	var auth string
	instance := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		(&cgi.Handler{
			Path: backend,
			Env:  []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1", "REMOTE_USER=oauth2"},
		}).ServeHTTP(w, r)
	}))
	defer instance.Close()
	host := mustHost(t, instance.URL)

	var events []WriteEvent
	hostCfg := HostConfig{Instances: []Instance{{URL: instance.URL, Token: "glpat-x"}}}
	cfg := ProjectConfig{Repos: map[string]forge.Access{host + "/group/app": forge.AccessWrite}}
	relay := httptest.NewServer(http.StripPrefix("/gitlab", Handler(hostCfg, cfg, func(e WriteEvent) error { events = append(events, e); return nil })))
	defer relay.Close()
	script := writeClient(t, root, relay.URL)

	work := filepath.Join(root, "work")
	gitIn(t, "init", "-q", "-b", "main", work)
	gitIn(t, "-C", work, "config", "user.email", "t@example.com")
	gitIn(t, "-C", work, "config", "user.name", "t")
	gitIn(t, "-C", work, "remote", "add", "origin", "ssh://git@"+host+":10022/group/app.git")
	gitIn(t, "-C", work, "commit", "-q", "--allow-empty", "-m", "first")
	push := exec.Command("python3", "-I", script, "push", "-u", "origin", "HEAD")
	push.Dir = work
	if out, err := push.CombinedOutput(); err != nil {
		t.Fatalf("push through relay: %v\n%s", err, out)
	}
	if auth != "Basic b2F1dGgyOmdscGF0LXg=" {
		t.Fatalf("instance saw Authorization %q", auth)
	}
	if out, err := exec.Command("git", "-C", bare, "rev-parse", "main").CombinedOutput(); err != nil || len(strings.TrimSpace(string(out))) != 40 {
		t.Fatalf("bare main after push: %v %s", err, out)
	}
	if len(events) != 1 || events[0] != (WriteEvent{Operation: "push", Repo: host + "/group/app"}) {
		t.Fatalf("events = %+v", events)
	}
	fetch := exec.Command("python3", "-I", script, "fetch", "origin")
	fetch.Dir = work
	if out, err := fetch.CombinedOutput(); err != nil {
		t.Fatalf("fetch through relay: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", work, "remote", "get-url", "origin").CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "ssh://git@"+host+":10022/group/app.git" {
		t.Fatalf("remote URL changed: %v %s", err, out)
	}
}
