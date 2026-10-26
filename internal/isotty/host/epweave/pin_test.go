package epweave

import (
	"strings"
	"testing"

	"github.com/yazawa/isotty/internal/isotty/host/pin"
)

func TestVerifyPinned(t *testing.T) {
	home := t.TempDir()
	synced := ProjectConfig{Prefix: "isotty"}

	if _, err := VerifyPinned(home, "abc", synced); err == nil || !strings.Contains(err.Error(), "not pinned") {
		t.Fatalf("unpinned error = %v", err)
	}

	if err := pin.Save(home, "abc", PinKind, synced); err != nil {
		t.Fatalf("pin.Save() error = %v", err)
	}
	if got, err := VerifyPinned(home, "abc", synced); err != nil || got.Prefix != "isotty" {
		t.Fatalf("VerifyPinned() = %#v, %v", got, err)
	}

	if _, err := VerifyPinned(home, "abc", ProjectConfig{Prefix: "other"}); err == nil || !strings.Contains(err.Error(), "differs from the prefix pinned") {
		t.Fatalf("tampered prefix error = %v", err)
	}

	if err := pin.Remove(home, "abc", PinKind); err != nil {
		t.Fatalf("pin.Remove() error = %v", err)
	}
	if _, err := VerifyPinned(home, "abc", synced); err == nil || !strings.Contains(err.Error(), "not pinned") {
		t.Fatalf("error after remove = %v", err)
	}
	if err := pin.Remove(home, "abc", PinKind); err != nil {
		t.Fatalf("second pin.Remove() error = %v", err)
	}
}

func TestAddedLines(t *testing.T) {
	prev := []string{"title", "", "a", "b", "#tags"}
	next := []string{"title", "", "a", "b", "c", "d", "#tags", "", ""}
	got := addedLines(prev, next)
	if strings.Join(got, "|") != "c|d||" {
		t.Fatalf("addedLines() = %q", got)
	}
	if got := addedLines(next, next); len(got) != 0 {
		t.Fatalf("addedLines(same) = %q", got)
	}
	if got := addedLines(nil, []string{"x"}); len(got) != 1 || got[0] != "x" {
		t.Fatalf("addedLines(nil) = %q", got)
	}
}
