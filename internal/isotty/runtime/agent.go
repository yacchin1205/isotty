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

	"sigs.k8s.io/yaml"
)

var agentPackages = map[string]string{
	"claude": "@anthropic-ai/claude-code",
	"codex":  "@openai/codex",
}

type agentConfig struct {
	Agents map[string]map[string]any `json:"agents" yaml:"agents"`
}

func AgentConfigPath(projectPath string) string {
	return filepath.Join(projectPath, ".isotty", "agent.yaml")
}

func ListAgents(projectPath string) ([]string, error) {
	configPath := AgentConfigPath(projectPath)
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", configPath, err)
	}

	var cfg agentConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", configPath, err)
	}
	if len(cfg.Agents) == 0 {
		return nil, fmt.Errorf("%s does not define any agents", configPath)
	}

	agents := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		switch name {
		case "codex", "claude":
			agents = append(agents, name)
		default:
			return nil, fmt.Errorf("%s contains unsupported agent %q", configPath, name)
		}
	}
	sort.Strings(agents)
	return agents, nil
}

func AddAgents(projectPath string, agents []string) error {
	if len(agents) == 0 {
		return errors.New("at least one agent is required")
	}

	current, err := ListAgents(projectPath)
	if err != nil {
		return err
	}
	for _, agent := range agents {
		if err := validateAgent(agent); err != nil {
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

func RemoveAgents(projectPath string, agents []string) error {
	if len(agents) == 0 {
		return errors.New("at least one agent is required")
	}

	current, err := ListAgents(projectPath)
	if err != nil {
		return err
	}
	removeSet := make(map[string]struct{}, len(agents))
	for _, agent := range agents {
		if err := validateAgent(agent); err != nil {
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

func validateAgent(agent string) error {
	switch agent {
	case "codex", "claude":
		return nil
	default:
		return fmt.Errorf("unsupported agent %q", agent)
	}
}

func AgentBootstrapCommand(cfg RuntimeConfig) (string, error) {
	if len(cfg.Agents) == 0 {
		return "", nil
	}

	packages := make([]string, 0, len(cfg.Agents))
	for _, agent := range cfg.Agents {
		pkg, ok := agentPackages[agent]
		if !ok {
			return "", fmt.Errorf("unsupported agent %q", agent)
		}
		packages = append(packages, pkg)
	}

	return fmt.Sprintf("sudo env PATH=/usr/local/bin:$PATH npm install -g %s", shellJoin(packages)), nil
}

func RunAgent(projectPath string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("runtime agent requires a subcommand: add, remove, or list")
	}

	switch args[0] {
	case "add":
		return runAgentAdd(projectPath, args[1:], stdout, stderr)
	case "remove":
		return runAgentRemove(projectPath, args[1:], stdout, stderr)
	case "list":
		return runAgentList(projectPath, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown runtime agent subcommand %q", args[0])
	}
}

func runAgentAdd(projectPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("runtime agent add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("runtime agent add requires at least one agent")
	}
	if err := AddAgents(projectPath, fs.Args()); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Added %d agent(s)\n", len(fs.Args()))
	return nil
}

func runAgentRemove(projectPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("runtime agent remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("runtime agent remove requires at least one agent")
	}
	if err := RemoveAgents(projectPath, fs.Args()); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Removed %d agent(s)\n", len(fs.Args()))
	return nil
}

func runAgentList(projectPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("runtime agent list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("runtime agent list does not accept arguments")
	}
	agents, err := ListAgents(projectPath)
	if err != nil {
		return err
	}
	if len(agents) == 0 {
		fmt.Fprintln(stdout, "No agents configured.")
		return nil
	}
	for _, agent := range agents {
		fmt.Fprintln(stdout, agent)
	}
	return nil
}

func saveAgents(projectPath string, agents []string) error {
	path := AgentConfigPath(projectPath)
	if len(agents) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		return nil
	}

	cfg := agentConfig{
		Agents: make(map[string]map[string]any, len(agents)),
	}
	for _, agent := range agents {
		cfg.Agents[agent] = map[string]any{}
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create agent config directory: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}
