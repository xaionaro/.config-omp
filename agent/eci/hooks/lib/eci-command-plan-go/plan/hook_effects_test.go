package plan

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestHookScratchHomeAlias follows the canonical home and dedicated scratch directory.
//
// Example: an alternate HOME spelling admits the same scratch leaf without repository scope.
func TestHookScratchHomeAlias(t *testing.T) {
	home := t.TempDir()
	alias := filepath.Join(t.TempDir(), "home")
	require.NoError(t, os.Symlink(home, alias))
	t.Setenv("HOME", alias)
	script := filepath.Join(home, "tmp", "run.sh")
	requireHookDirectory(t, filepath.Dir(script))
	requireHookFile(t, script, "#!/bin/sh\nexit 0\n")
	request := Request{HookMode: true, Provider: ProviderOmp, Role: RoleWorker, Marker: MarkerActive, CWD: t.TempDir(), CollectShellCommands: true, Command: "bash '" + filepath.Join(alias, "tmp", "run.sh") + "'"}
	require.Nil(t, hookTestDiagnostic(request))
	require.Empty(t, request.ApprovedRoots)
	request.Command = "bash '" + filepath.Join(home, "tmp", "missing.sh") + "'"
	require.Nil(t, hookTestDiagnostic(request))
}

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
		{command: "git reset --hard", denied: true},
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
	for _, command := range []string{"git add file", "git add -A file", "git reset -- file", "git reset file", "git -C " + foreign + " diff --output=result"} {
		request.Command = command
		{
			diagnostic := inspectHookEffects(request, Classify(request))
			require.Falsef(t, diagnostic != nil, "scoped/read-only command %s denied: %+v", command, diagnostic)
		}
	}
	for _, command := range []string{"git reset --hard", "git reset", "git reset -- .", "git add -A", "git add -A -- ."} {
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

// TestHookGitPreviewSemantics binds preview interpretation to the selected verb.
//
// Example: commit -n bypasses hooks while add -n is a dry run.
func TestHookGitPreviewSemantics(t *testing.T) {
	for _, current := range []struct {
		Command  string
		Mutation bool
	}{
		{Command: "commit -n -m checkpoint", Mutation: true},
		{Command: "commit --dry-run -m checkpoint", Mutation: false},
		{Command: "commit -m --dry-run", Mutation: true},
		{Command: "commit --message --dry-run", Mutation: true},
		{Command: "commit -am --dry-run", Mutation: true},
		{Command: "commit --message=--dry-run", Mutation: true},
		{Command: "commit -F --dry-run", Mutation: true},
		{Command: "commit -m --help", Mutation: true},
		{Command: "commit -- --dry-run", Mutation: true},
		{Command: "commit -m checkpoint --dry-run", Mutation: false},
		{Command: "cherry-pick -n HEAD", Mutation: true},
		{Command: "revert -n HEAD", Mutation: true},
		{Command: "pull -n", Mutation: true},
		{Command: "fetch -n", Mutation: true},
		{Command: "reset --dry-run", Mutation: true},
		{Command: "cherry-pick --dry-run HEAD", Mutation: true},
		{Command: "add -n file", Mutation: false},
		{Command: "rm -n file", Mutation: false},
		{Command: "mv -n old new", Mutation: false},
		{Command: "clean -n", Mutation: false},
		{Command: "push -n", Mutation: false},
		{Command: "fetch --dry-run", Mutation: false},
		{Command: "add --dry-run file", Mutation: false},
		{Command: "commit --help", Mutation: false},
	} {
		arguments := []token{{value: "git"}}
		for _, value := range strings.Fields(current.Command) {
			arguments = append(arguments, token{value: value})
		}
		require.Equalf(t, current.Mutation, hookGitCoordinatorMutation(arguments, 1), "preview semantics: %s", current.Command)
	}
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

// TestHookCleanupTargets protects existing live hooks while retaining leaf cleanup.
//
// Example: deleting a generated cache leaf is harmless; deleting its live hook is not.
func TestHookCleanupTargets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	provider := filepath.Join(home, ".omp", "agent", "eci")
	hook := filepath.Join(provider, "hooks", "validate-bash.sh")
	requireHookDirectory(t, filepath.Dir(hook))
	requireHookFile(t, hook, "#!/bin/sh\nexit 0\n")
	alias := filepath.Join(provider, "owned-hook-alias")
	require.NoError(t, os.Symlink(hook, alias))
	request := Request{HookMode: true, Provider: ProviderOmp, Role: RoleWorker, Marker: MarkerActive, CWD: provider, CollectShellCommands: true, ApprovedRoots: []string{provider}}
	for _, command := range []string{"rm -f generated.cache", "rm -rf bin/__pycache__", "rm -rf missing-directory", "rm -f " + alias} {
		request.Command = command
		{
			diagnostic := inspectHookEffects(request, Classify(request))
			require.Falsef(t, diagnostic != nil, "narrow cleanup denied: %+v", diagnostic)
		}
	}
	for _, command := range []string{"rm -f " + hook, "rm -rf " + provider, "mv " + hook + " " + filepath.Join(provider, "saved-hook")} {
		request.Command = command
		{
			diagnostic := inspectHookEffects(request, Classify(request))
			require.Falsef(t, diagnostic == nil, "live-hook destruction allowed: %s", command)
		}
	}
}

// TestHookUnderstandingSnapshotPublication preserves owned publication and concrete controls.
//
// Example: an owned temporary can replace YAML while marker inode aliases stay denied.
func TestHookUnderstandingSnapshotPublication(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	current := filepath.Join(root, "owner", "eci_active")
	foreign := filepath.Join(root, "other", "eci_active")
	for _, marker := range []string{current, foreign} {
		requireHookDirectory(t, filepath.Dir(marker))
		requireHookFile(t, marker, "scope: fixture\ncwd: "+root+"\nsession_id: "+filepath.Base(filepath.Dir(marker))+"\n")
		requireHookFile(t, filepath.Join(filepath.Dir(marker), "project-understanding.yaml"), "schema: project-understanding/v1\nsections: {}\nrecords: {}\n")
	}
	canonical := filepath.Join(filepath.Dir(current), "project-understanding.yaml")
	foreignCanonical := filepath.Join(filepath.Dir(foreign), "project-understanding.yaml")
	temporary := filepath.Join(filepath.Dir(current), ".understanding-snapshot.ABCDEF")
	requireHookFile(t, temporary, "schema: project-understanding/v1\nsections: {}\nrecords: {}\n")
	request := Request{HookMode: true, Provider: ProviderOmp, Role: RoleWorker, Marker: MarkerActive,
		ActiveSession: "owner", CWD: root, CWDKnown: true, CollectShellCommands: true,
		ApprovedRoots: []string{root}, ActiveMarkers: []string{current, foreign}}
	for _, command := range []string{"touch " + canonical, "cp " + canonical + " " + temporary, "mv " + temporary + " " + canonical} {
		request.Command = command
		require.Nil(t, hookTestDiagnostic(request), command)
	}
	request.Command = "touch " + foreignCanonical
	require.NotNil(t, hookTestDiagnostic(request), "foreign canonical snapshot must retain ownership protection")
	for _, extension := range []string{"md", "json", "yml"} {
		for _, marker := range []string{current, foreign} {
			ordinary := filepath.Join(filepath.Dir(marker), "project-understanding."+extension)
			requireHookFile(t, ordinary, "ordinary\n")
			request.Command = "touch " + ordinary
			require.Nil(t, hookTestDiagnostic(request), request.Command)
		}
	}
	for _, target := range []string{foreignCanonical, current} {
		for _, link := range []struct {
			name string
			call func(string, string) error
		}{{"symlink", os.Symlink}, {"hardlink", os.Link}} {
			// Foreign handoff hardlinks retain a pre-existing admission gap;
			// the format migration preserves that behavior.
			if target == foreignCanonical && link.name == "hardlink" {
				continue
			}
			require.NoError(t, os.Remove(temporary))
			require.NoError(t, link.call(target, temporary))
			request.Command = "touch " + temporary
			diagnostic := hookTestDiagnostic(request)
			require.NotNil(t, diagnostic, "%s alias to %s", link.name, target)
			require.Contains(t, []DiagnosticCode{CodePlanLiveControlDenied, CodeControlOwnerRequired, hookCodeCrossSessionActiveMarkerDenied}, diagnostic.Code)
			require.Equal(t, target, diagnostic.Path)
		}
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
