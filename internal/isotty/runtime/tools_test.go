package runtimecfg

import (
	"os"
	"strings"
	"testing"
)

func TestAddAndRemoveTools(t *testing.T) {
	projectDir := t.TempDir()

	if err := AddTools(projectDir, []string{"doc-tools"}); err != nil {
		t.Fatalf("AddTools() error = %v", err)
	}

	tools, err := ListTools(projectDir)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools) != 1 || tools[0] != "doc-tools" {
		t.Fatalf("tools = %v, want [doc-tools]", tools)
	}

	if err := RemoveTools(projectDir, []string{"doc-tools"}); err != nil {
		t.Fatalf("RemoveTools() error = %v", err)
	}
	if _, err := os.Stat(ToolsConfigPath(projectDir)); !os.IsNotExist(err) {
		t.Fatalf("tools config should be removed, stat err = %v", err)
	}
}

func TestAddUnsupportedTool(t *testing.T) {
	projectDir := t.TempDir()

	if err := AddTools(projectDir, []string{"not-a-tool"}); err == nil {
		t.Fatal("AddTools() with unsupported tool should error")
	}
	if _, err := os.Stat(ToolsConfigPath(projectDir)); !os.IsNotExist(err) {
		t.Fatalf("tools config should not be created, stat err = %v", err)
	}
}

func TestAptInstallPackagesMergesToolPackages(t *testing.T) {
	cfg := RuntimeConfig{
		AptPackages: []string{"jq", "ripgrep"},
		Tools:       []string{"doc-tools"},
	}

	packages := AptInstallPackages(cfg)

	if packages[0] != "jq" || packages[1] != "ripgrep" {
		t.Fatalf("apt.txt packages should come first, got %v", packages)
	}
	// ripgrep is also part of doc-tools and must not be duplicated.
	count := 0
	for _, pkg := range packages {
		if pkg == "ripgrep" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("ripgrep should appear once, got %d in %v", count, packages)
	}
	if !contains(packages, "poppler-utils") || !contains(packages, "libreoffice") {
		t.Fatalf("doc-tools apt packages missing from %v", packages)
	}
}

func TestBootstrapCommandWithTools(t *testing.T) {
	cfg := RuntimeConfig{Tools: []string{"doc-tools"}}

	command, err := BootstrapCommand(cfg, "/workspace")
	if err != nil {
		t.Fatalf("BootstrapCommand() error = %v", err)
	}

	if !strings.Contains(command, "sudo apt-get update") {
		t.Fatalf("expected apt-get update, got %q", command)
	}
	if !strings.Contains(command, "poppler-utils") {
		t.Fatalf("expected doc-tools apt packages, got %q", command)
	}
	if !strings.Contains(command, "pip3 install --break-system-packages") {
		t.Fatalf("expected system pip install, got %q", command)
	}
	if !strings.Contains(command, "python-docx") {
		t.Fatalf("expected pip install of doc tools, got %q", command)
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
