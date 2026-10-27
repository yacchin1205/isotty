// Package github relays a fixed set of GitHub operations from the VM to
// github.com with the host's token, for the repositories the human pinned
// and at the access level given to each. See docs/GITHUB.md.
package github

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/forge"
	"sigs.k8s.io/yaml"
)

type (
	Access        = forge.Access
	ProjectConfig = forge.ProjectConfig
)

const (
	AccessRead    = forge.AccessRead
	AccessComment = forge.AccessComment
	AccessWrite   = forge.AccessWrite
	AccessAdmin   = forge.AccessAdmin
)

// Kind is GitHub as the config files see it: .isotty/github.yaml lists
// repositories as "owner/name".
var Kind = forge.Kind{Name: "github", Title: "GitHub", ValidateRepo: ValidateRepo}

// HostConfig holds the GitHub token. It lives on the host only and is never
// synced into the VM; only the relay uses it.
type HostConfig struct {
	Token string `json:"token"`
}

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func HostConfigPath(homeDir string) string {
	return filepath.Join(homeDir, "github.yaml")
}

func LoadHostConfig(homeDir string) (HostConfig, error) {
	path := HostConfigPath(homeDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return HostConfig{}, fmt.Errorf("%s does not exist; it must define token for GitHub", path)
		}
		return HostConfig{}, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg HostConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return HostConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return HostConfig{}, fmt.Errorf("%s does not define token", path)
	}
	return cfg, nil
}

func ValidateRepo(repo string) error {
	if !repoPattern.MatchString(repo) {
		return fmt.Errorf("invalid repository %q: expected owner/name", repo)
	}
	return nil
}
