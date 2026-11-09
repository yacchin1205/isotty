package browser

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupCommand(t *testing.T) {
	command := SetupCommand([]string{"claude", "codex"})
	for _, want := range []string{
		"set -euo pipefail",
		"sudo tee " + ClientPath,
		"sudo chmod 755 " + ClientPath,
		".claude/skills/" + SkillName + "/SKILL.md",
		".codex/skills/" + SkillName + "/SKILL.md",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("SetupCommand() missing %q:\n%s", want, command)
		}
	}
	if strings.Contains(SetupCommand(nil), "SKILL.md") {
		t.Fatal("SetupCommand() should not install the skill without an agent")
	}
	rendered := render(clientScript)
	if !strings.Contains(rendered, "http://127.0.0.1:61001") || strings.Contains(rendered, "__ISOTTY_") {
		t.Fatalf("rendered client script not substituted:\n%s", rendered)
	}
}

// `isotty-open reviews` in the VM reads the comments the sync carried over,
// names the place of each, tells when the file changed since and where the
// quoted text is now, and leaves resolved ones out unless asked.
func TestClientListsTheReviewsOfTheWorkspace(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	workspace := t.TempDir()
	for name, content := range map[string]string{
		"docs/report.md": "# Report\n\nIntro.\n\nAll good.\n",
		"figs/plot.png":  "png",
		".isotty/reviews/docs/report.md.jsonl": `{"id":"a1","at":"2026-11-08T10:00:00Z","anchor":{"lines":[3,3],"quote":"All good."},"body":"Is it, though?\nShow the numbers.","file_sha256":"sha256:old","resolved":false}` + "\n" +
			`{"id":"a2","at":"2026-11-08T10:01:00Z","anchor":{},"body":"Nice report.","file_sha256":"sha256:old","resolved":true}` + "\n",
		".isotty/reviews/figs/plot.png.jsonl": `{"id":"b1","at":"2026-11-08T10:02:00Z","anchor":{"point":[0.25,0.5]},"body":"What is this spike?","file_sha256":"sha256:x","resolved":false}` + "\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(workspace, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(t.TempDir(), "isotty-open")
	if err := os.WriteFile(script, []byte(render(clientScript)), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		cmd := exec.Command("python3", append([]string{script, "reviews"}, args...)...)
		cmd.Dir = filepath.Join(workspace, "docs")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("reviews %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	out := run()
	for _, want := range []string{
		"docs/report.md\n",
		`  L3  2026-11-08T10:00:00Z  "All good."  [file changed since; quoted text now at L5]`,
		"    Is it, though?\n    Show the numbers.\n",
		"figs/plot.png\n",
		"  (25%, 50%)  2026-11-08T10:02:00Z  [file changed since]\n    What is this spike?\n",
		"2 comments; 1 resolved not shown (--all shows them)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("reviews lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Nice report.") {
		t.Fatalf("a resolved comment was shown:\n%s", out)
	}
	if out := run("--all", filepath.Join(workspace, "docs", "report.md")); !strings.Contains(out, "  whole file  2026-11-08T10:01:00Z  [resolved; file changed since]\n    Nice report.") || strings.Contains(out, "plot.png") {
		t.Fatalf("reviews --all of one file:\n%s", out)
	}
}
