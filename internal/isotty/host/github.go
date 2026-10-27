package host

import (
	"io"

	"github.com/yazawa/isotty/internal/isotty/host/forge"
	"github.com/yazawa/isotty/internal/isotty/host/github"
)

var githubPins = repoPins{
	kind:          github.Kind,
	resolveRemote: github.ResolveRemote,
	verify: func(homeDir, _, repo string, access forge.Access) (string, error) {
		hostCfg, err := github.LoadHostConfig(homeDir)
		if err != nil {
			return "", err
		}
		return github.Verify(hostCfg.Token, repo, access)
	},
	credential: func(homeDir, _ string) (string, error) {
		_, err := github.LoadHostConfig(homeDir)
		return github.HostConfigPath(homeDir), err
	},
}

func runGitHub(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	return githubPins.run(projectPath, projectHash, homeDir, args, stdout, stderr)
}
