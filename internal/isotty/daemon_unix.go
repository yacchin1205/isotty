//go:build !windows

package isotty

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// lockDaemon keeps one daemon per host user: two would hold the same VM's
// gateway and start the same VM for the same wake.
func lockDaemon(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, errors.Join(fmt.Errorf("another isotty daemon holds %s: %w", path, err), file.Close())
	}
	return file, nil
}

// detach starts the daemon in a session of its own, so it outlives the
// command and the terminal that started it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
