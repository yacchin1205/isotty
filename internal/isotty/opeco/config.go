package opeco

import (
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

// Config is one opeco setting file: the host user's ~/.isotty/opeco.yaml,
// which is never synced, or the project's .isotty/opeco.yaml.
type Config struct {
	// Enabled unset or true leaves opeco on; false turns it off.
	Enabled *bool `json:"enabled,omitempty"`
}

func (c Config) On() bool {
	return c.Enabled == nil || *c.Enabled
}

func UserConfigPath(homeDir string) string {
	return filepath.Join(homeDir, "opeco.yaml")
}

func ProjectConfigPath(projectPath string) string {
	return filepath.Join(projectPath, ".isotty", "opeco.yaml")
}

func ParseConfig(data []byte) (Config, error) {
	var cfg Config
	if err := yaml.UnmarshalStrict(data, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// LoadConfig returns the default, opeco on, when the file does not exist.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	cfg, err := ParseConfig(data)
	if err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// Settings decide whether a project's VM gets opeco. Either file turns it
// off and neither turns it back on over the other: the project's file is
// synced into the VM, where the agent can rewrite it, so it must not
// overrule what the human set for themselves on the host.
type Settings struct {
	User    Config
	Project Config
}

func LoadSettings(homeDir, projectPath string) (Settings, error) {
	user, err := LoadConfig(UserConfigPath(homeDir))
	if err != nil {
		return Settings{}, err
	}
	project, err := LoadConfig(ProjectConfigPath(projectPath))
	if err != nil {
		return Settings{}, err
	}
	return Settings{User: user, Project: project}, nil
}

func (s Settings) Enabled() bool {
	return s.User.On() && s.Project.On()
}

// DisableProject turns opeco off for the project.
func DisableProject(projectPath string) error {
	off := false
	data, err := yaml.Marshal(Config{Enabled: &off})
	if err != nil {
		return fmt.Errorf("marshal opeco config: %w", err)
	}
	path := ProjectConfigPath(projectPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create opeco config directory: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// EnableProject takes the project's setting away, which leaves the default.
func EnableProject(projectPath string) error {
	path := ProjectConfigPath(projectPath)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("opeco is not disabled for this project (%s does not exist)", path)
		}
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}
