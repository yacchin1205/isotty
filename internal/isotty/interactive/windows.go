//go:build windows

package interactive

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	pty "github.com/aymanbagabas/go-pty"
)

func Run(proc Process) error {
	terminal, err := pty.New()
	if err != nil {
		return fmt.Errorf("create pty: %w", err)
	}
	defer terminal.Close()

	cmd := terminal.Command(proc.Program, proc.Args...)
	cmd.Env = proc.Env
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start interactive process: %w", err)
	}

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
