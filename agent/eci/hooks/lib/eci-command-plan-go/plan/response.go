package main

import (
	"bytes"
	"encoding/json"
)

// NormalizedPlannerResponse is the small seam between the compiled planner
// and the callback adapter. TransparentFallback means that the planner did
// not produce a usable classification; the caller must continue with its
// ordinary effect-aware routes rather than turn planner trouble into a new
// command denial.
type NormalizedPlannerResponse struct {
	Result              Result
	TransparentFallback bool
}

// NormalizePlannerResponse validates the planner's process result and JSON
// envelope. Only a matching allow/deny/defer pair is authoritative. Empty,
// malformed, unexpected, or error responses become a transparent defer so
// the caller can apply its existing concrete-effect checks.
func NormalizePlannerResponse(exitStatus int, output []byte) NormalizedPlannerResponse {
	var result Result
	if len(bytes.TrimSpace(output)) == 0 || json.Unmarshal(output, &result) != nil {
		return transparentPlannerResponse()
	}

	valid := true
	switch result.Decision {
	case DecisionAllow:
		valid = valid && exitStatus == StatusAllow && result.Diagnostic == nil
	case DecisionDeny:
		valid = valid && exitStatus == StatusDeny && result.Diagnostic != nil
	case DecisionDefer:
		valid = valid && exitStatus == StatusDefer && result.Diagnostic == nil
	default:
		valid = false
	}
	if !valid {
		return transparentPlannerResponse()
	}
	return NormalizedPlannerResponse{Result: result}
}

func transparentPlannerResponse() NormalizedPlannerResponse {
	return NormalizedPlannerResponse{
		Result:              Result{Decision: DecisionDefer},
		TransparentFallback: true,
	}
}
