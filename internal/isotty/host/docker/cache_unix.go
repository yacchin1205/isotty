//go:build !windows

package docker

import (
	"io/fs"
	"os"
	"strconv"
	"syscall"
)

// rootName carries the user because the temporary directory may be one for
// every user of the machine.
func rootName() string {
	return "isotty-" + strconv.Itoa(os.Getuid())
}

func private(info fs.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.IsDir() && info.Mode().Perm() == 0o700 && int(stat.Uid) == os.Getuid()
}
