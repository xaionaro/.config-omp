package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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

// TestHookContextChildIgnoresParentMarker classifies a child despite a large history tail.
//
// Example: a child header selects Worker without inheriting its parent's marker.
func TestHookContextChildIgnoresParentMarker(t *testing.T) {
	root, cwd := contextFixture(t)
	sessions := sessionsFixture(t)
	contextMarker(t, root, "parent", cwd)
	parent := filepath.Join(sessions, "parent.jsonl")
	require.NoError(t, os.WriteFile(parent, []byte(`{"type":"session","version":3,"id":"parent"}`+"\n"), 0600))
	transcript := filepath.Join(subagentDirectory(t, sessions), "child.jsonl")
	writeSubagentTranscript(t, transcript, "child", parent, `{"type":"message","pad":"`+strings.Repeat("x", 2<<20)+`"}`)
	got, err := hookRequest(HookInput{SessionID: "child", CWD: cwd, TranscriptPath: transcript})
	require.NoError(t, err)
	require.Equal(t, RoleWorker, got.Role)
	require.Equal(t, MarkerInactive, got.Marker)
	require.Empty(t, got.ActiveMarkers)
	require.Empty(t, got.ApprovedRoots)
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

// TestHookContextChildNeedsNoParentLookup needs no parent transcript lookup.
//
// Example: a child remains a Worker when its declared parent is absent or external.
func TestHookContextChildNeedsNoParentLookup(t *testing.T) {
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
		require.NoError(t, err, name)
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

// TestHookContextPendingIdentity limits the lazy-transcript exception to verified main callbacks.
//
// Example: a missing depth-two file with a real parent admits main, but never a child.
func TestHookContextPendingIdentity(t *testing.T) {
	_, cwd := contextFixture(t)
	project := sessionsFixture(t)
	root := filepath.Dir(project)
	require.NoError(t, os.MkdirAll(filepath.Join(project, "children"), 0700))
	require.NoError(t, os.Symlink(project, filepath.Join(root, "linked")))
	require.NoError(t, os.Symlink(filepath.Join(project, "absent"), filepath.Join(project, "dangling.jsonl")))
	require.NoError(t, os.WriteFile(filepath.Join(root, "file-parent"), []byte("file"), 0600))
	for _, testCase := range []struct {
		name           string
		path           string
		kind           any
		workerOverride string
		want           Role
	}{
		{name: "pending main", path: filepath.Join(project, "pending.jsonl"), kind: "main", want: RoleCoordinator},
		{name: "pending child identity", path: filepath.Join(project, "pending.jsonl"), kind: "sub", want: RoleWorker},
		{name: "old adapter", path: filepath.Join(project, "pending.jsonl"), want: RoleWorker},
		{name: "invalid identity", path: filepath.Join(project, "pending.jsonl"), kind: "Main", want: RoleWorker},
		{name: "override one", path: filepath.Join(project, "pending.jsonl"), kind: "main", workerOverride: "1", want: RoleWorker},
		{name: "override true", path: filepath.Join(project, "pending.jsonl"), kind: "main", workerOverride: "true", want: RoleWorker},
		{name: "no session", kind: "main", want: RoleCoordinator},
		{name: "depth one", path: filepath.Join(root, "pending.jsonl"), kind: "main", want: RoleWorker},
		{name: "depth three", path: filepath.Join(project, "children", "pending.jsonl"), kind: "main", want: RoleWorker},
		{name: "missing parent", path: filepath.Join(root, "absent", "pending.jsonl"), kind: "main", want: RoleWorker},
		{name: "symlink parent", path: filepath.Join(root, "linked", "pending.jsonl"), kind: "main", want: RoleWorker},
		{name: "dangling leaf", path: filepath.Join(project, "dangling.jsonl"), kind: "main", want: RoleWorker},
		{name: "not directory", path: filepath.Join(root, "file-parent", "pending.jsonl"), kind: "main", want: RoleWorker},
		{name: "outside root", path: filepath.Join(t.TempDir(), "pending.jsonl"), kind: "main", want: RoleWorker},
		{name: "parent traversal", path: project + "/../../outside/pending.jsonl", kind: "main", want: RoleWorker},
		{name: "missing then parent traversal", path: root + "/missing/../project/pending.jsonl", kind: "main", want: RoleWorker},
		{name: "symlink then parent traversal", path: root + "/linked/../project/pending.jsonl", kind: "main", want: RoleWorker},
	} {
		t.Run(testCase.name,
			// Decode each callback through the wire format for older-adapter compatibility.
			//
			// Example: missing host identity keeps a pending transcript on Worker.
			func(t *testing.T) {
				t.Setenv("ECI_HOOK_IS_SUBAGENT", testCase.workerOverride)
				payload := map[string]any{"session_id": "owner", "cwd": cwd, "transcript_path": testCase.path}
				if testCase.kind != nil {
					payload["omp_agent_kind"] = testCase.kind
				}
				body, err := json.Marshal(payload)
				require.NoError(t, err)
				var input HookInput
				require.NoError(t, json.Unmarshal(body, &input))
				got, err := hookRequest(input)
				require.NoError(t, err)
				require.Equal(t, testCase.want, got.Role)
				require.Equal(t, MarkerInactive, got.Marker)
			})
	}
	require.NoFileExists(t, filepath.Join(project, "pending.jsonl"))
}

// TestHookContextHostIdentityMatrix checks host identity against saved, pending and conflicting transcript evidence.
//
// Example: only host main admits a verified fork; host sub always remains Worker, including advisor calls.
func TestHookContextHostIdentityMatrix(t *testing.T) {
	_, cwd := contextFixture(t)
	project := sessionsFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Join(project, "children"), 0700))
	paths := map[string]string{
		"pending":        filepath.Join(project, "pending.jsonl"),
		"no-session":     "",
		"missing-parent": filepath.Join(project, "absent", "pending.jsonl"),
		"outside":        filepath.Join(t.TempDir(), "pending.jsonl"),
		"relative":       "relative.jsonl",
		"depth-one":      filepath.Join(filepath.Dir(project), "pending.jsonl"),
		"directory":      project,
		"nonclean":       project + "/absent/../pending.jsonl",
	}
	for name, contents := range map[string]string{
		"main":                    "{\"type\":\"session\"}\n",
		"fork":                    "{\"type\":\"session\",\"parentSession\":\"parent\"}\n",
		"fork-null":               "{\"type\":\"session\",\"parentSession\":null}\n",
		"fork-number":             "{\"type\":\"session\",\"parentSession\":1}\n",
		"nonheader-parent":        "{\"type\":\"session\"}\n{\"type\":\"message\",\"parentSession\":\"p\"}\n",
		"fork-and-nonheader":      "{\"type\":\"session\",\"parentSession\":\"p\"}\n{\"type\":\"message\",\"parentSession\":\"p\"}\n",
		"fork-and-invalid-header": "{\"type\":\"session\",\"parentSession\":\"p\"}\n{\"type\":\"session\",\"parentSession\":null}\n",
		"fork-and-init":           "{\"type\":\"session\",\"parentSession\":\"p\"}\n{\"type\":\"session_init\"}\n",
		"fork-and-custom-init":    "{\"type\":\"session\",\"parentSession\":\"p\"}\n{\"type\":\"custom\",\"customType\":\"session_init\"}\n",
		"main-and-init":           "{\"type\":\"session\"}\n{\"type\":\"session_init\"}\n",
		"duplicate-parent":        "{\"type\":\"session\",\"parentSession\":\"p\",\"parentSession\":null}\n",
		"torn-header":             "{\"type\":\"session\"",
		"children/child":          "{\"type\":\"session\",\"parentSession\":\"p\"}\n",
		"children/nested/child":   "{\"type\":\"session\",\"parentSession\":\"p\"}\n",
		"malformed":               "not json\n",
		"empty":                   "",
	} {
		path := filepath.Join(project, name+".jsonl")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		require.NoError(t, os.WriteFile(path, []byte(contents), 0600))
		paths[name] = path
	}
	paths["leaf-link"] = filepath.Join(project, "linked.jsonl")
	require.NoError(t, os.Symlink(paths["main"], paths["leaf-link"]))
	paths["dangling-link"] = filepath.Join(project, "dangling.jsonl")
	require.NoError(t, os.Symlink(paths["pending"], paths["dangling-link"]))
	linkedParent := filepath.Join(filepath.Dir(project), "linked-project")
	require.NoError(t, os.Symlink(project, linkedParent))
	paths["linked-parent"] = filepath.Join(linkedParent, "pending.jsonl")
	paths["fork-through-symlink-parent"] = filepath.Dir(project) + "/linked-project/../project/fork.jsonl"
	paths["main-through-symlink-parent"] = filepath.Dir(project) + "/linked-project/../project/main.jsonl"
	if os.Geteuid() != 0 {
		paths["unreadable"] = filepath.Join(project, "unreadable.jsonl")
		require.NoError(t, os.WriteFile(paths["unreadable"], []byte("{\"type\":\"session\"}\n"), 0000))
	}
	for _, kind := range []string{"main", "sub", "", "Main"} {
		for _, override := range []string{"", "1", "true"} {
			assertHookHostIdentityPaths(t, cwd, kind, override, paths)
		}
	}
}

// assertHookHostIdentityPaths checks every transcript state for one host identity and environment override.
//
// Example: a main identity without an override admits fork but rejects conflicting child evidence.
func assertHookHostIdentityPaths(
	t *testing.T,
	cwd string,
	kind string,
	override string,
	paths map[string]string,
) {
	t.Helper()
	t.Setenv("ECI_HOOK_IS_SUBAGENT", override)
	for name, path := range paths {
		payload := map[string]string{"cwd": cwd, "transcript_path": path}
		if kind != "" {
			payload["omp_agent_kind"] = kind
		}
		body, err := json.Marshal(payload)
		require.NoError(t, err)
		var input HookInput
		require.NoError(t, json.Unmarshal(body, &input))
		got, err := hookRequest(input)
		require.NoError(t, err)
		want := RoleWorker
		switch {
		case override != "" || kind == "sub":
		case name == "main" || name == "main-through-symlink-parent":
			want = RoleCoordinator
		case kind == "main" && (name == "fork" || name == "pending" || name == "no-session"):
			want = RoleCoordinator
		}
		require.Equal(t, want, got.Role, "kind=%q override=%q transcript=%s", kind, override, name)
		require.Equal(t, MarkerInactive, got.Marker)
	}
}

// TestHookContextPendingRejectsUnreadableParent distinguishes permission errors from lazy persistence.
//
// Example: EACCES beneath a real project directory never grants coordinator ownership.
func TestHookContextPendingRejectsUnreadableParent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	_, cwd := contextFixture(t)
	project := sessionsFixture(t)
	require.NoError(t, os.Chmod(project, 0000))
	// Restore traversal before the temporary home is cleaned.
	//
	// Example: cleanup can remove the fixture home after a permission-denied callback.
	defer func() { require.NoError(t, os.Chmod(project, 0700)) }()
	body, err := json.Marshal(map[string]string{"cwd": cwd, "transcript_path": filepath.Join(project, "pending.jsonl"), "omp_agent_kind": "main"})
	require.NoError(t, err)
	var input HookInput
	require.NoError(t, json.Unmarshal(body, &input))
	got, err := hookRequest(input)
	require.NoError(t, err)
	require.Equal(t, RoleWorker, got.Role)
}

// TestHookInputRejectsNonStringAgentKind keeps malformed wire identity out of the planner.
//
// Example: a numeric kind cannot be interpreted as a documented main identity.
func TestHookInputRejectsNonStringAgentKind(t *testing.T) {
	for _, value := range []string{"1", "true", "{}", "[]", "null"} {
		var input HookInput
		require.Error(t, json.Unmarshal([]byte(`{"omp_agent_kind":`+value+`}`), &input))
	}
}

// TestHookInputAgentKindWireRoundTrip preserves recognized strings and omits absent identity.
//
// Example: a Go callback with main identity encodes the same "main" value accepted from TypeScript.
func TestHookInputAgentKindWireRoundTrip(t *testing.T) {
	for _, wire := range []string{"main", "sub", ""} {
		var input HookInput
		require.NoError(t, json.Unmarshal([]byte(`{"omp_agent_kind":"`+wire+`"}`), &input))
		body, err := json.Marshal(input)
		require.NoError(t, err)
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(body, &fields))
		if wire == "" {
			require.NotContains(t, fields, "omp_agent_kind")
			continue
		}
		require.JSONEq(t, `"`+wire+`"`, string(fields["omp_agent_kind"]))
	}
	var invalid HookInput
	require.NoError(t, json.Unmarshal([]byte(`{"omp_agent_kind":"Main"}`), &invalid))
	_, err := json.Marshal(invalid)
	require.Error(t, err)
}

// TestHookContextPendingAndForkSessionsAliases preserves the configured sessions store and HOME aliases.
//
// Example: pending and forked main files beneath the configured sessions symlink are coordinator, but a linked project is not.
func TestHookContextPendingAndForkSessionsAliases(t *testing.T) {
	_, cwd := contextFixture(t)
	project, alias := sessionsLinkFixture(t)
	root := filepath.Join(os.Getenv("HOME"), ".omp", "agent", "sessions")
	require.NoError(t, os.Symlink(project, filepath.Join(filepath.Dir(project), "linked")))
	require.NoError(t, os.WriteFile(filepath.Join(project, "fork.jsonl"), []byte("{\"type\":\"session\",\"parentSession\":\"parent\"}\n"), 0600))
	for _, path := range []string{
		filepath.Join(project, "pending.jsonl"),
		filepath.Join(root, "project", "pending.jsonl"),
		filepath.Join(alias, ".omp", "agent", "sessions", "project", "pending.jsonl"),
		filepath.Join(project, "fork.jsonl"),
		filepath.Join(root, "project", "fork.jsonl"),
		filepath.Join(alias, ".omp", "agent", "sessions", "project", "fork.jsonl"),
	} {
		body, err := json.Marshal(map[string]string{"cwd": cwd, "transcript_path": path, "omp_agent_kind": "main"})
		require.NoError(t, err)
		var input HookInput
		require.NoError(t, json.Unmarshal(body, &input))
		got, err := hookRequest(input)
		require.NoError(t, err)
		require.Equal(t, RoleCoordinator, got.Role, path)
	}
	body, err := json.Marshal(map[string]string{"cwd": cwd, "transcript_path": filepath.Join(root, "linked", "pending.jsonl"), "omp_agent_kind": "main"})
	require.NoError(t, err)
	var input HookInput
	require.NoError(t, json.Unmarshal(body, &input))
	got, err := hookRequest(input)
	require.NoError(t, err)
	require.Equal(t, RoleWorker, got.Role)
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

// TestHookContextIgnoresLegacyMarkerRecords ignores both valid and invalid legacy records.
//
// Example: neither session_owner nor a broken marker activates callback scope.
func TestHookContextIgnoresLegacyMarkerRecords(t *testing.T) {
	root, cwd := contextFixture(t)
	contextMarker(t, root, "session_owner", cwd)
	got, err := hookRequest(HookInput{SessionID: "owner", CWD: cwd})
	require.NoError(t, err)
	require.Equal(t, MarkerInactive, got.Marker)
	require.Empty(t, got.ActiveMarkers)
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

// TestHookContextIgnoresLegacyDependencyRecord ignores legacy dependency records.
//
// Example: a proof-root symlink and dependency record grant no callback roots.
func TestHookContextIgnoresLegacyDependencyRecord(t *testing.T) {
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
	require.Equal(t, MarkerInactive, got.Marker)
	require.Empty(t, got.ApprovedRoots)
	require.Empty(t, got.ActiveMarkers)
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

// TestHookTouchedRepositoryPreservesIndex checks that bookkeeping is read-only.
//
// Example: refreshing stale tracked-file metadata does not rewrite the index.
func TestHookTouchedRepositoryPreservesIndex(t *testing.T) {
	_, cwd := contextFixture(t)
	foreignIndex := filepath.Join(t.TempDir(), "foreign-index")
	t.Setenv("GIT_INDEX_FILE", foreignIndex)
	file := filepath.Join(cwd, "tracked")
	require.NoError(t, os.WriteFile(file, []byte("data"), 0600))
	for _, args := range [][]string{{"init"}, {"add", "tracked"}} {
		output, err := hookGitOutput(context.Background(), cwd, args...)
		require.NoErrorf(t, err, "%s", output)
	}
	old := time.Unix(1000000000, 0)
	require.NoError(t, os.Chtimes(file, old, old))
	index := filepath.Join(cwd, ".git", "index")
	require.NoError(t, os.Chtimes(index, old, old))
	before, err := os.ReadFile(index)
	require.NoError(t, err)
	info, err := os.Stat(index)
	require.NoError(t, err)
	require.NoError(t, hookTouchedRepository(context.Background(), Request{CWD: cwd, ActiveSession: "test"}))
	after, err := os.ReadFile(index)
	require.NoError(t, err)
	afterInfo, err := os.Stat(index)
	require.NoError(t, err)
	require.Equal(t, before, after)
	_, err = os.Stat(foreignIndex)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Equal(t, info.ModTime(), afterInfo.ModTime())
}

// slowHookRepository creates a real clean filter whose child holds Git's output pipe.
//
// Example: status cannot finish until the filter exits, even after Git is killed.
func slowHookRepository(
	t *testing.T,
	repository string,
	delay string,
) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "test@example.invalid"}, {"config", "user.name", "Test"}} {
		output, err := exec.Command("/usr/bin/git", append([]string{"-C", repository}, args...)...).CombinedOutput()
		require.NoErrorf(t, err, "%s", output)
	}
	require.NoError(t, os.WriteFile(filepath.Join(repository, ".gitattributes"), []byte("tracked filter=slow\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(repository, "tracked"), []byte("initial\n"), 0600))
	for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "baseline"}, {"config", "filter.slow.clean", "echo $$ > filter.pid; exec sleep " + delay}} {
		output, err := exec.Command("/usr/bin/git", append([]string{"-C", repository}, args...)...).CombinedOutput()
		require.NoErrorf(t, err, "%s", output)
	}
	require.NoError(t, os.WriteFile(filepath.Join(repository, "tracked"), []byte("changed\n"), 0600))
	old := time.Unix(1000000000, 0)
	require.NoError(t, os.Chtimes(filepath.Join(repository, "tracked"), old, old))
	// Kill the exact filter child even when the assertion exits before Git finishes.
	t.Cleanup(func() {
		data, err := os.ReadFile(filepath.Join(repository, "filter.pid"))
		if os.IsNotExist(err) {
			return
		}
		require.NoError(t, err)
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		require.NoError(t, err)
		process, err := os.FindProcess(pid)
		require.NoError(t, err)
		err = process.Kill()
		require.True(t, err == nil || errors.Is(err, os.ErrProcessDone), "%v", err)
	})
}

// TestHookActivitySlowRepositoryIsBounded retains an honest one-time incomplete baseline.
//
// Example: a filter holding the output pipe cannot delay every later shell write.
func TestHookActivitySlowRepositoryIsBounded(t *testing.T) {
	root, repository := contextFixture(t)
	slowHookRepository(t, repository, "8")
	index := filepath.Join(repository, ".git", "index")
	before, err := os.ReadFile(index)
	require.NoError(t, err)
	request := Request{CWD: repository, ActiveSession: "bounded", Role: RoleWorker}
	result := Result{ShellAnalysis: &ShellAnalysis{OutputWrites: true}}
	start := time.Now()
	err = recordHookActivity(request, result)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), 7*time.Second)
	entries, err := os.ReadDir(filepath.Join(root, "touched-repos", "sessions", "bounded"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	path := filepath.Join(root, "touched-repos", "sessions", "bounded", entries[0].Name())
	baseline, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(baseline), "status_sha: unavailable\n")
	require.NotContains(t, string(baseline), "head: unavailable\n")
	after, err := os.ReadFile(index)
	require.NoError(t, err)
	require.Equal(t, before, after)
	start = time.Now()
	require.NoError(t, recordHookActivity(request, result))
	require.Less(t, time.Since(start), time.Second)
	repeated, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, baseline, repeated)
}

// TestHookContextIgnoresManyMarkers ignores markers regardless of count.
//
// Example: a sorted-late callback session remains inactive among many markers.
func TestHookContextIgnoresManyMarkers(t *testing.T) {
	root, cwd := contextFixture(t)
	for index := 0; index < 65; index++ {
		contextMarker(t, root, fmt.Sprintf("aaa-%02d", index), cwd)
	}
	contextMarker(t, root, "zzz-owner", cwd)
	request, err := hookRequest(HookInput{SessionID: "zzz-owner", CWD: cwd})
	require.NoError(t, err)
	require.Equal(t, MarkerInactive, request.Marker)
	require.Equal(t, "zzz-owner", request.ActiveSession)
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

// TestHookUnknownCWDDoesNotUseProcessDirectory checks the explicit unknown envelope.
//
// Example: a virtual cwd cannot create a baseline for the planner's process cwd.
func TestHookUnknownCWDDoesNotUseProcessDirectory(t *testing.T) {
	root, repo := contextFixture(t)
	output, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput()
	require.NoErrorf(t, err, "%s", output)
	t.Chdir(repo)
	request, err := hookRequest(HookInput{SessionID: "child", CWDUnknown: true})
	require.NoError(t, err)
	require.Empty(t, request.CWD)
	request.HookMode = true
	request.CollectShellCommands = true
	request.Command = "touch relative-file"
	result := Classify(request)
	require.NotNil(t, result.ShellAnalysis)
	require.NotEmpty(t, result.ShellAnalysis.Commands)
	require.False(t, result.ShellAnalysis.Commands[0].CWDKnown)
	require.NoError(t, recordHookActivity(request, Result{ShellAnalysis: &ShellAnalysis{OutputWrites: true}}))
	_, err = os.Stat(filepath.Join(root, "touched-repos"))
	require.True(t, os.IsNotExist(err), "unknown cwd recorded a repository: %v", err)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "existing-tool"), []byte("#!/bin/sh\n"), 0700))
	require.Empty(t, resolveExecutable("./existing-tool", ""))
}

// TestHookActivityUnknownSegmentPreservesAbsoluteTargets checks per-segment cwd.
//
// Example: an absolute git -C still records its repository when the cwd is unknown.
func TestHookActivityUnknownSegmentPreservesAbsoluteTargets(t *testing.T) {
	root, repo := contextFixture(t)
	output, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput()
	require.NoErrorf(t, err, "%s", output)
	request := Request{Role: RoleWorker, ActiveSession: "child", CWD: repo}
	analysis := &ShellAnalysis{Commands: []ShellCommandRecord{{Argv: []string{"touch", "relative"}}}}
	require.NoError(t, recordHookActivity(request, Result{ShellAnalysis: analysis}))
	_, err = os.Stat(filepath.Join(root, "touched-repos"))
	require.True(t, os.IsNotExist(err), "unknown segment inherited callback repo: %v", err)
	analysis.Commands[0].Argv = []string{"git", "-C", repo, "add", "file"}
	require.NoError(t, recordHookActivity(request, Result{ShellAnalysis: analysis}))
	entries, err := os.ReadDir(filepath.Join(root, "touched-repos", "sessions", "child"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

// TestHookActivityRelativeWorkerRepositoryUsesSegmentCWD checks scoped relative paths.
//
// Example: --repo dependency is relative to the known command directory.
func TestHookActivityRelativeWorkerRepositoryUsesSegmentCWD(t *testing.T) {
	root, cwd := contextFixture(t)
	repo := filepath.Join(cwd, "dependency")
	require.NoError(t, os.Mkdir(repo, 0700))
	output, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput()
	require.NoErrorf(t, err, "%s", output)
	request := Request{Role: RoleWorker, ActiveSession: "child", CWD: cwd}
	analysis := &ShellAnalysis{Commands: []ShellCommandRecord{{Argv: []string{"eci-worker-git", "--repo", "dependency", "stage"}, CWD: cwd, CWDKnown: true}}}
	require.NoError(t, recordHookActivity(request, Result{ShellAnalysis: analysis}))
	entries, err := os.ReadDir(filepath.Join(root, "touched-repos", "sessions", "child"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

// TestHookUnknownCWDDoesNotRecoverAfterAbsoluteCD preserves explicit uncertainty.
//
// Example: cd /absolute cannot turn an unknown callback cwd into a known segment.
func TestHookUnknownCWDDoesNotRecoverAfterAbsoluteCD(t *testing.T) {
	target := t.TempDir()
	for _, tc := range []struct {
		name      string
		cwd       string
		candidate bool
	}{
		{name: "unknown", cwd: "", candidate: false},
		{name: "known", cwd: t.TempDir(), candidate: true},
	} {
		t.Run(tc.name,
			// Preserve uncertainty when the incoming working directory is unknown.
			//
			// Example: an absolute cd does not recover an unknown callback cwd.
			func(t *testing.T) {
				request := Request{Provider: ProviderOmp, Role: RoleCoordinator, CWD: tc.cwd, Command: "cd " + target + " && touch relative", HookMode: true, CollectShellCommands: true}
				result := Classify(request)
				require.NotNil(t, result.ShellAnalysis)
				require.Len(t, result.ShellAnalysis.Commands, 2)
				if tc.candidate {
					require.Contains(t, result.ShellAnalysis.Commands[1].CWDCandidates, target)
				} else {
					require.False(t, result.ShellAnalysis.Commands[1].CWDKnown)
					require.Empty(t, result.ShellAnalysis.Commands[1].CWDCandidates)
				}
			})
	}
}

// TestHookTouchedRepositoryExpiredContext records nothing without a known repository.
//
// Example: cancellation is an error, not an ordinary Git not-a-repository exit.
func TestHookTouchedRepositoryExpiredContext(t *testing.T) {
	root, repository := contextFixture(t)
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer cancel()
	require.ErrorIs(t, hookTouchedRepository(ctx, Request{CWD: repository, ActiveSession: "expired"}), context.DeadlineExceeded)
	_, err := os.Stat(filepath.Join(root, "touched-repos"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

// TestHookActivityMultipleRepositoriesShareBudget bounds the complete callback.
//
// Example: a second slow repository gets only the budget left by the first scan.
func TestHookActivityMultipleRepositoriesShareBudget(t *testing.T) {
	root, first := contextFixture(t)
	second := t.TempDir()
	slowHookRepository(t, first, "3")
	slowHookRepository(t, second, "8")
	request := Request{CWD: first, ActiveSession: "multiple", Role: RoleWorker}
	result := Result{ShellAnalysis: &ShellAnalysis{Commands: []ShellCommandRecord{
		{Argv: []string{"touch", "first"}, CWD: first, CWDKnown: true},
		{Argv: []string{"touch", "second"}, CWD: second, CWDKnown: true},
	}}}
	start := time.Now()
	require.ErrorIs(t, recordHookActivity(request, result), context.DeadlineExceeded)
	require.Less(t, time.Since(start), 7*time.Second)
	entries, err := os.ReadDir(filepath.Join(root, "touched-repos", "sessions", "multiple"))
	require.NoError(t, err)
	require.Len(t, entries, 2)
	var incomplete int
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(root, "touched-repos", "sessions", "multiple", entry.Name()))
		require.NoError(t, err)
		if strings.Contains(string(data), "status_sha: unavailable\n") {
			incomplete++
		}
	}
	require.Equal(t, 1, incomplete)
	start = time.Now()
	require.NoError(t, recordHookActivity(request, result))
	require.Less(t, time.Since(start), time.Second)
}

// TestHookTouchedRepositoryStatusFailureDoesNotInventBaseline propagates a real Git error.
//
// Example: a required clean filter failure cannot become a valid clean-status hash.
func TestHookTouchedRepositoryStatusFailureDoesNotInventBaseline(t *testing.T) {
	root, repository := contextFixture(t)
	slowHookRepository(t, repository, "0")
	for _, args := range [][]string{{"config", "filter.slow.clean", "false"}, {"config", "filter.slow.required", "true"}} {
		output, err := hookGitOutput(context.Background(), repository, args...)
		require.NoErrorf(t, err, "%s", output)
	}
	err := hookTouchedRepository(context.Background(), Request{CWD: repository, ActiveSession: "failed"})
	var exitError *exec.ExitError
	require.ErrorAs(t, err, &exitError)
	entries, err := os.ReadDir(filepath.Join(root, "touched-repos", "sessions", "failed"))
	require.NoError(t, err)
	require.Empty(t, entries)
}
