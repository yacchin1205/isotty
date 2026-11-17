package host

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/redmine"
)

func runRedmine(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("host redmine requires a subcommand: login, add, remove, clear, or show")
	}
	switch args[0] {
	case "login":
		return runRedmineLogin(homeDir, args[1:], stdout, stderr)
	case "add":
		return runRedmineAdd(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	case "remove":
		return runRedmineRemove(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	case "clear":
		return runRedmineClear(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	case "show":
		return runRedmineShow(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown host redmine subcommand %q", args[0])
	}
}

func redmineFlags(name string, args []string, stderr io.Writer, define func(*flag.FlagSet)) (*flag.FlagSet, bool, error) {
	fs := flag.NewFlagSet("host redmine "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	define(fs)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return fs, true, nil
}

// runRedmineLogin records the Redmine URL and the API key after the
// Redmine has confirmed whose key it is. The key is read from the
// terminal without echo, or from stdin when that is not a terminal, so it
// never appears in a shell history.
func runRedmineLogin(homeDir string, args []string, stdout, stderr io.Writer) error {
	fs, ok, err := redmineFlags("login", args, stderr, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprint(stderr, `Usage: isotty host redmine login <url>

Stores the Redmine URL and your API key in ~/.isotty/redmine.yaml, which
stays on the host. The key is under "My account" on the Redmine; it is
asked for, not passed as an argument, so it does not enter the shell
history. Logging in again replaces the URL and the key.
`)
		}
	})
	if err != nil || !ok {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("host redmine login takes the Redmine URL as its one argument")
	}
	url := strings.TrimRight(fs.Arg(0), "/")
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return fmt.Errorf("%s is not an http(s) URL", url)
	}
	key, err := readSecret("API key: ", stdout)
	if err != nil {
		return err
	}
	cfg := redmine.HostConfig{URL: url, APIKey: key}
	login, err := redmine.WhoAmI(cfg)
	if err != nil {
		return err
	}
	if err := redmine.SaveHostConfig(homeDir, cfg); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Logged in to %s as %s; the key is kept in %s\n", url, login, redmine.HostConfigPath(homeDir))
	return nil
}

func redmineProjectFlags(name string, args []string, stderr io.Writer) ([]string, bool, error) {
	var projects specs
	fs, ok, err := redmineFlags(name, args, stderr, func(fs *flag.FlagSet) {
		fs.Var(&projects, "project", "identifier of a project to "+name+" (repeatable)")
	})
	if err != nil || !ok {
		return nil, ok, err
	}
	if fs.NArg() != 0 {
		return nil, false, fmt.Errorf("host redmine %s does not accept arguments", name)
	}
	if len(projects) == 0 {
		return nil, false, errors.New("name at least one project with --project <identifier>")
	}
	for _, identifier := range projects {
		if err := redmine.ValidateIdentifier(identifier); err != nil {
			return nil, false, fmt.Errorf("--project: %w", err)
		}
	}
	return projects, true, nil
}

// runRedmineAdd pins the named projects, each read with the host key first.
func runRedmineAdd(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	projects, ok, err := redmineProjectFlags("add", args, stderr)
	if err != nil || !ok {
		return err
	}
	hostCfg, err := redmine.LoadHostConfig(homeDir)
	if err != nil {
		return err
	}
	cfg, err := redmine.LoadPin(homeDir, projectHash)
	if err != nil {
		return err
	}
	for _, identifier := range projects {
		project, err := redmine.Verify(hostCfg, identifier)
		if err != nil {
			return err
		}
		cfg.Put(project.Identifier)
		fmt.Fprintf(stdout, "Pinned %s (%s)\n", project.Identifier, project.Name)
	}
	if err := redmine.Save(projectPath, projectHash, homeDir, cfg); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "The VM may read:")
	printProjects(stdout, cfg)
	fmt.Fprintln(stdout, "Run `isotty up` to install the client in the VM.")
	return nil
}

func runRedmineRemove(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	projects, ok, err := redmineProjectFlags("remove", args, stderr)
	if err != nil || !ok {
		return err
	}
	cfg, err := redmine.LoadPin(homeDir, projectHash)
	if err != nil {
		return err
	}
	for _, identifier := range projects {
		if !cfg.Delete(identifier) {
			return fmt.Errorf("project %s is not pinned for this project", identifier)
		}
		fmt.Fprintf(stdout, "Removed %s\n", identifier)
	}
	if err := redmine.Save(projectPath, projectHash, homeDir, cfg); err != nil {
		return err
	}
	if len(cfg.Projects) == 0 {
		fmt.Fprintln(stdout, "No Redmine project is pinned for this project any more.")
	}
	return nil
}

func runRedmineClear(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs, ok, err := redmineFlags("clear", args, stderr, func(*flag.FlagSet) {})
	if err != nil || !ok {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host redmine clear does not accept arguments")
	}
	if err := redmine.Save(projectPath, projectHash, homeDir, redmine.ProjectConfig{}); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Removed every pinned Redmine project.")
	return nil
}

func runRedmineShow(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs, ok, err := redmineFlags("show", args, stderr, func(*flag.FlagSet) {})
	if err != nil || !ok {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host redmine show does not accept arguments")
	}
	cfg, err := redmine.LoadPinned(projectPath, projectHash, homeDir)
	if err != nil {
		return err
	}
	if cfg == nil {
		fmt.Fprintln(stdout, "No Redmine project is pinned for this project.")
		return nil
	}
	hostCfg, err := redmine.LoadHostConfig(homeDir)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, "projects:")
	printProjects(stdout, *cfg)
	fmt.Fprintf(stdout, "redmine: %s\n", hostCfg.URL)
	fmt.Fprintf(stdout, "pinned: %s\n", redmine.PinPath(homeDir, projectHash))
	fmt.Fprintf(stdout, "credential: %s\n", redmine.HostConfigPath(homeDir))
	fmt.Fprintf(stdout, "salt: %s\n", redmine.SaltPath(homeDir, projectHash))
	return nil
}

func printProjects(w io.Writer, cfg redmine.ProjectConfig) {
	for _, identifier := range cfg.Projects {
		fmt.Fprintf(w, "  %s\n", identifier)
	}
}
