package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// openPrivateSpill creates an anonymous spill in the OS account's existing tmp.
//
// Example: TMPDIR cannot redirect report content into a different directory.
func openPrivateSpill(prefix string) (*os.File, error) {
	return openPrivateSpillAt(accountHome(), prefix)
}

// openPrivateSpillAt creates and unlinks a private spill beneath the given home.
//
// Example: a missing home or tmp directory is reported rather than created.
func openPrivateSpillAt(
	home string,
	prefix string,
) (*os.File, error) {
	if home == "" || !filepath.IsAbs(home) {
		return nil, fmt.Errorf("account home is unavailable")
	}
	f, err := os.CreateTemp(filepath.Join(home, "tmp"), prefix)
	if err != nil {
		return nil, fmt.Errorf("create private spill: %w", err)
	}
	if err := os.Remove(f.Name()); err != nil {
		return nil, errors.Join(fmt.Errorf("unlink private spill: %w", err), f.Close())
	}
	return f, nil
}
