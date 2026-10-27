package github

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

var repoLookup = regexp.MustCompile(`^/repos/[^/]+/[^/]+$`)

type upstreamCall struct {
	Method, Path, EscapedPath, Query, Auth, Accept, Body string
}

// fakeGitHub stands in for github.com and api.github.com; it records what
// reached it and answers from a small table.
type fakeGitHub struct {
	calls   []upstreamCall
	answers map[string]func(w http.ResponseWriter, r *http.Request)
}

func (f *fakeGitHub) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.calls = append(f.calls, upstreamCall{r.Method, r.URL.Path, r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Get("Authorization"), r.Header.Get("Accept"), string(body)})
		if answer, ok := f.answers[r.Method+" "+r.URL.Path]; ok {
			answer(w, r)
			return
		}
		// A bare repository lookup is the relay's visibility check; an
		// unknown repository is private or absent, as on GitHub.
		if repoLookup.MatchString(r.URL.Path) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	})
}

func (f *fakeGitHub) last() upstreamCall {
	return f.calls[len(f.calls)-1]
}

func newTestRelay(t *testing.T, cfg ProjectConfig, onWrite func(WriteEvent) error) (*httptest.Server, *fakeGitHub, *fakeGitHub) {
	git := &fakeGitHub{answers: map[string]func(http.ResponseWriter, *http.Request){}}
	api := &fakeGitHub{answers: map[string]func(http.ResponseWriter, *http.Request){}}
	gitServer := httptest.NewServer(git.handler(t))
	apiServer := httptest.NewServer(api.handler(t))
	t.Cleanup(gitServer.Close)
	t.Cleanup(apiServer.Close)
	// Mounted as the gateway mounts it, so the client's paths are the real ones.
	relay := httptest.NewServer(http.StripPrefix("/github", newHandler("tok", cfg, onWrite, gitServer.URL, apiServer.URL)))
	t.Cleanup(relay.Close)
	return relay, git, api
}

func do(t *testing.T, method, url, body string, headers ...string) (int, string) {
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

var testPins = ProjectConfig{Repos: map[string]Access{"Me/Project": AccessWrite, "org/lib": AccessRead}}

func TestRelayStatus(t *testing.T) {
	relay, _, _ := newTestRelay(t, testPins, nil)
	status, body := do(t, "GET", relay.URL+"/github/status", "")
	if status != 200 || !strings.Contains(body, `"org/lib":"read"`) {
		t.Fatalf("status = %d %s", status, body)
	}
}

func TestRelayGit(t *testing.T) {
	var events []WriteEvent
	relay, git, _ := newTestRelay(t, testPins, func(e WriteEvent) error { events = append(events, e); return nil })

	status, _ := do(t, "GET", relay.URL+"/github/git/me/project.git/info/refs?service=git-upload-pack", "")
	if status != 200 {
		t.Fatalf("info/refs status = %d", status)
	}
	if c := git.last(); c.Path != "/me/project.git/info/refs" || c.Query != "service=git-upload-pack" || !strings.HasPrefix(c.Auth, "Basic ") {
		t.Fatalf("upstream call = %+v", c)
	}

	status, body := do(t, "POST", relay.URL+"/github/git/org/lib.git/git-receive-pack", "pack", "Content-Type", "application/x-git-receive-pack-request")
	if status != 403 || !strings.Contains(body, "pinned as read") {
		t.Fatalf("push to read-only repo = %d %s", status, body)
	}
	status, body = do(t, "GET", relay.URL+"/github/git/other/repo.git/info/refs?service=git-upload-pack", "")
	if status != 403 || !strings.Contains(body, "not among the repositories pinned") || !strings.Contains(body, "not public") {
		t.Fatalf("unpinned private fetch = %d %s", status, body)
	}
	status, _ = do(t, "GET", relay.URL+"/github/git/me/project.git/info/refs?service=git-upload-archive", "")
	if status != 400 {
		t.Fatalf("other service status = %d", status)
	}
	if len(events) != 0 {
		t.Fatalf("reads recorded events: %+v", events)
	}

	status, _ = do(t, "POST", relay.URL+"/github/git/me/project.git/git-receive-pack", "pack", "Content-Type", "application/x-git-receive-pack-request")
	if status != 200 {
		t.Fatalf("push status = %d", status)
	}
	if c := git.last(); c.Path != "/me/project.git/git-receive-pack" || c.Body != "pack" {
		t.Fatalf("push upstream call = %+v", c)
	}
	if len(events) != 1 || events[0] != (WriteEvent{Operation: "push", Repo: "me/project"}) {
		t.Fatalf("events = %+v", events)
	}
}

func TestRelayGitDoesNotFollowRedirects(t *testing.T) {
	relay, git, _ := newTestRelay(t, testPins, nil)
	git.answers["GET /me/project.git/info/refs"] = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://github.com/me/renamed.git/info/refs", http.StatusMovedPermanently)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(relay.URL + "/github/git/me/project.git/info/refs?service=git-upload-pack")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently || len(git.calls) != 1 {
		t.Fatalf("redirect passed through = %d, upstream calls %d", resp.StatusCode, len(git.calls))
	}
}

func TestRelayAPIReads(t *testing.T) {
	relay, _, api := newTestRelay(t, testPins, nil)
	status, _ := do(t, "GET", relay.URL+"/github/api/repos/org/lib/pulls?state=open&per_page=5", "", "Accept", "application/vnd.github.diff")
	if status != 200 {
		t.Fatalf("pulls status = %d", status)
	}
	if c := api.last(); c.Path != "/repos/org/lib/pulls" || c.Query != "state=open&per_page=5" || c.Auth != "Bearer tok" || c.Accept != "application/vnd.github.diff" {
		t.Fatalf("upstream call = %+v", c)
	}
	for _, denied := range []string{
		"/api/user/repos",
		"/api/orgs/org/repos",
		"/api/search/issues?q=x",
		"/api/repos/org/lib/collaborators",
		"/api/repos/org/lib/hooks",
		"/api/repos/org/lib/actions/variables",
		"/api/repos/org/lib/environments/prod/variables",
		"/api/repos/org/lib/pulls/1/files",
		"/api/repos/other/repo/pulls",
		"/api/repos/org/lib/branches/main/protection/required_status_checks",
	} {
		if status, _ := do(t, "GET", relay.URL+"/github"+denied, ""); status != 403 {
			t.Fatalf("GET %s status = %d, want 403", denied, status)
		}
	}
	// A slash in a branch name is sent encoded, as GitHub wants it, and
	// reaches GitHub encoded; sent plain, it is two segments and no route.
	if status, _ := do(t, "GET", relay.URL+"/github/api/repos/org/lib/branches/release%2Fv1/protection", ""); status != 200 {
		t.Fatalf("branch with encoded slash status = %d", status)
	}
	if c := api.last(); c.EscapedPath != "/repos/org/lib/branches/release%2Fv1/protection" {
		t.Fatalf("upstream path = %q", c.EscapedPath)
	}
	if status, _ := do(t, "GET", relay.URL+"/github/api/repos/org/lib/branches/release/v1/protection", ""); status != 403 {
		t.Fatalf("branch with plain slash status = %d, want 403", status)
	}
}

func TestRelayAPIWrites(t *testing.T) {
	var events []WriteEvent
	relay, _, api := newTestRelay(t, testPins, func(e WriteEvent) error { events = append(events, e); return nil })
	api.answers["POST /repos/me/project/pulls"] = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"number":42,"html_url":"https://github.com/me/project/pull/42"}`))
	}

	status, body := do(t, "POST", relay.URL+"/github/api/repos/me/project/pulls", `{"title":"t","head":"b","base":"main","body":"x","draft":true}`)
	if status != 201 || !strings.Contains(body, `"number":42`) {
		t.Fatalf("create = %d %s", status, body)
	}
	if c := api.last(); c.Method != "POST" || !strings.Contains(c.Body, `"title":"t"`) {
		t.Fatalf("upstream call = %+v", c)
	}
	status, _ = do(t, "PATCH", relay.URL+"/github/api/repos/me/project/pulls/42", `{"title":"new"}`)
	if status != 200 {
		t.Fatalf("edit status = %d", status)
	}
	status, _ = do(t, "POST", relay.URL+"/github/api/repos/me/project/actions/runs/7/rerun-failed-jobs", "")
	if status != 200 {
		t.Fatalf("rerun status = %d", status)
	}
	want := []WriteEvent{
		{Operation: "pr-create", Repo: "me/project", Target: "42"},
		{Operation: "pr-edit", Repo: "me/project", Target: "42"},
		{Operation: "run-rerun", Repo: "me/project", Target: "7"},
	}
	if len(events) != len(want) {
		t.Fatalf("events = %+v", events)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("event %d = %+v, want %+v", i, events[i], want[i])
		}
	}

	calls := len(api.calls)
	status, body = do(t, "PATCH", relay.URL+"/github/api/repos/me/project/pulls/42", `{"state":"closed"}`)
	if status != 400 || !strings.Contains(body, `state`) {
		t.Fatalf("closing via edit = %d %s", status, body)
	}
	status, body = do(t, "POST", relay.URL+"/github/api/repos/me/project/actions/runs/7/cancel", `{"x":1}`)
	if status != 400 || !strings.Contains(body, "does not take a request body") {
		t.Fatalf("cancel with body = %d %s", status, body)
	}
	status, body = do(t, "POST", relay.URL+"/github/api/repos/org/lib/issues", `{"title":"t"}`)
	if status != 403 || !strings.Contains(body, "pinned as read") {
		t.Fatalf("write to read-only repo = %d %s", status, body)
	}
	for _, denied := range []struct{ method, path string }{
		{"PUT", "/api/repos/me/project/pulls/42/merge"},
		{"POST", "/api/repos/me/project/pulls/42/reviews"},
		{"DELETE", "/api/repos/me/project"},
		{"POST", "/api/repos/me/project/actions/workflows/ci.yml/dispatches"},
		{"PUT", "/api/repos/me/project/collaborators/x"},
	} {
		if status, _ := do(t, denied.method, relay.URL+"/github"+denied.path, "{}"); status != 403 {
			t.Fatalf("%s %s status = %d, want 403", denied.method, denied.path, status)
		}
	}
	if len(api.calls) != calls {
		t.Fatal("a refused write reached the upstream")
	}
}

func TestRelayReportsAuditFailureAfterWrite(t *testing.T) {
	relay, _, api := newTestRelay(t, testPins, func(WriteEvent) error { return errors.New("ssh is gone") })
	api.answers["POST /repos/me/project/issues"] = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"number":3}`))
	}
	status, body := do(t, "POST", relay.URL+"/github/api/repos/me/project/issues", `{"title":"t"}`)
	if status != 500 {
		t.Fatalf("status = %d %s", status, body)
	}
	var payload struct {
		Error  string          `json:"error"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload.Error, "succeeded but recording the audit event failed") || string(payload.Result) != `{"number":3}` {
		t.Fatalf("payload = %+v", payload)
	}

	status, body = do(t, "POST", relay.URL+"/github/git/me/project.git/git-receive-pack", "pack")
	if status != 500 || !strings.Contains(body, "recording the audit event failed") {
		t.Fatalf("push = %d %s", status, body)
	}
}

func TestRelayFollowsLogRedirectWithoutToken(t *testing.T) {
	blob := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("token forwarded to the blob host: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("log line\n"))
	}))
	defer blob.Close()
	relay, _, api := newTestRelay(t, testPins, nil)
	api.answers["GET /repos/org/lib/actions/jobs/9/logs"] = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, blob.URL+"/signed", http.StatusFound)
	}
	status, body := do(t, "GET", relay.URL+"/github/api/repos/org/lib/actions/jobs/9/logs", "")
	if status != 200 || body != "log line\n" {
		t.Fatalf("log = %d %q", status, body)
	}
}

// TestRelayReadsPublicRepositoriesWithoutToken: a repository outside the pin
// is readable when GitHub serves it to anyone, and then without the token;
// writes still need a pin, and a private one stays refused.
func TestRelayReadsPublicRepositoriesWithoutToken(t *testing.T) {
	relay, git, api := newTestRelay(t, testPins, nil)
	api.answers["GET /repos/pub/lic"] = func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("visibility check carried the token")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"private":false}`))
	}
	api.answers["GET /repos/sec/ret"] = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}

	status, _ := do(t, "GET", relay.URL+"/github/api/repos/pub/lic/pulls", "")
	if status != 200 {
		t.Fatalf("public pulls status = %d", status)
	}
	if c := api.last(); c.Path != "/repos/pub/lic/pulls" || c.Auth != "" {
		t.Fatalf("public read upstream call = %+v", c)
	}
	status, _ = do(t, "GET", relay.URL+"/github/git/pub/lic.git/info/refs?service=git-upload-pack", "")
	if status != 200 {
		t.Fatalf("public fetch status = %d", status)
	}
	if c := git.last(); c.Auth != "" {
		t.Fatalf("public fetch carried the token: %+v", c)
	}
	visibilityChecks := 0
	for _, c := range api.calls {
		if c.Path == "/repos/pub/lic" {
			visibilityChecks++
		}
	}
	if visibilityChecks != 1 {
		t.Fatalf("visibility checked %d times, want once", visibilityChecks)
	}

	status, body := do(t, "POST", relay.URL+"/github/api/repos/pub/lic/issues", `{"title":"t"}`)
	if status != 403 || !strings.Contains(body, "writes need a pin") {
		t.Fatalf("public write = %d %s", status, body)
	}
	status, body = do(t, "POST", relay.URL+"/github/git/pub/lic.git/git-receive-pack", "pack")
	if status != 403 {
		t.Fatalf("public push = %d %s", status, body)
	}
	status, body = do(t, "GET", relay.URL+"/github/api/repos/sec/ret/pulls", "")
	if status != 403 || !strings.Contains(body, "not public") {
		t.Fatalf("private unpinned read = %d %s", status, body)
	}
	for _, c := range api.calls {
		if c.Path == "/repos/sec/ret/pulls" || c.Path == "/repos/pub/lic/issues" {
			t.Fatalf("refused request reached the upstream: %+v", c)
		}
	}
}

func TestRelayCommentLevel(t *testing.T) {
	var events []WriteEvent
	cfg := ProjectConfig{Repos: map[string]Access{"org/upstream": AccessComment}}
	relay, git, api := newTestRelay(t, cfg, func(e WriteEvent) error { events = append(events, e); return nil })
	api.answers["POST /repos/org/upstream/pulls"] = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"number":5}`))
	}

	status, body := do(t, "POST", relay.URL+"/github/api/repos/org/upstream/pulls", `{"title":"t","head":"me:b","base":"main"}`)
	if status != 201 || !strings.Contains(body, `"number":5`) {
		t.Fatalf("pr create = %d %s", status, body)
	}
	status, body = do(t, "POST", relay.URL+"/github/api/repos/org/upstream/actions/runs/7/rerun", "")
	if status != 403 || !strings.Contains(body, "pinned as comment") || !strings.Contains(body, "needs write") {
		t.Fatalf("rerun = %d %s", status, body)
	}
	status, body = do(t, "GET", relay.URL+"/github/git/org/upstream.git/info/refs?service=git-receive-pack", "")
	if status != 403 || !strings.Contains(body, "needs write") {
		t.Fatalf("push = %d %s", status, body)
	}
	if len(git.calls) != 0 || len(api.calls) != 1 {
		t.Fatalf("upstream calls: git %d api %d; want only the pull request", len(git.calls), len(api.calls))
	}
	if len(events) != 1 || events[0].Operation != "pr-create" || events[0].Target != "5" {
		t.Fatalf("events = %+v", events)
	}
}
