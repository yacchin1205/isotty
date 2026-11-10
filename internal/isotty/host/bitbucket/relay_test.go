package bitbucket

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

var repoLookup = regexp.MustCompile(`^/2\.0/repositories/[^/]+/[^/]+$`)

type upstreamCall struct {
	Method, Path, EscapedPath, Query, Auth, Accept, Body string
}

// fakeBitbucket stands in for bitbucket.org and api.bitbucket.org; it
// records what reached it and answers from a small table.
type fakeBitbucket struct {
	calls   []upstreamCall
	answers map[string]func(w http.ResponseWriter, r *http.Request)
}

func (f *fakeBitbucket) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.calls = append(f.calls, upstreamCall{r.Method, r.URL.Path, r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Get("Authorization"), r.Header.Get("Accept"), string(body)})
		if answer, ok := f.answers[r.Method+" "+r.URL.Path]; ok {
			answer(w, r)
			return
		}
		// A bare repository lookup is the relay's visibility check; an
		// unknown repository is private or absent, as on Bitbucket.
		if repoLookup.MatchString(r.URL.Path) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	})
}

func (f *fakeBitbucket) last() upstreamCall {
	return f.calls[len(f.calls)-1]
}

func jsonAnswer(body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}
}

// rawAnswer serves a diff or a log the way Bitbucket does: as its own
// content type, and 406 to a request that accepts JSON only.
func rawAnswer(contentType, body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") == "application/json" {
			w.WriteHeader(http.StatusNotAcceptable)
			w.Write([]byte(`{"code": 406, "message": "HTTP 406 Not Acceptable"}`))
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Write([]byte(body))
	}
}

func newTestRelay(t *testing.T, cfg ProjectConfig, onWrite func(WriteEvent) error) (*httptest.Server, *fakeBitbucket, *fakeBitbucket) {
	git := &fakeBitbucket{answers: map[string]func(http.ResponseWriter, *http.Request){}}
	api := &fakeBitbucket{answers: map[string]func(http.ResponseWriter, *http.Request){}}
	gitServer := httptest.NewServer(git.handler())
	apiServer := httptest.NewServer(api.handler())
	t.Cleanup(gitServer.Close)
	t.Cleanup(apiServer.Close)
	// Mounted as the gateway mounts it, so the client's paths are the real ones.
	relay := httptest.NewServer(http.StripPrefix("/bitbucket", newHandler("tok", cfg, onWrite, gitServer.URL, apiServer.URL+"/2.0")))
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

const (
	pipelineUUID = "%7B1f4a2c3e-0000-4000-8000-0123456789ab%7D"
	stepUUID     = "%7Bffffffff-1111-4222-8333-444444444444%7D"
)

var testPins = ProjectConfig{Repos: map[string]Access{"My-Team/App": AccessWrite, "org/lib": AccessRead}}

func TestRelayStatus(t *testing.T) {
	relay, _, _ := newTestRelay(t, testPins, nil)
	status, body := do(t, "GET", relay.URL+"/bitbucket/status", "")
	if status != 200 || !strings.Contains(body, `"org/lib":"read"`) {
		t.Fatalf("status = %d %s", status, body)
	}
}

func TestRelayGit(t *testing.T) {
	var events []WriteEvent
	relay, git, _ := newTestRelay(t, testPins, func(e WriteEvent) error { events = append(events, e); return nil })

	status, _ := do(t, "GET", relay.URL+"/bitbucket/git/my-team/app.git/info/refs?service=git-upload-pack", "")
	if status != 200 {
		t.Fatalf("info/refs status = %d", status)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-bitbucket-api-token-auth:tok"))
	if c := git.last(); c.Path != "/my-team/app.git/info/refs" || c.Query != "service=git-upload-pack" || c.Auth != want {
		t.Fatalf("upstream call = %+v", c)
	}
	status, body := do(t, "POST", relay.URL+"/bitbucket/git/org/lib.git/git-receive-pack", "pack", "Content-Type", "application/x-git-receive-pack-request")
	if status != 403 || !strings.Contains(body, "pinned as read") {
		t.Fatalf("push to read-only repo = %d %s", status, body)
	}
	status, body = do(t, "GET", relay.URL+"/bitbucket/git/other/repo.git/info/refs?service=git-upload-pack", "")
	if status != 403 || !strings.Contains(body, "not among the repositories pinned") || !strings.Contains(body, "not public") {
		t.Fatalf("unpinned private fetch = %d %s", status, body)
	}
	status, _ = do(t, "POST", relay.URL+"/bitbucket/git/my-team/app.git/git-receive-pack", "pack", "Content-Type", "application/x-git-receive-pack-request")
	if status != 200 {
		t.Fatalf("push status = %d", status)
	}
	if len(events) != 1 || events[0] != (WriteEvent{Operation: "push", Repo: "my-team/app"}) {
		t.Fatalf("events = %+v", events)
	}
}

func TestRelayAPIReads(t *testing.T) {
	relay, _, api := newTestRelay(t, testPins, nil)
	status, _ := do(t, "GET", relay.URL+"/bitbucket/api/repositories/org/lib/pullrequests?state=OPEN&pagelen=5", "")
	if status != 200 {
		t.Fatalf("pullrequests status = %d", status)
	}
	if c := api.last(); c.Path != "/2.0/repositories/org/lib/pullrequests" || c.Query != "state=OPEN&pagelen=5" || c.Auth != "Bearer tok" || c.Accept != "application/json" {
		t.Fatalf("upstream call = %+v", c)
	}
	// Pipelines and steps are named by UUIDs in braces, sent encoded and
	// forwarded as they came.
	status, _ = do(t, "GET", relay.URL+"/bitbucket/api/repositories/org/lib/pipelines/"+pipelineUUID+"/steps/"+stepUUID+"/log", "")
	if status != 200 {
		t.Fatalf("step log status = %d", status)
	}
	if c := api.last(); c.EscapedPath != "/2.0/repositories/org/lib/pipelines/"+pipelineUUID+"/steps/"+stepUUID+"/log" {
		t.Fatalf("upstream path = %q", c.EscapedPath)
	}
	for _, denied := range []string{
		"/api/user",
		"/api/user/workspaces/org/permissions/repositories",
		"/api/workspaces/org/members",
		"/api/repositories/org",
		"/api/repositories/org/lib/permissions-config/users",
		"/api/repositories/org/lib/hooks",
		"/api/repositories/org/lib/deploy-keys",
		"/api/repositories/org/lib/pipelines_config/variables",
		"/api/repositories/org/lib/deployments_config/environments/" + pipelineUUID + "/variables",
		"/api/repositories/org/lib/src/main/README.md",
		"/api/repositories/org/lib/pipelines/1f4a2c3e-0000-4000-8000-0123456789ab",
		"/api/repositories/other/repo/pullrequests",
	} {
		if status, _ := do(t, "GET", relay.URL+"/bitbucket"+denied, ""); status != 403 {
			t.Fatalf("GET %s status = %d, want 403", denied, status)
		}
	}
}

// TestRelayFollowsTheDiffRedirectWithTheToken: Bitbucket answers a pull
// request's diff with a redirect to the repository diff on the same host,
// which needs the token as much as the first call did; a log's redirect
// to a signed URL on another host must not see it.
func TestRelayFollowsTheDiffRedirectWithTheToken(t *testing.T) {
	relay, _, api := newTestRelay(t, testPins, nil)
	api.answers["GET /2.0/repositories/org/lib/pullrequests/7/diff"] = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/2.0/repositories/org/lib/diff/abc..def?from_pullrequest_id=7", http.StatusFound)
	}
	api.answers["GET /2.0/repositories/org/lib/diff/abc..def"] = func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("diff --git a/x b/x\n"))
	}
	status, body := do(t, "GET", relay.URL+"/bitbucket/api/repositories/org/lib/pullrequests/7/diff", "")
	if status != 200 || body != "diff --git a/x b/x\n" {
		t.Fatalf("diff = %d %q", status, body)
	}

	blob := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("token forwarded to the blob host: %q", r.Header.Get("Authorization"))
		}
		rawAnswer("application/octet-stream", "log line\n")(w, r)
	}))
	defer blob.Close()
	api.answers["GET /2.0/repositories/org/lib/pipelines/{1f4a2c3e-0000-4000-8000-0123456789ab}/steps/{ffffffff-1111-4222-8333-444444444444}/log"] = func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") == "application/json" {
			w.WriteHeader(http.StatusNotAcceptable)
			return
		}
		http.Redirect(w, r, blob.URL+"/signed", http.StatusTemporaryRedirect)
	}
	status, body = do(t, "GET", relay.URL+"/bitbucket/api/repositories/org/lib/pipelines/"+pipelineUUID+"/steps/"+stepUUID+"/log", "")
	if status != 406 {
		t.Fatalf("log accepting JSON only = %d %q", status, body)
	}
	status, body = do(t, "GET", relay.URL+"/bitbucket/api/repositories/org/lib/pipelines/"+pipelineUUID+"/steps/"+stepUUID+"/log", "", "Accept", "*/*")
	if status != 200 || body != "log line\n" {
		t.Fatalf("log = %d %q", status, body)
	}
}

func TestRelayAPIWrites(t *testing.T) {
	var events []WriteEvent
	relay, _, api := newTestRelay(t, testPins, func(e WriteEvent) error { events = append(events, e); return nil })
	api.answers["POST /2.0/repositories/my-team/app/pullrequests"] = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":42,"links":{"html":{"href":"https://bitbucket.org/my-team/app/pull-requests/42"}}}`))
	}
	api.answers["POST /2.0/repositories/my-team/app/pullrequests/42/comments"] = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":9001}`))
	}

	status, body := do(t, "POST", relay.URL+"/bitbucket/api/repositories/my-team/app/pullrequests",
		`{"title":"t","source":{"branch":{"name":"feat"}},"destination":{"branch":{"name":"main"}},"description":"x","draft":true}`)
	if status != 201 || !strings.Contains(body, `"id":42`) {
		t.Fatalf("create = %d %s", status, body)
	}
	status, _ = do(t, "PUT", relay.URL+"/bitbucket/api/repositories/my-team/app/pullrequests/42", `{"title":"new"}`)
	if status != 200 {
		t.Fatalf("edit status = %d", status)
	}
	status, _ = do(t, "POST", relay.URL+"/bitbucket/api/repositories/my-team/app/pullrequests/42/comments", `{"content":{"raw":"looks fine"}}`)
	if status != 201 {
		t.Fatalf("comment status = %d", status)
	}
	status, _ = do(t, "POST", relay.URL+"/bitbucket/api/repositories/my-team/app/pipelines/"+pipelineUUID+"/stopPipeline", "")
	if status != 200 {
		t.Fatalf("stop status = %d", status)
	}
	want := []WriteEvent{
		{Operation: "pr-create", Repo: "my-team/app", Target: "42"},
		{Operation: "pr-edit", Repo: "my-team/app", Target: "42"},
		{Operation: "comment", Repo: "my-team/app", Target: "42"},
		{Operation: "pipeline-stop", Repo: "my-team/app", Target: "{1f4a2c3e-0000-4000-8000-0123456789ab}"},
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
	// A pull request from a fork names a repository inside source; the
	// relay sees through the nesting.
	status, body = do(t, "POST", relay.URL+"/bitbucket/api/repositories/my-team/app/pullrequests",
		`{"title":"t","source":{"branch":{"name":"feat"},"repository":{"full_name":"someone/app"}}}`)
	if status != 400 || !strings.Contains(body, `source.repository`) {
		t.Fatalf("fork pull request = %d %s", status, body)
	}
	status, body = do(t, "POST", relay.URL+"/bitbucket/api/repositories/my-team/app/pullrequests",
		`{"title":"t","source":{"branch":{"name":"feat"}},"reviewers":[{"uuid":"{x}"}]}`)
	if status != 400 || !strings.Contains(body, `reviewers`) {
		t.Fatalf("pull request with reviewers = %d %s", status, body)
	}
	status, body = do(t, "PUT", relay.URL+"/bitbucket/api/repositories/my-team/app/pullrequests/42", `{"state":"DECLINED"}`)
	if status != 400 || !strings.Contains(body, `state`) {
		t.Fatalf("closing via edit = %d %s", status, body)
	}
	status, body = do(t, "POST", relay.URL+"/bitbucket/api/repositories/my-team/app/pullrequests/42/comments", `{"content":{"raw":"x","html":"<b>x</b>"}}`)
	if status != 400 || !strings.Contains(body, `content.html`) {
		t.Fatalf("comment with html = %d %s", status, body)
	}
	status, body = do(t, "POST", relay.URL+"/bitbucket/api/repositories/org/lib/pullrequests", `{"title":"t","source":{"branch":{"name":"f"}}}`)
	if status != 403 || !strings.Contains(body, "pinned as read") {
		t.Fatalf("write to read-only repo = %d %s", status, body)
	}
	for _, denied := range []struct{ method, path string }{
		{"POST", "/api/repositories/my-team/app/pullrequests/42/merge"},
		{"POST", "/api/repositories/my-team/app/pullrequests/42/approve"},
		{"POST", "/api/repositories/my-team/app/pullrequests/42/decline"},
		{"POST", "/api/repositories/my-team/app/pullrequests/42/request-changes"},
		{"POST", "/api/repositories/my-team/app/pipelines"},
		{"DELETE", "/api/repositories/my-team/app"},
		{"POST", "/api/repositories/my-team/app/forks"},
		{"PUT", "/api/repositories/my-team/app/permissions-config/users/x"},
		{"POST", "/api/repositories/my-team/app/pipelines_config/variables"},
	} {
		if status, _ := do(t, denied.method, relay.URL+"/bitbucket"+denied.path, "{}"); status != 403 {
			t.Fatalf("%s %s status = %d, want 403", denied.method, denied.path, status)
		}
	}
	if len(api.calls) != calls {
		t.Fatal("a refused write reached the upstream")
	}
}

func TestRelayAdminLevel(t *testing.T) {
	var events []WriteEvent
	cfg := ProjectConfig{Repos: map[string]Access{"my-team/app": AccessAdmin, "org/lib": AccessWrite}}
	relay, _, api := newTestRelay(t, cfg, func(e WriteEvent) error { events = append(events, e); return nil })
	api.answers["PUT /2.0/repositories/my-team/app"] = jsonAnswer(`{"full_name":"my-team/app","fork_policy":"no_public_forks"}`)
	api.answers["POST /2.0/repositories/my-team/app/branch-restrictions"] = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":31,"kind":"push","pattern":"main"}`))
	}

	status, body := do(t, "PUT", relay.URL+"/bitbucket/api/repositories/my-team/app", `{"fork_policy":"no_public_forks","description":"d"}`)
	if status != 200 || !strings.Contains(body, "no_public_forks") {
		t.Fatalf("repo edit = %d %s", status, body)
	}
	for _, field := range []string{`{"is_private":true}`, `{"name":"renamed"}`, `{"project":{"key":"OTHER"}}`} {
		if status, _ := do(t, "PUT", relay.URL+"/bitbucket/api/repositories/my-team/app", field); status != 400 {
			t.Fatalf("repo edit with %s = %d, want 400", field, status)
		}
	}
	status, _ = do(t, "POST", relay.URL+"/bitbucket/api/repositories/my-team/app/branch-restrictions", `{"kind":"push","branch_match_kind":"glob","pattern":"main","users":[],"groups":[]}`)
	if status != 201 {
		t.Fatalf("restriction create = %d", status)
	}
	status, _ = do(t, "DELETE", relay.URL+"/bitbucket/api/repositories/my-team/app/branch-restrictions/31", "")
	if status != 200 {
		t.Fatalf("restriction remove = %d", status)
	}
	want := []WriteEvent{
		{Operation: "repo-edit", Repo: "my-team/app"},
		{Operation: "restriction-create", Repo: "my-team/app", Target: "31"},
		{Operation: "restriction-remove", Repo: "my-team/app", Target: "31"},
	}
	if len(events) != len(want) {
		t.Fatalf("events = %+v", events)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("event %d = %+v, want %+v", i, events[i], want[i])
		}
	}
	status, body = do(t, "PUT", relay.URL+"/bitbucket/api/repositories/org/lib", `{"description":"d"}`)
	if status != 403 || !strings.Contains(body, "pinned as write") || !strings.Contains(body, "needs admin") {
		t.Fatalf("repo edit on write pin = %d %s", status, body)
	}
}

// TestRelayReadsPublicRepositories: a repository outside the pin is
// readable when Bitbucket serves it to anyone; writes still need a pin,
// and a private one stays refused.
func TestRelayReadsPublicRepositories(t *testing.T) {
	relay, git, api := newTestRelay(t, testPins, nil)
	api.answers["GET /2.0/repositories/pub/lic"] = func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("visibility check carried no token")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"full_name":"pub/lic","is_private":false}`))
	}
	api.answers["GET /2.0/repositories/corp/internal"] = jsonAnswer(`{"full_name":"corp/internal","is_private":true}`)

	status, _ := do(t, "GET", relay.URL+"/bitbucket/api/repositories/pub/lic/pullrequests", "")
	if status != 200 {
		t.Fatalf("public pullrequests status = %d", status)
	}
	status, _ = do(t, "GET", relay.URL+"/bitbucket/git/pub/lic.git/info/refs?service=git-upload-pack", "")
	if status != 200 {
		t.Fatalf("public fetch status = %d", status)
	}
	if c := git.last(); c.Auth == "" {
		t.Fatalf("public fetch carried no token: %+v", c)
	}
	status, body := do(t, "POST", relay.URL+"/bitbucket/api/repositories/pub/lic/pullrequests", `{"title":"t"}`)
	if status != 403 || !strings.Contains(body, "writes need a pin") {
		t.Fatalf("public write = %d %s", status, body)
	}
	status, body = do(t, "GET", relay.URL+"/bitbucket/api/repositories/corp/internal/pullrequests", "")
	if status != 403 || !strings.Contains(body, "not public") {
		t.Fatalf("private unpinned read = %d %s", status, body)
	}
	status, body = do(t, "GET", relay.URL+"/bitbucket/api/repositories/sec/ret/pullrequests", "")
	if status != 403 || !strings.Contains(body, "not public") {
		t.Fatalf("absent unpinned read = %d %s", status, body)
	}
}

func TestRelayCommentLevel(t *testing.T) {
	var events []WriteEvent
	cfg := ProjectConfig{Repos: map[string]Access{"org/upstream": AccessComment}}
	relay, git, api := newTestRelay(t, cfg, func(e WriteEvent) error { events = append(events, e); return nil })
	api.answers["POST /2.0/repositories/org/upstream/pullrequests"] = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":5}`))
	}
	status, body := do(t, "POST", relay.URL+"/bitbucket/api/repositories/org/upstream/pullrequests", `{"title":"t","source":{"branch":{"name":"b"}}}`)
	if status != 201 || !strings.Contains(body, `"id":5`) {
		t.Fatalf("pr create = %d %s", status, body)
	}
	status, body = do(t, "POST", relay.URL+"/bitbucket/api/repositories/org/upstream/pipelines/"+pipelineUUID+"/stopPipeline", "")
	if status != 403 || !strings.Contains(body, "pinned as comment") || !strings.Contains(body, "needs write") {
		t.Fatalf("stop = %d %s", status, body)
	}
	status, body = do(t, "GET", relay.URL+"/bitbucket/git/org/upstream.git/info/refs?service=git-receive-pack", "")
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
