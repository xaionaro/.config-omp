package main

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestStatNamedPreservesEntryIdentity checks descriptor-relative, nonfollowing metadata.
//
// Example: renaming the parent cannot redirect lookup, and dangling symlinks remain symlinks.
func TestStatNamedPreservesEntryIdentity(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "parent")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	directory, err := os.Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	if err := os.Symlink("missing-target", filepath.Join(parent, "link")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(parent, "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(parent, parent+"-moved"); err != nil {
		t.Fatal(err)
	}
	for name, wantType := range map[string]uint32{"link": syscall.S_IFLNK, "fifo": syscall.S_IFIFO} {
		got, err := statNamed(int(directory.Fd()), name)
		if err != nil {
			t.Fatalf("statNamed(%q): %v", name, err)
		}
		var want syscall.Stat_t
		if err := syscall.Lstat(filepath.Join(parent+"-moved", name), &want); err != nil {
			t.Fatal(err)
		}
		if got != fileIdentityFromStat(want) || got.mode&syscall.S_IFMT != wantType {
			t.Fatalf("statNamed(%q) = %#v, want %#v type %o", name, got, fileIdentityFromStat(want), wantType)
		}
	}
	for name, wantErr := range map[string]error{"missing": syscall.ENOENT, "bad\x00name": syscall.EINVAL} {
		if _, err := statNamed(int(directory.Fd()), name); !errors.Is(err, wantErr) {
			t.Fatalf("statNamed(%q) error = %v, want %v", name, err, wantErr)
		}
	}
}
