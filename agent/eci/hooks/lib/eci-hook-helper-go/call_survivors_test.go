package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startTagged starts a detached `sleep 30` whose environment carries id.
func startTagged(t *testing.T, id string) ProcessID {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	cmd.Env = append(os.Environ(), "PI_TOOL_CALL_ID="+id)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return ProcessID(cmd.Process.Pid)
}

func uniqueID(t *testing.T) ToolCallID {
	return ToolCallID(fmt.Sprintf("test-%s-%d", t.Name(), time.Now().UnixNano()))
}

func TestCallSurvivorsListsTaggedProcess(t *testing.T) {
	if _, err := os.Stat("/proc/self/environ"); err != nil {
		t.Skip("needs Linux /proc")
	}
	id := uniqueID(t)
	pid := startTagged(t, string(id))
	other := startTagged(t, string(id)+"x")

	got, err := callSurvivors("/proc", id, ProcessID(os.Getpid()))
	require.NoError(t, err)
	assert.Equal(t, []ProcessID{pid}, got)
	assert.NotContains(t, got, other)

	got, err = callSurvivors("/proc", id, pid)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestCallSurvivorsFakeRoot(t *testing.T) {
	got, err := callSurvivors(filepath.Join(t.TempDir(), "missing"), "a", 0)
	require.NoError(t, err)
	assert.Nil(t, got)

	root := t.TempDir()
	write := func(name, environ string) {
		require.NoError(t, os.MkdirAll(filepath.Join(root, name), 0o755))
		if environ != "" {
			require.NoError(t, os.WriteFile(filepath.Join(root, name, "environ"), []byte(environ), 0o600))
		}
	}
	write("self", "PI_TOOL_CALL_ID=a\x00")
	write("12", "")
	write("30", "X=1\x00PI_TOOL_CALL_ID=a\x00")
	write("7", "PI_TOOL_CALL_ID=a\x00")
	write("8", "PI_TOOL_CALL_ID=ab\x00")
	got, err = callSurvivors(root, "a", 0)
	require.NoError(t, err)
	assert.Equal(t, []ProcessID{7, 30}, got)
}

func TestSurvivorNotice(t *testing.T) {
	assert.Equal(t, "", survivorNotice(nil))
	assert.Equal(t, "This Bash call left processes running: PIDs 123. Inspect: `ps -o pid,etime,args -p 123`. Per the Processes rule, stop them before you finish unless the user or your parent agent needs them kept running.", survivorNotice([]ProcessID{123}))

	var pids []ProcessID
	var shown []string
	for i := 1; i <= 25; i++ {
		pids = append(pids, ProcessID(i))
		if i <= 20 {
			shown = append(shown, fmt.Sprint(i))
		}
	}
	notice := survivorNotice(pids)
	assert.Contains(t, notice, "PIDs "+strings.Join(shown, ", ")+" (+5 more).")
	assert.Contains(t, notice, "-p "+strings.Join(shown, ",")+"`")
}

func TestCallSurvivorsModeArgs(t *testing.T) {
	for _, args := range [][]string{{"call-survivors"}, {"call-survivors", ""}, {"call-survivors", "a", "b"}} {
		var stdout, stderr bytes.Buffer
		assert.Equal(t, statusRejected, run(args, nil, &stdout, &stderr), args)
	}
	var stdout, stderr bytes.Buffer
	assert.Equal(t, statusOK, run([]string{"call-survivors", "no-such-call-id-" + fmt.Sprint(time.Now().UnixNano())}, nil, &stdout, &stderr))
	assert.Empty(t, stdout.String())
}
