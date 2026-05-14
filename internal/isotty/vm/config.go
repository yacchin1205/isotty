package vmcfg

import (
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

type VMConfig struct {
	Provider string    `json:"provider"`
	GCP      GCPConfig `json:"gcp"`
}

func VMConfigPath(projectPath string) string {
	return filepath.Join(projectPath, ".isotty", "vm.yaml")
}

func Load(projectPath string) (VMConfig, error) {
	configPath := VMConfigPath(projectPath)
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			cfg := VMConfig{
				Provider: "gcp",
				GCP: GCPConfig{
					MachineType:  stringPointer(defaultGCPMachineType),
					BootDiskSize: stringPointer(defaultGCPDiskSize),
					ImageFamily:  stringPointer(defaultGCPImageFamily),
					ImageProject: stringPointer(defaultGCPImageProject),
				},
			}
			return cfg, nil
		}
		return VMConfig{}, fmt.Errorf("read %s: %w", configPath, err)
	}

	var cfg VMConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return VMConfig{}, fmt.Errorf("parse %s: %w", configPath, err)
	}
	if cfg.Provider != "gcp" {
		return VMConfig{}, fmt.Errorf("%s contains unsupported provider %q", configPath, cfg.Provider)
	}

	cfg, err = normalizeGCPVMConfig(cfg)
	if err != nil {
		return VMConfig{}, err
	}
	return cfg, nil
}

func saveVMConfig(projectPath string, cfg VMConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal vm config: %w", err)
	}
	path := VMConfigPath(projectPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create vm config directory: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}
