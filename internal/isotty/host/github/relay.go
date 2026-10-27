package github

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/forge"
)

const (
	gitUpstream = "https://github.com"
	apiUpstream = "https://api.github.com"
	apiVersion  = "2022-11-28"
)

type WriteEvent = forge.WriteEvent

// routes are the allowed REST calls under /repos/{owner}/{repo}.
var routes = []forge.Route{
	forge.Read("GET", ""),
	forge.Read("GET", "/pulls"),
	forge.Read("GET", "/pulls/{n}"),
	forge.Read("GET", "/pulls/{n}/reviews"),
	forge.Read("GET", "/pulls/{n}/comments"),
	forge.Read("GET", "/issues"),
	forge.Read("GET", "/issues/{n}"),
	forge.Read("GET", "/issues/{n}/comments"),
	forge.Read("GET", "/commits/{sha}/check-runs"),
	forge.Read("GET", "/actions/runs"),
	forge.Read("GET", "/actions/runs/{n}"),
	forge.Read("GET", "/actions/runs/{n}/jobs"),
	forge.Read("GET", "/actions/runs/{n}/artifacts"),
	forge.Read("GET", "/actions/jobs/{n}/logs"),
	forge.Read("GET", "/actions/artifacts/{n}/zip"),
	forge.Read("GET", "/branches/{b}/protection"),
	forge.Read("GET", "/rules/branches/{b}"),
	forge.Read("GET", "/actions/permissions"),
	forge.Read("GET", "/environments"),
	forge.Read("GET", "/pages"),
	forge.Read("GET", "/actions/secrets"),
	forge.Comment("POST", "/pulls", "pr-create", "title", "head", "base", "body", "draft"),
	forge.Comment("PATCH", "/pulls/{n}", "pr-edit", "title", "body"),
	forge.Comment("POST", "/issues", "issue-create", "title", "body", "labels"),
	forge.Comment("PATCH", "/issues/{n}", "issue-edit", "title", "body", "labels"),
	forge.Comment("POST", "/issues/{n}/comments", "comment", "body"),
	forge.Write("POST", "/actions/runs/{n}/rerun", "run-rerun"),
	forge.Write("POST", "/actions/runs/{n}/rerun-failed-jobs", "run-rerun"),
	forge.Write("POST", "/actions/runs/{n}/cancel", "run-cancel"),
}

var apiPathPattern = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)(/.*)?$`)

// github is the forge behind github.com and api.github.com.
type github struct {
	token    string
	git, api string
}

// Handler relays the allowed operations for the VM; the host gateway mounts
// it under /github.
func Handler(token string, cfg ProjectConfig, onWrite func(WriteEvent) error) http.Handler {
	return newHandler(token, cfg, onWrite, gitUpstream, apiUpstream)
}

func newHandler(token string, cfg ProjectConfig, onWrite func(WriteEvent) error, gitUpstream, apiUpstream string) http.Handler {
	return forge.NewHandler(Kind, &github{token: token, git: gitUpstream, api: apiUpstream}, cfg, onWrite)
}

func (g *github) GitTarget(path string) (string, string, error) {
	repo, ok := strings.CutSuffix(path, ".git")
	if !ok {
		return "", "", fmt.Errorf("%s does not end in .git", path)
	}
	if err := ValidateRepo(repo); err != nil {
		return "", "", err
	}
	return repo, g.git + "/" + repo + ".git", nil
}

func (g *github) APITarget(escapedPath string) (string, string, string, error) {
	m := apiPathPattern.FindStringSubmatch(escapedPath)
	if m == nil {
		return "", "", "", fmt.Errorf("%s is not under /repos/{owner}/{repo}", escapedPath)
	}
	repo := m[1] + "/" + m[2]
	if err := ValidateRepo(repo); err != nil {
		return "", "", "", err
	}
	return repo, g.api + "/repos/" + repo, m[3], nil
}

func (g *github) Prepare(req *http.Request, _ string, git, withToken bool) {
	if git {
		if withToken {
			req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("x-access-token:"+g.token)))
		}
		return
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	if withToken {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
}

// IsPublic asks api.github.com about the repository without a credential;
// a private repository the token could see is still not public.
func (g *github) IsPublic(client *http.Client, repo string) (bool, error) {
	req, err := http.NewRequest(http.MethodGet, g.api+"/repos/"+repo, nil)
	if err != nil {
		return false, err
	}
	g.Prepare(req, repo, false, false)
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return false, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		var info struct {
			Private *bool `json:"private"`
		}
		if err := json.Unmarshal(body, &info); err != nil || info.Private == nil {
			return false, fmt.Errorf("repository answer does not say whether it is private: %s", strings.TrimSpace(string(body)))
		}
		return !*info.Private, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("api.github.com answered %d", resp.StatusCode)
	}
}

func (g *github) Routes() []forge.Route { return routes }

func (g *github) CreatedTarget(result []byte) string { return forge.NumberOf(result, "number") }
