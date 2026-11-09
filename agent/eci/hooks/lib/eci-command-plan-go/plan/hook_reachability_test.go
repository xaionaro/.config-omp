package plan

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHookReachabilityAfterHomeWrapper keeps branch status independent of CWD uncertainty.
//
// Example: a HOME-expanded wrapper followed by false && git add leaves the add unreachable.
func TestHookReachabilityAfterHomeWrapper(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ECI_PROOF_ROOT", t.TempDir())
	request := Request{Provider: ProviderOmp, Role: RoleWorker, Marker: MarkerActive, ActiveSession: "reachability", CWD: cwd, HookMode: true, CollectShellCommands: true}
	prefix := `"$HOME/.omp/agent/eci/bin/eci-worker-git" --repo "$HOME/project" run-once --reason "authorized disposable status fixture" --user-authorized -- status --short; `
	tests := []struct {
		name    string
		command string
		denied  bool
	}{
		{name: "literal false and", command: "false && git add ordinary"},
		{name: "literal true or", command: "true || git add ordinary"},
		{name: "literal true and", command: "true && git add ordinary", denied: true},
		{name: "literal false or", command: "false || git add ordinary", denied: true},
		{name: "unknown or false and", command: "unknown || false && git add ordinary", denied: true},
		{name: "unknown and true or", command: "unknown && true || git add ordinary", denied: true},
	}
	for _, test := range tests {
		t.Run(test.name,
			// Each branch exercises the callback classifier and its complete effect inspection.
			//
			// Example: unknown-status branches retain a possible native Git mutation.
			func(t *testing.T) {
				request.Command = prefix + test.command
				result := Classify(request)
				require.NotEqual(t, DecisionError, result.Decision)
				require.NotNil(t, result.ShellAnalysis)
				diagnostic := inspectHookEffects(request, result)
				assert.Equal(t, test.denied, result.Decision == DecisionDeny || diagnostic != nil, "result=%+v diagnostic=%+v", result, diagnostic)
				if test.denied {
					require.NotNil(t, diagnostic)
					assert.Equal(t, CodeWorkerGitOwnershipDenied, diagnostic.Code)
				}
				require.NotNil(t, result.Plan)
				require.NotEmpty(t, result.Plan.Segments)
				finalSegment := result.Plan.Segments[len(result.Plan.Segments)-1]
				assert.True(t, finalSegment.CWDUnknown, "dynamic prefix must retain CWD uncertainty")
				if !test.denied {
					assert.Equal(t, segmentUnreachable, finalSegment.Reachability)
				}
				foundGit := false
				for _, record := range result.ShellAnalysis.Commands {
					if len(record.Argv) < 2 || record.Argv[0] != "git" || record.Argv[1] != "add" {
						continue
					}
					foundGit = true
					assert.False(t, record.CWDKnown, "dynamic prefix must retain explicit CWD uncertainty")
					if !test.denied {
						assert.Equal(t, segmentUnreachable, record.Reachability)
					}
				}
				if test.denied {
					require.True(t, foundGit, "reachable Git effect must remain available")
				}
			},
		)
	}
}

// TestHookReachabilityUntrackedBranches preserves effects from omitted branch states.
//
// Example: a state-cap overflow cannot prove a command dead using only retained skipped states.
func TestHookReachabilityUntrackedBranches(t *testing.T) {
	skipped := compoundReachableCWDState{states: []timeoutReplayState{{skipped: true}}, unknown: true}
	assert.Equal(t, segmentUnreachable, compoundReachabilityOf(skipped))
	empty := compoundReachableCWDState{unknown: true}
	assert.Equal(t, segmentReachabilityUnknown, compoundReachabilityOf(empty))
	joined := joinCompoundReachableCWDStates(skipped, empty)
	assert.True(t, joined.untrackedBranches)
	assert.Equal(t, segmentReachabilityUnknown, compoundReachabilityOf(joined))
	branches := make([]timeoutReplayState, maxReachableStates+1)
	for index := range branches {
		branches[index] = timeoutReplayState{cwd: fmt.Sprintf("/candidate/%d", index), skipped: true}
	}
	overflow := joinCompoundReachableCWDStates(skipped, compoundReachableCWDState{states: branches})
	assert.True(t, overflow.untrackedBranches)
	assert.Equal(t, segmentReachabilityUnknown, compoundReachabilityOf(overflow))
	advanced := advanceCompoundReachableCWDState(empty, segment{argv: []token{{value: "false"}}}, "&&")
	assert.True(t, advanced.untrackedBranches)
	assert.Equal(t, segmentReachabilityUnknown, compoundReachabilityOf(advanced))
}
