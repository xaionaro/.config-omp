package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestApproveCommitPrintsNoticeAndSucceeds keeps the approve-commit spelling at exit 0 with the exact notice on stdout, whatever the arguments.
func TestApproveCommitPrintsNoticeAndSucceeds(t *testing.T) {
	const notice = "Normal Git commits do not need ECI approval artifacts. Continue with the intended Git command.\n"
	for _, args := range [][]string{
		{"approve-commit"},
		{"approve-commit", "/repo", "env GIT_EDITOR=true git -C /repo commit -am 'ordinary commit'"},
	} {
		out, err := captureStdout(t, func() error { return run(args) })
		require.NoError(t, err, "args %q", args)
		assert.Equal(t, notice, out, "args %q", args)
	}
}

// captureStdout runs fn and returns the bytes it wrote to os.Stdout alongside fn's error.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	require.NoError(t, err)
	saved := os.Stdout
	os.Stdout = f
	defer func() { os.Stdout = saved }()
	runErr := fn()
	require.NoError(t, f.Close())
	out, err := os.ReadFile(f.Name())
	require.NoError(t, err)
	return string(out), runErr
}
