package main

import (
	"path/filepath"
	"testing"
)

func TestReproWorkerRepositoryAllowOnForeignSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	provider := filepath.Join(home, ".omp", "agent", "eci")
	requireHookDirectory(t, filepath.Join(provider, "bin"))
	lifecycle := filepath.Join(provider, "bin", "eci-active")
	requireHookFile(t, lifecycle, "#!/bin/sh\nexit 0\n")
	dep := t.TempDir()
	request := Request{HookMode: true, Provider: ProviderOmp, Role: RoleWorker, Marker: MarkerActive, ActiveSession: "owner", CWD: provider, CollectShellCommands: true, ApprovedRoots: []string{provider}}
	for _, c := range []string{
		`env ECI_SESSION_ID=other "$HOME/.omp/agent/eci/bin/eci-active" repository-allow-on ` + dep + ` "other session"`,
		`env ECI_SESSION_ID=other ` + lifecycle + ` repository-allow-on ` + dep + ` "other session"`,
	} {
		request.Command = c
		d := hookTestDiagnostic(request)
		if d == nil {
			t.Errorf("ADMITTED: %s", c)
		} else {
			t.Logf("denied %s: %s", d.Code, c)
		}
	}
}
