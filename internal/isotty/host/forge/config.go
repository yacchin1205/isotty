// Package forge holds what the GitHub and GitLab relays share: the access
// levels, the pinned repository list, its synced file and its host pin.
// The relays themselves differ in URL shapes, APIs and credentials, and
// live in their own packages.
package forge

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

// Access is what the human allows the VM on a repository. Each level
// includes the ones before it: read is fetch and the reads; comment adds
// opening and commenting on pull or merge requests and issues, which the
// forges let anyone who can see a repository do; write adds push and the
// CI operations, which need push permission.
type Access string

const (
	AccessRead    Access = "read"
	AccessComment Access = "comment"
	AccessWrite   Access = "write"
)

var accessOrder = map[Access]int{AccessRead: 0, AccessComment: 1, AccessWrite: 2}

// Covers reports whether the level allows what required asks for.
func (a Access) Covers(required Access) bool {
	return accessOrder[a] >= accessOrder[required]
}

func ValidateAccess(access Access) error {
	if _, ok := accessOrder[access]; !ok {
		return fmt.Errorf("invalid access %q: expected read, comment or write", access)
	}
	return nil
}

// Kind is one forge as the config files see it: Name is the file name under
// .isotty/ and the pin kind, Title is what messages call it, and
// ValidateRepo says what a repository key looks like there.
type Kind struct {
	Name         string
	Title        string
	ValidateRepo func(string) error
}

// ProjectConfig names the repositories the VM may reach, with the access
// level of each. The key shape is the forge's: owner/name on GitHub,
// host/group/project on GitLab.
type ProjectConfig struct {
	Repos map[string]Access `json:"repos"`
}

func (k Kind) ProjectConfigPath(projectPath string) string {
	return filepath.Join(projectPath, ".isotty", k.Name+".yaml")
}

// LoadProjectConfig reports enabled=false when the project has no config for the forge.
func (k Kind) LoadProjectConfig(projectPath string) (ProjectConfig, bool, error) {
	path := k.ProjectConfigPath(projectPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ProjectConfig{}, false, nil
		}
		return ProjectConfig{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	cfg, err := k.ParseProjectConfig(data)
	if err != nil {
		return ProjectConfig{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, true, nil
}

func (k Kind) ParseProjectConfig(data []byte) (ProjectConfig, error) {
	var cfg ProjectConfig
	if err := yaml.UnmarshalStrict(data, &cfg); err != nil {
		return ProjectConfig{}, err
	}
	if err := k.validate(cfg); err != nil {
		return ProjectConfig{}, err
	}
	return cfg, nil
}

func (k Kind) SaveProjectConfig(projectPath string, cfg ProjectConfig) error {
	if err := k.validate(cfg); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal %s config: %w", k.Name, err)
	}
	path := k.ProjectConfigPath(projectPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s config directory: %w", k.Name, err)
	}
	return os.WriteFile(path, data, 0o644)
}

// RemoveProjectConfig is what an empty list means: no config at all.
func (k Kind) RemoveProjectConfig(projectPath string) error {
	if err := os.Remove(k.ProjectConfigPath(projectPath)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", k.ProjectConfigPath(projectPath), err)
	}
	return nil
}

func (k Kind) validate(c ProjectConfig) error {
	if len(c.Repos) == 0 {
		return errors.New("repos must name at least one repository")
	}
	for repo, access := range c.Repos {
		if err := k.ValidateRepo(repo); err != nil {
			return err
		}
		if err := ValidateAccess(access); err != nil {
			return fmt.Errorf("%s: %w", repo, err)
		}
		for other := range c.Repos {
			if other != repo && strings.EqualFold(other, repo) {
				return fmt.Errorf("%s and %s name the same repository", repo, other)
			}
		}
	}
	return nil
}

// Access returns the level pinned for the repository. Repository names are
// case-insensitive on the forges, so the lookup is too.
func (c ProjectConfig) Access(repo string) (Access, bool) {
	for pinned, access := range c.Repos {
		if strings.EqualFold(pinned, repo) {
			return access, true
		}
	}
	return "", false
}

// Put replaces the entry for the repository, whatever its case, or adds one.
func (c *ProjectConfig) Put(repo string, access Access) {
	c.Delete(repo)
	if c.Repos == nil {
		c.Repos = map[string]Access{}
	}
	c.Repos[repo] = access
}

// Delete reports whether the repository was pinned.
func (c *ProjectConfig) Delete(repo string) bool {
	for pinned := range c.Repos {
		if strings.EqualFold(pinned, repo) {
			delete(c.Repos, pinned)
			return true
		}
	}
	return false
}

func (c ProjectConfig) SortedRepos() []string {
	repos := make([]string, 0, len(c.Repos))
	for repo := range c.Repos {
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	return repos
}

func (c ProjectConfig) summary() string {
	parts := make([]string, 0, len(c.Repos))
	for _, repo := range c.SortedRepos() {
		parts = append(parts, repo+":"+string(c.Repos[repo]))
	}
	return strings.Join(parts, ", ")
}
