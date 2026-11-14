package plan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	// manySessionFixtureEntries exercises immediate directory observation past 128 entries.
	//
	// Example: this many controls plus one marker must retain all 129 inode facts.
	manySessionFixtureEntries = 128
)

// TestRunHookScratchScript verifies execution admission in an active Worker callback.
//
// Example: an unchanged HOME/tmp script is inspected without running its body.
func TestRunHookScratchScript(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ECI_PROOF_ROOT", filepath.Join(home, "proof"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("ECI_HOOK_IS_SUBAGENT", "true")
	contextMarker(t, filepath.Join(home, "proof"), "scratch-owner", cwd)
	script := filepath.Join(home, "tmp", "proof", "run.sh")
	sentinel := filepath.Join(home, "script-executed")
	requireHookDirectory(t, filepath.Dir(script))
	requireHookFile(t, script, "#!/bin/sh\nprintf executed > '"+sentinel+"'\n")
	input := HookInput{SessionID: "scratch-owner", CWD: cwd, ToolName: "Bash"}
	input.ToolInput.Command = "bash '" + script + "'"
	request, err := hookRequest(input)
	require.NoError(t, err)
	require.Equal(t, MarkerInactive, request.Marker)
	require.Equal(t, RoleWorker, request.Role)
	require.Empty(t, request.ApprovedRoots)
	callback, err := json.Marshal(input)
	require.NoError(t, err)
	var output bytes.Buffer
	require.Zero(t, RunHook(bytes.NewReader(callback), &output, io.Discard))
	_, err = os.Stat(sentinel)
	require.ErrorIs(t, err, os.ErrNotExist, "hook must never execute the inspected script")
	require.Empty(t, output.String(), "active scratch script callback denied: %s", &output)
}

// TestRunHookScratchAliases verifies callback roles do not restore marker scope.
//
// Example: a child transcript remains a Worker without inheriting marker roots.
func TestRunHookScratchAliases(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	volume := t.TempDir()
	proof := filepath.Join(home, "proof")
	t.Setenv("HOME", home)
	t.Setenv("ECI_PROOF_ROOT", proof)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	contextMarker(t, proof, "scratch-parent", cwd)
	require.NoError(t, os.Symlink(volume, filepath.Join(home, "tmp")))
	script := filepath.Join(volume, "run.sh")
	sentinel := filepath.Join(volume, "executed")
	requireHookFile(t, script, "#!/bin/sh\nprintf executed > '"+sentinel+"'\n")
	alias := filepath.Join(home, "tmp", "alias.sh")
	require.NoError(t, os.Symlink(script, alias))
	sessions := filepath.Join(home, ".omp", "agent", "sessions", "project")
	require.NoError(t, os.MkdirAll(sessions, 0700))
	parentTranscript := filepath.Join(sessions, "parent.jsonl")
	require.NoError(t, os.WriteFile(parentTranscript, []byte(`{"type":"session","version":3,"id":"scratch-parent"}`+"\n"), 0600))
	childDir := filepath.Join(sessions, "children")
	require.NoError(t, os.MkdirAll(childDir, 0700))
	childTranscript := filepath.Join(childDir, "child.jsonl")
	require.NoError(t, os.WriteFile(childTranscript, []byte(`{"type":"title","v":1,"title":""}`+"\n"+`{"type":"session","version":3,"id":"scratch-child","parentSession":"`+parentTranscript+`"}`+"\n"), 0600))
	mainTranscript := filepath.Join(sessions, "main.jsonl")
	require.NoError(t, os.WriteFile(mainTranscript, []byte(`{"type":"session","version":3,"id":"scratch-parent"}`+"\n"), 0600))
	for _, actor := range []string{"direct-worker", "parent-worker", "coordinator"} {
		input := HookInput{SessionID: "scratch-parent", CWD: cwd, ToolName: "Bash"}
		t.Setenv("ECI_HOOK_IS_SUBAGENT", "false")
		role := RoleCoordinator
		switch actor {
		case "direct-worker":
			t.Setenv("ECI_HOOK_IS_SUBAGENT", "true")
			role = RoleWorker
		case "parent-worker":
			input.SessionID = "scratch-child"
			input.TranscriptPath = childTranscript
			role = RoleWorker
		case "coordinator":
			input.TranscriptPath = mainTranscript
		}
		for _, command := range []string{"bash '" + script + "'", "bash '" + alias + "'", "'" + alias + "'"} {
			input.ToolInput.Command = command
			request, err := hookRequest(input)
			require.NoError(t, err)
			require.Equal(t, role, request.Role, actor)
			require.Equal(t, MarkerInactive, request.Marker, actor)
			require.Equal(t, input.SessionID, request.ActiveSession, actor)
			require.Equal(t, input.SessionID, request.HookSessionID, actor)
			require.Empty(t, request.ApprovedRoots, actor)
			callback, err := json.Marshal(input)
			require.NoError(t, err)
			var output bytes.Buffer
			require.Zero(t, RunHook(bytes.NewReader(callback), &output, io.Discard))
			require.Empty(t, output.String(), "%s: %s", actor, command)
		}
	}
	_, err := os.Stat(sentinel)
	require.ErrorIs(t, err, os.ErrNotExist)
}

// TestRunHookEnvelope verifies silent allows and a valid callback denial envelope.
//
// Example: root removal emits one denial while an ordinary read emits nothing.
func TestRunHookEnvelope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ECI_PROOF_ROOT", filepath.Join(home, "proof"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("ECI_HOOK_IS_SUBAGENT", "false")
	markerDir := filepath.Join(home, "proof", "hook-envelope")
	require.NoError(t, os.MkdirAll(markerDir, 0700))
	marker := "scope: hook envelope\ncwd: /tmp\nsession_id: hook-envelope\ncreated_utc: 2026-10-04T00:00:00Z\n"
	require.NoError(t, os.WriteFile(filepath.Join(markerDir, "eci_active"), []byte(marker), 0600))
	tests := []struct {
		name, input string
		denied      bool
	}{
		{"ordinary allow", `{"session_id":"hook-envelope","tool_name":"Bash","cwd":"/tmp","tool_input":{"command":"cat ordinary"}}`, false},
		{"concrete root removal", `{"session_id":"hook-envelope","tool_name":"Bash","cwd":"/tmp","tool_input":{"command":"rm -rf /"}}`, true},
		{"other tool", `{"tool_name":"Read","tool_input":{"command":"rm -rf /"}}`, false},
	}
	for _, test := range tests {
		t.Run(test.name,
			// Verify one callback keeps the hook transport contract.
			//
			// Example: malformed callback metadata produces no denial envelope.
			func(t *testing.T) {
				var output bytes.Buffer
				{
					status := RunHook(strings.NewReader(test.input), &output, io.Discard)
					require.Falsef(t, status != 0, "hook status=%d, want0", status)
				}
				if !test.denied {
					require.Falsef(t, output.Len() != 0, "allow output=%q", output.String())
					return
				}
				var envelope struct {
					HookSpecificOutput HookSpecificOutput `json:"hookSpecificOutput"`
				}
				require.NoError(t, json.Unmarshal(output.Bytes(), &envelope))
				require.Falsef(t, envelope.HookSpecificOutput.HookEventName != "PreToolUse" || envelope.HookSpecificOutput.PermissionDecision != "deny" || envelope.HookSpecificOutput.PermissionDecisionReason == "", "invalid hook denial: %s", &output)
			})
	}
	for _, input := range []string{`{`, `{"tool_name":"Bash"} trailing`, `{"tool_name":"Bash"} {}`} {
		var output bytes.Buffer
		require.Equal(t, 2, RunHook(strings.NewReader(input), &output, io.Discard), "malformed callback must block")
		require.Empty(t, output.String())
	}
	closed, err := os.CreateTemp(t.TempDir(), "closed-output")
	require.NoError(t, err)
	require.NoError(t, closed.Close())
	for _, input := range []string{
		`{"session_id":"hook-envelope","tool_name":"Bash","cwd":"/tmp","tool_input":{"command":"rm -rf /"}}`,
		`{`,
	} {
		require.Equal(t, 2, RunHook(strings.NewReader(input), closed, io.Discard), "an undeliverable denial must block")
	}
}

// TestRunHookMalformedReportsToDiagnosticWriter keeps errors separate from decisions.
//
// Example: malformed JSON blocks the call and reports the failure to its caller.
func TestRunHookMalformedReportsToDiagnosticWriter(t *testing.T) {
	var output, diagnostic bytes.Buffer
	require.Equal(t, 2, RunHook(strings.NewReader("{"), &output, &diagnostic))
	require.Empty(t, output.String())
	require.Contains(t, diagnostic.String(), "decode callback failed")
}

// TestRunHookMarkerFreeBroadRoot protects a concrete root deletion without
// relying on lifecycle markers.
//
// Example: rm -rf / is denied while a bounded removal remains available.
func TestRunHookMarkerFreeBroadRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ECI_PROOF_ROOT", filepath.Join(home, "proof"))
	for _, current := range []struct {
		command string
		denied  bool
	}{
		{command: "rm -rf /", denied: true},
		{command: "rm -rf owned"},
		{command: "printf ready; rm -rf /", denied: true},
		{command: "printf ready; rm -rf owned"},
	} {
		input := HookInput{SessionID: "marker-free", ToolName: "Bash", CWD: home}
		input.ToolInput.Command = current.command
		callback, err := json.Marshal(input)
		require.NoError(t, err)
		var output bytes.Buffer
		require.Zero(t, RunHook(bytes.NewReader(callback), &output, io.Discard))
		if current.denied {
			require.Contains(t, output.String(), string(CodeBroadDestructiveDenied))
		} else {
			require.Empty(t, output.String())
		}
	}
}

// TestRunHookWorkerRemovalDiagnostics preserves distinct concrete target denials.
//
// Example: root removal reports the broad effect; session removal reports its target.
func TestRunHookWorkerRemovalDiagnostics(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ECI_PROOF_ROOT", filepath.Join(home, "proof"))
	t.Setenv("ECI_HOOK_IS_SUBAGENT", "true")
	sessions := filepath.Join(home, ".omp", "agent", "sessions")
	for _, current := range []struct {
		command string
		code    DiagnosticCode
	}{
		{"rm -rf /", CodeBroadDestructiveDenied},
		{"rm -rf '" + sessions + "'", hookCodeWorkerSessionsRootDenied},
		{"rm -rf '" + filepath.Join(home, "owned") + "'", ""},
	} {
		input := HookInput{SessionID: "worker", CWD: home, ToolName: "Bash"}
		input.ToolInput.Command = current.command
		callback, err := json.Marshal(input)
		require.NoError(t, err)
		var output bytes.Buffer
		require.Zero(t, RunHook(bytes.NewReader(callback), &output, io.Discard))
		if current.code == "" {
			require.Empty(t, output.String(), current.command)
			continue
		}
		require.Contains(t, output.String(), string(current.code), current.command)
	}
}

// TestRunHookMarkerFreeMultipleRepositories keeps bounded native Git requests
// available without treating a retired marker as repository authorization.
//
// Example: a dependency add is allowed while a whole-tree reset is denied.
func TestRunHookMarkerFreeMultipleRepositories(t *testing.T) {
	home := t.TempDir()
	primary := filepath.Join(home, "primary")
	dependency := filepath.Join(home, "dependency")
	t.Setenv("HOME", home)
	t.Setenv("ECI_PROOF_ROOT", filepath.Join(home, "proof"))
	t.Setenv("ECI_HOOK_IS_SUBAGENT", "true")
	for _, repository := range []string{primary, dependency} {
		require.NoError(t, os.MkdirAll(repository, 0700))
		require.NoError(t, exec.Command("git", "init", "-q", repository).Run())
	}
	contextMarker(t, filepath.Join(home, "proof"), "marker-free", primary)
	script := filepath.Join(dependency, "check.sh")
	sentinel := filepath.Join(home, "executed")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+sentinel+"'\n"), 0700))
	for _, current := range []struct {
		command string
		denied  bool
	}{
		{command: "git -C '" + primary + "' add -- file"},
		{command: "git -C '" + dependency + "' add -- file"},
		{command: "git -C '" + dependency + "' commit -m checkpoint"},
		{command: "bash '" + script + "'"},
		{command: "git -C '" + dependency + "' reset --hard", denied: true},
		{command: "git -C '" + dependency + "' checkout -- check.sh"},
		{command: "git -C '" + dependency + "' rm -- check.sh"},
		{command: "git -C '" + dependency + "' mv check.sh renamed.sh"},
		{command: "rm -f '" + script + "'"},
		{command: "mv '" + script + "' '" + filepath.Join(dependency, "moved.sh") + "'"},
	} {
		input := HookInput{SessionID: "marker-free", ToolName: "Bash", CWD: primary}
		input.ToolInput.Command = current.command
		callback, err := json.Marshal(input)
		require.NoError(t, err)
		var output bytes.Buffer
		require.Zero(t, RunHook(bytes.NewReader(callback), &output, io.Discard))
		if current.denied {
			require.Contains(t, output.String(), string(CodeBroadDestructiveDenied), current.command)
		} else {
			require.Empty(t, output.String(), current.command)
		}
	}
	_, err := os.Stat(sentinel)
	require.ErrorIs(t, err, os.ErrNotExist, "callback must not run the dependency script")
}

// TestRunHookMarkerFreeLiveHookOperations denies only live Bash-gate removal or relocation.
//
// Example: removing a live hook is denied; checking it out or unstaging it is not.
func TestRunHookMarkerFreeLiveHookOperations(t *testing.T) {
	home := t.TempDir()
	provider := filepath.Join(home, ".omp", "agent", "eci")
	hook := filepath.Join(provider, "hooks", "validate-bash.sh")
	t.Setenv("HOME", home)
	t.Setenv("ECI_PROOF_ROOT", filepath.Join(home, "proof"))
	require.NoError(t, os.MkdirAll(filepath.Dir(hook), 0700))
	require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\n"), 0700))
	_, err := hookGitOutput(context.Background(), provider, "init", "-q")
	require.NoError(t, err)
	for _, current := range []struct {
		command string
		denied  bool
	}{
		{command: "rm '" + hook + "'", denied: true},
		{command: "git rm -- hooks/validate-bash.sh", denied: true},
		{command: "git mv hooks/validate-bash.sh saved", denied: true},
		{command: "ECI_LIVE_HOOK_REMOVAL=1 git mv hooks/validate-bash.sh saved"},
		{command: "git checkout -- hooks/validate-bash.sh"},
		{command: "git rm --cached -- hooks/validate-bash.sh"},
		{command: "cat '" + hook + "'"},
		{command: "git add -A", denied: true},
	} {
		input := HookInput{SessionID: "marker-free", ToolName: "Bash", CWD: provider}
		input.ToolInput.Command = current.command
		callback, err := json.Marshal(input)
		require.NoError(t, err)
		var output bytes.Buffer
		require.Zero(t, RunHook(bytes.NewReader(callback), &output, io.Discard))
		if current.denied {
			require.Contains(t, output.String(), `"permissionDecision":"deny"`, current.command)
		} else {
			require.Empty(t, output.String(), current.command)
		}
	}
}

// TestHookGitDefaultsPreserveAllCommandEffects retains native verbs and siblings.
//
// Example: a native diff cannot hide a later concrete root removal.
func TestHookGitDefaultsPreserveAllCommandEffects(t *testing.T) {
	tests := []struct {
		name    string
		command string
		denied  bool
	}{
		{"diff options", "git diff --output=ordinary.patch", false},
		{"log options", "git log --all --format=%H", false},
		{"other verb", "git fsck --lost-found", false},
		{"harmless find action", "find . -exec cat '{}' ';'", false},
		{"long concrete effect", "printf '%s' '" + strings.Repeat("x", maxCommandBytes) + "'; rm -rf /", true},
		{"many concrete effects", strings.Repeat("printf ok;", maxSegments+1) + "rm -rf /", true},
		{"compound concrete effect", "git diff --output=ordinary.patch; rm -rf /", true},
		{"nested concrete effect", "git diff; bash -c 'rm -rf /'", true},
	}
	for _, test := range tests {
		t.Run(test.name,
			// Verify native defaults preserve every established command effect.
			//
			// Example: a nested destructive child remains a concrete denial.
			func(t *testing.T) {
				request := Request{Provider: ProviderOmp, Role: RoleCoordinator, CWD: t.TempDir(), Marker: MarkerActive, ActiveSession: "hook-test", Command: test.command, HookMode: true, CollectShellCommands: true}
				result := Classify(request)
				{
					got := result.Decision == DecisionDeny
					require.Falsef(t, got != test.denied, "denied=%v, want %v: %#v", got, test.denied, result)
				}
				require.False(t, !test.denied && (result.ShellAnalysis == nil || len(result.ShellAnalysis.Commands) == 0), "callback lost command records for native effect routing")
			})
	}
}

// TestHookOutputWritesReuseParsedEffects distinguishes output writes from data.
//
// Example: descriptor duplication is not a filesystem output write.
func TestHookOutputWritesReuseParsedEffects(t *testing.T) {
	for _, test := range []struct {
		command string
		writes  bool
	}{
		{"cat 'a>b'", false},
		{"printf note > ordinary", true},
		{"cat ordinary 2>&1", false},
		{"bash -c 'printf note >> ordinary'", true},
	} {
		request := Request{Provider: ProviderOmp, Role: RoleCoordinator, CWD: t.TempDir(), Marker: MarkerInactive, Command: test.command, HookMode: true, CollectShellCommands: true}
		result := Classify(request)
		require.Falsef(t, result.ShellAnalysis == nil || result.ShellAnalysis.OutputWrites != test.writes, "command=%q output writes=%#v want %v", test.command, result.ShellAnalysis, test.writes)
	}
}

// TestHookInactivePermissiveFinalization verifies optional compiled mode handling.
//
// Example: inactive permissive mode suppresses a denial; enforcing mode emits it.
func TestHookInactivePermissiveFinalization(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	gate := filepath.Join(home, ".omp", "agent", "eci", "bin", "eci-command-gate-mode")
	require.NoError(t, os.MkdirAll(filepath.Dir(gate), 0700))
	build := exec.Command("go", "build", "-o", gate, ".")
	build.Dir = filepath.Join("..", "..", "eci-command-gate-mode-go")
	{
		output, err := build.CombinedOutput()
		require.NoErrorf(t, err, "build optional finalizer: %v: %s", err, output)
	}
	t.Setenv("HOME", home)
	t.Setenv("ECI_PROOF_ROOT", filepath.Join(home, "proof"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("ECI_HOOK_IS_SUBAGENT", "true")
	contextMarker(t, filepath.Join(home, "proof"), "inactive", home)
	config := filepath.Join(home, "config", "eci", "command-gate-mode")
	require.NoError(t, os.MkdirAll(filepath.Dir(config), 0700))
	require.NoError(t, os.WriteFile(config, []byte("permissive\n"), 0600))
	input := HookInput{SessionID: "inactive", CWD: home, ToolName: "Bash"}
	input.ToolInput.Command = "'" + gate + "' set enforcing"
	callback, err := json.Marshal(input)
	require.NoError(t, err)
	var output bytes.Buffer
	{
		status := RunHook(bytes.NewReader(callback), &output, io.Discard)
		require.Falsef(t, status != 0 || output.Len() != 0, "inactive permissive callback status=%d output=%s", status, &output)
	}
	require.NoError(t, os.WriteFile(config, []byte("enforcing\n"), 0600))
	{
		status := RunHook(bytes.NewReader(callback), &output, io.Discard)
		require.Falsef(t, status != 0 || !strings.Contains(output.String(), `"permissionDecision":"deny"`), "inactive enforcing callback status=%d output=%s", status, &output)
	}
	require.Falsef(t, !strings.Contains(output.String(), string(CodeControlOwnerRequired)), "resolved worker control effect lost its ownership diagnostic: %s", &output)
	t.Setenv("ECI_HOOK_IS_SUBAGENT", "false")
	mainTranscript := filepath.Join(home, ".omp", "agent", "sessions", "project", "main.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(mainTranscript), 0700))
	require.NoError(t, os.WriteFile(mainTranscript, []byte(`{"type":"session","version":3,"id":"inactive"}`+"\n"), 0600))
	input.TranscriptPath = mainTranscript
	callback, err = json.Marshal(input)
	require.NoError(t, err)
	output.Reset()
	{
		status := RunHook(bytes.NewReader(callback), &output, io.Discard)
		require.Falsef(t, status != 0 || output.Len() != 0, "quoted coordinator control target denied: status=%d output=%s", status, &output)
	}
}

// TestHookAnalysisNeverExecutesTimeout retains child facts without wrapper execution.
//
// Example: timeout around native git add does not execute the inspected wrapper.
func TestHookAnalysisNeverExecutesTimeout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ECI_PROOF_ROOT", filepath.Join(home, "proof"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("ECI_HOOK_IS_SUBAGENT", "true")
	t.Setenv("PATH", home+":/usr/bin:/bin")
	sentinel := filepath.Join(home, "wrapper-executed")
	require.NoError(t, os.WriteFile(filepath.Join(home, "timeout"), []byte("#!/bin/sh\nprintf called > '"+sentinel+"'\nexec /usr/bin/timeout \"$@\"\n"), 0700))
	markerDir := filepath.Join(home, "proof", "timeout-owner")
	require.NoError(t, os.MkdirAll(markerDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(markerDir, "eci_active"), []byte("scope: callback\ncwd: "+home+"\nsession_id: timeout-owner\n"), 0600))
	input := HookInput{SessionID: "timeout-owner", CWD: home, ToolName: "Bash"}
	input.ToolInput.Command = "timeout 5 git add ordinary"
	callback, err := json.Marshal(input)
	require.NoError(t, err)
	var output bytes.Buffer
	{
		status := RunHook(bytes.NewReader(callback), &output, io.Discard)
		require.Zero(t, status)
		require.Empty(t, output.String(), "ordinary Git command denied")
	}
	{
		_, err := os.Stat(sentinel)
		require.Falsef(t, !os.IsNotExist(err), "callback executed inspected timeout wrapper: %v", err)
	}
}

// TestHookBookkeepingUsesNativeGit isolates queries from the inspected PATH.
//
// Example: a PATH Git wrapper is never invoked while recording a touched baseline.
func TestHookBookkeepingUsesNativeGit(t *testing.T) {
	home := t.TempDir()
	{
		output, err := exec.Command("/usr/bin/git", "-C", home, "init").CombinedOutput()
		require.NoErrorf(t, err, "init fixture: %v: %s", err, output)
	}
	t.Setenv("HOME", home)
	t.Setenv("ECI_PROOF_ROOT", filepath.Join(home, "proof"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("ECI_HOOK_IS_SUBAGENT", "true")
	t.Setenv("PATH", home+":/usr/bin:/bin")
	sentinel := filepath.Join(home, "git-wrapper-executed")
	require.NoError(t, os.WriteFile(filepath.Join(home, "git"), []byte("#!/bin/sh\nprintf called > '"+sentinel+"'\nexec /usr/bin/git \"$@\"\n"), 0700))
	input := HookInput{SessionID: "mutation", CWD: home, ToolName: "Bash"}
	input.ToolInput.Command = "printf data > ordinary"
	callback, err := json.Marshal(input)
	require.NoError(t, err)
	var output bytes.Buffer
	{
		status := RunHook(bytes.NewReader(callback), &output, io.Discard)
		require.Falsef(t, status != 0 || output.Len() != 0, "owned write denied: status=%d output=%s", status, &output)
	}
	{
		_, err := os.Stat(sentinel)
		require.Falsef(t, !os.IsNotExist(err), "bookkeeping executed PATH git wrapper: %v", err)
	}
}

// TestHookOwnedLinkCleanup verifies a stale marker does not protect its aliases.
//
// Example: removing or writing a marker alias is not a marker-based denial.
func TestHookOwnedLinkCleanup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ECI_PROOF_ROOT", filepath.Join(home, "proof"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("ECI_HOOK_IS_SUBAGENT", "true")
	markerDir := filepath.Join(home, "proof", "cleanup")
	require.NoError(t, os.MkdirAll(markerDir, 0700))
	marker := filepath.Join(markerDir, "eci_active")
	require.NoError(t, os.WriteFile(marker, []byte("scope: callback\ncwd: "+home+"\nsession_id: cleanup\n"), 0600))
	link := filepath.Join(home, "marker-link")
	hardlink := filepath.Join(home, "marker-hardlink")
	require.NoError(t, os.Symlink(marker, link))
	require.NoError(t, os.Link(marker, hardlink))
	for _, test := range []struct {
		command string
		denied  bool
	}{
		{"rm '" + link + "'", false},
		{"rm '" + hardlink + "'", false},
		{"unlink '" + link + "'", false},
		{"unlink '" + hardlink + "'", false},
		{"mv '" + link + "' '" + filepath.Join(home, "moved-link") + "'", false},
		{"tee '" + link + "'", false},
		{"tee '" + hardlink + "'", false},
		{"rm '" + marker + "'", false},
		{"unlink '" + marker + "'", false},
	} {
		input := HookInput{SessionID: "cleanup", CWD: home, ToolName: "Bash"}
		input.ToolInput.Command = test.command
		callback, err := json.Marshal(input)
		require.NoError(t, err)
		var output bytes.Buffer
		RunHook(bytes.NewReader(callback), &output, io.Discard)
		{
			got := output.Len() != 0
			require.Falsef(t, got != test.denied, "command=%s denied=%v want=%v output=%s", test.command, got, test.denied, &output)
		}
	}
}

// TestRunHookLargeCallback preserves all effects in a complete valid callback.
//
// Example: a large inert string cannot hide a later concrete root removal.
func TestRunHookLargeCallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ECI_PROOF_ROOT", filepath.Join(home, "proof"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("ECI_HOOK_IS_SUBAGENT", "false")
	contextMarker(t, filepath.Join(home, "proof"), "large", home)
	command := "printf '%s' '" + strings.Repeat("x", maxRequestBytes+1) + "'"
	for _, test := range []struct {
		command  string
		trailing string
		denied   bool
		status   int
	}{
		{command, "", false, 0},
		{command + "; rm -rf /", "", true, 0},
		{command + "; rm -rf /", "{}", false, 2},
		{command + "; rm -rf /", "{", false, 2},
	} {
		input := HookInput{SessionID: "large", CWD: home, ToolName: "Bash"}
		input.ToolInput.Command = test.command
		callback, err := json.Marshal(input)
		require.NoError(t, err)
		var output bytes.Buffer
		require.Equal(t, test.status, RunHook(strings.NewReader(string(callback)+test.trailing), &output, io.Discard), "trailing=%q", test.trailing)
		require.Equal(t, test.denied, output.Len() != 0, "trailing=%q", test.trailing)
	}
	var plannerOutput bytes.Buffer
	require.Equal(t, StatusInternal, Run(strings.NewReader(strings.Repeat(" ", maxRequestBytes+1)+"{}"), &plannerOutput))
}

// TestHookControlIndexKeepsAllObservedAliases ignores stale marker inode aliases.
//
// Example: marker and control aliases do not activate a callback denial.
func TestHookControlIndexKeepsAllObservedAliases(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ECI_PROOF_ROOT", filepath.Join(home, "proof"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("ECI_HOOK_IS_SUBAGENT", "true")
	contextMarker(t, filepath.Join(home, "proof"), "controls", home)
	marker := filepath.Join(home, "proof", "controls", "eci_active")
	markerAlias := filepath.Join(home, "marker-alias")
	require.NoError(t, os.Link(marker, markerAlias))
	late := ""
	for index := 0; index < manySessionFixtureEntries; index++ {
		late = filepath.Join(filepath.Dir(marker), fmt.Sprintf("eci_wait.%03d", index))
		require.NoError(t, os.WriteFile(late, []byte("control\n"), 0600))
	}
	lateAlias := filepath.Join(home, "late-alias")
	require.NoError(t, os.Link(late, lateAlias))
	ordinary := filepath.Join(home, "ordinary")
	require.NoError(t, os.WriteFile(ordinary, []byte("ordinary\n"), 0600))
	require.NoError(t, os.Link(ordinary, filepath.Join(home, "ordinary-alias")))
	for _, target := range []string{markerAlias, lateAlias, marker, ordinary} {
		input := HookInput{SessionID: "controls", CWD: home, ToolName: "Bash"}
		input.ToolInput.Command = "tee '" + target + "'"
		callback, err := json.Marshal(input)
		require.NoError(t, err)
		var output bytes.Buffer
		require.Zero(t, RunHook(bytes.NewReader(callback), &output, io.Discard))
		require.Empty(t, output.String(), "stale marker must not protect %s", target)
	}
}

// TestHookMalformedIdentityBlocks verifies malformed callback JSON fails closed.
//
// Example: session_id [] blocks with or without a legacy marker.
func TestHookMalformedIdentityBlocks(t *testing.T) {
	root := t.TempDir()
	t.Setenv("ECI_PROOF_ROOT", root)
	malformed := `{"session_id":[],"tool_name":"Bash","cwd":"/tmp","tool_input":{"command":"true"}}`
	var output bytes.Buffer
	require.Equal(t, 2, RunHook(strings.NewReader(malformed), &output, io.Discard))
	require.Empty(t, output.String())
	require.NoError(t, os.MkdirAll(filepath.Join(root, "scratch-owner"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "scratch-owner", "eci_active"), []byte("scope: x\ncwd: /tmp\nsession_id: scratch-owner\n"), 0600))
	output.Reset()
	require.Equal(t, 2, RunHook(strings.NewReader(malformed), &output, io.Discard))
	require.Empty(t, output.String())
}
