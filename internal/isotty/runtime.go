package isotty

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

func ListRuntimeAptPackages(projectPath string) ([]string, error) {
	return loadAptPackages(projectPath)
}

func AddRuntimeAptPackages(projectPath string, packages []string) error {
	if len(packages) == 0 {
		return errors.New("at least one package is required")
	}

	current, err := loadAptPackages(projectPath)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(current))
	for _, pkg := range current {
		seen[pkg] = struct{}{}
	}
	for _, pkg := range packages {
		if pkg == "" {
			return errors.New("package name is required")
		}
		if _, ok := seen[pkg]; ok {
			continue
		}
		seen[pkg] = struct{}{}
		current = append(current, pkg)
	}
	return saveAptPackages(projectPath, current)
}

func RemoveRuntimeAptPackages(projectPath string, packages []string) error {
	if len(packages) == 0 {
		return errors.New("at least one package is required")
	}

	current, err := loadAptPackages(projectPath)
	if err != nil {
		return err
	}
	removeSet := make(map[string]struct{}, len(packages))
	for _, pkg := range packages {
		if pkg == "" {
			return errors.New("package name is required")
		}
		removeSet[pkg] = struct{}{}
	}

	next := make([]string, 0, len(current))
	for _, pkg := range current {
		if _, ok := removeSet[pkg]; ok {
			continue
		}
		next = append(next, pkg)
	}
	return saveAptPackages(projectPath, next)
}

func RuntimeNodeVersion(projectPath string) (string, error) {
	return loadNodeVersion(projectPath)
}

func SetRuntimeNodeVersion(projectPath, version string) error {
	version = strings.TrimSpace(version)
	if version == "" {
		return errors.New("node version is required")
	}
	if !nodeMajorVersionPattern.MatchString(version) {
		return fmt.Errorf("node version must be a major version, got %q", version)
	}

	path := nodeVersionPath(projectPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create node config directory: %w", err)
	}
	return os.WriteFile(path, []byte(version+"\n"), 0o644)
}

func ListRuntimeAgents(projectPath string) ([]string, error) {
	return loadAgents(projectPath)
}

func AddRuntimeAgents(projectPath string, agents []string) error {
	if len(agents) == 0 {
		return errors.New("at least one agent is required")
	}

	current, err := loadAgents(projectPath)
	if err != nil {
		return err
	}
	for _, agent := range agents {
		if err := validateRuntimeAgent(agent); err != nil {
			return err
		}
		if slices.Contains(current, agent) {
			continue
		}
		current = append(current, agent)
	}
	sort.Strings(current)
	return saveAgents(projectPath, current)
}

func RemoveRuntimeAgents(projectPath string, agents []string) error {
	if len(agents) == 0 {
		return errors.New("at least one agent is required")
	}

	current, err := loadAgents(projectPath)
	if err != nil {
		return err
	}
	removeSet := make(map[string]struct{}, len(agents))
	for _, agent := range agents {
		if err := validateRuntimeAgent(agent); err != nil {
			return err
		}
		removeSet[agent] = struct{}{}
	}

	next := make([]string, 0, len(current))
	for _, agent := range current {
		if _, ok := removeSet[agent]; ok {
			continue
		}
		next = append(next, agent)
	}
	sort.Strings(next)
	return saveAgents(projectPath, next)
}

func ListRuntimeServices(projectPath string) ([]string, error) {
	return loadServices(projectPath)
}

func AddRuntimeServices(projectPath string, services []string) error {
	if len(services) == 0 {
		return errors.New("at least one service is required")
	}

	current, err := loadServices(projectPath)
	if err != nil {
		return err
	}
	for _, service := range services {
		if err := validateRuntimeService(service); err != nil {
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

func RemoveRuntimeServices(projectPath string, services []string) error {
	if len(services) == 0 {
		return errors.New("at least one service is required")
	}

	current, err := loadServices(projectPath)
	if err != nil {
		return err
	}
	removeSet := make(map[string]struct{}, len(services))
	for _, service := range services {
		if err := validateRuntimeService(service); err != nil {
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

func RuntimeGCPVMConfig(projectPath string) (gcpVMConfig, error) {
	return loadVMConfig(projectPath)
}

func SetRuntimeGCPVMConfig(projectPath string, updates gcpVMConfig) error {
	current, err := loadVMConfig(projectPath)
	if err != nil {
		return err
	}
	if updates.MachineType != nil {
		current.MachineType = updates.MachineType
	}
	if updates.BootDiskSize != nil {
		current.BootDiskSize = updates.BootDiskSize
	}
	if updates.ImageFamily != nil {
		current.ImageFamily = updates.ImageFamily
	}
	if updates.ImageProject != nil {
		current.ImageProject = updates.ImageProject
	}
	return saveVMConfig(projectPath, current)
}

func saveAptPackages(projectPath string, packages []string) error {
	var buffer bytes.Buffer
	for _, pkg := range packages {
		buffer.WriteString(pkg)
		buffer.WriteByte('\n')
	}

	path := aptPackagesPath(projectPath)
	if len(packages) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create apt config directory: %w", err)
	}
	return os.WriteFile(path, buffer.Bytes(), 0o644)
}

func saveAgents(projectPath string, agents []string) error {
	path := agentConfigPath(projectPath)
	if len(agents) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		return nil
	}

	var buffer bytes.Buffer
	buffer.WriteString("agents:\n")
	for _, agent := range agents {
		buffer.WriteString("  ")
		buffer.WriteString(agent)
		buffer.WriteString(": {}\n")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create agent config directory: %w", err)
	}
	return os.WriteFile(path, buffer.Bytes(), 0o644)
}

func saveServices(projectPath string, services []string) error {
	path := serviceConfigPath(projectPath)
	if len(services) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		return nil
	}

	var buffer bytes.Buffer
	buffer.WriteString("services:\n")
	for _, service := range services {
		buffer.WriteString("  ")
		buffer.WriteString(service)
		buffer.WriteString(": {}\n")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create service config directory: %w", err)
	}
	return os.WriteFile(path, buffer.Bytes(), 0o644)
}

func saveVMConfig(projectPath string, cfg gcpVMConfig) error {
	data, err := yaml.Marshal(vmConfig{
		Provider: stringPointer("gcp"),
		GCP:      cfg,
	})
	if err != nil {
		return fmt.Errorf("marshal vm config: %w", err)
	}
	path := vmConfigPath(projectPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create vm config directory: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

func validateRuntimeAgent(agent string) error {
	switch agent {
	case "codex", "claude":
		return nil
	default:
		return fmt.Errorf("unsupported agent %q", agent)
	}
}

func validateRuntimeService(service string) error {
	switch service {
	case "docker":
		return nil
	default:
		return fmt.Errorf("unsupported service %q", service)
	}
}
