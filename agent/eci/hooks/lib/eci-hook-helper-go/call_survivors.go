package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

// ToolCallID is the omp tool call identifier exported to Bash children as
// PI_TOOL_CALL_ID, e.g. "toolu_01AbC".
type ToolCallID string

// ProcessID is a Linux process ID, e.g. 4242.
type ProcessID int

// maxNoticePIDs bounds the PIDs named in a notice, e.g. 25 survivors show 20.
const maxNoticePIDs = 20

// runCallSurvivors implements `call-survivors <tool-call-id>`, e.g.
// `eci-hook-helper call-survivors toolu_01AbC` prints a notice or nothing.
func runCallSurvivors(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] == "" {
		return statusRejected
	}
	pids, err := callSurvivors("/proc", ToolCallID(args[0]), ProcessID(os.Getpid()))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return statusFailed
	}
	if _, err := io.WriteString(stdout, survivorNotice(pids)); err != nil {
		return statusFailed
	}
	return statusOK
}

// callSurvivors returns sorted PIDs under procRoot whose environment holds
// exactly PI_TOOL_CALL_ID=<id>, excluding self, e.g. callSurvivors("/proc",
// "toolu_01AbC", 99) returns [4242]. A missing procRoot returns nil, nil.
func callSurvivors(procRoot string, id ToolCallID, self ProcessID) ([]ProcessID, error) {
	entries, err := os.ReadDir(procRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	want := []byte("PI_TOOL_CALL_ID=" + string(id))
	var pids []ProcessID
	for _, entry := range entries {
		n, err := strconv.Atoi(entry.Name())
		if err != nil || n <= 0 || ProcessID(n) == self {
			continue
		}
		environ, err := os.ReadFile(filepath.Join(procRoot, entry.Name(), "environ"))
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.ESRCH) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for field := range bytes.SplitSeq(environ, []byte{0}) {
			if bytes.Equal(field, want) {
				pids = append(pids, ProcessID(n))
				break
			}
		}
	}
	slices.Sort(pids)
	return pids, nil
}

// survivorNotice renders the fixed advisory for pids, e.g. [123] gives
// "This Bash call left processes running: PIDs 123. Inspect: ...". No pids
// give "".
func survivorNotice(pids []ProcessID) string {
	if len(pids) == 0 {
		return ""
	}
	shown := pids[:min(len(pids), maxNoticePIDs)]
	text := make([]string, len(shown))
	for i, pid := range shown {
		text[i] = strconv.Itoa(int(pid))
	}
	more := ""
	if extra := len(pids) - len(shown); extra > 0 {
		more = fmt.Sprintf(" (+%d more)", extra)
	}
	return fmt.Sprintf("This Bash call left processes running: PIDs %s%s. Inspect: `ps -o pid,etime,args -p %s`. Per the Processes rule, stop them before you finish unless the user or your parent agent needs them kept running.",
		strings.Join(text, ", "), more, strings.Join(text, ","))
}
