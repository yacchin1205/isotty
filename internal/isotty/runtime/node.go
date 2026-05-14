package runtimecfg

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const defaultNodeMajorVersion = "24"

func NodeVersionPath(projectPath string) string {
	return filepath.Join(projectPath, ".isotty", "node.txt")
}

func NodeVersion(projectPath string) (string, error) {
	configPath := NodeVersionPath(projectPath)
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read %s: %w", configPath, err)
	}

	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("%s is empty", configPath)
	}
	if !nodeMajorVersionPattern.MatchString(value) {
		return "", fmt.Errorf("%s must contain only a Node.js major version, got %q", configPath, value)
	}
	return value, nil
}

func SetNodeVersion(projectPath, version string) error {
	version = strings.TrimSpace(version)
	if version == "" {
		return errors.New("node version is required")
	}
	if !nodeMajorVersionPattern.MatchString(version) {
		return fmt.Errorf("node version must be a major version, got %q", version)
	}

	path := NodeVersionPath(projectPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create node config directory: %w", err)
	}
	return os.WriteFile(path, []byte(version+"\n"), 0o644)
}

func NeedsNodeRuntime(cfg RuntimeConfig) bool {
	return cfg.NodeVersion != "" || len(cfg.Agents) > 0
}

func ResolvedNodeVersion(cfg RuntimeConfig) string {
	if cfg.NodeVersion != "" {
		return cfg.NodeVersion
	}
	return defaultNodeMajorVersion
}

func NodeBootstrapCommand(cfg RuntimeConfig) string {
	return fmt.Sprintf(`NODE_MAJOR=%s
if [ ! -f /etc/apt/sources.list.d/nodesource.list ] || ! grep -q "node_${NODE_MAJOR}\.x" /etc/apt/sources.list.d/nodesource.list; then
  curl -fsSL "https://deb.nodesource.com/setup_${NODE_MAJOR}.x" -o /tmp/nodesource_setup.sh
  sudo -E bash /tmp/nodesource_setup.sh
fi
sudo apt-get install -y nodejs`, shellJoin([]string{ResolvedNodeVersion(cfg)}))
}

func RunNode(projectPath string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("runtime node requires a subcommand: set or show")
	}

	switch args[0] {
	case "set":
		return runNodeSet(projectPath, args[1:], stdout, stderr)
	case "show":
		return runNodeShow(projectPath, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown runtime node subcommand %q", args[0])
	}
}

func runNodeSet(projectPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("runtime node set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("runtime node set requires exactly one major version")
	}
	if err := SetNodeVersion(projectPath, fs.Arg(0)); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Set Node.js major version to %s\n", fs.Arg(0))
	return nil
}

func runNodeShow(projectPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("runtime node show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("runtime node show does not accept arguments")
	}
	version, err := NodeVersion(projectPath)
	if err != nil {
		return err
	}
	if version == "" {
		fmt.Fprintln(stdout, "No Node.js version configured.")
		return nil
	}
	fmt.Fprintln(stdout, version)
	return nil
}
