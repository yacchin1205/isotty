package runtimecfg

import (
	"os"
	"testing"
)

func TestAddAndRemoveServices(t *testing.T) {
	projectDir := t.TempDir()

	if err := AddServices(projectDir, []string{"docker"}); err != nil {
		t.Fatalf("AddServices() error = %v", err)
	}

	services, err := ListServices(projectDir)
	if err != nil {
		t.Fatalf("ListServices() error = %v", err)
	}
	if len(services) != 1 || services[0] != "docker" {
		t.Fatalf("services = %v, want [docker]", services)
	}

	if err := RemoveServices(projectDir, []string{"docker"}); err != nil {
		t.Fatalf("RemoveServices() error = %v", err)
	}
	if _, err := os.Stat(ServiceConfigPath(projectDir)); !os.IsNotExist(err) {
		t.Fatalf("service config should be removed, stat err = %v", err)
	}
}
