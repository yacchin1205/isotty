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
	forge.Read("GET", "/rulesets"),
	forge.Read("GET", "/rulesets/{n}"),
	forge.Read("GET", "/actions/permissions"),
	forge.Read("GET", "/actions/permissions/workflow"),
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
	// The admin routes are the repository's settings and its branch
	// protection. The fields leave out what renames, hides, archives or
	// templates the repository (name, private, visibility, archived,
	// is_template) and the security settings.
	forge.Admin("PATCH", "", "repo-edit", "description", "homepage", "default_branch",
		"has_issues", "has_projects", "has_wiki", "has_discussions",
		"allow_merge_commit", "allow_squash_merge", "allow_rebase_merge", "allow_auto_merge", "allow_update_branch",
		"delete_branch_on_merge", "merge_commit_title", "merge_commit_message",
		"squash_merge_commit_title", "squash_merge_commit_message", "web_commit_signoff_required"),
	forge.Admin("PUT", "/topics", "repo-edit", "names"),
	forge.Admin("PUT", "/branches/{b}/protection", "protection-set", "required_status_checks", "enforce_admins",
		"required_pull_request_reviews", "restrictions", "required_linear_history", "allow_force_pushes",
		"allow_deletions", "block_creations", "required_conversation_resolution", "lock_branch", "allow_fork_syncing"),
	forge.Admin("DELETE", "/branches/{b}/protection", "protection-remove"),
	forge.Admin("POST", "/rulesets", "ruleset-create", rulesetFields...),
	forge.Admin("PUT", "/rulesets/{n}", "ruleset-edit", rulesetFields...),
	forge.Admin("DELETE", "/rulesets/{n}", "ruleset-remove"),
	forge.Admin("PUT", "/actions/permissions", "actions-permissions", "enabled", "allowed_actions"),
	forge.Admin("PUT", "/actions/permissions/workflow", "actions-permissions", "default_workflow_permissions", "can_approve_pull_request_reviews"),
	forge.Admin("PUT", "/environments/{b}", "environment-set", "wait_timer", "prevent_self_review", "reviewers", "deployment_branch_policy"),
	forge.Admin("DELETE", "/environments/{b}", "environment-remove"),
	forge.Admin("POST", "/pages", "pages-enable", "build_type", "source"),
	forge.Admin("PUT", "/pages", "pages-edit", "cname", "https_enforced", "build_type", "source"),
	forge.Admin("DELETE", "/pages", "pages-disable"),
}

var rulesetFields = []string{"name", "target", "enforcement", "bypass_actors", "conditions", "rules"}

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

func (g *github) Prepare(req *http.Request, _ string, git bool) {
	if git {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("x-access-token:"+g.token)))
		return
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("Authorization", "Bearer "+g.token)
}

func (g *github) IsPublic(client *http.Client, repo string) (bool, error) {
	req, err := http.NewRequest(http.MethodGet, g.api+"/repos/"+repo, nil)
	if err != nil {
		return false, err
	}
	g.Prepare(req, repo, false)
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
			Visibility string `json:"visibility"`
		}
		if err := json.Unmarshal(body, &info); err != nil || info.Visibility == "" {
			return false, fmt.Errorf("repository answer does not say its visibility: %s", strings.TrimSpace(string(body)))
		}
		return info.Visibility == "public", nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("api.github.com answered %d", resp.StatusCode)
	}
}

func (g *github) Routes() []forge.Route { return routes }

// CreatedTarget is the number of a pull request or issue; a ruleset has
// only an id.
func (g *github) CreatedTarget(result []byte) string {
	if number := forge.NumberOf(result, "number"); number != "" {
		return number
	}
	return forge.NumberOf(result, "id")
}
