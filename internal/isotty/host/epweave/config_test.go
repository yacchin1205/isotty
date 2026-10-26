package epweave

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, enabled, err := LoadProjectConfig(dir); err != nil || enabled {
		t.Fatalf("LoadProjectConfig() = enabled %v, err %v; want disabled", enabled, err)
	}

	if err := SaveProjectConfig(dir, ProjectConfig{Prefix: "isotty"}); err != nil {
		t.Fatalf("SaveProjectConfig() error = %v", err)
	}
	cfg, enabled, err := LoadProjectConfig(dir)
	if err != nil || !enabled {
		t.Fatalf("LoadProjectConfig() = enabled %v, err %v", enabled, err)
	}
	if cfg.Prefix != "isotty" || cfg.TitlePrefix() != "isotty/" {
		t.Fatalf("cfg = %#v", cfg)
	}

	if err := RemoveProjectConfig(dir); err != nil {
		t.Fatalf("RemoveProjectConfig() error = %v", err)
	}
	if err := RemoveProjectConfig(dir); err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("second RemoveProjectConfig() error = %v", err)
	}
}

func TestParseProjectConfig(t *testing.T) {
	cfg, err := ParseProjectConfig([]byte("prefix: demo\n"))
	if err != nil {
		t.Fatalf("ParseProjectConfig() error = %v", err)
	}
	if cfg.Prefix != "demo" {
		t.Fatalf("cfg = %#v", cfg)
	}
	for _, data := range []string{"", "prefix: ''\n", "prefix: a/b\n", "prefix: '#x'\n", "prefix: 'a b'\n", "prefix: ok\nvm_port: 61001\n"} {
		if _, err := ParseProjectConfig([]byte(data)); err == nil {
			t.Fatalf("ParseProjectConfig(%q) should fail", data)
		}
	}
}

func TestLoadHostConfig(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadHostConfig(dir); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing host config error = %v", err)
	}
	path := HostConfigPath(dir)
	if err := os.WriteFile(path, []byte("url: http://localhost:9001/\nclient_id: cli\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHostConfig(dir); err == nil || !strings.Contains(err.Error(), "client_secret") {
		t.Fatalf("incomplete host config error = %v", err)
	}
	if err := os.WriteFile(path, []byte("url: http://localhost:9001/\nclient_id: cli\nclient_secret: s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadHostConfig(dir)
	if err != nil {
		t.Fatalf("LoadHostConfig() error = %v", err)
	}
	if cfg.URL != "http://localhost:9001" {
		t.Fatalf("URL = %q", cfg.URL)
	}
	if filepath.Base(path) != "epweave.yaml" {
		t.Fatalf("path = %s", path)
	}
}

func TestMarkdownToHTML(t *testing.T) {
	got := MarkdownToHTML("# Title\n\n- a & b\n* c\n\ntext <x>\n\n### Deep\n")
	want := "<h1>Title</h1><ul><li>a &amp; b</li><li>c</li></ul><p>text &lt;x&gt;</p><h3>Deep</h3>"
	if got != want {
		t.Fatalf("MarkdownToHTML() = %s, want %s", got, want)
	}
}

func TestInsertionPoint(t *testing.T) {
	withTags := "<html><body>t<br>body<br>#a #b<br></body></html>"
	at, err := insertionPoint(withTags)
	if err != nil || withTags[at:] != "#a #b<br></body></html>" {
		t.Fatalf("insertionPoint() = %d, %v", at, err)
	}
	withoutTags := "<html><body>t<br>body<br></body></html>"
	at, err = insertionPoint(withoutTags)
	if err != nil || withoutTags[at:] != "</body></html>" {
		t.Fatalf("insertionPoint() = %d, %v", at, err)
	}
	if _, err := insertionPoint("<p>no body</p>"); err == nil {
		t.Fatal("insertionPoint() should fail without </body>")
	}
}

func TestSetupCommand(t *testing.T) {
	command := SetupCommand(ProjectConfig{Prefix: "demo"}, []string{"claude", "codex"})
	for _, want := range []string{"set -euo pipefail", "sudo tee " + ClientPath, "sudo chmod 755 " + ClientPath, "sshd_config.d/00-isotty.conf", "systemctl reload ssh", ".claude/skills/" + SkillName + "/SKILL.md", ".codex/skills/" + SkillName + "/SKILL.md"} {
		if !strings.Contains(command, want) {
			t.Fatalf("SetupCommand() missing %q:\n%s", want, command)
		}
	}
	if strings.Contains(SetupCommand(ProjectConfig{Prefix: "demo"}, nil), "SKILL.md") {
		t.Fatal("SetupCommand() should not install the skill without an agent")
	}
	rendered := render(notesClientScript, ProjectConfig{Prefix: "demo"})
	if !strings.Contains(rendered, "http://127.0.0.1:61001/epweave") || strings.Contains(rendered, "__ISOTTY_") {
		t.Fatalf("rendered client script not substituted:\n%s", rendered)
	}
	skill := render(notesSkill, ProjectConfig{Prefix: "demo"})
	if !strings.Contains(skill, "'demo/<topic>'") || strings.Contains(skill, "__ISOTTY_") {
		t.Fatalf("rendered skill not substituted:\n%s", skill)
	}
}

func TestHTMLLines(t *testing.T) {
	doc := "<!DOCTYPE HTML><html><body><h1>t&#x2F;x</h1><br><br><ul class=\"bullet\"><li>a &amp; b</li><li>c</ul><h2>next</h2>#tag<br></body></html>"
	got := htmlLines(doc)
	if strings.Join(got, "|") != "t/x|a & b|c|next|#tag" {
		t.Fatalf("htmlLines() = %q", got)
	}
}
