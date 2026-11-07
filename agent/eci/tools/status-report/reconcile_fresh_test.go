package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCanonicalRecalibrationConsistency checks optional unchanged values and inactive history.
//
// Example: Equivalent UTC spellings identify the same target.
func TestCanonicalRecalibrationConsistency(t *testing.T) {
	src := map[string]any{"target": "2001-01-01T00:00:00Z", "recalibration": map[string]any{"status": "unchanged", "evidence": "basis", "prior": "2001-01-01T00:00:00+00:00"}}
	require.Empty(t, validateCanonicalRecalibration(src))
	src["recalibration"].(map[string]any)["prior"] = "2001-01-01T01:00:00Z"
	require.Contains(t, strings.Join(validateCanonicalRecalibration(src), "\n"), "prior does not match target")
	delete(src, "target")
	require.Empty(t, validateCanonicalRecalibration(src))
}

// TestOutcomeIdentityChecks checks actual IDs and complete forecast ownership sets.
//
// Example: A custom outcome ID is valid when it points to an outcome record.
func TestOutcomeIdentityChecks(t *testing.T) {
	records := map[string]sourceRecord{"custom": {Type: "outcome"}, "wrong": {Type: "work"}}
	work := sourceRecord{Data: workData{State: workStateActive}, OutcomeRefs: []string{"custom"}}
	require.Empty(t, validateWorkOutcomes("w", work, records))
	work.OutcomeRefs = []string{"missing", "wrong"}
	require.Len(t, validateWorkOutcomes("w", work, records), 2)
	a := sourceRecord{Data: workData{State: workStateActive, ForecastRef: "forecasts.yaml#/records/f"}, OutcomeRefs: []string{"x", "y"}}
	b := sourceRecord{Data: workData{State: workStateActive, ForecastRef: "forecasts.yaml#/records/f"}, OutcomeRefs: []string{"y", "x"}}
	require.Empty(t, checkSharedForecastOwners(map[string]sourceRecord{"a": a, "b": b}))
	b.OutcomeRefs = []string{"x"}
	require.NotEmpty(t, checkSharedForecastOwners(map[string]sourceRecord{"a": a, "b": b}))
}

// TestForecastStatErrorIsVisible distinguishes unavailable paths from absent files.
//
// Example: A symlink loop must not be silently treated as no forecast file.
func TestForecastStatErrorIsVisible(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Symlink("forecasts.yaml", filepath.Join(dir, "forecasts.yaml")))
	var issues reconciliationIssues
	c := forecastReconciler{dir: dir, issues: &issues}
	c.checkUnclaimed(map[string]sourceRecord{})
	require.Contains(t, strings.Join(issues, "\n"), "forecast source stat failed")
}

// TestFutureAnchorsWithoutSessionStayVisible checks time before source lookup.
//
// Example: A future estimate is disclosed when session context is unavailable.
func TestFutureAnchorsWithoutSessionStayVisible(t *testing.T) {
	r, err := parse([]byte(linkedReport))
	require.NoError(t, err)
	evaluation, err := parseUTC("2026-11-02T15:00:00Z")
	require.NoError(t, err)
	issues := strings.Join(reconcileAt(r, "", evaluation), "\n")
	require.Contains(t, issues, "report timestamp")
	require.Contains(t, issues, "completion estimate as_of")
	require.Contains(t, issues, "session context unavailable")
}

// TestLaneProseNeedsCompleteCanonicalCorrespondence avoids guessing from different labels.
//
// Example: Each linked outcome must support the complete claim.
func TestLaneProseNeedsCompleteCanonicalCorrespondence(t *testing.T) {
	ledger := ledgerSource{Records: map[string]sourceRecord{"a": {Type: "outcome", Outcome: "Result"}, "b": {Type: "outcome", Outcome: "Result"}}}
	work := sourceRecord{OutcomeRefs: []string{"a"}}
	source := map[string]any{"outcome": "Result"}
	require.True(t, outcomeCorresponds(work, ledger.Records, "Result", textField(source, "outcome")))
	require.False(t, outcomeCorresponds(work, ledger.Records, "Other wording", textField(source, "outcome")))
	work.OutcomeRefs = []string{"a", "b"}
	require.True(t, outcomeCorresponds(work, ledger.Records, "Result", textField(source, "outcome")))
}

// TestHistoricalAnchorsAreEvaluatedAtCurrentTime preserves saved time anchors.
//
// Example: An old anchor can remain current if its endpoint has not elapsed.
func TestHistoricalAnchorsAreEvaluatedAtCurrentTime(t *testing.T) {
	reportText := strings.ReplaceAll(linkedReport, "2026-11-02T15:24:00Z", "2001-01-01T00:00:00Z")
	reportText = strings.ReplaceAll(reportText, "2026-11-02T16:00:00Z", "2001-01-01T01:00:00Z")
	reportText = strings.ReplaceAll(reportText, "2026-11-02T17:00:00Z", "2001-01-01T02:00:00Z")
	forecasts := strings.ReplaceAll(linkedForecasts, "2026-11-02T16:00:00+00:00", "2001-01-01T01:00:00Z")
	forecasts = strings.ReplaceAll(forecasts, "2026-11-02T16:00:00Z", "2001-01-01T01:00:00Z")
	forecasts = strings.ReplaceAll(forecasts, "2026-11-02T17:00:00Z", "2001-01-01T02:00:00Z")
	evaluation, err := parseUTC("2026-11-02T15:24:00Z")
	require.NoError(t, err)
	r, err := parse([]byte(reportText))
	require.NoError(t, err)
	require.Contains(t, strings.Join(reconcileAt(r, writeLinkedSources(t, linkedLedger, forecasts), evaluation), "\n"), "elapsed")
	reportText = strings.ReplaceAll(reportText, "2001-01-01T01:00:00Z", "2035-01-01T01:00:00Z")
	reportText = strings.ReplaceAll(reportText, "2001-01-01T02:00:00Z", "2035-01-01T02:00:00Z")
	reportText = strings.ReplaceAll(reportText, "min_minutes: 20, max_minutes: 40", "min_minutes: 18000000, max_minutes: 19000000")
	forecasts = strings.ReplaceAll(forecasts, "2001-01-01T01:00:00Z", "2035-01-01T01:00:00Z")
	forecasts = strings.ReplaceAll(forecasts, "2001-01-01T02:00:00Z", "2035-01-01T02:00:00Z")
	r, err = parse([]byte(reportText))
	require.NoError(t, err)
	require.Empty(t, reconcileAt(r, writeLinkedSources(t, linkedLedger, forecasts), evaluation))
}
