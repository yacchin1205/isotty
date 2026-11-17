// Package redmine relays reads of the pinned Redmine projects from the VM
// to the Redmine with the host's API key, filtered of people, contact
// details and secrets before the answer leaves the host, and recorded. See
// docs/REDMINE.md.
package redmine

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/pin"
	"sigs.k8s.io/yaml"
)

// Name is the file name under .isotty/ and the pin kind.
const Name = "redmine"

// HostConfig holds the Redmine URL and the API key. It lives on the host
// only and is never synced into the VM.
type HostConfig struct {
	URL    string `json:"url"`
	APIKey string `json:"api_key"`
}

// ProjectConfig names the Redmine projects the VM may read, by identifier.
type ProjectConfig struct {
	Projects []string `json:"projects"`
}

// Redmine identifiers are lower-case letters, digits, dashes and
// underscores, and start with a letter or a digit.
var identifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func ValidateIdentifier(identifier string) error {
	if !identifierPattern.MatchString(identifier) {
		return fmt.Errorf("invalid project identifier %q: expected the identifier shown in the project's URL, such as infra or help-desk", identifier)
	}
	return nil
}

func HostConfigPath(homeDir string) string {
	return filepath.Join(homeDir, Name+".yaml")
}

func LoadHostConfig(homeDir string) (HostConfig, error) {
	path := HostConfigPath(homeDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return HostConfig{}, fmt.Errorf("%s does not exist; run `isotty host redmine login <url>` first", path)
		}
		return HostConfig{}, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg HostConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return HostConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	for key, value := range map[string]string{"url": cfg.URL, "api_key": cfg.APIKey} {
		if strings.TrimSpace(value) == "" {
			return HostConfig{}, fmt.Errorf("%s does not define %s", path, key)
		}
	}
	cfg.URL = strings.TrimRight(cfg.URL, "/")
	return cfg, nil
}

// SaveHostConfig writes the host config for the host only to read.
func SaveHostConfig(homeDir string, cfg HostConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal redmine config: %w", err)
	}
	path := HostConfigPath(homeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	return os.WriteFile(path, data, 0o600)
}

// saltSize is the length of the salt the pseudonyms are keyed with.
const saltSize = 32

// SaltPath is the project's salt, on the host only: with it, a pseudonym
// leads back to the Redmine user id.
func SaltPath(homeDir, projectHash string) string {
	return filepath.Join(homeDir, "projects", projectHash+"."+Name+"-salt")
}

// LoadSalt reads the project's salt, making one the first time.
func LoadSalt(path string) ([]byte, error) {
	salt, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		salt = make([]byte, saltSize)
		if _, err := rand.Read(salt); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, salt, 0o600); err != nil {
			return nil, err
		}
		return salt, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(salt) != saltSize {
		return nil, fmt.Errorf("%s holds %d bytes, not a salt of %d", path, len(salt), saltSize)
	}
	return salt, nil
}

func ProjectConfigPath(projectPath string) string {
	return filepath.Join(projectPath, ".isotty", Name+".yaml")
}

// LoadProjectConfig reports enabled=false when the project has no Redmine config.
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

func (c ProjectConfig) validate() error {
	if len(c.Projects) == 0 {
		return errors.New("projects must name at least one project")
	}
	seen := map[string]bool{}
	for _, identifier := range c.Projects {
		if err := ValidateIdentifier(identifier); err != nil {
			return err
		}
		if seen[identifier] {
			return fmt.Errorf("project %s is listed twice", identifier)
		}
		seen[identifier] = true
	}
	return nil
}

// Pinned reports whether the project identifier is in the list.
func (c ProjectConfig) Pinned(identifier string) bool {
	for _, pinned := range c.Projects {
		if pinned == identifier {
			return true
		}
	}
	return false
}

// Put adds the project unless it is listed already.
func (c *ProjectConfig) Put(identifier string) {
	if !c.Pinned(identifier) {
		c.Projects = append(c.Projects, identifier)
		sort.Strings(c.Projects)
	}
}

// Delete reports whether the project was listed.
func (c *ProjectConfig) Delete(identifier string) bool {
	for i, pinned := range c.Projects {
		if pinned == identifier {
			c.Projects = append(c.Projects[:i], c.Projects[i+1:]...)
			return true
		}
	}
	return false
}

func (c ProjectConfig) summary() string {
	return strings.Join(c.Projects, ", ")
}

// VerifyPinned returns the synced config only when it matches the host pin.
func VerifyPinned(homeDir, projectHash string, synced ProjectConfig) (ProjectConfig, error) {
	var pinned ProjectConfig
	ok, err := pin.Load(homeDir, projectHash, Name, &pinned)
	if err != nil {
		return ProjectConfig{}, err
	}
	if !ok {
		return ProjectConfig{}, errors.New("Redmine is configured in .isotty/redmine.yaml but not pinned on this host; run `isotty host redmine add` here for each project to accept it")
	}
	sort.Strings(pinned.Projects)
	sortedSynced := append([]string(nil), synced.Projects...)
	sort.Strings(sortedSynced)
	if strings.Join(pinned.Projects, "\n") != strings.Join(sortedSynced, "\n") {
		return ProjectConfig{}, fmt.Errorf(
			".isotty/redmine.yaml (%s) differs from the projects pinned on this host (%s); the file is synced from the VM, so something in the VM may have edited it. Review it, then run `isotty host redmine add` or `remove` to accept the change",
			synced.summary(), pinned.summary(),
		)
	}
	return synced, nil
}

// LoadPinned returns the synced project config once it has been checked
// against the pin, or nil when the project has no pinned Redmine project.
func LoadPinned(projectPath, projectHash, homeDir string) (*ProjectConfig, error) {
	synced, enabled, err := LoadProjectConfig(projectPath)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, nil
	}
	cfg, err := VerifyPinned(homeDir, projectHash, synced)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

// LoadPin returns what this host has accepted so far, which add and remove
// build on; the synced file may have been edited in the VM.
func LoadPin(homeDir, projectHash string) (ProjectConfig, error) {
	var pinned ProjectConfig
	if _, err := pin.Load(homeDir, projectHash, Name, &pinned); err != nil {
		return ProjectConfig{}, err
	}
	return pinned, nil
}

func PinPath(homeDir, projectHash string) string {
	return pin.Path(homeDir, projectHash, Name)
}

// Save writes the synced file and the pin together; an empty list is no
// config at all.
func Save(projectPath, projectHash, homeDir string, cfg ProjectConfig) error {
	if len(cfg.Projects) == 0 {
		if err := os.Remove(ProjectConfigPath(projectPath)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", ProjectConfigPath(projectPath), err)
		}
		return pin.Remove(homeDir, projectHash, Name)
	}
	if err := cfg.validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal redmine config: %w", err)
	}
	path := ProjectConfigPath(projectPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	return pin.Save(homeDir, projectHash, Name, cfg)
}
