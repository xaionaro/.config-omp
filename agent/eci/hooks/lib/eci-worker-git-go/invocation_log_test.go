package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestInvocationLogInventoriesEveryRoute checks metadata before validation without recording payloads.
//
// Example: accepted and rejected native calls each produce one run-once row.
func TestInvocationLogInventoriesEveryRoute(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GIT_LOG_SECRET", "fake-secret-environment")
	root := fixtureRepository(t)
	requests := [][]string{
		{"--repo", root, "stage-content", "--", "file.txt"},
		{"--repo", root, "run-once", "--reason", "fake-secret-reason", "--user-authorized", "--", "status", "--short"},
		{"fake-secret-argument"},
		{"--repo", root, "run-once"},
		nil,
	}
	for index, arguments := range requests {
		code := Run(arguments, Streams{Output: io.Discard, Error: io.Discard})
		if (index < 2 && code != 0) || (index >= 2 && code == 0) {
			t.Fatalf("request %d returned %d", index, code)
		}
	}
	path := filepath.Join(home, ".cache", "eci", "eci-worker-git.jsonl")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "fake-secret") || strings.Contains(string(content), root) {
		t.Fatalf("log contains command payload: %s", content)
	}
	rows := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	if len(rows) != len(requests) || !bytes.HasSuffix(content, []byte("\n")) {
		t.Fatalf("wrong complete row count: %q", content)
	}
	for index, row := range rows {
		var event map[string]json.RawMessage
		if err := json.Unmarshal([]byte(row), &event); err != nil {
			t.Fatal(err)
		}
		if len(event) != 4 || string(event["pid"]) != strconv.Itoa(os.Getpid()) || string(event["argc"]) != strconv.Itoa(len(requests[index])) {
			t.Fatalf("wrong metadata: %s", row)
		}
		var timestamp, route string
		if err := json.Unmarshal(event["timestamp"], &timestamp); err != nil {
			t.Fatal(err)
		}
		parsed, err := time.Parse(time.RFC3339Nano, timestamp)
		if err != nil || !strings.HasSuffix(timestamp, "Z") || parsed.IsZero() {
			t.Fatalf("invalid UTC timestamp: %q: %v", timestamp, err)
		}
		if err := json.Unmarshal(event["route"], &route); err != nil {
			t.Fatal(err)
		}
		want := "typed-or-invalid"
		if index == 1 || index == 3 {
			want = "run-once"
		}
		if route != want {
			t.Fatalf("route %q, want %q", route, want)
		}
	}
	for _, entry := range []struct {
		Path string
		Mode os.FileMode
	}{{filepath.Dir(path), 0700}, {path, 0600}} {
		info, err := os.Stat(entry.Path)
		if err != nil || info.Mode().Perm() != entry.Mode {
			t.Fatalf("wrong created permissions for %s: %v", entry.Path, err)
		}
	}
}

// TestInvocationLogUnavailablePreservesExecution checks special targets and failed warning output.
//
// Example: a FIFO never blocks the CLI or prevents an acknowledged alias from executing once.
func TestInvocationLogUnavailablePreservesExecution(t *testing.T) {
	for _, kind := range []string{"directory", "fifo", "symlink", "warning-sink"} {
		// Each storage failure must preserve validation and the native exit status.
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			path := filepath.Join(home, ".cache", "eci", "eci-worker-git.jsonl")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "fifo":
				if err := syscall.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(filepath.Join(home, "absent"), path); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			root := fixtureRepository(t)
			marker := filepath.Join(home, "runs")
			t.Setenv("GIT_LOG_RUN_MARKER", marker)
			arguments := []string{"--repo", root, "run-once", "--reason", "approved fixture", "--user-authorized", "--", "-c", `alias.logged=!printf x >> "$GIT_LOG_RUN_MARKER"; exit 37`, "logged"}
			var output, warning bytes.Buffer
			var sink io.Writer = &warning
			if kind == "warning-sink" {
				sink = invocationWarningFailure{}
			}
			if code := Run(arguments, Streams{Output: &output, Error: sink}); code != 37 {
				t.Fatalf("logging failure changed native exit: %d", code)
			}
			if content, err := os.ReadFile(marker); err != nil || string(content) != "x" {
				t.Fatalf("native invocation count changed: %q: %v", content, err)
			}
			if kind != "warning-sink" && warning.Len() == 0 {
				t.Fatalf("missing storage warning: %q", warning.String())
			}
			if code := Run(arguments[:5], Streams{Output: &output, Error: sink}); code == 0 {
				t.Fatal("logging failure waived authorization")
			}
			if content, err := os.ReadFile(marker); err != nil || string(content) != "x" {
				t.Fatalf("invalid request executed: %q: %v", content, err)
			}
		})
	}
}

// invocationWarningFailure models an unavailable diagnostic sink.
//
// Example: logging cannot turn a failed warning write into a changed Git exit.
type invocationWarningFailure struct{}

// Write rejects diagnostic bytes without accepting partial output.
//
// Example: an unavailable stderr returns a closed-pipe error.
func (invocationWarningFailure) Write(content []byte) (int, error) {
	return 0, io.ErrClosedPipe
}

// TestInvocationLogSubprocess appends one row from a separate process in the concurrency fixture.
//
// Example: the parent launches multiple children sharing one append-only log.
func TestInvocationLogSubprocess(t *testing.T) {
	if os.Getenv("ECI_LOG_TEST_CHILD") != "1" {
		return
	}
	if code := Run(nil, Streams{Output: io.Discard, Error: io.Discard}); code != 1 {
		os.Exit(2)
	}
	os.Exit(0)
}

// TestInvocationLogConcurrentProcesses checks complete records across concurrent callers.
//
// Example: eight simultaneous rejected invocations append exactly eight JSON objects.
func TestInvocationLogConcurrentProcesses(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var commands []*exec.Cmd
	for range 8 {
		command := exec.Command(executable, "-test.run=^TestInvocationLogSubprocess$")
		command.Env = append(os.Environ(), "ECI_LOG_TEST_CHILD=1")
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, command)
	}
	for _, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	content, err := os.ReadFile(filepath.Join(home, ".cache", "eci", "eci-worker-git.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	if len(rows) != 8 {
		t.Fatalf("wrong concurrent record count: %q", content)
	}
	seen := make(map[string]bool)
	for _, row := range rows {
		var event map[string]json.RawMessage
		if err := json.Unmarshal([]byte(row), &event); err != nil {
			t.Fatal(err)
		}
		pid := string(event["pid"])
		if len(event) != 4 || seen[pid] || string(event["argc"]) != "0" || string(event["route"]) != `"typed-or-invalid"` {
			t.Fatalf("incomplete or duplicate concurrent row: %s", row)
		}
		seen[pid] = true
	}
}
