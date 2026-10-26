// Package pin keeps, on the host, a copy of a project setting that is also
// synced with the VM. Anything running in the VM can rewrite the synced
// file, so `up` and `attach` trust the file only when it matches the pin the
// human recorded with the corresponding `isotty host ... enable`.
package pin

import (
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

func Path(homeDir, projectHash, kind string) string {
	return filepath.Join(homeDir, "projects", projectHash+"."+kind+".yaml")
}

func Save(homeDir, projectHash, kind string, cfg any) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal %s pin: %w", kind, err)
	}
	path := Path(homeDir, projectHash, kind)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s pin directory: %w", kind, err)
	}
	return os.WriteFile(path, data, 0o644)
}

// Load fills cfg from the pin and reports whether one exists.
func Load(homeDir, projectHash, kind string, cfg any) (bool, error) {
	path := Path(homeDir, projectHash, kind)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.UnmarshalStrict(data, cfg); err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}
	return true, nil
}

func Remove(homeDir, projectHash, kind string) error {
	path := Path(homeDir, projectHash, kind)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}
