package runtimecfg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAddAndRemoveAgents(t *testing.T) {
	projectDir := t.TempDir()

	if err := AddAgents(projectDir, []string{"codex"}); err != nil {
		t.Fatalf("AddAgents() error = %v", err)
	}
	if err := AddAgents(projectDir, []string{"claude", "codex"}); err != nil {
		t.Fatalf("AddAgents() second error = %v", err)
	}

	agents, err := ListAgents(projectDir)
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(agents) != 2 || agents[0] != "claude" || agents[1] != "codex" {
		t.Fatalf("agents = %v, want [claude codex]", agents)
	}

	if err := RemoveAgents(projectDir, []string{"codex"}); err != nil {
		t.Fatalf("RemoveAgents() error = %v", err)
	}
	agents, err = ListAgents(projectDir)
	if err != nil {
		t.Fatalf("ListAgents() after remove error = %v", err)
	}
	if len(agents) != 1 || agents[0] != "claude" {
		t.Fatalf("agents = %v, want [claude]", agents)
	}
}

func TestRemoveAgentsDeletesFileWhenEmpty(t *testing.T) {
	projectDir := t.TempDir()
	if err := AddAgents(projectDir, []string{"codex"}); err != nil {
		t.Fatalf("AddAgents() error = %v", err)
	}

	if err := RemoveAgents(projectDir, []string{"codex"}); err != nil {
		t.Fatalf("RemoveAgents() error = %v", err)
	}

	if _, err := os.Stat(AgentConfigPath(projectDir)); !os.IsNotExist(err) {
		t.Fatalf("agent config should be removed, stat err = %v", err)
	}
}

func TestAgentConfigIsProjectLocal(t *testing.T) {
	projectDir := t.TempDir()
	if err := AddAgents(projectDir, []string{"codex"}); err != nil {
		t.Fatalf("AddAgents() error = %v", err)
	}

	if _, err := os.Stat(AgentConfigPath(projectDir)); err != nil {
		t.Fatalf("expected %s to exist: %v", filepath.Base(AgentConfigPath(projectDir)), err)
	}
}
