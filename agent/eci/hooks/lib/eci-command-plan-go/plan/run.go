package plan

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

const (
	maxRequestBytes = 64 * 1024
	// StatusAllow is the process status for an admitted command plan.
	StatusAllow = 0
	// StatusDeny is the process status for a denied command plan.
	StatusDeny = 2
	// StatusDefer is the process status for a provider-routed capability.
	StatusDefer = 3
	// StatusInternal is the process status for malformed input or I/O failure.
	StatusInternal = 64
)

// Run classifies one bounded JSON request and writes one JSON response.
func Run(input io.Reader, output io.Writer) int {
	requestBytes, err := io.ReadAll(io.LimitReader(input, maxRequestBytes+1))
	if err != nil || len(requestBytes) > maxRequestBytes {
		return writeInternalResult(output, "read-request")
	}

	// Decode the stable routing fields and ignore additive callback metadata.
	// A newer hook can include observability or scheduling data without making
	// an ordinary command depend on a synchronized planner protocol.
	decoder := json.NewDecoder(bytes.NewReader(requestBytes))
	var request Request
	if err := decoder.Decode(&request); err != nil {
		return writeInternalResult(output, "decode-request")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return writeInternalResult(output, "trailing-request")
	}
	if !validRequest(request) {
		return writeInternalResult(output, "validate-request")
	}

	result := Classify(request)
	if err := json.NewEncoder(output).Encode(result); err != nil {
		return StatusInternal
	}
	switch result.Decision {
	case DecisionAllow:
		return StatusAllow
	case DecisionDeny:
		return StatusDeny
	case DecisionDefer:
		return StatusDefer
	default:
		return StatusInternal
	}
}

func validRequest(request Request) bool {
	validProvider := request.Provider == ProviderOmp || request.Provider == ProviderKimi
	validRole := request.Role == RoleCoordinator || request.Role == RoleWorker
	validMarker := request.Marker == MarkerActive || request.Marker == MarkerInactive
	if !validProvider || !validRole || !validMarker || request.CWD == "" {
		return false
	}
	if request.Marker == MarkerActive && request.ActiveSession == "" {
		return false
	}
	return true
}

func writeInternalResult(output io.Writer, predicate string) int {
	result := Result{
		Decision: DecisionError,
		Diagnostic: &Diagnostic{
			Code:        CodePlanInternalDenied,
			Operation:   "plan-segment",
			Segment:     0,
			ArgvIndex:   0,
			ByteOffset:  0,
			Token:       "<request>",
			Path:        "n/a",
			Predicate:   predicate,
			Reason:      "command-plan request could not be classified",
			Remediation: "submit one bounded JSON request with provider, role, cwd, marker, session, command, and marker paths",
		},
	}
	if err := json.NewEncoder(output).Encode(result); err != nil {
		return StatusInternal
	}
	return StatusInternal
}
