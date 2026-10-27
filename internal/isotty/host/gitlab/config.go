// Package gitlab relays a fixed set of GitLab operations from the VM to the
// GitLab instances the human holds tokens for, for the projects the human
// pinned and at the access level given to each. See docs/GITLAB.md.
package gitlab

import (
	"fmt"
	"net/url"
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

// Instance is one GitLab the project reaches: its web URL, which the API
// and git over HTTPS hang off, and a personal access token. The token is
// meant to be a fine-grained one scoped to the pinned projects, so it is
// kept per isotty project, on the host only.
type Instance struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// HostConfig lists the instances of one isotty project with their tokens.
type HostConfig struct {
	Instances []Instance `json:"instances"`
	path      string
}

// Kind is GitLab as the config files see it: .isotty/gitlab.yaml lists
// projects as "host/group/project", the host naming the instance.
var Kind = forge.Kind{Name: "gitlab", Title: "GitLab", ValidateRepo: ValidateRepo}

// A project key is the instance's host name followed by the project path,
// which may nest groups.
var repoPattern = regexp.MustCompile(`^[A-Za-z0-9.-]+(/[A-Za-z0-9_.-]+){2,}$`)

func HostConfigPath(homeDir, projectHash string) string {
	return filepath.Join(homeDir, "projects", projectHash+".gitlab-tokens.yaml")
}

func LoadHostConfig(homeDir, projectHash string) (HostConfig, error) {
	path := HostConfigPath(homeDir, projectHash)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return HostConfig{}, fmt.Errorf("%s does not exist; run `isotty host gitlab login --url <https://instance>` in this project first", path)
		}
		return HostConfig{}, fmt.Errorf("read %s: %w", path, err)
	}
	cfg := HostConfig{path: path}
	if err := yaml.UnmarshalStrict(data, &cfg); err != nil {
		return HostConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(cfg.Instances) == 0 {
		return HostConfig{}, fmt.Errorf("%s lists no instance", path)
	}
	for _, instance := range cfg.Instances {
		if _, err := instance.host(); err != nil {
			return HostConfig{}, fmt.Errorf("%s: %w", path, err)
		}
		if strings.TrimSpace(instance.Token) == "" {
			return HostConfig{}, fmt.Errorf("%s: instance %s has no token", path, instance.URL)
		}
	}
	return cfg, nil
}

func (i Instance) host() (string, error) {
	parsed, err := url.Parse(i.URL)
	if err != nil || parsed.Scheme != "https" && parsed.Scheme != "http" || parsed.Hostname() == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("instance url %q is not an http(s) URL with a host", i.URL)
	}
	return parsed.Hostname(), nil
}

// Find returns the instance behind a project key's host.
func (c HostConfig) Find(host string) (Instance, bool) {
	for _, instance := range c.Instances {
		if name, _ := instance.host(); strings.EqualFold(name, host) {
			return instance, true
		}
	}
	return Instance{}, false
}

// apiURL is the v4 endpoint for the project path under the instance.
func (i Instance) apiURL(path string) string {
	return strings.TrimRight(i.URL, "/") + "/api/v4/projects/" + strings.ReplaceAll(url.PathEscape(path), "/", "%2F")
}

func ValidateRepo(repo string) error {
	if !repoPattern.MatchString(repo) {
		return fmt.Errorf("invalid project %q: expected host/group/project", repo)
	}
	return nil
}

// Split separates a project key into the instance host and the project path.
func Split(repo string) (host, path string, err error) {
	if err := ValidateRepo(repo); err != nil {
		return "", "", err
	}
	host, path, _ = strings.Cut(repo, "/")
	return host, path, nil
}

// Path is the file the config was loaded from or will be saved to.
func (c HostConfig) Path() string { return c.path }

// instanceFor returns the instance of a project key, or an error naming
// the token file when none is listed for the host.
func (c HostConfig) instanceFor(repo string) (Instance, string, error) {
	host, path, err := Split(repo)
	if err != nil {
		return Instance{}, "", err
	}
	instance, ok := c.Find(host)
	if !ok {
		return Instance{}, "", fmt.Errorf("no instance for %s in %s; run `isotty host gitlab login --url https://%s` in this project", host, c.path, host)
	}
	return instance, path, nil
}

// Put replaces the instance with the same host or adds it.
func (c *HostConfig) Put(instance Instance) error {
	host, err := instance.host()
	if err != nil {
		return err
	}
	for i := range c.Instances {
		if name, _ := c.Instances[i].host(); strings.EqualFold(name, host) {
			c.Instances[i] = instance
			return nil
		}
	}
	c.Instances = append(c.Instances, instance)
	return nil
}

// LoadOrEmptyHostConfig is what login builds on: the project's token file,
// or an empty one when no instance has been logged in yet.
func LoadOrEmptyHostConfig(homeDir, projectHash string) (HostConfig, error) {
	path := HostConfigPath(homeDir, projectHash)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return HostConfig{path: path}, nil
	}
	return LoadHostConfig(homeDir, projectHash)
}

func SaveHostConfig(cfg HostConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal gitlab tokens: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(cfg.path), err)
	}
	return os.WriteFile(cfg.path, data, 0o600)
}
