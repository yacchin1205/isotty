package epweave

import (
	"fmt"

	"github.com/yazawa/isotty/internal/isotty/host/pin"
)

// PinKind names the host pin of the project config; see package pin.
const PinKind = "epweave"

// VerifyPinned returns the synced config only when it matches the host pin.
func VerifyPinned(homeDir, projectHash string, synced ProjectConfig) (ProjectConfig, error) {
	var pinned ProjectConfig
	ok, err := pin.Load(homeDir, projectHash, PinKind, &pinned)
	if err != nil {
		return ProjectConfig{}, err
	}
	if !ok {
		return ProjectConfig{}, fmt.Errorf("ep_weave is configured in .isotty/epweave.yaml but not pinned on this host; run `isotty host epweave enable --prefix %s` here to accept it", synced.Prefix)
	}
	if pinned.Prefix != synced.Prefix {
		return ProjectConfig{}, fmt.Errorf(
			".isotty/epweave.yaml (prefix=%s) differs from the prefix pinned on this host (%s); the file is synced from the VM, so something in the VM may have edited it. Review it, then run `isotty host epweave enable` again to accept the new value",
			synced.Prefix, pinned.Prefix,
		)
	}
	return synced, nil
}
