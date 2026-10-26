package github

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// GitHub remote URLs come in three shapes; everything else is not a GitHub
// repository as far as the relay is concerned.
var remotePatterns = []*regexp.Regexp{
	regexp.MustCompile(`^https://(?:[^@/]+@)?github\.com/([^/]+)/([^/]+?)(?:\.git)?/?$`),
	regexp.MustCompile(`^ssh://git@github\.com/([^/]+)/([^/]+?)(?:\.git)?/?$`),
	regexp.MustCompile(`^git@github\.com:([^/]+)/([^/]+?)(?:\.git)?/?$`),
}

// ResolveRemote returns the owner/name behind a remote of the project on the
// host, read at this moment and never again: `.git/` is synced with the VM,
// so a remote name is not a stable statement of which repository is meant.
func ResolveRemote(projectPath, remote string) (string, error) {
	output, err := exec.Command("git", "-C", projectPath, "remote", "get-url", remote).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("remote %q: %s", remote, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("git remote get-url %s: %w", remote, err)
	}
	repo, err := ParseRemoteURL(strings.TrimSpace(string(output)))
	if err != nil {
		return "", fmt.Errorf("remote %q: %w", remote, err)
	}
	return repo, nil
}

func ParseRemoteURL(url string) (string, error) {
	for _, pattern := range remotePatterns {
		if m := pattern.FindStringSubmatch(url); m != nil {
			return m[1] + "/" + m[2], nil
		}
	}
	return "", fmt.Errorf("%s is not a github.com repository URL", url)
}
