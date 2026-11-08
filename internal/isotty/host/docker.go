package host

import (
	"fmt"
	"io"
	"os/exec"

	"github.com/yazawa/isotty/internal/isotty/host/docker"
	"github.com/yazawa/isotty/internal/isotty/host/forge"
)

// checkRegistry is docker.CheckRegistry, replaced where a test pins
// repositories of registries that do not exist.
var checkRegistry = docker.CheckRegistry

var dockerPins = repoPins{
	kind: docker.Kind,
	// A repository cannot be asked for without naming an image in it, so
	// only its registry is checked here; a pull or a push says what the
	// host's Docker could not find or was not let into.
	verify: func(_, _, repo string, access forge.Access) (string, error) {
		if access != docker.AccessRead && access != docker.AccessWrite {
			return "", fmt.Errorf("--allow %s: an image repository is pinned with read, to pull, or with write, to push as well", access)
		}
		if err := checkRegistry(repo); err != nil {
			return "", err
		}
		return repo, nil
	},
	credential: func(_, _ string) (string, error) {
		path, err := exec.LookPath("docker")
		if err != nil {
			return "", fmt.Errorf("the host's Docker pulls and pushes the images, and no docker command was found: %w", err)
		}
		return "the login of " + path, nil
	},
}

func runDocker(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	return dockerPins.run(projectPath, projectHash, homeDir, args, stdout, stderr)
}
