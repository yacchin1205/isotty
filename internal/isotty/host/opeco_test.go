package host

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpecoCommandTurnsTheProjectOffAndOn(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := Run(project, "hash", home, append([]string{"opeco"}, args...), &out, &out)
		return out.String(), err
	}
	if out, err := run("show"); err != nil || !strings.Contains(out, "enabled") {
		t.Fatalf("show = %q, %v", out, err)
	}
	if _, err := run("disable"); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(project, ".isotty", "opeco.yaml")
	if out, err := run("show"); err != nil || !strings.Contains(out, "disabled for this project: "+file) {
		t.Fatalf("show = %q, %v", out, err)
	}
	if _, err := run("enable"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("enable should leave no project setting: %v", err)
	}
}

// The host user's own setting is not the project's to undo.
func TestOpecoCommandCannotEnableOverTheHostUser(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	user := filepath.Join(home, "opeco.yaml")
	if err := os.WriteFile(user, []byte("enabled: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(project, "hash", home, []string{"opeco", "enable"}, &out, &out); err == nil || !strings.Contains(err.Error(), user) {
		t.Fatalf("enable = %v, want it to name %s", err, user)
	}
	if err := Run(project, "hash", home, []string{"opeco", "show"}, &out, &out); err != nil || !strings.Contains(out.String(), "every project of this host user: "+user) {
		t.Fatalf("show = %q, %v", out.String(), err)
	}
}
