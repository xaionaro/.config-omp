package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestBashGateDeniesUnknownProofRoot(t *testing.T) {
	t.Setenv("HOME", "relative")
	t.Setenv("ECI_PROOF_ROOT", "")
	var stdout, stderr bytes.Buffer
	code := run([]string{"bash-gate"}, strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"true"}}`), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	var decision hookDecision
	if err := json.Unmarshal(stdout.Bytes(), &decision); err != nil {
		t.Fatalf("decode %q: %v", stdout.String(), err)
	}
	if decision.HookSpecificOutput.PermissionDecision != "deny" ||
		!strings.HasPrefix(decision.HookSpecificOutput.PermissionDecisionReason, reasonNativeGateUnavailable+"hook validate-bash denied") {
		t.Fatalf("decision %+v", decision)
	}
}

// TestBashGateMalformedCallbackBlocks reports the planner failure to the caller.
//
// Example: malformed input exits two with a diagnostic and no decision JSON.
func TestBashGateMalformedCallbackBlocks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := run([]string{"bash-gate"}, strings.NewReader("{"), &stdout, &stderr)
	if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "decode callback failed") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, bytes.ErrTooLarge }

func TestDenyUndeliverableExitsTwo(t *testing.T) {
	var stderr bytes.Buffer
	if code := deny(failingWriter{}, &stderr, "validate-bash", "x"); code != exitUndeliverable {
		t.Fatalf("exit %d, want %d", code, exitUndeliverable)
	}
}

func TestUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"nope"}, strings.NewReader(""), &stdout, &stderr); code != exitUsage {
		t.Fatalf("exit %d", code)
	}
}
