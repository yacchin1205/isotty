// Package host configures what the host side provides to the VM while
// `isotty attach` runs: port forwards into the VM and the services of the
// host gateway, such as ep_weave.
package host

import (
	"errors"
	"fmt"
	"io"

	"github.com/yazawa/isotty/internal/isotty/host/epweave"
)

func Run(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("host requires a subcommand: forward, epweave, or github")
	}

	switch args[0] {
	case "forward":
		return runForward(projectPath, args[1:], stdout, stderr)
	case "epweave":
		return runEpweave(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	case "github":
		return runGitHub(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown host subcommand %q", args[0])
	}
}

// LoadEpweaveProjectConfig returns the synced project config once it has
// been checked against the pin this host recorded at `isotty host epweave
// enable`, or nil when the project has not enabled ep_weave.
func LoadEpweaveProjectConfig(projectPath, projectHash, homeDir string) (*epweave.ProjectConfig, error) {
	synced, enabled, err := epweave.LoadProjectConfig(projectPath)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, nil
	}
	cfg, err := epweave.VerifyPinned(homeDir, projectHash, synced)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}
