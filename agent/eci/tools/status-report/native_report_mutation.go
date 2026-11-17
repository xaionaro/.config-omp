package main

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"path/filepath"
	"time"
)

// nativeReportMutation carries an observed database token and one owner-authorized action.
//
// Example: A late report creation supplies explicit decisions for every existing log operation.
type nativeReportMutation struct {
	Base json.RawMessage `json:"base"`
	Document json.RawMessage `json:"document,omitempty"`
	Decisions map[string]string `json:"decisions,omitempty"`
	Log json.RawMessage `json:"log"`
	Action baselineReconciliationAction `json:"action,omitempty"`
	Pending syncOperationLocator `json:"pending,omitempty"`
	Predecessor *syncOperationLocator `json:"predecessor,omitempty"`
	Expected map[string]reportEditExpectation `json:"expected,omitempty"`
}

// runNativeReportMutation applies a create or reconciliation while the caller holds the session lock.
//
// Example: A stale database observation is rejected without publishing report changes.
func runNativeReportMutation(
	args []string,
	in io.Reader,
	out io.Writer,
	errOut io.Writer,
	create bool,
) int {
	if len(args) != 2 || args[0] == "" || args[1] != "--stdin" {
		if err := writeRefreshDiagnostic(errOut, "usage: status-report report mutation SESSION_DIR --stdin\n"); err != nil { return 1 }
		return 2
	}
	data, err := io.ReadAll(in)
	if err == nil { err = validateNativeEnvelopeKeys(data, "base", "document", "decisions", "log", "action", "pending", "predecessor", "expected") }
	var request nativeReportMutation
	if err == nil {
		decoder := json.NewDecoder(bytes.NewReader(data)); decoder.DisallowUnknownFields(); err = decoder.Decode(&request)
	}
	var base nativeReportBase
	if err == nil { base, err = decodeNativeReportBase(request.Base) }
	if err == nil { err = mutateNativeReport(context.Background(), args[0], base, request, create) }
	if err != nil {
		if writeErr := writeRefreshDiagnostic(errOut, fmt.Sprintln("status-report report mutation:", err)); writeErr != nil { return 1 }
		return 1
	}
	result := struct {
		DatabaseID string `json:"database_id"`
		Generation string `json:"generation"`
	}{DatabaseID: base.DatabaseID, Generation: strconv.FormatInt(base.Generation+1, 10)}
	if err := json.NewEncoder(out).Encode(result); err != nil {
		if writeErr := writeRefreshDiagnostic(errOut, fmt.Sprintln("status-report: report committed but token output failed:", err)); writeErr != nil { return 1 }
		return 1
	}
	return 0
}

// mutateNativeReport prepares, projects, and atomically publishes one authorized report action.
//
// Example: A changed log or generation aborts the complete reconciliation.
func mutateNativeReport(
	ctx context.Context,
	dir string,
	base nativeReportBase,
	request nativeReportMutation,
	create bool,
) (_err error) {
	path := filepath.Join(dir, "ledger.db")
	if err := guardSQLiteFiles(path); err != nil { return err }
	u := url.URL{Scheme:"file",Path:path}; q:=u.Query(); q.Set("mode","rw"); q.Add("_pragma","busy_timeout(5000)"); u.RawQuery=q.Encode()
	db, err := sql.Open("sqlite",u.String()); if err != nil { return err }
	defer func(){ _err=errors.Join(_err,db.Close()) }()
	snapshot, err := loadProjectionSnapshot(ctx,db,dir); if err != nil { return err }
	if snapshot.DatabaseID != base.DatabaseID || snapshot.Generation != base.Generation { return errRefreshRetry }
	if create == snapshot.ReportPresent { return fmt.Errorf("report presence conflicts with requested action") }
	expectedLog, err := decodeNativeLogObservation(request.Log); if err != nil { return err }
	log, err := captureReplayLog(dir); if err != nil { return err }
	if err := checkNativeLogObservation(log, expectedLog); err != nil { return err }
	var prepared []byte
	if create {
		if request.Decisions == nil { return fmt.Errorf("decisions required") }
		if _,err := decodeReportValue(request.Document); err != nil { return err }
		if _,err := parseDBReport(request.Document); err != nil { return err }
		decisions:=make(map[string]baselineMode,len(request.Decisions))
		for key,value:=range request.Decisions { switch value { case "pending": decisions[key]=baselinePending; case "retire": decisions[key]=baselineRetire; case "adopt-current": decisions[key]=baselineAdopt; default: return fmt.Errorf("invalid decision %q",value) } }
		prepared,err=prepareBaseline(request.Document,log.Bytes,decisions)
	} else {
		reconciliation:=baselineReconciliation{Action:request.Action,Pending:request.Pending,Predecessor:request.Predecessor,Expected:request.Expected}
		prepared,err=reconcileBaselinePending(snapshot.Report,log.Bytes,reconciliation)
	}
	if err != nil { return err }
	projectionSnapshot:=snapshot; projectionSnapshot.Report=prepared; projectionSnapshot.ReportPresent=true
	resume := !create && request.Action == baselineResume
	projected,err:=projectDBReportResult(dir,"report_create",projectionSnapshot,time.Now().UTC(),log,resume); if err != nil { return err }
	if resume {
		found := false
		for _, receipt := range projected.Receipts { if receipt == request.Pending { found = true; break } }
		if !found { return fmt.Errorf("selected operation did not project") }
	}
	return publishNativeReportMutation(ctx,db,dir,snapshot,log,projected.Document,create)
}

// publishNativeReportMutation compares the captured snapshot and log before committing.
//
// Example: A concurrent source edit rejects the report without changing either version.
func publishNativeReportMutation(
	ctx context.Context,
	db *sql.DB,
	dir string,
	snapshot projectionSnapshot,
	log replayLogSnapshot,
	projected []byte,
	create bool,
) (_err error) {
	conn,err:=db.Conn(ctx);if err!=nil{return err};defer func(){_err=errors.Join(_err,conn.Close())}()
	if _,err:=conn.ExecContext(ctx,"BEGIN IMMEDIATE");err!=nil{return err}; committed:=false
	defer func(){if !committed {_,e:=conn.ExecContext(context.Background(),"ROLLBACK");_err=errors.Join(_err,e);if e!=nil {discard:=conn.Raw(discardReportConnection);if !errors.Is(discard,driver.ErrBadConn){_err=errors.Join(_err,discard)}}}}()
	var id string;var generation int64
	if err:=conn.QueryRowContext(ctx,"SELECT database_id,generation FROM ledger_meta WHERE id=1").Scan(&id,&generation);err!=nil{return err}
	if id!=snapshot.DatabaseID||generation!=snapshot.Generation{return errRefreshRetry}
	if err:=verifyReplayLog(dir,log);err!=nil{return err}
	var result sql.Result
	if create {result,err=conn.ExecContext(ctx,"INSERT INTO report(id,document_json,revision) VALUES(1,?,1)",string(projected))} else {result,err=conn.ExecContext(ctx,"UPDATE report SET document_json=?,revision=revision+1 WHERE id=1 AND revision=?",string(projected),snapshot.Revision)}
	if err!=nil{return err};count,err:=result.RowsAffected();if err!=nil{return err};if count!=1{return errRefreshRetry}
	result,err=conn.ExecContext(ctx,"UPDATE ledger_meta SET generation=generation+1 WHERE id=1 AND database_id=? AND generation=?",id,generation);if err!=nil{return err};count,err=result.RowsAffected();if err!=nil{return err};if count!=1{return errRefreshRetry}
	if _,err:=conn.ExecContext(ctx,"COMMIT");err!=nil{return err};committed=true;return nil
}
