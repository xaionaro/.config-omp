//go:build linux

package main

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// renameNativeNoReplace atomically installs a native database without replacement.
//
// Example: a competing initializer cannot overwrite an existing database.
func renameNativeNoReplace(
	source string,
	target string,
) error {
	if err := unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, target, unix.RENAME_NOREPLACE); err != nil {
		return fmt.Errorf("native no-replace install failed; staging preserved: %w", err)
	}
	return nil
}
