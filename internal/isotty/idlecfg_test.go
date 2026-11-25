package isotty

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestLoadIdleConfig(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		file string
		want time.Duration
		err  string
	}{
		{"", defaultIdleStop, ""},
		{"stop_after: 2h\n", 2 * time.Hour, ""},
		{"stop_after: 0\n", 0, ""},
		{"stop_after: \"0\"\n", 0, ""},
		{"stop_after: 30\n", 0, "duration such as 30m"},
		{"stop_after: -5m\n", 0, "negative"},
		{"stop: 1h\n", 0, "stop"},
	} {
		if c.file == "" {
			os.Remove(IdleConfigPath(dir))
		} else if err := os.WriteFile(IdleConfigPath(dir), []byte(c.file), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadIdleConfig(dir)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: error %v, want %q", c.file, err, c.err)
			}
			continue
		}
		if err != nil || cfg.Stop() != c.want {
			t.Errorf("%q: %v, %v; want %v", c.file, cfg.Stop(), err, c.want)
		}
	}
}
