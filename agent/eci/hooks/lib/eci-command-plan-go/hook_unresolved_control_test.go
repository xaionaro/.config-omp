package main

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestHookUnresolvedControlTargetKeepsIdentity keeps provider-control identity
// checks when the executable or script word cannot be resolved.
//
// Example: env ECI_SESSION_ID=foreign "$HOME/.omp/agent/eci/bin/eci-active" off
// is denied like its absolute form, while the owner session stays allowed.
func TestHookUnresolvedControlTargetKeepsIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	provider := filepath.Join(home, ".omp", "agent", "eci")
	requireHookDirectory(t, filepath.Join(provider, "bin"))
	requireHookDirectory(t, filepath.Join(provider, "hooks"))
	lifecycle := filepath.Join(provider, "bin", "eci-active")
	reviewGate := filepath.Join(provider, "hooks", "eci-review-gate.sh")
	stopGate := filepath.Join(provider, "hooks", "stop-gate.sh")
	for _, path := range []string{lifecycle, reviewGate, stopGate} {
		requireHookFile(t, path, "#!/bin/sh\nexit 0\n")
	}
	dependency := t.TempDir()
	ledger := ` ledger-append --json '{"event":"decision","summary":"mismatch probe"}'`
	type row struct {
		name      string
		role      Role
		command   string
		code      DiagnosticCode
		predicate string
	}
	var rows []row
	deny := func(name string, role Role, command string, code DiagnosticCode, predicate string) {
		rows = append(rows, row{name: name, role: role, command: command, code: code, predicate: predicate})
	}
	allow := func(name string, role Role, command string) {
		rows = append(rows, row{name: name, role: role, command: command})
	}
	spellings := []string{
		lifecycle,
		`"$HOME/.omp/agent/eci/bin/eci-active"`,
		`"$HOME"/.omp/agent/eci/bin/eci-active`,
		`$HOME/.omp/agent/eci/bin/eci-active`,
		`${HOME}/.omp/agent/eci/bin/eci-active`,
		`~/.omp/agent/eci/bin/eci-active`,
		`"$ECI_HOME/bin/eci-active"`,
	}
	for _, spelling := range spellings {
		deny("ledger foreign env", RoleCoordinator, "env ECI_SESSION_ID=foreign "+spelling+ledger, CodeControlIdentityDenied, "lifecycle-session")
		deny("off foreign assignment", RoleCoordinator, "ECI_SESSION_ID=foreign "+spelling+" off", CodeControlIdentityDenied, "lifecycle-session")
		deny("off foreign flag", RoleCoordinator, spelling+" off --session foreign", CodeControlIdentityDenied, "lifecycle-session")
		deny("off foreign sh -c", RoleCoordinator, "env ECI_SESSION_ID=foreign sh -c '"+spelling+" off'", CodeControlIdentityDenied, "lifecycle-session")
		deny("worker allow-on foreign", RoleWorker, "env ECI_SESSION_ID=other "+spelling+" repository-allow-on "+dependency+` "other session"`, CodeControlIdentityDenied, "lifecycle-session")
		deny("worker off", RoleWorker, "env "+spelling+" off", CodeControlOwnerRequired, "worker-lifecycle-control")
		allow("ledger owner env", RoleCoordinator, "env ECI_SESSION_ID=owner "+spelling+ledger)
		allow("off owner flag", RoleCoordinator, spelling+" off --session owner")
		allow("status foreign", RoleCoordinator, "env ECI_SESSION_ID=foreign "+spelling+" status")
		allow("ledger owner assignment", RoleCoordinator, "ECI_SESSION_ID=owner "+spelling+ledger)
		allow("ledger exported session", RoleCoordinator, spelling+ledger)
		allow("status", RoleCoordinator, spelling+" status")
		allow("help", RoleCoordinator, spelling+" --help")
		allow("worker allow-on owner", RoleWorker, "env ECI_SESSION_ID=owner "+spelling+" repository-allow-on "+dependency+` "same session"`)
		allow("worker allow-on", RoleWorker, spelling+" repository-allow-on "+dependency+` "same session"`)
		for _, verb := range []string{"maintain-planner", "sync-runtime"} {
			allow(verb+" wrong session", RoleCoordinator, "ECI_SESSION_ID=wrong-session "+spelling+" "+verb)
			allow(verb+" wrong session extra", RoleCoordinator, "ECI_SESSION_ID=wrong-session "+spelling+" "+verb+" extra")
			allow(verb+" kimi wrong session", RoleCoordinator, "KIMI_SESSION_ID=wrong-session "+spelling+" "+verb)
			allow(verb+" foreign flag", RoleCoordinator, spelling+" "+verb+" --session wrong-session")
		}
		allow("sync-runtime invalid env", RoleCoordinator, "env ECI_SESSION_ID=invalid/session "+spelling+" sync-runtime")
		allow("sync-runtime invalid env extra", RoleCoordinator, "env ECI_SESSION_ID=invalid/session "+spelling+" sync-runtime extra")
		deny("ledger wrong session control", RoleCoordinator, "ECI_SESSION_ID=wrong-session "+spelling+ledger, CodeControlIdentityDenied, "lifecycle-session")
		deny("worker maintain-planner wrong session", RoleWorker, "ECI_SESSION_ID=wrong-session "+spelling+" maintain-planner", CodeControlOwnerRequired, "worker-lifecycle-control")
	}
	for _, spelling := range []string{spellings[0], spellings[1], spellings[2], spellings[5], spellings[6]} {
		deny("bash off foreign", RoleCoordinator, "env ECI_SESSION_ID=foreign bash "+spelling+" off", CodeControlIdentityDenied, "lifecycle-session")
		allow("bash off owner", RoleCoordinator, "env ECI_SESSION_ID=owner bash "+spelling+" off")
	}
	deny("PATH foreign", RoleCoordinator, `env PATH="$HOME/.omp/agent/eci/bin" ECI_SESSION_ID=foreign eci-active off`, CodeControlIdentityDenied, "lifecycle-session")
	deny("unknown cd foreign", RoleCoordinator, `cd "$DIRECTORY" && env ECI_SESSION_ID=foreign ./eci-active off`, CodeControlIdentityDenied, "lifecycle-session")
	deny("absolute cd foreign", RoleCoordinator, "cd "+filepath.Join(provider, "bin")+" && env ECI_SESSION_ID=foreign ./eci-active off", CodeControlIdentityDenied, "lifecycle-session")
	allow("PATH owner", RoleCoordinator, `env PATH="$HOME/.omp/agent/eci/bin" ECI_SESSION_ID=owner eci-active off`)
	allow("lookalike notes", RoleCoordinator, `"$HOME/tmp/eci-active-notes" off`)
	allow("unknown tool", RoleCoordinator, `"$TOOL" off`)
	allow("worker scratch script", RoleWorker, `bash "$HOME/tmp/check.sh"`)
	for _, gate := range []string{reviewGate, `"$HOME/.omp/agent/eci/hooks/eci-review-gate.sh"`, `~/.omp/agent/eci/hooks/eci-review-gate.sh`} {
		deny("review foreign", RoleCoordinator, "bash "+gate+" review foreign", CodeControlIdentityDenied, "review-session")
		allow("review owner", RoleCoordinator, "bash "+gate+" review owner")
	}
	for _, gate := range []string{stopGate, `"$HOME/.omp/agent/eci/hooks/stop-gate.sh"`, `~/.omp/agent/eci/hooks/stop-gate.sh`} {
		deny("worker stop-gate bash", RoleWorker, "bash "+gate, CodeControlOwnerRequired, "")
		deny("worker stop-gate direct", RoleWorker, gate, CodeControlOwnerRequired, "")
	}
	outside := t.TempDir()
	outsideScript := filepath.Join(outside, "check.py")
	requireHookFile(t, outsideScript, "print(1)\n")
	deny("absolute interpreter outside", RoleWorker, filepath.Join(home, "venv", "bin", "python3")+" "+outsideScript, hookCodeWorkerScriptTargetDenied, "script-target")
	for _, role := range []Role{RoleCoordinator, RoleWorker} {
		allow("unresolved venv interpreter", role, `"$VENV"/bin/python3 `+outsideScript)
		allow("home venv interpreter", role, `"$HOME/venv/bin/python3" `+outsideScript)
		allow("tilde node", role, `~/.nvm/x/node `+outsideScript)
	}
	for index, current := range rows {
		t.Run(fmt.Sprintf("%03d_%s", index, current.name), func(t *testing.T) {
			request := Request{HookMode: true, Provider: ProviderOmp, Role: current.role, Marker: MarkerActive, ActiveSession: "owner", CWD: provider, CollectShellCommands: true, ApprovedRoots: []string{provider}, Command: current.command}
			diagnostic := hookTestDiagnostic(request)
			if current.code == "" {
				require.Nilf(t, diagnostic, "allowed route denied: %s", current.command)
				return
			}
			require.NotNilf(t, diagnostic, "control route admitted: %s", current.command)
			require.Equalf(t, current.code, diagnostic.Code, "%s -> %+v", current.command, diagnostic)
			if current.predicate != "" {
				require.Equalf(t, current.predicate, diagnostic.Predicate, "%s -> %+v", current.command, diagnostic)
			}
		})
	}
}
