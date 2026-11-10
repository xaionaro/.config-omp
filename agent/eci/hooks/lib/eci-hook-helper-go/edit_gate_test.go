package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// editGateFixture is a stub dispatcher plus the log of callbacks it received.
//
// Example: newEditGateFixture(t, "exit 17") for a failing dispatcher.
type editGateFixture struct {
	bash   string
	script string
	log    string
}

// newEditGateFixture installs body as the dispatcher. The stub appends each
// callback to the log before running body with the callback in $callback and
// its target in $target.
//
// Example: newEditGateFixture(t, `printf '%s\n' ECI_EDIT_GATE_TARGET_UNCLASSIFIED >&2`).
func newEditGateFixture(
	t *testing.T,
	body string,
) editGateFixture {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is unavailable")
	}
	dir := t.TempDir()
	fixture := editGateFixture{bash: bash, script: filepath.Join(dir, "dispatch.sh"), log: filepath.Join(dir, "calls")}
	stub := "callback=$(cat)\nprintf '%s\\n' \"$callback\" >>'" + fixture.log + "'\ntarget=$(printf '%s' \"$callback\" | jq -r .tool_input.file_path)\n" + body + "\n"
	require.NoError(t, os.WriteFile(fixture.script, []byte(stub), 0o700))
	return fixture
}

// run sends request to the edit-gate mode and returns its status, stdout and stderr.
//
// Example: status, stdout, _ := fixture.run(t, editRequest("a", "/repo/a")).
func (f editGateFixture) run(
	t *testing.T,
	request any,
) (int, string, string) {
	t.Helper()
	input, ok := request.(string)
	if !ok {
		encoded, err := json.Marshal(request)
		require.NoError(t, err)
		input = string(encoded)
	}
	var stdout, stderr bytes.Buffer
	status := run([]string{"edit-gate", f.bash, f.script}, strings.NewReader(input), &stdout, &stderr)
	return status, stdout.String(), stderr.String()
}

// calls returns the callbacks the stub received, in order.
//
// Example: len(fixture.calls(t)) counts dispatcher runs.
func (f editGateFixture) calls(t *testing.T) []dispatchCallback {
	t.Helper()
	data, err := os.ReadFile(f.log)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var calls []dispatchCallback
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var call dispatchCallback
		require.NoError(t, json.Unmarshal([]byte(line), &call), "callback %q", line)
		calls = append(calls, call)
	}
	return calls
}

// editRequest builds a Write request for targets given as path/file_path pairs.
//
// Example: editRequest(":~/a", "/home/u/a", "b", "/repo/b").
func editRequest(pairs ...string) editGateRequest {
	request := editGateRequest{SessionID: "host-session", TranscriptPath: "/t.jsonl", AgentKind: "sub", Cwd: "/repo", HookEventName: hookEventPreToolUse, ToolName: editGateToolWrite}
	for i := 0; i+1 < len(pairs); i += 2 {
		request.Targets = append(request.Targets, editTarget{Path: pairs[i], FilePath: pairs[i+1]})
	}
	return request
}

// decodeDecision parses the gate's stdout envelope.
//
// Example: decodeDecision(t, stdout).PermissionDecision.
func decodeDecision(
	t *testing.T,
	stdout string,
) hookSpecificOutput {
	t.Helper()
	var decision hookDecision
	require.NoError(t, json.Unmarshal([]byte(stdout), &decision), "decision %q", stdout)
	require.Equal(t, hookEventPreToolUse, decision.HookSpecificOutput.HookEventName)
	return decision.HookSpecificOutput
}

func TestEditGateDispatchesDistinctNormalizedTargetsInOrder(t *testing.T) {
	fixture := newEditGateFixture(t, "")
	status, stdout, stderr := fixture.run(t, editRequest(":~/a", "/home/u/a", "b", "/repo/b", "~/a", "/home/u/a"))
	require.Equal(t, statusOK, status)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
	calls := fixture.calls(t)
	require.Len(t, calls, 2)
	assert.Equal(t, dispatchCallback{SessionID: "host-session", TranscriptPath: "/t.jsonl", AgentKind: "sub", Cwd: "/repo", HookEventName: hookEventPreToolUse, ToolName: editGateToolWrite, ToolInput: dispatchToolInput{FilePath: "/home/u/a"}}, calls[0])
	assert.Equal(t, "/repo/b", calls[1].ToolInput.FilePath)

	data, err := os.ReadFile(fixture.log)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"tool_name":"Write"`)
}

func TestEditGateFirstDenialWinsAndNamesRequestedSpelling(t *testing.T) {
	fixture := newEditGateFixture(t, `case "$target" in
/protected*) printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"protected target"}}' ;;
*) printf '%s\n' 'ECI_EDIT_GATE_FAILED_ALLOWED eci-active-gate.sh' >&2 ;;
esac`)
	status, stdout, _ := fixture.run(t, editRequest("ordinary", "/repo/ordinary", ":/protected/one", "/protected/one", "/protected/two", "/protected/two"))
	require.Equal(t, statusOK, status)
	decision := decodeDecision(t, stdout)
	assert.Equal(t, decisionDeny, decision.PermissionDecision)
	assert.Empty(t, decision.AdditionalContext)
	assert.Equal(t, `protected target (requested as ":/protected/one", checked as "/protected/one")`, decision.PermissionDecisionReason)
	assert.Len(t, fixture.calls(t), 2, "a later target ran after the denial")
}

func TestEditGateReportsExactFailedGuardMarkersOnce(t *testing.T) {
	t.Setenv("ECI_HOOK_IS_SUBAGENT", "1")
	fixture := newEditGateFixture(t, `printf '%s\n' 'ECI_EDIT_GATE_FAILED_ALLOWED validate-edit-write.sh' \
  'ECI_EDIT_GATE_FAILED_ALLOWED validate-edit-write.sh extra' 'ECI_EDIT_GATE_FAILED_ALLOWED validate-apply-patch.sh' \
  'ECI_EDIT_GATE_FAILED_ALLOWED ../x' ' ECI_EDIT_GATE_TARGET_UNCLASSIFIED' 'untrusted payload' >&2`)
	request := editRequest("a", "/repo/a", "b", "/repo/b")
	request.SessionID = "it's"
	status, stdout, _ := fixture.run(t, request)
	require.Equal(t, statusOK, status)
	decision := decodeDecision(t, stdout)
	context := decision.AdditionalContext
	assert.Equal(t, decisionAllow, decision.PermissionDecision)
	assert.Equal(t, 1, strings.Count(context, "ECI edit guard "))
	for _, unwanted := range []string{"untrusted payload", "../x", "validate-apply-patch", "extra", "could not classify"} {
		assert.NotContains(t, context, unwanted)
	}
	callback, err := json.Marshal(request.callback(request.Targets[0]))
	require.NoError(t, err)
	command := "printf '%s\\n' " + shellQuote(string(callback)) + ` | ECI_SESSION_ID='it'\''s' ECI_THREAD_ID='it'\''s' ECI_HOOK_IS_SUBAGENT='1' bash -x "$HOME/.omp/agent/eci/hooks/validate-edit-write.sh"`
	assert.Contains(t, context, command)
	assert.Contains(t, context, `"/repo/a"`)
	assert.Contains(t, context, "same session")
}

func TestEditGateReportsUnclassifiedTargets(t *testing.T) {
	fixture := newEditGateFixture(t, `printf '%s\n' ECI_EDIT_GATE_TARGET_UNCLASSIFIED >&2`)
	status, stdout, _ := fixture.run(t, editRequest("a", "/repo/a"))
	require.Equal(t, statusOK, status)
	decision := decodeDecision(t, stdout)
	assert.Equal(t, decisionAllow, decision.PermissionDecision)
	assert.Contains(t, decision.AdditionalContext, `could not classify the allowed target "/repo/a"`)
	assert.Contains(t, decision.AdditionalContext, "coverage is unknown")
}

func TestEditGateFailsClosedOnDispatcherFailure(t *testing.T) {
	cases := map[string]struct {
		body string
		want string
	}{
		"non-zero exit":   {"printf '%s\\n' 'guard crashed' >&2; exit 17", "ECI hook pretooluse-edit-dispatch exited 17: guard crashed; blocking (fail-closed)."},
		"non-JSON":        {"printf '%s\\n' 'not json'", "ECI hook pretooluse-edit-dispatch returned non-JSON output; blocking (fail-closed)."},
		"wrong event":     {`printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PostToolUse","permissionDecision":"deny"}}'`, "ECI hook pretooluse-edit-dispatch returned unrecognized output; blocking (fail-closed)."},
		"wrong type":      {`printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":1}}'`, "ECI hook pretooluse-edit-dispatch returned unrecognized output; blocking (fail-closed)."},
		"array":           {`printf '%s\n' '[]'`, "ECI hook pretooluse-edit-dispatch returned unrecognized output; blocking (fail-closed)."},
		"bad decision":    {`printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask"}}'`, "ECI hook pretooluse-edit-dispatch returned unsupported permissionDecision; blocking (fail-closed)."},
		"oversized":       {"head -c 70000 /dev/zero | tr '\\0' ' '; printf x", "ECI hook pretooluse-edit-dispatch returned more than 65536 bytes of output; blocking (fail-closed)."},
		"reasonless deny": {`printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny"}}'`, "denied by ECI hook pretooluse-edit-dispatch"},
		"killed":          {"kill -9 $$", "ECI hook pretooluse-edit-dispatch was ended by a signal; blocking (fail-closed)."},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fixture := newEditGateFixture(t, tc.body)
			status, stdout, _ := fixture.run(t, editRequest("/repo/a", "/repo/a"))
			require.Equal(t, statusOK, status)
			decision := decodeDecision(t, stdout)
			assert.Equal(t, decisionDeny, decision.PermissionDecision)
			assert.Equal(t, tc.want, decision.PermissionDecisionReason)
		})
	}
}

func TestEditGateBoundsDrainAfterDispatcherExit(t *testing.T) {
	cases := map[string]struct {
		body     string
		decision string
	}{
		"allow":    {"sleep 5 &", ""},
		"deny":     {`sleep 5 & printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"held"}}'`, decisionDeny},
		"non-zero": {"sleep 5 & exit 3", decisionDeny},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fixture := newEditGateFixture(t, tc.body)
			started := time.Now()
			status, stdout, _ := fixture.run(t, editRequest("/repo/a", "/repo/a"))
			elapsed := time.Since(started)
			require.Equal(t, statusOK, status)
			assert.Less(t, elapsed, dispatchDrainDelay+2*time.Second, "a held output pipe delayed the decision")
			if tc.decision == "" {
				assert.Empty(t, stdout)
				return
			}
			assert.Equal(t, tc.decision, decodeDecision(t, stdout).PermissionDecision)
		})
	}
}

func TestEditGateAllowDecisionKeepsChecking(t *testing.T) {
	fixture := newEditGateFixture(t, `printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"fine"}}'`)
	status, stdout, _ := fixture.run(t, editRequest("a", "/repo/a", "b", "/repo/b"))
	require.Equal(t, statusOK, status)
	assert.Empty(t, stdout)
	assert.Len(t, fixture.calls(t), 2)
}

func TestEditGateFailsClosedWhenDispatcherCannotStart(t *testing.T) {
	var stdout, stderr bytes.Buffer
	request, err := json.Marshal(editRequest("a", "/repo/a"))
	require.NoError(t, err)
	status := run([]string{"edit-gate", filepath.Join(t.TempDir(), "missing-bash"), "dispatch.sh"}, bytes.NewReader(request), &stdout, &stderr)
	require.Equal(t, statusOK, status)
	decision := decodeDecision(t, stdout.String())
	assert.Equal(t, decisionDeny, decision.PermissionDecision)
	assert.True(t, strings.HasPrefix(decision.PermissionDecisionReason, "ECI hook pretooluse-edit-dispatch could not run ("), decision.PermissionDecisionReason)
}

func TestEditGateRejectsMalformedRequests(t *testing.T) {
	fixture := newEditGateFixture(t, "")
	valid := editRequest("a", "/repo/a")
	wrongEvent := valid
	wrongEvent.HookEventName = "PostToolUse"
	noTargets := valid
	noTargets.Targets = nil
	cases := map[string]any{
		"non-JSON":      "not json",
		"array":         "[]",
		"unknown field": `{"session_id":"s","cwd":"/","hook_event_name":"PreToolUse","tool_name":"Write","targets":[{"path":"a","file_path":"/a"}],"tool_input":{}}`,
		"trailing":      `{"session_id":"s","cwd":"/","hook_event_name":"PreToolUse","tool_name":"Write","targets":[{"path":"a","file_path":"/a"}]} {}`,
		"unknown tool":  `{"session_id":"s","cwd":"/","hook_event_name":"PreToolUse","tool_name":"Bash","targets":[{"path":"a","file_path":"/a"}]}`,
		"missing tool":  `{"session_id":"s","cwd":"/","hook_event_name":"PreToolUse","targets":[{"path":"a","file_path":"/a"}]}`,
		"wrong event":   wrongEvent,
		"no targets":    noTargets,
		"empty target":  editRequest("a", ""),
		"oversized":     strings.Repeat(" ", inputBudget+1),
		"NUL":           "{\x00}",
	}
	for name, request := range cases {
		t.Run(name, func(t *testing.T) {
			status, stdout, stderr := fixture.run(t, request)
			assert.Equal(t, statusRejected, status)
			assert.Empty(t, stdout)
			assert.True(t, strings.HasPrefix(stderr, "eci-hook-helper edit-gate: "), stderr)
		})
	}
	assert.Empty(t, fixture.calls(t), "a rejected request ran the dispatcher")
	for _, args := range [][]string{{"edit-gate"}, {"edit-gate", fixture.bash}, {"edit-gate", "", fixture.script}, {"edit-gate", fixture.bash, fixture.script, "extra"}} {
		status, _ := runMode(t, args, "")
		assert.Equal(t, statusRejected, status, "args %v", args)
	}
}

func TestEditGateToolJSON(t *testing.T) {
	for tool, name := range map[editGateTool]string{editGateToolEdit: `"Edit"`, editGateToolWrite: `"Write"`} {
		encoded, err := json.Marshal(tool)
		require.NoError(t, err)
		assert.Equal(t, name, string(encoded))
		var decoded editGateTool
		require.NoError(t, json.Unmarshal(encoded, &decoded))
		assert.Equal(t, tool, decoded)
	}
	_, err := json.Marshal(editGateToolUnknown)
	assert.Error(t, err)
}
