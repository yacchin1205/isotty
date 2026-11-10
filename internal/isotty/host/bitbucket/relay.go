package bitbucket

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
	gitUpstream = "https://bitbucket.org"
	apiUpstream = "https://api.bitbucket.org/2.0"
	// gitUser is the static user name Bitbucket takes with an API token
	// over git's HTTPS; the token alone names the account.
	gitUser = "x-bitbucket-api-token-auth"
)

type WriteEvent = forge.WriteEvent

// routes are the allowed REST calls under /repositories/{workspace}/{repo_slug}.
var routes = []forge.Route{
	forge.Read("GET", ""),
	forge.Read("GET", "/pullrequests"),
	forge.Read("GET", "/pullrequests/{n}"),
	forge.Read("GET", "/pullrequests/{n}/comments"),
	forge.Read("GET", "/pullrequests/{n}/activity"),
	forge.Read("GET", "/pullrequests/{n}/commits"),
	forge.Read("GET", "/pullrequests/{n}/diff"),
	forge.Read("GET", "/pullrequests/{n}/diffstat"),
	forge.Read("GET", "/pullrequests/{n}/statuses"),
	forge.Read("GET", "/commit/{sha}/statuses"),
	forge.Read("GET", "/pipelines"),
	forge.Read("GET", "/pipelines/{uuid}"),
	forge.Read("GET", "/pipelines/{uuid}/steps"),
	forge.Read("GET", "/pipelines/{uuid}/steps/{step}"),
	forge.Read("GET", "/pipelines/{uuid}/steps/{step}/log"),
	forge.Read("GET", "/branch-restrictions"),
	forge.Read("GET", "/branch-restrictions/{n}"),
	forge.Read("GET", "/branching-model"),
	// A pull request names its branches inside source and destination;
	// the dotted paths keep a repository out of them, so a pull request
	// cannot come from a fork, and reviewers out of the request.
	forge.Comment("POST", "/pullrequests", "pr-create", "title", "description", "draft", "close_source_branch",
		"source.branch.name", "destination.branch.name"),
	forge.Comment("PUT", "/pullrequests/{n}", "pr-edit", "title", "description"),
	forge.Comment("POST", "/pullrequests/{n}/comments", "comment", "content.raw"),
	forge.Write("POST", "/pipelines/{uuid}/stopPipeline", "pipeline-stop"),
	// The admin routes are the repository's settings and its branch
	// restrictions. The fields leave out what renames, moves or hides the
	// repository (name, project, is_private) and the issue tracker and
	// wiki switches, which Bitbucket no longer has.
	forge.Admin("PUT", "", "repo-edit", "description", "language", "fork_policy", "enforced_signed_commits"),
	forge.Admin("POST", "/branch-restrictions", "restriction-create", restrictionFields...),
	forge.Admin("PUT", "/branch-restrictions/{n}", "restriction-edit", restrictionFields...),
	forge.Admin("DELETE", "/branch-restrictions/{n}", "restriction-remove"),
}

var restrictionFields = []string{"kind", "branch_match_kind", "branch_type", "pattern", "value", "users", "groups"}

var apiPathPattern = regexp.MustCompile(`^/repositories/([^/]+)/([^/]+)(/.*)?$`)

// bitbucket is the forge behind bitbucket.org and api.bitbucket.org.
type bitbucket struct {
	token    string
	git, api string
}

// Handler relays the allowed operations for the VM; the host gateway mounts
// it under /bitbucket.
func Handler(token string, cfg ProjectConfig, onWrite func(WriteEvent) error) http.Handler {
	return newHandler(token, cfg, onWrite, gitUpstream, apiUpstream)
}

func newHandler(token string, cfg ProjectConfig, onWrite func(WriteEvent) error, gitUpstream, apiUpstream string) http.Handler {
	return forge.NewHandler(Kind, &bitbucket{token: token, git: gitUpstream, api: apiUpstream}, cfg, onWrite)
}

func (b *bitbucket) GitTarget(path string) (string, string, error) {
	repo, ok := strings.CutSuffix(path, ".git")
	if !ok {
		return "", "", fmt.Errorf("%s does not end in .git", path)
	}
	if err := ValidateRepo(repo); err != nil {
		return "", "", err
	}
	return repo, b.git + "/" + repo + ".git", nil
}

func (b *bitbucket) APITarget(escapedPath string) (string, string, string, error) {
	m := apiPathPattern.FindStringSubmatch(escapedPath)
	if m == nil {
		return "", "", "", fmt.Errorf("%s is not under /repositories/{workspace}/{repo_slug}", escapedPath)
	}
	repo := m[1] + "/" + m[2]
	if err := ValidateRepo(repo); err != nil {
		return "", "", "", err
	}
	return repo, b.api + "/repositories/" + repo, m[3], nil
}

func (b *bitbucket) Prepare(req *http.Request, _ string, git bool) {
	if git {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(gitUser+":"+b.token)))
		return
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+b.token)
}

// IsPublic reads the repository's is_private; a repository the token
// cannot see answers 404 and is not public.
func (b *bitbucket) IsPublic(client *http.Client, repo string) (bool, error) {
	req, err := http.NewRequest(http.MethodGet, b.api+"/repositories/"+repo, nil)
	if err != nil {
		return false, err
	}
	b.Prepare(req, repo, false)
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
			IsPrivate *bool `json:"is_private"`
		}
		if err := json.Unmarshal(body, &info); err != nil || info.IsPrivate == nil {
			return false, fmt.Errorf("repository answer does not say is_private: %s", strings.TrimSpace(string(body)))
		}
		return !*info.IsPrivate, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("api.bitbucket.org answered %d", resp.StatusCode)
	}
}

func (b *bitbucket) Routes() []forge.Route { return routes }

// CreatedTarget is the id of a pull request, a comment or a branch
// restriction.
func (b *bitbucket) CreatedTarget(result []byte) string {
	return forge.NumberOf(result, "id")
}
