package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReprioritizeIntoExistingEstimateReplaysAtomically checks an existing destination.
//
// Example: The last high-priority task moves into low while its unchanged estimate survives.
func TestReprioritizeIntoExistingEstimateReplaysAtomically(t *testing.T) {
	// estimate identifies the unchanged destination priority estimate.
	//
	// Example: Reuse the existing low-priority record.
	const estimate = `{"priority":"low","min_minutes":20,"max_minutes":40,"as_of":"2026-11-02T15:24:00Z","basis":"guess","assumptions":"none","dependencies":"none","critical_path":"latest"}`
	// prior identifies the exact source estimate authorized for retirement.
	//
	// Example: Remove high only when its supplied prior still matches.
	const prior = `{"priority":"high","min_minutes":20,"max_minutes":40,"as_of":"2026-11-02T15:24:00Z","basis":"guess","assumptions":"none","dependencies":"none","critical_path":"latest"}`
	report := strings.Replace(linkedReport, "title: Second, priority: high", "title: Second, priority: low", 1)
	report = strings.Replace(report, "completion_estimate:", "  - {priority: low, min_minutes: 20, max_minutes: 40, as_of: '2026-11-02T15:24:00Z', basis: guess, assumptions: none, dependencies: none, critical_path: latest} # owner low estimate\ncompletion_estimate:", 1)
	event := `{"event":"decision","details":{"status_update":{"updates":[{"target":"task","id":"1","values":{"priority":"low"}}],"additions":[{"target":"priority_estimate","retire_priority":"high","expected_prior":` + prior + `,"record":` + estimate + `}]}}}` + "\n"
	for _, tc := range []struct {
		name     string
		report   string
		succeeds bool
	}{
		{"matching destination", report, true},
		{"unquoted timestamp", strings.Replace(report, "as_of: '2026-11-02T15:24:00Z', basis: guess, assumptions: none, dependencies: none, critical_path: latest} # owner low", "as_of: 2026-11-02T15:24:00Z, basis: guess, assumptions: none, dependencies: none, critical_path: latest} # owner low", 1), true},
		{"equivalent quoted offset", strings.Replace(report, "as_of: '2026-11-02T15:24:00Z', basis: guess, assumptions: none, dependencies: none, critical_path: latest} # owner low", "as_of: '2026-11-02T15:24:00+00:00', basis: guess, assumptions: none, dependencies: none, critical_path: latest} # owner low", 1), true},
		{"unquoted retired timestamp", strings.Replace(report, "as_of: '2026-11-02T15:24:00Z', basis: guess", "as_of: 2026-11-02T15:24:00Z, basis: guess", 1), true},
		{"equivalent retired offset", strings.Replace(report, "as_of: '2026-11-02T15:24:00Z', basis: guess", "as_of: '2026-11-02T15:24:00+00:00', basis: guess", 1), true},
		{"changed retired timestamp", strings.Replace(report, "as_of: '2026-11-02T15:24:00Z', basis: guess", "as_of: '2026-11-02T15:23:00+00:00', basis: guess", 1), false},
		{"changed timestamp", strings.Replace(report, "as_of: '2026-11-02T15:24:00Z', basis: guess, assumptions: none, dependencies: none, critical_path: latest} # owner low", "as_of: 2026-11-02T15:23:00Z, basis: guess, assumptions: none, dependencies: none, critical_path: latest} # owner low", 1), false},
		{"changed destination", strings.Replace(report, "basis: guess, assumptions: none, dependencies: none, critical_path: latest} # owner low", "basis: owner edit, assumptions: none, dependencies: none, critical_path: latest} # owner low", 1), false},
		{"changed retired estimate", strings.Replace(report, "basis: guess, assumptions: none", "basis: owner edit, assumptions: none", 1), false},
		{"another high priority task", strings.Replace(report, "top_priorities:", "  - {id: '3', title: Other, priority: high, impact: Useful, effort: {min_minutes: 10, max_minutes: 20, kind: elapsed, basis: guess}, status: IN PROGRESS}\ntop_priorities:", 1), false},
	} {
		t.Run(tc.name,
			// Exercise the real refresh command and persisted replay metadata.
			//
			// Example: A conflicting owner edit leaves both task and estimates intact.
			func(t *testing.T) {
				dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
				path := filepath.Join(dir, "latest-status-report.yaml")
				require.NoError(t, os.WriteFile(path, []byte(tc.report), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
				var out, errOut bytes.Buffer
				require.Equal(t, 0, run([]string{"refresh", dir, "decision"}, strings.NewReader(""), &out, &errOut), errOut.String())
				result, err := os.ReadFile(path)
				require.NoError(t, err)
				parsed, err := parse(result)
				require.NoError(t, err)
				require.Contains(t, string(result), "# owner low estimate")
				if !tc.succeeds {
					require.Equal(t, priorityLabel("high"), parsed.Tasks[0].Priority)
					require.Len(t, parsed.PriorityEstimates, 2)
					require.NotEmpty(t, parsed.StatusSync.Groups)
					return
				}
				require.Equal(t, priorityLabel("low"), parsed.Tasks[0].Priority)
				require.Len(t, parsed.PriorityEstimates, 1)
				require.Equal(t, priorityLabel("low"), parsed.PriorityEstimates[0].Priority)
				if tc.name == "equivalent quoted offset" {
					require.Contains(t, string(result), "as_of: '2026-11-02T15:24:00+00:00'")
				}
				require.Empty(t, parsed.StatusSync.Groups)
				require.Empty(t, parsed.StatusSync.PendingAdditions)
				require.NotContains(t, parsed.StatusSync.Applied, "addition/priority_estimate/low")
				out.Reset()
				errOut.Reset()
				require.Equal(t, 0, run([]string{"refresh", dir, "decision"}, strings.NewReader(""), &out, &errOut), errOut.String())
				again, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, result, again)
			})
	}
}
