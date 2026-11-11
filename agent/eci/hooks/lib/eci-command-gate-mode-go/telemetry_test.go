//go:build linux

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unicode/utf8"
)

// TestParseDenialReducesToClosedFields checks that unregistered identities collapse to the closed fallbacks.
//
// Example: an unknown denial code and operation become the two closed fallback values.
func TestParseDenialReducesToClosedFields(t *testing.T) {
	raw := []byte(`{"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"[ECI_UNREGISTERED] ECI gate denied (phase=PreToolUse, operation=unregistered, token=opaque); reason: sample"}}`)

	denial, err := ParseDenial(raw)
	if err != nil {
		t.Fatalf("ParseDenial(): %v", err)
	}
	if denial.Code != TelemetryCodeOther {
		t.Fatalf("code = %q, want %q", denial.Code, TelemetryCodeOther)
	}
	if denial.Operation != TelemetryOperationOther {
		t.Fatalf("operation = %q, want %q", denial.Operation, TelemetryOperationOther)
	}
}

// TestParseDenialUsesThePublishedRegistries checks every registered identity path.
//
// Example: a current command-syntax denial stays specific instead of collapsing to ECI_OTHER_DENIAL.
func TestParseDenialUsesThePublishedRegistries(t *testing.T) {
	raw := []byte(`{"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"[ECI_COMMAND_SYNTAX_DENIED] ECI gate denied (phase=PreToolUse, operation=acceptance-boundary, token=opaque); reason: syntax"}}`)
	denial, err := ParseDenial(raw)
	if err != nil {
		t.Fatalf("ParseDenial(): %v", err)
	}
	if denial.Code != TelemetryCodeCommandSyntaxDenied {
		t.Fatalf("code = %q, want %q", denial.Code, TelemetryCodeCommandSyntaxDenied)
	}
	if denial.Operation != TelemetryOperationAcceptanceBoundary {
		t.Fatalf("operation = %q, want %q", denial.Operation, TelemetryOperationAcceptanceBoundary)
	}
	if string(denial.Raw) != string(raw) {
		t.Fatalf("raw = %q, want original denial bytes", denial.Raw)
	}

	knownCodes := []TelemetryCode{
		TelemetryCodeBroadDestructiveDenied,
		TelemetryCodeCommandDynamicIndirectionDenied,
		TelemetryCodeCommandNonliteralDenied,
		TelemetryCodeCommandNotAllowlisted,
		TelemetryCodeCommandSyntaxDenied,
		TelemetryCodeCommandWrapperUnsupported,
		TelemetryCodeCommitAdmissionRequired,
		TelemetryCodeControlIdentityDenied,
		TelemetryCodeControlOwnerRequired,
		TelemetryCodeCoordinatorCleanupPipelineDenied,
		TelemetryCodeCoordinatorControlPipelineDenied,
		TelemetryCodeCoordinatorRouteArgumentsDenied,
		TelemetryCodeCoordinatorSourceWriteDenied,
		TelemetryCodeEnvironmentContextDenied,
		TelemetryCodeEnvironmentEnumerationDenied,
		TelemetryCodeEnvironmentNameDenied,
		TelemetryCodeEnvironmentOptionDenied,
		TelemetryCodeGitBranchRemoteDenied,
		TelemetryCodeGitDynamicExecutionDenied,
		TelemetryCodeGitExecutionContextDenied,
		TelemetryCodeGitMutationDenied,
		TelemetryCodeHookIdentityMalformed,
		TelemetryCodeLifecycleArgumentsDenied,
		TelemetryCodeLifecycleIdentityDenied,
		TelemetryCodeLifecycleOwnerRequired,
		TelemetryCodeMarkerMalformed,
		TelemetryCodeMarkerMissingCurrent,
		TelemetryCodeMarkerOwnershipAmbiguous,
		TelemetryCodeMarkerOwnershipInvalid,
		TelemetryCodeMarkerScopeMismatch,
		TelemetryCodeMarkerUnsafePath,
		TelemetryCodePlanDynamicLaunchDenied,
		TelemetryCodePlanInternalDenied,
		TelemetryCodePlanLifecycleIdentityDenied,
		TelemetryCodePlanLimitDenied,
		TelemetryCodePlanLiveControlDenied,
		TelemetryCodePlanSyntaxDenied,
		TelemetryCodePlanWrapperDenied,
		TelemetryCodePlanWrapperDepthDenied,
		TelemetryCodeProofPathEscapeDenied,
		TelemetryCodeReviewGateArgumentsDenied,
		TelemetryCodeWorkerAcceptanceDenied,
		TelemetryCodeWorkerCommandNotAllowlisted,
		TelemetryCodeWorkerControlReadDenied,
		TelemetryCodeWorkerControlScriptDenied,
		TelemetryCodeWorkerCoordinatorRouteDenied,
		TelemetryCodeWorkerGitOwnershipDenied,
		TelemetryCodeWorkerInstructionReadDenied,
		TelemetryCodeWorkerLauncherDenied,
		TelemetryCodeWorkerReviewGateDenied,
		TelemetryCodeOther,
	}
	for _, code := range knownCodes {
		if _, ok := telemetryCodeRegistry[code]; !ok {
			t.Errorf("code %q is missing from the parser registry", code)
		}
	}
	if len(telemetryCodeRegistry) != len(knownCodes) {
		t.Fatalf("code registry has %d entries, want %d", len(telemetryCodeRegistry), len(knownCodes))
	}

	knownOperations := []TelemetryOperation{
		TelemetryOperationAcceptanceBoundary,
		TelemetryOperationBroadDestructive,
		TelemetryOperationCommitBoundary,
		TelemetryOperationCoordinatorCleanupRoute,
		TelemetryOperationCoordinatorMktemp,
		TelemetryOperationCoordinatorRoute,
		TelemetryOperationCoordinatorSourceWrite,
		TelemetryOperationCoordinatorStaticPipeline,
		TelemetryOperationDirectArgv,
		TelemetryOperationECIControl,
		TelemetryOperationECILifecycle,
		TelemetryOperationECIOff,
		TelemetryOperationEnvironmentBoundary,
		TelemetryOperationGitBranchRemote,
		TelemetryOperationGitExecutionContext,
		TelemetryOperationHookIdentity,
		TelemetryOperationPlanSegment,
		TelemetryOperationProofPathOwnership,
		TelemetryOperationReviewGate,
		TelemetryOperationWorkerAcceptance,
		TelemetryOperationWorkerCommand,
		TelemetryOperationWorkerControl,
		TelemetryOperationWorkerControlRead,
		TelemetryOperationWorkerControlScript,
		TelemetryOperationWorkerGitOwnership,
		TelemetryOperationWorkerInstructionRead,
		TelemetryOperationWorkerLauncher,
		TelemetryOperationWorkerReviewGate,
		TelemetryOperationOther,
	}
	for _, operation := range knownOperations {
		if _, ok := telemetryOperationRegistry[operation]; !ok {
			t.Errorf("operation %q is missing from the parser registry", operation)
		}
	}
	if len(telemetryOperationRegistry) != len(knownOperations) {
		t.Fatalf("operation registry has %d entries, want %d", len(telemetryOperationRegistry), len(knownOperations))
	}
}

// TestParseDenialRetainsCompilerLikeErrorCategories protects compatibility diagnostics.
//
// Example: malformed JSON is a document error, while a valid denial without identities is an identity error.
func TestParseDenialRetainsCompilerLikeErrorCategories(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
		want string
	}{
		{name: "empty", raw: nil, want: "denial size rejected"},
		{name: "malformed document", raw: []byte("not-json"), want: "denial document rejected"},
		{name: "missing document field", raw: []byte(`{"hookSpecificOutput":{}}`), want: "denial document rejected"},
		{name: "wrong shape", raw: []byte(`{"hookSpecificOutput":{"permissionDecision":"allow","permissionDecisionReason":"[ECI_X] operation=other"}}`), want: "denial shape rejected"},
		{name: "missing identity", raw: []byte(`{"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"denied"}}`), want: "diagnostic identity rejected"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseDenial(test.raw)
			if err == nil || err.Error() != test.want {
				t.Fatalf("ParseDenial() error = %v, want %q", err, test.want)
			}
		})
	}
}

// TestParseDenialRejectsMalformedAndOversizeInput checks bounded reduction failures.
//
// Example: malformed JSON and an over-limit document are rejected before telemetry writes.
func TestParseDenialRejectsMalformedAndOversizeInput(t *testing.T) {
	for _, raw := range [][]byte{
		nil,
		[]byte("{}\n"),
		[]byte("not-json\n"),
		bytes.Repeat([]byte("x"), MaxDenialBytes+1),
	} {
		if _, err := ParseDenial(raw); err == nil {
			t.Fatalf("ParseDenial(%d bytes) unexpectedly succeeded", len(raw))
		}
	}
}

// TestTelemetryStoreAppendsDiagnosticEvent checks the exact event shape and state binding.
//
// Example: a valid denial produces one compact would-deny record with its reason and context.
func TestTelemetryStoreAppendsDiagnosticEvent(t *testing.T) {
	store := newTestTelemetryStore(t)
	denial, err := ParseDenial(sampleTelemetryDenial("private-token"))
	if err != nil {
		t.Fatalf("ParseDenial(): %v", err)
	}
	if err := store.AppendEvent(denial, EventContext{
		Provider:    ProviderOmp,
		Role:        RoleWorker,
		Marker:      MarkerActive,
		Source:      SourceParser,
		ConfigState: ConfigStateMissing,
		Command:     "git add -A",
		CWD:         "/tmp/repo",
		SessionID:   "sess-1",
	}); err != nil {
		t.Fatalf("AppendEvent(): %v", err)
	}

	path := filepath.Join(store.Root, TelemetryParentDirectoryName, TelemetryDirectoryName, TelemetryFileName)
	records, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read telemetry: %v", err)
	}
	var event map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(records), &event); err != nil {
		t.Fatalf("decode telemetry: %v", err)
	}
	wantKeys := map[string]bool{
		"schema": true, "event": true, "at_utc": true, "provider": true,
		"role": true, "marker": true, "source": true, "code": true,
		"operation": true, "config_state": true,
		"command": true, "cwd": true, "session_id": true, "reason": true,
	}
	if len(event) != len(wantKeys) {
		t.Fatalf("event keys = %#v, want exactly %#v", event, wantKeys)
	}
	for key := range event {
		if !wantKeys[key] {
			t.Fatalf("unexpected event key %q", key)
		}
	}
	if event["schema"] != EventSchema || event["event"] != "would-deny" {
		t.Fatalf("event identity = %#v", event)
	}
	if event["provider"] != string(ProviderOmp) || event["role"] != string(RoleWorker) || event["marker"] != string(MarkerActive) || event["source"] != string(SourceParser) {
		t.Fatalf("event context = %#v", event)
	}
	if event["code"] != string(TelemetryCodeOther) || event["operation"] != string(TelemetryOperationOther) {
		t.Fatalf("event reduction = %#v", event)
	}
	if event["command"] != "git add -A" || event["cwd"] != "/tmp/repo" || event["session_id"] != "sess-1" {
		t.Fatalf("event diagnostic context = %#v", event)
	}
	if reason, _ := event["reason"].(string); !strings.Contains(reason, "[ECI_SAMPLE_DENIED]") || !strings.Contains(reason, "token=***") || strings.Contains(reason, "private-token") {
		t.Fatalf("event reason = %#v, want masked denial reason", event["reason"])
	}

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat telemetry: %v", err)
	}
	if info.Mode().Perm() != 0o600 || !info.Mode().IsRegular() || info.Sys().(*syscall.Stat_t).Nlink != 1 {
		t.Fatalf("telemetry metadata = mode=%04o regular=%v nlink=%d", info.Mode().Perm(), info.Mode().IsRegular(), info.Sys().(*syscall.Stat_t).Nlink)
	}
}

// TestTelemetryStoreBoundsDiagnosticFields checks truncation and JSON safety of free-text fields.
//
// Example: a huge command full of escapable bytes is cut with a marker and the record stays bounded.
func TestTelemetryStoreBoundsDiagnosticFields(t *testing.T) {
	store := newTestTelemetryStore(t)
	hostile := strings.Repeat("\x01\"<\\é\u2028", 2_000)
	denial, err := ParseDenial(sampleTelemetryDenial("x " + strings.Repeat("r", 8_000)))
	if err != nil {
		t.Fatalf("ParseDenial(): %v", err)
	}
	if err := store.AppendEvent(denial, EventContext{
		Provider: ProviderOmp, Role: RoleWorker, Marker: MarkerActive, Source: SourceParser,
		ConfigState: ConfigStateMissing,
		Command:     "git add -A " + hostile,
		CWD:         "/tmp/" + hostile,
		SessionID:   hostile,
	}); err != nil {
		t.Fatalf("AppendEvent(): %v", err)
	}
	path := filepath.Join(store.Root, TelemetryParentDirectoryName, TelemetryDirectoryName, TelemetryFileName)
	records, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read telemetry: %v", err)
	}
	if len(records) > MaxEventBytes || bytes.Count(records, []byte("\n")) != 1 {
		t.Fatalf("record size=%d lines=%d, want one bounded line", len(records), bytes.Count(records, []byte("\n")))
	}
	var encoded map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimSpace(records), &encoded); err != nil {
		t.Fatalf("decode telemetry: %v", err)
	}
	limits := map[string]int{
		"command": MaxEventCommandBytes, "cwd": MaxEventCWDBytes,
		"session_id": MaxEventSessionIDBytes, "reason": MaxEventReasonBytes,
	}
	for key, limit := range limits {
		var value string
		if err := json.Unmarshal(encoded[key], &value); err != nil {
			t.Fatalf("decode %s: %v", key, err)
		}
		if body := len(encoded[key]) - 2; body > limit {
			t.Fatalf("%s encoded body = %d bytes, want <= %d", key, body, limit)
		}
		if !strings.HasSuffix(value, TruncationMarker) || !utf8.ValidString(value) {
			t.Fatalf("%s = %q, want valid UTF-8 ending in %q", key, value, TruncationMarker)
		}
	}
	if !bytes.Contains(encoded["command"], []byte(`git add -A \u0001\"<\\é`)) || !bytes.HasPrefix(encoded["cwd"], []byte(`"/tmp/`)) {
		t.Fatalf("truncated fields lost their prefix or HTML-escaped: command=%s", encoded["command"])
	}
}

// TestBoundDiagnosticTextLimits checks exact-limit retention, one-over truncation, and invalid UTF-8.
//
// Example: a value whose encoding is exactly the limit is kept verbatim.
func TestBoundDiagnosticTextLimits(t *testing.T) {
	exact := strings.Repeat("a<", 10) + "\n"
	limit := 22
	if got := boundDiagnosticText(exact, limit); got != exact {
		t.Fatalf("exact-limit text = %q, want unchanged", got)
	}
	over := exact + "b"
	got := boundDiagnosticText(over, limit)
	if !strings.HasSuffix(got, TruncationMarker) || jsonStringBytes(got) > limit {
		t.Fatalf("one-over text = %q (%d bytes), want truncated within %d", got, jsonStringBytes(got), limit)
	}
	for _, text := range []string{"a\xffb", "\xff\xfe\xfd", "ok\x00\u2028<>&\"\\\t"} {
		bounded := boundDiagnosticText(text, 64)
		if !utf8.ValidString(bounded) {
			t.Fatalf("bounded %q is invalid UTF-8", bounded)
		}
		var buffer bytes.Buffer
		encoder := json.NewEncoder(&buffer)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(bounded); err != nil {
			t.Fatal(err)
		}
		if want := buffer.Len() - 3; jsonStringBytes(bounded) != want {
			t.Fatalf("jsonStringBytes(%q) = %d, encoder emits %d", bounded, jsonStringBytes(bounded), want)
		}
	}
}

// TestMaskSecrets checks that likely credentials in command and reason text become ***.
//
// Example: `GITHUB_TOKEN=abc git push` is stored as `GITHUB_TOKEN=*** git push`.
func TestMaskSecrets(t *testing.T) {
	for _, test := range []struct{ in, want string }{
		{"git add -A", "git add -A"},
		{"GITHUB_TOKEN=abc git push", "GITHUB_TOKEN=*** git push"},
		{"export db_password='p w' && run", "export db_password=*** && run"},
		{`API_KEY="x y" AUTH_HEADER=z PATH=/bin cmd`, "API_KEY=*** AUTH_HEADER=*** PATH=/bin cmd"},
		{"aws_secret_access_key=s3 credential_file=c pass=p", "aws_secret_access_key=*** credential_file=*** pass=***"},
		{`curl -H "Authorization: Bearer abc.def" url`, `curl -H "Authorization: ***" url`},
		{"curl -H 'authorization: Basic Zm9v' url", "curl -H 'authorization: ***' url"},
		{"Bearer abc123 trailing", "Bearer *** trailing"},
		{"tool --token=abc --password hunter2 --api-key k --verbose", "tool --token=*** --password *** --api-key *** --verbose"},
		{"tool --client-secret=s --auth-token t", "tool --client-secret=*** --auth-token ***"},
		{"MONKEY=banana", "MONKEY=***"},
		{"(argv_index=2, byte_offset=2, token=add, path=/r)", "(argv_index=2, byte_offset=2, token=*** path=/r)"},
		{"TOKEN=abc token_x=y", "TOKEN=*** token_x=***"},
		{"API_KEY=a,SECRET25 x", "API_KEY=*** x"},
		{"token=SECRET21 ./deploy.sh", "token=*** ./deploy.sh"},
		{`curl "https://api.example/x?token=SECRET22&y=1"`, `curl "https://api.example/x?token=***&y=1"`},
		{"(byte_offset=0, token=GH_TOKEN=SECRET26, path=/r)", "(byte_offset=0, token=*** path=/r)"},
		{"(byte_offset=4, token=--password=SECRET27) x", "(byte_offset=4, token=*** x"},
		{`curl -H "Authorization: token ghp_SECRET1" url`, `curl -H "Authorization: ***" url`},
		{"curl -H 'X-Api-Key: SECRET2' url", "curl -H 'X-Api-Key: ***' url"},
		{`FOO_TOKEN="ab\"SECRET5" next`, "FOO_TOKEN=*** next"},
		{`FOO_TOKEN=ab"SECRET6" next`, "FOO_TOKEN=*** next"},
		{"tool --password -SECRET7 next", "tool --password *** next"},
		{"curl -u alice:SECRET8 url", "curl -u alice:*** url"},
		{"curl --user=alice:SECRET9 url", "curl --user=alice:*** url"},
	} {
		if got := maskSecrets(test.in); got != test.want {
			t.Errorf("maskSecrets(%q) = %q, want %q", test.in, got, test.want)
		}
	}
}

// TestTelemetryStoreMasksSecrets checks AppendEvent masks command and reason before writing.
//
// Example: a denied `TOKEN=x git push` never persists x.
func TestTelemetryStoreMasksSecrets(t *testing.T) {
	store := newTestTelemetryStore(t)
	denial, err := ParseDenial(sampleTelemetryDenial("--password=leak1"))
	if err != nil {
		t.Fatalf("ParseDenial(): %v", err)
	}
	if err := store.AppendEvent(denial, EventContext{ConfigState: ConfigStateMissing, Command: "GH_TOKEN=leak2 git push"}); err != nil {
		t.Fatalf("AppendEvent(): %v", err)
	}
	records, err := os.ReadFile(filepath.Join(store.Root, TelemetryParentDirectoryName, TelemetryDirectoryName, TelemetryFileName))
	if err != nil {
		t.Fatalf("read telemetry: %v", err)
	}
	if bytes.Contains(records, []byte("leak1")) || bytes.Contains(records, []byte("leak2")) || !bytes.Contains(records, []byte("GH_TOKEN=***")) {
		t.Fatalf("record not masked: %s", records)
	}
}

// TestTelemetryStoreRotatesOnlyFixedGenerations checks bounded rotation and preservation of unrelated names.
//
// Example: a full active generation rotates through `.1` to `.4` and never probes `.5`.
func TestTelemetryStoreRotatesOnlyFixedGenerations(t *testing.T) {
	store := newTestTelemetryStore(t)
	denial, err := ParseDenial(sampleTelemetryDenial("secret"))
	if err != nil {
		t.Fatalf("ParseDenial(): %v", err)
	}
	if err := store.AppendEvent(denial, EventContext{ConfigState: ConfigStateMissing}); err != nil {
		t.Fatalf("seed AppendEvent(): %v", err)
	}
	logPath := filepath.Join(store.Root, TelemetryParentDirectoryName, TelemetryDirectoryName, TelemetryFileName)
	seed, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}
	full := bytes.Repeat(seed, MaxLogBytes/len(seed)+1)
	if err := os.WriteFile(logPath, full, 0o600); err != nil {
		t.Fatalf("fill log: %v", err)
	}
	sentinel := filepath.Join(store.Root, TelemetryParentDirectoryName, TelemetryDirectoryName, TelemetryFileName+".5")
	if err := os.WriteFile(sentinel, []byte("untouched\n"), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	if err := store.AppendEvent(denial, EventContext{ConfigState: ConfigStateMissing}); err != nil {
		t.Fatalf("rotating AppendEvent(): %v", err)
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "untouched\n" {
		t.Fatalf("sentinel changed: %q err=%v", got, err)
	}
	rotated := filepath.Join(store.Root, TelemetryParentDirectoryName, TelemetryDirectoryName, TelemetryFileName+".1")
	for _, path := range []string{rotated, logPath} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if info.Mode().Perm() != 0o600 || !info.Mode().IsRegular() {
			t.Fatalf("%s mode = %04o regular=%v, want 0600 regular", path, info.Mode().Perm(), info.Mode().IsRegular())
		}
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "untouched\n" {
		t.Fatalf("fifth generation sentinel changed: %q err=%v", got, err)
	}
}

// TestTelemetryStoreContendedRotationDoesNotWait checks the nonblocking lock fallback.
//
// Example: a held rotation lock still appends one event without waiting for a lock release.
func TestTelemetryStoreContendedRotationDoesNotWait(t *testing.T) {
	store := newTestTelemetryStore(t)
	denial, err := ParseDenial(sampleTelemetryDenial("secret"))
	if err != nil {
		t.Fatalf("ParseDenial(): %v", err)
	}
	if err := store.AppendEvent(denial, EventContext{ConfigState: ConfigStateMissing}); err != nil {
		t.Fatalf("seed AppendEvent(): %v", err)
	}
	lockPath := filepath.Join(store.Root, TelemetryParentDirectoryName, TelemetryDirectoryName, RotationLockName)
	lockFD, err := syscall.Open(lockPath, syscall.O_RDWR|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open rotation lock: %v", err)
	}
	defer syscall.Close(lockFD)
	if err := syscall.Flock(lockFD, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("hold rotation lock: %v", err)
	}
	if err := store.AppendEvent(denial, EventContext{ConfigState: ConfigStateMissing}); err != nil {
		t.Fatalf("contended AppendEvent(): %v", err)
	}
}

// TestTelemetryStoreRejectsAliasesWithoutTouchingTargets checks fail-closed special-file handling.
//
// Example: a symlink at the active log name yields an error and leaves its target unchanged.
func TestTelemetryStoreRejectsAliasesWithoutTouchingTargets(t *testing.T) {
	store := newTestTelemetryStore(t)
	logDirectory := filepath.Join(store.Root, TelemetryParentDirectoryName, TelemetryDirectoryName)
	if err := os.MkdirAll(logDirectory, 0o700); err != nil {
		t.Fatalf("create telemetry directory: %v", err)
	}
	outside := filepath.Join(store.Root, "outside-log")
	if err := os.WriteFile(outside, []byte("unchanged\n"), 0o600); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(logDirectory, TelemetryFileName)); err != nil {
		t.Fatalf("create log symlink: %v", err)
	}
	denial, err := ParseDenial(sampleTelemetryDenial("secret"))
	if err != nil {
		t.Fatalf("ParseDenial(): %v", err)
	}
	if err := store.AppendEvent(denial, EventContext{ConfigState: ConfigStateMissing}); err == nil {
		t.Fatal("AppendEvent accepted a symlink log")
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != "unchanged\n" {
		t.Fatalf("outside target changed: %q err=%v", got, err)
	}
}

// TestTelemetryStoreRejectsStateRootAlias checks explicit state roots cannot traverse symlinks.
//
// Example: an explicit alias is rejected before descendant directories are created.
func TestTelemetryStoreRejectsStateRootAlias(t *testing.T) {
	base := newTestTelemetryRoot(t)
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("create target: %v", err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatalf("create alias: %v", err)
	}
	store := TelemetryStore{Root: alias}
	denial, err := ParseDenial(sampleTelemetryDenial("secret"))
	if err != nil {
		t.Fatalf("ParseDenial(): %v", err)
	}
	if err := store.AppendEvent(denial, EventContext{ConfigState: ConfigStateMissing}); err == nil {
		t.Fatal("AppendEvent accepted an explicit symlink root")
	}
	if _, err := os.Stat(filepath.Join(target, TelemetryParentDirectoryName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("aliased target received telemetry directory: %v", err)
	}
}

// sampleTelemetryDenial creates a bounded hook denial containing a caller-selected secret.
//
// Example: sampleTelemetryDenial("token") supplies a valid reduction input.
func sampleTelemetryDenial(secret string) []byte {
	return []byte(`{"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"[ECI_SAMPLE_DENIED] ECI gate denied (phase=PreToolUse, operation=sample-boundary, token=` + secret + `); reason: sample"}}`)
}

// newTestTelemetryStore creates a state root beneath the canonical user temporary directory.
//
// Example: telemetry tests never allocate runtime state under `/tmp`.
func newTestTelemetryStore(t *testing.T) TelemetryStore {
	return TelemetryStore{Root: newTestTelemetryRoot(t)}
}

// newTestTelemetryRoot creates an owner-only canonical state root for telemetry tests.
//
// Example: the returned root is removed automatically after the test.
func newTestTelemetryRoot(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve home: %v", err)
	}
	base, err := filepath.EvalSymlinks(filepath.Join(home, "tmp"))
	if err != nil {
		t.Fatalf("resolve home temporary directory: %v", err)
	}
	root, err := os.MkdirTemp(base, "eci-command-gate-state.")
	if err != nil {
		t.Fatalf("create telemetry root: %v", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("chmod telemetry root: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove telemetry root: %v", err)
		}
	})
	return root
}
