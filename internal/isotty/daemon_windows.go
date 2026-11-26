//go:build windows

package isotty

import (
	"errors"
	"os"
	"os/exec"
)

func lockDaemon(string) (*os.File, error) {
	return nil, errors.New("the isotty daemon is not available on Windows")
}

func detach(*exec.Cmd) {}
