package isotty

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

// gcpContext is the GCP project and zone a project's VM was last brought up
// in. It outlives `down`, so that the project comes back where it was
// without the human naming them again.
type gcpContext struct {
	Project string `json:"project"`
	Zone    string `json:"zone"`
}

func gcpContextPath(homeDir, projectHash string) string {
	return filepath.Join(homeDir, "projects", projectHash+".gcp.yaml")
}

// loadGCPContext returns what the project was last brought up in: the state
// while the VM is up, and the remembered context once it was brought down.
func loadGCPContext(homeDir, projectHash string) (gcpContext, bool, error) {
	state, err := LoadState(projectHash)
	if err == nil {
		return gcpContext{Project: state.GCPProjectID, Zone: state.Zone}, true, nil
	}
	if !os.IsNotExist(err) {
		return gcpContext{}, false, err
	}
	path := gcpContextPath(homeDir, projectHash)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return gcpContext{}, false, nil
		}
		return gcpContext{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	var ctx gcpContext
	if err := yaml.UnmarshalStrict(data, &ctx); err != nil {
		return gcpContext{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	if ctx.Project == "" || ctx.Zone == "" {
		return gcpContext{}, false, fmt.Errorf("%s must name both project and zone", path)
	}
	return ctx, true, nil
}

func saveGCPContext(homeDir, projectHash string, ctx gcpContext) error {
	data, err := yaml.Marshal(ctx)
	if err != nil {
		return err
	}
	path := gcpContextPath(homeDir, projectHash)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// resolveGCPContext takes each of the project and the zone from its
// environment variable, else from where the project was last brought up,
// else from the active gcloud configuration.
func resolveGCPContext(homeDir, projectHash string) (gcpContext, error) {
	last, hasLast, err := loadGCPContext(homeDir, projectHash)
	if err != nil {
		return gcpContext{}, err
	}
	project := strings.TrimSpace(os.Getenv("ISOTTY_GCP_PROJECT"))
	zone := strings.TrimSpace(os.Getenv("ISOTTY_GCP_ZONE"))
	if project == "" && hasLast {
		project = last.Project
	}
	if zone == "" && hasLast {
		zone = last.Zone
	}
	if project == "" {
		if project, err = gcloudSetting("ISOTTY_GCP_PROJECT", "project"); err != nil {
			return gcpContext{}, fmt.Errorf("resolve GCP project: %w", err)
		}
	}
	if zone == "" {
		if zone, err = gcloudSetting("ISOTTY_GCP_ZONE", "compute/zone"); err != nil {
			return gcpContext{}, fmt.Errorf("resolve GCP zone: %w", err)
		}
	}
	return gcpContext{Project: project, Zone: zone}, nil
}
