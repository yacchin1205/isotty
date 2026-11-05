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

// A frame is drawn over the one before it, so a frame of another shape
// would leave pieces of that one on the terminal.
func TestTidepoolFramesShareOneShape(t *testing.T) {
	frames := strings.Split(strings.TrimSuffix(tidepoolArt, "\n"), "\n\n")
	if len(frames) < 2 {
		t.Fatalf("the tide pool has %d frames, nothing moves", len(frames))
	}
	first := strings.Split(frames[0], "\n")
	for i, frame := range frames[1:] {
		lines := strings.Split(frame, "\n")
		if len(lines) != len(first) {
			t.Fatalf("frame %d has %d lines, the first has %d", i+1, len(lines), len(first))
		}
		for j, line := range lines {
			if len(line) != len(first[j]) {
				t.Fatalf("frame %d line %d is %d wide, the first frame's is %d", i+1, j, len(line), len(first[j]))
			}
		}
	}
}

// However short the wait, the pool is there, and the terminal is left
// below it for whatever comes next.
func TestTidepoolShowsWithoutWaitingForATick(t *testing.T) {
	var buf bytes.Buffer
	pool := newTidepool(&buf)
	pool.start()
	pool.stop()
	first, _, _ := strings.Cut(tidepoolArt, "\n\n")
	if !strings.HasPrefix(buf.String(), "\n"+first+"\n") {
		t.Fatalf("output = %q", buf.String())
	}
}

// A pipe or a log file gets the wait's result and no drawing.
func TestWaitAtTidepoolDrawsNothingOffATerminal(t *testing.T) {
	var buf bytes.Buffer
	app := &App{stdout: &buf}
	ran := false
	if err := app.waitAtTidepool(func() error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("waitAtTidepool: ran = %v, err = %v", ran, err)
	}
	if buf.Len() != 0 {
		t.Fatalf("output = %q", buf.String())
	}
}
