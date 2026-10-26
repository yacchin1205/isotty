package github

import (
	"fmt"
	"maps"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/pin"
)

// PinKind names the host pin of the project config; see package pin.
const PinKind = "github"

// VerifyPinned returns the synced config only when it matches the host pin.
func VerifyPinned(homeDir, projectHash string, synced ProjectConfig) (ProjectConfig, error) {
	var pinned ProjectConfig
	ok, err := pin.Load(homeDir, projectHash, PinKind, &pinned)
	if err != nil {
		return ProjectConfig{}, err
	}
	if !ok {
		return ProjectConfig{}, fmt.Errorf("GitHub is configured in .isotty/github.yaml but not pinned on this host; run `isotty host github add` here for each repository to accept it")
	}
	if !maps.Equal(pinned.Repos, synced.Repos) {
		return ProjectConfig{}, fmt.Errorf(
			".isotty/github.yaml (%s) differs from the repositories pinned on this host (%s); the file is synced from the VM, so something in the VM may have edited it. Review it, then run `isotty host github add` or `remove` to accept the change",
			synced.summary(), pinned.summary(),
		)
	}
	return synced, nil
}

func (c ProjectConfig) summary() string {
	parts := make([]string, 0, len(c.Repos))
	for _, repo := range c.SortedRepos() {
		parts = append(parts, repo+":"+string(c.Repos[repo]))
	}
	return strings.Join(parts, ", ")
}
