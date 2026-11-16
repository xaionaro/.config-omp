package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// guardSQLiteFiles rejects linked or nonregular mutable SQLite files before open.
//
// Example: a WAL hardlinked into another session cannot be opened accidentally.
func guardSQLiteFiles(path string) error {
	for i, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		name := path + suffix
		info, err := os.Lstat(name)
		if i != 0 && errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect SQLite file %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("SQLite file is not regular: %s", name)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Nlink != 1 {
			return fmt.Errorf("SQLite file link count is not one: %s", name)
		}
	}
	return nil
}
