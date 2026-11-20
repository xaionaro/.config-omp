package main

import (
	"os"
	"testing"
)

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
