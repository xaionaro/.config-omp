package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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
