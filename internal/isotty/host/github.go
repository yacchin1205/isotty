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
// been checked against the pin this host recorded by `isotty host github
// add`, or nil when the project has no pinned repository.
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
		return errors.New("host github requires a subcommand: add, remove, clear, or show")
	}

	switch args[0] {
	case "add":
		return runGitHubAdd(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	case "remove":
		return runGitHubRemove(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	case "clear":
		return runGitHubClear(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
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

// loadGitHubPin returns what this host has accepted so far. add and remove
// build on the pin, not on the synced file, which the VM may have edited.
func loadGitHubPin(homeDir, projectHash string) (github.ProjectConfig, error) {
	var pinned github.ProjectConfig
	if _, err := pin.Load(homeDir, projectHash, github.PinKind, &pinned); err != nil {
		return github.ProjectConfig{}, err
	}
	return pinned, nil
}

// saveGitHub writes the synced file and the pin together; an empty list is
// no config at all.
func saveGitHub(projectPath, projectHash, homeDir string, cfg github.ProjectConfig) error {
	if len(cfg.Repos) == 0 {
		if err := github.RemoveProjectConfig(projectPath); err != nil {
			return err
		}
		return pin.Remove(homeDir, projectHash, github.PinKind)
	}
	if err := github.SaveProjectConfig(projectPath, cfg); err != nil {
		return err
	}
	return pin.Save(homeDir, projectHash, github.PinKind, cfg)
}

// repoFlags reads the repositories named by --remote and --repo, in that
// order. Remote names are resolved on the host at this moment and never
// stored, because `.git/` is synced with the VM.
func repoFlags(projectPath string, remotes, repos specs) ([]string, error) {
	var found []string
	for _, name := range remotes {
		repo, err := github.ResolveRemote(projectPath, name)
		if err != nil {
			return nil, err
		}
		found = append(found, repo)
	}
	for _, repo := range repos {
		if err := github.ValidateRepo(repo); err != nil {
			return nil, fmt.Errorf("--repo: %w", err)
		}
		found = append(found, repo)
	}
	if len(found) == 0 {
		return nil, errors.New("name at least one repository with --remote <name> or --repo <owner/name>")
	}
	return found, nil
}

// runGitHubAdd pins the named repositories at the level of --allow, each
// checked with the host token first; a repository already pinned gets the
// level given here.
func runGitHubAdd(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host github add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var remotes, repos specs
	fs.Var(&remotes, "remote", "remote of the project whose repository to pin (repeatable)")
	fs.Var(&repos, "repo", "repository to pin, owner/name (repeatable)")
	allow := fs.String("allow", string(github.AccessRead), "what the VM may do there: read, comment or write")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host github add does not accept arguments")
	}
	access := github.Access(*allow)
	if err := github.ValidateAccess(access); err != nil {
		return fmt.Errorf("--allow: %w", err)
	}
	found, err := repoFlags(projectPath, remotes, repos)
	if err != nil {
		return err
	}
	hostCfg, err := github.LoadHostConfig(homeDir)
	if err != nil {
		return err
	}
	cfg, err := loadGitHubPin(homeDir, projectHash)
	if err != nil {
		return err
	}
	for _, repo := range found {
		name, err := github.Verify(hostCfg.Token, repo, access)
		if err != nil {
			return err
		}
		cfg.Put(name, access)
		fmt.Fprintf(stdout, "Pinned %s: %s\n", name, access)
	}
	if err := saveGitHub(projectPath, projectHash, homeDir, cfg); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "The VM may reach:")
	printRepos(stdout, cfg)
	fmt.Fprintln(stdout, "Run `isotty up` to install the client in the VM.")
	return nil
}

func runGitHubRemove(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host github remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var remotes, repos specs
	fs.Var(&remotes, "remote", "remote of the project whose repository to remove (repeatable)")
	fs.Var(&repos, "repo", "repository to remove, owner/name (repeatable)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host github remove does not accept arguments")
	}
	found, err := repoFlags(projectPath, remotes, repos)
	if err != nil {
		return err
	}
	cfg, err := loadGitHubPin(homeDir, projectHash)
	if err != nil {
		return err
	}
	for _, repo := range found {
		if !cfg.Delete(repo) {
			return fmt.Errorf("repository %s is not pinned for this project", repo)
		}
		fmt.Fprintf(stdout, "Removed %s\n", repo)
	}
	if err := saveGitHub(projectPath, projectHash, homeDir, cfg); err != nil {
		return err
	}
	if len(cfg.Repos) == 0 {
		fmt.Fprintln(stdout, "No repository is pinned for this project any more.")
	}
	return nil
}

func runGitHubClear(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host github clear", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host github clear does not accept arguments")
	}
	if err := saveGitHub(projectPath, projectHash, homeDir, github.ProjectConfig{}); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Removed every pinned repository.")
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
		fmt.Fprintln(stdout, "No repository is pinned for this project.")
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
