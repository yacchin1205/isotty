package forge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
)

const (
	// Writes are buffered so the audit record can be made before the VM
	// learns the result; the forges' own payloads for these calls are small.
	maxWriteBody = 1 << 20
	maxWriteResp = 4 << 20
)

// WriteEvent describes a write that reached the forge so the caller can
// record it in the VM audit log. Target is the pull or merge request, issue,
// run, pipeline or job the write touched; a push has none.
type WriteEvent struct {
	Operation string
	Repo      string
	Target    string
}

// Route is one allowed REST call, relative to the repository's API prefix.
// The pattern binds target for calls on one item; writes also list the JSON
// fields the VM may send, so an update cannot reach fields the operation
// does not cover (state, for instance, which would close the item).
type Route struct {
	Method    string
	Pattern   *regexp.Regexp
	Access    Access
	Operation string
	Fields    []string
}

func Read(method, path string) Route {
	return Route{Method: method, Pattern: compileRoute(path), Access: AccessRead}
}

func Comment(method, path, operation string, fields ...string) Route {
	return Route{Method: method, Pattern: compileRoute(path), Access: AccessComment, Operation: operation, Fields: fields}
}

func Write(method, path, operation string, fields ...string) Route {
	return Route{Method: method, Pattern: compileRoute(path), Access: AccessWrite, Operation: operation, Fields: fields}
}

// changes reports whether the route writes to the forge, whatever level it needs.
func (r Route) changes() bool { return r.Access != AccessRead }

func compileRoute(path string) *regexp.Regexp {
	expr := strings.NewReplacer(
		"{n}", `(?P<target>[0-9]+)`,
		"{sha}", `[0-9a-fA-F]+`,
		"{b}", `.+`,
	).Replace(path)
	return regexp.MustCompile("^" + expr + "$")
}

// Forge is what differs between the forges behind one relay: how the VM's
// paths name a repository and where that repository lives upstream, which
// headers and credential the upstream takes, how a public repository is
// told, and which REST calls are allowed.
type Forge interface {
	// GitTarget maps the path after /git/, without the protocol suffix, to
	// the repository key and the upstream URL of the bare repository.
	GitTarget(path string) (repo, upstream string, err error)
	// APITarget maps the escaped path after /api to the repository key,
	// the upstream URL of that repository's API and the remainder the
	// routes see, which starts with a slash or is empty.
	APITarget(escapedPath string) (repo, upstream, rest string, err error)
	// Prepare sets the headers the upstream expects and, when withToken,
	// the credential for the repository.
	Prepare(req *http.Request, repo string, git, withToken bool)
	// IsPublic asks the upstream about the repository without a credential,
	// which is the same view the VM would have on its own.
	IsPublic(client *http.Client, repo string) (bool, error)
	Routes() []Route
	// CreatedTarget reads the item identifier from a create response; an
	// empty string means the forge did not return one.
	CreatedTarget(result []byte) string
}

type relay struct {
	kind    Kind
	forge   Forge
	cfg     ProjectConfig
	onWrite func(WriteEvent) error
	// git must see the upstream's redirects itself (a renamed repository is
	// not pinned under its new name); the REST client follows them because
	// logs and artifacts may be served from a signed URL on another host,
	// which the VM must not talk to directly.
	gitClient *http.Client
	apiClient *http.Client

	// public remembers, per repository, whether the upstream answers for it
	// without a credential; see allowed.
	publicMu sync.Mutex
	public   map[string]bool
}

// NewHandler relays the allowed operations for the VM; the host gateway
// mounts it under the forge's own prefix.
func NewHandler(kind Kind, forge Forge, cfg ProjectConfig, onWrite func(WriteEvent) error) http.Handler {
	// Bodies may be large (packs, artifacts), so only the wait for the
	// upstream's first byte is bounded, not the transfer.
	transport := &http.Transport{ResponseHeaderTimeout: 60 * time.Second}
	r := &relay{
		kind:    kind,
		forge:   forge,
		cfg:     cfg,
		onWrite: onWrite,
		public:  map[string]bool{},
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
			req.Header.Del("PRIVATE-TOKEN")
			return nil
		}},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		gateway.WriteJSON(w, http.StatusOK, map[string]any{"repos": cfg.Repos})
	})
	mux.HandleFunc("/git/", r.git)
	mux.HandleFunc("/api/", r.api)
	return mux
}

// allowed answers the request itself when it may not be forwarded. It
// reports whether the relay forwards it and whether the token goes along:
// pinned repositories get the token; a public repository that is not pinned
// is read without it, so the token is never used outside the pin.
func (r *relay) allowed(w http.ResponseWriter, repo string, required Access) (forward, withToken bool) {
	access, pinned := r.cfg.Access(repo)
	if pinned {
		if !access.Covers(required) {
			gateway.WriteError(w, http.StatusForbidden, fmt.Sprintf("%s is pinned as %s for this project; this operation needs %s", repo, access, required))
			return false, false
		}
		return true, true
	}
	if required != AccessRead {
		gateway.WriteError(w, http.StatusForbidden, fmt.Sprintf("%s is not among the repositories pinned for this project (%s); writes need a pin", repo, strings.Join(r.cfg.SortedRepos(), ", ")))
		return false, false
	}
	public, err := r.isPublic(repo)
	if err != nil {
		gateway.WriteError(w, http.StatusBadGateway, fmt.Sprintf("could not tell whether %s is public: %v", repo, err))
		return false, false
	}
	if !public {
		gateway.WriteError(w, http.StatusForbidden, fmt.Sprintf("%s is not among the repositories pinned for this project (%s) and is not public", repo, strings.Join(r.cfg.SortedRepos(), ", ")))
		return false, false
	}
	return true, false
}

func (r *relay) isPublic(repo string) (bool, error) {
	r.publicMu.Lock()
	defer r.publicMu.Unlock()
	if public, ok := r.public[repo]; ok {
		return public, nil
	}
	public, err := r.forge.IsPublic(r.apiClient, repo)
	if err != nil {
		return false, err
	}
	r.public[repo] = public
	return public, nil
}

// git serves the smart HTTP protocol: info/refs announces, upload-pack
// fetches, receive-pack pushes.
func (r *relay) git(w http.ResponseWriter, req *http.Request) {
	rest := strings.TrimPrefix(req.URL.Path, "/git/")
	var suffix string
	var push bool
	switch {
	case req.Method == http.MethodGet && strings.HasSuffix(rest, "/info/refs"):
		suffix = "/info/refs"
		switch req.URL.Query().Get("service") {
		case "git-upload-pack":
		case "git-receive-pack":
			push = true
		default:
			gateway.WriteError(w, http.StatusBadRequest, "only the smart HTTP protocol is relayed; service must be git-upload-pack or git-receive-pack")
			return
		}
	case req.Method == http.MethodPost && strings.HasSuffix(rest, "/git-upload-pack"):
		suffix = "/git-upload-pack"
	case req.Method == http.MethodPost && strings.HasSuffix(rest, "/git-receive-pack"):
		suffix, push = "/git-receive-pack", true
	default:
		gateway.WriteError(w, http.StatusNotFound, "not a git repository path")
		return
	}
	repo, upstream, err := r.forge.GitTarget(strings.TrimSuffix(rest, suffix))
	if err != nil {
		gateway.WriteError(w, http.StatusNotFound, "not a git repository path: "+err.Error())
		return
	}
	required := AccessRead
	if push {
		required = AccessWrite
	}
	forward, withToken := r.allowed(w, repo, required)
	if !forward {
		return
	}
	query := ""
	if suffix == "/info/refs" {
		query = req.URL.RawQuery
	}
	r.forwardGit(w, req, repo, upstream+suffix, query, suffix == "/git-receive-pack", withToken)
}

func (r *relay) forwardGit(w http.ResponseWriter, req *http.Request, repo, target, query string, push, withToken bool) {
	if query != "" {
		target += "?" + query
	}
	upstream, err := http.NewRequestWithContext(req.Context(), req.Method, target, req.Body)
	if err != nil {
		gateway.WriteError(w, http.StatusBadGateway, "upstream URL: "+err.Error())
		return
	}
	upstream.ContentLength = req.ContentLength
	for _, name := range []string{"Content-Type", "Accept", "Content-Encoding", "Git-Protocol"} {
		if value := req.Header.Get(name); value != "" {
			upstream.Header.Set(name, value)
		}
	}
	r.forge.Prepare(upstream, repo, true, withToken)
	resp, err := r.gitClient.Do(upstream)
	if err != nil {
		gateway.WriteError(w, http.StatusBadGateway, "upstream request failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if !push || resp.StatusCode != http.StatusOK {
		copyResponse(w, resp, nil)
		return
	}
	// The pack has been applied once the upstream answers 200; the audit
	// record is made before git hears about it, so a push the host could
	// not record fails loudly rather than silently.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxWriteResp))
	if err != nil {
		gateway.WriteError(w, http.StatusBadGateway, "read upstream response: "+err.Error())
		return
	}
	if err := r.onWrite(WriteEvent{Operation: "push", Repo: repo}); err != nil {
		http.Error(w, fmt.Sprintf("the push reached %s but recording the audit event failed: %v", repo, err), http.StatusInternalServerError)
		return
	}
	copyResponse(w, resp, body)
}

// cleanAPIPath rejects what the allowlist patterns cannot see: the forges
// resolve dot segments, so a percent-encoded ".." inside a wildcard would
// walk out of the pinned repository, and an encoded "?" would add query
// parameters.
func cleanAPIPath(p string) bool {
	if p != "" && (p[0] != '/' || path.Clean(p) != p) || strings.ContainsAny(p, "?#") {
		return false
	}
	for _, c := range p {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

func (r *relay) api(w http.ResponseWriter, req *http.Request) {
	repo, upstreamPrefix, rest, err := r.forge.APITarget(strings.TrimPrefix(req.URL.EscapedPath(), "/api"))
	if err != nil {
		gateway.WriteError(w, http.StatusForbidden, fmt.Sprintf("%s %s is not an operation the relay provides (%v); see docs/%s.md", req.Method, req.URL.Path, err, strings.ToUpper(r.kind.Name)))
		return
	}
	if !cleanAPIPath(rest) {
		gateway.WriteError(w, http.StatusBadRequest, "the path is not a plain API path")
		return
	}
	var route *Route
	var match []string
	routes := r.forge.Routes()
	for i := range routes {
		if routes[i].Method != req.Method {
			continue
		}
		if m := routes[i].Pattern.FindStringSubmatch(rest); m != nil {
			route, match = &routes[i], m
			break
		}
	}
	if route == nil {
		gateway.WriteError(w, http.StatusForbidden, fmt.Sprintf("%s %s is not an operation the relay provides on %s; see docs/%s.md", req.Method, rest, repo, strings.ToUpper(r.kind.Name)))
		return
	}
	var target string
	if i := route.Pattern.SubexpIndex("target"); i > 0 {
		target = match[i]
	}
	forward, withToken := r.allowed(w, repo, route.Access)
	if !forward {
		return
	}

	var body []byte
	if route.changes() {
		body, err = io.ReadAll(http.MaxBytesReader(w, req.Body, maxWriteBody))
		if err != nil {
			gateway.WriteError(w, http.StatusBadRequest, "read request body: "+err.Error())
			return
		}
		if msg := checkFields(body, route.Fields); msg != "" {
			gateway.WriteError(w, http.StatusBadRequest, msg)
			return
		}
	}

	targetURL := upstreamPrefix + rest
	if req.URL.RawQuery != "" {
		targetURL += "?" + req.URL.RawQuery
	}
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	upstream, err := http.NewRequestWithContext(req.Context(), req.Method, targetURL, reqBody)
	if err != nil {
		gateway.WriteError(w, http.StatusBadGateway, "upstream URL: "+err.Error())
		return
	}
	if accept := req.Header.Get("Accept"); accept != "" {
		upstream.Header.Set("Accept", accept)
	}
	if body != nil {
		upstream.Header.Set("Content-Type", "application/json")
	}
	r.forge.Prepare(upstream, repo, false, withToken)
	resp, err := r.apiClient.Do(upstream)
	if err != nil {
		gateway.WriteError(w, http.StatusBadGateway, "upstream request failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if !route.changes() || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		copyResponse(w, resp, nil)
		return
	}

	result, err := io.ReadAll(io.LimitReader(resp.Body, maxWriteResp))
	if err != nil {
		gateway.WriteError(w, http.StatusBadGateway, "read upstream response: "+err.Error())
		return
	}
	if target == "" {
		target = r.forge.CreatedTarget(result)
	}
	if err := r.onWrite(WriteEvent{Operation: route.Operation, Repo: repo, Target: target}); err != nil {
		gateway.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"error":  fmt.Sprintf("the %s on %s succeeded but recording the audit event failed: %v", route.Operation, repo, err),
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

// NumberOf reads a numeric field from a create response as a string; an
// empty string means the field is absent.
func NumberOf(result []byte, field string) string {
	var created map[string]json.RawMessage
	if err := json.Unmarshal(result, &created); err != nil {
		return ""
	}
	var number json.Number
	if err := json.Unmarshal(created[field], &number); err != nil {
		return ""
	}
	return number.String()
}

// copyResponse passes the upstream's answer through: status, the headers
// the VM client reads, and the body, either the buffered one or streamed.
func copyResponse(w http.ResponseWriter, resp *http.Response, body []byte) {
	for _, name := range []string{"Content-Type", "Link", "Content-Length", "X-Next-Page", "X-Total-Pages"} {
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
