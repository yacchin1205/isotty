package epweave

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

// HostConfig holds the ep_weave credentials. It lives on the host only and is
// never synced into the VM; only the gateway uses it.
type HostConfig struct {
	URL          string `json:"url"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// ProjectConfig enables ep_weave for a project. Pads whose title starts with
// "<prefix>/" are writable from the VM; every pad is readable.
type ProjectConfig struct {
	Prefix string `json:"prefix"`
}

func HostConfigPath(homeDir string) string {
	return filepath.Join(homeDir, "epweave.yaml")
}

func ProjectConfigPath(projectPath string) string {
	return filepath.Join(projectPath, ".isotty", "epweave.yaml")
}

func LoadHostConfig(homeDir string) (HostConfig, error) {
	path := HostConfigPath(homeDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return HostConfig{}, fmt.Errorf("%s does not exist; it must define url, client_id and client_secret for ep_weave", path)
		}
		return HostConfig{}, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg HostConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return HostConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	for key, value := range map[string]string{"url": cfg.URL, "client_id": cfg.ClientID, "client_secret": cfg.ClientSecret} {
		if strings.TrimSpace(value) == "" {
			return HostConfig{}, fmt.Errorf("%s does not define %s", path, key)
		}
	}
	cfg.URL = strings.TrimRight(cfg.URL, "/")
	return cfg, nil
}

// LoadProjectConfig reports enabled=false when the project has no ep_weave config.
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
	if err := ValidatePrefix(cfg.Prefix); err != nil {
		return ProjectConfig{}, err
	}
	return cfg, nil
}

func SaveProjectConfig(projectPath string, cfg ProjectConfig) error {
	if err := ValidatePrefix(cfg.Prefix); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal epweave config: %w", err)
	}
	path := ProjectConfigPath(projectPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create epweave config directory: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

func RemoveProjectConfig(projectPath string) error {
	path := ProjectConfigPath(projectPath)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("ep_weave is not enabled for this project (%s does not exist)", path)
		}
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

// TitlePrefix is the prefix every title written from the VM must carry.
func (c ProjectConfig) TitlePrefix() string {
	return c.Prefix + "/"
}

// ValidatePrefix rejects anything that would break the "<prefix>/<topic>"
// title convention or get indexed as a hashtag by ep_weave.
func ValidatePrefix(prefix string) error {
	if prefix == "" {
		return errors.New("prefix is required")
	}
	if strings.ContainsAny(prefix, "/#") || strings.ContainsFunc(prefix, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' }) {
		return fmt.Errorf("invalid prefix %q: it must not contain '/', '#' or whitespace", prefix)
	}
	return nil
}
