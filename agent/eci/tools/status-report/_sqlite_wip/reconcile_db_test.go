package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestReconcileSnapshotUsesSuppliedSources checks that reconciliation does not consult stale files.
//
// Example: An activated session uses its transaction snapshot rather than legacy YAML.
func TestReconcileSnapshotUsesSuppliedSources(t *testing.T) {
	t.Parallel()
	r := report{ScopeComplete: new(bool)}
	*r.ScopeComplete = true
	sources := reconciliationSnapshot{
		ledger: ledgerSource{Schema: "project-understanding/v1", Sections: map[string]any{}, Records: map[string]sourceRecord{}},
		ledgerPresent: true,
		forecasts: forecastSource{Schema: "forecasts/v1", Records: map[string]map[string]any{}},
		forecastPresent: true,
	}
	issues := strings.Join(reconcileSnapshotAt(r, sources, time.Time{}), "\n")
	require.NotContains(t, issues, "ledger unavailable")
	require.NotContains(t, issues, "forecast source unavailable")
}

// TestProjectionEvidenceDistinguishesAbsentAndEmpty checks snapshot presence consistency.
//
// Example: A present empty source is valid, while a missing envelope is malformed.
func TestProjectionEvidenceDistinguishesAbsentAndEmpty(t *testing.T) {
	t.Parallel()
	empty := ledgerSource{Schema: "project-understanding/v1", Sections: map[string]any{}, Records: map[string]sourceRecord{}}
	require.NoError(t, validateProjectionEvidence(reconciliationSnapshot{}))
	require.NoError(t, validateProjectionEvidence(reconciliationSnapshot{ledgerPresent: true, ledger: empty}))
	require.Error(t, validateProjectionEvidence(reconciliationSnapshot{ledger: empty}))
	require.Error(t, validateProjectionEvidence(reconciliationSnapshot{ledgerPresent: true}))
	require.Error(t, validateProjectionEvidence(reconciliationSnapshot{forecastPresent: true}))
	require.Error(t, validateProjectionEvidence(reconciliationSnapshot{forecasts: forecastSource{Schema: "forecasts/v1", Records: map[string]map[string]any{}}}))
}

// TestSourceRecordRejectsWrongConsumedTypes checks source scalar shape.
//
// Example: An integer owner cannot be coerced into a textual owner.
func TestSourceRecordRejectsWrongConsumedTypes(t *testing.T) {
	t.Parallel()
	var record sourceRecord
	require.Error(t, yaml.Unmarshal([]byte(`{"type":"work","section":"s","data":{"owner":7}}`), &record))
	require.Error(t, yaml.Unmarshal([]byte(`{"type":"work","section":"s","data":{"reporting_roles":[true]}}`), &record))
	require.NoError(t, yaml.Unmarshal([]byte(`{"type":"work","section":"s","data":{"owner":"alice"}}`), &record))
	require.Equal(t, "alice", record.Data.Owner)
}
