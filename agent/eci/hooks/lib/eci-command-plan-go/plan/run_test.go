package plan

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestRunIgnoresAdditiveRequestFields verifies that optional callback metadata
// does not turn an otherwise ordinary command into an internal planner error.
//
// Example: a newer hook can add tracing metadata while an older planner still
// classifies `printf '%s\\n' ordinary` normally.
func TestRunIgnoresAdditiveRequestFields(t *testing.T) {
	t.Parallel()

	input := strings.NewReader(`{
		"provider":"omp",
		"role":"coordinator",
		"cwd":"/workspace",
		"marker":"active",
		"active_session":"session",
		"command":"printf '%s\\n' ordinary",
		"active_markers":[],
		"approved_roots":[],
		"future_callback_metadata":{"trace_id":"future-hook"}
	}`)
	var output bytes.Buffer

	status := Run(input, &output)
	if status != StatusAllow {
		t.Fatalf("status: got %d, want %d; output=%s", status, StatusAllow, output.String())
	}

	var result Result
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode planner result: %v; output=%s", err, output.String())
	}
	if result.Decision != DecisionAllow || result.Diagnostic != nil {
		t.Fatalf("result: got decision=%q diagnostic=%#v, want ordinary allow", result.Decision, result.Diagnostic)
	}
}
