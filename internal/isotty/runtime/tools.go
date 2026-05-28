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

// toolBundle is a curated set of packages that prepares a typical working
// environment in the VM. Each bundle contributes apt packages and optional
// follow-up shell commands (for example creating a Python virtualenv) that run
// after the apt packages are installed.
type toolBundle struct {
	Summary     string
	AptPackages []string
	Commands    []string
}

// builtinTools holds the selectable tool bundles. New typical environments are
// added here, then enabled per project with `isotty runtime tools enable`.
var builtinTools = map[string]toolBundle{
	"doc-tools": {
		Summary: "Document handling: PDF/Word/Excel extraction and conversion (poppler, libreoffice, ripgrep, Python libs)",
		AptPackages: []string{
			"poppler-utils",
			"libreoffice",
			"unzip",
			"ripgrep",
			"python3-pip",
		},
		// The VM is disposable, so the Python libraries are installed straight
		// into the system interpreter. This keeps the default `python3` able to
		// import them without any venv path or launcher to discover.
		Commands: []string{
			"sudo pip3 install --break-system-packages -U pypdf pdfplumber pymupdf python-docx",
		},
	},
}

type toolsConfig struct {
	Tools map[string]map[string]any `json:"tools" yaml:"tools"`
}

func ToolsConfigPath(projectPath string) string {
	return filepath.Join(projectPath, ".isotty", "tools.yaml")
}

func ListTools(projectPath string) ([]string, error) {
	configPath := ToolsConfigPath(projectPath)
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", configPath, err)
	}

	var cfg toolsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", configPath, err)
	}
	if len(cfg.Tools) == 0 {
		return nil, fmt.Errorf("%s does not define any tools", configPath)
	}

	tools := make([]string, 0, len(cfg.Tools))
	for name := range cfg.Tools {
		if _, ok := builtinTools[name]; !ok {
			return nil, fmt.Errorf("%s contains unsupported tool %q", configPath, name)
		}
		tools = append(tools, name)
	}
	sort.Strings(tools)
	return tools, nil
}

func AddTools(projectPath string, tools []string) error {
	if len(tools) == 0 {
		return errors.New("at least one tool is required")
	}

	current, err := ListTools(projectPath)
	if err != nil {
		return err
	}
	for _, tool := range tools {
		if err := validateTool(tool); err != nil {
			return err
		}
		if slices.Contains(current, tool) {
			continue
		}
		current = append(current, tool)
	}
	sort.Strings(current)
	return saveTools(projectPath, current)
}

func RemoveTools(projectPath string, tools []string) error {
	if len(tools) == 0 {
		return errors.New("at least one tool is required")
	}

	current, err := ListTools(projectPath)
	if err != nil {
		return err
	}
	removeSet := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		if err := validateTool(tool); err != nil {
			return err
		}
		removeSet[tool] = struct{}{}
	}

	next := make([]string, 0, len(current))
	for _, tool := range current {
		if _, ok := removeSet[tool]; ok {
			continue
		}
		next = append(next, tool)
	}
	sort.Strings(next)
	return saveTools(projectPath, next)
}

func validateTool(tool string) error {
	if _, ok := builtinTools[tool]; !ok {
		return fmt.Errorf("unsupported tool %q", tool)
	}
	return nil
}

// ToolAptPackages returns the apt packages contributed by the enabled tool
// bundles, in bundle declaration order.
func ToolAptPackages(cfg RuntimeConfig) []string {
	var packages []string
	for _, tool := range cfg.Tools {
		bundle, ok := builtinTools[tool]
		if !ok {
			continue
		}
		packages = append(packages, bundle.AptPackages...)
	}
	return packages
}

// ToolBootstrapCommand returns the follow-up commands (run after apt install)
// for the enabled tool bundles. It returns an empty string when no enabled
// bundle defines follow-up commands.
func ToolBootstrapCommand(cfg RuntimeConfig) (string, error) {
	var commands []string
	for _, tool := range cfg.Tools {
		bundle, ok := builtinTools[tool]
		if !ok {
			return "", fmt.Errorf("unsupported tool %q", tool)
		}
		commands = append(commands, bundle.Commands...)
	}
	return strings.Join(commands, " && "), nil
}

func RunTools(projectPath string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("runtime tools requires a subcommand: enable, disable, list, or available")
	}

	switch args[0] {
	case "enable":
		return runToolsEnable(projectPath, args[1:], stdout, stderr)
	case "disable":
		return runToolsDisable(projectPath, args[1:], stdout, stderr)
	case "list":
		return runToolsList(projectPath, args[1:], stdout, stderr)
	case "available":
		return runToolsAvailable(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown runtime tools subcommand %q", args[0])
	}
}

func runToolsEnable(projectPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("runtime tools enable", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("runtime tools enable requires at least one tool")
	}
	if err := AddTools(projectPath, fs.Args()); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Enabled %d tool(s)\n", len(fs.Args()))
	return nil
}

func runToolsDisable(projectPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("runtime tools disable", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("runtime tools disable requires at least one tool")
	}
	if err := RemoveTools(projectPath, fs.Args()); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Disabled %d tool(s)\n", len(fs.Args()))
	return nil
}

func runToolsList(projectPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("runtime tools list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("runtime tools list does not accept arguments")
	}
	tools, err := ListTools(projectPath)
	if err != nil {
		return err
	}
	if len(tools) == 0 {
		fmt.Fprintln(stdout, "No tools configured.")
		return nil
	}
	for _, tool := range tools {
		fmt.Fprintln(stdout, tool)
	}
	return nil
}

func runToolsAvailable(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("runtime tools available", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("runtime tools available does not accept arguments")
	}
	names := make([]string, 0, len(builtinTools))
	for name := range builtinTools {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(stdout, "%s\t%s\n", name, builtinTools[name].Summary)
	}
	return nil
}

func saveTools(projectPath string, tools []string) error {
	path := ToolsConfigPath(projectPath)
	if len(tools) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		return nil
	}

	cfg := toolsConfig{
		Tools: make(map[string]map[string]any, len(tools)),
	}
	for _, tool := range tools {
		cfg.Tools[tool] = map[string]any{}
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create tools config directory: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}
