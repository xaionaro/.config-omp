package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// snapshotFixture supplies changed and unchanged binding conditions with comments.
//
// Example: capture unit-aware compactness without discarding expanded-content acceptance.
const snapshotFixture = `# keep this unrelated comment
schema: "project-understanding/v1"
sections:
  task:
    title: "Synthetic capture verification"
records:
  requirement:metrics:
    section: task
    type: requirement
    data:
      source_text: "User: old compactness"
      acceptance: ["Compact expanded content; collapse is additional"]
    evidence: ["original source"]
  requirement:preserved:
    section: task
    type: requirement
    data:
      source_text: "User: Preserve useful values, units, colors, sign, zero versus unavailable and readable font settings"
      acceptance: ["No font shrinking or height cap as substitute"]
      domain_detail: "must survive projection"
    evidence: ["preserved evidence"]
  outcome:metrics:
    section: task
    type: outcome
    data: {description: "Compact Dashboard metrics"}
    links:
      requirements: ["requirement:metrics", "requirement:preserved"]
      scope: ["scope:metrics", "scope:preserved"]
    evidence: ["outcome evidence"]
  scope:metrics:
    section: task
    type: scope
    data: {include: ["expanded panel"], exclude: ["production deployment"]}
    evidence: ["scope evidence"]
  scope:preserved:
    section: task
    type: scope
    data: {include: ["unit formatting"], exclude: ["unrelated panels"]}
    evidence: ["second scope evidence"]
  unrelated:
    type: context
    data: |-
      retain: exact bytes and ordering
    evidence: []
`

// cliFixture owns disposable canonical state and its request, never historical sessions.
//
// Example: execute the installed CLI against a temporary explicit proof session.
type cliFixture struct {
	Ledger  string
	Request string
	Root    string
	Home    string
	CLI     string
}

// newCLIFixture creates an isolated native snapshot and structured request.
//
// Example: prepare a changed assignment using the existing metrics requirement identity.
func newCLIFixture(t *testing.T) cliFixture {
	t.Helper()
	// The CLI rejects ledgers whose resolved path differs from the lexical path,
	// so the fixture root must be canonical even when TMPDIR crosses a symlink.
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	home := filepath.Join(root, "home")
	// The canonical ledger lives in <HOME>/.agent/context/sessions/<sid>, as in the pre-migration source;
	// the proof root only names the session.
	session := filepath.Join(home, ".agent", "context", "sessions", "test-capture")
	require.NoError(t, os.MkdirAll(session, 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "proof", "test-capture"), 0700))
	ledger := filepath.Join(session, "project-understanding.yaml")
	require.NoError(t, os.WriteFile(ledger, []byte(snapshotFixture), 0640))
	cli, err := filepath.Abs("../../../bin/eci-active")
	require.NoError(t, err)
	request := filepath.Join(root, "request.json")
	contents := `{"edits":[{"old":"User: old compactness","new":"User: 0.4A is more compact than 443 mA"}],"requirements":["requirement:metrics"],"outcome":"outcome:metrics","target":"/root/receiver","instructions":"Implement locally; preserve every binding condition.","actor":{"id":"/root","role":"Supervisor"},"reason":"material correction"}`
	require.NoError(t, os.WriteFile(request, []byte(contents), 0600))
	return cliFixture{Ledger: ledger, Request: request, Root: root, Home: home, CLI: cli}
}

// env isolates HOME so ledger and log writes never reach the real ~/.agent.
// The CLI runs `go run` for status refresh; Go derives its module cache and GOPATH
// from HOME, so those are pinned to the account locations to keep module downloads
// and read-only cache files out of the temporary HOME. The build cache is pinned to
// the path `go env GOCACHE` reports under the account HOME; a non-empty GOCACHE the
// caller set is inherited unchanged.
//
// Example: every CLI invocation of one fixture shares the same temporary HOME.
func (f cliFixture) env(t *testing.T) []string {
	t.Helper()
	env := append(os.Environ(),
		"HOME="+f.Home,
		"ECI_PROOF_ROOT="+filepath.Join(f.Root, "proof"),
		"ECI_SESSION_ID=test-capture")
	if os.Getenv("GOCACHE") == "" {
		cache, err := globalGoCache()
		require.NoError(t, err, "pin GOCACHE to the global build cache")
		env = append(env, "GOCACHE="+cache)
	}

	_, gopathSet := os.LookupEnv("GOPATH")
	_, modcacheSet := os.LookupEnv("GOMODCACHE")
	if !gopathSet || !modcacheSet {
		acctHome, err := accountHome()
		require.NoError(t, err, "pin GOPATH and GOMODCACHE to the account home")
		if !gopathSet {
			env = append(env, "GOPATH="+filepath.Join(acctHome, "go"))
		}
		if !modcacheSet {
			env = append(env, "GOMODCACHE="+filepath.Join(acctHome, "go", "pkg", "mod"))
		}
	}
	return env
}

// resolveGoCache returns the absolute build cache directory that `go env GOCACHE` reports under the account HOME.
//
// Example: resolve the build cache under the account home, not the fixture's isolated HOME.
func resolveGoCache() (string, error) {
	home, err := accountHome()
	if err != nil {
		return "", err
	}
	cmd := exec.Command("go", "env", "GOCACHE")
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go env GOCACHE: %w", err)
	}
	cache := strings.TrimSpace(string(out))
	if !filepath.IsAbs(cache) {
		return "", fmt.Errorf("go env GOCACHE returned %q; want an absolute path", cache)
	}
	return cache, nil
}

// accountHome returns the account's home directory from the user database, never from $HOME.
//
// Example: resolve the account home before a fixture overrides HOME.
func accountHome() (string, error) {
	u, err := user.LookupId(strconv.Itoa(os.Getuid()))
	if err != nil {
		return "", fmt.Errorf("look up account home: %w", err)
	}
	if u.HomeDir == "" {
		return "", fmt.Errorf("account %q has no home directory", u.Username)
	}
	return u.HomeDir, nil
}

// globalGoCache memoizes resolveGoCache so every fixture in the test process shares one `go env` run.
//
// Example: env calls globalGoCache to pin a child CLI's GOCACHE to the account cache.
var globalGoCache = sync.OnceValues(resolveGoCache)

// invoke captures actual stdout separately so failed operations cannot hide assignment output.
//
// Example: assert publication and logging failures leave stdout empty.
func (f cliFixture) invoke(t *testing.T) (string, string, error) {
	t.Helper()
	command := exec.Command(f.CLI, "requirement-assignment", "--request", f.Request)
	command.Env = f.env(t)
	var stderr strings.Builder
	command.Stderr = &stderr
	output, err := command.Output()
	return string(output), stderr.String(), err
}

// TestCaptureProjectsSavedFullChain rejects assignment-only handling and omitted unchanged bindings.
//
// Example: the original compactness correction must retain all outcome requirements and scopes.
func TestCaptureProjectsSavedFullChain(t *testing.T) {
	f := newCLIFixture(t)
	output, stderr, err := f.invoke(t)
	require.NoError(t, err, stderr)
	var payload struct {
		Target  string
		Message string
	}
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.Equal(t, "/root/receiver", payload.Target)
	for _, binding := range []string{"0.4A is more compact than 443 mA", "Compact expanded content; collapse is additional", "requirement:preserved", "must survive projection", "scope:preserved", "unrelated panels", "preserved evidence"} {
		require.Contains(t, payload.Message, binding)
	}
	require.NotContains(t, payload.Message, "User: old compactness")
	saved, err := os.ReadFile(f.Ledger)
	require.NoError(t, err)
	require.Equal(t, strings.Replace(snapshotFixture, "User: old compactness", "User: 0.4A is more compact than 443 mA", 1), string(saved))
	info, err := os.Stat(f.Ledger)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0640), info.Mode().Perm())
	log, err := os.ReadFile(filepath.Join(filepath.Dir(f.Ledger), "high_level_log.jsonl"))
	require.NoError(t, err)
	require.Contains(t, string(log), "assignment preparation")
	require.NotContains(t, string(log), "delivered")
}

// rewriteRequest changes only test request fields before invoking the production command.
//
// Example: reuse a faithful record by submitting an empty edits array.
func (f cliFixture) rewriteRequest(t *testing.T, edits string) {
	t.Helper()
	contents, err := os.ReadFile(f.Request)
	require.NoError(t, err)
	var request map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(contents, &request))
	request["edits"] = json.RawMessage(edits)
	contents, err = json.Marshal(request)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(f.Request, contents, 0600))
}

// TestFailedPreparationEmitsNothing covers stale edits and incomplete canonical chains.
//
// Example: a missing unchanged requirement cannot be ignored just because the changed one exists.
func TestFailedPreparationEmitsNothing(t *testing.T) {
	// Each case independently falsifies a prepublication boundary.
	for _, name := range []string{"stale", "ambiguous", "missing-link", "selected-not-linked", "unknown-request"} {
		t.Run(name,
			// Isolate each rejected preparation and verify its specific cause without snapshot mutation.
			//
			// Example: a missing linked requirement yields no assignment or ledger change.
			func(t *testing.T) {
				f := newCLIFixture(t)
				original := snapshotFixture
				expectedError := "native edit is stale"
				switch name {
				case "stale":
					f.rewriteRequest(t, `[{"old":"not current","new":"new"}]`)
				case "ambiguous":
					f.rewriteRequest(t, `[{"old":"requirement","new":"new"}]`)
				case "missing-link":
					original = strings.Replace(original, `requirements: ["requirement:metrics", "requirement:preserved"]`, `requirements: ["requirement:metrics", "requirement:absent"]`, 1)
					expectedError = "linked record requirement:absent"
				case "selected-not-linked":
					original = strings.Replace(original, `requirements: ["requirement:metrics", "requirement:preserved"]`, `requirements: ["requirement:preserved"]`, 1)
					expectedError = "is not linked to outcome"
				case "unknown-request":
					expectedError = "unknown field"
					contents, err := os.ReadFile(f.Request)
					require.NoError(t, err)
					require.NoError(t, os.WriteFile(f.Request, []byte(strings.Replace(string(contents), `"reason":`, `"requirements_message":"independent stale prose","reason":`, 1)), 0600))
				}
				require.NoError(t, os.WriteFile(f.Ledger, []byte(original), 0640))
				output, stderr, err := f.invoke(t)
				require.Error(t, err)
				require.Empty(t, output)
				require.NotEmpty(t, stderr)
				require.Contains(t, stderr, expectedError)
				saved, err := os.ReadFile(f.Ledger)
				require.NoError(t, err)
				require.Equal(t, original, string(saved))
				_, err = os.Stat(filepath.Join(filepath.Dir(f.Ledger), "high_level_log.jsonl"))
				require.True(t, os.IsNotExist(err))
			})
	}
}

// TestFaithfulReuseDoesNotRewrite proves reuse preserves the native inode, timestamp, and bytes.
//
// Example: unchanged requirement capture still prepares from all saved bindings.
func TestFaithfulReuseDoesNotRewrite(t *testing.T) {
	f := newCLIFixture(t)
	f.rewriteRequest(t, `[]`)
	before, err := os.Stat(f.Ledger)
	require.NoError(t, err)
	output, stderr, err := f.invoke(t)
	require.NoError(t, err, stderr)
	after, err := os.Stat(f.Ledger)
	require.NoError(t, err)
	require.True(t, os.SameFile(before, after))
	require.Equal(t, before.ModTime(), after.ModTime())
	saved, err := os.ReadFile(f.Ledger)
	require.NoError(t, err)
	require.Equal(t, snapshotFixture, string(saved))
	require.Contains(t, output, "requirement:preserved")
}

// TestPublicationFailureEmitsNothing uses a real unwritable directory, without a simulated writer.
//
// Example: failure to create the same-directory replacement leaves the snapshot unchanged.
func TestPublicationFailureEmitsNothing(t *testing.T) {
	f := newCLIFixture(t)
	directory := filepath.Dir(f.Ledger)
	require.NoError(t, os.Chmod(directory, 0500))
	defer
	// Restore writable fixture permissions so test cleanup can remove the disposable session.
	//
	// Example: remove the failed-publication fixture after assertions finish.
	func() { require.NoError(t, os.Chmod(directory, 0700)) }()
	output, stderr, err := f.invoke(t)
	require.Error(t, err, stderr)
	require.Empty(t, output)
	require.Contains(t, stderr, "permission denied")
	saved, err := os.ReadFile(f.Ledger)
	require.NoError(t, err)
	require.Equal(t, snapshotFixture, string(saved))
}

// TestLogFailureReportsSavedSnapshot permits explicit retry without pretending to roll back.
//
// Example: an unsafe log destination blocks assignment output after successful publication.
func TestLogFailureReportsSavedSnapshot(t *testing.T) {
	f := newCLIFixture(t)
	require.NoError(t, os.Mkdir(filepath.Join(filepath.Dir(f.Ledger), "high_level_log.jsonl"), 0700))
	output, stderr, err := f.invoke(t)
	require.Error(t, err)
	require.Empty(t, output)
	require.Contains(t, stderr, "snapshot saved")
	require.Contains(t, stderr, "reconcile missing material-change history with ledger-append")
	require.Contains(t, stderr, "reuse")
	saved, err := os.ReadFile(f.Ledger)
	require.NoError(t, err)
	require.Contains(t, string(saved), "0.4A is more compact than 443 mA")
	require.NotContains(t, string(saved), "old compactness")
	before, err := os.Stat(f.Ledger)
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(filepath.Dir(f.Ledger), "high_level_log.jsonl")))
	recoveryEvent := `{"event":"requirement_change","actor":{"id":"/root","role":"Supervisor"},"summary":"Recovered original material requirement capture before reuse","change":{"before":"User: old compactness","after":"User: 0.4A is more compact than 443 mA"},"reason":"Snapshot saved while log append failed","evidence":["Current snapshot and failed request exact native edits"]}`
	recovery := exec.Command(f.CLI, "ledger-append", "--json", recoveryEvent)
	recovery.Env = f.env(t)
	recoveryOutput, err := recovery.CombinedOutput()
	require.NoError(t, err, string(recoveryOutput))
	history, err := os.ReadFile(filepath.Join(filepath.Dir(f.Ledger), "high_level_log.jsonl"))
	require.NoError(t, err)
	require.Contains(t, string(history), `"before":"User: old compactness"`)
	require.Contains(t, string(history), `"after":"User: 0.4A is more compact than 443 mA"`)
	f.rewriteRequest(t, `[]`)
	output, stderr, err = f.invoke(t)
	require.NoError(t, err, stderr)
	require.NotEmpty(t, output)
	after, err := os.Stat(f.Ledger)
	require.NoError(t, err)
	require.True(t, os.SameFile(before, after))
	require.Equal(t, before.ModTime(), after.ModTime())
	history, err = os.ReadFile(filepath.Join(filepath.Dir(f.Ledger), "high_level_log.jsonl"))
	require.NoError(t, err)
	require.Equal(t, 2, len(strings.Split(strings.TrimSpace(string(history)), "\n")))
	require.Contains(t, string(history), `"before":"User: old compactness"`)
}

// TestDifferentRetentionCorrection replaces obsolete permission and preserves the full chain.
//
// Example: summarization alone cannot authorize deletion after a retention correction.
func TestDifferentRetentionCorrection(t *testing.T) {
	f := newCLIFixture(t)
	original := strings.Replace(snapshotFixture, "User: old compactness", "User: Delete raw logs after summarizing", 1)
	require.NoError(t, os.WriteFile(f.Ledger, []byte(original), 0640))
	f.rewriteRequest(t, `[{"old":"User: Delete raw logs after summarizing","new":"User: Retain raw diagnostic logs until manual review; do not delete them after summarizing."},{"old":"Compact expanded content; collapse is additional","new":"Retain raw logs until manual review; summarization alone is not a deletion condition"}]`)
	output, stderr, err := f.invoke(t)
	require.NoError(t, err, stderr)
	require.Contains(t, output, "Retain raw diagnostic logs until manual review")
	require.Contains(t, output, "summarization alone is not a deletion condition")
	require.Contains(t, output, "scope:preserved")
	require.Contains(t, output, "requirement:preserved")
	require.NotContains(t, output, "Delete raw logs after summarizing")
}

// TestOmittedUnchangedAcceptanceIsRefused checks the full chain instead of only new text and transport.
//
// Example: a corrected compactness source is insufficient when another binding acceptance is missing.
func TestOmittedUnchangedAcceptanceIsRefused(t *testing.T) {
	f := newCLIFixture(t)
	original := strings.Replace(snapshotFixture, `acceptance: ["No font shrinking or height cap as substitute"]`, `acceptance: []`, 1)
	require.NoError(t, os.WriteFile(f.Ledger, []byte(original), 0640))
	output, stderr, err := f.invoke(t)
	require.Error(t, err)
	require.Empty(t, output)
	require.Contains(t, stderr, "requirement:preserved acceptance")
	saved, err := os.ReadFile(f.Ledger)
	require.NoError(t, err)
	require.Equal(t, original, string(saved))
}

// TestExactAttributedMultilineSources projects all three original compactness statements faithfully.
//
// Example: the unit example does not become a universal decimal rule.
func TestExactAttributedMultilineSources(t *testing.T) {
	f := newCLIFixture(t)
	f.rewriteRequest(t, `[{"old":"source_text: \"User: old compactness\"","new":"source_text: |-\n        User: 0.4A is more compact than 443 mA\n        User: You must keep them as compact/compressed as possible.\n        User: Every bit of space must have value there"}]`)
	output, stderr, err := f.invoke(t)
	require.NoError(t, err, stderr)
	var payload Payload
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	for _, source := range []string{"User: 0.4A is more compact than 443 mA", "User: You must keep them as compact/compressed as possible.", "User: Every bit of space must have value there"} {
		require.Contains(t, payload.Message, source)
	}
	require.Contains(t, payload.Message, "Compact expanded content; collapse is additional")
	require.Contains(t, payload.Message, "zero versus unavailable")
}

// TestUnknownCaptureEventKindIsRejected prevents a generated event from inventing a wire value.
//
// Example: unsupported event kinds fail serialization before log publication.
func TestUnknownCaptureEventKindIsRejected(t *testing.T) {
	for _, kind := range []CaptureEventKind{0, -1, 3} {
		encoded, err := json.Marshal(CaptureEvent{Event: kind})
		require.ErrorContains(t, err, "unknown capture event kind")
		require.Empty(t, encoded)
	}
}

// TestCaptureEventWireKinds preserves both canonical event alternatives after integer typing.
//
// Example: no-write preparation still logs decision rather than a numeric enum value.
func TestCaptureEventWireKinds(t *testing.T) {
	for kind, expected := range map[CaptureEventKind]string{CaptureDecision: "decision", CaptureRequirementChange: "requirement_change"} {
		encoded, err := json.Marshal(CaptureEvent{Event: kind})
		require.NoError(t, err)
		var wire struct {
			Event string `json:"event"`
		}
		require.NoError(t, json.Unmarshal(encoded, &wire))
		require.Equal(t, expected, wire.Event)
	}
}
