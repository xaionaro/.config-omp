//go:build linux

package main

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// renameRecoveryNoReplace atomically quarantines a directory without overwriting a target.
//
// Example: an existing evidence directory remains untouched.
func renameRecoveryNoReplace(
	source string,
	target string,
) error {
	if err := unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, target, unix.RENAME_NOREPLACE); err != nil {
		return fmt.Errorf("no-replace recovery quarantine unavailable or conflicted; evidence preserved: %w", err)
	}
	return nil
}
