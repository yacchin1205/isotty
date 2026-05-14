package runtimecfg

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func AptPackagesPath(projectPath string) string {
	return filepath.Join(projectPath, ".isotty", "apt.txt")
}

func ListAptPackages(projectPath string) ([]string, error) {
	configPath := AptPackagesPath(projectPath)
	file, err := os.Open(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open %s: %w", configPath, err)
	}
	defer file.Close()

	var packages []string
	seen := map[string]struct{}{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		packages = append(packages, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", configPath, err)
	}
	return packages, nil
}

func AddAptPackages(projectPath string, packages []string) error {
	if len(packages) == 0 {
		return errors.New("at least one package is required")
	}

	current, err := ListAptPackages(projectPath)
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
	return saveTextList(AptPackagesPath(projectPath), "apt config", current)
}

func RemoveAptPackages(projectPath string, packages []string) error {
	if len(packages) == 0 {
		return errors.New("at least one package is required")
	}

	current, err := ListAptPackages(projectPath)
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
	return saveTextList(AptPackagesPath(projectPath), "apt config", next)
}

func AptBootstrapCommands(cfg RuntimeConfig) []string {
	if len(cfg.AptPackages) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("sudo apt-get install -y %s", shellJoin(cfg.AptPackages))}
}

func RunApt(projectPath string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("runtime apt requires a subcommand: add, remove, or list")
	}

	switch args[0] {
	case "add":
		return runAptAdd(projectPath, args[1:], stdout, stderr)
	case "remove":
		return runAptRemove(projectPath, args[1:], stdout, stderr)
	case "list":
		return runAptList(projectPath, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown runtime apt subcommand %q", args[0])
	}
}

func runAptAdd(projectPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("runtime apt add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("runtime apt add requires at least one package")
	}
	if err := AddAptPackages(projectPath, fs.Args()); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Added %d apt package(s)\n", len(fs.Args()))
	return nil
}

func runAptRemove(projectPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("runtime apt remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("runtime apt remove requires at least one package")
	}
	if err := RemoveAptPackages(projectPath, fs.Args()); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Removed %d apt package(s)\n", len(fs.Args()))
	return nil
}

func runAptList(projectPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("runtime apt list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("runtime apt list does not accept arguments")
	}
	packages, err := ListAptPackages(projectPath)
	if err != nil {
		return err
	}
	if len(packages) == 0 {
		fmt.Fprintln(stdout, "No apt packages configured.")
		return nil
	}
	for _, pkg := range packages {
		fmt.Fprintln(stdout, pkg)
	}
	return nil
}

func saveTextList(path, label string, values []string) error {
	var buffer bytes.Buffer
	for _, value := range values {
		buffer.WriteString(value)
		buffer.WriteByte('\n')
	}
	if len(values) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s directory: %w", label, err)
	}
	return os.WriteFile(path, buffer.Bytes(), 0o644)
}
