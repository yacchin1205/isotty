package isotty

import "testing"

func TestSyncIgnored(t *testing.T) {
	for rel, want := range map[string]bool{
		"figs/plot.png":             false,
		".env":                      true,
		".env/inner.txt":            true,
		".ENV":                      false,
		"app/.env.local":            true,
		"environment.md":            false,
		"certs/server.pem":          true,
		"keys/id.key":               true,
		".ssh/id_ed25519":           true,
		"home/.aws/credentials":     true,
		".isotty/audit/2026.tar.gz": true,
		"src/.isotty/audit/x":       false,
		".isotty/claude.tar.gz.enc": false,
		"node_modules/x/index.js":   true,
		"docs/node_modules.md":      false,
		".venv/bin/python":          true,
		"a/.venv":                   false,
		"":                          false,
	} {
		if got := syncIgnored(rel); got != want {
			t.Errorf("syncIgnored(%q) = %v, want %v", rel, got, want)
		}
	}
}
