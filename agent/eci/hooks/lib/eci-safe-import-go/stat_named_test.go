package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
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
	if err := unix.Mkfifo(filepath.Join(parent, "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(parent, parent+"-moved"); err != nil {
		t.Fatal(err)
	}
	for name, wantType := range map[string]uint32{"link": unix.S_IFLNK, "fifo": unix.S_IFIFO} {
		var got, want unix.Stat_t
		if err := statNamed(int(directory.Fd()), name, &got); err != nil {
			t.Fatalf("statNamed(%q): %v", name, err)
		}
		if err := unix.Lstat(filepath.Join(parent+"-moved", name), &want); err != nil {
			t.Fatal(err)
		}
		if got.Dev != want.Dev || got.Ino != want.Ino || got.Mode != want.Mode || uint32(got.Mode)&unix.S_IFMT != wantType {
			t.Fatalf("statNamed(%q) = %#v, want %#v type %o", name, got, want, wantType)
		}
	}
	for name, wantErr := range map[string]error{"missing": unix.ENOENT, "bad\x00name": unix.EINVAL} {
		var got unix.Stat_t
		if err := statNamed(int(directory.Fd()), name, &got); !errors.Is(err, wantErr) {
			t.Fatalf("statNamed(%q) error = %v, want %v", name, err, wantErr)
		}
	}
}
