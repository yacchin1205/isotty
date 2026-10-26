package gas

import (
	"fmt"
	"slices"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/pin"
)

// PinKind names the host pin of the project config; see package pin.
const PinKind = "gas"

// VerifyPinned returns the synced config only when it matches the host pin.
func VerifyPinned(homeDir, projectHash string, synced ProjectConfig) (ProjectConfig, error) {
	var pinned ProjectConfig
	ok, err := pin.Load(homeDir, projectHash, PinKind, &pinned)
	if err != nil {
		return ProjectConfig{}, err
	}
	if !ok {
		return ProjectConfig{}, fmt.Errorf("Apps Script is configured in .isotty/gas.yaml but not pinned on this host; run `isotty host gas add` here for each script to accept it")
	}
	if !pinned.equal(synced) {
		return ProjectConfig{}, fmt.Errorf(
			".isotty/gas.yaml (%s) differs from what is pinned on this host (%s); the file is synced from the VM, so something in the VM may have edited it. Review it, then run `isotty host gas add` or `remove` to accept the change",
			synced.summary(), pinned.summary(),
		)
	}
	return synced, nil
}

func (c ProjectConfig) equal(other ProjectConfig) bool {
	return slices.EqualFunc(c.sorted(), other.sorted(), func(a, b Script) bool {
		return a.ID == b.ID && a.Client == b.Client && a.Account == b.Account && slices.Equal(a.Scopes, b.Scopes)
	})
}

func (c ProjectConfig) summary() string {
	var parts []string
	for _, script := range c.sorted() {
		parts = append(parts, fmt.Sprintf("%s as %s via %s [%s]", script.ID, script.Account, script.Client, strings.Join(script.Scopes, ",")))
	}
	return strings.Join(parts, "; ")
}
