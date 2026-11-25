package isotty

import (
	"fmt"
	"sort"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host"
	"github.com/yazawa/isotty/internal/isotty/host/aws"
	"github.com/yazawa/isotty/internal/isotty/host/bitbucket"
	"github.com/yazawa/isotty/internal/isotty/host/child"
	"github.com/yazawa/isotty/internal/isotty/host/dns"
	"github.com/yazawa/isotty/internal/isotty/host/docker"
	"github.com/yazawa/isotty/internal/isotty/host/epweave"
	"github.com/yazawa/isotty/internal/isotty/host/gas"
	"github.com/yazawa/isotty/internal/isotty/host/github"
	"github.com/yazawa/isotty/internal/isotty/host/gitlab"
	"github.com/yazawa/isotty/internal/isotty/host/google"
	"github.com/yazawa/isotty/internal/isotty/host/redmine"
	"github.com/yazawa/isotty/internal/isotty/host/sshhost"
	"github.com/yazawa/isotty/internal/isotty/opeco"
	runtimecfg "github.com/yazawa/isotty/internal/isotty/runtime"
)

// hostConfig is what the daemon serves a project with: its forwards and the
// relays of its gateway, as the host agreed to them.
type hostConfig struct {
	forwards  host.ForwardConfig
	epweave   *epweave.ProjectConfig
	github    *github.ProjectConfig
	gitlab    *gitlab.ProjectConfig
	bitbucket *bitbucket.ProjectConfig
	redmine   *redmine.ProjectConfig
	docker    *docker.ProjectConfig
	google    *google.ProjectConfig
	aws       *aws.ProjectConfig
	gas       *gas.ProjectConfig
	dns       *dns.ProjectConfig
	child     *child.ProjectConfig
	ssh       *sshhost.ProjectConfig
	// refused names what is not served, and why: a pin the synced file no
	// longer matches, or a file that cannot be read.
	refused map[string]error
}

// loadHostConfig reads the project's forwards and relays. One that cannot
// be read, its pin no longer matching among the reasons, is left out and
// named in refused, so the rest are still served: the attach that used to
// read them refused everything for one of them, but a daemon has no human
// in front of it to fix it there and then.
func loadHostConfig(projectPath string, state State, homeDir string) hostConfig {
	cfg := hostConfig{refused: map[string]error{}}
	load := func(name string, fn func() error) {
		if err := fn(); err != nil {
			cfg.refused[name] = err
		}
	}
	load("forwards", func() (err error) {
		cfg.forwards, err = host.LoadForwardConfig(projectPath)
		return err
	})
	if cfg.forwards.Forwards == nil {
		cfg.forwards = host.ForwardConfig{Forwards: map[string]host.Forward{}}
	}
	load("ep_weave", func() (err error) {
		cfg.epweave, err = host.LoadEpweaveProjectConfig(projectPath, state.ProjectHash, homeDir)
		return err
	})
	load("GitHub", func() (err error) {
		cfg.github, err = github.Kind.LoadPinned(projectPath, state.ProjectHash, homeDir)
		return err
	})
	load("GitLab", func() (err error) {
		cfg.gitlab, err = gitlab.Kind.LoadPinned(projectPath, state.ProjectHash, homeDir)
		return err
	})
	load("Bitbucket", func() (err error) {
		cfg.bitbucket, err = bitbucket.Kind.LoadPinned(projectPath, state.ProjectHash, homeDir)
		return err
	})
	load("Redmine", func() (err error) {
		cfg.redmine, err = redmine.LoadPinned(projectPath, state.ProjectHash, homeDir)
		return err
	})
	load("Docker", func() (err error) {
		cfg.docker, err = docker.Kind.LoadPinned(projectPath, state.ProjectHash, homeDir)
		return err
	})
	load("Google", func() (err error) {
		cfg.google, err = host.LoadGoogleProjectConfig(projectPath, state.ProjectHash, homeDir)
		return err
	})
	load("AWS", func() (err error) {
		cfg.aws, err = host.LoadAWSProjectConfig(projectPath, state.ProjectHash, homeDir)
		return err
	})
	load("Apps Script", func() (err error) {
		cfg.gas, err = host.LoadGASProjectConfig(projectPath, state.ProjectHash, homeDir)
		return err
	})
	load("DNS", func() (err error) {
		cfg.dns, err = host.LoadDNSProjectConfig(projectPath, state.ProjectHash, homeDir)
		return err
	})
	load("child VMs", func() (err error) {
		cfg.child, err = host.LoadChildProjectConfig(projectPath, state.ProjectHash, homeDir)
		return err
	})
	load("SSH hosts", func() (err error) {
		cfg.ssh, err = host.LoadSSHProjectConfig(projectPath, state.ProjectHash, homeDir)
		return err
	})
	load("opeco", func() error {
		agents, err := runtimecfg.ListAgents(projectPath)
		if err != nil {
			return err
		}
		settings, err := opeco.LoadSettings(homeDir, projectPath)
		if err != nil {
			return err
		}
		if opeco.Active(agents, settings) {
			return addOpecoForward(cfg.forwards, state.OpecoQRPort)
		}
		return nil
	})
	return cfg
}

// describeRefused says what is not served, one relay a line.
func (c hostConfig) describeRefused() string {
	names := make([]string, 0, len(c.refused))
	for name := range c.refused {
		names = append(names, name)
	}
	sort.Strings(names)
	var lines []string
	for _, name := range names {
		lines = append(lines, fmt.Sprintf("%s not served: %v", name, c.refused[name]))
	}
	return strings.Join(lines, "\n")
}
