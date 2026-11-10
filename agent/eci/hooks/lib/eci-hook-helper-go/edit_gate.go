package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// The edit-gate mode serves the omp edit adapter. The adapter sends one
// request naming every host-normalized target of an Edit or Write call; this
// mode runs pretooluse-edit-dispatch.sh once per distinct normalized target,
// in request order. The first denial decides the call. Otherwise the exact
// failed-guard and unclassified-target markers the dispatcher prints become
// one advisory context. The adapter owns the deadline and the process-group
// cleanup, so this mode starts no timer and no process group.

const (
	// dispatchLabel names the dispatcher in fail-closed denial reasons.
	//
	// Example: "ECI hook pretooluse-edit-dispatch exited 3; blocking (fail-closed)."
	dispatchLabel = "pretooluse-edit-dispatch"

	// hookEventPreToolUse is the only hook event the edit gate serves.
	//
	// Example: the request's and every callback's hook_event_name.
	hookEventPreToolUse = "PreToolUse"

	// failedGuardMarker prefixes the dispatcher's stderr line for a failed
	// guard on an allowed target; the guard script name follows it.
	//
	// Example: "ECI_EDIT_GATE_FAILED_ALLOWED validate-edit-write.sh".
	failedGuardMarker = "ECI_EDIT_GATE_FAILED_ALLOWED "

	// unclassifiedMarker is the dispatcher's stderr line for an allowed target
	// whose protected-target class could not be decided.
	//
	// Example: printed after a failed guard on an unresolvable target.
	unclassifiedMarker = "ECI_EDIT_GATE_TARGET_UNCLASSIFIED"

	// editGuardHooksDir is the hook directory the diagnostics name.
	//
	// Example: "$HOME/.omp/agent/eci/hooks/validate-edit-write.sh".
	editGuardHooksDir = "$HOME/.omp/agent/eci/hooks"

	// decisionAllow is the hook permission decision that admits the call.
	//
	// Example: a dispatcher allow, or the gate's advisory-only result.
	decisionAllow = "allow"

	// decisionDeny is the hook permission decision that blocks the call.
	//
	// Example: a guard's concrete target denial.
	decisionDeny = "deny"
)

// editGateTool names the host tool whose targets one request carries.
//
// Example: editGateToolWrite for a write call.
type editGateTool string

const (
	// editGateToolEdit is the callback tool name for the host edit tool.
	//
	// Example: a hashline patch or a replace edit.
	editGateToolEdit editGateTool = "Edit"

	// editGateToolWrite is the callback tool name for the host write tool.
	//
	// Example: a whole-file write.
	editGateToolWrite editGateTool = "Write"
)

// editGuards are the dispatcher children whose failure markers the gate
// reports; any other marker text is ignored.
//
// Example: editGuards["validate-edit-write.sh"] is true.
var editGuards = map[string]bool{
	"validate-edit-write.sh": true,
	"eci-active-gate.sh":     true,
}

// editTarget is one edit target as the model requested it and as the host
// resolves it.
//
// Example: {Path: ":~/notes.txt", FilePath: "/home/user/notes.txt"}.
type editTarget struct {
	// Path is the requested spelling, kept for diagnostics only.
	Path string `json:"path"`
	// FilePath is the host-normalized target every check uses.
	FilePath string `json:"file_path"`
}

// editGateRequest is the adapter's single request: the hook identity fields
// and the ordered targets of one tool call.
//
// Example: {"session_id":"s","cwd":"/repo","hook_event_name":"PreToolUse","tool_name":"Write","targets":[...]}.
type editGateRequest struct {
	SessionID      string       `json:"session_id"`
	TranscriptPath string       `json:"transcript_path,omitempty"`
	AgentKind      string       `json:"omp_agent_kind,omitempty"`
	Cwd            string       `json:"cwd"`
	HookEventName  string       `json:"hook_event_name"`
	ToolName       editGateTool `json:"tool_name"`
	Targets        []editTarget `json:"targets"`
}

// dispatchToolInput is the tool input of one dispatcher callback; the
// guards read only the target.
//
// Example: {"file_path":"/repo/a.txt"}.
type dispatchToolInput struct {
	FilePath string `json:"file_path"`
}

// dispatchCallback is the hook-shaped stdin of one dispatcher run.
//
// Example: the request identity plus tool_input {file_path: target}.
type dispatchCallback struct {
	SessionID      string            `json:"session_id"`
	TranscriptPath string            `json:"transcript_path,omitempty"`
	AgentKind      string            `json:"omp_agent_kind,omitempty"`
	Cwd            string            `json:"cwd"`
	HookEventName  string            `json:"hook_event_name"`
	ToolName       editGateTool      `json:"tool_name"`
	ToolInput      dispatchToolInput `json:"tool_input"`
}

// hookDecision is the gate's PreToolUse output envelope.
//
// Example: {"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"..."}}.
type hookDecision struct {
	HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput"`
}

// hookSpecificOutput is a PreToolUse decision with an optional reason or
// advisory context.
//
// Example: an allow carrying failed-guard diagnostics.
type hookSpecificOutput struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
}

// editDispatcher runs the dispatcher script with the launcher's interpreter.
//
// Example: editDispatcher{Bash: "/usr/bin/bash", Script: hooks + "/pretooluse-edit-dispatch.sh"}.
type editDispatcher struct {
	Bash   string
	Script string
}

// dispatchRun is the captured result of one dispatcher run.
//
// Example: exit 0 with empty stdout allows its target.
type dispatchRun struct {
	StartErr error
	// ExitCode is the dispatcher status; -1 when a signal ended it.
	ExitCode int
	Stdout   boundedBuffer
	Stderr   boundedBuffer
}

// boundedBuffer keeps the first inputBudget bytes written to it and records
// whether more arrived, so a runaway child cannot grow the helper.
//
// Example: the dispatcher's stdout capture.
type boundedBuffer struct {
	Data     []byte
	Overflow bool
}

// failedGuard is a guard that failed on an allowed target, with the first
// callback it failed on.
//
// Example: validate-edit-write.sh on /repo/a.txt.
type failedGuard struct {
	Name     string
	Callback dispatchCallback
}

// editGateWarnings accumulates the advisory markers of allowed targets.
//
// Example: one failed guard and one unclassified target.
type editGateWarnings struct {
	FailedGuards []failedGuard
	Unclassified []string
}

// runEditGate serves `edit-gate <bash> <dispatcher>`: it reads one request
// from stdin and writes the gate decision to stdout. A rejected request exits
// 2 with the reason on stderr; an allowed call without warnings prints nothing.
func runEditGate(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[0] == "" || args[1] == "" {
		return statusRejected
	}
	request, status := readEditGateRequest(stdin, stderr)
	if status != statusOK {
		return status
	}
	decision := editDispatcher{Bash: args[0], Script: args[1]}.decide(request, subagentFlag())
	if decision == nil {
		return statusOK
	}
	encoded, err := json.Marshal(decision)
	if err != nil {
		return statusFailed
	}
	if _, err := stdout.Write(append(encoded, '\n')); err != nil {
		return statusFailed
	}
	return statusOK
}

// readEditGateRequest decodes and validates the bounded request on stdin;
// it reports a rejection on stderr.
func readEditGateRequest(stdin io.Reader, stderr io.Writer) (editGateRequest, int) {
	var request editGateRequest
	data, overflow, err := readBounded(stdin)
	switch {
	case err != nil:
		return request, statusFailed
	case overflow:
		return request, rejectEditGateRequest(stderr, fmt.Sprintf("request is larger than %d bytes; split the edit into calls with fewer targets", inputBudget))
	case !validBounded(data):
		return request, rejectEditGateRequest(stderr, "request contains NUL or invalid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, rejectEditGateRequest(stderr, "request is not a valid edit-gate object: "+err.Error())
	}
	if decoder.More() {
		return request, rejectEditGateRequest(stderr, "request has trailing data")
	}
	if reason := request.invalidReason(); reason != "" {
		return request, rejectEditGateRequest(stderr, reason)
	}
	return request, statusOK
}

// rejectEditGateRequest prints the rejection and returns statusRejected.
func rejectEditGateRequest(stderr io.Writer, reason string) int {
	fmt.Fprintf(stderr, "eci-hook-helper edit-gate: %s\n", reason)
	return statusRejected
}

// invalidReason names the first violated request invariant, or returns "".
func (r editGateRequest) invalidReason() string {
	switch r.ToolName {
	case editGateToolEdit, editGateToolWrite:
	default:
		return fmt.Sprintf("unsupported tool_name %q", r.ToolName)
	}
	if r.HookEventName != hookEventPreToolUse {
		return fmt.Sprintf("unsupported hook_event_name %q", r.HookEventName)
	}
	if len(r.Targets) == 0 {
		return "request names no targets"
	}
	for _, target := range r.Targets {
		if target.Path == "" || target.FilePath == "" {
			return "every target needs a non-empty path and file_path"
		}
	}
	return ""
}

// callback is the dispatcher stdin for target.
func (r editGateRequest) callback(target editTarget) dispatchCallback {
	return dispatchCallback{
		SessionID:      r.SessionID,
		TranscriptPath: r.TranscriptPath,
		AgentKind:      r.AgentKind,
		Cwd:            r.Cwd,
		HookEventName:  r.HookEventName,
		ToolName:       r.ToolName,
		ToolInput:      dispatchToolInput{FilePath: target.FilePath},
	}
}

// subagentFlag is the inherited ECI_HOOK_IS_SUBAGENT value the adapter's
// runner set for this call; diagnostics repeat it.
func subagentFlag() string {
	return os.Getenv("ECI_HOOK_IS_SUBAGENT")
}

// decide checks each distinct normalized target in order. The first denial
// returns at once and discards earlier warnings; nil allows silently.
func (d editDispatcher) decide(request editGateRequest, subagent string) *hookDecision {
	var warnings editGateWarnings
	checked := map[string]bool{}
	for _, target := range request.Targets {
		if checked[target.FilePath] {
			continue
		}
		checked[target.FilePath] = true

		callback := request.callback(target)
		payload, err := json.Marshal(callback)
		if err != nil {
			return denyDecision(failClosed("could not encode its callback (" + err.Error() + ")"))
		}
		run := d.run(payload)
		if denial := run.denial(); denial != "" {
			return denyDecision(denial + requestedSpelling(target))
		}
		warnings.collect(run.Stderr.Data, callback)
	}
	return warnings.decision(subagent)
}

// run executes the dispatcher once with payload on stdin. The child stays in
// this process group so the adapter's group cleanup reaches it.
func (d editDispatcher) run(payload []byte) dispatchRun {
	var run dispatchRun
	command := exec.Command(d.Bash, d.Script)
	command.Stdin = bytes.NewReader(payload)
	command.Stdout = &run.Stdout
	command.Stderr = &run.Stderr
	err := command.Run()

	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		run.ExitCode = exitErr.ExitCode()
	default:
		run.StartErr = err
	}
	return run
}

// denial returns the fail-closed or dispatcher denial reason, or "" when
// the dispatcher allowed its target.
func (r dispatchRun) denial() string {
	switch {
	case r.StartErr != nil:
		return failClosed("could not run (" + r.StartErr.Error() + ")")
	case r.ExitCode == -1:
		return failClosed("was ended by a signal")
	case r.ExitCode != 0:
		if detail := strings.TrimSpace(string(r.Stderr.Data)); detail != "" {
			return failClosed(fmt.Sprintf("exited %d: %s", r.ExitCode, detail))
		}
		return failClosed(fmt.Sprintf("exited %d", r.ExitCode))
	case r.Stdout.Overflow:
		return failClosed(fmt.Sprintf("returned more than %d bytes of output", inputBudget))
	}
	return dispatchOutputDenial(bytes.TrimSpace(r.Stdout.Data))
}

// dispatchOutputDenial classifies a successful dispatcher's stdout: empty
// or an allow decision admits the target; anything but a well-formed
// PreToolUse allow or deny fails closed.
func dispatchOutputDenial(output []byte) string {
	if len(output) == 0 {
		return ""
	}
	var parsed map[string]any
	if err := json.Unmarshal(output, &parsed); err != nil {
		return failClosed("returned non-JSON output")
	}
	specific, ok := parsed["hookSpecificOutput"].(map[string]any)
	if !ok || specific["hookEventName"] != hookEventPreToolUse {
		return failClosed("returned unrecognized output")
	}
	switch specific["permissionDecision"] {
	case decisionAllow:
		return ""
	case decisionDeny:
		if reason, ok := specific["permissionDecisionReason"].(string); ok && reason != "" {
			return reason
		}
		return "denied by ECI hook " + dispatchLabel
	default:
		return failClosed("returned unsupported permissionDecision")
	}
}

// failClosed words a denial caused by the dispatcher's own failure.
func failClosed(why string) string {
	return fmt.Sprintf("ECI hook %s %s; blocking (fail-closed).", dispatchLabel, why)
}

// requestedSpelling notes the requested spelling when normalization changed
// it, so a denial names both forms.
func requestedSpelling(target editTarget) string {
	if target.Path == target.FilePath {
		return ""
	}
	return fmt.Sprintf(" (requested as %q, checked as %q)", target.Path, target.FilePath)
}

// denyDecision is the gate's deny envelope.
func denyDecision(reason string) *hookDecision {
	return &hookDecision{HookSpecificOutput: hookSpecificOutput{
		HookEventName:            hookEventPreToolUse,
		PermissionDecision:       decisionDeny,
		PermissionDecisionReason: reason,
	}}
}

// Write keeps the first inputBudget bytes and reports every byte consumed so
// the child never blocks on a full pipe.
func (b *boundedBuffer) Write(p []byte) (int, error) {
	room := inputBudget - len(b.Data)
	kept := p
	if len(p) > room {
		b.Overflow = true
		kept = p[:max(room, 0)]
	}
	b.Data = append(b.Data, kept...)
	return len(p), nil
}

// collect records the exact markers among stderr lines; other text is
// untrusted and ignored.
func (w *editGateWarnings) collect(stderr []byte, callback dispatchCallback) {
	for _, line := range strings.Split(string(stderr), "\n") {
		switch {
		case line == unclassifiedMarker:
			w.addUnclassified(callback.ToolInput.FilePath)
		case strings.HasPrefix(line, failedGuardMarker):
			w.addFailedGuard(strings.TrimPrefix(line, failedGuardMarker), callback)
		}
	}
}

// addUnclassified records path once.
func (w *editGateWarnings) addUnclassified(path string) {
	for _, known := range w.Unclassified {
		if known == path {
			return
		}
	}
	w.Unclassified = append(w.Unclassified, path)
}

// addFailedGuard records a known guard once, with its first callback.
func (w *editGateWarnings) addFailedGuard(name string, callback dispatchCallback) {
	if !editGuards[name] {
		return
	}
	for _, known := range w.FailedGuards {
		if known.Name == name {
			return
		}
	}
	w.FailedGuards = append(w.FailedGuards, failedGuard{Name: name, Callback: callback})
}

// decision is the allow envelope carrying the warnings, or nil without any.
func (w editGateWarnings) decision(subagent string) *hookDecision {
	var lines []string
	for _, guard := range w.FailedGuards {
		lines = append(lines, guard.diagnostic(subagent))
	}
	if len(w.Unclassified) > 0 {
		quoted := make([]string, len(w.Unclassified))
		for i, path := range w.Unclassified {
			quoted[i] = fmt.Sprintf("%q", path)
		}
		lines = append(lines, fmt.Sprintf("ECI edit gate could not classify the allowed target %s; protected-target coverage is unknown. Inspect the target and repair dispatch_protected_target in %s/pretooluse-edit-dispatch.sh in the same session.", strings.Join(quoted, ", "), editGuardHooksDir))
	}
	if len(lines) == 0 {
		return nil
	}
	return &hookDecision{HookSpecificOutput: hookSpecificOutput{
		HookEventName:      hookEventPreToolUse,
		PermissionDecision: decisionAllow,
		AdditionalContext:  strings.Join(lines, "\n"),
	}}
}

// diagnostic tells the agent how to rerun the failed guard with the exact
// callback and ECI identity environment of the failed check.
func (g failedGuard) diagnostic(subagent string) string {
	payload, err := json.Marshal(g.Callback)
	if err != nil {
		return fmt.Sprintf("ECI edit guard %s failed while checking an allowed edit; protected-target coverage may be incomplete. Inspect and repair %s/%s in the same session.", g.Name, editGuardHooksDir, g.Name)
	}
	session := shellQuote(g.Callback.SessionID)
	return fmt.Sprintf("ECI edit guard %s failed while checking the allowed target %q; protected-target coverage may be incomplete. In the same session, rerun it with: printf '%%s\\n' %s | ECI_SESSION_ID=%s ECI_THREAD_ID=%s ECI_HOOK_IS_SUBAGENT=%s bash -x \"%s/%s\". Inspect the ordinary command output, repair the guard, and report the fix.",
		g.Name, g.Callback.ToolInput.FilePath, shellQuote(string(payload)), session, session, shellQuote(subagent), editGuardHooksDir, g.Name)
}

// shellQuote quotes s as one POSIX shell word.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
