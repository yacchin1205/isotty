package gitlab

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// Git remote URLs come in three shapes. The host is kept and the port
// dropped: SSH often listens elsewhere than HTTPS, and the key names the
// instance, whose HTTPS URL the host config knows.
var remotePatterns = []*regexp.Regexp{
	regexp.MustCompile(`^https?://(?:[^@/]+@)?([^/:]+)(?::[0-9]+)?/(.+?)(?:\.git)?/?$`),
	regexp.MustCompile(`^ssh://(?:[^@/]+@)?([^/:]+)(?::[0-9]+)?/(.+?)(?:\.git)?/?$`),
	regexp.MustCompile(`^(?:[^@/:]+@)?([^/:]+):([^/].*?)(?:\.git)?/?$`),
}

// ResolveRemote returns the project key behind a remote of the project on
// the host, read at this moment and never again: `.git/` is synced with the
// VM, so a remote name is not a stable statement of which project is meant.
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
			repo := m[1] + "/" + m[2]
			if err := ValidateRepo(repo); err != nil {
				return "", fmt.Errorf("%s: %w", url, err)
			}
			return repo, nil
		}
	}
	return "", fmt.Errorf("%s is not a git remote URL with a host and a project path", url)
}
