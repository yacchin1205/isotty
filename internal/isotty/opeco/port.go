package opeco

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"sigs.k8s.io/yaml"
)

// The host browser must reach opeco's QR page at the address opeco serves it
// on in the VM, and opeco learns that port when the agent starts. The port is
// therefore fixed per project for the life of its VM, and chosen on the host so
// that no two projects attached from one host share it.
const qrPortBase = 61002

type record struct {
	QRPort int `json:"qr_port" yaml:"qr_port"`
}

func RecordPath(homeDir, projectHash string) string {
	return filepath.Join(homeDir, "projects", projectHash+".opeco.yaml")
}

// AllocateQRPort returns the project's QR port, choosing the lowest one
// no other project on this host holds when the project has none yet.
func AllocateQRPort(homeDir, projectHash string) (int, error) {
	if port, ok, err := loadQRPort(homeDir, projectHash); err != nil || ok {
		return port, err
	}
	paths, err := filepath.Glob(filepath.Join(homeDir, "projects", "*.opeco.yaml"))
	if err != nil {
		return 0, fmt.Errorf("list opeco records: %w", err)
	}
	used := make([]int, 0, len(paths))
	for _, path := range paths {
		rec, err := readRecord(path)
		if err != nil {
			return 0, err
		}
		used = append(used, rec.QRPort)
	}
	sort.Ints(used)
	port := qrPortBase
	for _, taken := range used {
		if taken == port {
			port++
		}
	}
	data, err := yaml.Marshal(record{QRPort: port})
	if err != nil {
		return 0, fmt.Errorf("marshal opeco record: %w", err)
	}
	path := RecordPath(homeDir, projectHash)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, fmt.Errorf("create opeco record directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return 0, fmt.Errorf("write %s: %w", path, err)
	}
	return port, nil
}

// LoadQRPort returns the port `up` allocated for the project.
func LoadQRPort(homeDir, projectHash string) (int, error) {
	port, ok, err := loadQRPort(homeDir, projectHash)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("no opeco QR port is recorded for this project in %s; run `isotty up`", RecordPath(homeDir, projectHash))
	}
	return port, nil
}

func ReleaseQRPort(homeDir, projectHash string) error {
	path := RecordPath(homeDir, projectHash)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

func loadQRPort(homeDir, projectHash string) (int, bool, error) {
	path := RecordPath(homeDir, projectHash)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("stat %s: %w", path, err)
	}
	rec, err := readRecord(path)
	if err != nil {
		return 0, false, err
	}
	return rec.QRPort, true, nil
}

func readRecord(path string) (record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return record{}, fmt.Errorf("read %s: %w", path, err)
	}
	var rec record
	if err := yaml.UnmarshalStrict(data, &rec); err != nil {
		return record{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if rec.QRPort < qrPortBase || rec.QRPort > 65535 {
		return record{}, fmt.Errorf("%s: qr_port %d is outside %d-65535", path, rec.QRPort, qrPortBase)
	}
	return rec, nil
}
