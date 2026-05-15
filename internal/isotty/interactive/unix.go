//go:build darwin || linux

package interactive

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
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

	go func() {
		_, _ = io.Copy(terminal, os.Stdin)
	}()
	go func() {
		_, _ = io.Copy(os.Stdout, terminal)
	}()

	if err := cmd.Wait(); err != nil {
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

func terminalSize() (height, width int) {
	width, height, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return 24, 80
	}
	return height, width
}
