package isotty

import (
	"path/filepath"
	"testing"
)

func TestForwardConfigRoundTrip(t *testing.T) {
	projectDir := t.TempDir()
	cfg := ForwardConfig{
		Forwards: map[string]Forward{
			"web":  {LocalPort: 8080, RemotePort: 8080},
			"vite": {LocalPort: 5173, RemotePort: 5173},
		},
	}

	if err := SaveForwardConfig(projectDir, cfg); err != nil {
		t.Fatalf("SaveForwardConfig() error = %v", err)
	}

	loaded, err := LoadForwardConfig(projectDir)
	if err != nil {
		t.Fatalf("LoadForwardConfig() error = %v", err)
	}

	if len(loaded.Forwards) != 2 {
		t.Fatalf("len(loaded.Forwards) = %d, want 2", len(loaded.Forwards))
	}
	if loaded.Forwards["web"].LocalPort != 8080 || loaded.Forwards["web"].RemotePort != 8080 {
		t.Fatalf("loaded web forward = %#v", loaded.Forwards["web"])
	}
}

func TestAddForwardAndRemoveForward(t *testing.T) {
	projectDir := t.TempDir()

	if err := AddForward(projectDir, "web", Forward{LocalPort: 8080, RemotePort: 8080}); err != nil {
		t.Fatalf("AddForward() error = %v", err)
	}
	if err := AddForward(projectDir, "vite", Forward{LocalPort: 5173, RemotePort: 5173}); err != nil {
		t.Fatalf("AddForward() error = %v", err)
	}

	cfg, err := LoadForwardConfig(projectDir)
	if err != nil {
		t.Fatalf("LoadForwardConfig() error = %v", err)
	}
	names := SortedForwardNames(cfg)
	if len(names) != 2 || names[0] != "vite" || names[1] != "web" {
		t.Fatalf("SortedForwardNames() = %v", names)
	}

	if err := RemoveForward(projectDir, "web"); err != nil {
		t.Fatalf("RemoveForward() error = %v", err)
	}
	cfg, err = LoadForwardConfig(projectDir)
	if err != nil {
		t.Fatalf("LoadForwardConfig() error = %v", err)
	}
	if _, ok := cfg.Forwards["web"]; ok {
		t.Fatal("web forward should have been removed")
	}
	if _, err := filepath.Abs(forwardConfigPath(projectDir)); err != nil {
		t.Fatalf("forwardConfigPath() should be valid: %v", err)
	}
}
