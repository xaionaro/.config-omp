package main

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// syncTarget identifies a supported report projection target.
//
// Example: A task target selects one package by ID.
type syncTarget int

const (
	// syncTargetUnknown denotes an unsupported target.
	//
	// Example: Reject an unrecognized target while retaining its log event.
	syncTargetUnknown syncTarget = iota
	// syncTargetTask selects a task record.
	//
	// Example: Update a task's acceptance evidence.
	syncTargetTask
	// syncTargetLane selects a lane record.
	//
	// Example: Update one lane's readiness.
	syncTargetLane
	// syncTargetReport selects top-level report fields.
	//
	// Example: Change the next focus.
	syncTargetReport
	// syncTargetProgress selects keyed progress entries.
	//
	// Example: Add a progress update.
	syncTargetProgress
	// syncTargetDecisions selects keyed decisions.
	//
	// Example: Record a design choice.
	syncTargetDecisions
	// syncTargetBlockers selects keyed risks.
	//
	// Example: Record a blocker.
	syncTargetBlockers
	// syncTargetVerification selects keyed verification entries.
	//
	// Example: Record test evidence.
	syncTargetVerification
	// syncTargetReview selects a review insertion.
	//
	// Example: Add an explicitly supplied queue review.
	syncTargetReview
	// syncTargetPriorityEstimate selects a priority estimate insertion.
	//
	// Example: Add an estimate with its new-priority task.
	syncTargetPriorityEstimate
	// syncTargetRootCompletion selects an actual root completion record.
	//
	// Example: Record a completed root without a lane.
	syncTargetRootCompletion
	// syncTargetRootForecast selects a root forecast record.
	//
	// Example: Add an explicitly referenced root forecast.
	syncTargetRootForecast
)

// String returns the canonical wire name for a target.
//
// Example: A task target renders as task.
func (t syncTarget) String() string {
	switch t {
	case syncTargetTask:
		return "task"
	case syncTargetLane:
		return "lane"
	case syncTargetReport:
		return "report"
	case syncTargetProgress:
		return "progress"
	case syncTargetDecisions:
		return "decisions"
	case syncTargetBlockers:
		return "blockers_risks"
	case syncTargetVerification:
		return "verification"
	case syncTargetReview:
		return "review"
	case syncTargetPriorityEstimate:
		return "priority_estimate"
	case syncTargetRootCompletion:
		return "root_completion"
	case syncTargetRootForecast:
		return "root_forecast"
	default:
		return "unknown"
	}
}

// parseSyncTarget translates a wire name into a supported target.
//
// Example: An unrecognized name remains unknown for diagnostic handling.
func parseSyncTarget(value string) syncTarget {
	switch value {
	case "task":
		return syncTargetTask
	case "lane":
		return syncTargetLane
	case "report":
		return syncTargetReport
	case "progress":
		return syncTargetProgress
	case "decisions":
		return syncTargetDecisions
	case "blockers_risks":
		return syncTargetBlockers
	case "verification":
		return syncTargetVerification
	case "review":
		return syncTargetReview
	case "priority_estimate":
		return syncTargetPriorityEstimate
	case "root_completion":
		return syncTargetRootCompletion
	case "root_forecast":
		return syncTargetRootForecast
	default:
		return syncTargetUnknown
	}
}

// UnmarshalJSON accepts a target name from the optional event payload.
//
// Example: An unsupported string decodes to the unknown target.
func (t *syncTarget) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*t = parseSyncTarget(value)
	return nil
}

// MarshalJSON writes a target name in replay metadata.
//
// Example: A pending lane update retains its wire target.
func (t syncTarget) MarshalJSON() ([]byte, error) { return json.Marshal(t.String()) }

// UnmarshalYAML reads the target stored in replay metadata.
//
// Example: A pending task remains a task after a retry.
func (t *syncTarget) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return fmt.Errorf("status sync target must be a string")
	}
	*t = parseSyncTarget(node.Value)
	return nil
}

// syncEnvironment identifies a lane acceptance boundary in a sync payload.
//
// Example: Production readiness is distinct from implementation readiness.
type syncEnvironment int

const (
	// syncEnvironmentUnknown denotes an absent or unsupported environment.
	//
	// Example: Reject a lane update without a valid selector.
	syncEnvironmentUnknown syncEnvironment = iota
	// syncEnvironmentImplementation selects implementation readiness.
	//
	// Example: Close an implementation lane.
	syncEnvironmentImplementation
	// syncEnvironmentTest selects test readiness.
	//
	// Example: Record a blocked test lane.
	syncEnvironmentTest
	// syncEnvironmentProd selects production readiness.
	//
	// Example: Record deployment acceptance.
	syncEnvironmentProd
)

// String returns the wire name of an acceptance boundary.
//
// Example: The production boundary renders as prod.
func (e syncEnvironment) String() string {
	switch e {
	case syncEnvironmentImplementation:
		return "implementation"
	case syncEnvironmentTest:
		return "test"
	case syncEnvironmentProd:
		return "prod"
	default:
		return ""
	}
}

// parseSyncEnvironment converts a wire selector into a known boundary.
//
// Example: An unknown environment remains unsupported.
func parseSyncEnvironment(value string) syncEnvironment {
	switch value {
	case "implementation":
		return syncEnvironmentImplementation
	case "test":
		return syncEnvironmentTest
	case "prod":
		return syncEnvironmentProd
	default:
		return syncEnvironmentUnknown
	}
}

// UnmarshalJSON reads an environment selector from an event.
//
// Example: An unknown selector is rejected by update validation.
func (e *syncEnvironment) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*e = parseSyncEnvironment(value)
	return nil
}

// MarshalJSON preserves the wire environment in pending metadata.
//
// Example: A pending test update retains its boundary.
func (e syncEnvironment) MarshalJSON() ([]byte, error) { return json.Marshal(e.String()) }

// UnmarshalYAML restores an environment selector from report metadata.
//
// Example: A pending production update stays scoped to production.
func (e *syncEnvironment) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return fmt.Errorf("status sync environment must be a string")
	}
	*e = parseSyncEnvironment(node.Value)
	return nil
}
