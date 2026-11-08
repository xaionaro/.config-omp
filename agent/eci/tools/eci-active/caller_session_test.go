package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Session ids of the harness fixture. ownerRoot is the calling top-level session; staleRoot is
// an unrelated top-level session whose id a copied environment or resumed shell still exports.
const (
	ownerRoot  = "01aaaaaa-0000-7000-8000-000000000001"
	staleRoot  = "01bbbbbb-0000-7000-8000-000000000002"
	ownerChild = "01aaaaaa-0000-7000-8000-0000000000c1"
	ownerGrand = "01aaaaaa-0000-7000-8000-0000000000c2"
	sibling    = "01aaaaaa-0000-7000-8000-0000000000c3"
	recovery   = "01aaaaaa-0000-7000-8000-0000000000r1"
)

// harness is an omp sessions tree under the fixture HOME: ownerRoot with a child, a grandchild and
// a sibling child, an unrelated staleRoot, and a recovery agent whose parentSession is a bare id.
type harness struct {
	root, child, grand, sib, stale, recovery string // transcript paths
}

// writeTranscript writes an omp transcript: a title record, then the session header. An empty
// parent omits parentSession.
func writeTranscript(t *testing.T, path, id, parent string) string {
	t.Helper()
	header := `{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-11-08T00:00:00.000Z","cwd":"/tmp"`
	if parent != "" {
		header += `,"parentSession":"` + parent + `"`
	}
	content := `{"type":"title","v":1,"title":""}` + "\n" + header + "}\n" + `{"type":"model_change","id":"x"}` + "\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newHarness(t *testing.T, home string) harness {
	t.Helper()
	sessions := filepath.Join(home, ".omp", "agent", "sessions", "-tmp")
	rootDir := filepath.Join(sessions, "2026-11-08T00-00-00-000Z_"+ownerRoot)
	var h harness
	h.root = writeTranscript(t, rootDir+".jsonl", ownerRoot, "")
	h.child = writeTranscript(t, filepath.Join(rootDir, "Orch.jsonl"), ownerChild, h.root)
	h.grand = writeTranscript(t, filepath.Join(rootDir, "Orch", "Orch.Worker.jsonl"), ownerGrand, h.child)
	h.sib = writeTranscript(t, filepath.Join(rootDir, "Peer.jsonl"), sibling, h.root)
	h.recovery = writeTranscript(t, filepath.Join(rootDir, "Recover.jsonl"), recovery, staleRoot)
	h.stale = writeTranscript(t, filepath.Join(sessions, "2026-11-07T00-00-00-000Z_"+staleRoot+".jsonl"), staleRoot, "")
	return h
}

// callerEnv is the per-call identity the omp bash tool injects. Empty values stand for an absent harness identity.
func callerEnv(id, file string) []string {
	return []string{"PI_SESSION_ID=" + id, "PI_SESSION_FILE=" + file}
}

// realStores points the proof root at the fixture HOME default, so proof-root verbs write into the harness home's own store.
func realStores(fx wrapperFixture) []string {
	return []string{"ECI_PROOF_ROOT=" + filepath.Join(fx.home, "tmp", "agent", "proof")}
}

func mkdirAll(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func requireAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("%s exists (Lstat err %v); the refused call changed state", path, err)
	}
}

func requireRefused(t *testing.T, code int, stderr, want string, fragments ...string) {
	t.Helper()
	if code != 1 {
		t.Errorf("exit %d, want 1; stderr %q", code, stderr)
	}
	for _, f := range append([]string{want}, fragments...) {
		if !strings.Contains(stderr, f) {
			t.Errorf("stderr %q does not contain %q", stderr, f)
		}
	}
}

const appendEvent = `{"event":"decision","summary":"identity probe"}`

// A stale exported ECI_SESSION_ID that names an unrelated session is refused before any ledger,
// anchor, context directory or status-report change. HEAD appends into the stale session's log.
func TestCallerSessionLedgerAppendRefusesStaleExportedSession(t *testing.T) {
	fx := newWrapperFixture(t)
	h := newHarness(t, fx.home)
	_, stderr, code := fx.runAs(t, staleRoot, cwdNow(t), callerEnv(ownerRoot, h.root), "ledger-append", "--json", appendEvent)
	requireRefused(t, code, stderr, "[ECI_SESSION_IDENTITY_MISMATCH]",
		"ECI_SESSION_ID="+staleRoot, ownerRoot, "unset ECI_SESSION_ID ECI_THREAD_ID", "no state was changed",
		"set ECI_SESSION_ID="+ownerRoot+" (the calling session)")
	requireAbsent(t, ctxDir(fx, staleRoot))
	fx.assertWrapperCalls(t)
}

// The same refusal for ECI_THREAD_ID when ECI_SESSION_ID is empty; the message names the selected variable.
func TestCallerSessionThreadIDRefusesStaleExportedSession(t *testing.T) {
	fx := newWrapperFixture(t)
	h := newHarness(t, fx.home)
	_, stderr, code := fx.runAs(t, "", cwdNow(t), append(callerEnv(ownerRoot, h.root), "ECI_THREAD_ID="+staleRoot),
		"ledger-append", "--json", appendEvent)
	requireRefused(t, code, stderr, "[ECI_SESSION_IDENTITY_MISMATCH]", "ECI_THREAD_ID="+staleRoot)
	requireAbsent(t, ctxDir(fx, staleRoot))
}

// status-refresh and requirement-assignment write the stale session's report and snapshot on HEAD.
func TestCallerSessionReportAndRequirementRefuseStaleExportedSession(t *testing.T) {
	fx := newWrapperFixture(t)
	h := newHarness(t, fx.home)
	request := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(request, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"status-refresh", "--event", "decision"},
		{"requirement-assignment", "--request", request},
	} {
		_, stderr, code := fx.runAs(t, staleRoot, cwdNow(t), callerEnv(ownerRoot, h.root), args...)
		requireRefused(t, code, stderr, "[ECI_SESSION_IDENTITY_MISMATCH]", args[0])
	}
	requireAbsent(t, ctxDir(fx, staleRoot))
	fx.assertWrapperCalls(t)
}

// Proof-root mutators in the harness home's default proof root: repository-allow-on/off,
// permissive-off session and manifest-write leave the stale session's records unchanged.
func TestCallerSessionProofRecordsRefuseStaleExportedSession(t *testing.T) {
	fx := newWrapperFixture(t)
	h := newHarness(t, fx.home)
	stores := realStores(fx)
	staleProof := mkdirAll(t, filepath.Join(fx.home, "tmp", "agent", "proof", staleRoot))
	work := gitInit(t, filepath.Join(t.TempDir(), "work"))
	dependency := gitInit(t, filepath.Join(t.TempDir(), "dep"))
	env := append(callerEnv(ownerRoot, h.root), stores...)

	_, stderr, code := fx.runAs(t, staleRoot, work, env, "repository-allow-on", dependency, "probe reason")
	requireRefused(t, code, stderr, "[ECI_SESSION_IDENTITY_MISMATCH]", "repository-allow-on")
	requireAbsent(t, filepath.Join(staleProof, repoRecordName))

	keep := map[string]string{
		filepath.Join(staleProof, repoRecordName): "schema: eci-additional-repository/v1\nsession_id: " + staleRoot +
			"\ncwd: " + work + "\nrepository: " + dependency + "\nreason: probe reason\nstate: active\n",
		filepath.Join(staleProof, permissiveRecordName): "schema: eci-permissive-mode/v2\n",
	}
	for path, content := range keep {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, stderr, code = fx.runAs(t, staleRoot, work, env, "repository-allow-off", dependency)
	requireRefused(t, code, stderr, "[ECI_SESSION_IDENTITY_MISMATCH]", "repository-allow-off")
	_, stderr, code = fx.runAs(t, staleRoot, work, env, "permissive-off", "session", "gate", "op", "request")
	requireRefused(t, code, stderr, "[ECI_SESSION_IDENTITY_MISMATCH]", "permissive-off")
	for path, content := range keep {
		if data, err := os.ReadFile(path); err != nil || string(data) != content {
			t.Errorf("%s changed by a refused call: %q, %v", path, data, err)
		}
	}

	// A stub importer that publishes the leaf shows HEAD writing the stale session's manifest.
	writeExecutable(t, filepath.Join(filepath.Dir(fx.binary), "eci-safe-import"),
		"#!/bin/sh\nwhile [ $# -gt 0 ]; do [ \"$1\" = --session-dir ] && d=$2; shift; done\n: > \"$d/eci-required-critics.json\"\n")
	source := filepath.Join(t.TempDir(), "src.json")
	if err := os.WriteFile(source, []byte(`{"required_critics":[]}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, stderr, code = fx.runAs(t, staleRoot, work, env, "manifest-write", source)
	requireRefused(t, code, stderr, "[ECI_SESSION_IDENTITY_MISMATCH]", "manifest-write")
	requireAbsent(t, filepath.Join(staleProof, singletonManifestName))
}

// A sibling child is not an ancestor: Peer cannot write Orch's session, and the eval-kernel shape
// (PI_SESSION_FILE without PI_SESSION_ID) is checked the same way.
func TestCallerSessionRefusesNonAncestorSessions(t *testing.T) {
	fx := newWrapperFixture(t)
	h := newHarness(t, fx.home)
	for name, env := range map[string][]string{
		"sibling":         callerEnv(sibling, h.sib),
		"transcript only": callerEnv("", h.sib),
		// The transcript header names the caller; an inherited PI_SESSION_ID naming the target does not count.
		"inherited PI_SESSION_ID": callerEnv(ownerChild, h.sib),
	} {
		_, stderr, code := fx.runAs(t, ownerChild, cwdNow(t), env, "ledger-append", "--json", appendEvent)
		if code != 1 || !strings.Contains(stderr, "[ECI_SESSION_IDENTITY_MISMATCH]") {
			t.Errorf("%s: exit %d stderr %q; want the identity refusal", name, code, stderr)
		}
	}
	requireAbsent(t, ctxDir(fx, ownerChild))
}

// A harness identity that cannot be verified fails closed: the transcript is missing.
func TestCallerSessionUnverifiableHarnessIdentityFailsClosed(t *testing.T) {
	fx := newWrapperFixture(t)
	h := newHarness(t, fx.home)
	for name, env := range map[string][]string{
		"missing file": callerEnv(ownerRoot, filepath.Join(filepath.Dir(h.root), "absent.jsonl")),
	} {
		_, stderr, code := fx.runAs(t, ownerRoot, cwdNow(t), env, "ledger-append", "--json", appendEvent)
		if code != 1 || !strings.Contains(stderr, "[ECI_SESSION_IDENTITY_UNVERIFIED]") {
			t.Errorf("%s: exit %d stderr %q; want the unverified refusal", name, code, stderr)
		}
	}
	requireAbsent(t, ctxDir(fx, ownerRoot))
}

// A parentSession cycle terminates and does not admit an unrelated session.
func TestCallerSessionParentCycleTerminates(t *testing.T) {
	fx := newWrapperFixture(t)
	dir := filepath.Join(fx.home, ".omp", "agent", "sessions", "-tmp")
	a := filepath.Join(dir, "a.jsonl")
	b := filepath.Join(dir, "b.jsonl")
	writeTranscript(t, a, "cycle-a", b)
	writeTranscript(t, b, "cycle-b", a)
	_, stderr, code := fx.runAs(t, staleRoot, cwdNow(t), callerEnv("cycle-a", a), "ledger-append", "--json", appendEvent)
	requireRefused(t, code, stderr, "[ECI_SESSION_IDENTITY_MISMATCH]")
	requireAbsent(t, ctxDir(fx, staleRoot))
}

// Legitimate uses that must keep working, before and after the fix: the caller's own session,
// Supervisor children and grandchildren recording into the root ledger, a recovery agent writing
// the session its harness header names, calls outside an omp bash tool call, fixtures in a private
// store, and read-only verbs.
func TestCallerSessionKeepsLegitimateUse(t *testing.T) {
	fx := newWrapperFixture(t)
	h := newHarness(t, fx.home)
	for _, c := range []struct {
		name, sid string
		env       []string
	}{
		{"same session", ownerRoot, callerEnv(ownerRoot, h.root)},
		{"supervisor child to root", ownerRoot, callerEnv(ownerChild, h.child)},
		{"grandchild to root", ownerRoot, callerEnv(ownerGrand, h.grand)},
		{"grandchild to parent", ownerChild, callerEnv(ownerGrand, h.grand)},
		{"eval kernel own session", ownerChild, callerEnv("", h.child)},
		{"eval kernel with an inherited PI_SESSION_ID", ownerRoot, callerEnv(staleRoot, h.root)},
		{"--no-session caller with a private HOME", staleRoot, callerEnv(ownerRoot, "")},
		{"recovery agent to recovered session", staleRoot, callerEnv(recovery, h.recovery)},
		{"outside an omp tool call", staleRoot, callerEnv("", "")},
	} {
		_, stderr, code := fx.runAs(t, c.sid, cwdNow(t), c.env, "ledger-append", "--json", appendEvent)
		if code != 0 {
			t.Errorf("%s: exit %d stderr %q; want success", c.name, code, stderr)
		}
	}

	// A fixture with a private HOME is not the harness home's store: the real caller identity does not bind it.
	private := mkdirAll(t, filepath.Join(filepath.Dir(fx.home), "private-home"))
	_, stderr, code := fx.runAs(t, "fixture-session", cwdNow(t),
		append(callerEnv(ownerRoot, h.root), "HOME="+private), "ledger-append", "--json", appendEvent)
	if code != 0 {
		t.Errorf("private store: exit %d stderr %q; want success", code, stderr)
	}
	// A private proof root with the harness HOME leaves proof-root verbs unbound (the fixture proof root).
	mkdirAll(t, filepath.Join(fx.proof, "fixture-session"))
	_, stderr, code = fx.runAs(t, "fixture-session", cwdNow(t), callerEnv(ownerRoot, h.root),
		"permissive-off", "session", "gate", "op", "request")
	if code != 0 {
		t.Errorf("private proof root: exit %d stderr %q; want success", code, stderr)
	}
	// Read-only verbs are not identity-checked.
	_, stderr, code = fx.runAs(t, staleRoot, cwdNow(t), append(callerEnv(ownerRoot, h.root), realStores(fx)...), "repository-allow-status")
	if code != 0 || strings.Contains(stderr, "IDENTITY") {
		t.Errorf("repository-allow-status: exit %d stderr %q", code, stderr)
	}
}

// ECI_THREAD_ID selects another session while ECI_SESSION_ID is absent from the environment
// (not merely empty): the thread fallback is checked against the caller like the session id.
func TestCallerSessionThreadIDRefusedWithSessionIDUnset(t *testing.T) {
	fx := newWrapperFixture(t)
	h := newHarness(t, fx.home)
	cmd := exec.Command(fx.binary, "ledger-append", "--json", appendEvent)
	cmd.Dir = cwdNow(t)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "ECI_SESSION_ID=") && !strings.HasPrefix(kv, "ECI_THREAD_ID=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env,
		"HOME="+fx.home,
		"ECI_PROOF_ROOT="+fx.proof,
		"ECI_THREAD_ID="+staleRoot,
		"PATH="+fx.shimDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GO_LOG="+fx.goLog,
		"WRAPPER_LOG="+fx.wrapLog,
		"WRAPPER_EXIT=0",
	)
	cmd.Env = append(cmd.Env, callerEnv(ownerRoot, h.root)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	requireRefused(t, code, stderr.String(), "[ECI_SESSION_IDENTITY_MISMATCH]", "ECI_THREAD_ID="+staleRoot, ownerRoot)
	requireAbsent(t, ctxDir(fx, staleRoot))
	fx.assertWrapperCalls(t)
}

// Transcripts outside the default sessions tree. A named profile's sessions tree belongs to the
// home that holds the profile: same-session and child-to-root use work and a stale session is
// refused. A --session-dir transcript belongs to the account home, so a private-HOME fixture is
// not bound and same-session use works.
func TestCallerSessionProfileAndSessionDirLayouts(t *testing.T) {
	fx := newWrapperFixture(t)
	newHarness(t, fx.home)
	profile := filepath.Join(fx.home, ".omp", "profiles", "work", "agent", "sessions", "-tmp", "2026-11-08T00-00-00-000Z_"+ownerRoot)
	pRoot := writeTranscript(t, profile+".jsonl", ownerRoot, "")
	pChild := writeTranscript(t, filepath.Join(profile, "Orch.jsonl"), ownerChild, pRoot)
	sessionDir := filepath.Join(fx.home, "custom-session-dir", "2026-11-08T00-00-00-000Z_"+ownerRoot)
	dRoot := writeTranscript(t, sessionDir+".jsonl", ownerRoot, "")
	dChild := writeTranscript(t, filepath.Join(sessionDir, "Orch.jsonl"), ownerChild, dRoot)
	for _, c := range []struct {
		name, sid string
		env       []string
	}{
		{"profile same session", ownerRoot, callerEnv(ownerRoot, pRoot)},
		{"profile child to root", ownerRoot, callerEnv(ownerChild, pChild)},
		{"--session-dir same session", ownerRoot, callerEnv(ownerRoot, dRoot)},
		{"--session-dir child to root", ownerRoot, callerEnv(ownerChild, dChild)},
	} {
		_, stderr, code := fx.runAs(t, c.sid, cwdNow(t), c.env, "ledger-append", "--json", appendEvent)
		if code != 0 {
			t.Errorf("%s: exit %d stderr %q; want success", c.name, code, stderr)
		}
	}
	_, stderr, code := fx.runAs(t, staleRoot, cwdNow(t), callerEnv(ownerChild, pChild), "ledger-append", "--json", appendEvent)
	requireRefused(t, code, stderr, "[ECI_SESSION_IDENTITY_MISMATCH]", ownerChild)
	requireAbsent(t, ctxDir(fx, staleRoot))
}
