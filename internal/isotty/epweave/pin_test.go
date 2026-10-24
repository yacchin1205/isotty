package epweave

import (
	"strings"
	"testing"
)

func TestVerifyPinned(t *testing.T) {
	home := t.TempDir()
	synced := ProjectConfig{Prefix: "isotty"}

	if _, err := VerifyPinned(home, "abc", synced); err == nil || !strings.Contains(err.Error(), "not pinned") {
		t.Fatalf("unpinned error = %v", err)
	}

	if err := SavePin(home, "abc", synced); err != nil {
		t.Fatalf("SavePin() error = %v", err)
	}
	if got, err := VerifyPinned(home, "abc", synced); err != nil || got.Prefix != "isotty" {
		t.Fatalf("VerifyPinned() = %#v, %v", got, err)
	}

	port := 61002
	for _, tampered := range []ProjectConfig{{Prefix: "other"}, {Prefix: "isotty", VMPort: &port}} {
		if _, err := VerifyPinned(home, "abc", tampered); err == nil || !strings.Contains(err.Error(), "differs from the configuration pinned") {
			t.Fatalf("tampered %#v error = %v", tampered, err)
		}
	}

	if err := RemovePin(home, "abc"); err != nil {
		t.Fatalf("RemovePin() error = %v", err)
	}
	if _, ok, err := LoadPin(home, "abc"); err != nil || ok {
		t.Fatalf("LoadPin() after remove = %v, %v", ok, err)
	}
	if err := RemovePin(home, "abc"); err != nil {
		t.Fatalf("second RemovePin() error = %v", err)
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
