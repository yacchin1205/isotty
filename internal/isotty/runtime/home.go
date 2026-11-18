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
	"strings"
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

// SavedAgentPaths returns the paths of an agent's home that a saved
// archive holds.
func SavedAgentPaths(agent string, archive []byte) ([]string, error) {
	paths, ok := agentHomePaths[agent]
	if !ok {
		panic(fmt.Sprintf("unsupported agent %q", agent))
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("read %s login state: %w", agent, err)
	}
	held := map[string]bool{}
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read %s login state: %w", agent, err)
		}
		held[strings.SplitN(strings.TrimPrefix(header.Name, "./"), "/", 2)[0]] = true
	}
	var saved []string
	for _, p := range paths {
		if held[p] {
			saved = append(saved, p)
		}
	}
	return saved, nil
}

// AgentHomeCheck holds when every given path of the home exists.
func AgentHomeCheck(paths []string) string {
	checks := []string{"true"}
	for _, p := range paths {
		checks = append(checks, fmt.Sprintf("[ -e ~/%s ]", shellJoin([]string{p})))
	}
	return strings.Join(checks, " && ")
}

// RestoreAgentHomeCommand restores, from an archive on stdin, each of the
// agent's paths that is absent from the home: staged on the home's file
// system, so that the path appears whole with one rename or not at all.
// A path that exists is the VM's own and is left as it is. What an
// interrupted restore staged is removed first.
func RestoreAgentHomeCommand(agent string) string {
	paths, ok := agentHomePaths[agent]
	if !ok {
		panic(fmt.Sprintf("unsupported agent %q", agent))
	}
	return fmt.Sprintf(
		`set -euo pipefail && rm -rf ~/.isotty-restore.* && tmp=$(mktemp -d -p ~ .isotty-restore.XXXXXX) && tar -xzf - -C "$tmp" && for p in %s; do if [ ! -e ~/"$p" ] && [ -e "$tmp/$p" ]; then mv "$tmp/$p" ~/; fi; done && rm -rf "$tmp"`,
		shellJoin(paths))
}
