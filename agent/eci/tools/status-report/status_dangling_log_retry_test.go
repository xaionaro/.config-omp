package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDanglingLogTargetUsesReadPresenceForPublication checks absent log semantics.
//
// Example: A stable dangling symlink is absent, but a newly created target triggers retry.
func TestDanglingLogTargetUsesReadPresenceForPublication(t *testing.T) {
	// event is the first record written when the dangling target appears.
	//
	// Example: A later refresh projects the event after retry.
	const event = `{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"created target progress"}}]}}}` + "\n"
	for _, created := range []bool{false, true} {
		name := "stable missing target"
		if created {
			name = "target created during refresh"
		}
		t.Run(name,
			// Check one target state across the publication boundary.
			//
			// Example: A target that appears cannot be omitted from a successful refresh.
			func(t *testing.T) {
				dir := t.TempDir()
				reportPath := filepath.Join(dir, "latest-status-report.yaml")
				logPath := filepath.Join(dir, "high_level_log.jsonl")
				target := filepath.Join(dir, "future.jsonl")
				require.NoError(t, os.WriteFile(reportPath, []byte(linkedReport), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
				require.NoError(t, os.Symlink("future.jsonl", logPath))
				observer := testRefreshObserver{Action:
				// Create the previously absent target only for the race case.
				//
				// Example: A concurrent writer creates the first log entry.
				func() {
					if created {
						require.NoError(t, os.WriteFile(target, []byte(event), 0600))
					}
				}}
				err := refreshTestLinkedStatus(dir, "requirement_change", observer)
				if created {
					require.ErrorContains(t, err, "retry needed")
					before, readErr := os.ReadFile(reportPath)
					require.NoError(t, readErr)
					require.Equal(t, linkedReport, string(before))
					require.NoError(t, refreshTestStatus(dir, "requirement_change"))
				} else {
					require.NoError(t, err)
				}
				result, err := os.ReadFile(reportPath)
				require.NoError(t, err)
				report, err := parse(result)
				require.NoError(t, err)
				require.Contains(t, report.Blockers, refreshDiagnostic)
				require.Contains(t, report.Progress, "Implementation started")
				if created {
					require.Contains(t, report.Progress, "created target progress")
					require.Equal(t, 1, report.StatusSync.Cursor)
				} else {
					require.NotContains(t, report.Progress, "created target progress")
				}
			})
	}
}

// TestAbsentLogBecomingDanglingStillHasNoReadableHistory checks consistent presence.
//
// Example: Creating a dangling symlink alone does not force a false retry.
func TestAbsentLogBecomingDanglingStillHasNoReadableHistory(t *testing.T) {
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "latest-status-report.yaml")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(reportPath, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
	observer := testRefreshObserver{Action:
	// Create a link whose target remains absent during publication.
	//
	// Example: The readable event history is still absent.
	func() { require.NoError(t, os.Symlink("future.jsonl", logPath)) }}
	require.NoError(t, refreshTestLinkedStatus(dir, "requirement_change", observer))
	result, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Contains(t, report.Blockers, refreshDiagnostic)
	require.Contains(t, report.Progress, "Implementation started")
}

// TestAbsentLogChangingToAnUnusableTargetRequiresRetry checks publication safety.
//
// Example: A new empty file, directory or symlink loop is no longer an absent log.
func TestAbsentLogChangingToAnUnusableTargetRequiresRetry(t *testing.T) {
	for _, kind := range []string{"empty file", "directory", "symlink loop"} {
		t.Run(kind,
			// Create one new target after the absent-log read.
			//
			// Example: Reject a directory rather than publishing from a stale snapshot.
			func(t *testing.T) {
				dir := t.TempDir()
				reportPath := filepath.Join(dir, "latest-status-report.yaml")
				logPath := filepath.Join(dir, "high_level_log.jsonl")
				require.NoError(t, os.WriteFile(reportPath, []byte(linkedReport), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
				observer := testRefreshObserver{Action:
				// Install one source path before publication.
				//
				// Example: A loop must be reported as a retry rather than suppressed.
				func() {
					switch kind {
					case "empty file":
						require.NoError(t, os.WriteFile(logPath, []byte{}, 0600))
					case "directory":
						require.NoError(t, os.Mkdir(logPath, 0700))
					case "symlink loop":
						require.NoError(t, os.Symlink("high_level_log.jsonl", logPath))
					}
				}}
				require.ErrorContains(t, refreshTestLinkedStatus(dir, "requirement_change", observer), "retry needed")
				result, err := os.ReadFile(reportPath)
				require.NoError(t, err)
				require.Equal(t, linkedReport, string(result))
			})
	}
}
