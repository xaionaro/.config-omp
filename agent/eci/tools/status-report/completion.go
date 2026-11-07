package main

import (
	"fmt"
	"strings"
	"time"
)

// completionEstimate records a projected elapsed completion range and basis.
//
// Example: Record the overall elapsed range and critical path.
type completionEstimate struct {
	Priority     priorityLabel      `yaml:"priority"`
	MinMinutes   *fractionalMinutes `yaml:"min_minutes"`
	MaxMinutes   *fractionalMinutes `yaml:"max_minutes"`
	AsOf         string             `yaml:"as_of"`
	Basis        string             `yaml:"basis"`
	Assumptions  string             `yaml:"assumptions"`
	Dependencies string             `yaml:"dependencies"`
	CriticalPath string             `yaml:"critical_path"`
}

// renderCompletionEstimates reports supplied projections without adding parallel durations.
//
// Example: Display each priority and overall completion range.
func renderCompletionEstimates(
	b *strings.Builder,
	r report,
) {
	estimates := map[priorityLabel]completionEstimate{}
	for _, e := range r.PriorityEstimates {
		estimates[e.Priority] = e
	}
	seen := map[priorityLabel]bool{}
	fmt.Fprintln(b, "\nCompletion estimates:")
	if len(r.Incomplete) > 0 {
		fmt.Fprintln(b, "Estimates unverified; see source reconciliation issues above.")
	}
	for _, t := range r.Tasks {
		if seen[t.Priority] {
			continue
		}
		seen[t.Priority] = true
		e, ok := estimates[t.Priority]
		if !ok {
			fmt.Fprintf(b, "Priority %s completion estimate: unknown\n", t.Priority)
			continue
		}
		writeCompletionEstimate(b, "Priority "+string(t.Priority)+" completion estimate", e)
	}
	if r.CompletionEstimate == nil {
		fmt.Fprintln(b, "Overall completion estimate: unknown")
	} else {
		writeCompletionEstimate(b, "Overall completion estimate", *r.CompletionEstimate)
	}
}

// writeCompletionEstimate renders one supplied range and its limitations.
//
// Example: Show a range together with its assumptions.
func writeCompletionEstimate(
	b *strings.Builder,
	label string,
	e completionEstimate,
) {
	earliest, latest, err := completionEndpoints(e)
	if err != nil {
		fmt.Fprintf(b, "%s: unknown (invalid estimate: %v)\n", label, err)
		return
	}
	fmt.Fprintf(b, "%s: %g–%g minutes elapsed as of %s; endpoint %s–%s; basis: %s; assumptions: %s; dependencies: %s; critical path: %s\n", label, *e.MinMinutes, *e.MaxMinutes, e.AsOf, earliest.Format(time.RFC3339Nano), latest.Format(time.RFC3339Nano), e.Basis, e.Assumptions, e.Dependencies, e.CriticalPath)
}

// fractionalMinutes records a potentially fractional estimated duration.
//
// Example: Half a minute represents thirty seconds.
type fractionalMinutes float64
