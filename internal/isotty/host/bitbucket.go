package host

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/yazawa/isotty/internal/isotty/host/bitbucket"
	"github.com/yazawa/isotty/internal/isotty/host/forge"
)

var bitbucketPins = repoPins{
	kind:          bitbucket.Kind,
	resolveRemote: bitbucket.ResolveRemote,
	verify: func(homeDir, _, repo string, access forge.Access) (string, error) {
		hostCfg, err := bitbucket.LoadHostConfig(homeDir)
		if err != nil {
			return "", err
		}
		return bitbucket.Verify(hostCfg.Token, repo, access)
	},
	credential: func(homeDir, _ string) (string, error) {
		_, err := bitbucket.LoadHostConfig(homeDir)
		return bitbucket.HostConfigPath(homeDir), err
	},
}

func runBitbucket(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("host bitbucket requires a subcommand: login, add, remove, clear, or show")
	}
	if args[0] == "login" {
		return runBitbucketLogin(homeDir, args[1:], stdout, stderr)
	}
	return bitbucketPins.run(projectPath, projectHash, homeDir, args, stdout, stderr)
}

// runBitbucketLogin records the API token after Bitbucket has confirmed
// whose it is. The token is read from the terminal without echo, or from
// stdin when that is not a terminal, so it never appears in a shell
// history. It acts as the account across its workspaces, so there is one
// for the host, not one per project.
func runBitbucketLogin(homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host bitbucket login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, `Usage: isotty host bitbucket login

Stores an Atlassian API token for Bitbucket Cloud in ~/.isotty/bitbucket.yaml,
which stays on the host. The command prints where to create the token and
which scopes it needs, then asks for it; the token is not passed as an
argument, so it does not enter the shell history. Logging in again
replaces the token, which is what to do when it expires.
`)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host bitbucket login takes no arguments")
	}
	fmt.Fprint(stdout, bitbucketTokenGuide)
	token, err := readSecret("API token: ", stdout)
	if err != nil {
		return err
	}
	account, err := bitbucket.WhoAmI(token)
	if err != nil {
		return err
	}
	if err := bitbucket.SaveHostConfig(homeDir, bitbucket.HostConfig{Token: token}); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Logged in to Bitbucket as %s; the token is kept in %s\n", account, bitbucket.HostConfigPath(homeDir))
	return nil
}

const bitbucketTokenGuide = `Create an API token with scopes on your Atlassian account:

  1. Open https://id.atlassian.com/manage-profile/security/api-tokens
     (Atlassian account > Security > API tokens).
  2. Choose "Create API token with scopes". Any name will do, e.g.
     "isotty"; set the expiry date, which Atlassian requires.
  3. Select the app "Bitbucket" and these scopes:
       read:repository:bitbucket, read:pullrequest:bitbucket,
       read:pipeline:bitbucket                 (every pin)
       write:pullrequest:bitbucket             (pins of comment and above)
       write:repository:bitbucket, write:pipeline:bitbucket
                                               (pins of write and above)
       admin:repository:bitbucket              (pins of admin)
       read:user:bitbucket                     (this command asks whose
                                                token it is)
  4. Create the token and copy it. Run this command again when it expires.

  The token acts as you in every workspace you are in; the relay narrows
  it to the repositories pinned for each project.

Paste the token here. It is not shown while typing.
`
