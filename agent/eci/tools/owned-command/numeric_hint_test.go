//go:build linux

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// TestNumericHintNative compares stable leader metadata with the ordinary anchored view.
//
// Example: task-stat preserves PID, birth and parent while state may change between reads.
func TestNumericHintNative(t *testing.T) {
	kernel := linuxKernel{}
	pid := os.Getpid()
	fd, err := kernel.OpenDirectory(pid)
	if err != nil {
		t.Fatal(err)
	}
	anchored := kernel.ReadStat(fd, pid)
	if err := syscall.Close(fd); err != nil {
		t.Fatal(err)
	}
	hint, err := kernel.Hint(pid)
	if err != nil || anchored.InspectionError != nil || anchored.CloseError != nil || hint.ProcessIdentity != anchored.Stat.ProcessIdentity || hint.Parent != anchored.Stat.Parent || hint.Birth == 0 {
		t.Fatalf("hint=%+v err=%v anchored=%+v", hint, err, anchored)
	}
}

// TestNumericHintFallbackTrace isolates task availability, cleanup and fallback causes.
//
// Example: task ENOENT followed by group EIO returns only EIO, with exactly one fallback.
func TestNumericHintFallbackTrace(t *testing.T) {
	if mode := os.Getenv("GUARD_HINT_TRACE_CHILD"); mode != "" {
		pid, err := strconv.Atoi(os.Getenv("GUARD_HINT_TRACE_PID"))
		if err != nil {
			t.Fatal(err)
		}
		// strace injection ordinals are per thread; keep both weak attempts on one thread.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		hint, err := (linuxKernel{}).Hint(pid)
		switch mode {
		case "normal", "open-ENOENT", "open-ESRCH", "open-EACCES", "open-EPERM", "open-EINTR", "read-ENOENT", "read-ESRCH", "read-EACCES", "read-EPERM", "read-EINTR", "read-data-EINTR":
			if err != nil || hint.PID != pid || hint.Birth == 0 {
				t.Fatalf("expected complete hint: mode=%s hint=%+v err=%v", mode, hint, err)
			}
		case "fallback-EIO", "open-EIO", "read-EIO", "read-data-EIO":
			if !errors.Is(err, syscall.EIO) || errors.Is(err, syscall.ENOENT) || errors.Is(err, errNumericStatClose) || hint != (processStat{}) {
				t.Fatalf("unknown failure lost or polluted: mode=%s hint=%+v err=%v", mode, hint, err)
			}
		case "both-ENOENT":
			if !errors.Is(err, syscall.ENOENT) || hint != (processStat{}) {
				t.Fatalf("fallback visibility failure: hint=%+v err=%v", hint, err)
			}
		case "fallback-ESRCH", "fallback-EACCES", "fallback-EPERM":
			known := error(syscall.ESRCH)
			switch mode {
			case "fallback-EACCES":
				known = syscall.EACCES
			case "fallback-EPERM":
				known = syscall.EPERM
			}
			if !errors.Is(err, known) || errors.Is(err, syscall.ENOENT) || errors.Is(err, errNumericStatClose) || hint != (processStat{}) {
				t.Fatalf("fallback error chain polluted: mode=%s hint=%+v err=%v", mode, hint, err)
			}
		case "read-ENOENT-close-EIO", "read-EACCES-close-EIO":
			known := error(syscall.ENOENT)
			if mode == "read-EACCES-close-EIO" {
				known = syscall.EACCES
			}
			if !errors.Is(err, known) || !errors.Is(err, syscall.EIO) || !errors.Is(err, errNumericStatClose) || hint != (processStat{}) {
				t.Fatalf("task cleanup causes lost: mode=%s hint=%+v err=%v", mode, hint, err)
			}
		case "close-EIO", "fallback-close-EIO":
			if !errors.Is(err, syscall.EIO) || !errors.Is(err, errNumericStatClose) || errors.Is(err, syscall.ENOENT) || hint.PID != pid || hint.Birth == 0 {
				t.Fatalf("valid metadata cleanup failure: mode=%s hint=%+v err=%v", mode, hint, err)
			}
		case "empty":
			if err == nil || !strings.Contains(err.Error(), "malformed stat") || errors.Is(err, errNumericStatClose) || hint != (processStat{}) {
				t.Fatalf("empty record accepted: hint=%+v err=%v", hint, err)
			}
		default:
			t.Fatalf("unknown mode %q", mode)
		}
		return
	}
	if os.Getenv("GUARD_STAT_TRACE") != "1" {
		t.Skip("set GUARD_STAT_TRACE=1 for controlled task/fallback syscall traces")
	}
	pid := strconv.Itoa(os.Getpid())
	taskPath, groupPath := "/proc/"+pid+"/task/"+pid+"/stat", "/proc/"+pid+"/stat"
	// traceCase records the exact route and descriptor ownership expected from a Hint.
	//
	// Example: failed task open plus successful fallback has two opens and one close.
	type traceCase struct {
		Mode       string
		Injections []string
		TaskOpens  int
		GroupOpens int
		Closes     int
		AfterData  string
	}
	for _, scenario := range []traceCase{
		{Mode: "normal", TaskOpens: 1, Closes: 1},
		{Mode: "open-ENOENT", Injections: []string{"openat:error=ENOENT:when=1"}, TaskOpens: 1, GroupOpens: 1, Closes: 1},
		{Mode: "open-ESRCH", Injections: []string{"openat:error=ESRCH:when=1"}, TaskOpens: 1, GroupOpens: 1, Closes: 1},
		{Mode: "open-EACCES", Injections: []string{"openat:error=EACCES:when=1"}, TaskOpens: 1, GroupOpens: 1, Closes: 1},
		{Mode: "open-EPERM", Injections: []string{"openat:error=EPERM:when=1"}, TaskOpens: 1, GroupOpens: 1, Closes: 1},
		{Mode: "open-EINTR", Injections: []string{"openat:error=EINTR:when=1"}, TaskOpens: 2, Closes: 1},
		{Mode: "read-ENOENT", Injections: []string{"read:error=ENOENT:when=1"}, TaskOpens: 1, GroupOpens: 1, Closes: 2},
		{Mode: "read-ESRCH", Injections: []string{"read:error=ESRCH:when=1"}, TaskOpens: 1, GroupOpens: 1, Closes: 2},
		{Mode: "read-EACCES", Injections: []string{"read:error=EACCES:when=1"}, TaskOpens: 1, GroupOpens: 1, Closes: 2},
		{Mode: "read-EPERM", Injections: []string{"read:error=EPERM:when=1"}, TaskOpens: 1, GroupOpens: 1, Closes: 2},
		{Mode: "read-EINTR", Injections: []string{"read:error=EINTR:when=1"}, TaskOpens: 1, Closes: 1},
		{Mode: "read-data-EINTR", Injections: []string{"read:error=EINTR:when=2"}, TaskOpens: 1, Closes: 1, AfterData: "EINTR"},
		{Mode: "read-data-EIO", Injections: []string{"read:error=EIO:when=2"}, TaskOpens: 1, Closes: 1, AfterData: "EIO"},
		{Mode: "fallback-EIO", Injections: []string{"openat:error=ENOENT:when=1", "read:error=EIO:when=1"}, TaskOpens: 1, GroupOpens: 1, Closes: 1},
		{Mode: "fallback-ESRCH", Injections: []string{"openat:error=ENOENT:when=1", "read:error=ESRCH:when=1"}, TaskOpens: 1, GroupOpens: 1, Closes: 1},
		{Mode: "fallback-EACCES", Injections: []string{"openat:error=ENOENT:when=1", "read:error=EACCES:when=1"}, TaskOpens: 1, GroupOpens: 1, Closes: 1},
		{Mode: "fallback-EPERM", Injections: []string{"openat:error=ENOENT:when=1", "read:error=EPERM:when=1"}, TaskOpens: 1, GroupOpens: 1, Closes: 1},
		{Mode: "both-ENOENT", Injections: []string{"openat:error=ENOENT:when=1+"}, TaskOpens: 1, GroupOpens: 1},
		{Mode: "read-ENOENT-close-EIO", Injections: []string{"read:error=ENOENT:when=1", "close:error=EIO:when=1"}, TaskOpens: 1, Closes: 1},
		{Mode: "read-EACCES-close-EIO", Injections: []string{"read:error=EACCES:when=1", "close:error=EIO:when=1"}, TaskOpens: 1, Closes: 1},
		{Mode: "close-EIO", Injections: []string{"close:error=EIO:when=1"}, TaskOpens: 1, Closes: 1},
		{Mode: "fallback-close-EIO", Injections: []string{"openat:error=ENOENT:when=1", "close:error=EIO:when=1"}, TaskOpens: 1, GroupOpens: 1, Closes: 1},
		{Mode: "open-EIO", Injections: []string{"openat:error=EIO:when=1"}, TaskOpens: 1},
		{Mode: "read-EIO", Injections: []string{"read:error=EIO:when=1"}, TaskOpens: 1, Closes: 1},
		{Mode: "empty", Injections: []string{"read:retval=0:when=1"}, TaskOpens: 1, Closes: 1},
	} {
		tracePath := filepath.Join(t.TempDir(), "hint.trace")
		args := []string{"-f", "-qq", "-yy", "-s", "4096", "-P", taskPath, "-P", groupPath, "-o", tracePath, "-e", "trace=openat,read,close"}
		for _, injection := range scenario.Injections {
			args = append(args, "-e", "inject="+injection)
		}
		args = append(args, os.Args[0], "-test.run=^TestNumericHintFallbackTrace$", "-test.count=1")
		cmd := exec.Command("strace", args...)
		cmd.Env = append(os.Environ(), "GUARD_HINT_TRACE_CHILD="+scenario.Mode, "GUARD_HINT_TRACE_PID="+pid)
		output, runErr := cmd.CombinedOutput()
		trace, readErr := os.ReadFile(tracePath)
		if runErr != nil || readErr != nil {
			t.Errorf("%s: run=%v read=%v output=%s trace=%s", scenario.Mode, runErr, readErr, output, trace)
		}
		taskOpens, groupOpens, closes := 0, 0, 0
		taskReads, positiveReads, injectedRead := 0, 0, 0
		eofAfterInjection := false
		for _, line := range strings.Split(string(trace), "\n") {
			if strings.Contains(line, "read(") && strings.Contains(line, "<"+taskPath+">") {
				taskReads++
				if strings.Contains(line, "(INJECTED)") {
					if positiveReads != 0 && strings.Contains(line, "= -1 "+scenario.AfterData+" (") {
						injectedRead = taskReads
					}
				}
				if at := strings.LastIndex(line, " = "); !strings.Contains(line, "(INJECTED)") && at >= 0 {
					result, err := strconv.Atoi(strings.TrimSpace(line[at+3:]))
					if err == nil && result > 0 && injectedRead == 0 {
						positiveReads++
					}
					if err == nil && result == 0 && injectedRead != 0 {
						eofAfterInjection = true
					}
				}
			}
			switch {
			case strings.Contains(line, "openat(") && strings.Contains(line, `"`+taskPath+`"`):
				taskOpens++
			case strings.Contains(line, "openat(") && strings.Contains(line, `"`+groupPath+`"`):
				groupOpens++
			case strings.Contains(line, "close(") && strings.Contains(line, "/stat>"):
				closes++
			}
		}
		if taskOpens != scenario.TaskOpens || groupOpens != scenario.GroupOpens || closes != scenario.Closes || (len(scenario.Injections) != 0 && !strings.Contains(string(trace), "(INJECTED)")) {
			t.Errorf("%s: task opens=%d wanted=%d group opens=%d wanted=%d closes=%d wanted=%d trace=%s", scenario.Mode, taskOpens, scenario.TaskOpens, groupOpens, scenario.GroupOpens, closes, scenario.Closes, trace)
		}
		if scenario.AfterData != "" {
			wantedReads := 2
			if scenario.AfterData == "EINTR" {
				wantedReads = 3
			}
			if positiveReads != 1 || injectedRead != 2 || taskReads != wantedReads || eofAfterInjection != (scenario.AfterData == "EINTR") {
				t.Errorf("%s after-data proof: positive=%d injected read=%d reads=%d EOF=%t trace=%s", scenario.Mode, positiveReads, injectedRead, taskReads, eofAfterInjection, trace)
			}
			t.Logf("%s positive=%d injected read=%d reads=%d EOF=%t", scenario.Mode, positiveReads, injectedRead, taskReads, eofAfterInjection)
		}
		t.Logf("%s task opens=%d group opens=%d close attempts=%d", scenario.Mode, taskOpens, groupOpens, closes)
	}
}
