package isotty

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestSpinnerStopSuccess(t *testing.T) {
	var buf bytes.Buffer
	s := newSpinner(&buf, "Testing")
	s.stopSuccess()
	output := buf.String()
	if !strings.Contains(output, "OK") {
		t.Fatalf("output = %q", output)
	}
	if !strings.Contains(output, "Testing") {
		t.Fatalf("output = %q", output)
	}
}

func TestSpinnerStopFailure(t *testing.T) {
	var buf bytes.Buffer
	s := newSpinner(&buf, "Testing")
	s.stopFailure()
	output := buf.String()
	if !strings.Contains(output, "NG") {
		t.Fatalf("output = %q", output)
	}
	if !strings.Contains(output, "Testing") {
		t.Fatalf("output = %q", output)
	}
}

func TestIsTTYFalseForBuffer(t *testing.T) {
	app := &App{stdout: &bytes.Buffer{}}
	if app.isTTY() {
		t.Fatal("isTTY() = true, want false for bytes.Buffer")
	}
}

func TestIsTTYFalseForDumbTerminal(t *testing.T) {
	t.Setenv("TERM", "dumb")
	app := &App{stdout: os.Stdout}
	if app.isTTY() {
		t.Fatal("isTTY() = true, want false for TERM=dumb")
	}
}
