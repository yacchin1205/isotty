package docker

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"sigs.k8s.io/yaml"
)

// DefaultKeep is how long the host keeps what passed through when
// ~/.isotty/docker.yaml does not say.
const DefaultKeep = 4 * time.Hour

// HostConfig is how this machine's owner has the host keep what passes
// between the VMs and their registries. It is one setting for the machine,
// not per project: whether and how long images may stay on this disk is
// the disk owner's call. The file is optional and written by hand, like
// ~/.isotty/epweave.yaml.
type HostConfig struct {
	// Keep is how long a blob stays after its last use, and a pushed
	// image after its last push when KeepPushed is set.
	Keep time.Duration
	// KeepPushed leaves the images the VMs push in the host's Docker, so
	// the next push of one loads and compresses only what changed.
	KeepPushed bool
}

func HostConfigPath(homeDir string) string {
	return filepath.Join(homeDir, "docker.yaml")
}

// LoadHostConfig reads ~/.isotty/docker.yaml, or returns the defaults when
// there is none.
func LoadHostConfig(homeDir string) (HostConfig, error) {
	cfg := HostConfig{Keep: DefaultKeep}
	path := HostConfigPath(homeDir)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return HostConfig{}, err
	}
	var file struct {
		Keep       string `json:"keep"`
		KeepPushed bool   `json:"keep_pushed"`
	}
	if err := yaml.UnmarshalStrict(data, &file); err != nil {
		return HostConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if file.Keep != "" {
		keep, err := time.ParseDuration(file.Keep)
		if err != nil || keep <= 0 {
			return HostConfig{}, fmt.Errorf("%s: keep must be a duration such as 4h or 30m, not %q", path, file.Keep)
		}
		cfg.Keep = keep
	}
	cfg.KeepPushed = file.KeepPushed
	return cfg, nil
}
