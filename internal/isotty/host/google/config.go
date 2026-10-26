// Package google issues short-lived Google access tokens to the VM for a
// service account whose key stays on the host, with the scopes the human
// pinned. See docs/GOOGLE.md.
package google

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"
)

const scopePrefix = "https://www.googleapis.com/auth/"

var (
	emailPattern = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`)
	scopePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// Key is the part of a service account key file the token issuer needs.
type Key struct {
	Type        string `json:"type"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

// ProjectConfig names the service account the VM acts as and the scopes its
// tokens carry, written without the https://www.googleapis.com/auth/ prefix.
type ProjectConfig struct {
	ServiceAccount string   `json:"service_account"`
	Scopes         []string `json:"scopes"`
}

func KeyDir(homeDir string) string {
	return filepath.Join(homeDir, "google")
}

func KeyPath(homeDir, serviceAccount string) string {
	return filepath.Join(KeyDir(homeDir), serviceAccount+".json")
}

func ProjectConfigPath(projectPath string) string {
	return filepath.Join(projectPath, ".isotty", "google.yaml")
}

// ImportKey copies a service account key file into the host key directory,
// named by the account's address, and returns that address. The copy is
// what the relay uses, so the original can live anywhere the human keeps
// secrets; it must not be inside the project, which is synced into the VM.
func ImportKey(homeDir, projectPath, path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}
	if rel, err := filepath.Rel(projectPath, absPath); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is inside the project, which is synced into the VM; move the key file out of the project first", path)
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	key, err := ParseKey(data)
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	if err := os.MkdirAll(KeyDir(homeDir), 0o700); err != nil {
		return "", fmt.Errorf("create key directory: %w", err)
	}
	if err := os.WriteFile(KeyPath(homeDir, key.ClientEmail), data, 0o600); err != nil {
		return "", err
	}
	return key.ClientEmail, nil
}

func ParseKey(data []byte) (Key, error) {
	var key Key
	if err := json.Unmarshal(data, &key); err != nil {
		return Key{}, fmt.Errorf("parse service account key: %w", err)
	}
	if key.Type != "service_account" {
		return Key{}, fmt.Errorf("not a service account key (type %q)", key.Type)
	}
	for name, value := range map[string]string{"client_email": key.ClientEmail, "private_key": key.PrivateKey, "token_uri": key.TokenURI} {
		if strings.TrimSpace(value) == "" {
			return Key{}, fmt.Errorf("service account key does not define %s", name)
		}
	}
	return key, nil
}

func LoadKey(homeDir, serviceAccount string) (Key, error) {
	path := KeyPath(homeDir, serviceAccount)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Key{}, fmt.Errorf("%s does not exist; run `isotty host google enable --key <file>` to import the key of %s", path, serviceAccount)
		}
		return Key{}, fmt.Errorf("read %s: %w", path, err)
	}
	key, err := ParseKey(data)
	if err != nil {
		return Key{}, fmt.Errorf("%s: %w", path, err)
	}
	if key.ClientEmail != serviceAccount {
		return Key{}, fmt.Errorf("%s holds the key of %s, not %s", path, key.ClientEmail, serviceAccount)
	}
	return key, nil
}

// LoadProjectConfig reports enabled=false when the project has no Google config.
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
		return fmt.Errorf("marshal google config: %w", err)
	}
	path := ProjectConfigPath(projectPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create google config directory: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

func RemoveProjectConfig(projectPath string) error {
	path := ProjectConfigPath(projectPath)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("Google is not enabled for this project (%s does not exist)", path)
		}
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

func (c ProjectConfig) validate() error {
	if !emailPattern.MatchString(c.ServiceAccount) {
		return fmt.Errorf("invalid service_account %q: expected an email address", c.ServiceAccount)
	}
	if len(c.Scopes) == 0 {
		return errors.New("scopes must name at least one scope")
	}
	for _, scope := range c.Scopes {
		if err := ValidateScope(scope); err != nil {
			return err
		}
	}
	return nil
}

// ValidateScope accepts the part after https://www.googleapis.com/auth/.
func ValidateScope(scope string) error {
	if strings.HasPrefix(scope, scopePrefix) {
		return fmt.Errorf("write scope %q without the %s prefix", scope, scopePrefix)
	}
	if !scopePattern.MatchString(scope) {
		return fmt.Errorf("invalid scope %q", scope)
	}
	return nil
}

// ScopeURLs returns the full scope URLs, sorted, for the token request.
func (c ProjectConfig) ScopeURLs() []string {
	urls := make([]string, 0, len(c.Scopes))
	for _, scope := range c.Scopes {
		urls = append(urls, scopePrefix+scope)
	}
	slices.Sort(urls)
	return urls
}

func (c ProjectConfig) sortedScopes() []string {
	scopes := slices.Clone(c.Scopes)
	slices.Sort(scopes)
	return scopes
}
