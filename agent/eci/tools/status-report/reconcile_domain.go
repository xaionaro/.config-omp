package main

import (
	"fmt"
	"gopkg.in/yaml.v3"
)

// workState describes the lifecycle of canonical work.
//
// Example: Active work still has a next action.
type workState int

// Canonical work lifecycle values.
//
// Example: Completed work has reached its outcome.
const (
	// workStateUnknown identifies this canonical category.
	//
	// Example: Use workStateUnknown when its documented category applies.
	workStateUnknown workState = iota
	// workStateActive identifies this canonical category.
	//
	// Example: Use workStateActive when its documented category applies.
	workStateActive
	// workStateBlocked marks work awaiting user input.
	//
	// Example: A pending user decision blocks the work.
	workStateBlocked
	// workStateCompleted identifies this canonical category.
	//
	// Example: Use workStateCompleted when its documented category applies.
	workStateCompleted
	// workStateCancelled marks work withdrawn without completion.
	//
	// Example: A cancelled outcome has no active deadline.
	workStateCancelled
)

// UnmarshalYAML decodes work lifecycle labels, retaining unknown values as unknown.
//
// Example: Decode active as workStateActive.
func (s *workState) UnmarshalYAML(n *yaml.Node) error {
	if err := checkEnumScalar(n); err != nil {
		return err
	}
	switch n.Value {
	case "active":
		*s = workStateActive
	case "blocked":
		*s = workStateBlocked
	case "completed":
		*s = workStateCompleted
	case "cancelled":
		*s = workStateCancelled
	default:
		*s = workStateUnknown
	}
	return nil
}

// reportingRole identifies an explicit reporting responsibility.
//
// Example: One work record can have both lane and root roles.
type reportingRole int

// Supported reporting responsibilities.
//
// Example: A lane role declares an independently advancing workstream.
const (
	// reportingRoleUnknown identifies this canonical category.
	//
	// Example: Use reportingRoleUnknown when its documented category applies.
	reportingRoleUnknown reportingRole = iota
	// reportingRoleLane identifies this canonical category.
	//
	// Example: Use reportingRoleLane when its documented category applies.
	reportingRoleLane
	// reportingRoleRoot identifies a full root outcome.
	//
	// Example: Root-only work may need its own forecast.
	reportingRoleRoot
)

// UnmarshalYAML decodes declared reporting roles without guessing unknown labels.
//
// Example: Decode lane as reportingRoleLane.
func (r *reportingRole) UnmarshalYAML(n *yaml.Node) error {
	if err := checkEnumScalar(n); err != nil {
		return err
	}
	switch n.Value {
	case "lane":
		*r = reportingRoleLane
	case "root":
		*r = reportingRoleRoot
	default:
		*r = reportingRoleUnknown
	}
	return nil
}

// forecastKind distinguishes lane and root forecast ownership.
//
// Example: A root forecast covers an outcome not represented by a lane.
type forecastKind int

// Supported forecast ownership categories.
//
// Example: forecastKindLane identifies a lane forecast.
const (
	// forecastKindUnknown identifies this canonical category.
	//
	// Example: Use forecastKindUnknown when its documented category applies.
	forecastKindUnknown forecastKind = iota
	// forecastKindLane identifies this canonical category.
	//
	// Example: Use forecastKindLane when its documented category applies.
	forecastKindLane
	// forecastKindRoot identifies a root outcome forecast.
	//
	// Example: A root forecast covers an unrepresented outcome.
	forecastKindRoot
)

// String returns the canonical wire label for a forecast kind.
//
// Example: forecastKindLane renders as lane.
func (k forecastKind) String() string {
	switch k {
	case forecastKindLane:
		return "lane"
	case forecastKindRoot:
		return "root"
	default:
		return "unknown"
	}
}

// forecastGroup identifies a report collection containing forecasts.
//
// Example: Lane forecasts are nested in the lane collection.
type forecastGroup int

// Supported report forecast collections.
//
// Example: forecastGroupRoots selects root_forecasts.
const (
	// forecastGroupUnknown identifies this canonical category.
	//
	// Example: Use forecastGroupUnknown when its documented category applies.
	forecastGroupUnknown forecastGroup = iota
	// forecastGroupLanes identifies this canonical category.
	//
	// Example: Use forecastGroupLanes when its documented category applies.
	forecastGroupLanes
	// forecastGroupRoots identifies the report root forecast collection.
	//
	// Example: Select root_forecasts during refresh.
	forecastGroupRoots
)

// String returns the YAML key for a forecast collection.
//
// Example: forecastGroupLanes renders as lanes.
func (g forecastGroup) String() string {
	switch g {
	case forecastGroupLanes:
		return "lanes"
	case forecastGroupRoots:
		return "root_forecasts"
	default:
		return "unknown"
	}
}

// laneLifecycle identifies an optional terminal lane lifecycle marker.
//
// Example: A cancelled lane does not claim completion.
type laneLifecycle int

// Supported optional lane lifecycle values.
//
// Example: laneLifecycleCancelled marks a cancelled lane.
const (
	// laneLifecycleNone indicates no lifecycle marker.
	//
	// Example: Active lanes omit the optional marker.
	laneLifecycleNone laneLifecycle = iota
	// laneLifecycleCancelled indicates cancelled work.
	//
	// Example: A cancelled lane has no active deadline.
	laneLifecycleCancelled
)

// UnmarshalYAML decodes only the supported lifecycle string labels.
//
// Example: Decode cancelled as laneLifecycleCancelled.
func (l *laneLifecycle) UnmarshalYAML(n *yaml.Node) error {
	if err := checkEnumScalar(n); err != nil {
		return err
	}
	switch n.Value {
	case "":
		*l = laneLifecycleNone
	case "cancelled":
		*l = laneLifecycleCancelled
	default:
		return fmt.Errorf("invalid lane lifecycle %q", n.Value)
	}
	return nil
}
