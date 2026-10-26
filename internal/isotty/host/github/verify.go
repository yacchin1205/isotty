package github

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Verify asks api.github.com for the repository with the host token and
// returns its name as GitHub spells it. A repository the token cannot see
// answers 404, whether it is missing or private, and GitHub reports what
// the token may do with it, so a write pin is refused when it cannot push.
func Verify(token, repo string, access Access) (string, error) {
	return verify(token, repo, access, apiUpstream)
}

func verify(token, repo string, access Access, upstream string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, upstream+"/repos/"+repo, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("repository %s: %w", repo, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxWriteResp))
	if err != nil {
		return "", fmt.Errorf("repository %s: read response: %w", repo, err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return "", fmt.Errorf("repository %s does not exist or is not visible to the token in %s", repo, "~/.isotty/github.yaml")
	case http.StatusUnauthorized:
		return "", fmt.Errorf("api.github.com rejected the token in ~/.isotty/github.yaml: %s", strings.TrimSpace(string(body)))
	default:
		return "", fmt.Errorf("repository %s: api.github.com answered %d: %s", repo, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var info struct {
		FullName    string `json:"full_name"`
		Permissions struct {
			Push bool `json:"push"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(body, &info); err != nil || info.FullName == "" {
		return "", fmt.Errorf("repository %s: answer names no full_name: %s", repo, strings.TrimSpace(string(body)))
	}
	if access == AccessWrite && !info.Permissions.Push {
		return "", fmt.Errorf("the token cannot push to %s; pin it as %s:read", info.FullName, info.FullName)
	}
	return info.FullName, nil
}
