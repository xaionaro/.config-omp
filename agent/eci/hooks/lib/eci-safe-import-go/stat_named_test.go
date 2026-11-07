package main

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestStatNamedPreservesEntryIdentity checks lookup after a parent rename and special entries.
//
// Example: inspecting a dangling symlink or FIFO must neither follow it nor open it for I/O.
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
		var got, want syscall.Stat_t
		if err := statNamed(int(directory.Fd()), name, &got); err != nil {
			t.Fatalf("statNamed(%q): %v", name, err)
		}
		if err := syscall.Lstat(filepath.Join(parent+"-moved", name), &want); err != nil {
			t.Fatal(err)
		}
		if got.Dev != want.Dev || got.Ino != want.Ino || got.Mode != want.Mode || got.Mode&syscall.S_IFMT != wantType {
			t.Fatalf("statNamed(%q) = %#v, want %#v type %o", name, got, want, wantType)
		}
	}
	for name, wantErr := range map[string]error{"missing": syscall.ENOENT, "bad\x00name": syscall.EINVAL} {
		var got syscall.Stat_t
		if err := statNamed(int(directory.Fd()), name, &got); !errors.Is(err, wantErr) {
			t.Fatalf("statNamed(%q) error = %v, want %v", name, err, wantErr)
		}
	}
}
