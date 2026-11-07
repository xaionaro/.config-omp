package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// The unknown state represents absent or explicitly unknown task status.
//
// Example: The zero value represents an unknown or absent state.
const (
	// statusUnknown marks an unscored status.
	//
	// Example: Use it when the report marks an unscored status.
	statusUnknown workStatus = iota
	// statusNew marks work that has not started.
	//
	// Example: Use it when the report marks work that has not started.
	statusNew
	// statusInProgress marks work underway.
	//
	// Example: Use it when the report marks work underway.
	statusInProgress
	// statusPaused marks work awaiting another lane.
	//
	// Example: Use it when the report marks work awaiting another lane.
	statusPaused
	// statusBlocked marks work awaiting user input.
	//
	// Example: Use it when the report marks work awaiting user input.
	statusBlocked
	// statusClosed marks accepted work.
	//
	// Example: Use it when the report marks accepted work.
	statusClosed
)

// Timeline states distinguish unavailable, empty, and present evidence.
//
// Example: The zero value represents an unknown or absent state.
const (
	// timelineUnknown marks unknown history coverage.
	//
	// Example: Use it when the report marks unknown history coverage.
	timelineUnknown timelineState = iota
	// timelineUnavailable marks unavailable or incomplete history.
	//
	// Example: Use it when the report marks unavailable or incomplete history.
	timelineUnavailable
	// timelineVerifiedEmpty marks verified absence of evidenced activity.
	//
	// Example: Use it when the report marks verified absence of evidenced activity.
	timelineVerifiedEmpty
	// timelinePresent marks supplied timeline rows.
	//
	// Example: Use it when the report marks supplied timeline rows.
	timelinePresent
)

// Estimate kinds identify how a duration range should be interpreted.
//
// Example: The zero value represents an unknown or absent state.
const (
	// effortUnknown marks an unspecified effort kind.
	//
	// Example: Use it when the report marks an unspecified effort kind.
	effortUnknown effortKind = iota
	// effortElapsed marks an elapsed duration.
	//
	// Example: Use it when the report marks an elapsed duration.
	effortElapsed
	// effortWork marks work effort.
	//
	// Example: Use it when the report marks work effort.
	effortWork
)

// Root-task statuses describe the remaining post-Fast sequence.
//
// Example: The zero value represents an unknown or absent state.
const (
	// rootTaskNone omits post-Fast follow-up status.
	//
	// Example: Use it when the report omits post-Fast follow-up status.
	rootTaskNone rootTaskStatus = iota
	// rootTaskFollowup marks pending post-Fast work.
	//
	// Example: Use it when the report marks pending post-Fast work.
	rootTaskFollowup
	// rootTaskFollowupPaused marks post-Fast work awaiting a dependency.
	//
	// Example: Use it when the report marks post-Fast work awaiting a dependency.
	rootTaskFollowupPaused
)

// Required environments define the acceptance boundary for a lane.
//
// Example: Source-only work uses the implementation environment.
const (
	// environmentUnknown indicates a missing acceptance boundary.
	//
	// Example: Validation rejects a lane without an explicit environment.
	environmentUnknown requiredEnvironment = iota
	// environmentImplementation selects source-level acceptance.
	//
	// Example: A source-only lane closes when implementation is CLOSED.
	environmentImplementation
	// environmentTest selects test-environment acceptance.
	//
	// Example: An integration lane closes when test readiness is CLOSED.
	environmentTest
	// environmentProd selects production acceptance.
	//
	// Example: A deployed lane closes when production readiness is CLOSED.
	environmentProd
)

// workStatus is the closed set of task and readiness states.
//
// Example: A NEW task can become CLOSED after acceptance.
type workStatus int

// String returns the human-readable wire value.
//
// Example: A known value renders its documented YAML label.
func (s workStatus) String() string {
	switch s {
	case statusNew:
		return "NEW"
	case statusInProgress:
		return "IN PROGRESS"
	case statusPaused:
		return "PAUSED"
	case statusBlocked:
		return "BLOCKED"
	case statusClosed:
		return "CLOSED"
	default:
		return "unknown"
	}
}

// UnmarshalYAML decodes only documented status values.
//
// Example: Decode a documented label and reject other values.
func (s *workStatus) UnmarshalYAML(n *yaml.Node) error {
	if err := checkEnumScalar(n); err != nil {
		return err
	}
	switch n.Value {
	case "", "unknown", "null":
		*s = statusUnknown
	case "NEW":
		*s = statusNew
	case "IN PROGRESS":
		*s = statusInProgress
	case "PAUSED":
		*s = statusPaused
	case "BLOCKED":
		*s = statusBlocked
	case "CLOSED":
		*s = statusClosed
	default:
		return fmt.Errorf("invalid status %q", n.Value)
	}
	return nil
}

// validForecast requires either explicit unknown or a complete supplied forecast.
//
// Example: Reject a forecast missing its outcome or target.
func validForecast(f forecast) bool {
	return strings.TrimSpace(f.Outcome) != "" && strings.TrimSpace(f.Target) != "" && strings.TrimSpace(f.Assessment) != ""
}

// timelineState describes the coverage of supplied timeline evidence.
//
// Example: Distinguish verified empty history from unavailable history.
type timelineState int

// String returns the documented timeline coverage label.
//
// Example: A verified empty timeline renders as verified_empty.
func (s timelineState) String() string {
	switch s {
	case timelineUnavailable:
		return "unavailable"
	case timelineVerifiedEmpty:
		return "verified_empty"
	case timelinePresent:
		return "present"
	default:
		return "unknown"
	}
}

// MarshalJSON encodes the timeline coverage label.
//
// Example: A present timeline retains its wire string.
func (s timelineState) MarshalJSON() ([]byte, error) {
	if s < timelineUnknown || s > timelinePresent {
		return nil, fmt.Errorf("invalid timeline state %d", s)
	}
	return json.Marshal(s.String())
}

// UnmarshalJSON decodes a documented timeline coverage label.
//
// Example: A verified_empty snapshot becomes timelineVerifiedEmpty.
func (s *timelineState) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	return s.UnmarshalYAML(&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

// UnmarshalYAML decodes the supported timeline coverage states.
//
// Example: Decode a documented label and reject other values.
func (s *timelineState) UnmarshalYAML(n *yaml.Node) error {
	if err := checkEnumScalar(n); err != nil {
		return err
	}
	switch n.Value {
	case "", "unknown", "null":
		*s = timelineUnknown
	case "unavailable":
		*s = timelineUnavailable
	case "verified_empty":
		*s = timelineVerifiedEmpty
	case "present":
		*s = timelinePresent
	default:
		return fmt.Errorf("invalid timeline_state %q", n.Value)
	}
	return nil
}

// effortKind distinguishes elapsed time from work effort.
//
// Example: Distinguish elapsed duration from work effort.
type effortKind int

// String returns the documented effort kind.
//
// Example: A known value renders its documented YAML label.
func (k effortKind) String() string {
	switch k {
	case effortElapsed:
		return "elapsed"
	case effortWork:
		return "work"
	default:
		return "unknown"
	}
}

// UnmarshalYAML decodes supported effort kinds.
//
// Example: Decode a documented label and reject other values.
func (k *effortKind) UnmarshalYAML(n *yaml.Node) error {
	if err := checkEnumScalar(n); err != nil {
		return err
	}
	switch n.Value {
	case "elapsed":
		*k = effortElapsed
	case "work":
		*k = effortWork
	default:
		return fmt.Errorf("invalid effort kind %q", n.Value)
	}
	return nil
}

// rootTaskStatus represents the post-Fast follow-up state.
//
// Example: Record FOLLOWUP while post-Fast work remains.
type rootTaskStatus int

// String returns the status label or an empty string when absent.
//
// Example: A known value renders its documented YAML label.
func (s rootTaskStatus) String() string {
	switch s {
	case rootTaskFollowup:
		return "FOLLOWUP"
	case rootTaskFollowupPaused:
		return "FOLLOWUP_PAUSED"
	default:
		return ""
	}
}

// UnmarshalYAML decodes the supported post-Fast statuses.
//
// Example: Decode a documented label and reject other values.
func (s *rootTaskStatus) UnmarshalYAML(n *yaml.Node) error {
	if err := checkEnumScalar(n); err != nil {
		return err
	}
	switch n.Value {
	case "", "null":
		*s = rootTaskNone
	case "FOLLOWUP":
		*s = rootTaskFollowup
	case "FOLLOWUP_PAUSED":
		*s = rootTaskFollowupPaused
	default:
		return fmt.Errorf("invalid root_task_status %q", n.Value)
	}
	return nil
}

// requiredEnvironment selects the readiness column that determines lane closure.
//
// Example: A test-gated lane remains active while its test status is open.
type requiredEnvironment int

// UnmarshalYAML decodes an explicitly selected lane acceptance boundary.
//
// Example: The value test selects the test status column.
func (e *requiredEnvironment) UnmarshalYAML(n *yaml.Node) error {
	if err := checkEnumScalar(n); err != nil {
		return err
	}
	switch n.Value {
	case "implementation":
		*e = environmentImplementation
	case "test":
		*e = environmentTest
	case "prod":
		*e = environmentProd
	default:
		return fmt.Errorf("invalid required_environment %q", n.Value)
	}
	return nil
}

// String returns the documented environment name.
//
// Example: A production lane displays prod.
func (e requiredEnvironment) String() string {
	switch e {
	case environmentImplementation:
		return "implementation"
	case environmentTest:
		return "test"
	case environmentProd:
		return "prod"
	default:
		return "unknown"
	}
}

// requiredStatus returns the lane readiness selected by its acceptance boundary.
//
// Example: A test-gated lane returns its test status.
func (l lane) requiredStatus() workStatus {
	switch l.RequiredEnvironment {
	case environmentImplementation:
		return l.ImplementationStatus
	case environmentTest:
		return l.TestStatus
	case environmentProd:
		return l.ProdStatus
	default:
		return statusUnknown
	}
}

// checkEnumScalar rejects mappings, sequences, and non-string YAML tags.
//
// Example: A mapping cannot stand in for an unknown status.
func checkEnumScalar(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return fmt.Errorf("enum requires a string scalar (kind %d, tag %q)", node.Kind, node.Tag)
	}
	return nil
}

// taskLifecycle identifies an optional task cancellation marker.
//
// Example: A cancelled task carries the cancellation marker.
type taskLifecycle int

// Task lifecycle markers distinguish active and cancelled tasks.
//
// Example: taskLifecycleCancelled marks a cancelled task.
const (
	// taskLifecycleNone indicates no lifecycle marker.
	//
	// Example: An active task has no marker.
	taskLifecycleNone taskLifecycle = iota
	// taskLifecycleCancelled indicates explicit cancellation.
	//
	// Example: A cancelled task is excluded from remaining work.
	taskLifecycleCancelled
)

// UnmarshalYAML decodes supported task lifecycle markers.
//
// Example: Decode the cancelled marker.
func (l *taskLifecycle) UnmarshalYAML(n *yaml.Node) error {
	if err := checkEnumScalar(n); err != nil {
		return err
	}
	switch n.Value {
	case "":
		*l = taskLifecycleNone
	case "cancelled":
		*l = taskLifecycleCancelled
	default:
		return fmt.Errorf("invalid task lifecycle %q", n.Value)
	}
	return nil
}
