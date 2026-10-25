package epweave

import (
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

// The project config is synced with the VM, so anything running there can
// rewrite its own writable prefix. `isotty host epweave enable` therefore pins the
// accepted config on the host, and attach/up refuse a synced file that
// disagrees with the pin.

func PinPath(homeDir, projectHash string) string {
	return filepath.Join(homeDir, "projects", projectHash+".epweave.yaml")
}

func SavePin(homeDir, projectHash string, cfg ProjectConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal epweave pin: %w", err)
	}
	path := PinPath(homeDir, projectHash)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create epweave pin directory: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

func LoadPin(homeDir, projectHash string) (ProjectConfig, bool, error) {
	path := PinPath(homeDir, projectHash)
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

func RemovePin(homeDir, projectHash string) error {
	path := PinPath(homeDir, projectHash)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

// VerifyPinned returns the synced config only when it matches the host pin.
func VerifyPinned(homeDir, projectHash string, synced ProjectConfig) (ProjectConfig, error) {
	pinned, ok, err := LoadPin(homeDir, projectHash)
	if err != nil {
		return ProjectConfig{}, err
	}
	if !ok {
		return ProjectConfig{}, fmt.Errorf("ep_weave is configured in .isotty/epweave.yaml but not pinned on this host; run `isotty host epweave enable --prefix %s` here to accept it", synced.Prefix)
	}
	if pinned.Prefix != synced.Prefix {
		return ProjectConfig{}, fmt.Errorf(
			".isotty/epweave.yaml (prefix=%s) differs from the prefix pinned on this host (%s); the file is synced from the VM, so something in the VM may have edited it. Review it, then run `isotty host epweave enable` again to accept the new value",
			synced.Prefix, pinned.Prefix,
		)
	}
	return synced, nil
}
