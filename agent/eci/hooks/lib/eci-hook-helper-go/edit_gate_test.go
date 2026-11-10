package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// editGateFixture is a stub dispatcher plus the log of callbacks it received.
type editGateFixture struct {
	bash   string
	script string
	log    string
}

// newEditGateFixture installs body as the dispatcher. The stub appends each
// callback to the log before running body with the callback in $callback and
// its target in $target.
func newEditGateFixture(t *testing.T, body string) editGateFixture {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is unavailable")
	}
	dir := t.TempDir()
	fixture := editGateFixture{bash: bash, script: filepath.Join(dir, "dispatch.sh"), log: filepath.Join(dir, "calls")}
	stub := "callback=$(cat)\nprintf '%s\\n' \"$callback\" >>'" + fixture.log + "'\ntarget=$(printf '%s' \"$callback\" | jq -r .tool_input.file_path)\n" + body + "\n"
	if err := os.WriteFile(fixture.script, []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	return fixture
}

// run sends request to the edit-gate mode and returns its status, stdout and stderr.
func (f editGateFixture) run(t *testing.T, request any) (int, string, string) {
	t.Helper()
	input, ok := request.(string)
	if !ok {
		encoded, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		input = string(encoded)
	}
	var stdout, stderr bytes.Buffer
	status := run([]string{"edit-gate", f.bash, f.script}, strings.NewReader(input), &stdout, &stderr)
	return status, stdout.String(), stderr.String()
}

// calls returns the callbacks the stub received, in order.
func (f editGateFixture) calls(t *testing.T) []dispatchCallback {
	t.Helper()
	data, err := os.ReadFile(f.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var calls []dispatchCallback
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var call dispatchCallback
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			t.Fatalf("callback %q: %v", line, err)
		}
		calls = append(calls, call)
	}
	return calls
}

// editRequest builds a Write request for targets given as path/file_path pairs.
func editRequest(pairs ...string) editGateRequest {
	request := editGateRequest{SessionID: "host-session", TranscriptPath: "/t.jsonl", AgentKind: "sub", Cwd: "/repo", HookEventName: hookEventPreToolUse, ToolName: editGateToolWrite}
	for i := 0; i+1 < len(pairs); i += 2 {
		request.Targets = append(request.Targets, editTarget{Path: pairs[i], FilePath: pairs[i+1]})
	}
	return request
}

// decodeDecision parses the gate's stdout envelope.
func decodeDecision(t *testing.T, stdout string) hookSpecificOutput {
	t.Helper()
	var decision hookDecision
	if err := json.Unmarshal([]byte(stdout), &decision); err != nil {
		t.Fatalf("decision %q: %v", stdout, err)
	}
	if decision.HookSpecificOutput.HookEventName != hookEventPreToolUse {
		t.Fatalf("decision event: %q", stdout)
	}
	return decision.HookSpecificOutput
}

func TestEditGateDispatchesDistinctNormalizedTargetsInOrder(t *testing.T) {
	fixture := newEditGateFixture(t, "")
	status, stdout, stderr := fixture.run(t, editRequest(":~/a", "/home/u/a", "b", "/repo/b", "~/a", "/home/u/a"))
	if status != statusOK || stdout != "" || stderr != "" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	calls := fixture.calls(t)
	if len(calls) != 2 || calls[0].ToolInput.FilePath != "/home/u/a" || calls[1].ToolInput.FilePath != "/repo/b" {
		t.Fatalf("calls: %+v", calls)
	}
	want := dispatchCallback{SessionID: "host-session", TranscriptPath: "/t.jsonl", AgentKind: "sub", Cwd: "/repo", HookEventName: hookEventPreToolUse, ToolName: editGateToolWrite, ToolInput: dispatchToolInput{FilePath: "/home/u/a"}}
	if calls[0] != want {
		t.Fatalf("callback: %+v", calls[0])
	}
}

func TestEditGateFirstDenialWinsAndNamesRequestedSpelling(t *testing.T) {
	fixture := newEditGateFixture(t, `case "$target" in
/protected*) printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"protected target"}}' ;;
*) printf '%s\n' 'ECI_EDIT_GATE_FAILED_ALLOWED eci-active-gate.sh' >&2 ;;
esac`)
	status, stdout, _ := fixture.run(t, editRequest("ordinary", "/repo/ordinary", ":/protected/one", "/protected/one", "/protected/two", "/protected/two"))
	if status != statusOK {
		t.Fatalf("status=%d", status)
	}
	decision := decodeDecision(t, stdout)
	if decision.PermissionDecision != decisionDeny || decision.AdditionalContext != "" ||
		decision.PermissionDecisionReason != `protected target (requested as ":/protected/one", checked as "/protected/one")` {
		t.Fatalf("decision: %+v", decision)
	}
	if calls := fixture.calls(t); len(calls) != 2 {
		t.Fatalf("a later target ran after the denial: %+v", calls)
	}
}

func TestEditGateReportsExactFailedGuardMarkersOnce(t *testing.T) {
	t.Setenv("ECI_HOOK_IS_SUBAGENT", "1")
	fixture := newEditGateFixture(t, `printf '%s\n' 'ECI_EDIT_GATE_FAILED_ALLOWED validate-edit-write.sh' \
  'ECI_EDIT_GATE_FAILED_ALLOWED validate-edit-write.sh extra' 'ECI_EDIT_GATE_FAILED_ALLOWED validate-apply-patch.sh' \
  'ECI_EDIT_GATE_FAILED_ALLOWED ../x' ' ECI_EDIT_GATE_TARGET_UNCLASSIFIED' 'untrusted payload' >&2`)
	request := editRequest("a", "/repo/a", "b", "/repo/b")
	request.SessionID = "it's"
	status, stdout, _ := fixture.run(t, request)
	if status != statusOK {
		t.Fatalf("status=%d", status)
	}
	decision := decodeDecision(t, stdout)
	context := decision.AdditionalContext
	if decision.PermissionDecision != decisionAllow || strings.Count(context, "ECI edit guard ") != 1 {
		t.Fatalf("decision: %+v", decision)
	}
	for _, unwanted := range []string{"untrusted payload", "../x", "validate-apply-patch", "extra", "could not classify"} {
		if strings.Contains(context, unwanted) {
			t.Fatalf("context contains %q: %s", unwanted, context)
		}
	}
	callback, err := json.Marshal(request.callback(request.Targets[0]))
	if err != nil {
		t.Fatal(err)
	}
	command := "printf '%s\\n' " + shellQuote(string(callback)) + ` | ECI_SESSION_ID='it'\''s' ECI_THREAD_ID='it'\''s' ECI_HOOK_IS_SUBAGENT='1' bash -x "$HOME/.omp/agent/eci/hooks/validate-edit-write.sh"`
	if !strings.Contains(context, command) || !strings.Contains(context, `"/repo/a"`) || !strings.Contains(context, "same session") {
		t.Fatalf("diagnostic lacks the exact rerun command %q:\n%s", command, context)
	}
}

func TestEditGateReportsUnclassifiedTargets(t *testing.T) {
	fixture := newEditGateFixture(t, `printf '%s\n' ECI_EDIT_GATE_TARGET_UNCLASSIFIED >&2`)
	status, stdout, _ := fixture.run(t, editRequest("a", "/repo/a"))
	decision := decodeDecision(t, stdout)
	if status != statusOK || decision.PermissionDecision != decisionAllow ||
		!strings.Contains(decision.AdditionalContext, `could not classify the allowed target "/repo/a"`) ||
		!strings.Contains(decision.AdditionalContext, "coverage is unknown") {
		t.Fatalf("status=%d decision=%+v", status, decision)
	}
}

func TestEditGateFailsClosedOnDispatcherFailure(t *testing.T) {
	cases := map[string]struct {
		body string
		want string
	}{
		"non-zero exit":   {"printf '%s\\n' 'guard crashed' >&2; exit 17", "ECI hook pretooluse-edit-dispatch exited 17: guard crashed; blocking (fail-closed)."},
		"non-JSON":        {"printf '%s\\n' 'not json'", "ECI hook pretooluse-edit-dispatch returned non-JSON output; blocking (fail-closed)."},
		"wrong event":     {`printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PostToolUse","permissionDecision":"deny"}}'`, "ECI hook pretooluse-edit-dispatch returned unrecognized output; blocking (fail-closed)."},
		"bad decision":    {`printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask"}}'`, "ECI hook pretooluse-edit-dispatch returned unsupported permissionDecision; blocking (fail-closed)."},
		"oversized":       {"head -c 70000 /dev/zero | tr '\\0' ' '; printf x", "ECI hook pretooluse-edit-dispatch returned more than 65536 bytes of output; blocking (fail-closed)."},
		"reasonless deny": {`printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny"}}'`, "denied by ECI hook pretooluse-edit-dispatch"},
		"killed":          {"kill -9 $$", "ECI hook pretooluse-edit-dispatch was ended by a signal; blocking (fail-closed)."},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fixture := newEditGateFixture(t, tc.body)
			status, stdout, _ := fixture.run(t, editRequest("/repo/a", "/repo/a"))
			decision := decodeDecision(t, stdout)
			if status != statusOK || decision.PermissionDecision != decisionDeny || decision.PermissionDecisionReason != tc.want {
				t.Fatalf("status=%d decision=%+v", status, decision)
			}
		})
	}
}

func TestEditGateAllowDecisionKeepsChecking(t *testing.T) {
	fixture := newEditGateFixture(t, `printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"fine"}}'`)
	status, stdout, _ := fixture.run(t, editRequest("a", "/repo/a", "b", "/repo/b"))
	if status != statusOK || stdout != "" || len(fixture.calls(t)) != 2 {
		t.Fatalf("status=%d stdout=%q", status, stdout)
	}
}

func TestEditGateFailsClosedWhenDispatcherCannotStart(t *testing.T) {
	var stdout, stderr bytes.Buffer
	request, err := json.Marshal(editRequest("a", "/repo/a"))
	if err != nil {
		t.Fatal(err)
	}
	status := run([]string{"edit-gate", filepath.Join(t.TempDir(), "missing-bash"), "dispatch.sh"}, bytes.NewReader(request), &stdout, &stderr)
	decision := decodeDecision(t, stdout.String())
	if status != statusOK || decision.PermissionDecision != decisionDeny || !strings.HasPrefix(decision.PermissionDecisionReason, "ECI hook pretooluse-edit-dispatch could not run (") {
		t.Fatalf("status=%d decision=%+v", status, decision)
	}
}

func TestEditGateRejectsMalformedRequests(t *testing.T) {
	fixture := newEditGateFixture(t, "")
	valid := editRequest("a", "/repo/a")
	unknownTool := valid
	unknownTool.ToolName = "Bash"
	wrongEvent := valid
	wrongEvent.HookEventName = "PostToolUse"
	noTargets := valid
	noTargets.Targets = nil
	emptyTarget := editRequest("a", "")
	cases := map[string]any{
		"non-JSON":      "not json",
		"array":         "[]",
		"unknown field": `{"session_id":"s","cwd":"/","hook_event_name":"PreToolUse","tool_name":"Write","targets":[{"path":"a","file_path":"/a"}],"tool_input":{}}`,
		"trailing":      `{"session_id":"s","cwd":"/","hook_event_name":"PreToolUse","tool_name":"Write","targets":[{"path":"a","file_path":"/a"}]} {}`,
		"unknown tool":  unknownTool,
		"wrong event":   wrongEvent,
		"no targets":    noTargets,
		"empty target":  emptyTarget,
		"oversized":     strings.Repeat(" ", inputBudget+1),
		"NUL":           "{\x00}",
	}
	for name, request := range cases {
		t.Run(name, func(t *testing.T) {
			status, stdout, stderr := fixture.run(t, request)
			if status != statusRejected || stdout != "" || !strings.HasPrefix(stderr, "eci-hook-helper edit-gate: ") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
		})
	}
	if calls := fixture.calls(t); len(calls) != 0 {
		t.Fatalf("a rejected request ran the dispatcher: %+v", calls)
	}
	for _, args := range [][]string{{"edit-gate"}, {"edit-gate", fixture.bash}, {"edit-gate", "", fixture.script}, {"edit-gate", fixture.bash, fixture.script, "extra"}} {
		if status, _ := runMode(t, args, ""); status != statusRejected {
			t.Fatalf("args %v: want %d, got %d", args, statusRejected, status)
		}
	}
}
