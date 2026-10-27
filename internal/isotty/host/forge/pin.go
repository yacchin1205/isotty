package forge

import (
	"fmt"
	"maps"

	"github.com/yazawa/isotty/internal/isotty/host/pin"
)

// VerifyPinned returns the synced config only when it matches the host pin.
func (k Kind) VerifyPinned(homeDir, projectHash string, synced ProjectConfig) (ProjectConfig, error) {
	var pinned ProjectConfig
	ok, err := pin.Load(homeDir, projectHash, k.Name, &pinned)
	if err != nil {
		return ProjectConfig{}, err
	}
	if !ok {
		return ProjectConfig{}, fmt.Errorf("%s is configured in .isotty/%s.yaml but not pinned on this host; run `isotty host %s add` here for each repository to accept it", k.Title, k.Name, k.Name)
	}
	if !maps.Equal(pinned.Repos, synced.Repos) {
		return ProjectConfig{}, fmt.Errorf(
			".isotty/%s.yaml (%s) differs from the repositories pinned on this host (%s); the file is synced from the VM, so something in the VM may have edited it. Review it, then run `isotty host %s add` or `remove` to accept the change",
			k.Name, synced.summary(), pinned.summary(), k.Name,
		)
	}
	return synced, nil
}

// LoadPinned returns the synced project config once it has been checked
// against the pin, or nil when the project has no pinned repository.
func (k Kind) LoadPinned(projectPath, projectHash, homeDir string) (*ProjectConfig, error) {
	synced, enabled, err := k.LoadProjectConfig(projectPath)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, nil
	}
	cfg, err := k.VerifyPinned(homeDir, projectHash, synced)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

// LoadPin returns what this host has accepted so far, which add and remove
// build on; the synced file may have been edited in the VM.
func (k Kind) LoadPin(homeDir, projectHash string) (ProjectConfig, error) {
	var pinned ProjectConfig
	if _, err := pin.Load(homeDir, projectHash, k.Name, &pinned); err != nil {
		return ProjectConfig{}, err
	}
	return pinned, nil
}

func (k Kind) PinPath(homeDir, projectHash string) string {
	return pin.Path(homeDir, projectHash, k.Name)
}

// Save writes the synced file and the pin together; an empty list is no
// config at all.
func (k Kind) Save(projectPath, projectHash, homeDir string, cfg ProjectConfig) error {
	if len(cfg.Repos) == 0 {
		if err := k.RemoveProjectConfig(projectPath); err != nil {
			return err
		}
		return pin.Remove(homeDir, projectHash, k.Name)
	}
	if err := k.SaveProjectConfig(projectPath, cfg); err != nil {
		return err
	}
	return pin.Save(homeDir, projectHash, k.Name, cfg)
}
