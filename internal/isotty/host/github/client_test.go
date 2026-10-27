package github

import (
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestClient runs the VM client against the relay, with fake upstreams, the
// way `isotty up` installs it: the gateway port in the script is the test
// relay's port.
func TestClient(t *testing.T) {
	for _, tool := range []string{"python3", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available", tool)
		}
	}
	var events []WriteEvent
	pins := ProjectConfig{Repos: map[string]Access{"Me/Project": AccessAdmin, "org/lib": AccessRead}}
	relay, _, api := newTestRelay(t, pins, func(e WriteEvent) error { events = append(events, e); return nil })
	api.answers["PATCH /repos/me/project"] = jsonAnswer(`{"full_name":"me/project"}`)
	api.answers["GET /repos/me/project/actions/permissions"] = jsonAnswer(`{"enabled":true,"allowed_actions":"all"}`)
	// A public repository the token cannot push to: GitHub leaves the merge
	// settings out, and answers its protection and Pages with 404.
	api.answers["GET /repos/pub/lic"] = jsonAnswer(`{"full_name":"pub/lic","visibility":"public","archived":false,"default_branch":"main","has_issues":true,"has_wiki":false,"has_projects":true}`)
	api.answers["GET /repos/pub/lic/branches/main/protection"] = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) }
	api.answers["GET /repos/pub/lic/rules/branches/main"] = jsonAnswer(`[]`)
	api.answers["GET /repos/pub/lic/actions/permissions"] = jsonAnswer(`{"enabled":true,"allowed_actions":"all"}`)
	api.answers["GET /repos/pub/lic/environments"] = jsonAnswer(`{"environments":[]}`)
	api.answers["GET /repos/pub/lic/pages"] = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) }
	api.answers["GET /repos/pub/lic/actions/secrets"] = jsonAnswer(`{"secrets":[]}`)
	api.answers["GET /repos/me/project/actions/permissions/workflow"] = jsonAnswer(`{"default_workflow_permissions":"read","can_approve_pull_request_reviews":false}`)
	api.answers["GET /repos/me/project/pulls"] = jsonAnswer(`[{"number":1,"state":"open","draft":false,"head":{"ref":"feat"},"title":"Add feature"}]`)
	api.answers["POST /repos/me/project/pulls"] = jsonAnswer(`{"number":2,"html_url":"https://github.com/me/project/pull/2"}`)
	api.answers["GET /repos/me/project/actions/runs"] = jsonAnswer(`{"workflow_runs":[{"id":5,"status":"completed","conclusion":"failure","name":"CI","path":".github/workflows/ci.yml","head_branch":"feat","event":"push","created_at":"2026-10-26T00:00:00Z"}]}`)
	api.answers["GET /repos/me/project/actions/runs/5/jobs"] = jsonAnswer(`{"jobs":[{"id":50,"name":"test","status":"completed","conclusion":"failure","steps":[{"number":3,"name":"go test","conclusion":"failure"}]}]}`)
	api.answers["GET /repos/me/project/actions/jobs/50/logs"] = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("FAIL: TestX\n"))
	}

	dir := t.TempDir()
	port := strings.TrimPrefix(relay.URL, "http://127.0.0.1:")
	if _, err := strconv.Atoi(port); err != nil {
		t.Fatalf("relay URL %s is not on 127.0.0.1", relay.URL)
	}
	script := filepath.Join(dir, "isotty-github")
	if err := os.WriteFile(script, []byte(strings.ReplaceAll(clientScript, "__ISOTTY_GATEWAY_PORT__", port)), 0o755); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(dir, "work")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", work},
		{"-C", work, "remote", "add", "origin", "https://github.com/me/project.git"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run := func(stdin string, args ...string) (string, int) {
		cmd := exec.Command("python3", append([]string{"-I", script}, args...)...)
		cmd.Dir = work
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		var exitErr *exec.ExitError
		if err != nil && !asExit(err, &exitErr) {
			t.Fatalf("%v: %v", args, err)
		}
		return string(out), cmd.ProcessState.ExitCode()
	}

	if out, code := run("", "status"); code != 0 || !strings.Contains(out, "Me/Project\tadmin") {
		t.Fatalf("status: %d\n%s", code, out)
	}
	if out, code := run("", "pr", "list"); code != 0 || !strings.Contains(out, "#1\topen\tfeat\tAdd feature") {
		t.Fatalf("pr list: %d\n%s", code, out)
	}
	if out, code := run("why\n", "pr", "create", "--title", "T", "--base", "main", "--head", "feat"); code != 0 || !strings.Contains(out, "#2 https://github.com/me/project/pull/2") {
		t.Fatalf("pr create: %d\n%s", code, out)
	}
	if c := api.last(); !strings.Contains(c.Body, `"head": "feat"`) || !strings.Contains(c.Body, `"body": "why\n"`) {
		t.Fatalf("pr create upstream body = %s", c.Body)
	}
	if out, code := run("", "run", "list"); code != 0 || !strings.Contains(out, "5\tcompleted/failure\tCI\tfeat") {
		t.Fatalf("run list: %d\n%s", code, out)
	}
	if out, code := run("", "run", "log", "5"); code != 0 || !strings.Contains(out, "===== job 50 test") || !strings.Contains(out, "FAIL: TestX") {
		t.Fatalf("run log: %d\n%s", code, out)
	}
	if out, code := run("", "pr", "list", "--repo", "other/repo"); code == 0 || !strings.Contains(out, "not among the repositories pinned") {
		t.Fatalf("unpinned repo: %d\n%s", code, out)
	}
	if out, code := run("", "push", "-u", "https://github.com/other/repo.git", "HEAD"); code == 0 || !strings.Contains(out, "403") || !strings.Contains(out, "isotty-github status") {
		t.Fatalf("push to unpinned repo: %d\n%s", code, out)
	}
	if out, code := run("", "repo", "view", "--repo", "pub/lic"); code != 0 || !strings.Contains(out, "pub/lic: public") || !strings.Contains(out, "merge: not shown") || !strings.Contains(out, "pages: none") {
		t.Fatalf("repo view of an outside public repository: %d\n%s", code, out)
	}
	if out, code := run("", "repo", "edit", "--set", "delete_branch_on_merge=true", "--set", "description=A relay"); code != 0 || !strings.Contains(out, "me/project updated") {
		t.Fatalf("repo edit: %d\n%s", code, out)
	}
	if c := api.last(); c.Method != "PATCH" || !strings.Contains(c.Body, `"delete_branch_on_merge": true`) || !strings.Contains(c.Body, `"description": "A relay"`) {
		t.Fatalf("repo edit upstream call = %+v", c)
	}
	if out, code := run("", "repo", "edit", "--set", "private=true"); code == 0 || !strings.Contains(out, "private") || !strings.Contains(out, "not accepted") {
		t.Fatalf("repo edit private: %d\n%s", code, out)
	}
	if out, code := run("", "repo", "protect", "release/v1", "--require-reviews", "1", "--require-checks", "ci,lint", "--strict", "--enforce-admins"); code != 0 || !strings.Contains(out, "release/v1: protection set") {
		t.Fatalf("repo protect: %d\n%s", code, out)
	}
	if c := api.last(); c.Method != "PUT" || c.EscapedPath != "/repos/me/project/branches/release%2Fv1/protection" || !strings.Contains(c.Body, `"contexts": ["ci", "lint"]`) || !strings.Contains(c.Body, `"required_approving_review_count": 1`) || !strings.Contains(c.Body, `"restrictions": null`) {
		t.Fatalf("protect upstream call = %+v", c)
	}
	if out, code := run("", "repo", "actions", "--workflow-permissions", "read", "--allowed", "selected"); code != 0 || !strings.Contains(out, "actions: enabled=True allowed_actions=all workflow_permissions=read") {
		t.Fatalf("repo actions: %d\n%s", code, out)
	}
	want := []WriteEvent{
		{Operation: "pr-create", Repo: "me/project", Target: "2"},
		{Operation: "repo-edit", Repo: "me/project"},
		{Operation: "protection-set", Repo: "me/project", Target: "release/v1"},
		{Operation: "actions-permissions", Repo: "me/project"},
		{Operation: "actions-permissions", Repo: "me/project"},
	}
	if len(events) != len(want) {
		t.Fatalf("events = %+v", events)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("event %d = %+v, want %+v", i, events[i], want[i])
		}
	}
}

func jsonAnswer(body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}
}

func asExit(err error, target **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError)
	if ok {
		*target = e
	}
	return ok
}

// TestClientPushThroughRelay pushes with the real git client through the
// relay to a real `git http-backend`, so the smart HTTP forwarding is
// checked against git itself rather than a fake.
func TestClientPushThroughRelay(t *testing.T) {
	for _, tool := range []string{"python3", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available", tool)
		}
	}
	execPath, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Fatal(err)
	}
	backend := filepath.Join(strings.TrimSpace(string(execPath)), "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		t.Skip("git http-backend not available")
	}

	root := t.TempDir()
	bare := filepath.Join(root, "me", "project.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", bare, "config", "http.receivepack", "true").CombinedOutput(); err != nil {
		t.Fatalf("git config: %v\n%s", err, out)
	}
	upstream := httptest.NewServer(&cgi.Handler{
		Path: backend,
		Env:  []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1", "REMOTE_USER=x-access-token"},
	})
	defer upstream.Close()

	var events []WriteEvent
	relay := httptest.NewServer(http.StripPrefix("/github", newHandler("tok", testPins, func(e WriteEvent) error { events = append(events, e); return nil }, upstream.URL, "http://api.invalid")))
	defer relay.Close()
	port := strings.TrimPrefix(relay.URL, "http://127.0.0.1:")
	script := filepath.Join(root, "isotty-github")
	if err := os.WriteFile(script, []byte(strings.ReplaceAll(clientScript, "__ISOTTY_GATEWAY_PORT__", port)), 0o755); err != nil {
		t.Fatal(err)
	}

	work := filepath.Join(root, "work")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", work},
		{"-C", work, "config", "user.email", "t@example.com"},
		{"-C", work, "config", "user.name", "t"},
		{"-C", work, "remote", "add", "origin", "git@github.com:me/project.git"},
		{"-C", work, "commit", "-q", "--allow-empty", "-m", "first"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	push := exec.Command("python3", "-I", script, "push", "-u", "origin", "HEAD")
	push.Dir = work
	if out, err := push.CombinedOutput(); err != nil {
		t.Fatalf("push through relay: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", bare, "rev-parse", "main").CombinedOutput(); err != nil || len(strings.TrimSpace(string(out))) != 40 {
		t.Fatalf("bare main after push: %v %s", err, out)
	}
	if len(events) != 1 || events[0] != (WriteEvent{Operation: "push", Repo: "me/project"}) {
		t.Fatalf("events = %+v", events)
	}
	fetch := exec.Command("python3", "-I", script, "fetch", "origin")
	fetch.Dir = work
	if out, err := fetch.CombinedOutput(); err != nil {
		t.Fatalf("fetch through relay: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", work, "remote", "get-url", "origin").CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "git@github.com:me/project.git" {
		t.Fatalf("remote URL changed: %v %s", err, out)
	}
}
