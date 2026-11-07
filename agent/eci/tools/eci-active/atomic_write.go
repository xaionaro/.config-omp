package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// writeFileAtomic streams src into a fresh sibling temp file, sets mode, and renames it over path.
func writeFileAtomic(path string, src io.Reader, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := io.Copy(tmp, src); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	committed = true
	return nil
}

// copyFileAtomic copies a regular source file over dst with mode, via writeFileAtomic.
func copyFileAtomic(src, dst string, mode os.FileMode) error {
	if !isRegularNoFollow(src) {
		return fmt.Errorf("source is not a regular file: %s", src)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return writeFileAtomic(dst, in, mode)
}
