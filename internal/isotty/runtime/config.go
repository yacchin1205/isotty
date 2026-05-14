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
	return RuntimeConfig{
		AptPackages: aptPackages,
		NodeVersion: nodeVersion,
		Agents:      agents,
		Services:    services,
	}, nil
}

func BootstrapCommand(cfg RuntimeConfig, workspacePath string) (string, error) {
	parts := WorkspaceBootstrapPrefix(workspacePath)
	if len(cfg.AptPackages) > 0 || NeedsNodeRuntime(cfg) {
		parts = append(parts, "sudo apt-get update")
	}
	parts = append(parts, AptBootstrapCommands(cfg)...)
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
	if len(cfg.Services) > 0 {
		command, err := ServiceBootstrapCommand(cfg)
		if err != nil {
			return "", err
		}
		parts = append(parts, command)
	}
	return strings.Join(parts, " && "), nil
}
