package runtimecfg

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

type serviceConfig struct {
	Services map[string]map[string]any `json:"services" yaml:"services"`
}

func ServiceConfigPath(projectPath string) string {
	return filepath.Join(projectPath, ".isotty", "service.yaml")
}

func ListServices(projectPath string) ([]string, error) {
	configPath := ServiceConfigPath(projectPath)
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", configPath, err)
	}

	var cfg serviceConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", configPath, err)
	}
	if len(cfg.Services) == 0 {
		return nil, fmt.Errorf("%s does not define any services", configPath)
	}

	services := make([]string, 0, len(cfg.Services))
	for name := range cfg.Services {
		switch name {
		case "docker":
			services = append(services, name)
		default:
			return nil, fmt.Errorf("%s contains unsupported service %q", configPath, name)
		}
	}
	sort.Strings(services)
	return services, nil
}

func AddServices(projectPath string, services []string) error {
	if len(services) == 0 {
		return errors.New("at least one service is required")
	}

	current, err := ListServices(projectPath)
	if err != nil {
		return err
	}
	for _, service := range services {
		if err := validateService(service); err != nil {
			return err
		}
		if slices.Contains(current, service) {
			continue
		}
		current = append(current, service)
	}
	sort.Strings(current)
	return saveServices(projectPath, current)
}

func RemoveServices(projectPath string, services []string) error {
	if len(services) == 0 {
		return errors.New("at least one service is required")
	}

	current, err := ListServices(projectPath)
	if err != nil {
		return err
	}
	removeSet := make(map[string]struct{}, len(services))
	for _, service := range services {
		if err := validateService(service); err != nil {
			return err
		}
		removeSet[service] = struct{}{}
	}

	next := make([]string, 0, len(current))
	for _, service := range current {
		if _, ok := removeSet[service]; ok {
			continue
		}
		next = append(next, service)
	}
	sort.Strings(next)
	return saveServices(projectPath, next)
}

func validateService(service string) error {
	switch service {
	case "docker":
		return nil
	default:
		return fmt.Errorf("unsupported service %q", service)
	}
}

func ServiceBootstrapCommand(cfg RuntimeConfig) (string, error) {
	commands := make([]string, 0, len(cfg.Services))
	for _, service := range cfg.Services {
		switch service {
		case "docker":
			commands = append(commands,
				`curl -fsSL https://get.docker.com -o /tmp/get-docker.sh`,
				`sudo sh /tmp/get-docker.sh`,
				`sudo systemctl enable --now docker`,
				`sudo usermod -aG docker "$USER"`,
			)
		default:
			return "", fmt.Errorf("unsupported service %q", service)
		}
	}
	return strings.Join(commands, " && "), nil
}

func RunService(projectPath string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("runtime service requires a subcommand: enable, disable, or list")
	}

	switch args[0] {
	case "enable":
		return runServiceEnable(projectPath, args[1:], stdout, stderr)
	case "disable":
		return runServiceDisable(projectPath, args[1:], stdout, stderr)
	case "list":
		return runServiceList(projectPath, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown runtime service subcommand %q", args[0])
	}
}

func runServiceEnable(projectPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("runtime service enable", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("runtime service enable requires at least one service")
	}
	if err := AddServices(projectPath, fs.Args()); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Enabled %d service(s)\n", len(fs.Args()))
	return nil
}

func runServiceDisable(projectPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("runtime service disable", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("runtime service disable requires at least one service")
	}
	if err := RemoveServices(projectPath, fs.Args()); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Disabled %d service(s)\n", len(fs.Args()))
	return nil
}

func runServiceList(projectPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("runtime service list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("runtime service list does not accept arguments")
	}
	services, err := ListServices(projectPath)
	if err != nil {
		return err
	}
	if len(services) == 0 {
		fmt.Fprintln(stdout, "No services configured.")
		return nil
	}
	for _, service := range services {
		fmt.Fprintln(stdout, service)
	}
	return nil
}

func saveServices(projectPath string, services []string) error {
	path := ServiceConfigPath(projectPath)
	if len(services) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		return nil
	}

	cfg := serviceConfig{
		Services: make(map[string]map[string]any, len(services)),
	}
	for _, service := range services {
		cfg.Services[service] = map[string]any{}
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create service config directory: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}
