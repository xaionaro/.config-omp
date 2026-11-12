package plan

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestHookMarkerFreeConcreteGitEffects preserves broad Git safeguards without
// inferring ECI lifecycle state from a retired marker.
//
// Example: a whole-index add is denied while an exact-path add remains native.
func TestHookMarkerFreeConcreteGitEffects(t *testing.T) {
	root := t.TempDir()
	for _, current := range []struct {
		command string
		denied  bool
	}{
		{command: "git add -A", denied: true},
		{command: "git reset --hard"},
		{command: "git reset --hard HEAD"},
		{command: "git reset"},
		{command: "git reset -- ."},
		{command: "git add -- owned.txt"},
		{command: "git status"},
	} {
		request := Request{HookMode: true, Provider: ProviderOmp, Role: RoleWorker, Marker: MarkerInactive, CWD: root, CWDKnown: true, Command: current.command, CollectShellCommands: true}
		diagnostic := inspectHookEffects(request, Classify(request))
		if current.denied {
			require.NotNil(t, diagnostic, current.command)
			require.Equal(t, CodeBroadDestructiveDenied, diagnostic.Code, current.command)
		} else {
			require.Nil(t, diagnostic, current.command)
		}
	}
}

// TestHookWorkerGitWorktreeAddStaysNative verifies that worktree add has no typed route and stays native.
//
// Example: git worktree add next is an ordinary native Worker command, not a typed-route denial.
func TestHookWorkerGitWorktreeAddStaysNative(t *testing.T) {
	root := t.TempDir()
	for _, command := range []string{"git worktree add next", "git worktree add --detach next HEAD"} {
		t.Run(command,
			// Verify worktree add stays native without a typed-route denial.
			//
			// Example: a detached worktree add is accepted by the native Worker path.
			func(t *testing.T) {
				request := Request{HookMode: true, Provider: ProviderOmp, Role: RoleWorker, Marker: MarkerActive, CWD: root, Command: command, CollectShellCommands: true, ApprovedRoots: []string{root}}
				diagnostic := inspectHookEffects(request, Classify(request))
				require.Nilf(t, diagnostic, "native git worktree add denied: %+v", diagnostic)
			})
	}
}

// TestHookCoordinatorGitTargets preserves narrow and broad Git effect coverage.
//
// Example: exact file additions remain distinct from whole-worktree additions.
func TestHookCoordinatorGitTargets(t *testing.T) {
	root := t.TempDir()
	foreign := t.TempDir()
	request := Request{HookMode: true, Provider: ProviderOmp, Role: RoleCoordinator, Marker: MarkerInactive, CWD: root, CollectShellCommands: true}
	for _, command := range []string{"git add file", "git add -A file", "git reset -- file", "git reset file", "git reset --hard", "git reset", "git reset -- .", "git -C " + foreign + " diff --output=result"} {
		request.Command = command
		{
			diagnostic := inspectHookEffects(request, Classify(request))
			require.Falsef(t, diagnostic != nil, "scoped/read-only command %s denied: %+v", command, diagnostic)
		}
	}
	for _, command := range []string{"git add -A", "git add -A -- ."} {
		request.Command = command
		{
			diagnostic := inspectHookEffects(request, Classify(request))
			require.Falsef(t, diagnostic == nil || diagnostic.Code != CodeBroadDestructiveDenied, "broad mutation %s allowed: %+v", command, diagnostic)
		}
	}
}

// TestHookWorkerGitMutationScope applies Coordinator-only Git mutation denials to Worker mutations.
//
// Example: a Worker clean -fdx is denied while read-only status and log stay native.
func TestHookWorkerGitMutationScope(t *testing.T) {
	root := t.TempDir()
	foreign := t.TempDir()
	for _, directory := range []string{root, foreign} {
		output, err := exec.Command("git", "init", "-q", directory).CombinedOutput()
		require.NoErrorf(t, err, "init fixture: %v: %s", err, output)
	}
	request := Request{HookMode: true, Provider: ProviderOmp, Role: RoleWorker, Marker: MarkerActive, CWD: root, CollectShellCommands: true, ApprovedRoots: []string{root}}
	for _, command := range []string{"git clean -fdx", "git clean -fd"} {
		request.Command = command
		diagnostic := hookTestDiagnostic(request)
		require.Falsef(t, diagnostic == nil || diagnostic.Code != CodeBroadDestructiveDenied, "worker clean allowed: %s: %+v", command, diagnostic)
	}
	request.Command = "git -C " + foreign + " branch -D topic"
	{
		diagnostic := hookTestDiagnostic(request)
		require.Nilf(t, diagnostic, "foreign narrow operation denied: %+v", diagnostic)
	}
	for _, command := range []string{"git status", "git log --oneline", "git -C " + foreign + " log --oneline"} {
		request.Command = command
		{
			diagnostic := hookTestDiagnostic(request)
			require.Falsef(t, diagnostic != nil, "read-only worker Git denied: %s: %+v", command, diagnostic)
		}
	}
}

// TestHookWorkerSessionsWritesDenied keeps omp session transcripts out of Worker writes and removals.
//
// Example: a Worker tee into the sessions root is denied while a scratch write stays allowed.
func TestHookWorkerSessionsWritesDenied(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sessions := filepath.Join(home, ".omp", "agent", "sessions")
	transcript := filepath.Join(sessions, "slug", "main.jsonl")
	fresh := filepath.Join(sessions, "slug", "new.jsonl")
	scratch := filepath.Join(home, "tmp", "zz.txt")
	requireHookDirectory(t, filepath.Dir(transcript))
	requireHookFile(t, transcript, "{}\n")
	requireHookDirectory(t, filepath.Dir(scratch))
	link := filepath.Join(home, "sessions-link")
	require.NoError(t, os.Symlink(sessions, link))
	request := Request{HookMode: true, Provider: ProviderOmp, Role: RoleWorker, Marker: MarkerInactive, CWD: home, CWDKnown: true, CollectShellCommands: true}
	for _, argv := range [][]token{
		{{value: "tee"}, {value: fresh}},
		{{value: "cp"}, {value: filepath.Join(home, "source.txt")}, {value: fresh}},
		{{value: "rm"}, {value: transcript}},
		{{value: "tee"}, {value: filepath.Join(sessions, "slug", "..", "x.jsonl")}},
		{{value: "tee"}, {value: filepath.Join(link, "slug", "new.jsonl")}},
		{{value: "rm"}, {value: "-rf"}, {value: filepath.Join(home, ".omp", "agent")}},
		{{value: "mv"}, {value: sessions}, {value: filepath.Join(home, "elsewhere")}},
	} {
		diagnostic := inspectHookSessionsWriter(request, argv, 1)
		require.Falsef(t, diagnostic == nil || diagnostic.Code != hookCodeWorkerSessionsRootDenied, "sessions writer allowed: %+v", diagnostic)
	}
	for _, command := range []string{
		"echo x > " + fresh,
		"printf x >> " + transcript,
		"echo x > $HOME/.omp/agent/sessions/slug/new.jsonl",
		"tee " + filepath.Join(sessions, "slug", "..", "x.jsonl"),
		"cp " + filepath.Join(home, "source.txt") + " " + fresh,
		"rm " + transcript,
	} {
		request.Command = command
		diagnostic := hookTestDiagnostic(request)
		require.Falsef(t, diagnostic == nil || diagnostic.Code != hookCodeWorkerSessionsRootDenied, "sessions command allowed: %s: %+v", command, diagnostic)
	}
	{
		diagnostic := inspectHookSessionsWriter(request, []token{{value: "tee"}, {value: scratch}}, 1)
		require.Falsef(t, diagnostic != nil, "scratch write denied: %+v", diagnostic)
	}
	request.Command = "echo x > " + scratch
	{
		diagnostic := hookTestDiagnostic(request)
		require.Falsef(t, diagnostic != nil, "scratch command denied: %+v", diagnostic)
	}
	// An unquoted leading tilde expands to HOME, so the sessions-root check denies it.
	for _, command := range []string{
		"echo x > ~/.omp/agent/sessions/slug/new.jsonl",
		"tee ~/.omp/agent/sessions/slug/new.jsonl",
		"rm -rf ~/.omp/agent",
	} {
		request.Command = command
		diagnostic := hookTestDiagnostic(request)
		require.Falsef(t, diagnostic == nil || diagnostic.Code != hookCodeWorkerSessionsRootDenied, "unquoted tilde sessions command allowed: %s: %+v", command, diagnostic)
	}
	// Unresolved spellings are advisory rather than evidence of a target.
	for _, command := range []string{
		"echo x > ~/.omp/agent/sess[s]ions/slug/new.jsonl",
		"tee ~/.omp/agent/sess*/slug/new.jsonl",
		"tee ~/.omp/agent/{sessions,eci}/slug/new.jsonl",
		"tee ~pheona/.omp/agent/sessions/slug/new.jsonl",
		"tee ~+/.omp/agent/sessions/slug/new.jsonl",
		"tee ~-/.omp/agent/sessions/slug/new.jsonl",
		"echo x > ~other/.omp/agent/sessions/slug/new.jsonl",
		"tee ~/\".omp/agent/sessions/slug/new.jsonl\"",
		"tee ~/.omp/agent/x/../sessions/y.jsonl",
	} {
		request.Command = command
		diagnostic := hookTestDiagnostic(request)
		require.Nilf(t, diagnostic, "ambiguous sessions command denied: %s: %+v", command, diagnostic)
	}
	// Quoted "~/..." and '~/...', and a quote inside the tilde-prefix (~"/..."), are literal:
	// they keep the observed C1r5 behavior and return no diagnostic.
	for _, command := range []string{
		"echo x > \"~/.omp/agent/sessions/slug/new.jsonl\"",
		"echo x > '~/.omp/agent/sessions/slug/new.jsonl'",
		"echo x > ~\"/.omp/agent/sessions/slug/new.jsonl\"",
	} {
		request.Command = command
		diagnostic := hookTestDiagnostic(request)
		require.Falsef(t, diagnostic != nil, "literal tilde command denied: %s: %+v", command, diagnostic)
	}
	// With an unknown CWD, an operand that mentions the sessions root denies; other relative operands are not resolved.
	unknown := request
	unknown.CWDKnown = false
	for _, argv := range [][]token{
		{{value: "tee"}, {value: ".omp/agent/sessions/slug/new.jsonl"}},
	} {
		diagnostic := inspectHookSessionsWriter(unknown, argv, 1)
		require.Nilf(t, diagnostic, "unknown-CWD sessions mention denied: %+v", diagnostic)
	}
	{
		diagnostic := inspectHookSessionsWriter(unknown, []token{{value: "tee"}, {value: "relative/scratch.txt"}}, 1)
		require.Falsef(t, diagnostic != nil, "unknown-CWD non-sessions operand denied: %+v", diagnostic)
	}
	// Removing a symlink removes the entry only: a dangling ENOTDIR link and a loop are not followed.
	badlink := filepath.Join(home, "tmp", "badlink")
	loop1 := filepath.Join(home, "tmp", "loop1")
	loop2 := filepath.Join(home, "tmp", "loop2")
	require.NoError(t, os.Symlink("/etc/hostname/x", badlink))
	require.NoError(t, os.Symlink(loop2, loop1))
	require.NoError(t, os.Symlink(loop1, loop2))
	for _, command := range []string{"rm " + badlink, "unlink " + badlink, "rm " + loop1, "unlink " + loop2} {
		request.Command = command
		diagnostic := hookTestDiagnostic(request)
		require.Falsef(t, diagnostic != nil && diagnostic.Code == hookCodeWorkerSessionsRootDenied, "symlink removal denied: %s: %+v", command, diagnostic)
	}
	for _, argv := range [][]token{
		{{value: "rm"}, {value: badlink}},
		{{value: "unlink"}, {value: loop2}},
	} {
		diagnostic := inspectHookSessionsWriter(request, argv, 1)
		require.Falsef(t, diagnostic != nil && diagnostic.Code == hookCodeWorkerSessionsRootDenied, "symlink removal denied: %+v", diagnostic)
	}
	// An absolute operand does not depend on the CWD, so an unknown CWD does not deny it; only a
	// relative operand that mentions the root does.
	absolute := filepath.Join(home, "tmp", "session-notes.txt")
	unknown.Command = "tee " + absolute
	{
		diagnostic := hookTestDiagnostic(unknown)
		require.Falsef(t, diagnostic != nil && diagnostic.Code == hookCodeWorkerSessionsRootDenied, "unknown-CWD absolute operand denied: %+v", diagnostic)
	}
	for _, argv := range [][]token{
		{{value: "tee"}, {value: filepath.Join(home, ".omp", "agent", "notes.txt")}},
	} {
		diagnostic := inspectHookSessionsWriter(unknown, argv, 1)
		require.Falsef(t, diagnostic != nil && diagnostic.Code == hookCodeWorkerSessionsRootDenied, "unknown-CWD absolute operand denied: %+v", diagnostic)
	}
	{
		diagnostic := inspectHookSessionsWriter(unknown, []token{{value: "tee"}, {value: filepath.Join(sessions, "slug", "new.jsonl")}}, 1)
		require.Falsef(t, diagnostic == nil || diagnostic.Code != hookCodeWorkerSessionsRootDenied, "unknown-CWD absolute sessions write allowed: %+v", diagnostic)
	}
	// The mention predicate is the literal ".omp" only: names that spell only "sess" do not mention
	// the root and pass; a glob that keeps ".omp" still denies.
	for _, argv := range [][]token{
		{{value: "tee"}, {value: "session-*.txt"}},
		{{value: "tee"}, {value: "session{1,2}.txt"}},
		{{value: "tee"}, {value: "sess.txt"}},
	} {
		diagnostic := inspectHookSessionsWriter(request, argv, 1)
		require.Falsef(t, diagnostic != nil && diagnostic.Code == hookCodeWorkerSessionsRootDenied, "sess-only operand denied: %+v", diagnostic)
	}
	request.Command = "tee ~/.omp/*/x.txt"
	{
		diagnostic := hookTestDiagnostic(request)
		require.Nilf(t, diagnostic, "ambiguous .omp glob denied: %+v", diagnostic)
	}
}

// TestHookGitPreviewSemantics preserves native commit invocations.
//
// Example: commit -n bypasses hooks without selecting a broad Git effect.
func TestHookGitPreviewSemantics(t *testing.T) {
	root := t.TempDir()
	for _, command := range []string{"git commit -n -m checkpoint", "git commit -m --dry-run", "git commit -- --dry-run", "git commit -m --help"} {
		request := Request{HookMode: true, Provider: ProviderOmp, Role: RoleCoordinator, Marker: MarkerActive, CWD: root, Command: command, CollectShellCommands: true}
		diagnostic := inspectHookEffects(request, Classify(request))
		require.Nil(t, diagnostic, command)
	}
	for _, command := range []string{"git commit --dry-run -m checkpoint", "git commit -m checkpoint --dry-run"} {
		request := Request{HookMode: true, Provider: ProviderOmp, Role: RoleCoordinator, Marker: MarkerActive, CWD: root, Command: command, CollectShellCommands: true}
		require.Nil(t, inspectHookEffects(request, Classify(request)), command)
	}
}

// requireHookDirectory creates a fixture directory and reports setup failures.
//
// Example: a fixture lifecycle executable needs an existing bin directory.
func requireHookDirectory(
	t *testing.T,
	path string,
) {
	t.Helper()
	require.NoError(t, os.MkdirAll(path, 0o700))
}

// requireHookFile creates an executable fixture without invoking its contents.
//
// Example: script ownership tests resolve a shebang file on disk.
func requireHookFile(
	t *testing.T,
	path string,
	contents string,
) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o700))
}

// TestHookGitDirectoryFacts verifies resolved directory and argument uncertainty.
//
// Example: an absolute -C still identifies a target after an unknown cd.
func TestHookGitDirectoryFacts(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	request := Request{HookMode: true, Provider: ProviderOmp, Role: RoleWorker, Marker: MarkerActive, CWD: root, ApprovedRoots: []string{root}}
	result := Result{ShellAnalysis: &ShellAnalysis{Commands: []ShellCommandRecord{{Argv: []string{"git", "-C", other, "add", "file"}, CWD: root, CWDKnown: true, Reachability: segmentReachable}}}}
	diagnostic := inspectHookEffects(request, result)
	require.Nilf(t, diagnostic, "exact Git operation denied: %+v", diagnostic)
	result.ShellAnalysis.Commands[0].UnknownArguments = []int{3}
	{
		diagnostic := inspectHookEffects(request, result)
		require.Falsef(t, diagnostic != nil, "unknown verb denied: %+v", diagnostic)
	}
	result.ShellAnalysis.Commands[0].Reachability = segmentUnreachable
	{
		diagnostic := inspectHookEffects(request, result)
		require.Falsef(t, diagnostic != nil, "unreachable record denied: %+v", diagnostic)
	}
}

// TestHookGitAbsoluteContextWithUnknownCWD retains absolute selector evidence.
//
// Example: git -C /dependency add file selects that repository after unknown cd.
func TestHookGitAbsoluteContextWithUnknownCWD(t *testing.T) {
	root := t.TempDir()
	foreign := t.TempDir()
	{
		output, err := exec.Command("git", "init", "-q", foreign).CombinedOutput()
		require.NoErrorf(t, err, "init fixture: %v: %s", err, output)
	}
	request := Request{HookMode: true, Provider: ProviderOmp, Role: RoleCoordinator, Marker: MarkerActive, CWD: root, ApprovedRoots: []string{root}}
	result := Result{ShellAnalysis: &ShellAnalysis{Commands: []ShellCommandRecord{{Argv: []string{"git", "-C", foreign, "add", "file"}, CWDKnown: false, Reachability: segmentReachable}}}}
	{
		diagnostic := inspectHookEffects(request, result)
		require.Nilf(t, diagnostic, "foreign exact Git operation denied: %+v", diagnostic)
	}
}

// hookTestDiagnostic retains planner-owned denials before callback-owned checks.
//
// Example: a canonical lifecycle owner diagnostic can precede shell publication.
func hookTestDiagnostic(request Request) *Diagnostic {
	result := Classify(request)
	if result.Diagnostic != nil {
		return result.Diagnostic
	}
	return inspectHookEffects(request, result)
}

// TestHookCleanupTargets denies removal of live Bash-gate files while retaining ordinary cleanup.
//
// Example: deleting a generated cache leaf is harmless; deleting the live validate-bash.sh is not.
func TestHookCleanupTargets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	provider := filepath.Join(home, ".omp", "agent", "eci")
	hook := filepath.Join(provider, "hooks", "validate-bash.sh")
	guard := filepath.Join(provider, "hooks", "lib", "eci-bash-guard.sh")
	other := filepath.Join(provider, "hooks", "eci-review-gate.sh")
	clone := filepath.Join(home, "tmp", "omp-clone", "agent", "eci", "hooks", "validate-bash.sh")
	adapter := filepath.Join(home, ".omp", "agent", "hooks", "pre", "eci-bash-gate.ts")
	for _, file := range []string{hook, guard, other, clone, adapter} {
		requireHookDirectory(t, filepath.Dir(file))
		requireHookFile(t, file, "#!/bin/sh\nexit 0\n")
	}
	alias := filepath.Join(provider, "owned-hook-alias")
	require.NoError(t, os.Symlink(hook, alias))
	request := Request{HookMode: true, Provider: ProviderOmp, Role: RoleWorker, Marker: MarkerActive, CWD: provider, CollectShellCommands: true, ApprovedRoots: []string{provider}}
	for _, command := range []string{
		"rm -f generated.cache", "rm -rf bin/__pycache__", "rm -rf missing-directory", "rm -f " + alias,
		"rm -f " + other, "rm -f " + clone, "rm -rf " + filepath.Dir(filepath.Dir(clone)),
		"mv " + filepath.Join(provider, "new-hook") + " " + hook,
		hookLiveRemovalAcknowledgment + " rm -f " + hook,
		"env " + hookLiveRemovalAcknowledgment + " mv " + hook + " " + filepath.Join(provider, "saved-hook"),
		// Disabling the omp Bash adapter is the user's bridge and stays native.
		"mkdir -p " + filepath.Join(home, ".omp", "agent", "hooks-disabled", "pre") + " && mv " + adapter + " " + filepath.Join(home, ".omp", "agent", "hooks-disabled", "pre") + "/",
	} {
		request.Command = command
		{
			diagnostic := inspectHookEffects(request, Classify(request))
			require.Falsef(t, diagnostic != nil, "ordinary or acknowledged cleanup denied: %s: %+v", command, diagnostic)
		}
	}
	// A relative removal in a clone with the live layout is ordinary multi-repo work.
	cloneRequest := request
	cloneRequest.CWD = filepath.Dir(filepath.Dir(clone))
	cloneRequest.Command = "rm -f hooks/validate-bash.sh"
	{
		diagnostic := inspectHookEffects(cloneRequest, Classify(cloneRequest))
		require.Nilf(t, diagnostic, "clone removal denied: %+v", diagnostic)
	}
	for _, command := range []string{
		"rm -f " + hook, "rm -f -- " + guard, "rm -rf " + provider, "unlink " + hook, "shred -u " + alias,
		"mv " + hook + " " + filepath.Join(provider, "saved-hook"),
		"mv -t " + provider + " " + hook,
		"rm -f hooks/validate-bash.sh",
	} {
		request.Command = command
		{
			diagnostic := inspectHookEffects(request, Classify(request))
			require.Falsef(t, diagnostic == nil || diagnostic.Code != hookCodeLiveBashGateRemoval, "live Bash-gate removal allowed: %s: %+v", command, diagnostic)
			for _, hatch := range []string{"Edit or Write", "git rm --cached", hookLiveRemovalAcknowledgment} {
				require.Contains(t, diagnostic.Remediation, hatch, command)
			}
			for _, checkout := range []string{"clone", "worktree"} {
				require.NotContains(t, diagnostic.Remediation, checkout, command)
			}
		}
	}
}

// TestHookGitLiveGateRemoval denies physical Git removal of live Bash-gate files only.
//
// Example: git rm of the live hook is denied; git rm --cached, checkout and other repositories are not.
func TestHookGitLiveGateRemoval(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := filepath.Join(home, ".omp")
	dependency := filepath.Join(home, "dependency")
	for _, file := range []string{
		filepath.Join(repo, "agent", "eci", "hooks", "validate-bash.sh"),
		filepath.Join(dependency, "agent", "eci", "hooks", "validate-bash.sh"),
	} {
		requireHookDirectory(t, filepath.Dir(file))
		requireHookFile(t, file, "hook")
	}
	for _, directory := range []string{repo, dependency} {
		output, err := hookGitOutput(context.Background(), directory, "init")
		require.NoErrorf(t, err, "%s", output)
	}
	for _, tc := range []struct {
		command string
		denied  bool
	}{
		{"git rm agent/eci/hooks/validate-bash.sh", true},
		{"git rm -r -- agent/eci/hooks", true},
		{"git mv agent/eci/hooks/validate-bash.sh saved", true},
		{"git -C agent/eci mv hooks/validate-bash.sh saved", true},
		{"git rm --cached agent/eci/hooks/validate-bash.sh", false},
		{"git rm -n agent/eci/hooks/validate-bash.sh", false},
		{"git rm -rn -- agent/eci/hooks", false},
		{"git mv -nk agent/eci/hooks/validate-bash.sh saved", false},
		{"git rm -rf -- agent/eci/hooks", true},
		{"git mv saved agent/eci/hooks/validate-bash.sh", false},
		{"git checkout -- agent/eci/hooks/validate-bash.sh", false},
		{"git restore -- agent/eci/hooks/validate-bash.sh", false},
		{hookLiveRemovalAcknowledgment + " git rm agent/eci/hooks/validate-bash.sh", false},
		{"git -C '" + dependency + "' rm agent/eci/hooks/validate-bash.sh", false},
		{"git -C '" + dependency + "' mv agent/eci/hooks/validate-bash.sh saved", false},
		{"git -C '" + dependency + "' checkout -- agent/eci/hooks/validate-bash.sh", false},
		{"git add -A", true},
	} {
		t.Run(tc.command,
			// Check one Git command against the callback-owned effect gates.
			//
			// Example: a whole-tree add stays denied.
			func(t *testing.T) {
				request := Request{HookMode: true, Provider: ProviderOmp, Role: RoleWorker, Marker: MarkerInactive, CWD: repo, CWDKnown: true, Command: tc.command, CollectShellCommands: true}
				diagnostic := inspectHookEffects(request, Classify(request))
				if tc.denied {
					require.NotNil(t, diagnostic)
				} else {
					require.Nil(t, diagnostic)
				}
			})
	}
}

// TestHookMakeMemoryCap pins which make launches lack a finite memory cap.
//
// Example: systemd-run without MemoryMax is denied while MemoryMax=1G passes.
func TestHookMakeMemoryCap(t *testing.T) {
	denied := [][]string{
		{"make"},
		{"env", "make"},
		{"sudo", "make"},
		{"sudo", "-u", "root", "make"},
		{"command", "--", "make"},
		{"exec", "--", "make"},
		{"time", "make"},
		{"time", "-f", "%e", "make"},
		{"nice", "make"},
		{"timeout", "30s", "make"},
		{"bash", "-c", "make"},
		{"bash", "-lc", "make test"},
		{"sh", "-c", "make"},
		{"bash", "-o", "pipefail", "-c", "make"},
		{"xargs", "make"},
		{"find", ".", "-exec", "make", ";"},
		{"systemd-run", "--scope", "make"},
		{"systemd-run", "-u", "build", "make"},
		{"systemd-run", "--scope", "-p", "MemoryHigh=1G", "make"},
		{"prlimit", "--as=unlimited", "make"},
	}
	allowed := [][]string{
		{"systemd-run", "--scope", "--property=MemoryMax=1G", "make"},
		{"systemd-run", "--scope", "-p", "MemoryMax=1G", "make"},
		{"systemd-run", "-u", "build", "-p", "MemoryMax=1G", "make"},
		{"prlimit", "--as=1073741824", "make"},
		{"prlimit", "-v1048576", "make"},
		{"cmake", "--version"},
		{"printf", "make"},
		{"echo", "make"},
		{"command", "-v", "make"},
		{"find", ".", "-name", "make", "-print"},
		{"xargs", "echo", "make"},
	}
	for _, argv := range denied {
		require.True(t, hookMakeUncapped(argv), "expected uncapped: %v", argv)
	}
	for _, argv := range allowed {
		require.False(t, hookMakeUncapped(argv), "expected capped or unrelated: %v", argv)
	}
}

// TestHookMakeMemoryCapPlatform keeps Linux enforcement while disabling the
// gate on Darwin, where its launcher routes have not been verified.
//
// Example: uncapped make is denied on Linux but not on Darwin.
func TestHookMakeMemoryCapPlatform(t *testing.T) {
	t.Setenv("PATH", "")

	for _, tc := range []struct {
		goos   string
		argv   []string
		denied bool
	}{
		{"linux", []string{"make"}, true},
		{"linux", []string{"prlimit", "--as=1073741824", "make"}, false},
		{"darwin", []string{"make"}, false},
		{"darwin", []string{"env", "make"}, false},
		{"freebsd", []string{"make"}, true},
	} {
		t.Run(tc.goos+"/"+strings.Join(tc.argv, "_"),
			// Check the selected platform's diagnostic for this launch.
			//
			// Example: Darwin make produces no cap diagnostic.
			func(t *testing.T) {
				diagnostic := inspectHookMakeMemoryCapForOS(ShellCommandRecord{Argv: tc.argv}, tc.goos)
				require.Equal(t, tc.denied, diagnostic != nil)
			})
	}
}
