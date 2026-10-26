package host

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/github"
	"github.com/yazawa/isotty/internal/isotty/host/pin"
)

// LoadGitHubProjectConfig returns the synced project config once it has
// been checked against the pin this host recorded at `isotty host github
// enable`, or nil when the project has not enabled GitHub.
func LoadGitHubProjectConfig(projectPath, projectHash, homeDir string) (*github.ProjectConfig, error) {
	synced, enabled, err := github.LoadProjectConfig(projectPath)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, nil
	}
	cfg, err := github.VerifyPinned(homeDir, projectHash, synced)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

func runGitHub(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("host github requires a subcommand: enable, disable, or show")
	}

	switch args[0] {
	case "enable":
		return runGitHubEnable(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	case "disable":
		return runGitHubDisable(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	case "show":
		return runGitHubShow(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown host github subcommand %q", args[0])
	}
}

// specs collects a repeatable flag.
type specs []string

func (s *specs) String() string     { return strings.Join(*s, ",") }
func (s *specs) Set(v string) error { *s = append(*s, v); return nil }

// runGitHubEnable writes the whole repository set from scratch: origin,
// resolved on the host right now, plus whatever the flags add. Remote names
// are resolved here and never stored, because `.git/` is synced with the VM.
func runGitHubEnable(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host github enable", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var remotes, repos specs
	fs.Var(&remotes, "remote", "remote name with optional access, name[:read|write]; origin is always included (repeatable)")
	fs.Var(&repos, "repo", "repository with optional access, owner/name[:read|write] (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host github enable does not accept arguments")
	}

	cfg := github.ProjectConfig{Repos: map[string]github.Access{}}
	origin, err := github.ResolveRemote(projectPath, "origin")
	if err != nil {
		return err
	}
	cfg.Repos[origin] = github.AccessWrite
	for _, spec := range remotes {
		name, access := spec, github.AccessWrite
		if i := strings.LastIndex(spec, ":"); i >= 0 {
			name, access = spec[:i], github.Access(spec[i+1:])
		}
		if err := github.ValidateAccess(access); err != nil {
			return fmt.Errorf("--remote %s: %w", spec, err)
		}
		repo, err := github.ResolveRemote(projectPath, name)
		if err != nil {
			return err
		}
		cfg.Repos[repo] = access
	}
	for _, spec := range repos {
		repo, access, err := github.ParseRepoSpec(spec)
		if err != nil {
			return fmt.Errorf("--repo %s: %w", spec, err)
		}
		cfg.Repos[repo] = access
	}

	if err := github.SaveProjectConfig(projectPath, cfg); err != nil {
		return err
	}
	if err := pin.Save(homeDir, projectHash, github.PinKind, cfg); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Enabled GitHub for this project; the VM may reach:")
	printRepos(stdout, cfg)
	fmt.Fprintln(stdout, "Run `isotty up` to install the client in the VM.")
	return nil
}

func runGitHubDisable(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host github disable", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host github disable does not accept arguments")
	}
	if err := github.RemoveProjectConfig(projectPath); err != nil {
		return err
	}
	if err := pin.Remove(homeDir, projectHash, github.PinKind); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Disabled GitHub for this project.")
	return nil
}

func runGitHubShow(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host github show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host github show does not accept arguments")
	}
	cfg, err := LoadGitHubProjectConfig(projectPath, projectHash, homeDir)
	if err != nil {
		return err
	}
	if cfg == nil {
		fmt.Fprintln(stdout, "GitHub is not enabled for this project.")
		return nil
	}
	if _, err := github.LoadHostConfig(homeDir); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "repos:")
	printRepos(stdout, *cfg)
	fmt.Fprintf(stdout, "pinned: %s\n", pin.Path(homeDir, projectHash, github.PinKind))
	fmt.Fprintf(stdout, "token: %s\n", github.HostConfigPath(homeDir))
	return nil
}

func printRepos(w io.Writer, cfg github.ProjectConfig) {
	for _, repo := range cfg.SortedRepos() {
		fmt.Fprintf(w, "  %s: %s\n", repo, cfg.Repos[repo])
	}
}
