// Package bitbucket relays a fixed set of Bitbucket Cloud operations from
// the VM to bitbucket.org with the host's token, for the repositories the
// human pinned and at the access level given to each. See
// docs/BITBUCKET.md.
package bitbucket

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

// Kind is Bitbucket as the config files see it: .isotty/bitbucket.yaml
// lists repositories as "workspace/repo_slug".
var Kind = forge.Kind{Name: "bitbucket", Title: "Bitbucket", ValidateRepo: ValidateRepo}

// HostConfig holds the Atlassian API token. It lives on the host only and
// is never synced into the VM; only the relay uses it.
type HostConfig struct {
	Token string `json:"token"`
}

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func HostConfigPath(homeDir string) string {
	return filepath.Join(homeDir, "bitbucket.yaml")
}

func LoadHostConfig(homeDir string) (HostConfig, error) {
	path := HostConfigPath(homeDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return HostConfig{}, fmt.Errorf("%s does not exist; run `isotty host bitbucket login` first", path)
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
		return fmt.Errorf("invalid repository %q: expected workspace/repo_slug", repo)
	}
	return nil
}

// SaveHostConfig writes the token file for the host only to read.
func SaveHostConfig(homeDir string, cfg HostConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal bitbucket token: %w", err)
	}
	path := HostConfigPath(homeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	return os.WriteFile(path, data, 0o600)
}
