package runtimecfg

import (
	"os"
	"testing"
)

func TestAddAndRemoveAptPackages(t *testing.T) {
	projectDir := t.TempDir()

	if err := AddAptPackages(projectDir, []string{"ripgrep", "jq"}); err != nil {
		t.Fatalf("AddAptPackages() error = %v", err)
	}
	if err := AddAptPackages(projectDir, []string{"jq", "fd-find"}); err != nil {
		t.Fatalf("AddAptPackages() second error = %v", err)
	}

	packages, err := ListAptPackages(projectDir)
	if err != nil {
		t.Fatalf("ListAptPackages() error = %v", err)
	}
	if len(packages) != 3 {
		t.Fatalf("len(packages) = %d, want 3", len(packages))
	}
	if packages[0] != "ripgrep" || packages[1] != "jq" || packages[2] != "fd-find" {
		t.Fatalf("packages = %v, want [ripgrep jq fd-find]", packages)
	}

	if err := RemoveAptPackages(projectDir, []string{"jq", "missing"}); err != nil {
		t.Fatalf("RemoveAptPackages() error = %v", err)
	}
	packages, err = ListAptPackages(projectDir)
	if err != nil {
		t.Fatalf("ListAptPackages() after remove error = %v", err)
	}
	if len(packages) != 2 || packages[0] != "ripgrep" || packages[1] != "fd-find" {
		t.Fatalf("packages = %v, want [ripgrep fd-find]", packages)
	}
}

func TestRemoveAptPackagesDeletesFileWhenEmpty(t *testing.T) {
	projectDir := t.TempDir()
	if err := AddAptPackages(projectDir, []string{"ripgrep"}); err != nil {
		t.Fatalf("AddAptPackages() error = %v", err)
	}

	if err := RemoveAptPackages(projectDir, []string{"ripgrep"}); err != nil {
		t.Fatalf("RemoveAptPackages() error = %v", err)
	}

	if _, err := os.Stat(AptPackagesPath(projectDir)); !os.IsNotExist(err) {
		t.Fatalf("apt config should be removed, stat err = %v", err)
	}
}
