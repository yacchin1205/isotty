package docker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ~/.isotty/docker.yaml is optional: without it the host keeps blobs for
// four hours and no pushed image; with it, the owner says how long and
// whether, and a time that is not one is refused.
func TestHostConfigIsOptionalAndSaysHowLongToKeep(t *testing.T) {
	home := t.TempDir()
	cfg, err := LoadHostConfig(home)
	if err != nil || cfg.Keep != 4*time.Hour || cfg.KeepPushed {
		t.Fatalf("without the file: %+v, %v", cfg, err)
	}
	write := func(content string) {
		if err := os.WriteFile(filepath.Join(home, "docker.yaml"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("keep: 30m\nkeep_pushed: true\n")
	cfg, err = LoadHostConfig(home)
	if err != nil || cfg.Keep != 30*time.Minute || !cfg.KeepPushed {
		t.Fatalf("with the file: %+v, %v", cfg, err)
	}
	write("keep_pushed: true\n")
	cfg, err = LoadHostConfig(home)
	if err != nil || cfg.Keep != 4*time.Hour || !cfg.KeepPushed {
		t.Fatalf("with keep left out: %+v, %v", cfg, err)
	}
	for _, content := range []string{"keep: 4\n", "keep: -1h\n", "keep: 0s\n", "keep_pushed: 4h\n", "kept: 4h\n"} {
		write(content)
		if _, err := LoadHostConfig(home); err == nil || !strings.Contains(err.Error(), "docker.yaml") {
			t.Fatalf("%q was accepted: %v", content, err)
		}
	}
}
