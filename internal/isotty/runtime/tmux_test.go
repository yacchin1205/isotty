package runtimecfg

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
)

func TestTmuxSetupCommandRelaysHyperlinks(t *testing.T) {
	command := TmuxSetupCommand()
	install, write, found := strings.Cut(command, " && printf ")
	if !found || !strings.HasPrefix(install, "command -v tmux") {
		t.Fatalf("command = %q, want the install before the configuration", command)
	}
	if !strings.HasSuffix(write, "| sudo tee /etc/tmux.conf >/dev/null") {
		t.Fatalf("configuration = %q, want it written to /etc/tmux.conf", write)
	}
	encoded := regexp.MustCompile(`^%s ([A-Za-z0-9+/=]+) \|`).FindStringSubmatch(write)
	if encoded == nil {
		t.Fatalf("configuration = %q, want the content base64-encoded", write)
	}
	content, err := base64.StdEncoding.DecodeString(encoded[1])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "\nset -as terminal-features '*:hyperlinks'\n") {
		t.Fatalf("/etc/tmux.conf:\n%s\nwant the hyperlinks feature declared for every terminal", content)
	}
}
