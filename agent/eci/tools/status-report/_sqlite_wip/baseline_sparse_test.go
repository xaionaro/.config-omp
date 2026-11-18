package main

import (
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "strings"
 "testing"
 "time"

 "github.com/stretchr/testify/require"
)

// sparseStatusLog combines one independent update with one canonically dependent closure.
//
// Example: The closure keeps its ordinal while next_focus can advance.
const sparseStatusLog = `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"report","id":"current","fields":{"next_focus":"independent-sparse"}},{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}`+"\n"

// sparseEvaluation keeps completeness checks independent of wall time.
//
// Example: The source completion is not future-dated.
var sparseEvaluation = time.Date(2026,11,2,15,26,0,0,time.UTC)

// sparseParsed reads the native output using the actual report decoder.
//
// Example: Preserve opaque JSON while checking semantic replay results.
func sparseParsed(t *testing.T, raw []byte) report {
 t.Helper()
 replay,err:=reportJSONToReplayYAML(raw);require.NoError(t,err)
 parsed,err:=parseDBReport(replay);require.NoError(t,err)
 return parsed
}

// TestBaselineSparseSourcesPreservePendingAndRetry covers every source presence combination.
//
// Example: Adding missing canonical evidence retries the original closure without reappend.
func TestBaselineSparseSourcesPreservePendingAndRetry(t *testing.T) {
 t.Parallel()
 for _,ledgerPresent:=range []bool{false,true} { for _,forecastPresent:=range []bool{false,true} {
  t.Run(fmt.Sprintf("ledger=%t/forecast=%t",ledgerPresent,forecastPresent),
   // The callback tests one explicit presence combination through preparation and projection.
   //
   // Example: A present report does not manufacture an absent ledger.
   func(t *testing.T) {
    dir:=t.TempDir();rawLog:=[]byte(sparseStatusLog)
    require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),rawLog,0600))
    captured,err:=captureReplayLog(dir);require.NoError(t,err)
    native:=baselineNativeFixture(t,linkedReport)
    value,err:=decodeReportValue(native);require.NoError(t,err)
    root:=value.(map[string]any);root["owner_extension"]=map[string]any{"huge":json.Number("1e400"),"precise":json.Number("1.10")}
    native,err=json.Marshal(root);require.NoError(t,err)
    prepared,err:=prepareBaseline(native,rawLog,map[string]baselineMode{"1/u/0":baselinePending,"1/u/1":baselinePending});require.NoError(t,err)
    completeLedger,completeForecasts:=baselineSources(t)
    sources:=reconciliationSnapshot{ledgerPresent:ledgerPresent,forecastPresent:forecastPresent}
    if ledgerPresent {sources.ledger=completeLedger};if forecastPresent {sources.forecasts=completeForecasts}
    sourceBefore,err:=json.Marshal(sources.ledger);require.NoError(t,err)
    projection,err:=projectDBReportResult(dir,"report_create",projectionSnapshot{Report:baselineReload(t,prepared),ReportPresent:true,Sources:sources},sparseEvaluation,captured,true);require.NoError(t,err)
    projected,accepted:=projection.Document,projection.Receipts
    parsed:=sparseParsed(t,projected);require.Equal(t,"independent-sparse",parsed.NextFocus)
    require.Contains(t,accepted,syncOperationLocator{Kind:syncOperationUpdate,Line:1,Ordinal:0})
    selector:=syncUpdate{Target:syncTargetLane,ID:"1",WorkRef:"project-understanding.yaml#/records/work:first",RequiredEnvironment:syncEnvironmentImplementation}
    key:=syncGroupKey(1,1)
    if ledgerPresent&&forecastPresent {
     require.Equal(t,statusClosed,parsed.Lanes[0].ImplementationStatus);require.Nil(t,parsed.Lanes[0].Forecast)
     require.NotContains(t,parsed.StatusSync.Groups,key)
     require.Equal(t,1,parsed.StatusSync.Latest[syncTargetKey(selector,"implementation_status")]);require.Equal(t,1,parsed.StatusSync.AppliedOrdinals[syncTargetKey(selector,"implementation_status")])
     require.Contains(t,accepted,syncOperationLocator{Kind:syncOperationUpdate,Line:1,Ordinal:1})
    } else {
     require.Equal(t,statusInProgress,parsed.Lanes[0].ImplementationStatus);require.NotNil(t,parsed.Lanes[0].Forecast)
     require.False(t,*parsed.ScopeComplete);require.NotEmpty(t,parsed.StuckRisks)
     pending,present:=parsed.StatusSync.Groups[key];require.True(t,present);require.Equal(t,1,pending.Line);require.Equal(t,1,pending.Ordinal)
     occurrence,err:=resolveBaselineOperation(rawLog,syncOperationLocator{Kind:syncOperationUpdate,Line:1,Ordinal:1});require.NoError(t,err)
     stored,err:=decodeSyncGroup(pending.Update);require.NoError(t,err);same,err:=sameBaselineOperation(stored,*occurrence.Update);require.NoError(t,err);require.True(t,same)
     require.Zero(t,parsed.StatusSync.Latest[syncTargetKey(selector,"implementation_status")])
     require.NotContains(t,accepted,syncOperationLocator{Kind:syncOperationUpdate,Line:1,Ordinal:1})
    }
    saved:=string(projected)
    decoded,err:=decodeReportValue([]byte(saved));require.NoError(t,err);extension:=decoded.(map[string]any)["owner_extension"].(map[string]any)
    require.Equal(t,json.Number("1e400"),extension["huge"]);require.Equal(t,json.Number("1.10"),extension["precise"])
    require.Equal(t,ledgerPresent,sources.ledgerPresent);require.Equal(t,forecastPresent,sources.forecastPresent)
    sourceAfter,err:=json.Marshal(sources.ledger);require.NoError(t,err);require.Equal(t,string(sourceBefore),string(sourceAfter))
    _,err=os.Stat(filepath.Join(dir,"project-understanding.yaml"));require.True(t,os.IsNotExist(err));_,err=os.Stat(filepath.Join(dir,"forecasts.yaml"));require.True(t,os.IsNotExist(err))
    if ledgerPresent&&forecastPresent {return}
    arrived:=reconciliationSnapshot{ledgerPresent:true,forecastPresent:true,ledger:completeLedger,forecasts:completeForecasts}
    retry,err:=projectDBReportResult(dir,"worker_progress",projectionSnapshot{Report:baselineReload(t,[]byte(saved)),ReportPresent:true,Sources:arrived},sparseEvaluation,captured,true);require.NoError(t,err)
    retried:=sparseParsed(t,retry.Document);require.Equal(t,statusClosed,retried.Lanes[0].ImplementationStatus);require.Nil(t,retried.Lanes[0].Forecast);require.NotContains(t,retried.StatusSync.Groups,key)
    require.Equal(t,1,retried.StatusSync.Latest[syncTargetKey(selector,"implementation_status")]);require.Equal(t,1,retried.StatusSync.AppliedOrdinals[syncTargetKey(selector,"implementation_status")])
    // A fresh owner forecast edit must remain protected after source arrival.
    current,err:=decodeReportValue([]byte(saved));require.NoError(t,err);forecast:=current.(map[string]any)["lanes"].([]any)[0].(map[string]any)["forecast"].(map[string]any);forecast["outcome"]="Owner-after-birth"
    diverged,err:=json.Marshal(current);require.NoError(t,err)
    retained,err:=projectDBReportResult(dir,"worker_progress",projectionSnapshot{Report:baselineReload(t,diverged),ReportPresent:true,Sources:arrived},sparseEvaluation,captured,true);require.NoError(t,err)
    protected:=sparseParsed(t,retained.Document);require.Equal(t,statusInProgress,protected.Lanes[0].ImplementationStatus);require.Equal(t,"Owner-after-birth",protected.Lanes[0].Forecast.Outcome);require.Contains(t,protected.StatusSync.Groups,key)
    require.Equal(t,"independent-sparse",protected.NextFocus)
    sourceLog,err:=os.ReadFile(filepath.Join(dir,"high_level_log.jsonl"));require.NoError(t,err);require.Equal(t,string(rawLog),string(sourceLog))
   })
 }}
}

// TestBaselineSparseReferencedMissingForecastPreservesContent separates missing evidence from malformed sources.
//
// Example: Both an absent forecast source and a present empty source leave the owner forecast intact.
func TestBaselineSparseReferencedMissingForecastPreservesContent(t *testing.T) {
 t.Parallel()
 for _,present:=range []bool{false,true} {t.Run(fmt.Sprint(present),
  // The callback checks one missing-record representation without manufacturing values.
  //
  // Example: Independent source-free work still proceeds.
  func(t *testing.T) {
   ledger,_:=baselineSources(t)
   work:=ledger.Records["work:first"];work.Data.State=workStateActive;work.Data.CompletedAt="";ledger.Records["work:first"]=work
   sources:=reconciliationSnapshot{ledgerPresent:true,forecastPresent:present,ledger:ledger}
   if present {sources.forecasts=forecastSource{Schema:"forecasts/v1",Records:map[string]map[string]any{}}}
   log:=[]byte(`{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"report","id":"current","fields":{"next_focus":"missing-forecast-independent"}}]}}}`+"\n")
   native:=baselineNativeFixture(t,linkedReport);prepared,err:=prepareBaseline(native,log,map[string]baselineMode{"1/u/0":baselinePending});require.NoError(t,err)
   dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),log,0600));captured,err:=captureReplayLog(dir);require.NoError(t,err)
   out,err:=projectDBReportResult(dir,"report_create",projectionSnapshot{Report:prepared,ReportPresent:true,Sources:sources},sparseEvaluation,captured,true);require.NoError(t,err)
   parsed:=sparseParsed(t,out.Document);require.Equal(t,"missing-forecast-independent",parsed.NextFocus);require.NotNil(t,parsed.Lanes[0].Forecast);require.Equal(t,"First result",parsed.Lanes[0].Forecast.Outcome);require.False(t,*parsed.ScopeComplete)
   require.Contains(t,strings.Join(reconcileSnapshotAt(parsed,sources,sparseEvaluation),"\n"),"forecast")
  })}
}

// TestBaselineSparseRejectsInconsistentEvidence refuses malformed snapshots instead of treating them as absent.
//
// Example: A source with rows but an absent flag cannot authorize replay.
func TestBaselineSparseRejectsInconsistentEvidence(t *testing.T) {
 t.Parallel()
 ledger,forecasts:=baselineSources(t)
 cases:=[]reconciliationSnapshot{{ledger:ledger},{forecasts:forecasts},{ledgerPresent:true},{forecastPresent:true},{ledgerPresent:true,ledger:ledgerSource{Schema:"wrong",Records:map[string]sourceRecord{},Sections:map[string]any{}}}}
 for index,sources:=range cases {t.Run(fmt.Sprint(index),
  // The callback checks one invalid presence or source shape.
  //
  // Example: No partial document escapes validation.
  func(t *testing.T) {
   dir:=t.TempDir();captured,err:=captureReplayLog(dir);require.NoError(t,err)
   native:=baselineNativeFixture(t,linkedReport);prepared,err:=prepareBaseline(native,nil,map[string]baselineMode{});require.NoError(t,err)
   out,err:=projectDBReportResult(dir,"report_create",projectionSnapshot{Report:prepared,ReportPresent:true,Sources:sources},sparseEvaluation,captured,true);require.Error(t,err);require.Empty(t,out.Document);require.Empty(t,out.Receipts)
  })}
}

// TestBaselineSparsePresentEmptyAndAbsentReport preserves first-class source and report absence.
//
// Example: Empty present ledger maps do not become unavailable or manufacture a missing report.
func TestBaselineSparsePresentEmptyAndAbsentReport(t *testing.T) {
 t.Parallel()
 sources:=reconciliationSnapshot{ledgerPresent:true,ledger:ledgerSource{Schema:"project-understanding/v1",Sections:map[string]any{},Records:map[string]sourceRecord{}}}
 dir:=t.TempDir();log:=[]byte(sparseStatusLog);require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),log,0600));captured,err:=captureReplayLog(dir);require.NoError(t,err)
 prepared,err:=prepareBaseline(baselineNativeFixture(t,linkedReport),log,map[string]baselineMode{"1/u/0":baselinePending,"1/u/1":baselinePending});require.NoError(t,err)
 out,err:=projectDBReportResult(dir,"report_create",projectionSnapshot{Report:prepared,ReportPresent:true,Sources:sources},sparseEvaluation,captured,true);require.NoError(t,err)
 parsed:=sparseParsed(t,out.Document);require.Equal(t,"independent-sparse",parsed.NextFocus);require.Equal(t,statusInProgress,parsed.Lanes[0].ImplementationStatus);require.Contains(t,parsed.StatusSync.Groups,syncGroupKey(1,1));require.NotNil(t,sources.ledger.Records);require.Empty(t,sources.ledger.Records)
 absent,err:=projectDBReportResult(dir,"worker_progress",projectionSnapshot{ReportPresent:false,Sources:sources},sparseEvaluation,captured,true);require.Error(t,err);require.Empty(t,absent.Document);require.Empty(t,absent.Receipts)
 _,err=os.Stat(filepath.Join(dir,"ledger.db"));require.True(t,os.IsNotExist(err))
}
