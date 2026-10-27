package gitlab

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/forge"
)

type WriteEvent = forge.WriteEvent

// routes are the allowed REST calls under /projects/{id}.
var routes = []forge.Route{
	forge.Read("GET", ""),
	forge.Read("GET", "/merge_requests"),
	forge.Read("GET", "/merge_requests/{n}"),
	forge.Read("GET", "/merge_requests/{n}/notes"),
	forge.Read("GET", "/merge_requests/{n}/changes"),
	forge.Read("GET", "/merge_requests/{n}/pipelines"),
	forge.Read("GET", "/issues"),
	forge.Read("GET", "/issues/{n}"),
	forge.Read("GET", "/issues/{n}/notes"),
	forge.Read("GET", "/pipelines"),
	forge.Read("GET", "/pipelines/{n}"),
	forge.Read("GET", "/pipelines/{n}/jobs"),
	forge.Read("GET", "/jobs/{n}"),
	forge.Read("GET", "/jobs/{n}/trace"),
	forge.Read("GET", "/jobs/{n}/artifacts"),
	forge.Read("GET", "/protected_branches"),
	forge.Comment("POST", "/merge_requests", "mr-create", "source_branch", "target_branch", "title", "description"),
	forge.Comment("PUT", "/merge_requests/{n}", "mr-edit", "title", "description"),
	forge.Comment("POST", "/merge_requests/{n}/notes", "comment", "body"),
	forge.Comment("POST", "/issues", "issue-create", "title", "description", "labels"),
	forge.Comment("PUT", "/issues/{n}", "issue-edit", "title", "description", "labels"),
	forge.Comment("POST", "/issues/{n}/notes", "comment", "body"),
	forge.Write("POST", "/pipelines/{n}/retry", "pipeline-retry"),
	forge.Write("POST", "/pipelines/{n}/cancel", "pipeline-cancel"),
	forge.Write("POST", "/jobs/{n}/retry", "job-retry"),
	forge.Write("POST", "/jobs/{n}/cancel", "job-cancel"),
}

// The VM names a project's API as /api/{host}/projects/{path with %2F}.
var apiPathPattern = regexp.MustCompile(`^/([^/]+)/projects/([^/]+)(/.*)?$`)

// gitlab is the forge behind the instances the host config lists.
type gitlab struct {
	hostCfg HostConfig
}

// Handler relays the allowed operations for the VM; the host gateway mounts
// it under /gitlab.
func Handler(hostCfg HostConfig, cfg ProjectConfig, onWrite func(WriteEvent) error) http.Handler {
	return forge.NewHandler(Kind, &gitlab{hostCfg: hostCfg}, cfg, onWrite)
}

func (g *gitlab) GitTarget(path string) (string, string, error) {
	repo, ok := strings.CutSuffix(path, ".git")
	if !ok {
		return "", "", fmt.Errorf("%s does not end in .git", path)
	}
	instance, projectPath, err := g.hostCfg.instanceFor(repo)
	if err != nil {
		return "", "", err
	}
	return repo, strings.TrimRight(instance.URL, "/") + "/" + projectPath + ".git", nil
}

func (g *gitlab) APITarget(escapedPath string) (string, string, string, error) {
	m := apiPathPattern.FindStringSubmatch(escapedPath)
	if m == nil {
		return "", "", "", fmt.Errorf("%s is not under /{host}/projects/{path}", escapedPath)
	}
	projectPath, err := url.PathUnescape(m[2])
	if err != nil {
		return "", "", "", err
	}
	repo := m[1] + "/" + projectPath
	instance, _, err := g.hostCfg.instanceFor(repo)
	if err != nil {
		return "", "", "", err
	}
	return repo, instance.apiURL(projectPath), m[3], nil
}

func (g *gitlab) Prepare(req *http.Request, repo string, git, withToken bool) {
	if !withToken {
		return
	}
	// The pin was checked against the token file when the relay started,
	// so the instance is there.
	instance, _, _ := g.hostCfg.instanceFor(repo)
	if git {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("oauth2:"+instance.Token)))
		return
	}
	req.Header.Set("PRIVATE-TOKEN", instance.Token)
}

// IsPublic asks the instance about the project without a credential: a
// project anyone can see answers 200 with visibility public, an internal or
// private one answers 404.
func (g *gitlab) IsPublic(client *http.Client, repo string) (bool, error) {
	instance, projectPath, err := g.hostCfg.instanceFor(repo)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequest(http.MethodGet, instance.apiURL(projectPath), nil)
	if err != nil {
		return false, err
	}
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
			return false, fmt.Errorf("project answer does not say its visibility: %s", strings.TrimSpace(string(body)))
		}
		return info.Visibility == "public", nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("%s answered %d", instance.URL, resp.StatusCode)
	}
}

func (g *gitlab) Routes() []forge.Route { return routes }

func (g *gitlab) CreatedTarget(result []byte) string { return forge.NumberOf(result, "iid") }
