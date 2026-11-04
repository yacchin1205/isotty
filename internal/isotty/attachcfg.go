package isotty

import (
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

// AttachConfig is the host's own attach preference, ~/.isotty/attach.yaml.
// It is never synced: how the human's terminal attaches is theirs to choose.
type AttachConfig struct {
	// Tmux unset or true runs the session in tmux; false gives a plain shell.
	Tmux *bool `json:"tmux,omitempty"`
}

func AttachConfigPath(homeDir string) string {
	return filepath.Join(homeDir, "attach.yaml")
}

// LoadAttachConfig returns the defaults when the file does not exist.
func LoadAttachConfig(homeDir string) (AttachConfig, error) {
	path := AttachConfigPath(homeDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return AttachConfig{}, nil
		}
		return AttachConfig{}, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg AttachConfig
	if err := yaml.UnmarshalStrict(data, &cfg); err != nil {
		return AttachConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

func (c AttachConfig) UseTmux() bool {
	return c.Tmux == nil || *c.Tmux
}
