package google

import (
	"fmt"
	"slices"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/pin"
)

// PinKind names the host pin of the project config; see package pin.
const PinKind = "google"

// VerifyPinned returns the synced config only when it matches the host pin.
func VerifyPinned(homeDir, projectHash string, synced ProjectConfig) (ProjectConfig, error) {
	var pinned ProjectConfig
	ok, err := pin.Load(homeDir, projectHash, PinKind, &pinned)
	if err != nil {
		return ProjectConfig{}, err
	}
	if !ok {
		return ProjectConfig{}, fmt.Errorf("Google is configured in .isotty/google.yaml but not pinned on this host; run `isotty host google enable` here to accept it")
	}
	if pinned.ServiceAccount != synced.ServiceAccount || !slices.Equal(pinned.sortedScopes(), synced.sortedScopes()) {
		return ProjectConfig{}, fmt.Errorf(
			".isotty/google.yaml (%s) differs from what is pinned on this host (%s); the file is synced from the VM, so something in the VM may have edited it. Review it, then run `isotty host google enable` again to accept the new value",
			synced.summary(), pinned.summary(),
		)
	}
	return synced, nil
}

func (c ProjectConfig) summary() string {
	return c.ServiceAccount + " " + strings.Join(c.sortedScopes(), ",")
}
