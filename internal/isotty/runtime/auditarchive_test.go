package runtimecfg

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testAuditTar(t *testing.T, stamp string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: stamp + "/" + name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(content))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

const archiveExec = `----
time->Wed May 14 01:02:03 2026
type=SYSCALL msg=audit(05/14/2026 01:02:03.123:4242): arch=x86_64 syscall=execve success=yes exit=0 exe="/usr/bin/bash" key="isotty-exec"
type=EXECVE msg=audit(05/14/2026 01:02:03.123:4242): argc=2 a0="bash" a1="script.sh"
`

func TestAuditArchiveRoundTrip(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	stamp := AuditArchiveStamp(time.Date(2026, 10, 27, 3, 4, 5, 0, time.FixedZone("JST", 9*3600)))
	if stamp != "20261026T180405Z" {
		t.Fatalf("stamp = %s", stamp)
	}
	archive := testAuditTar(t, stamp, map[string]string{
		"exec.txt":      archiveExec,
		"connect.txt":   "",
		"events.jsonl":  `{"time":"2026-05-14T01:02:04Z","type":"github-push","project_hash":"h","repo":"me/project"}` + "\n",
		"raw/audit.log": "type=SYSCALL garbage that must not be parsed",
	})

	if archives, err := ListAuditArchives(project); err != nil || len(archives) != 0 {
		t.Fatalf("ListAuditArchives() before save = %v, %v", archives, err)
	}
	path, err := SaveAuditArchive(home, project, stamp, bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("SaveAuditArchive() error = %v", err)
	}
	if path != AuditArchivePath(project, stamp) || !strings.HasSuffix(path, "/.isotty/audit/"+stamp+".tar.gz.enc") {
		t.Fatalf("path = %s", path)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("stat = %v, %v", info, err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Fatalf("archive directory holds %d entries, want the archive alone", len(entries))
	}
	sealed, _ := os.ReadFile(path)
	if bytes.Contains(sealed, archive[:32]) {
		t.Fatal("archive is stored in plaintext")
	}
	archives, err := ListAuditArchives(project)
	if err != nil || len(archives) != 1 || archives[0] != path {
		t.Fatalf("ListAuditArchives() = %v, %v", archives, err)
	}

	stream, err := OpenAuditArchive(home, path)
	if err != nil {
		t.Fatalf("OpenAuditArchive() error = %v", err)
	}
	defer stream.Close()
	events, err := ReadAuditArchive(stream)
	if err != nil {
		t.Fatalf("ReadAuditArchive() error = %v", err)
	}
	if len(events) != 2 || events[0].Kind != "exec" || events[0].Command != "bash script.sh" || events[1].Kind != "isotty" || !strings.Contains(events[1].RawSummary(), "github-push") {
		t.Fatalf("events = %+v", events)
	}

	if _, err := OpenAuditArchive(t.TempDir(), path); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("OpenAuditArchive without the key error = %v", err)
	}
}

func TestReadAuditArchiveRejectsOtherTars(t *testing.T) {
	archive := testAuditTar(t, "x", map[string]string{"notes.txt": "hello"})
	if _, err := ReadAuditArchive(bytes.NewReader(archive)); err == nil || !strings.Contains(err.Error(), "not an isotty audit archive") {
		t.Fatalf("error = %v", err)
	}
}

func TestAuditArchiveCommands(t *testing.T) {
	capture := CaptureAuditArchiveCommand("20261027T000000Z")
	for _, want := range []string{
		"set -euo pipefail",
		"sudo ausearch --input-logs -i --key isotty-exec > $d/20261027T000000Z/exec.txt",
		"--key isotty-connect > $d/20261027T000000Z/connect.txt",
		"grep -q '<no matches>'",
		"/var/log/isotty/events.jsonl",
		`cp /var/log/audit/audit.log* "$0"' $d/20261027T000000Z/raw`,
		`tar -C "$d" -czf - 20261027T000000Z | base64 -w0`,
	} {
		if !strings.Contains(capture, want) {
			t.Fatalf("capture command lacks %q:\n%s", want, capture)
		}
	}
	restore := RestoreAuditArchiveCommand([]string{"claude", "codex"})
	for _, want := range []string{
		"sudo tar -xzf - -C /var/log/isotty/archive --exclude='*/raw'",
		"sudo chmod -R a+rX /var/log/isotty/archive",
		"~/.claude/skills/isotty-audit/SKILL.md",
		"~/.codex/skills/isotty-audit/SKILL.md",
		"name: isotty-audit",
	} {
		if !strings.Contains(restore, want) {
			t.Fatalf("restore command lacks %q:\n%s", want, restore)
		}
	}
	if strings.Contains(restore, "EOF &&") {
		t.Fatal("a heredoc terminator is followed by &&")
	}
}
