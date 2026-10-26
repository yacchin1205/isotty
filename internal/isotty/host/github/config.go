// Package github relays a fixed set of GitHub operations from the VM to
// github.com with the host's token, for the repositories the human pinned
// and at the access level given to each. See docs/GITHUB.md.
package github

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

type Access string

const (
	AccessRead  Access = "read"
	AccessWrite Access = "write"
)

// HostConfig holds the GitHub token. It lives on the host only and is never
// synced into the VM; only the relay uses it.
type HostConfig struct {
	Token string `json:"token"`
}

// ProjectConfig names the repositories the VM may reach, as "owner/name",
// with the access level of each. Write implies read.
type ProjectConfig struct {
	Repos map[string]Access `json:"repos"`
}

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func HostConfigPath(homeDir string) string {
	return filepath.Join(homeDir, "github.yaml")
}

func ProjectConfigPath(projectPath string) string {
	return filepath.Join(projectPath, ".isotty", "github.yaml")
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

// LoadProjectConfig reports enabled=false when the project has no GitHub config.
func LoadProjectConfig(projectPath string) (ProjectConfig, bool, error) {
	path := ProjectConfigPath(projectPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ProjectConfig{}, false, nil
		}
		return ProjectConfig{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	cfg, err := ParseProjectConfig(data)
	if err != nil {
		return ProjectConfig{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, true, nil
}

func ParseProjectConfig(data []byte) (ProjectConfig, error) {
	var cfg ProjectConfig
	if err := yaml.UnmarshalStrict(data, &cfg); err != nil {
		return ProjectConfig{}, err
	}
	if err := cfg.validate(); err != nil {
		return ProjectConfig{}, err
	}
	return cfg, nil
}

func SaveProjectConfig(projectPath string, cfg ProjectConfig) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal github config: %w", err)
	}
	path := ProjectConfigPath(projectPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create github config directory: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

func RemoveProjectConfig(projectPath string) error {
	path := ProjectConfigPath(projectPath)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("GitHub is not enabled for this project (%s does not exist)", path)
		}
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

func (c ProjectConfig) validate() error {
	if len(c.Repos) == 0 {
		return errors.New("repos must name at least one repository")
	}
	for repo, access := range c.Repos {
		if err := ValidateRepo(repo); err != nil {
			return err
		}
		if err := ValidateAccess(access); err != nil {
			return fmt.Errorf("%s: %w", repo, err)
		}
	}
	return nil
}

// Access returns the level pinned for owner/name; GitHub repository names
// are case-insensitive, so the lookup is too.
func (c ProjectConfig) Access(repo string) (Access, bool) {
	for pinned, access := range c.Repos {
		if strings.EqualFold(pinned, repo) {
			return access, true
		}
	}
	return "", false
}

func (c ProjectConfig) SortedRepos() []string {
	repos := make([]string, 0, len(c.Repos))
	for repo := range c.Repos {
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	return repos
}

func ValidateRepo(repo string) error {
	if !repoPattern.MatchString(repo) {
		return fmt.Errorf("invalid repository %q: expected owner/name", repo)
	}
	return nil
}

func ValidateAccess(access Access) error {
	switch access {
	case AccessRead, AccessWrite:
		return nil
	default:
		return fmt.Errorf("invalid access %q: expected read or write", access)
	}
}

// ParseRepoSpec splits "owner/name[:read|write]"; the access defaults to write.
func ParseRepoSpec(spec string) (string, Access, error) {
	repo, access := spec, AccessWrite
	if i := strings.LastIndex(spec, ":"); i >= 0 {
		repo, access = spec[:i], Access(spec[i+1:])
	}
	if err := ValidateRepo(repo); err != nil {
		return "", "", err
	}
	if err := ValidateAccess(access); err != nil {
		return "", "", fmt.Errorf("%s: %w", spec, err)
	}
	return repo, access, nil
}
