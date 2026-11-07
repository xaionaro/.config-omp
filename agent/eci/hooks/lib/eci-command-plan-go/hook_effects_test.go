package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestHookScratchScriptTargets covers resolved scratch leaves and retained controls.
//
// Example: a private scratch alias admits execution but cannot hide a provider control.
func TestHookScratchScriptTargets(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	volume := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.Symlink(volume, filepath.Join(home, "tmp")))
	script := filepath.Join(volume, "run.sh")
	requireHookFile(t, script, "#!/bin/sh\nprintf ordinary\n")
	alias := filepath.Join(home, "tmp", "alias.sh")
	require.NoError(t, os.Symlink(script, alias))
	outside := filepath.Join(t.TempDir(), "outside.sh")
	requireHookFile(t, outside, "#!/bin/sh\nexit 0\n")
	escaped := filepath.Join(volume, "escaped.sh")
	require.NoError(t, os.Symlink(outside, escaped))
	provider := filepath.Join(home, ".omp", "agent", "eci")
	control := filepath.Join(provider, "hooks", "eci-review-gate.sh")
	lifecycle := filepath.Join(provider, "bin", "eci-active")
	requireHookDirectory(t, filepath.Dir(control))
	requireHookDirectory(t, filepath.Dir(lifecycle))
	requireHookFile(t, control, "#!/bin/sh\nexit 0\n")
	requireHookFile(t, lifecycle, "#!/bin/sh\nexit 0\n")
	controlAlias := filepath.Join(volume, "control.sh")
	lifecycleAlias := filepath.Join(volume, "lifecycle.sh")
	require.NoError(t, os.Symlink(control, controlAlias))
	require.NoError(t, os.Symlink(lifecycle, lifecycleAlias))
	foreignMarker := filepath.Join(home, "proof", "foreign", "eci_active")
	requireHookDirectory(t, filepath.Dir(foreignMarker))
	requireHookFile(t, foreignMarker, "scope: fixture\ncwd: "+cwd+"\nsession_id: foreign\n")
	request := Request{HookMode: true, Provider: ProviderOmp, Role: RoleWorker, Marker: MarkerActive, ActiveSession: "owner", CWD: cwd, CollectShellCommands: true, ApprovedRoots: []string{cwd}, ActiveMarkers: []string{foreignMarker}}
	for _, current := range []struct {
		Command string
		Code    DiagnosticCode
	}{
		{Command: "bash '" + script + "'"},
		{Command: "bash '" + alias + "'"},
		{Command: "'" + alias + "'"},
		{Command: "python3 '" + script + "'"},
		{Command: "bash -n '" + outside + "'"},
		{Command: "bash '" + filepath.Join(volume, "missing.sh") + "'"},
		{Command: "bash '" + volume + "'"},
		{Command: "bash '" + escaped + "'", Code: hookCodeWorkerScriptTargetDenied},
		{Command: "bash '" + controlAlias + "'", Code: CodeControlOwnerRequired},
		{Command: "'" + lifecycleAlias + "' --session foreign off", Code: CodeControlOwnerRequired},
		{Command: "bash '" + alias + "'; rm -rf /", Code: hookCodeCrossSessionActiveMarkerDenied},
		{Command: "bash '" + alias + "'; tee '" + foreignMarker + "'", Code: CodePlanLiveControlDenied},
		{Command: "bash '" + alias + "'; git -C '" + volume + "' add file", Code: CodeWorkerGitOwnershipDenied},
	} {
		request.Command = current.Command
		diagnostic := hookTestDiagnostic(request)
		if current.Code == "" {
			require.Nil(t, diagnostic, current.Command)
			continue
		}
		require.NotNil(t, diagnostic, current.Command)
		require.Equal(t, current.Code, diagnostic.Code, current.Command)
	}
	request.Command = "bash '" + escaped + "'"
	request.ApprovedRoots = append(request.ApprovedRoots, filepath.Dir(outside))
	require.Nil(t, hookTestDiagnostic(request), "independently approved escaped leaf keeps its existing route")
	request.Role = RoleCoordinator
	request.ApprovedRoots = []string{cwd}
	request.Command = "env ECI_SESSION_ID=foreign '" + lifecycleAlias + "' off"
	diagnostic := hookTestDiagnostic(request)
	require.NotNil(t, diagnostic)
	require.Equal(t, CodeControlIdentityDenied, diagnostic.Code)
	request.Command = "bash '" + alias + "'"
	require.Nil(t, hookTestDiagnostic(request))
	request.Command = "git -C '" + volume + "' add file"
	output, err := exec.Command("git", "init", "-q", volume).CombinedOutput()
	require.NoErrorf(t, err, "init scratch fixture: %s", output)
	diagnostic = hookTestDiagnostic(request)
	require.NotNil(t, diagnostic)
	require.Equal(t, hookCodeGitCrossScopeDenied, diagnostic.Code, "scratch execution must not grant repository authority")
	require.Equal(t, []string{cwd}, request.ApprovedRoots)
}

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

// TestHookScratchRootBoundaries rejects broad canonical root aliases.
//
// Example: HOME/tmp pointing at an ancestor of HOME cannot admit another user's script.
func TestHookScratchRootBoundaries(t *testing.T) {
	for _, kind := range []string{"root", "home", "home-ancestor", "shared-tmp", "shared-var-tmp", "shared-ancestor", "approved-root", "approved-ancestor", "private-descendant", "missing-root", "non-directory-root", "loop-root"} {
		t.Run(kind,
			// Verify one configured scratch root against canonical containment boundaries.
			//
			// Example: a dedicated descendant remains distinct from its shared temp parent.
			func(t *testing.T) {
				base := t.TempDir()
				home := filepath.Join(base, "home")
				cwd := filepath.Join(base, "repo")
				requireHookDirectory(t, home)
				requireHookDirectory(t, cwd)
				t.Setenv("HOME", home)
				root := base
				allowed := false
				switch kind {
				case "root":
					root = string(filepath.Separator)
				case "home":
					root = home
				case "home-ancestor":
					root = base
				case "shared-tmp":
					root = "/tmp"
				case "shared-var-tmp":
					root = "/var/tmp"
				case "shared-ancestor":
					root = "/var"
				case "approved-root":
					root = cwd
				case "approved-ancestor":
					root = filepath.Join(base, "volume")
					cwd = filepath.Join(root, "repo")
					requireHookDirectory(t, cwd)
				case "private-descendant":
					root = filepath.Join(base, "private")
					requireHookDirectory(t, root)
					allowed = true
				case "missing-root":
					root = filepath.Join(base, "missing")
				case "non-directory-root":
					root = filepath.Join(base, "file")
					requireHookFile(t, root, "ordinary\n")
				case "loop-root":
					root = filepath.Join(base, "loop")
					require.NoError(t, os.Symlink(root, root))
				}
				require.NoError(t, os.Symlink(root, filepath.Join(home, "tmp")))
				scriptDir := base
				if allowed || kind == "home" || kind == "approved-root" || kind == "approved-ancestor" {
					scriptDir = root
				}
				script := filepath.Join(scriptDir, "run.sh")
				requireHookFile(t, script, "#!/bin/sh\nexit 0\n")
				request := Request{HookMode: true, Provider: ProviderOmp, Role: RoleWorker, Marker: MarkerActive, CWD: cwd, CollectShellCommands: true, ApprovedRoots: []string{cwd}, Command: "bash '" + script + "'"}
				require.Equal(t, allowed, hookScratchScriptAllowed(resolvePathIdentity(script), request.ApprovedRoots), "scratch predicate must remain separate from repository admission")
				// Test shared-root containment without creating files in shared temporary storage.
				if kind == "shared-tmp" || kind == "shared-var-tmp" || kind == "shared-ancestor" {
					require.False(t, hookScratchScriptAllowed(filepath.Join(resolvePathIdentity(root), "run.sh"), request.ApprovedRoots))
				}
				diagnostic := hookTestDiagnostic(request)
				if allowed || kind == "approved-root" {
					require.Nil(t, diagnostic, "dedicated scratch or pre-existing repository admission")
					return
				}
				require.NotNil(t, diagnostic)
				require.Equal(t, hookCodeWorkerScriptTargetDenied, diagnostic.Code)
				request.Command = "bash -n '" + script + "'"
				require.Nil(t, hookTestDiagnostic(request), "root observation failures must not deny syntax-only inspection")
			})
	}
}

// TestHookWorkerGitFamilies verifies native defaults and complete nested routing.
//
// Example: git diff stays native while a later git add uses the typed CLI.
func TestHookWorkerGitFamilies(t *testing.T) {
	root := t.TempDir()
	for _, command := range []string{"git diff --output=diff.txt", "git show --format=%H", "git log --all", "git bisect reset", "git branch feature", "git status", "git restore file", "git restore --staged file", "git restore --staged --worktree file", "git restore --worktree --staged file", "git apply patch", "git apply --index patch", "git apply --cached patch", "git worktree list", "git worktree move old new", "git worktree remove old", "git worktree lock old", "git worktree unlock old", "git worktree repair old", "git worktree prune"} {
		t.Run(command,
			// Verify each native verb keeps its original option semantics.
			//
			// Example: diff --output remains a native command.
			func(t *testing.T) {
				request := Request{HookMode: true, Provider: ProviderOmp, Role: RoleWorker, Marker: MarkerActive, CWD: root, Command: command, CollectShellCommands: true, ApprovedRoots: []string{root}}
				result := Classify(request)
				{
					diagnostic := inspectHookEffects(request, result)
					require.Falsef(t, diagnostic != nil, "native command denied: %+v", diagnostic)
				}
			})
	}
	for _, command := range []string{"git add --help", "git rm --cached file", "git mv old new", "git reset --dry-run", "git checkout topic", "git switch topic", "git update-index --refresh", "git read-tree HEAD", "git merge topic", "git rebase topic", "git cherry-pick -n HEAD", "git revert -n HEAD", "git am patch", "git commit -n -m checkpoint", "git stash", "git pull", "git worktree add next", "git diff; git add file", "git restore --staged file; git stash", "git apply --index patch; git pull", "git worktree repair old; git add file", "bash -c 'git diff; git commit -m checkpoint'", "printf '%s' \"$(git rebase topic)\"", "timeout 5 git add file", "command git checkout file", "env -C " + root + " git add file"} {
		t.Run(command,
			// Verify each reachable selected mutation has the typed route.
			//
			// Example: a nested add cannot disappear behind a native diff.
			func(t *testing.T) {
				request := Request{HookMode: true, Provider: ProviderOmp, Role: RoleWorker, Marker: MarkerActive, CWD: root, Command: command, CollectShellCommands: true, ApprovedRoots: []string{root}}
				result := Classify(request)
				diagnostic := inspectHookEffects(request, result)
				require.Falsef(t, diagnostic == nil || diagnostic.Code != CodeWorkerGitOwnershipDenied, "want typed Worker Git route, got %+v (analysis=%+v)", diagnostic, result.ShellAnalysis)
				require.Falsef(t, !strings.Contains(diagnostic.Remediation, "eci-worker-git") || !strings.Contains(diagnostic.Remediation, "run-once"), "missing bounded hatch: %+v", diagnostic)
			})
	}
}

// TestHookCoordinatorGitTargets verifies concrete scope and broad mutations.
//
// Example: the owner can add one file, but a foreign repository needs declaration.
func TestHookCoordinatorGitTargets(t *testing.T) {
	root := t.TempDir()
	foreign := t.TempDir()
	for _, directory := range []string{root, foreign} {
		{
			output, err := exec.Command("git", "init", "-q", directory).CombinedOutput()
			require.NoErrorf(t, err, "init fixture: %v: %s", err, output)
		}
	}
	request := Request{HookMode: true, Provider: ProviderOmp, Role: RoleCoordinator, Marker: MarkerActive, CWD: root, CollectShellCommands: true, ApprovedRoots: []string{root}}
	request.Command = "git -C " + foreign + " add file"
	{
		diagnostic := inspectHookEffects(request, Classify(request))
		require.Falsef(t, diagnostic == nil || diagnostic.Code != DiagnosticCode("ECI_GIT_CROSS_SCOPE_DENIED"), "foreign mutation scope missing: %+v", diagnostic)
	}
	request.ApprovedRoots = append(request.ApprovedRoots, foreign)
	{
		diagnostic := inspectHookEffects(request, Classify(request))
		require.Falsef(t, diagnostic != nil, "declared repository denied: %+v", diagnostic)
	}
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
		require.Falsef(t, diagnostic == nil || diagnostic.Code != hookCodeGitCrossScopeDenied, "worker cross-repository branch delete allowed: %+v", diagnostic)
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
	request := Request{HookMode: true, Provider: ProviderOmp, Role: RoleWorker, Marker: MarkerActive, CWD: home, CWDKnown: true, CollectShellCommands: true, ApprovedRoots: []string{home}}
	for _, argv := range [][]token{
		{{value: "tee"}, {value: fresh}},
		{{value: "cp"}, {value: filepath.Join(home, "source.txt")}, {value: fresh}},
		{{value: "rm"}, {value: transcript}},
		{{value: "tee"}, {value: filepath.Join(sessions, "slug", "..", "x.jsonl")}},
		{{value: "tee"}, {value: filepath.Join(link, "slug", "new.jsonl")}},
		{{value: "rm"}, {value: "-rf"}, {value: filepath.Join(home, ".omp", "agent")}},
		{{value: "mv"}, {value: sessions}, {value: filepath.Join(home, "elsewhere")}},
	} {
		diagnostic := inspectHookControlWriter(request, argv, 1)
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
		diagnostic := inspectHookControlWriter(request, []token{{value: "tee"}, {value: scratch}}, 1)
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
	// Observed before C1r5 and kept: ~other/... and quoted "~/..." or '~/...' are literal
	// (not HOME-expanded), so the sessions-root check does not fire and no diagnostic is returned.
	for _, command := range []string{
		"echo x > ~other/.omp/agent/sessions/slug/new.jsonl",
		"echo x > \"~/.omp/agent/sessions/slug/new.jsonl\"",
		"echo x > '~/.omp/agent/sessions/slug/new.jsonl'",
	} {
		request.Command = command
		diagnostic := hookTestDiagnostic(request)
		require.Falsef(t, diagnostic != nil, "literal tilde command denied: %s: %+v", command, diagnostic)
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
		require.NotNil(t, diagnostic, command)
		require.Equal(t, hookCodeCommitProducerRequired, diagnostic.Code, command)
	}
	for _, command := range []string{"git commit --dry-run -m checkpoint", "git commit -m checkpoint --dry-run"} {
		request := Request{HookMode: true, Provider: ProviderOmp, Role: RoleCoordinator, Marker: MarkerActive, CWD: root, Command: command, CollectShellCommands: true}
		require.Nil(t, inspectHookEffects(request, Classify(request)), command)
	}
}

// TestHookLifecycleAndScriptTargets verifies concrete execution ownership.
//
// Example: an existing foreign script is denied until its repository is declared.
func TestHookLifecycleAndScriptTargets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	provider := filepath.Join(home, ".omp", "agent", "eci")
	requireHookDirectory(t, filepath.Join(provider, "bin"))
	lifecycle := filepath.Join(provider, "bin", "eci-active")
	requireHookFile(t, lifecycle, "#!/bin/sh\nexit 0\n")
	other := t.TempDir()
	script := filepath.Join(other, "check.sh")
	requireHookFile(t, script, "#!/bin/sh\nexit 0\n")
	request := Request{HookMode: true, Provider: ProviderOmp, Role: RoleWorker, Marker: MarkerActive, ActiveSession: "owner", CWD: provider, CollectShellCommands: true, ApprovedRoots: []string{provider}}
	for _, command := range []string{lifecycle + " status", lifecycle + " --help", lifecycle + " repository-allow-on " + other + " reason", "bash -n " + script, "bash -nx " + script, "bash --unknown-option " + script} {
		request.Command = command
		{
			diagnostic := inspectHookEffects(request, Classify(request))
			require.Falsef(t, diagnostic != nil, "legitimate route %s denied: %+v", command, diagnostic)
		}
	}
	request.Command = lifecycle + " off"
	{
		diagnostic := hookTestDiagnostic(request)
		require.Falsef(t, diagnostic == nil || diagnostic.Code != CodeControlOwnerRequired, "Worker lifecycle mutation was not routed: %+v", diagnostic)
	}
	request.Command = "bash " + script
	{
		diagnostic := inspectHookEffects(request, Classify(request))
		require.Falsef(t, diagnostic == nil || diagnostic.Code != DiagnosticCode("ECI_WORKER_SCRIPT_TARGET_DENIED"), "foreign script was not denied: %+v", diagnostic)
	}
	request.Command = "bash -O extglob " + script
	{
		diagnostic := inspectHookEffects(request, Classify(request))
		require.Falsef(t, diagnostic == nil || diagnostic.Code != hookCodeWorkerScriptTargetDenied, "shell option value obscured the script target: %+v", diagnostic)
	}
	request.ApprovedRoots = append(request.ApprovedRoots, other)
	{
		diagnostic := inspectHookEffects(request, Classify(request))
		require.Falsef(t, diagnostic != nil, "declared script target denied: %+v", diagnostic)
	}
	installer := filepath.Join(provider, "hooks", "install-pre-commit-go-mod.sh")
	requireHookDirectory(t, filepath.Dir(installer))
	requireHookFile(t, installer, "#!/bin/sh\nexit 0\n")
	request.Command = "bash " + installer
	{
		diagnostic := inspectHookEffects(request, Classify(request))
		require.Falsef(t, diagnostic == nil || diagnostic.Code != DiagnosticCode("ECI_WORKER_HOOK_INSTALLER_DENIED"), "provider installer ownership missing: %+v", diagnostic)
	}
	request.Role = RoleCoordinator
	request.Command = "bash " + script
	request.ApprovedRoots = []string{provider}
	{
		diagnostic := inspectHookEffects(request, Classify(request))
		require.Falsef(t, diagnostic == nil || diagnostic.Code != DiagnosticCode("ECI_COORDINATOR_SCRIPT_TARGET_DENIED"), "coordinator foreign script boundary missing: %+v", diagnostic)
	}
	request.ApprovedRoots = append(request.ApprovedRoots, other)
	{
		diagnostic := inspectHookEffects(request, Classify(request))
		require.Falsef(t, diagnostic != nil, "declared coordinator dependency script denied: %+v", diagnostic)
	}
	request.Command = "env ECI_SESSION_ID=foreign " + lifecycle + " off"
	{
		diagnostic := inspectHookEffects(request, Classify(request))
		require.Falsef(t, diagnostic == nil || diagnostic.Code != CodeControlIdentityDenied, "foreign lifecycle session target allowed: %+v analysis=%+v", diagnostic, Classify(request).ShellAnalysis)
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
	require.Falsef(t, diagnostic == nil || diagnostic.Path != resolvePathIdentity(other), "want selected target %s, got %+v", other, diagnostic)
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
		require.Falsef(t, diagnostic == nil || diagnostic.Code != hookCodeGitCrossScopeDenied, "absolute repository evidence lost: %+v", diagnostic)
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

// TestHookControlWriterTargets distinguishes owned notes and foreign live state.
//
// Example: an ordinary project file stays writable while a sibling marker does not.
func TestHookControlWriterTargets(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "owner", "eci_active")
	foreign := filepath.Join(root, "other", "eci_active")
	requireHookDirectory(t, filepath.Dir(current))
	requireHookDirectory(t, filepath.Dir(foreign))
	requireHookFile(t, current, "scope: fixture\ncwd: "+root+"\nsession_id: owner\n")
	requireHookFile(t, foreign, "scope: fixture\ncwd: "+root+"\nsession_id: other\n")
	request := Request{HookMode: true, Provider: ProviderOmp, Role: RoleWorker, Marker: MarkerActive, ActiveSession: "owner", CWD: root, CWDKnown: true, ActiveMarkers: []string{current, foreign}}
	for _, target := range []string{filepath.Join(root, "file"), filepath.Join(root, "owner", "project-understanding.yaml")} {
		{
			diagnostic := inspectHookControlWriter(request, []token{{value: "touch"}, {value: target}}, 1)
			require.Falsef(t, diagnostic != nil, "owned target denied: %+v", diagnostic)
		}
	}
	for _, target := range []string{current, filepath.Join(root, "owner", "eci_wait"), foreign, filepath.Join(root, "other", "project-understanding.yaml")} {
		{
			diagnostic := inspectHookControlWriter(request, []token{{value: "tee"}, {value: target}}, 1)
			require.Falsef(t, diagnostic == nil, "control target %s allowed", target)
		}
	}
	{
		diagnostic := inspectHookControlWriter(request, []token{{value: "rm"}, {value: "-rf"}, {value: root}}, 1)
		require.False(t, diagnostic == nil, "ancestor removal with live markers allowed")
	}
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
