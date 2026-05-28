package runtimecfg

import (
	"regexp"
	"strings"
)

type RuntimeConfig struct {
	AptPackages []string
	NodeVersion string
	Agents      []string
	Services    []string
	Tools       []string
}

var nodeMajorVersionPattern = regexp.MustCompile(`^[0-9]+$`)

func Load(projectPath string) (RuntimeConfig, error) {
	aptPackages, err := ListAptPackages(projectPath)
	if err != nil {
		return RuntimeConfig{}, err
	}
	nodeVersion, err := NodeVersion(projectPath)
	if err != nil {
		return RuntimeConfig{}, err
	}
	agents, err := ListAgents(projectPath)
	if err != nil {
		return RuntimeConfig{}, err
	}
	services, err := ListServices(projectPath)
	if err != nil {
		return RuntimeConfig{}, err
	}
	tools, err := ListTools(projectPath)
	if err != nil {
		return RuntimeConfig{}, err
	}
	return RuntimeConfig{
		AptPackages: aptPackages,
		NodeVersion: nodeVersion,
		Agents:      agents,
		Services:    services,
		Tools:       tools,
	}, nil
}

// AptInstallPackages returns every apt package to install in one pass: the
// packages from apt.txt followed by those contributed by enabled tool bundles,
// de-duplicated while preserving order.
func AptInstallPackages(cfg RuntimeConfig) []string {
	seen := make(map[string]struct{})
	var packages []string
	for _, pkg := range cfg.AptPackages {
		if _, ok := seen[pkg]; ok {
			continue
		}
		seen[pkg] = struct{}{}
		packages = append(packages, pkg)
	}
	for _, pkg := range ToolAptPackages(cfg) {
		if _, ok := seen[pkg]; ok {
			continue
		}
		seen[pkg] = struct{}{}
		packages = append(packages, pkg)
	}
	return packages
}

func BootstrapCommand(cfg RuntimeConfig, workspacePath string) (string, error) {
	parts := WorkspaceBootstrapPrefix(workspacePath)
	aptPackages := AptInstallPackages(cfg)
	if len(aptPackages) > 0 || NeedsNodeRuntime(cfg) {
		parts = append(parts, "sudo apt-get update")
	}
	parts = append(parts, AptBootstrapCommands(aptPackages)...)
	if NeedsNodeRuntime(cfg) {
		parts = append(parts, NodeBootstrapCommand(cfg))
	}
	if len(cfg.Agents) > 0 {
		command, err := AgentBootstrapCommand(cfg)
		if err != nil {
			return "", err
		}
		parts = append(parts, command)
	}
	if len(cfg.Tools) > 0 {
		command, err := ToolBootstrapCommand(cfg)
		if err != nil {
			return "", err
		}
		if command != "" {
			parts = append(parts, command)
		}
	}
	if len(cfg.Services) > 0 {
		command, err := ServiceBootstrapCommand(cfg)
		if err != nil {
			return "", err
		}
		parts = append(parts, command)
	}
	return strings.Join(parts, " && "), nil
}
