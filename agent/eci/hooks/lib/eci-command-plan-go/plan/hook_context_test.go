package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// contextFixture isolates proof storage and command working directories.
//
// Example: marker discovery reads a temporary proof root without real session state.
func contextFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("ECI_PROOF_ROOT", root)
	t.Setenv("ECI_HOOK_IS_SUBAGENT", "")
	return root, cwd
}

// contextMarker writes a valid session marker in isolated proof storage.
//
// Example: a parent marker binds its session identifier to the fixture directory.
func contextMarker(
	t *testing.T,
	root string,
	sid string,
	cwd string,
) {
	t.Helper()
	dir := filepath.Join(root, sid)
	require.NoError(t, os.MkdirAll(dir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "eci_active"), []byte("scope: test\ncwd: "+cwd+"\nsession_id: "+sid+"\n"), 0600))
}

// sessionsFixture points HOME at a temporary omp home and returns a project directory under its sessions root.
//
// Example: transcripts in the returned directory sit two levels below the sessions root, like an omp main transcript.
func sessionsFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := filepath.Join(home, ".omp", "agent", "sessions", "project")
	require.NoError(t, os.MkdirAll(project, 0700))
	return project
}

// writeSubagentTranscript writes a title record, a line-two session header that declares parentSession, and tail records.
//
// Example: {"type":"session","parentSession":...} on line two marks a subagent transcript.
func writeSubagentTranscript(t *testing.T, path, id, parentSession, tail string) {
	t.Helper()
	header := `{"type":"session","version":3,"id":"` + id + `","parentSession":"` + parentSession + `"}`
	require.NoError(t, os.WriteFile(path, []byte(`{"type":"title","v":1,"title":""}`+"\n"+header+"\n"+tail), 0600))
}

// subagentDirectory returns a directory three levels below the sessions root, where a transcript with a parentSession header is a subagent.
//
// Example: project/children/child.jsonl sits at depth three.
func subagentDirectory(t *testing.T, project string) string {
	t.Helper()
	dir := filepath.Join(project, "children")
	require.NoError(t, os.MkdirAll(dir, 0700))
	return dir
}

// TestHookContextParentPrefix inherits a declared parent marker before a large history tail.
//
// Example: a child whose line-two header names parentSession inherits the parent's active marker.
func TestHookContextParentPrefix(t *testing.T) {
	root, cwd := contextFixture(t)
	sessions := sessionsFixture(t)
	contextMarker(t, root, "parent", cwd)
	parent := filepath.Join(sessions, "parent.jsonl")
	require.NoError(t, os.WriteFile(parent, []byte(`{"type":"session","version":3,"id":"parent"}`+"\n"), 0600))
	transcript := filepath.Join(subagentDirectory(t, sessions), "child.jsonl")
	writeSubagentTranscript(t, transcript, "child", parent, `{"type":"message","pad":"`+strings.Repeat("x", 2<<20)+`"}`)
	got, err := hookRequest(HookInput{SessionID: "child", CWD: cwd, TranscriptPath: transcript})
	require.NoError(t, err)
	require.Falsef(t, got.Role != RoleWorker || got.ActiveSession != "parent" || got.Marker != MarkerActive || len(got.ActiveMarkers) != 1, "context=%+v", got)
}

// TestHookContextOversizedFirstRecordTail fails closed when the session header exceeds the bounded prefix before parentSession.
//
// Example: an oversized header is unknown, so it keeps the worker role and inherits no marker.
func TestHookContextOversizedFirstRecordTail(t *testing.T) {
	root, cwd := contextFixture(t)
	sessions := sessionsFixture(t)
	contextMarker(t, root, "parent", cwd)
	parent := filepath.Join(sessions, "parent.jsonl")
	require.NoError(t, os.WriteFile(parent, []byte(`{"type":"session","version":3,"id":"parent"}`+"\n"), 0600))
	transcript := filepath.Join(subagentDirectory(t, sessions), "child.jsonl")
	header := `{"type":"session","version":3,"id":"child","tail":"` + strings.Repeat("x", 2<<20) + `","parentSession":"` + parent + `"}`
	require.NoError(t, os.WriteFile(transcript, []byte(header+"\n"), 0600))
	status, err := hookTranscriptStatus(transcript)
	require.ErrorIs(t, err, errHookTranscriptUnknown)
	require.ErrorContains(t, err, "bounded prefix")
	require.Empty(t, status)
	got, err := hookRequest(HookInput{SessionID: "child", CWD: cwd, TranscriptPath: transcript})
	require.NoError(t, err)
	require.Falsef(t, got.Role != RoleWorker || got.ActiveSession == "parent" || got.Marker == MarkerActive, "context=%+v", got)
}

// TestHookContextUnresolvableParentIsWorker grants no inherited marker when the declared parent is not a regular sessions-root transcript.
//
// Example: a parentSession that is missing, outside the root, or a symlink fails closed to worker.
func TestHookContextUnresolvableParentIsWorker(t *testing.T) {
	root, cwd := contextFixture(t)
	sessions := sessionsFixture(t)
	contextMarker(t, root, "parent", cwd)
	outside := filepath.Join(t.TempDir(), "parent.jsonl")
	require.NoError(t, os.WriteFile(outside, []byte(`{"type":"session","version":3,"id":"parent"}`+"\n"), 0600))
	linked := filepath.Join(sessions, "linked.jsonl")
	require.NoError(t, os.Symlink(outside, linked))
	for name, declared := range map[string]string{"missing": filepath.Join(sessions, "missing.jsonl"), "outside": outside, "symlink": linked} {
		transcript := filepath.Join(subagentDirectory(t, sessions), "child-"+name+".jsonl")
		writeSubagentTranscript(t, transcript, "child", declared, "")
		got, err := hookRequest(HookInput{SessionID: "child", CWD: cwd, TranscriptPath: transcript})
		require.Error(t, err, name)
		require.Equal(t, RoleWorker, got.Role, name)
		require.Falsef(t, got.Marker == MarkerActive || got.ActiveSession == "parent", "%s: context=%+v", name, got)
	}
}

// TestHookContextUnknownTranscriptIsWorker runs unverifiable callbacks as restricted workers.
//
// Example: an empty or missing transcript path is unknown and therefore a worker.
func TestHookContextUnknownTranscriptIsWorker(t *testing.T) {
	_, cwd := contextFixture(t)
	sessionsFixture(t)
	for name, path := range map[string]string{"empty": "", "missing": filepath.Join(t.TempDir(), "missing.jsonl")} {
		got, err := hookRequest(HookInput{SessionID: "owner", CWD: cwd, TranscriptPath: path})
		require.NoError(t, err, name)
		require.Equal(t, RoleWorker, got.Role, name)
	}
}

// TestHookContextMainTranscriptIsCoordinator keeps a verified main transcript on the coordinator role.
//
// Example: a depth-two transcript without parentSession is main.
func TestHookContextMainTranscriptIsCoordinator(t *testing.T) {
	_, cwd := contextFixture(t)
	sessions := sessionsFixture(t)
	mainTranscript := filepath.Join(sessions, "main.jsonl")
	require.NoError(t, os.WriteFile(mainTranscript, []byte(`{"type":"session","version":3,"id":"owner"}`+"\n"), 0600))
	got, err := hookRequest(HookInput{SessionID: "owner", CWD: cwd, TranscriptPath: mainTranscript})
	require.NoError(t, err)
	require.Equal(t, RoleCoordinator, got.Role)
}

// TestHookContextOversizedMainIsWorker keeps a depth-two transcript whose header exceeds the bounded prefix on the worker role; a long record after the header keeps main.
//
// Example: a first record longer than 64 KiB with no visible signal is unknown, not main.
func TestHookContextOversizedMainIsWorker(t *testing.T) {
	_, cwd := contextFixture(t)
	sessions := sessionsFixture(t)
	pad := strings.Repeat("x", maxHookTranscriptPrefixBytes)
	oversize := filepath.Join(sessions, "oversize.jsonl")
	require.NoError(t, os.WriteFile(oversize, []byte(`{"type":"session","version":3,"id":"owner","pad":"`+pad+`"}`+"\n"), 0600))
	status, err := hookTranscriptStatus(oversize)
	require.ErrorIs(t, err, errHookTranscriptUnknown)
	require.Empty(t, status)
	got, err := hookRequest(HookInput{SessionID: "owner", CWD: cwd, TranscriptPath: oversize})
	require.NoError(t, err)
	require.Equal(t, RoleWorker, got.Role)
	long := filepath.Join(sessions, "long-message.jsonl")
	require.NoError(t, os.WriteFile(long, []byte(`{"type":"session","version":3,"id":"owner"}`+"\n"+`{"type":"message","pad":"`+pad+`"}`+"\n"), 0600))
	got, err = hookRequest(HookInput{SessionID: "owner", CWD: cwd, TranscriptPath: long})
	require.NoError(t, err)
	require.Equal(t, RoleCoordinator, got.Role)
}

// TestHookContextDuplicateSignalKeyIsWorker keeps a repeated signal key from hiding a parentSession or session_init marker.
//
// Example: a second type key on a depth-two header makes the transcript unknown, not main.
func TestHookContextDuplicateSignalKeyIsWorker(t *testing.T) {
	_, cwd := contextFixture(t)
	sessions := sessionsFixture(t)
	cases := map[string]string{
		"duplicate-type":          `{"type":"session","version":3,"id":"owner","parentSession":"/p.jsonl","type":"message"}` + "\n",
		"duplicate-parentSession": `{"type":"session","version":3,"id":"owner","parentSession":"/p.jsonl","parentSession":5}` + "\n",
		"duplicate-customType":    `{"type":"session","version":3,"id":"owner"}` + "\n" + `{"type":"custom","customType":"session_init","customType":"other"}` + "\n",
	}
	for name, content := range cases {
		transcript := filepath.Join(sessions, name+".jsonl")
		require.NoError(t, os.WriteFile(transcript, []byte(content), 0600))
		_, err := hookTranscriptStatus(transcript)
		require.ErrorIs(t, err, errHookTranscriptUnknown, name)
		got, err := hookRequest(HookInput{SessionID: "owner", CWD: cwd, TranscriptPath: transcript})
		require.NoError(t, err, name)
		require.Equal(t, RoleWorker, got.Role, name)
	}
}

// TestHookContextDepthThreeWithoutParentIsWorker grants no inherited marker to a depth-three transcript that names no parent.
//
// Example: a deep transcript without parentSession is unknown, so it runs as a worker without the parent marker.
func TestHookContextDepthThreeWithoutParentIsWorker(t *testing.T) {
	root, cwd := contextFixture(t)
	sessions := sessionsFixture(t)
	contextMarker(t, root, "parent", cwd)
	transcript := filepath.Join(subagentDirectory(t, sessions), "child.jsonl")
	require.NoError(t, os.WriteFile(transcript, []byte(`{"type":"session","version":3,"id":"child"}`+"\n"), 0600))
	got, err := hookRequest(HookInput{SessionID: "child", CWD: cwd, TranscriptPath: transcript})
	require.NoError(t, err)
	require.Equal(t, RoleWorker, got.Role)
	require.NotEqual(t, "parent", got.ActiveSession)
}

// TestHookContextDepthTwoParentSessionIsWorker grants no inherited marker when a depth-two header names a parent.
//
// Example: a parentSession at depth two is ambiguous, so the callback runs as a worker without the parent marker.
func TestHookContextDepthTwoParentSessionIsWorker(t *testing.T) {
	root, cwd := contextFixture(t)
	sessions := sessionsFixture(t)
	contextMarker(t, root, "parent", cwd)
	parent := filepath.Join(sessions, "parent.jsonl")
	require.NoError(t, os.WriteFile(parent, []byte(`{"type":"session","version":3,"id":"parent"}`+"\n"), 0600))
	transcript := filepath.Join(sessions, "child.jsonl")
	writeSubagentTranscript(t, transcript, "child", parent, "")
	got, err := hookRequest(HookInput{SessionID: "child", CWD: cwd, TranscriptPath: transcript})
	require.NoError(t, err)
	require.Equal(t, RoleWorker, got.Role)
	require.NotEqual(t, "parent", got.ActiveSession)
}

// TestHookContextDepthTwoSignalsAreUnknown keeps depth-two transcripts without a session header, with any parentSession key, or with a session_init marker out of the main role.
//
// Example: a depth-two header with parentSession null is unknown, not main.
func TestHookContextDepthTwoSignalsAreUnknown(t *testing.T) {
	_, cwd := contextFixture(t)
	sessions := sessionsFixture(t)
	cases := map[string]string{
		"parent-number": `{"type":"session","version":3,"id":"owner","parentSession":5}` + "\n",
		"parent-null":   `{"type":"session","version":3,"id":"owner","parentSession":null}` + "\n",
		"no-header":     `{"type":"message","id":"owner"}` + "\n",
		"session-init":  `{"type":"session","version":3,"id":"owner"}` + "\n" + `{"type":"session_init"}` + "\n",
	}
	for name, content := range cases {
		transcript := filepath.Join(sessions, name+".jsonl")
		require.NoError(t, os.WriteFile(transcript, []byte(content), 0600))
		status, err := hookTranscriptStatus(transcript)
		require.ErrorIs(t, err, errHookTranscriptUnknown, name)
		require.Empty(t, status, name)
		got, err := hookRequest(HookInput{SessionID: "owner", CWD: cwd, TranscriptPath: transcript})
		require.NoError(t, err, name)
		require.Equal(t, RoleWorker, got.Role, name)
	}
}

// TestHookContextSymlinkedAncestorIsWorker keeps a transcript reached through a symlinked intermediate directory out of the main role.
//
// Example: project/P/C.jsonl with P linked to project is unknown, although the physical file sits at depth two.
func TestHookContextSymlinkedAncestorIsWorker(t *testing.T) {
	_, cwd := contextFixture(t)
	project := sessionsFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(project, "C.jsonl"), []byte(`{"type":"session","version":3,"id":"owner"}`+"\n"), 0600))
	link := filepath.Join(project, "P")
	require.NoError(t, os.Symlink(project, link))
	transcript := filepath.Join(link, "C.jsonl")
	status, err := hookTranscriptStatus(transcript)
	require.ErrorIs(t, err, errHookTranscriptUnknown)
	require.ErrorContains(t, err, "crosses a symlink")
	require.Empty(t, status)
	got, err := hookRequest(HookInput{SessionID: "owner", CWD: cwd, TranscriptPath: transcript})
	require.NoError(t, err)
	require.Equal(t, RoleWorker, got.Role)
}

// TestHookContextHomeAliasAboveRootIsCoordinator keeps a main transcript reached through a HOME alias on the coordinator role and a symlink below the root unknown.
//
// Example: ALIAS/.omp/agent/sessions/project/C.jsonl with ALIAS linked to HOME is main, while project/P/C.jsonl with P linked to project is unknown.
func TestHookContextHomeAliasAboveRootIsCoordinator(t *testing.T) {
	_, cwd := contextFixture(t)
	project := sessionsFixture(t)
	home := os.Getenv("HOME")
	require.NoError(t, os.WriteFile(filepath.Join(project, "C.jsonl"), []byte(`{"type":"session","version":3,"id":"owner"}`+"\n"), 0600))
	alias := filepath.Join(t.TempDir(), "alias")
	require.NoError(t, os.Symlink(home, alias))
	transcript := filepath.Join(alias, ".omp", "agent", "sessions", "project", "C.jsonl")
	status, err := hookTranscriptStatus(transcript)
	require.NoError(t, err)
	require.Equal(t, "main", status)
	got, err := hookRequest(HookInput{SessionID: "owner", CWD: cwd, TranscriptPath: transcript})
	require.NoError(t, err)
	require.Equal(t, RoleCoordinator, got.Role)
	require.NoError(t, os.Symlink(project, filepath.Join(project, "P")))
	linked := filepath.Join(alias, ".omp", "agent", "sessions", "project", "P", "C.jsonl")
	status, err = hookTranscriptStatus(linked)
	require.ErrorIs(t, err, errHookTranscriptUnknown)
	require.ErrorContains(t, err, "crosses a symlink")
	require.Empty(t, status)
	got, err = hookRequest(HookInput{SessionID: "owner", CWD: cwd, TranscriptPath: linked})
	require.NoError(t, err)
	require.Equal(t, RoleWorker, got.Role)
}

// sessionsLinkFixture points HOME at a temporary omp home whose sessions link leads to a store outside it, and returns the store's project directory and a HOME alias.
//
// Example: HOME/.omp/agent/sessions -> store/sessions, so ALIAS/.omp/agent/sessions/project is store/sessions/project.
func sessionsLinkFixture(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := filepath.Join(t.TempDir(), "sessions", "project")
	require.NoError(t, os.MkdirAll(project, 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".omp", "agent"), 0700))
	require.NoError(t, os.Symlink(filepath.Dir(project), filepath.Join(home, ".omp", "agent", "sessions")))
	alias := filepath.Join(t.TempDir(), "alias")
	require.NoError(t, os.Symlink(home, alias))
	return project, alias
}

// TestHookContextSessionsLinkIsExemptFromIntoRootRule accepts a main transcript whose configured sessions link is itself a symlink into the store, both through a HOME alias and through the real HOME.
//
// Example: ALIAS/.omp/agent/sessions/project/C.jsonl with HOME/.omp/agent/sessions -> store/sessions is main, and so is the same file spelled via HOME.
func TestHookContextSessionsLinkIsExemptFromIntoRootRule(t *testing.T) {
	_, cwd := contextFixture(t)
	project, alias := sessionsLinkFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(project, "C.jsonl"), []byte(`{"type":"session","version":3,"id":"owner"}`+"\n"), 0600))
	transcript := filepath.Join(alias, ".omp", "agent", "sessions", "project", "C.jsonl")
	status, err := hookTranscriptStatus(transcript)
	require.NoError(t, err)
	require.Equal(t, "main", status)
	got, err := hookRequest(HookInput{SessionID: "owner", CWD: cwd, TranscriptPath: transcript})
	require.NoError(t, err)
	require.Equal(t, RoleCoordinator, got.Role)
	direct := filepath.Join(os.Getenv("HOME"), ".omp", "agent", "sessions", "project", "C.jsonl")
	status, err = hookTranscriptStatus(direct)
	require.NoError(t, err)
	require.Equal(t, "main", status)
	got, err = hookRequest(HookInput{SessionID: "owner", CWD: cwd, TranscriptPath: direct})
	require.NoError(t, err)
	require.Equal(t, RoleCoordinator, got.Role)
}

// TestHookContextSessionsLinkDoesNotExemptReentryBelowIt keeps a symlink below the exempt sessions link that re-enters the root unknown.
//
// Example: ALIAS/.omp/agent/sessions/project/up -> root makes project/up/project/C.jsonl unknown.
func TestHookContextSessionsLinkDoesNotExemptReentryBelowIt(t *testing.T) {
	_, cwd := contextFixture(t)
	project, alias := sessionsLinkFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(project, "C.jsonl"), []byte(`{"type":"session","version":3,"id":"owner"}`+"\n"), 0600))
	require.NoError(t, os.Symlink(filepath.Dir(project), filepath.Join(project, "up")))
	transcript := filepath.Join(alias, ".omp", "agent", "sessions", "project", "up", "project", "C.jsonl")
	status, err := hookTranscriptStatus(transcript)
	require.ErrorIs(t, err, errHookTranscriptUnknown)
	require.ErrorContains(t, err, "crosses a symlink")
	require.Empty(t, status)
	got, err := hookRequest(HookInput{SessionID: "owner", CWD: cwd, TranscriptPath: transcript})
	require.NoError(t, err)
	require.Equal(t, RoleWorker, got.Role)
}

// TestHookContextSessionsLinkRequiresHomeAlias keeps a sessions-root link unknown unless its prefix physically resolves to HOME, even when its path ends in .omp/agent/sessions.
//
// Example: ALIAS/evil/.omp/agent/sessions with HOME/evil/.omp/agent/sessions -> root is unknown, because ALIAS/evil does not resolve to HOME.
func TestHookContextSessionsLinkRequiresHomeAlias(t *testing.T) {
	_, cwd := contextFixture(t)
	project, alias := sessionsLinkFixture(t)
	root := filepath.Dir(project)
	require.NoError(t, os.WriteFile(filepath.Join(project, "C.jsonl"), []byte(`{"type":"session","version":3,"id":"owner"}`+"\n"), 0600))
	home := os.Getenv("HOME")
	other := t.TempDir()
	cases := []struct {
		name       string
		parent     string
		transcript string
	}{
		{"evil", filepath.Join(home, "evil"), filepath.Join(alias, "evil", ".omp", "agent", "sessions", "project", "C.jsonl")},
		{"deep", filepath.Join(home, "a", "b", "c"), filepath.Join(alias, "a", "b", "c", ".omp", "agent", "sessions", "project", "C.jsonl")},
		{"outside", other, filepath.Join(other, ".omp", "agent", "sessions", "project", "C.jsonl")},
	}
	for _, tc := range cases {
		require.NoError(t, os.MkdirAll(filepath.Join(tc.parent, ".omp", "agent"), 0700))
		require.NoError(t, os.Symlink(root, filepath.Join(tc.parent, ".omp", "agent", "sessions")))
		status, err := hookTranscriptStatus(tc.transcript)
		require.ErrorIs(t, err, errHookTranscriptUnknown, tc.name)
		require.ErrorContains(t, err, "crosses a symlink", tc.name)
		require.Empty(t, status, tc.name)
		got, err := hookRequest(HookInput{SessionID: "owner", CWD: cwd, TranscriptPath: tc.transcript})
		require.NoError(t, err, tc.name)
		require.Equal(t, RoleWorker, got.Role, tc.name)
	}
}

// TestHookContextSymlinkIntoRootAboveMatchIsWorker rejects a HOME alias whose path re-enters the sessions root through a symlink inside it.
//
// Example: HOME/s -> root/project and root/project/up -> root make HOME/s/up/project/C.jsonl unknown.
func TestHookContextSymlinkIntoRootAboveMatchIsWorker(t *testing.T) {
	_, cwd := contextFixture(t)
	project := sessionsFixture(t)
	home := os.Getenv("HOME")
	root := filepath.Dir(project)
	require.NoError(t, os.WriteFile(filepath.Join(project, "C.jsonl"), []byte(`{"type":"session","version":3,"id":"owner"}`+"\n"), 0600))
	require.NoError(t, os.Symlink(root, filepath.Join(project, "up")))
	require.NoError(t, os.Symlink(project, filepath.Join(home, "s")))
	transcript := filepath.Join(home, "s", "up", "project", "C.jsonl")
	status, err := hookTranscriptStatus(transcript)
	require.ErrorIs(t, err, errHookTranscriptUnknown)
	require.ErrorContains(t, err, "crosses a symlink")
	require.Empty(t, status)
	got, err := hookRequest(HookInput{SessionID: "owner", CWD: cwd, TranscriptPath: transcript})
	require.NoError(t, err)
	require.Equal(t, RoleWorker, got.Role)
}

// TestHookContextSymlinkBaseIsWorker rejects a HOME alias that is itself a symlink to the sessions root.
//
// Example: HOME/b -> root makes HOME/b/project/C.jsonl unknown, although it sits at depth two.
func TestHookContextSymlinkBaseIsWorker(t *testing.T) {
	_, cwd := contextFixture(t)
	project := sessionsFixture(t)
	home := os.Getenv("HOME")
	root := filepath.Dir(project)
	require.NoError(t, os.WriteFile(filepath.Join(project, "C.jsonl"), []byte(`{"type":"session","version":3,"id":"owner"}`+"\n"), 0600))
	require.NoError(t, os.Symlink(root, filepath.Join(home, "b")))
	transcript := filepath.Join(home, "b", "project", "C.jsonl")
	status, err := hookTranscriptStatus(transcript)
	require.ErrorIs(t, err, errHookTranscriptUnknown)
	require.ErrorContains(t, err, "crosses a symlink")
	require.Empty(t, status)
	got, err := hookRequest(HookInput{SessionID: "owner", CWD: cwd, TranscriptPath: transcript})
	require.NoError(t, err)
	require.Equal(t, RoleWorker, got.Role)
}

// TestHookContextRelativeTranscriptIsWorker treats a relative transcript path as unknown, even when it names a real main transcript.
//
// Example: a path relative to the working directory is unknown, so the callback runs as a worker.
func TestHookContextRelativeTranscriptIsWorker(t *testing.T) {
	_, cwd := contextFixture(t)
	project := sessionsFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(project, "C.jsonl"), []byte(`{"type":"session","version":3,"id":"owner"}`+"\n"), 0600))
	wd, err := os.Getwd()
	require.NoError(t, err)
	relative, err := filepath.Rel(wd, filepath.Join(project, "C.jsonl"))
	require.NoError(t, err)
	require.False(t, filepath.IsAbs(relative))
	status, err := hookTranscriptStatus(relative)
	require.ErrorIs(t, err, errHookTranscriptUnknown)
	require.ErrorContains(t, err, "not absolute")
	require.Empty(t, status)
	got, err := hookRequest(HookInput{SessionID: "owner", CWD: cwd, TranscriptPath: relative})
	require.NoError(t, err)
	require.Equal(t, RoleWorker, got.Role)
}

// TestHookContextMarkerAliasAndInvalid checks supported aliases and invalid records.
//
// Example: owner resolves session_owner, while a broken marker grants no authority.
func TestHookContextMarkerAliasAndInvalid(t *testing.T) {
	root, cwd := contextFixture(t)
	contextMarker(t, root, "session_owner", cwd)
	got, err := hookRequest(HookInput{SessionID: "owner", CWD: cwd})
	require.NoError(t, err)
	require.Falsef(t, got.ActiveSession != "session_owner" || got.Marker != MarkerActive, "context=%+v", got)
	require.NoError(t, os.WriteFile(filepath.Join(root, "session_owner", "eci_active"), []byte("broken\n"), 0600))
	got, err = hookRequest(HookInput{SessionID: "owner", CWD: cwd})
	require.NoError(t, err)
	require.Falsef(t, got.Marker == MarkerActive || len(got.ActiveMarkers) != 0, "invalid marker accepted: %+v", got)
}

// TestHookContextExplicitWorkerAndActivity verifies role and mutation bookkeeping.
//
// Example: ordinary reads create no activity, while coordinator writes record it.
func TestHookContextExplicitWorkerAndActivity(t *testing.T) {
	root, cwd := contextFixture(t)
	t.Setenv("ECI_HOOK_IS_SUBAGENT", "1")
	input := HookInput{SessionID: "child", CWD: cwd}
	input.ToolInput.Command = "cat ordinary"
	request, err := hookRequest(input)
	require.NoError(t, err)
	require.Falsef(t, request.Role != RoleWorker || request.Command != "cat ordinary", "request=%+v", request)
	readonly := Result{ShellAnalysis: &ShellAnalysis{Commands: []ShellCommandRecord{{Argv: []string{"cat", "ordinary"}}}}}
	require.NoError(t, recordHookActivity(request, readonly))
	{
		_, err := os.Stat(filepath.Join(root, "activity"))
		require.Falsef(t, !os.IsNotExist(err), "worker read created activity: %v", err)
	}
	{
		_, err := os.Stat(filepath.Join(root, "touched-repos"))
		require.Falsef(t, !os.IsNotExist(err), "read-only command touched repositories: %v", err)
	}
	request.Role = RoleCoordinator
	require.NoError(t, recordHookActivity(request, readonly))
	{
		_, err := os.Stat(filepath.Join(root, "activity"))
		require.Falsef(t, !os.IsNotExist(err), "coordinator read created activity: %v", err)
	}
	request.Command = "printf data > output"
	require.NoError(t, recordHookActivity(request, Result{ShellAnalysis: &ShellAnalysis{OutputWrites: true}}))
	data, err := os.ReadFile(filepath.Join(root, "activity", "sessions", "child", "shell"))
	require.NoError(t, err)
	require.Falsef(t, !strings.Contains(string(data), "kind: shell\n"), "activity=%q", data)
}

// TestHookContextDependencyAndRootAlias resolves canonical owner and dependency roots.
//
// Example: a proof-root symlink preserves a declared dependency allowance.
func TestHookContextDependencyAndRootAlias(t *testing.T) {
	root, cwd := contextFixture(t)
	contextMarker(t, root, "owner", cwd)
	alias := filepath.Join(t.TempDir(), "proof")
	require.NoError(t, os.Symlink(root, alias))
	t.Setenv("ECI_PROOF_ROOT", alias)
	dependency := t.TempDir()
	data := "schema: eci-additional-repository/v1\nsession_id: owner\ncwd: " + cwd + "\nrepository: " + dependency + "\nreason: fix dependency\nstate: active\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "owner", "eci-additional-repository"), []byte(data), 0600))
	got, err := hookRequest(HookInput{SessionID: "owner", CWD: dependency})
	require.NoError(t, err)
	canonicalDependency, err := hookCanonicalPath(dependency)
	require.NoError(t, err)
	canonicalRoot, err := hookCanonicalPath(root)
	require.NoError(t, err)
	require.Falsef(t, len(got.ApprovedRoots) != 2 || got.ApprovedRoots[1] != canonicalDependency || got.ActiveMarkers[0] != filepath.Join(canonicalRoot, "owner", "eci_active"), "context=%+v", got)
}

// TestHookContextTouchedBaseline checks consumer-compatible child-session records.
//
// Example: worker writes record initial HEAD and status without coordinator activity.
func TestHookContextTouchedBaseline(t *testing.T) {
	root, cwd := contextFixture(t)
	{
		output, err := exec.Command("git", "-C", cwd, "init").CombinedOutput()
		require.NoErrorf(t, err, "fixture: %v: %s", err, output)
	}
	request := Request{Role: RoleWorker, ActiveSession: "owner", HookSessionID: "child", CWD: cwd, Command: "printf data > output"}
	result := Result{ShellAnalysis: &ShellAnalysis{OutputWrites: true}}
	require.NoError(t, recordHookActivity(request, result))
	entries, err := os.ReadDir(filepath.Join(root, "touched-repos", "sessions", "child"))
	require.NoError(t, err)
	require.Falsef(t, len(entries) != 1, "records=%d", len(entries))
	data, err := os.ReadFile(filepath.Join(root, "touched-repos", "sessions", "child", entries[0].Name()))
	require.NoError(t, err)
	require.Falsef(t, !strings.Contains(string(data), "status_sha: ") || !strings.Contains(string(data), "head: \n") || !strings.Contains(string(data), "repo_wide: true\n"), "baseline=%q", data)
	{
		_, err := os.Stat(filepath.Join(root, "touched-repos", "sessions", "owner"))
		require.Falsef(t, !os.IsNotExist(err), "parent state created: %v", err)
	}
	{
		_, err := os.Stat(filepath.Join(root, "activity"))
		require.Falsef(t, !os.IsNotExist(err), "worker mutation created activity: %v", err)
	}
}

// TestHookContextDirectOwnerBeyondPeerCap prioritizes direct authority over peers.
//
// Example: a sorted-late current marker stays active after more than 64 peers.
func TestHookContextDirectOwnerBeyondPeerCap(t *testing.T) {
	root, cwd := contextFixture(t)
	for index := 0; index < 65; index++ {
		contextMarker(t, root, fmt.Sprintf("aaa-%02d", index), cwd)
	}
	contextMarker(t, root, "zzz-owner", cwd)
	request, err := hookRequest(HookInput{SessionID: "zzz-owner", CWD: cwd})
	require.NoError(t, err)
	require.Falsef(t, request.Marker != MarkerActive || request.ActiveSession != "zzz-owner", "direct owner hidden: %+v", request)
	require.False(t, !hookReadonlyCommand(ShellCommandRecord{Argv: []string{"git", "-C", cwd, "diff"}}) || hookReadonlyCommand(ShellCommandRecord{Argv: []string{"git", "add", "diff"}}), "git arguments confused with command verb")
}

// TestHookContextTypedDependencyTouched records the typed command's selected repository.
//
// Example: a worker stage targets its dependency while inspect creates no baseline.
func TestHookContextTypedDependencyTouched(t *testing.T) {
	root, cwd := contextFixture(t)
	dependency := t.TempDir()
	{
		output, err := exec.Command("git", "-C", dependency, "init").CombinedOutput()
		require.NoErrorf(t, err, "fixture: %v: %s", err, output)
	}
	request := Request{Role: RoleWorker, ActiveSession: "parent", HookSessionID: "child", CWD: cwd}
	analysis := &ShellAnalysis{Commands: []ShellCommandRecord{{Argv: []string{"eci-worker-git", "--repo", dependency, "inspect"}}}}
	require.NoError(t, recordHookActivity(request, Result{ShellAnalysis: analysis}))
	{
		_, err := os.Stat(filepath.Join(root, "touched-repos"))
		require.Falsef(t, !os.IsNotExist(err), "inspect touched dependency: %v", err)
	}
	analysis.Commands[0].Argv[3] = "stage"
	require.NoError(t, recordHookActivity(request, Result{ShellAnalysis: analysis}))
	entries, err := os.ReadDir(filepath.Join(root, "touched-repos", "sessions", "child"))
	require.NoError(t, err)
	require.Falsef(t, len(entries) != 1, "touched=%d", len(entries))
	data, err := os.ReadFile(filepath.Join(root, "touched-repos", "sessions", "child", entries[0].Name()))
	require.NoError(t, err)
	canonical, err := hookCanonicalPath(dependency)
	require.NoError(t, err)
	require.Falsef(t, !strings.Contains(string(data), "repo: "+canonical+"\n"), "wrong repository: %q", data)
}

// TestHookContextAssignmentReadonly preserves child effects after environment assignments.
//
// Example: KEY=value cat stays readonly, while KEY=value git add stays mutable.
func TestHookContextAssignmentReadonly(t *testing.T) {
	require.False(t, !hookReadonlyCommand(ShellCommandRecord{Argv: []string{"KEY=value", "cat", "ordinary"}}), "assignment-prefixed read marked mutable")
	require.False(t, hookReadonlyCommand(ShellCommandRecord{Argv: []string{"KEY=value", "git", "add", "ordinary"}}), "assignment-prefixed mutation marked readonly")
}
