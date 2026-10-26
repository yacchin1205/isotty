package github

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
)

const (
	gitUpstream = "https://github.com"
	apiUpstream = "https://api.github.com"
	apiVersion  = "2022-11-28"
	// Writes are buffered so the audit record can be made before the VM
	// learns the result; GitHub's own payloads for these calls are small.
	maxWriteBody = 1 << 20
	maxWriteResp = 4 << 20
)

// WriteEvent describes a write that reached GitHub so the caller can record
// it in the VM audit log. Target is the pull request or issue number, or the
// workflow run id; a push has none.
type WriteEvent struct {
	Operation string
	Repo      string
	Target    string
}

// apiRoute is one allowed REST call. The pattern binds owner, repo and, for
// calls on one item, target; writes also list the JSON fields the VM may
// send, so a PATCH cannot reach fields the operation does not cover (state,
// for instance, which would close the item).
type apiRoute struct {
	method    string
	pattern   *regexp.Regexp
	access    Access
	operation string
	fields    []string
}

var apiRoutes = []apiRoute{
	read("GET", "/repos/{o}/{r}"),
	read("GET", "/repos/{o}/{r}/pulls"),
	read("GET", "/repos/{o}/{r}/pulls/{n}"),
	read("GET", "/repos/{o}/{r}/pulls/{n}/reviews"),
	read("GET", "/repos/{o}/{r}/pulls/{n}/comments"),
	read("GET", "/repos/{o}/{r}/issues"),
	read("GET", "/repos/{o}/{r}/issues/{n}"),
	read("GET", "/repos/{o}/{r}/issues/{n}/comments"),
	read("GET", "/repos/{o}/{r}/commits/{sha}/check-runs"),
	read("GET", "/repos/{o}/{r}/actions/runs"),
	read("GET", "/repos/{o}/{r}/actions/runs/{n}"),
	read("GET", "/repos/{o}/{r}/actions/runs/{n}/jobs"),
	read("GET", "/repos/{o}/{r}/actions/runs/{n}/artifacts"),
	read("GET", "/repos/{o}/{r}/actions/jobs/{n}/logs"),
	read("GET", "/repos/{o}/{r}/actions/artifacts/{n}/zip"),
	read("GET", "/repos/{o}/{r}/branches/{b}/protection"),
	read("GET", "/repos/{o}/{r}/rules/branches/{b}"),
	read("GET", "/repos/{o}/{r}/actions/permissions"),
	read("GET", "/repos/{o}/{r}/environments"),
	read("GET", "/repos/{o}/{r}/pages"),
	read("GET", "/repos/{o}/{r}/actions/secrets"),
	write("POST", "/repos/{o}/{r}/pulls", "pr-create", "title", "head", "base", "body", "draft"),
	write("PATCH", "/repos/{o}/{r}/pulls/{n}", "pr-edit", "title", "body"),
	write("POST", "/repos/{o}/{r}/issues", "issue-create", "title", "body", "labels"),
	write("PATCH", "/repos/{o}/{r}/issues/{n}", "issue-edit", "title", "body", "labels"),
	write("POST", "/repos/{o}/{r}/issues/{n}/comments", "comment", "body"),
	write("POST", "/repos/{o}/{r}/actions/runs/{n}/rerun", "run-rerun"),
	write("POST", "/repos/{o}/{r}/actions/runs/{n}/rerun-failed-jobs", "run-rerun"),
	write("POST", "/repos/{o}/{r}/actions/runs/{n}/cancel", "run-cancel"),
}

func read(method, path string) apiRoute {
	return apiRoute{method: method, pattern: compileRoute(path), access: AccessRead}
}

func write(method, path, operation string, fields ...string) apiRoute {
	return apiRoute{method: method, pattern: compileRoute(path), access: AccessWrite, operation: operation, fields: fields}
}

func compileRoute(path string) *regexp.Regexp {
	expr := strings.NewReplacer(
		"{o}", `(?P<owner>[A-Za-z0-9_.-]+)`,
		"{r}", `(?P<repo>[A-Za-z0-9_.-]+)`,
		"{n}", `(?P<target>[0-9]+)`,
		"{sha}", `[0-9a-fA-F]+`,
		"{b}", `.+`,
	).Replace(path)
	return regexp.MustCompile("^" + expr + "$")
}

type relay struct {
	token       string
	cfg         ProjectConfig
	onWrite     func(WriteEvent) error
	gitUpstream *url.URL
	apiUpstream *url.URL
	// git must see GitHub's redirects itself (a renamed repository is not
	// pinned under its new name); the REST client follows them because logs
	// and artifacts are served from a signed URL on another host, which the
	// VM must not talk to directly.
	gitClient *http.Client
	apiClient *http.Client
}

// Handler relays the allowed operations for the VM; the host gateway mounts
// it under /github.
func Handler(token string, cfg ProjectConfig, onWrite func(WriteEvent) error) http.Handler {
	return newHandler(token, cfg, onWrite, gitUpstream, apiUpstream)
}

func newHandler(token string, cfg ProjectConfig, onWrite func(WriteEvent) error, gitUpstream, apiUpstream string) http.Handler {
	git, err := url.Parse(gitUpstream)
	if err != nil {
		panic(err)
	}
	api, err := url.Parse(apiUpstream)
	if err != nil {
		panic(err)
	}
	// Bodies may be large (packs, artifacts), so only the wait for GitHub's
	// first byte is bounded, not the transfer.
	transport := &http.Transport{ResponseHeaderTimeout: 60 * time.Second}
	r := &relay{
		token:       token,
		cfg:         cfg,
		onWrite:     onWrite,
		gitUpstream: git,
		apiUpstream: api,
		gitClient: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
		// The signed URL a redirect points at needs no token, and must not see
		// it; Go only drops the header on its own when the domain changes.
		apiClient: &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			req.Header.Del("Authorization")
			return nil
		}},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		gateway.WriteJSON(w, http.StatusOK, map[string]any{"repos": cfg.Repos})
	})
	mux.HandleFunc("GET /git/{owner}/{repo}/info/refs", r.gitInfoRefs)
	mux.HandleFunc("POST /git/{owner}/{repo}/git-upload-pack", r.gitService)
	mux.HandleFunc("POST /git/{owner}/{repo}/git-receive-pack", r.gitService)
	mux.HandleFunc("/api/", r.api)
	return mux
}

// pinned answers the request itself when the repository is not pinned at the
// required level and reports whether the caller may continue.
func (r *relay) pinned(w http.ResponseWriter, repo string, required Access) bool {
	access, ok := r.cfg.Access(repo)
	if !ok {
		gateway.WriteError(w, http.StatusForbidden, fmt.Sprintf("%s is not among the repositories pinned for this project (%s)", repo, strings.Join(r.cfg.SortedRepos(), ", ")))
		return false
	}
	if required == AccessWrite && access != AccessWrite {
		gateway.WriteError(w, http.StatusForbidden, fmt.Sprintf("%s is pinned read-only for this project", repo))
		return false
	}
	return true
}

func gitRepo(req *http.Request) (string, bool) {
	name, ok := strings.CutSuffix(req.PathValue("repo"), ".git")
	if !ok {
		return "", false
	}
	repo := req.PathValue("owner") + "/" + name
	if ValidateRepo(repo) != nil {
		return "", false
	}
	return repo, true
}

func (r *relay) gitInfoRefs(w http.ResponseWriter, req *http.Request) {
	repo, ok := gitRepo(req)
	if !ok {
		gateway.WriteError(w, http.StatusNotFound, "not a git repository path")
		return
	}
	service := req.URL.Query().Get("service")
	required := AccessRead
	switch service {
	case "git-upload-pack":
	case "git-receive-pack":
		required = AccessWrite
	default:
		gateway.WriteError(w, http.StatusBadRequest, "only the smart HTTP protocol is relayed; service must be git-upload-pack or git-receive-pack")
		return
	}
	if !r.pinned(w, repo, required) {
		return
	}
	r.forwardGit(w, req, repo, "/info/refs", req.URL.RawQuery, false)
}

func (r *relay) gitService(w http.ResponseWriter, req *http.Request) {
	repo, ok := gitRepo(req)
	if !ok {
		gateway.WriteError(w, http.StatusNotFound, "not a git repository path")
		return
	}
	push := strings.HasSuffix(req.URL.Path, "/git-receive-pack")
	required := AccessRead
	if push {
		required = AccessWrite
	}
	if !r.pinned(w, repo, required) {
		return
	}
	suffix := "/git-upload-pack"
	if push {
		suffix = "/git-receive-pack"
	}
	r.forwardGit(w, req, repo, suffix, "", push)
}

func (r *relay) forwardGit(w http.ResponseWriter, req *http.Request, repo, suffix, query string, push bool) {
	target := *r.gitUpstream
	target.Path = r.gitUpstream.Path + "/" + repo + ".git" + suffix
	target.RawQuery = query
	upstream, err := http.NewRequestWithContext(req.Context(), req.Method, target.String(), req.Body)
	if err != nil {
		panic(err)
	}
	upstream.ContentLength = req.ContentLength
	for _, name := range []string{"Content-Type", "Accept", "Content-Encoding", "Git-Protocol"} {
		if value := req.Header.Get(name); value != "" {
			upstream.Header.Set(name, value)
		}
	}
	upstream.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("x-access-token:"+r.token)))
	resp, err := r.gitClient.Do(upstream)
	if err != nil {
		gateway.WriteError(w, http.StatusBadGateway, "github.com request failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if !push || resp.StatusCode != http.StatusOK {
		copyResponse(w, resp, nil)
		return
	}
	// The pack has been applied once GitHub answers 200; the audit record is
	// made before git hears about it, so a push the host could not record
	// fails loudly rather than silently.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxWriteResp))
	if err != nil {
		gateway.WriteError(w, http.StatusBadGateway, "read github.com response: "+err.Error())
		return
	}
	if err := r.onWrite(WriteEvent{Operation: "push", Repo: repo}); err != nil {
		http.Error(w, fmt.Sprintf("the push reached %s but recording the audit event failed: %v", repo, err), http.StatusInternalServerError)
		return
	}
	copyResponse(w, resp, body)
}

// cleanAPIPath rejects what the allowlist patterns cannot see: GitHub resolves
// dot segments, so a percent-encoded ".." inside a wildcard would walk out of
// the pinned repository, and an encoded "?" would add query parameters.
func cleanAPIPath(p string) (string, bool) {
	if p == "" || p[0] != '/' || path.Clean(p) != p || strings.ContainsAny(p, "?#") {
		return "", false
	}
	for _, c := range p {
		if unicode.IsControl(c) {
			return "", false
		}
	}
	return p, true
}

func (r *relay) api(w http.ResponseWriter, req *http.Request) {
	path, ok := cleanAPIPath(strings.TrimPrefix(req.URL.Path, "/api"))
	if !ok {
		gateway.WriteError(w, http.StatusBadRequest, "the path is not a plain API path")
		return
	}
	var route *apiRoute
	var match []string
	for i := range apiRoutes {
		if apiRoutes[i].method != req.Method {
			continue
		}
		if m := apiRoutes[i].pattern.FindStringSubmatch(path); m != nil {
			route, match = &apiRoutes[i], m
			break
		}
	}
	if route == nil {
		gateway.WriteError(w, http.StatusForbidden, fmt.Sprintf("%s %s is not an operation the relay provides; see docs/GITHUB.md", req.Method, path))
		return
	}
	names := route.pattern.SubexpNames()
	var owner, name, target string
	for i, n := range names {
		switch n {
		case "owner":
			owner = match[i]
		case "repo":
			name = match[i]
		case "target":
			target = match[i]
		}
	}
	repo := owner + "/" + name
	if !r.pinned(w, repo, route.access) {
		return
	}

	var body []byte
	if route.access == AccessWrite {
		var err error
		body, err = io.ReadAll(http.MaxBytesReader(w, req.Body, maxWriteBody))
		if err != nil {
			gateway.WriteError(w, http.StatusBadRequest, "read request body: "+err.Error())
			return
		}
		if msg := checkFields(body, route.fields); msg != "" {
			gateway.WriteError(w, http.StatusBadRequest, msg)
			return
		}
	}

	upstreamURL := *r.apiUpstream
	upstreamURL.Path = r.apiUpstream.Path + path
	upstreamURL.RawQuery = req.URL.RawQuery
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	upstream, err := http.NewRequestWithContext(req.Context(), req.Method, upstreamURL.String(), reqBody)
	if err != nil {
		panic(err)
	}
	upstream.Header.Set("Accept", "application/vnd.github+json")
	if accept := req.Header.Get("Accept"); accept != "" {
		upstream.Header.Set("Accept", accept)
	}
	if body != nil {
		upstream.Header.Set("Content-Type", "application/json")
	}
	upstream.Header.Set("X-GitHub-Api-Version", apiVersion)
	upstream.Header.Set("Authorization", "Bearer "+r.token)
	resp, err := r.apiClient.Do(upstream)
	if err != nil {
		gateway.WriteError(w, http.StatusBadGateway, "api.github.com request failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if route.access != AccessWrite || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		copyResponse(w, resp, nil)
		return
	}

	result, err := io.ReadAll(io.LimitReader(resp.Body, maxWriteResp))
	if err != nil {
		gateway.WriteError(w, http.StatusBadGateway, "read api.github.com response: "+err.Error())
		return
	}
	if target == "" {
		target = numberOf(result)
	}
	if err := r.onWrite(WriteEvent{Operation: route.operation, Repo: repo, Target: target}); err != nil {
		gateway.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"error":  fmt.Sprintf("the %s on %s succeeded but recording the audit event failed: %v", route.operation, repo, err),
			"result": json.RawMessage(result),
		})
		return
	}
	copyResponse(w, resp, result)
}

// checkFields rejects a JSON body that carries fields outside the
// operation's set; an operation without fields takes an empty body only.
func checkFields(body []byte, allowed []string) string {
	if len(bytes.TrimSpace(body)) == 0 {
		return ""
	}
	if allowed == nil {
		return "this operation does not take a request body"
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return "invalid JSON body: " + err.Error()
	}
	for field := range fields {
		found := false
		for _, name := range allowed {
			if field == name {
				found = true
				break
			}
		}
		if !found {
			return fmt.Sprintf("field %q is not accepted here; allowed: %s", field, strings.Join(allowed, ", "))
		}
	}
	return ""
}

// numberOf reads the item number from a create response; an empty string
// means GitHub did not return one.
func numberOf(result []byte) string {
	var created struct {
		Number *json.Number `json:"number"`
	}
	if err := json.Unmarshal(result, &created); err != nil || created.Number == nil {
		return ""
	}
	return created.Number.String()
}

// copyResponse passes GitHub's answer through: status, the headers the VM
// client reads, and the body, either the buffered one or streamed.
func copyResponse(w http.ResponseWriter, resp *http.Response, body []byte) {
	for _, name := range []string{"Content-Type", "Link", "Content-Length"} {
		if value := resp.Header.Get(name); value != "" && (name != "Content-Length" || body == nil) {
			w.Header().Set(name, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if body != nil {
		w.Write(body)
		return
	}
	io.Copy(w, resp.Body)
}
