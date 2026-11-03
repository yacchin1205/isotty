package dns

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"sigs.k8s.io/yaml"
)

// Tracked is a record the VM created, kept on the host so the human can
// see what is left to clean up.
type Tracked struct {
	Provider    string `json:"provider"`
	Project     string `json:"project"`
	ManagedZone string `json:"managed_zone"`
	Name        string `json:"name"`
	Type        string `json:"type"`
}

type trackedFile struct {
	Records []Tracked `json:"records"`
}

func TrackedPath(homeDir, projectHash string) string {
	return filepath.Join(homeDir, "projects", projectHash+".dns-records.yaml")
}

func LoadTracked(homeDir, projectHash string) ([]Tracked, error) {
	path := TrackedPath(homeDir, projectHash)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var file trackedFile
	if err := yaml.UnmarshalStrict(data, &file); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return file.Records, nil
}

func saveTracked(homeDir, projectHash string, records []Tracked) error {
	path := TrackedPath(homeDir, projectHash)
	if len(records) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		return nil
	}
	data, err := yaml.Marshal(trackedFile{Records: records})
	if err != nil {
		return fmt.Errorf("marshal tracked records: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	return os.WriteFile(path, data, 0o644)
}

// Track notes a record the VM created; one the VM only changed is already
// the human's to remember.
func Track(homeDir, projectHash string, record Tracked) error {
	records, err := LoadTracked(homeDir, projectHash)
	if err != nil {
		return err
	}
	if slices.Contains(records, record) {
		return nil
	}
	return saveTracked(homeDir, projectHash, append(records, record))
}

func Untrack(homeDir, projectHash string, record Tracked) error {
	records, err := LoadTracked(homeDir, projectHash)
	if err != nil {
		return err
	}
	kept := slices.DeleteFunc(records, func(t Tracked) bool { return t == record })
	return saveTracked(homeDir, projectHash, kept)
}

// Remaining is the tracked records that still exist, read back from the
// zones so a record the human removed by hand is not reported.
func Remaining(client *Client, tracked []Tracked) ([]Record, error) {
	var remaining []Record
	for _, t := range tracked {
		records, err := client.List(t.Project, t.ManagedZone, t.Name)
		if err != nil {
			return nil, fmt.Errorf("%s/%s: %w", t.Project, t.ManagedZone, err)
		}
		for _, record := range records {
			if record.Name == t.Name && record.Type == t.Type {
				remaining = append(remaining, record)
			}
		}
	}
	return remaining, nil
}
