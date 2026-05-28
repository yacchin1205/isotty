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

func PostInstallScriptPath(projectPath string) string {
	return filepath.Join(projectPath, ".isotty", "post-install.sh")
}

func HasPostInstallScript(projectPath string) (bool, error) {
	path := PostInstallScriptPath(projectPath)
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("stat %s: %w", path, err)
}

func PostInstallCommand(workspacePath string) string {
	return fmt.Sprintf("cd %s && sudo bash ./.isotty/post-install.sh", workspacePath)
}

func BootstrapLabel(cfg RuntimeConfig) string {
	parts := []string{"Bootstrapping workspace"}
	if len(AptInstallPackages(cfg)) > 0 {
		parts = append(parts, "installing packages")
	}
	if NeedsNodeRuntime(cfg) {
		parts = append(parts, "installing Node.js")
	}
	if len(cfg.Agents) > 0 {
		parts = append(parts, "installing agents")
	}
	if len(cfg.Tools) > 0 {
		parts = append(parts, "installing tools")
	}
	return strings.Join(parts, " and ")
}

func RunPostInstall(projectPath string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("runtime post-install requires a subcommand: path")
	}

	switch args[0] {
	case "path":
		return runPostInstallPath(projectPath, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown runtime post-install subcommand %q", args[0])
	}
}

func runPostInstallPath(projectPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("runtime post-install path", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("runtime post-install path does not accept arguments")
	}
	fmt.Fprintln(stdout, PostInstallScriptPath(projectPath))
	return nil
}
