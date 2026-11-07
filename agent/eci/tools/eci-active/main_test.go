package main

import (
	"os"
	"testing"
)

// TestApproveCommitPrintsNoticeAndSucceeds keeps the approve-commit spelling at exit 0 with the exact notice on stdout, whatever the arguments.
func TestApproveCommitPrintsNoticeAndSucceeds(t *testing.T) {
	const notice = "Normal Git commits do not need ECI approval artifacts. Continue with the intended Git command.\n"
	for _, args := range [][]string{
		{"approve-commit"},
		{"approve-commit", "/repo", "env GIT_EDITOR=true git -C /repo commit -am 'ordinary commit'"},
	} {
		out, err := captureStdout(t, func() error { return run(args) })
		if err != nil {
			t.Fatalf("run %q: unexpected error: %#v", args, err)
		}
		if out != notice {
			t.Errorf("run %q: stdout = %q, want %q", args, out, notice)
		}
	}
}

// captureStdout runs fn and returns the bytes it wrote to os.Stdout alongside fn's error.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatalf("create stdout capture: %v", err)
	}
	saved := os.Stdout
	os.Stdout = f
	defer func() { os.Stdout = saved }()
	runErr := fn()
	if err := f.Close(); err != nil {
		t.Fatalf("close stdout capture: %v", err)
	}
	out, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("read stdout capture: %v", err)
	}
	return string(out), runErr
}
