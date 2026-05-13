package isotty

import (
	"path/filepath"
	"testing"
)

func TestHashProjectPathIsStable(t *testing.T) {
	path := filepath.Clean("/tmp/project")
	first := hashProjectPath(path)
	second := hashProjectPath(path)

	if first != second {
		t.Fatalf("hash should be stable: %q != %q", first, second)
	}
	if len(first) != 12 {
		t.Fatalf("hash length = %d, want 12", len(first))
	}
}

func TestValidateSyncMode(t *testing.T) {
	if err := validateSyncMode(defaultSyncMode); err != nil {
		t.Fatalf("default mode should be valid: %v", err)
	}
	if err := validateSyncMode(developmentSyncMode); err != nil {
		t.Fatalf("development mode should be valid: %v", err)
	}
	if err := validateSyncMode("two-way-resolved"); err == nil {
		t.Fatal("unexpected validation success for unsupported mode")
	}
}
