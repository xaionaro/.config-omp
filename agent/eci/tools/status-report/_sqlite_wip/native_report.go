package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"time"
)

// nativeReportPreparation describes a report and explicit decisions for a captured log.
//
// Example: A private candidate supplies the prospective native sources for projection.
type nativeReportPreparation struct {
	Document json.RawMessage `json:"document"`
	Decisions map[string]string `json:"decisions"`
	Log json.RawMessage `json:"log"`
	CandidatePath string `json:"candidate_path"`
	SessionDir string `json:"session_dir"`
	Event string `json:"event"`
}

// runNativeReportPrepare prepares and projects a report against a private candidate snapshot.
//
// Example: Native initialization verifies report semantics before installing its candidate.
func runNativeReportPrepare(
	args []string,
	in io.Reader,
	out io.Writer,
	errOut io.Writer,
) int {
	if len(args) != 1 || args[0] != "--stdin" {
		if err := writeRefreshDiagnostic(errOut, "usage: status-report report-prepare --stdin\n"); err != nil { return 1 }
		return 2
	}
	data, err := io.ReadAll(in)
	if err == nil { err = validateNativeEnvelopeKeys(data, "document", "decisions", "log", "candidate_path", "session_dir", "event") }
	var request nativeReportPreparation
	if err == nil {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&request)
	}
	var result []byte
	if err == nil { result, err = prepareNativeReport(context.Background(), request) }
	if err == nil {
		var written int
		written, err = out.Write(result)
		if err == nil && written != len(result) { err = io.ErrShortWrite }
	}
	if err != nil {
		if writeErr := writeRefreshDiagnostic(errOut, fmt.Sprintln("status-report report-prepare:", err)); writeErr != nil { return 1 }
		return 1
	}
	return 0
}

// validateNativeEnvelopeKeys rejects ambiguous or case-folded request member names.
//
// Example: "Document" is not the selected "document" field.
func validateNativeEnvelopeKeys(data []byte, allowed ...string) error {
	value, err := decodeReportValue(data)
	if err != nil { return err }
	object, ok := value.(map[string]any)
	if !ok { return fmt.Errorf("request must be an object") }
	for key := range object {
		found := false
		for _, candidate := range allowed { if key == candidate { found = true; break } }
		if !found { return fmt.Errorf("unknown request field %q", key) }
	}
	return nil
}

// prepareNativeReport validates, prepares, and projects against one private database snapshot.
//
// Example: An unresolved historical log occurrence prevents candidate publication.
func prepareNativeReport(
	ctx context.Context,
	request nativeReportPreparation,
) (_ []byte, _err error) {
	if !filepath.IsAbs(request.CandidatePath) || !filepath.IsAbs(request.SessionDir) || request.Event != "report_create" || request.Decisions == nil {
		return nil, fmt.Errorf("candidate, session, report_create event and decisions are required")
	}
	if _, err := decodeReportValue(request.Document); err != nil { return nil, err }
	if _, err := parseDBReport(request.Document); err != nil { return nil, err }
	decisions := make(map[string]baselineMode, len(request.Decisions))
	for key, mode := range request.Decisions {
		switch mode {
		case "pending": decisions[key] = baselinePending
		case "retire": decisions[key] = baselineRetire
		case "adopt-current": decisions[key] = baselineAdopt
		default: return nil, fmt.Errorf("invalid decision %q", mode)
		}
	}
	if err := guardSQLiteFiles(request.CandidatePath); err != nil { return nil, err }
	u := url.URL{Scheme: "file", Path: request.CandidatePath}
	q := u.Query(); q.Set("mode", "ro"); q.Add("_pragma", "busy_timeout(5000)"); u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String()); if err != nil { return nil, err }
	defer func() { _err = errors.Join(_err, db.Close()) }()
	snapshot, err := loadProjectionSnapshot(ctx, db, request.SessionDir); if err != nil { return nil, err }
	if snapshot.ReportPresent { return nil, fmt.Errorf("report already exists") }
	expectedLog, err := decodeNativeLogObservation(request.Log); if err != nil { return nil, err }
	log, err := captureReplayLog(request.SessionDir); if err != nil { return nil, err }
	if err := checkNativeLogObservation(log, expectedLog); err != nil { return nil, err }
	prepared, err := prepareBaseline(request.Document, log.Bytes, decisions); if err != nil { return nil, err }
	snapshot.Report, snapshot.ReportPresent = prepared, true
	projected, err := projectDBReport(request.SessionDir, request.Event, snapshot, time.Now().UTC(), log); if err != nil { return nil, err }
	if err := verifyReplayLog(request.SessionDir, log); err != nil { return nil, err }
	return json.Marshal(struct { Document json.RawMessage `json:"document"` }{Document: projected})
}
