//go:build darwin || linux

package interactive

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"

	pty "github.com/aymanbagabas/go-pty"
	"golang.org/x/term"
)

func Run(proc Process) error {
	rows, cols := terminalSize()
	terminal, err := pty.New()
	if err != nil {
		return fmt.Errorf("create pty: %w", err)
	}
	defer terminal.Close()
	if err := terminal.Resize(cols, rows); err != nil {
		return fmt.Errorf("resize pty: %w", err)
	}

	cmd := terminal.Command(proc.Program, proc.Args...)
	cmd.Env = proc.Env
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start interactive process: %w", err)
	}

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("set raw terminal mode: %w", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	defer signal.Stop(sigCh)
	go func() {
		for range sigCh {
			rows, cols := terminalSize()
			_ = terminal.Resize(cols, rows)
		}
	}()

	done := make(chan struct{})
	go forwardStdin(terminal, done)
	go func() {
		_, _ = io.Copy(os.Stdout, terminal)
	}()

	err = cmd.Wait()
	close(done)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// Interactive sessions may return 130 after delivering Ctrl+C to the remote process.
			if exitErr.ExitCode() == 130 {
				return nil
			}
			return fmt.Errorf("interactive process failed with exit code %d: %w", exitErr.ExitCode(), err)
		}
		return err
	}
	return nil
}

// stdin is read by one goroutine for the life of the process, not one per
// Run: a read blocks until the human types, so a goroutine of a Run that
// has ended would take the first keys meant for the next one, an attach
// again after a dropped connection, and write them to a terminal no one
// reads.
var (
	stdinOnce sync.Once
	stdinKeys chan []byte

	// pendingKeys is what reached a Run after its process ended, kept for
	// the next one.
	pendingMu   sync.Mutex
	pendingKeys []byte
)

func readStdin() <-chan []byte {
	stdinOnce.Do(func() {
		stdinKeys = make(chan []byte)
		go func() {
			buf := make([]byte, 32*1024)
			for {
				n, err := os.Stdin.Read(buf)
				if n > 0 {
					stdinKeys <- append([]byte(nil), buf[:n]...)
				}
				if err != nil {
					close(stdinKeys)
					return
				}
			}
		}()
	})
	return stdinKeys
}

// forwardStdin writes the human's keys to the terminal until done.
func forwardStdin(terminal io.Writer, done <-chan struct{}) {
	pendingMu.Lock()
	kept := pendingKeys
	pendingKeys = nil
	pendingMu.Unlock()
	if len(kept) > 0 {
		if _, err := terminal.Write(kept); err != nil {
			return
		}
	}
	keys := readStdin()
	for {
		select {
		case <-done:
			return
		case chunk, ok := <-keys:
			if !ok {
				return
			}
			select {
			case <-done:
				pendingMu.Lock()
				pendingKeys = append(pendingKeys, chunk...)
				pendingMu.Unlock()
				return
			default:
			}
			if _, err := terminal.Write(chunk); err != nil {
				return
			}
		}
	}
}

func terminalSize() (height, width int) {
	width, height, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return 24, 80
	}
	return height, width
}
