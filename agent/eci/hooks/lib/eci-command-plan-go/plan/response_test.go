package main

import "testing"

func TestNormalizePlannerResponse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		status      int
		output      string
		decision    DecisionKind
		transparent bool
	}{
		{name: "allow", status: StatusAllow, output: `{"decision":"allow"}`, decision: DecisionAllow},
		{name: "defer", status: StatusDefer, output: `{"decision":"defer"}`, decision: DecisionDefer},
		{name: "deny", status: StatusDeny, output: `{"decision":"deny","diagnostic":{"code":"ECI_CONTROL_OWNER_REQUIRED"}}`, decision: DecisionDeny},
		{name: "empty", status: StatusInternal, output: "", decision: DecisionDefer, transparent: true},
		{name: "malformed", status: StatusInternal, output: "not-json", decision: DecisionDefer, transparent: true},
		{name: "unexpected decision", status: StatusInternal, output: `{"decision":"error"}`, decision: DecisionDefer, transparent: true},
		{name: "status mismatch", status: StatusAllow, output: `{"decision":"deny","diagnostic":{"code":"ECI_CONTROL_OWNER_REQUIRED"}}`, decision: DecisionDefer, transparent: true},
		{name: "deny without diagnostic", status: StatusDeny, output: `{"decision":"deny"}`, decision: DecisionDefer, transparent: true},
		{name: "allow with diagnostic", status: StatusAllow, output: `{"decision":"allow","diagnostic":{"code":"ECI_CONTROL_OWNER_REQUIRED"}}`, decision: DecisionDefer, transparent: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizePlannerResponse(tt.status, []byte(tt.output))
			if got.Result.Decision != tt.decision {
				t.Fatalf("decision: got %q, want %q", got.Result.Decision, tt.decision)
			}
			if got.TransparentFallback != tt.transparent {
				t.Fatalf("transparent fallback: got %t, want %t", got.TransparentFallback, tt.transparent)
			}
		})
	}
}
