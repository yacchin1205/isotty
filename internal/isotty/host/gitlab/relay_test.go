package gitlab

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yazawa/isotty/internal/isotty/host/forge"
)

type upstreamCall struct {
	Method, Path, Query, Auth, Token, Body string
}

// fakeGitLab stands in for one instance, git and API alike; it records what
// reached it and answers from a small table.
type fakeGitLab struct {
	calls   []upstreamCall
	answers map[string]func(w http.ResponseWriter, r *http.Request)
}

func (f *fakeGitLab) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.calls = append(f.calls, upstreamCall{r.Method, r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Get("Authorization"), r.Header.Get("PRIVATE-TOKEN"), string(body)})
		if answer, ok := f.answers[r.Method+" "+r.URL.EscapedPath()]; ok {
			answer(w, r)
			return
		}
		if strings.HasPrefix(r.URL.EscapedPath(), "/api/v4/projects/") && strings.Count(r.URL.EscapedPath(), "/") == 4 {
			// A bare project lookup is the relay's visibility check.
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	})
}

func (f *fakeGitLab) last() upstreamCall { return f.calls[len(f.calls)-1] }

func newTestRelay(t *testing.T, repos map[string]forge.Access, onWrite func(WriteEvent) error) (*httptest.Server, *fakeGitLab, string) {
	t.Helper()
	upstream := &fakeGitLab{answers: map[string]func(w http.ResponseWriter, r *http.Request){}}
	instance := httptest.NewServer(upstream.handler())
	t.Cleanup(instance.Close)
	host := mustHost(t, instance.URL)
	cfg := ProjectConfig{Repos: map[string]forge.Access{}}
	for repo, access := range repos {
		cfg.Repos[host+"/"+repo] = access
	}
	hostCfg := HostConfig{Instances: []Instance{{URL: instance.URL, Token: "glpat-x"}}}
	relay := httptest.NewServer(http.StripPrefix("/gitlab", Handler(hostCfg, cfg, onWrite)))
	t.Cleanup(relay.Close)
	return relay, upstream, host
}

func do(t *testing.T, method, url, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

func TestRelayGit(t *testing.T) {
	var events []WriteEvent
	relay, upstream, host := newTestRelay(t, map[string]forge.Access{"me/mine": forge.AccessWrite, "group/sub/lib": forge.AccessRead}, func(e WriteEvent) error { events = append(events, e); return nil })

	status, _ := do(t, "GET", relay.URL+"/gitlab/git/"+host+"/group/sub/lib.git/info/refs?service=git-upload-pack", "")
	if status != 200 {
		t.Fatalf("info/refs status = %d", status)
	}
	if c := upstream.last(); c.Path != "/group/sub/lib.git/info/refs" || c.Query != "service=git-upload-pack" || c.Auth != "Basic b2F1dGgyOmdscGF0LXg=" {
		t.Fatalf("upstream call = %+v", c)
	}
	status, body := do(t, "POST", relay.URL+"/gitlab/git/"+host+"/group/sub/lib.git/git-receive-pack", "pack")
	if status != 403 || !strings.Contains(body, "pinned as read") {
		t.Fatalf("push to read-only project = %d %s", status, body)
	}
	status, _ = do(t, "POST", relay.URL+"/gitlab/git/"+host+"/me/mine.git/git-receive-pack", "pack")
	if status != 200 || len(events) != 1 || events[0].Operation != "push" || events[0].Repo != host+"/me/mine" {
		t.Fatalf("push = %d, events %+v", status, events)
	}
	status, body = do(t, "GET", relay.URL+"/gitlab/git/other.example.org/g/p.git/info/refs?service=git-upload-pack", "")
	if status != 404 || !strings.Contains(body, "no instance for other.example.org") {
		t.Fatalf("unknown instance = %d %s", status, body)
	}
}

func TestRelayAPI(t *testing.T) {
	var events []WriteEvent
	relay, upstream, host := newTestRelay(t, map[string]forge.Access{"me/mine": forge.AccessWrite, "group/sub/up": forge.AccessComment}, func(e WriteEvent) error { events = append(events, e); return nil })
	upstream.answers["POST /api/v4/projects/group%2Fsub%2Fup/merge_requests"] = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":900,"iid":7}`))
	}
	project := relay.URL + "/gitlab/api/" + host + "/projects/group%2Fsub%2Fup"

	status, body := do(t, "GET", project+"/merge_requests?state=opened", "")
	if status != 200 {
		t.Fatalf("list = %d %s", status, body)
	}
	if c := upstream.last(); c.Path != "/api/v4/projects/group%2Fsub%2Fup/merge_requests" || c.Query != "state=opened" || c.Token != "glpat-x" {
		t.Fatalf("upstream call = %+v", c)
	}
	status, body = do(t, "POST", project+"/merge_requests", `{"source_branch":"b","target_branch":"main","title":"t"}`)
	if status != 201 || !strings.Contains(body, `"iid":7`) {
		t.Fatalf("create = %d %s", status, body)
	}
	status, body = do(t, "PUT", project+"/merge_requests/7", `{"state_event":"close"}`)
	if status != 400 || !strings.Contains(body, "state_event") {
		t.Fatalf("closing via edit = %d %s", status, body)
	}
	status, body = do(t, "POST", project+"/pipelines/3/retry", "")
	if status != 403 || !strings.Contains(body, "needs write") {
		t.Fatalf("retry on comment pin = %d %s", status, body)
	}
	status, _ = do(t, "POST", relay.URL+"/gitlab/api/"+host+"/projects/me%2Fmine/jobs/5/cancel", "")
	if status != 200 {
		t.Fatalf("cancel = %d", status)
	}
	for _, denied := range []struct{ method, path string }{
		{"PUT", project + "/merge_requests/7/merge"},
		{"POST", project + "/merge_requests/7/approve"},
		{"DELETE", project},
		{"POST", project + "/pipeline"},
		{"GET", relay.URL + "/gitlab/api/" + host + "/projects"},
		{"GET", relay.URL + "/gitlab/api/" + host + "/user"},
	} {
		if status, _ := do(t, denied.method, denied.path, "{}"); status != 403 {
			t.Fatalf("%s %s status = %d, want 403", denied.method, denied.path, status)
		}
	}
	want := []WriteEvent{
		{Operation: "mr-create", Repo: host + "/group/sub/up", Target: "7"},
		{Operation: "job-cancel", Repo: host + "/me/mine", Target: "5"},
	}
	if len(events) != len(want) || events[0] != want[0] || events[1] != want[1] {
		t.Fatalf("events = %+v, want %+v", events, want)
	}
}

func TestRelayReadsPublicProjectsWithoutToken(t *testing.T) {
	relay, upstream, host := newTestRelay(t, map[string]forge.Access{"me/mine": forge.AccessWrite}, func(WriteEvent) error { return nil })
	upstream.answers["GET /api/v4/projects/open%2Fsource"] = func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"visibility":"public"}`))
	}
	upstream.answers["GET /api/v4/projects/corp%2Finternal"] = func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"visibility":"internal"}`))
	}
	status, _ := do(t, "GET", relay.URL+"/gitlab/api/"+host+"/projects/open%2Fsource/issues", "")
	if status != 200 {
		t.Fatalf("public read = %d", status)
	}
	if c := upstream.last(); c.Token != "" || c.Auth != "" {
		t.Fatalf("public read carried a credential: %+v", c)
	}
	status, body := do(t, "GET", relay.URL+"/gitlab/api/"+host+"/projects/corp%2Finternal/issues", "")
	if status != 403 || !strings.Contains(body, "not public") {
		t.Fatalf("internal read = %d %s", status, body)
	}
	status, body = do(t, "POST", relay.URL+"/gitlab/api/"+host+"/projects/open%2Fsource/issues", `{"title":"t"}`)
	if status != 403 || !strings.Contains(body, "writes need a pin") {
		t.Fatalf("public write = %d %s", status, body)
	}
}
