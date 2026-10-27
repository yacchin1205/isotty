package host

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/forge"
)

// specs collects a repeatable flag.
type specs []string

func (s *specs) String() string     { return strings.Join(*s, ",") }
func (s *specs) Set(v string) error { *s = append(*s, v); return nil }

// repoPins is the `isotty host <forge> add|remove|clear|show` command for
// one forge: the list handling is shared, the forge supplies how a remote
// resolves to a repository, how a repository is checked before pinning and
// where its credential lives.
type repoPins struct {
	kind forge.Kind
	// resolveRemote turns a remote name of the project into a repository key.
	resolveRemote func(projectPath, remote string) (string, error)
	// verify checks the repository with the host credential and returns its
	// name as the forge spells it.
	verify func(homeDir, projectHash, repo string, access forge.Access) (string, error)
	// credential returns the path of the host credential the relay will
	// use, after checking it can be loaded.
	credential func(homeDir, projectHash string) (string, error)
}

func (p repoPins) run(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("host %s requires a subcommand: add, remove, clear, or show", p.kind.Name)
	}
	switch args[0] {
	case "add":
		return p.add(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	case "remove":
		return p.remove(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	case "clear":
		return p.clear(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	case "show":
		return p.show(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown host %s subcommand %q", p.kind.Name, args[0])
	}
}

// repoFlags reads the repositories named by --remote and --repo, in that
// order. Remote names are resolved on the host at this moment and never
// stored, because `.git/` is synced with the VM.
func (p repoPins) repoFlags(projectPath string, remotes, repos specs) ([]string, error) {
	var found []string
	for _, name := range remotes {
		repo, err := p.resolveRemote(projectPath, name)
		if err != nil {
			return nil, err
		}
		found = append(found, repo)
	}
	for _, repo := range repos {
		if err := p.kind.ValidateRepo(repo); err != nil {
			return nil, fmt.Errorf("--repo: %w", err)
		}
		found = append(found, repo)
	}
	if len(found) == 0 {
		return nil, errors.New("name at least one repository with --remote <name> or --repo <repository>")
	}
	return found, nil
}

func (p repoPins) parse(name string, args []string, stderr io.Writer, define func(*flag.FlagSet)) (bool, error) {
	fs := flag.NewFlagSet("host "+p.kind.Name+" "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	define(fs)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return false, nil
		}
		return false, err
	}
	if fs.NArg() != 0 {
		return false, fmt.Errorf("host %s %s does not accept arguments", p.kind.Name, name)
	}
	return true, nil
}

// add pins the named repositories at the level of --allow, each checked
// with the host credential first; a repository already pinned gets the
// level given here.
func (p repoPins) add(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	var remotes, repos specs
	var allow string
	ok, err := p.parse("add", args, stderr, func(fs *flag.FlagSet) {
		fs.Var(&remotes, "remote", "remote of the project whose repository to pin (repeatable)")
		fs.Var(&repos, "repo", "repository to pin (repeatable)")
		fs.StringVar(&allow, "allow", string(forge.AccessRead), "what the VM may do there: read, comment, write or admin")
	})
	if err != nil || !ok {
		return err
	}
	access := forge.Access(allow)
	if err := forge.ValidateAccess(access); err != nil {
		return fmt.Errorf("--allow: %w", err)
	}
	found, err := p.repoFlags(projectPath, remotes, repos)
	if err != nil {
		return err
	}
	cfg, err := p.kind.LoadPin(homeDir, projectHash)
	if err != nil {
		return err
	}
	for _, repo := range found {
		name, err := p.verify(homeDir, projectHash, repo, access)
		if err != nil {
			return err
		}
		cfg.Put(name, access)
		fmt.Fprintf(stdout, "Pinned %s: %s\n", name, access)
	}
	if err := p.kind.Save(projectPath, projectHash, homeDir, cfg); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "The VM may reach:")
	printRepos(stdout, cfg)
	fmt.Fprintln(stdout, "Run `isotty up` to install the client in the VM.")
	return nil
}

func (p repoPins) remove(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	var remotes, repos specs
	ok, err := p.parse("remove", args, stderr, func(fs *flag.FlagSet) {
		fs.Var(&remotes, "remote", "remote of the project whose repository to remove (repeatable)")
		fs.Var(&repos, "repo", "repository to remove (repeatable)")
	})
	if err != nil || !ok {
		return err
	}
	found, err := p.repoFlags(projectPath, remotes, repos)
	if err != nil {
		return err
	}
	cfg, err := p.kind.LoadPin(homeDir, projectHash)
	if err != nil {
		return err
	}
	for _, repo := range found {
		if !cfg.Delete(repo) {
			return fmt.Errorf("repository %s is not pinned for this project", repo)
		}
		fmt.Fprintf(stdout, "Removed %s\n", repo)
	}
	if err := p.kind.Save(projectPath, projectHash, homeDir, cfg); err != nil {
		return err
	}
	if len(cfg.Repos) == 0 {
		fmt.Fprintln(stdout, "No repository is pinned for this project any more.")
	}
	return nil
}

func (p repoPins) clear(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	ok, err := p.parse("clear", args, stderr, func(*flag.FlagSet) {})
	if err != nil || !ok {
		return err
	}
	if err := p.kind.Save(projectPath, projectHash, homeDir, forge.ProjectConfig{}); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Removed every pinned repository.")
	return nil
}

func (p repoPins) show(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	ok, err := p.parse("show", args, stderr, func(*flag.FlagSet) {})
	if err != nil || !ok {
		return err
	}
	cfg, err := p.kind.LoadPinned(projectPath, projectHash, homeDir)
	if err != nil {
		return err
	}
	if cfg == nil {
		fmt.Fprintln(stdout, "No repository is pinned for this project.")
		return nil
	}
	credential, err := p.credential(homeDir, projectHash)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, "repos:")
	printRepos(stdout, *cfg)
	fmt.Fprintf(stdout, "pinned: %s\n", p.kind.PinPath(homeDir, projectHash))
	fmt.Fprintf(stdout, "credential: %s\n", credential)
	return nil
}

func printRepos(w io.Writer, cfg forge.ProjectConfig) {
	for _, repo := range cfg.SortedRepos() {
		fmt.Fprintf(w, "  %s: %s\n", repo, cfg.Repos[repo])
	}
}
