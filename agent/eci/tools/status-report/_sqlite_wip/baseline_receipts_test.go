package main

import (
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "testing"

 "github.com/stretchr/testify/require"
)

// TestBaselineProjectionReceiptsCoverTypedOperations checks receipts from actual verified acceptance branches.
//
// Example: A task update, complete addition and timeline each retain their exact canonical locator.
func TestBaselineProjectionReceiptsCoverTypedOperations(t *testing.T) {
 t.Parallel()
 log:=`{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"receipt-owner"}}]}}}`+"\n"+
 `{"event":"requirement_change","details":{"status_update":{"additions":[{"target":"task","record":{"id":"3","title":"New","priority":"high","impact":"Useful","effort":{"min_minutes":10,"max_minutes":20,"kind":"elapsed","basis":"guess"},"status":"NEW"}}]}}}`+"\n"+baselineTimelineEvent(t,"receipt-timeline")
 native:=baselineNativeFixture(t,linkedReport+"timeline_as_of: '2026-11-02T15:24:00Z'\ntimeline_state: verified_empty\ntimeline: []\n")
 prepared,err:=prepareBaseline(native,[]byte(log),map[string]baselineMode{"1/u/0":baselinePending,"2/a/0":baselinePending,"3/t/0":baselinePending});require.NoError(t,err)
 dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(log),0600));projection,err:=projectLogStatusResult(dir,baselineReload(t,prepared),ledgerSource{},forecastSource{},true);require.NoError(t,err)
 result,receipts:=projection.Document,projection.Receipts
 parsed,err:=parseDBReport(result);require.NoError(t,err);require.Equal(t,"receipt-owner",parsed.Tasks[0].Owner);require.Len(t,parsed.Tasks,3);require.Equal(t,"receipt-timeline",parsed.Timeline[0].Normal)
 require.Contains(t,receipts,syncOperationLocator{Kind:syncOperationUpdate,Line:1,Ordinal:0});require.Contains(t,receipts,syncOperationLocator{Kind:syncOperationAddition,Line:2,Ordinal:0});require.Contains(t,receipts,syncOperationLocator{Kind:syncOperationTimeline,Line:3,Ordinal:0})
 require.Len(t,receipts,3)
 // Retrying without any new event does not repeat old successful receipts.
 retry,err:=projectLogStatusResult(dir,result,ledgerSource{},forecastSource{},true);require.NoError(t,err);require.Empty(t,retry.Receipts)
}

// TestBaselineProjectionReceiptRefusesInvalidPendingCleanup checks the concrete false-positive route.
//
// Example: A self-correcting queued operation is removed as invalid, not reported projected.
func TestBaselineProjectionReceiptRefusesInvalidPendingCleanup(t *testing.T) {
 t.Parallel()
 log:=[]byte(`{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"must-not-apply"}},{"target":"report","id":"current","fields":{"next_focus":"receipt-independent"}}]}}}`+"\n")
 prepared,err:=prepareBaseline(baselineNativeFixture(t,linkedReport),log,map[string]baselineMode{"1/u/0":baselinePending,"1/u/1":baselinePending});require.NoError(t,err)
 decoded,err:=decodeReportValue(prepared);require.NoError(t,err);root:=decoded.(map[string]any);raw,err:=json.Marshal(root["status_sync"]);require.NoError(t,err);var state syncState;require.NoError(t,json.Unmarshal(raw,&state))
 key:=syncGroupKey(1,0);group:=state.Groups[key];update,err:=decodeSyncGroup(group.Update);require.NoError(t,err);update.Corrects=group.Line;ordinal:=group.Ordinal;update.CorrectsOrdinal=&ordinal;group.Update,err=encodeSyncGroup(update);require.NoError(t,err);state.Groups[key]=group;root["status_sync"]=state;corrupted,err:=json.Marshal(root);require.NoError(t,err)
 dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),log,0600));projection,err:=projectLogStatusResult(dir,corrupted,ledgerSource{},forecastSource{},true);require.NoError(t,err)
 out,receipts:=projection.Document,projection.Receipts
 parsed,err:=parseDBReport(out);require.NoError(t,err);require.NotContains(t,parsed.StatusSync.Groups,key);require.NotEqual(t,"must-not-apply",parsed.Tasks[0].Owner)
 require.Equal(t,"receipt-independent",parsed.NextFocus);require.NotContains(t,receipts,syncOperationLocator{Kind:syncOperationUpdate,Line:1,Ordinal:0});require.Contains(t,receipts,syncOperationLocator{Kind:syncOperationUpdate,Line:1,Ordinal:1})
}

// TestBaselineProjectionReceiptsStayPrivateOnWholePassFailure checks final projection gating.
//
// Example: A later malformed forecast cannot leak an earlier independent operation's receipt.
func TestBaselineProjectionReceiptsStayPrivateOnWholePassFailure(t *testing.T) {
 t.Parallel()
 ledger,forecasts:=baselineSources(t);work:=ledger.Records["work:first"];work.Data.State=workStateActive;work.Data.CompletedAt="";ledger.Records["work:first"]=work;forecasts.Records["first"]["status"]="active";forecasts.Records["first"]["kind"]="wrong"
 log:=[]byte(`{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"report","id":"current","fields":{"next_focus":"not-published"}}]}}}`+"\n")
 prepared,err:=prepareBaseline(baselineNativeFixture(t,linkedReport),log,map[string]baselineMode{"1/u/0":baselinePending});require.NoError(t,err)
 dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),log,0600));captured,err:=captureReplayLog(dir);require.NoError(t,err)
 result,err:=projectDBReportResult(dir,"report_create",projectionSnapshot{Report:prepared,ReportPresent:true,Sources:reconciliationSnapshot{ledgerPresent:true,forecastPresent:true,ledger:ledger,forecasts:forecasts}},sparseEvaluation,captured,true)
 require.Error(t,err);require.Empty(t,result.Document);require.Empty(t,result.Receipts)
}

// TestBaselineProjectionReceiptRefusesOvertakenCleanup uses genuine earlier projection receipts.
//
// Example: Requeued older work disappears without falsely becoming projected again.
func TestBaselineProjectionReceiptRefusesOvertakenCleanup(t *testing.T) {
 t.Parallel()
 for _,timeline:=range []bool{false,true} {t.Run(fmt.Sprint(timeline),
  // The callback requeues an old canonical occurrence after a real newer projection.
  //
  // Example: The actual Latest or TimelineAppliedLine remains unchanged.
  func(t *testing.T) {
   log:=`{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"old"}}]}}}`+"\n"+
    `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"new"}}]}}}`+"\n"
   choices:=map[string]baselineMode{"1/u/0":baselinePending,"2/u/0":baselinePending}
   native:=baselineNativeFixture(t,linkedReport)
   if timeline {
    log=baselineTimelineEvent(t,"old")+baselineTimelineEvent(t,"new")
    choices=map[string]baselineMode{"1/t/0":baselinePending,"2/t/0":baselinePending}
    native=baselineNativeFixture(t,linkedReport+"timeline_as_of: '2026-11-02T15:24:00Z'\ntimeline_state: verified_empty\ntimeline: []\n")
   }
   prepared,err:=prepareBaseline(native,[]byte(log),choices);require.NoError(t,err)
   dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(log),0600))
   projection,err:=projectLogStatusResult(dir,prepared,ledgerSource{},forecastSource{},true);require.NoError(t,err);projected:=projection.Document
   decoded,err:=decodeReportValue(projected);require.NoError(t,err);root:=decoded.(map[string]any);raw,err:=json.Marshal(root["status_sync"]);require.NoError(t,err);var state syncState;require.NoError(t,json.Unmarshal(raw,&state))
   locator:=syncOperationLocator{Kind:syncOperationUpdate,Line:1,Ordinal:0}
   if timeline {locator.Kind=syncOperationTimeline}
   occurrence,err:=resolveBaselineOperation([]byte(log),locator);require.NoError(t,err)
   if timeline {
    require.Equal(t,2,state.TimelineAppliedLine);encoded,err:=json.Marshal(occurrence.Timeline);require.NoError(t,err)
    if state.PendingTimelines==nil {state.PendingTimelines=map[string]syncPendingTimeline{}}
    state.PendingTimelines["1"]=syncPendingTimeline{Line:1,Snapshot:string(encoded)}
   } else {
    require.Equal(t,2,state.Latest[syncTargetKey(*occurrence.Update,"owner")]);encoded,err:=encodeSyncGroup(*occurrence.Update);require.NoError(t,err)
    if state.Groups==nil {state.Groups=map[string]syncPendingGroup{}}
    state.Groups[syncGroupKey(1,0)]=syncPendingGroup{Line:1,Ordinal:0,Update:encoded}
   }
   root["status_sync"]=state;requeued,err:=json.Marshal(root);require.NoError(t,err)
   retry,err:=projectLogStatusResult(dir,requeued,ledgerSource{},forecastSource{},true);require.NoError(t,err);result,receipts:=retry.Document,retry.Receipts
   parsed,err:=parseDBReport(result);require.NoError(t,err);require.NotContains(t,receipts,locator)
   if timeline {require.Empty(t,parsed.StatusSync.PendingTimelines);require.Equal(t,2,parsed.StatusSync.TimelineAppliedLine);require.Equal(t,"new",parsed.Timeline[0].Normal)} else {require.Empty(t,parsed.StatusSync.Groups);require.Equal(t,"new",parsed.Tasks[0].Owner)}
  })}
}
