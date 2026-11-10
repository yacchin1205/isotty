package bitbucket

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

const (
	plainPipelineUUID = "{1f4a2c3e-0000-4000-8000-0123456789ab}"
	plainStepUUID     = "{ffffffff-1111-4222-8333-444444444444}"
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
	pins := ProjectConfig{Repos: map[string]Access{"My-Team/App": AccessAdmin, "org/lib": AccessRead}}
	relay, _, api := newTestRelay(t, pins, func(e WriteEvent) error { events = append(events, e); return nil })
	api.answers["GET /2.0/repositories/my-team/app"] = jsonAnswer(`{"full_name":"my-team/app","is_private":true,"mainbranch":{"name":"main"},"project":{"key":"APP"},"fork_policy":"no_public_forks","language":"go","enforced_signed_commits":false}`)
	api.answers["GET /2.0/repositories/my-team/app/branch-restrictions"] = jsonAnswer(`{"values":[{"id":31,"kind":"push","branch_match_kind":"glob","pattern":"main","users":[{"display_name":"Someone"}],"groups":[]},{"id":32,"kind":"require_approvals_to_merge","branch_match_kind":"branching_model","branch_type":"production","value":1}]}`)
	api.answers["PUT /2.0/repositories/my-team/app"] = jsonAnswer(`{"full_name":"my-team/app"}`)
	// The pull request list spans two pages, the second named by an
	// absolute URL of api.bitbucket.org as Bitbucket does it.
	api.answers["GET /2.0/repositories/my-team/app/pullrequests"] = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			w.Write([]byte(`{"values":[{"id":2,"state":"OPEN","title":"Second","source":{"branch":{"name":"other"}}}]}`))
			return
		}
		w.Write([]byte(`{"values":[{"id":1,"state":"OPEN","draft":true,"title":"Add feature","source":{"branch":{"name":"feat"}}}],"next":"https://api.bitbucket.org/2.0/repositories/my-team/app/pullrequests?state=OPEN&pagelen=50&page=2"}`))
	}
	api.answers["POST /2.0/repositories/my-team/app/pullrequests"] = jsonAnswer(`{"id":3,"links":{"html":{"href":"https://bitbucket.org/my-team/app/pull-requests/3"}}}`)
	api.answers["GET /2.0/repositories/my-team/app/pullrequests/1/diff"] = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/2.0/repositories/my-team/app/diff/abc..def", http.StatusFound)
	}
	api.answers["GET /2.0/repositories/my-team/app/diff/abc..def"] = rawAnswer("text/plain", "diff --git a/x b/x\n")
	api.answers["GET /2.0/repositories/my-team/app/pullrequests/1/statuses"] = jsonAnswer(`{"values":[{"key":"ci","name":"Pipeline #5","state":"FAILED","url":"https://bitbucket.org/my-team/app/pipelines/results/5"}]}`)
	api.answers["GET /2.0/repositories/my-team/app/pipelines"] = jsonAnswer(`{"values":[{"uuid":"` + plainPipelineUUID + `","build_number":5,"state":{"name":"COMPLETED","result":{"name":"FAILED"}},"target":{"ref_type":"branch","ref_name":"feat"},"trigger":{"name":"PUSH"},"created_on":"2026-11-10T00:00:00Z"}]}`)
	api.answers["GET /2.0/repositories/my-team/app/pipelines/"+plainPipelineUUID] = jsonAnswer(`{"uuid":"` + plainPipelineUUID + `","build_number":5,"state":{"name":"COMPLETED","result":{"name":"FAILED"}},"target":{"ref_type":"branch","ref_name":"feat"},"trigger":{"name":"PUSH"},"created_on":"2026-11-10T00:00:00Z","completed_on":"2026-11-10T00:01:00Z","build_seconds_used":60}`)
	api.answers["GET /2.0/repositories/my-team/app/pipelines/"+plainPipelineUUID+"/steps"] = jsonAnswer(`{"values":[{"uuid":"` + plainStepUUID + `","name":"test","started_on":"2026-11-10T00:00:10Z","state":{"name":"COMPLETED","result":{"name":"FAILED"}}},{"uuid":"{eeeeeeee-1111-4222-8333-444444444444}","name":"deploy","state":{"name":"PENDING"}}]}`)
	api.answers["GET /2.0/repositories/my-team/app/pipelines/"+plainPipelineUUID+"/steps/"+plainStepUUID+"/log"] = rawAnswer("application/octet-stream", "FAIL: TestX\n")
	api.answers["POST /2.0/repositories/my-team/app/branch-restrictions"] = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":33,"kind":"require_passing_builds_to_merge","branch_match_kind":"glob","pattern":"release/*","value":1}`))
	}

	dir := t.TempDir()
	port := strings.TrimPrefix(relay.URL, "http://127.0.0.1:")
	if _, err := strconv.Atoi(port); err != nil {
		t.Fatalf("relay URL %s is not on 127.0.0.1", relay.URL)
	}
	script := filepath.Join(dir, "isotty-bitbucket")
	if err := os.WriteFile(script, []byte(strings.ReplaceAll(clientScript, "__ISOTTY_GATEWAY_PORT__", port)), 0o755); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(dir, "work")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", work},
		{"-C", work, "remote", "add", "origin", "https://someone@bitbucket.org/my-team/app.git"},
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
		if _, ok := err.(*exec.ExitError); err != nil && !ok {
			t.Fatalf("%v: %v", args, err)
		}
		return string(out), cmd.ProcessState.ExitCode()
	}

	if out, code := run("", "status"); code != 0 || !strings.Contains(out, "My-Team/App\tadmin") {
		t.Fatalf("status: %d\n%s", code, out)
	}
	if out, code := run("", "pr", "list"); code != 0 || !strings.Contains(out, "#1\tOPEN (draft)\tfeat\tAdd feature") || !strings.Contains(out, "#2\tOPEN\tother\tSecond") {
		t.Fatalf("pr list: %d\n%s", code, out)
	}
	if out, code := run("why\n", "pr", "create", "--title", "T", "--destination", "main", "--source", "feat", "--draft"); code != 0 || !strings.Contains(out, "#3 https://bitbucket.org/my-team/app/pull-requests/3") {
		t.Fatalf("pr create: %d\n%s", code, out)
	}
	if c := api.last(); !strings.Contains(c.Body, `"source": {"branch": {"name": "feat"}}`) || !strings.Contains(c.Body, `"destination": {"branch": {"name": "main"}}`) || !strings.Contains(c.Body, `"description": "why\n"`) || !strings.Contains(c.Body, `"draft": true`) {
		t.Fatalf("pr create upstream body = %s", c.Body)
	}
	if out, code := run("", "pr", "view", "1", "--diff"); code != 0 || out != "diff --git a/x b/x\n" {
		t.Fatalf("pr view --diff: %d\n%s", code, out)
	}
	if out, code := run("", "pr", "checks", "1"); code != 0 || !strings.Contains(out, "Pipeline #5\tFAILED\thttps://bitbucket.org/my-team/app/pipelines/results/5") {
		t.Fatalf("pr checks: %d\n%s", code, out)
	}
	if out, code := run("", "pipeline", "list"); code != 0 || !strings.Contains(out, "#5\tCOMPLETED/FAILED\tfeat\t"+plainPipelineUUID) {
		t.Fatalf("pipeline list: %d\n%s", code, out)
	}
	if out, code := run("", "pipeline", "view", "5"); code != 0 || !strings.Contains(out, "pipeline #5 "+plainPipelineUUID) || !strings.Contains(out, "step "+plainStepUUID+"\tCOMPLETED/FAILED\ttest") {
		t.Fatalf("pipeline view by build number: %d\n%s", code, out)
	}
	if out, code := run("", "pipeline", "log", "1f4a2c3e-0000-4000-8000-0123456789ab"); code != 0 || !strings.Contains(out, "===== step "+plainStepUUID+" test (COMPLETED/FAILED)") || !strings.Contains(out, "FAIL: TestX") || !strings.Contains(out, "deploy (PENDING)\n(the step has not started)") {
		t.Fatalf("pipeline log by bare uuid: %d\n%s", code, out)
	}
	if c := api.last(); c.EscapedPath != "/2.0/repositories/my-team/app/pipelines/"+pipelineUUID+"/steps/"+stepUUID+"/log" {
		t.Fatalf("step log upstream path = %q; the pending step has no log to ask for", c.EscapedPath)
	}
	if out, code := run("", "pr", "list", "--repo", "other/repo"); code == 0 || !strings.Contains(out, "not among the repositories pinned") {
		t.Fatalf("unpinned repo: %d\n%s", code, out)
	}
	if out, code := run("", "push", "-u", "https://bitbucket.org/other/repo.git", "HEAD"); code == 0 || !strings.Contains(out, "403") || !strings.Contains(out, "isotty-bitbucket status") {
		t.Fatalf("push to unpinned repo: %d\n%s", code, out)
	}
	if out, code := run("", "repo", "view"); code != 0 || !strings.Contains(out, "my-team/app: private") || !strings.Contains(out, "main branch: main") || !strings.Contains(out, "31\tpush\tmain allowed: Someone") || !strings.Contains(out, "32\trequire_approvals_to_merge\tproduction branches value=1") {
		t.Fatalf("repo view: %d\n%s", code, out)
	}
	if out, code := run("", "repo", "edit", "--set", "fork_policy=no_forks", "--set", "description=A relay"); code != 0 || !strings.Contains(out, "my-team/app updated") {
		t.Fatalf("repo edit: %d\n%s", code, out)
	}
	if c := api.last(); c.Method != "PUT" || !strings.Contains(c.Body, `"fork_policy": "no_forks"`) || !strings.Contains(c.Body, `"description": "A relay"`) {
		t.Fatalf("repo edit upstream call = %+v", c)
	}
	if out, code := run("", "repo", "edit", "--set", "is_private=false"); code == 0 || !strings.Contains(out, "is_private") || !strings.Contains(out, "not accepted") {
		t.Fatalf("repo edit is_private: %d\n%s", code, out)
	}
	if out, code := run("", "repo", "restriction", "create", "--kind", "require_passing_builds_to_merge", "--pattern", "release/*", "--value", "1"); code != 0 || !strings.Contains(out, "restriction created: 33\trequire_passing_builds_to_merge\trelease/* value=1") {
		t.Fatalf("restriction create: %d\n%s", code, out)
	}
	if c := api.last(); !strings.Contains(c.Body, `"branch_match_kind": "glob"`) || !strings.Contains(c.Body, `"pattern": "release/*"`) || !strings.Contains(c.Body, `"value": 1`) {
		t.Fatalf("restriction create upstream body = %s", c.Body)
	}
	want := []WriteEvent{
		{Operation: "pr-create", Repo: "my-team/app", Target: "3"},
		{Operation: "repo-edit", Repo: "my-team/app"},
		{Operation: "restriction-create", Repo: "my-team/app", Target: "33"},
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

// TestClientPushThroughRelay pushes with the real git client through the
// relay to a real `git http-backend`, so the smart HTTP forwarding is
// checked against git itself rather than a fake; the remote carries the
// user's name, as Bitbucket's clone dialog writes it.
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
	bare := filepath.Join(root, "my-team", "app.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", bare, "config", "http.receivepack", "true").CombinedOutput(); err != nil {
		t.Fatalf("git config: %v\n%s", err, out)
	}
	upstream := httptest.NewServer(&cgi.Handler{
		Path: backend,
		Env:  []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1", "REMOTE_USER=x-bitbucket-api-token-auth"},
	})
	defer upstream.Close()

	var events []WriteEvent
	relay := httptest.NewServer(http.StripPrefix("/bitbucket", newHandler("tok", testPins, func(e WriteEvent) error { events = append(events, e); return nil }, upstream.URL, "http://api.invalid/2.0")))
	defer relay.Close()
	port := strings.TrimPrefix(relay.URL, "http://127.0.0.1:")
	script := filepath.Join(root, "isotty-bitbucket")
	if err := os.WriteFile(script, []byte(strings.ReplaceAll(clientScript, "__ISOTTY_GATEWAY_PORT__", port)), 0o755); err != nil {
		t.Fatal(err)
	}

	work := filepath.Join(root, "work")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", work},
		{"-C", work, "config", "user.email", "t@example.com"},
		{"-C", work, "config", "user.name", "t"},
		{"-C", work, "remote", "add", "origin", "https://someone@bitbucket.org/my-team/app.git"},
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
	if len(events) != 1 || events[0] != (WriteEvent{Operation: "push", Repo: "my-team/app"}) {
		t.Fatalf("events = %+v", events)
	}
	fetch := exec.Command("python3", "-I", script, "fetch", "origin")
	fetch.Dir = work
	if out, err := fetch.CombinedOutput(); err != nil {
		t.Fatalf("fetch through relay: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", work, "remote", "get-url", "origin").CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "https://someone@bitbucket.org/my-team/app.git" {
		t.Fatalf("remote URL changed: %v %s", err, out)
	}
}
