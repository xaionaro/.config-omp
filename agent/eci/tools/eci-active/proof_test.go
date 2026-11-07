package main

import (
	"errors"
	"strings"
	"testing"
)

// exitMessage returns the diagnostic carried by err, or fails the test when err is not an exitError.
func exitMessage(t *testing.T, err error) string {
	t.Helper()
	var ee exitError
	if !errors.As(err, &ee) {
		t.Fatalf("want exitError, got %#v", err)
	}
	return ee.msg
}

// TestCurrentSessionSelectsSessionThenThreadID checks that a non-empty ECI_SESSION_ID wins and ECI_THREAD_ID is used only when ECI_SESSION_ID is empty.
func TestCurrentSessionSelectsSessionThenThreadID(t *testing.T) {
	const session = "session_one"
	const thread = "thread_two"
	tests := []struct {
		name    string
		sid     string // value of ECI_SESSION_ID
		tid     string // value of ECI_THREAD_ID
		wantSID string // identity expected when wantErr is empty
		wantErr string // substring of the expected diagnostic
	}{
		{name: "session valid", sid: session, tid: thread, wantSID: session},
		{name: "session empty, thread valid", sid: "", tid: thread, wantSID: thread},
		{name: "session invalid, thread valid", sid: "bad id", tid: thread, wantErr: "Invalid ECI_SESSION_ID"},
		{name: "session empty, thread invalid", sid: "", tid: "bad id", wantErr: "Invalid ECI_THREAD_ID"},
		{name: "both empty", sid: "", tid: "", wantErr: "ECI coordination requires an explicit ECI_SESSION_ID or ECI_THREAD_ID."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ECI_SESSION_ID", tt.sid)
			t.Setenv("ECI_THREAD_ID", tt.tid)
			sid, err := currentSession()
			if tt.wantErr != "" {
				if msg := exitMessage(t, err); !strings.Contains(msg, tt.wantErr) {
					t.Fatalf("currentSession diagnostic = %q, want it to contain %q", msg, tt.wantErr)
				}
				return
			}
			if err != nil || sid != tt.wantSID {
				t.Fatalf("currentSession() = %q, %v; want %q, nil", sid, err, tt.wantSID)
			}
		})
	}
}

// TestCurrentSessionReadersReportBothVariables checks that the readers that do not call currentSession name both variables when both are unset.
func TestCurrentSessionReadersReportBothVariables(t *testing.T) {
	t.Setenv("ECI_SESSION_ID", "")
	t.Setenv("ECI_THREAD_ID", "")
	if msg := exitMessage(t, manifestWrite([]string{"manifest-write", "x"})); msg != "manifest-write requires an explicit current ECI_SESSION_ID or ECI_THREAD_ID." {
		t.Errorf("manifest-write diagnostic = %q", msg)
	}
	if msg := exitMessage(t, aggregateReview([]string{"aggregate-review", "prewrite", "repo-a"})); msg != "aggregate-review requires an explicit current ECI_SESSION_ID or ECI_THREAD_ID." {
		t.Errorf("aggregate-review diagnostic = %q", msg)
	}
}
