package isotty

import (
	"strings"
	"testing"
)

// The agent runs the binary that enabled it with that PATH, which launchd
// would not give it, and escapes what XML would read otherwise.
func TestLaunchdPlist(t *testing.T) {
	plist := launchdPlist("/Users/me/bin/isotty&dev", "/Users/me/.isotty/daemon.log", map[string]string{"PATH": "/opt/homebrew/bin:/usr/bin"})
	for _, want := range []string{
		"<string>/Users/me/bin/isotty&amp;dev</string><string>daemon</string><string>run</string>",
		"<key>PATH</key><string>/opt/homebrew/bin:/usr/bin</string>",
		"<key>KeepAlive</key><true/>",
		"<key>StandardOutPath</key><string>/Users/me/.isotty/daemon.log</string>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist lacks %s:\n%s", want, plist)
		}
	}
	if strings.Contains(plist, "ISOTTY_HOME") {
		t.Error("an unset ISOTTY_HOME was written")
	}
}
