package main

import (
	"fmt"
	"os"
	"syscall"
)

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
