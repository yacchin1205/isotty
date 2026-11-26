package wake

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

// Consent is the human's agreement, on the host, that the project's agents
// may have the VM started by a wake. Starting a VM costs the human, so it
// is theirs to allow project by project, and the VM's own files cannot.
type Consent struct {
	ProjectPath string `json:"project_path"`
	ProjectHash string `json:"-"`
}

const consentSuffix = ".schedule.yaml"

func consentPath(homeDir, projectHash string) string {
	return filepath.Join(homeDir, "projects", projectHash+consentSuffix)
}

func Enable(homeDir, projectHash, projectPath string) error {
	data, err := yaml.Marshal(Consent{ProjectPath: projectPath})
	if err != nil {
		return err
	}
	path := consentPath(homeDir, projectHash)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// Disable withdraws the agreement and forgets the project's wakes.
func Disable(homeDir, projectHash string) error {
	if err := os.Remove(consentPath(homeDir, projectHash)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return Forget(homeDir, projectHash)
}

func Enabled(homeDir, projectHash string) (bool, error) {
	_, err := os.Stat(consentPath(homeDir, projectHash))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// Consents lists the projects that agreed, in path order.
func Consents(homeDir string) ([]Consent, error) {
	matches, err := filepath.Glob(filepath.Join(homeDir, "projects", "*"+consentSuffix))
	if err != nil {
		return nil, err
	}
	var consents []Consent
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var consent Consent
		if err := yaml.UnmarshalStrict(data, &consent); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		consent.ProjectHash = strings.TrimSuffix(filepath.Base(path), consentSuffix)
		consents = append(consents, consent)
	}
	sort.Slice(consents, func(i, j int) bool { return consents[i].ProjectPath < consents[j].ProjectPath })
	return consents, nil
}
