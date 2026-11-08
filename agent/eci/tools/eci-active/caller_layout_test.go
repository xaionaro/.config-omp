package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// A --session-dir transcript binds the account home's stores and walks parents only through the
// caller's own top-level session directory. The store argument names the account home's context
// root; verifyCallerSession only reads it, so nothing is written there.
func TestVerifyCallerSessionSessionDirLineage(t *testing.T) {
	home := accountHome()
	if home == "" {
		t.Skip("no account home")
	}
	store := filepath.Join(home, ".agent", "context", "sessions")
	dir := filepath.Join(t.TempDir(), "session-dir")
	top := filepath.Join(dir, "2026-11-08T00-00-00-000Z_"+ownerRoot)
	root := writeTranscript(t, top+".jsonl", ownerRoot, "")
	child := writeTranscript(t, filepath.Join(top, "Orch.jsonl"), ownerChild, root)
	grand := writeTranscript(t, filepath.Join(top, "Orch", "Orch.Worker.jsonl"), ownerGrand, child)
	foreign := writeTranscript(t, filepath.Join(dir, "elsewhere", staleRoot+".jsonl"), staleRoot, "")
	stray := writeTranscript(t, filepath.Join(top, "Stray.jsonl"), sibling, foreign)
	for _, c := range []struct {
		name, sid, file, want string // want "" accepts
	}{
		{"same session", ownerRoot, root, ""},
		{"child to root", ownerRoot, child, ""},
		{"grandchild to root", ownerRoot, grand, ""},
		{"stale session", staleRoot, child, "[ECI_SESSION_IDENTITY_MISMATCH]"},
		{"parent outside the top-level session directory", staleRoot, stray, "[ECI_SESSION_IDENTITY_MISMATCH]"},
		{"missing transcript", ownerRoot, filepath.Join(top, "absent.jsonl"), "[ECI_SESSION_IDENTITY_UNVERIFIED]"},
	} {
		t.Setenv("ECI_SESSION_ID", c.sid)
		t.Setenv("ECI_THREAD_ID", "")
		t.Setenv("PI_SESSION_ID", "")
		t.Setenv("PI_SESSION_FILE", c.file)
		err := verifyCallerSession("ledger-append", c.sid, store)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v; want accepted", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: %v; want %s", c.name, err, c.want)
		}
	}
}
