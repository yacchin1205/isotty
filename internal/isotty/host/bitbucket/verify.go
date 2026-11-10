package bitbucket

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxAnswer = 4 << 20

// Verify asks api.bitbucket.org for the repository with the host token and
// returns its name as Bitbucket spells it. A repository the token cannot
// see answers 404, whether it is missing or private. The repository itself
// does not say what the token may do with it; the workspace's permission
// list does, so a write pin is refused below the write permission and an
// admin pin below admin.
func Verify(token, repo string, access Access) (string, error) {
	return verify(token, repo, access, apiUpstream)
}

func verify(token, repo string, access Access, upstream string) (string, error) {
	body, status, err := get(token, upstream+"/repositories/"+repo)
	if err != nil {
		return "", fmt.Errorf("repository %s: %w", repo, err)
	}
	switch status {
	case http.StatusOK:
	case http.StatusNotFound:
		return "", fmt.Errorf("repository %s does not exist or is not visible to the token in ~/.isotty/bitbucket.yaml", repo)
	case http.StatusUnauthorized, http.StatusForbidden:
		return "", fmt.Errorf("api.bitbucket.org rejected the token in ~/.isotty/bitbucket.yaml (%d): %s", status, strings.TrimSpace(string(body)))
	default:
		return "", fmt.Errorf("repository %s: api.bitbucket.org answered %d: %s", repo, status, strings.TrimSpace(string(body)))
	}
	var info struct {
		FullName string `json:"full_name"`
	}
	if err := json.Unmarshal(body, &info); err != nil || info.FullName == "" {
		return "", fmt.Errorf("repository %s: answer names no full_name: %s", repo, strings.TrimSpace(string(body)))
	}
	if !access.Covers(AccessWrite) {
		return info.FullName, nil
	}
	permission, err := permission(token, info.FullName, upstream)
	if err != nil {
		return "", err
	}
	if permission != "write" && permission != "admin" {
		return "", fmt.Errorf("the token has %s permission on %s, so it cannot push; pin it with --allow comment or read", permission, info.FullName)
	}
	if access == AccessAdmin && permission != "admin" {
		return "", fmt.Errorf("the token has %s permission on %s, so it cannot change its settings; pin it with --allow write or below", permission, info.FullName)
	}
	return info.FullName, nil
}

// permission is the token's own permission on the repository: what the
// workspace lists for the calling user, "none" when it lists nothing.
func permission(token, fullName, upstream string) (string, error) {
	workspace, _, _ := strings.Cut(fullName, "/")
	query := url.Values{"q": {fmt.Sprintf("repository.full_name=%q", fullName)}}
	body, status, err := get(token, upstream+"/user/workspaces/"+workspace+"/permissions/repositories?"+query.Encode())
	if err != nil {
		return "", fmt.Errorf("permission on %s: %w", fullName, err)
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("permission on %s: api.bitbucket.org answered %d: %s", fullName, status, strings.TrimSpace(string(body)))
	}
	var answer struct {
		Values []struct {
			Permission string `json:"permission"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
		} `json:"values"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return "", fmt.Errorf("permission on %s: %w: %s", fullName, err, strings.TrimSpace(string(body)))
	}
	for _, value := range answer.Values {
		if strings.EqualFold(value.Repository.FullName, fullName) {
			return value.Permission, nil
		}
	}
	return "none", nil
}

func get(token, target string) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return nil, 0, fmt.Errorf("read response: %w", err)
	}
	return body, resp.StatusCode, nil
}

// WhoAmI asks api.bitbucket.org which account the token belongs to, which
// proves the token before it is written down.
func WhoAmI(token string) (string, error) {
	return whoAmI(token, apiUpstream)
}

func whoAmI(token, upstream string) (string, error) {
	body, status, err := get(token, upstream+"/user")
	if err != nil {
		return "", fmt.Errorf("api.bitbucket.org: %w", err)
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("api.bitbucket.org answered %d to the token: %s", status, strings.TrimSpace(string(body)))
	}
	var user struct {
		DisplayName string `json:"display_name"`
		Nickname    string `json:"nickname"`
	}
	if err := json.Unmarshal(body, &user); err != nil || user.DisplayName == "" && user.Nickname == "" {
		return "", fmt.Errorf("api.bitbucket.org: /user names no account: %s", strings.TrimSpace(string(body)))
	}
	if user.Nickname != "" {
		return user.Nickname, nil
	}
	return user.DisplayName, nil
}
