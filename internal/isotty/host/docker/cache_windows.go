package docker

import "io/fs"

// The temporary directory of Windows is the user's own.
func rootName() string {
	return "isotty"
}

func private(info fs.FileInfo) bool {
	return info.IsDir()
}
