package isotty

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"sigs.k8s.io/yaml"
)

type Forward struct {
	LocalPort  int `json:"local_port" yaml:"local_port"`
	RemotePort int `json:"remote_port" yaml:"remote_port"`
}

type ForwardConfig struct {
	Forwards map[string]Forward `json:"forwards" yaml:"forwards"`
}

func LoadForwardConfig(projectPath string) (ForwardConfig, error) {
	path := forwardConfigPath(projectPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ForwardConfig{Forwards: map[string]Forward{}}, nil
		}
		return ForwardConfig{}, fmt.Errorf("read %s: %w", path, err)
	}

	cfg, err := ParseForwardConfig(data)
	if err != nil {
		return ForwardConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

func ParseForwardConfig(data []byte) (ForwardConfig, error) {
	var cfg ForwardConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return ForwardConfig{}, err
	}
	if cfg.Forwards == nil {
		cfg.Forwards = map[string]Forward{}
	}
	return cfg, nil
}

func SaveForwardConfig(projectPath string, cfg ForwardConfig) error {
	if cfg.Forwards == nil {
		cfg.Forwards = map[string]Forward{}
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal forward config: %w", err)
	}

	path := forwardConfigPath(projectPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create forward config directory: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

func AddForward(projectPath, name string, forward Forward) error {
	if name == "" {
		return errors.New("forward name is required")
	}
	if err := validateForward(forward); err != nil {
		return err
	}

	cfg, err := LoadForwardConfig(projectPath)
	if err != nil {
		return err
	}
	cfg.Forwards[name] = forward
	return SaveForwardConfig(projectPath, cfg)
}

func RemoveForward(projectPath, name string) error {
	cfg, err := LoadForwardConfig(projectPath)
	if err != nil {
		return err
	}
	if _, ok := cfg.Forwards[name]; !ok {
		return fmt.Errorf("forward %q does not exist", name)
	}
	delete(cfg.Forwards, name)
	return SaveForwardConfig(projectPath, cfg)
}

func SortedForwardNames(cfg ForwardConfig) []string {
	names := make([]string, 0, len(cfg.Forwards))
	for name := range cfg.Forwards {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func validateForward(forward Forward) error {
	if err := validatePort(forward.LocalPort); err != nil {
		return fmt.Errorf("invalid local port: %w", err)
	}
	if err := validatePort(forward.RemotePort); err != nil {
		return fmt.Errorf("invalid remote port: %w", err)
	}
	return nil
}

func validatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("%d", port)
	}
	return nil
}

func forwardConfigPath(projectPath string) string {
	return filepath.Join(projectPath, ".isotty", "forward.yaml")
}
