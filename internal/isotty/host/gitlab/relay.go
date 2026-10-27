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
	// The admin routes are the project's settings and its protected
	// branches. The fields leave out what renames, moves or hides the
	// project (name, path, namespace_id, visibility), what brings in
	// people or external sources (request_access_enabled, mirror and
	// import settings) and the container expiration policy.
	forge.Admin("PUT", "", "project-edit", projectFields...),
	forge.Admin("POST", "/protected_branches", "protection-set", "name", "push_access_level", "merge_access_level",
		"unprotect_access_level", "allow_force_push", "allowed_to_push", "allowed_to_merge", "allowed_to_unprotect",
		"code_owner_approval_required"),
	forge.Admin("DELETE", "/protected_branches/{b}", "protection-remove"),
}

// projectFields are the general and CI/CD settings of PUT /projects/{id}.
var projectFields = []string{
	"description", "topics", "default_branch", "lfs_enabled", "packages_enabled", "mr_default_target_self",
	"merge_method", "squash_option", "squash_commit_template", "merge_commit_template", "suggestion_commit_message",
	"remove_source_branch_after_merge", "printing_merge_request_link_enabled", "resolve_outdated_diff_discussions",
	"autoclose_referenced_issues", "issue_branch_template",
	"only_allow_merge_if_pipeline_succeeds", "allow_merge_on_skipped_pipeline",
	"only_allow_merge_if_all_discussions_are_resolved", "only_allow_merge_if_all_status_checks_passed",
	"merge_pipelines_enabled", "merge_trains_enabled", "merge_trains_skip_train_allowed",
	"issues_access_level", "merge_requests_access_level", "repository_access_level", "forking_access_level",
	"wiki_access_level", "snippets_access_level", "builds_access_level", "pages_access_level",
	"analytics_access_level", "releases_access_level", "environments_access_level", "feature_flags_access_level",
	"infrastructure_access_level", "monitor_access_level", "container_registry_access_level",
	"model_experiments_access_level", "model_registry_access_level", "requirements_access_level",
	"security_and_compliance_access_level",
	"ci_config_path", "ci_default_git_depth", "ci_forward_deployment_enabled", "ci_forward_deployment_rollback_allowed",
	"ci_separated_caches", "ci_restrict_pipeline_cancellation_role", "ci_pipeline_variables_minimum_override_role",
	"ci_push_repository_for_job_token_allowed", "ci_allow_fork_pipelines_to_run_in_parent_project",
	"ci_id_token_sub_claim_components", "ci_delete_pipelines_in_seconds",
	"auto_cancel_pending_pipelines", "auto_devops_enabled", "auto_devops_deploy_strategy",
	"build_timeout", "build_git_strategy", "shared_runners_enabled", "group_runners_enabled",
	"keep_latest_artifact", "public_jobs",
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

func (g *gitlab) Prepare(req *http.Request, repo string, git bool) {
	// The repository's host was resolved to an instance before any request
	// is prepared, so the instance is there.
	instance, _, _ := g.hostCfg.instanceFor(repo)
	if git {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("oauth2:"+instance.Token)))
		return
	}
	req.Header.Set("PRIVATE-TOKEN", instance.Token)
}

func (g *gitlab) IsPublic(client *http.Client, repo string) (bool, error) {
	instance, projectPath, err := g.hostCfg.instanceFor(repo)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequest(http.MethodGet, instance.apiURL(projectPath), nil)
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

// CreatedTarget is the iid of a merge request or issue; a protected branch
// is known by its name.
func (g *gitlab) CreatedTarget(result []byte) string {
	if iid := forge.NumberOf(result, "iid"); iid != "" {
		return iid
	}
	var created struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(result, &created); err != nil {
		return ""
	}
	return created.Name
}
