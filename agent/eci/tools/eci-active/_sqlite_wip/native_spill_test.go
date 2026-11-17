package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPrivateSpillUsesAccountTemporaryDirectory ignores ambient temporary paths.
//
// Example: the open file is private and unlinked before any content is written.
func TestPrivateSpillUsesAccountTemporaryDirectory(t *testing.T) {
	home := t.TempDir()
	other := t.TempDir()
	t.Setenv("TMPDIR", other)
	require.NoError(t, os.Mkdir(filepath.Join(home, "tmp"), 0700))
	spill, err := openPrivateSpillAt(home, "proof-")
	require.NoError(t, err)
	defer spill.Close()
	require.Equal(t, filepath.Join(home, "tmp"), filepath.Dir(spill.Name()))
	_, err = os.Lstat(spill.Name())
	require.True(t, errors.Is(err, os.ErrNotExist))
	info, err := spill.Stat()
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	_, err = spill.WriteString("private")
	require.NoError(t, err)
}

// TestPrivateSpillRequiresExistingAccountTemporaryDirectory never falls back.
//
// Example: a missing account tmp is an explicit error without creating it.
func TestPrivateSpillRequiresExistingAccountTemporaryDirectory(t *testing.T) {
	home := t.TempDir()
	other := t.TempDir()
	t.Setenv("TMPDIR", other)
	_, err := openPrivateSpillAt(home, "proof-")
	require.Error(t, err)
	_, err = os.Lstat(filepath.Join(home, "tmp"))
	require.True(t, errors.Is(err, os.ErrNotExist))
	_, err = openPrivateSpillAt("", "proof-")
	require.Error(t, err)
	entries, err := os.ReadDir(other)
	require.NoError(t, err)
	require.Empty(t, entries)
}

// TestPrivateSpillAllowsAccountTmpSymlink preserves the workstation layout.
//
// Example: a user-owned tmp symlink can point to another filesystem.
func TestPrivateSpillAllowsAccountTmpSymlink(t *testing.T) {
	home := t.TempDir()
	target := t.TempDir()
	require.NoError(t, os.Symlink(target, filepath.Join(home, "tmp")))
	spill, err := openPrivateSpillAt(home, "proof-")
	require.NoError(t, err)
	require.NoError(t, spill.Close())
}
