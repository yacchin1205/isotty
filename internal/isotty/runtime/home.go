package runtimecfg

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// HomeFilesDir is the host directory whose contents every VM gets in its home.
func HomeFilesDir(homeDir string) string {
	return filepath.Join(homeDir, "home")
}

// HomeFilesArchive packs HomeFilesDir as a tar.gz for UnpackHomeCommand. ok is
// false when the directory is missing or empty. Symbolic links are rejected.
func HomeFilesArchive(homeDir string) ([]byte, bool, error) {
	root := HomeFilesDir(homeDir)
	rootInfo, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if rootInfo.Mode()&fs.ModeSymlink != 0 {
		return nil, false, symlinkError(root, root)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	entries := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return symlinkError(path, root)
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return fmt.Errorf("%s is neither a regular file nor a directory", path)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		header := &tar.Header{
			Name:    filepath.ToSlash(rel),
			Mode:    int64(info.Mode().Perm()),
			ModTime: info.ModTime(),
		}
		if d.IsDir() {
			header.Typeflag = tar.TypeDir
			header.Name += "/"
		} else {
			header.Typeflag = tar.TypeReg
			header.Size = info.Size()
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !d.IsDir() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			if _, err := io.Copy(tw, f); err != nil {
				return fmt.Errorf("read %s: %w", path, err)
			}
		}
		entries++
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if entries == 0 {
		return nil, false, nil
	}
	if err := tw.Close(); err != nil {
		return nil, false, err
	}
	if err := gz.Close(); err != nil {
		return nil, false, err
	}
	return buf.Bytes(), true, nil
}

func symlinkError(path, root string) error {
	return fmt.Errorf("%s is a symbolic link; %s must hold copies, not links", path, root)
}

// UnpackHomeCommand extracts an archive streamed on stdin into the VM home.
func UnpackHomeCommand() string {
	return "tar -xzf - -C ~"
}
