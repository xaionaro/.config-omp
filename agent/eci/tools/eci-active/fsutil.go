package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// digestFile returns the lowercase SHA-256 of a regular file. Symlinks are refused.
func digestFile(path string) (string, error) {
	if !isRegularNoFollow(path) {
		return "", fmt.Errorf("not a regular file: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// refuseUnsafeLeaf rejects a managed leaf that exists but is not a regular file (including symlinks).
func refuseUnsafeLeaf(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("[ECI_RUNTIME_SYNC_TARGET_UNSAFE] managed leaf is not a regular file: %s", path)
	}
	return nil
}

// ensureSafeDir creates the parent directories of rel under root. Any existing symlinked or non-directory
// component is refused.
func ensureSafeDir(root, rel string) error {
	cur := root
	for _, part := range strings.Split(filepath.ToSlash(filepath.Dir(rel)), "/") {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if os.IsNotExist(err) {
			if err := os.Mkdir(cur, 0o755); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("[ECI_RUNTIME_SYNC_TARGET_UNSAFE] managed directory is not a plain directory: %s", cur)
		}
	}
	return nil
}

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

// lockFileExclusive takes a blocking exclusive flock on path, creating it if needed. Symlinks are refused.
func lockFileExclusive(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("[ECI_RUNTIME_LOCK] cannot open lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}
