package isotty

import (
	"fmt"
	"io"
	"os"
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
