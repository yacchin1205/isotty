package gitlab

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/yazawa/isotty/internal/isotty/host/forge"
)

const maxAnswer = 4 << 20

// grant is how the API reports a role: directly on the project or through
// its group, either of which may be absent.
type grant struct {
	AccessLevel int `json:"access_level"`
}

// Developer is the GitLab role that may push; the API reports roles as
// numbers.
const developerLevel = 30

// Verify asks the instance for the project with its token and returns the
// key with the path as GitLab spells it. A project the token cannot see
// answers 404, whether it is missing or private. GitLab reports the role
// the token holds, so a write pin is refused below Developer.
func Verify(hostCfg HostConfig, repo string, access forge.Access) (string, error) {
	instance, path, err := hostCfg.instanceFor(repo)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodGet, instance.apiURL(path), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("PRIVATE-TOKEN", instance.Token)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("project %s: %w", repo, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return "", fmt.Errorf("project %s: read response: %w", repo, err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return "", fmt.Errorf("project %s does not exist or is not visible to the token of %s", repo, instance.URL)
	case http.StatusUnauthorized:
		return "", fmt.Errorf("%s rejected its token in %s: %s", instance.URL, hostCfg.path, strings.TrimSpace(string(body)))
	default:
		return "", fmt.Errorf("project %s: %s answered %d: %s", repo, instance.URL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var info struct {
		PathWithNamespace string `json:"path_with_namespace"`
		Permissions       struct {
			ProjectAccess *grant `json:"project_access"`
			GroupAccess   *grant `json:"group_access"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(body, &info); err != nil || info.PathWithNamespace == "" {
		return "", fmt.Errorf("project %s: answer names no path_with_namespace: %s", repo, strings.TrimSpace(string(body)))
	}
	level := 0
	for _, granted := range []*grant{info.Permissions.ProjectAccess, info.Permissions.GroupAccess} {
		if granted != nil && granted.AccessLevel > level {
			level = granted.AccessLevel
		}
	}
	host, _, _ := Split(repo)
	name := host + "/" + info.PathWithNamespace
	if access == forge.AccessWrite && level < developerLevel {
		return "", fmt.Errorf("the token holds no Developer role on %s (access level %d), so it cannot push; pin it with --allow comment or read", name, level)
	}
	return name, nil
}

// WhoAmI asks the instance which user the token belongs to, which proves
// the URL and the token before either is written down.
func WhoAmI(instance Instance) (string, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(instance.URL, "/")+"/api/v4/user", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("PRIVATE-TOKEN", instance.Token)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("%s: %w", instance.URL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return "", fmt.Errorf("%s: read response: %w", instance.URL, err)
	}
	if resp.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf("%s accepted the token but refused /api/v4/user (%s); a fine-grained token needs the permission User: Read for this check", instance.URL, strings.TrimSpace(string(body)))
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s answered %d to the token: %s", instance.URL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var user struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(body, &user); err != nil || user.Username == "" {
		return "", fmt.Errorf("%s: /api/v4/user names no username: %s", instance.URL, strings.TrimSpace(string(body)))
	}
	return user.Username, nil
}
