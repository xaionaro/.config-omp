package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInvalidRetractionsPreserveClaimAndIndependentProgress checks correction input.
//
// Example: A retraction outside a correction event cannot remove an owned claim.
func TestInvalidRetractionsPreserveClaimAndIndependentProgress(t *testing.T) {
	cases := []struct{ name, event, update string }{
		{"wrong event", "decision", `{"target":"progress","id":"work:one","corrects":1,"retract":true}`},
		{"missing predecessor", "correction", `{"target":"progress","id":"work:one","retract":true}`},
		{"negative ordinal", "correction", `{"target":"progress","id":"work:one","corrects":1,"corrects_ordinal":-1,"retract":true}`},
		{"retract with replacement", "correction", `{"target":"progress","id":"work:one","corrects":1,"retract":true,"fields":{"text":"replacement"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name,
			// Check one malformed correction beside independent progress.
			//
			// Example: An invalid retraction never removes the supplied claim.
			func(t *testing.T) {
				dir := t.TempDir()
				input := strings.Replace(linkedReport, "progress: [Implementation started]", "progress: [Implementation started, {subject_ref: 'work:one', text: 'owner claim'}] # keep", 1)
				require.NotEqual(t, linkedReport, input)
				event := `{"event":"` + tc.event + `","details":{"status_update":{"updates":[` + tc.update + `,{"target":"progress","id":"other","fields":{"text":"independent progress"}}]}}}` + "\n"
				// Correction events require an explicit predecessor for each update.
				if tc.event == "correction" {
					event = `{"event":"` + tc.event + `","details":{"status_update":{"updates":[` + tc.update + `]}}}` + "\n" + `{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"other","fields":{"text":"independent progress"}}]}}}` + "\n"
				}
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
				result, _, err := projectLogStatus(dir, []byte(input), ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				report, err := parse(result)
				require.NoError(t, err)
				require.Contains(t, report.Progress, "owner claim")
				require.Contains(t, report.Progress, "independent progress")
				require.Contains(t, string(result), "# keep")
				require.Contains(t, report.Blockers, "Status refresh incomplete: invalid status_update requires manual review")
			})
	}
}
