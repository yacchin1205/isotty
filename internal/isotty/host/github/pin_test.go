package github

import (
	"strings"
	"testing"

	"github.com/yazawa/isotty/internal/isotty/host/pin"
)

func TestVerifyPinned(t *testing.T) {
	home := t.TempDir()
	synced := ProjectConfig{Repos: map[string]Access{"a/b": AccessWrite}}

	if _, err := VerifyPinned(home, "abc", synced); err == nil || !strings.Contains(err.Error(), "not pinned") {
		t.Fatalf("unpinned error = %v", err)
	}
	if err := pin.Save(home, "abc", PinKind, synced); err != nil {
		t.Fatalf("pin.Save() error = %v", err)
	}
	if got, err := VerifyPinned(home, "abc", synced); err != nil || got.Repos["a/b"] != AccessWrite {
		t.Fatalf("VerifyPinned() = %#v, %v", got, err)
	}
	widened := ProjectConfig{Repos: map[string]Access{"a/b": AccessWrite, "c/d": AccessRead}}
	if _, err := VerifyPinned(home, "abc", widened); err == nil || !strings.Contains(err.Error(), "differs from the repositories pinned") {
		t.Fatalf("widened error = %v", err)
	}
	promoted := ProjectConfig{Repos: map[string]Access{"a/b": AccessWrite}}
	promoted.Repos["a/b"] = AccessWrite
	if err := pin.Save(home, "abc", PinKind, ProjectConfig{Repos: map[string]Access{"a/b": AccessRead}}); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyPinned(home, "abc", promoted); err == nil || !strings.Contains(err.Error(), "a/b:write") || !strings.Contains(err.Error(), "a/b:read") {
		t.Fatalf("promoted access error = %v", err)
	}
}
