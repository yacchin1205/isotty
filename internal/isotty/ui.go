package isotty

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	colorReset = "\033[0m"
	colorCyan  = "\033[36m"
	colorGreen = "\033[32m"
	colorRed   = "\033[31m"
)

type spinner struct {
	writer io.Writer
	label  string
	done   chan struct{}
}

func newSpinner(writer io.Writer, label string) *spinner {
	return &spinner{
		writer: writer,
		label:  label,
		done:   make(chan struct{}),
	}
}

func (s *spinner) start() {
	frames := []string{"|", "/", "-", "\\"}
	go func() {
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()
		index := 0
		for {
			select {
			case <-s.done:
				return
			case <-ticker.C:
				fmt.Fprintf(s.writer, "\r%s%s%s %s", colorCyan, frames[index], colorReset, s.label)
				index = (index + 1) % len(frames)
			}
		}
	}()
}

func (s *spinner) stopSuccess() {
	close(s.done)
	fmt.Fprintf(s.writer, "\r%s%s%s %s\n", colorGreen, "OK", colorReset, s.label)
}

func (s *spinner) stopFailure() {
	close(s.done)
	fmt.Fprintf(s.writer, "\r%s%s%s %s\n", colorRed, "NG", colorReset, s.label)
}

// tidepoolArt holds the frames of the tide pool, a blank line between them.
// Every frame has the same lines of the same widths, since one is drawn over
// the other.
//
//go:embed assets/tidepool.txt
var tidepoolArt string

type tidepool struct {
	writer  io.Writer
	done    chan struct{}
	stopped chan struct{}
}

func newTidepool(writer io.Writer) *tidepool {
	return &tidepool{
		writer:  writer,
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
}

func (p *tidepool) start() {
	frames := strings.Split(strings.TrimSuffix(tidepoolArt, "\n"), "\n\n")
	height := strings.Count(frames[0], "\n") + 1
	fmt.Fprintf(p.writer, "\n%s\n", frames[0])
	go func() {
		defer close(p.stopped)
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()
		for index := 1; ; index = (index + 1) % len(frames) {
			select {
			case <-p.done:
				return
			case <-ticker.C:
				fmt.Fprintf(p.writer, "\033[%dA%s\n", height, frames[index])
			}
		}
	}()
}

// stop returns only once no frame is being drawn: whatever takes the
// terminal next must not have one land on it.
func (p *tidepool) stop() {
	close(p.done)
	<-p.stopped
	fmt.Fprintln(p.writer)
}

// waitAtTidepool runs fn, a wait the human sits through, with the tide pool
// moving on the terminal. It only fills time that passes anyway, and it
// draws on the host's terminal rather than as a tmux popup in the session,
// which would swallow what the human types ahead.
func (a *App) waitAtTidepool(fn func() error) error {
	if a.debug || !a.isTTY() {
		return fn()
	}
	pool := newTidepool(a.stdout)
	pool.start()
	defer pool.stop()
	return fn()
}

func (a *App) isTTY() bool {
	file, ok := a.stdout.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return true
}
