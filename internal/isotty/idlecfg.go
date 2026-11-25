package isotty

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"sigs.k8s.io/yaml"
)

// defaultIdleStop is how long a VM stays up without agent work by default:
// long enough for a human to read what an agent did and answer it.
const defaultIdleStop = 30 * time.Minute

// IdleConfig is the host's own choice of when a VM without agent work
// powers off, ~/.isotty/idle.yaml. It is never synced: what the VM may cost
// is the human's to decide, not the agent's.
type IdleConfig struct {
	// StopAfter unset is the default; 0 never stops.
	StopAfter *idleDuration `json:"stop_after,omitempty"`
}

func IdleConfigPath(homeDir string) string {
	return filepath.Join(homeDir, "idle.yaml")
}

// LoadIdleConfig returns the defaults when the file does not exist.
func LoadIdleConfig(homeDir string) (IdleConfig, error) {
	path := IdleConfigPath(homeDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return IdleConfig{}, nil
		}
		return IdleConfig{}, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg IdleConfig
	if err := yaml.UnmarshalStrict(data, &cfg); err != nil {
		return IdleConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

func (c IdleConfig) Stop() time.Duration {
	if c.StopAfter == nil {
		return defaultIdleStop
	}
	return time.Duration(*c.StopAfter)
}

// idleDuration is a duration as Go writes one, "30m" or "2h"; a bare 0
// turns the stop off.
type idleDuration time.Duration

func (d *idleDuration) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("0")) {
		*d = 0
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return fmt.Errorf("stop_after is a duration such as 30m, or 0 for never: %s", data)
	}
	parsed, err := time.ParseDuration(text)
	if err != nil {
		return fmt.Errorf("stop_after: %w", err)
	}
	if parsed < 0 {
		return fmt.Errorf("stop_after is negative: %s", text)
	}
	*d = idleDuration(parsed)
	return nil
}
