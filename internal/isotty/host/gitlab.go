package host

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/forge"
	"github.com/yazawa/isotty/internal/isotty/host/gitlab"
	"golang.org/x/term"
)

var gitlabPins = repoPins{
	kind:          gitlab.Kind,
	resolveRemote: gitlab.ResolveRemote,
	verify: func(homeDir, projectHash, repo string, access forge.Access) (string, error) {
		hostCfg, err := gitlab.LoadHostConfig(homeDir, projectHash)
		if err != nil {
			return "", err
		}
		return gitlab.Verify(hostCfg, repo, access)
	},
	credential: func(homeDir, projectHash string) (string, error) {
		_, err := gitlab.LoadHostConfig(homeDir, projectHash)
		return gitlab.HostConfigPath(homeDir, projectHash), err
	},
}

func runGitLab(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("host gitlab requires a subcommand: login, add, remove, clear, or show")
	}
	if args[0] == "login" {
		return runGitLabLogin(homeDir, projectHash, args[1:], stdout, stderr)
	}
	return gitlabPins.run(projectPath, projectHash, homeDir, args, stdout, stderr)
}

// runGitLabLogin records an instance and its token for this project,
// after the instance has confirmed whose token it is. The token is read
// from the terminal without echo, or from stdin when that is not a
// terminal, so it never appears in a shell history.
func runGitLabLogin(homeDir, projectHash string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host gitlab login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	url := fs.String("url", "", "the instance, e.g. https://gitlab.example.org")
	fs.Usage = func() {
		fmt.Fprint(stderr, `Usage: isotty host gitlab login --url <https://instance>

Stores a personal access token for the instance, for this project, under
~/.isotty/projects/, which stays on the host. The token is meant to be a
fine-grained one covering the projects this isotty project will pin, so
each isotty project has its own. The command prints where to create the
token on the instance and which permissions it needs, then asks for it;
the token is not passed as an argument, so it does not enter the shell
history. Logging in again for the same instance replaces its token.

--url <https://instance>
    The instance's web address, the one the browser uses, with its port
    when it is not the default; git's SSH port is not it. The API and git
    over HTTPS are reached under this address.
`)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 || *url == "" {
		return errors.New("host gitlab login requires --url <https://instance> and no arguments")
	}
	fmt.Fprintf(stdout, tokenGuide, strings.TrimRight(*url, "/"), strings.TrimRight(*url, "/"))
	token, err := readSecret("Personal access token: ", stdout)
	if err != nil {
		return err
	}
	instance := gitlab.Instance{URL: *url, Token: token}
	username, err := gitlab.WhoAmI(instance)
	if err != nil {
		return err
	}
	cfg, err := gitlab.LoadOrEmptyHostConfig(homeDir, projectHash)
	if err != nil {
		return err
	}
	if err := cfg.Put(instance); err != nil {
		return err
	}
	if err := gitlab.SaveHostConfig(cfg); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Logged in to %s as %s for this project; the token is kept in %s\n", *url, username, cfg.Path())
	return nil
}

const tokenGuide = `Create a personal access token on the instance:

  1. Open the access tokens page of your user settings:
       %s/-/user_settings/personal_access_tokens
     On GitLab before 16.x the page is:
       %s/-/profile/personal_access_tokens
  2. Choose "Generate token" > "Fine-grained token" (GitLab 18.10 and
     later). Any name will do, e.g. "isotty <project>".
  3. Under "Group and project access", add the GitLab projects this isotty
     project will pin. A group covers the projects in it.
  4. Under "Add resource permissions", select on the "Group and project"
     tab, for those projects:
       Code:              Download, Push   (Push only for projects pinned as write)
       Project:           Read
       Merge Request:     Read, Create, Update
       Work Item:         Read, Create, Update   (issues and notes)
       Pipeline:          Read, Update
       Job:               Read, Run, Update
       Protected Branch:  Read
     and on the "User" tab:
       User:              Read   (this command checks the token with it)
  5. Create the token and copy it. The instance may force an expiration
     date; run this command again when the token expires.

  On an instance without fine-grained tokens, a legacy token with the
  scope "api" works too; it reaches every project the account can.

Paste the token here. It is not shown while typing.
`

func readSecret(prompt string, stdout io.Writer) (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprint(stdout, prompt)
		secret, err := term.ReadPassword(fd)
		fmt.Fprintln(stdout)
		if err != nil {
			return "", fmt.Errorf("read token: %w", err)
		}
		return strings.TrimSpace(string(secret)), nil
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read token from stdin: %w", err)
	}
	secret := strings.TrimSpace(line)
	if secret == "" {
		return "", errors.New("no token was given")
	}
	return secret, nil
}
