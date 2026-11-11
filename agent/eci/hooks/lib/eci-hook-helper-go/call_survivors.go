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

// toolCallIDVar is the environment entry prefix omp puts on every Bash child.
//
// Example: "PI_TOOL_CALL_ID=toolu_01AbC".
const toolCallIDVar = "PI_TOOL_CALL_ID="

// maxNoticePIDs bounds the PIDs named in a notice.
//
// Example: 25 survivors show 20 PIDs and "(+5 more)".
const maxNoticePIDs = 20

// ToolCallID is the omp tool call identifier exported to Bash children as PI_TOOL_CALL_ID.
//
// Example: ToolCallID("toolu_01AbC").
type ToolCallID string

// ProcessID is a Linux process ID.
//
// Example: ProcessID(4242).
type ProcessID int

// runCallSurvivors implements `call-survivors <tool-call-id>...`, printing one notice for all IDs or nothing.
//
// Example: `eci-hook-helper call-survivors toolu_01AbC toolu_01XyZ`.
func runCallSurvivors(
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if len(args) == 0 || slices.Contains(args, "") {
		return statusRejected
	}
	ids := make([]ToolCallID, len(args))
	for i, arg := range args {
		ids[i] = ToolCallID(arg)
	}
	pids, err := callSurvivors("/proc", ids, ProcessID(os.Getpid()))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return statusFailed
	}
	if _, err := io.WriteString(stdout, survivorNotice(pids)); err != nil {
		return statusFailed
	}
	return statusOK
}

// callSurvivors returns sorted PIDs under procRoot tagged with any of ids, excluding self; a missing procRoot gives nil, nil.
//
// Example: callSurvivors("/proc", []ToolCallID{"toolu_01AbC"}, 99) returns []ProcessID{4242}.
func callSurvivors(
	procRoot string,
	ids []ToolCallID,
	self ProcessID,
) ([]ProcessID, error) {
	entries, err := os.ReadDir(procRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	want := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		want[toolCallIDVar+string(id)] = struct{}{}
	}
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
		if taggedWithAny(environ, want) {
			pids = append(pids, ProcessID(n))
		}
	}
	slices.Sort(pids)
	return pids, nil
}

// taggedWithAny reports whether a NUL-separated environ block holds an entry exactly equal to a key of want.
//
// Example: taggedWithAny([]byte("A=1\x00PI_TOOL_CALL_ID=x\x00"), {"PI_TOOL_CALL_ID=x"}) is true; "PI_TOOL_CALL_ID=xy" is not.
func taggedWithAny(
	environ []byte,
	want map[string]struct{},
) bool {
	for field := range bytes.SplitSeq(environ, []byte{0}) {
		if _, ok := want[string(field)]; ok {
			return true
		}
	}
	return false
}

// survivorNotice renders the fixed advisory for pids, or "" when there are none.
//
// Example: survivorNotice([]ProcessID{123}) begins "Processes still running after their Bash call finished: PIDs 123.".
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
	return fmt.Sprintf("Processes still running after their Bash call finished: PIDs %s%s. Inspect: `ps -o pid,etime,args -p %s`. Per the Processes rule, stop them before you finish unless the user or your parent agent needs them; leave shared on-demand servers (adb, gpg-agent, ssh ControlPersist masters) running.",
		strings.Join(text, ", "), more, strings.Join(text, ","))
}
