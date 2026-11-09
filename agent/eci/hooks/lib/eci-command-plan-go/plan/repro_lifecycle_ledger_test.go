package plan

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Example: a coordinator ledger-append under another ECI_SESSION_ID is denied.
func TestHookLifecycleLedgerAppendForeignSessionDenied(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	provider := filepath.Join(home, ".omp", "agent", "eci")
	requireHookDirectory(t, filepath.Join(provider, "bin"))
	lifecycle := filepath.Join(provider, "bin", "eci-active")
	requireHookFile(t, lifecycle, "#!/bin/sh\nexit 0\n")
	request := Request{HookMode: true, Provider: ProviderOmp, Role: RoleCoordinator, Marker: MarkerActive, ActiveSession: "owner", CWD: provider, CollectShellCommands: true, ApprovedRoots: []string{provider}}
	for _, command := range []string{
		"env ECI_SESSION_ID=foreign " + lifecycle + " off",
		"env ECI_SESSION_ID=foreign " + lifecycle + ` ledger-append --json '{"event":"decision","summary":"mismatch probe"}'`,
		`env ECI_SESSION_ID=foreign "$HOME/.omp/agent/eci/bin/eci-active" ledger-append --json '{"event":"decision","summary":"mismatch probe"}'`,
	} {
		request.Command = command
		diagnostic := hookTestDiagnostic(request)
		require.Truef(t, diagnostic != nil && diagnostic.Code == CodeControlIdentityDenied, "foreign lifecycle session allowed: %s -> %+v", command, diagnostic)
	}
}
