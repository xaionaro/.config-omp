package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// LogInvocation appends call metadata without command arguments or environment payloads.
//
// Example: a rejected native request records its UTC timestamp, PID, argument count and route.
func LogInvocation(
	argc int,
	native bool,
) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, ".cache", "eci", "eci-worker-git.jsonl")
	info, err := os.Lstat(path)
	switch {
	case err == nil:
		if !info.Mode().IsRegular() {
			return fmt.Errorf("invocation log target is not a regular file")
		}
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	route := "typed-or-invalid"
	if native {
		route = "run-once"
	}
	record, err := json.Marshal(struct {
		Timestamp string `json:"timestamp"`
		PID       int    `json:"pid"`
		Argc      int    `json:"argc"`
		Route     string `json:"route"`
	}{Timestamp: time.Now().UTC().Format(time.RFC3339Nano), PID: os.Getpid(), Argc: argc, Route: route})
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	record = append(record, '\n')
	written, writeErr := file.Write(record)
	if written != len(record) {
		writeErr = errors.Join(writeErr, io.ErrShortWrite)
	}
	return errors.Join(writeErr, file.Close())
}

// warnInvocationLogUnavailable reports a best-effort diagnostic without changing command behavior.
//
// Example: even a failed stderr write leaves native execution and its exit status unchanged.
func warnInvocationLogUnavailable(output io.Writer) {
	if _, err := fmt.Fprintln(output, "eci-worker-git: invocation log unavailable"); err != nil {
		return
	}
}
