//go:build windows

package interactive

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

func Run(proc Process) error {
	cmd := exec.Command(proc.Program, proc.Args...)
	cmd.Env = proc.Env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
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
