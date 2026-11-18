package main

import (
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "testing"

 "github.com/stretchr/testify/require"
 "gopkg.in/yaml.v3"
)

// baselineClosedNativeReport authors current closed state without importing replay metadata.
//
// Example: A pending reopening must not lose the completion while forecast applicability is unknown.
func baselineClosedNativeReport(t *testing.T, cancelled bool) []byte {
 t.Helper()
 native:=baselineNativeFixture(t,linkedReport);decoded,err:=decodeReportValue(native);require.NoError(t,err);root:=decoded.(map[string]any);lane:=root["lanes"].([]any)[0].(map[string]any)
 lane["implementation_status"]="CLOSED";lane["completed_at"]="2026-11-02T15:25:00Z";lane["awaits"]="owner-wait";delete(lane,"forecast")
 if cancelled {lane["lifecycle"]="cancelled";lane["implementation_status"]=statusInProgress.String();delete(lane,"completed_at")}
 root["owner_extension"]=map[string]any{"precise":json.Number("1.10"),"large":json.Number("1e400")}
 native,err=json.Marshal(root);require.NoError(t,err);_,err=parseDBReport(native);require.NoError(t,err);return native
}

// baselineActiveSources supplies known current work and its active forecast.
//
// Example: Delayed reopening uses these sources without another log event.
func baselineActiveSources(t *testing.T) (ledgerSource,forecastSource) {
 t.Helper()
 ledger,forecasts:=baselineSources(t);work:=ledger.Records["work:first"];work.Data.State=workStateActive;work.Data.CompletedAt="";ledger.Records["work:first"]=work;forecasts.Records["first"]["status"]="active"
 return ledger,forecasts
}

// TestBaselineSparseReopeningWaitsForImplicitEvidence falsifies consumption of an unknown restoration.
//
// Example: Neither absent work nor absent forecast evidence permits a half-applied reopening.
func TestBaselineSparseReopeningWaitsForImplicitEvidence(t *testing.T) {
 t.Parallel()
 for _,reactivate:=range []bool{false,true} {for _,missing:=range []string{"ledger","work-record","forecast","forecast-record","forecast-ref","stuck-forecast-ref","roles","unknown-role","state"} {
  t.Run(fmt.Sprintf("reactivate=%t/missing=%s",reactivate,missing),
   // The callback retains one complete operation until every required implicit dependency is available.
   //
   // Example: Source-independent owner and awaits readiness changes still progress on the same lane.
   func(t *testing.T) {
    update:=syncUpdate{Target:syncTargetLane,ID:"1",WorkRef:"project-understanding.yaml#/records/work:first",RequiredEnvironment:syncEnvironmentImplementation,Fields:map[string]string{"implementation_status":"IN PROGRESS"}}
    if reactivate {update.Clear=[]string{"lifecycle"};update.Evidence="reactivated"}
    rawUpdate,err:=json.Marshal(update);require.NoError(t,err)
    log:=[]byte(`{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"report","id":"current","fields":{"next_focus":"independent-reopen"}},`+string(rawUpdate)+`,{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"owner":"ready-owner"},"clear":["awaits"]}]}}}`+"\n")
    authored:=baselineClosedNativeReport(t,reactivate);before:=sparseParsed(t,authored)
    prepared,err:=prepareBaseline(authored,log,map[string]baselineMode{"1/u/0":baselinePending,"1/u/1":baselinePending,"1/u/2":baselinePending});require.NoError(t,err)
    ledger,forecasts:=baselineActiveSources(t);sources:=reconciliationSnapshot{ledgerPresent:true,forecastPresent:true,ledger:ledger,forecasts:forecasts}
    switch missing {
    case "ledger":sources.ledgerPresent=false;sources.ledger=ledgerSource{}
    case "work-record":sources.ledger=ledgerSource{Schema:"project-understanding/v1",Sections:map[string]any{},Records:map[string]sourceRecord{}}
    case "forecast":sources.forecastPresent=false;sources.forecasts=forecastSource{}
    case "forecast-record":sources.forecasts=forecastSource{Schema:"forecasts/v1",Records:map[string]map[string]any{}}
    case "forecast-ref", "stuck-forecast-ref":
     work:=sources.ledger.Records["work:first"];work.Data.ForecastRef="";if missing=="stuck-forecast-ref" {work.Data.State=workStateStuck};sources.ledger.Records["work:first"]=work
    case "roles":
     work:=sources.ledger.Records["work:first"];work.Data.Roles=nil;sources.ledger.Records["work:first"]=work
    case "unknown-role":
     work:=sources.ledger.Records["work:first"];work.Data.Roles=[]reportingRole{reportingRoleUnknown};sources.ledger.Records["work:first"]=work
    case "state":
     work:=sources.ledger.Records["work:first"];work.Data.State=workStateUnknown;sources.ledger.Records["work:first"]=work
    }
    dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),log,0600));captured,err:=captureReplayLog(dir);require.NoError(t,err)
    result,err:=projectDBReportResult(dir,"report_create",projectionSnapshot{Report:baselineReload(t,prepared),ReportPresent:true,Sources:sources},sparseEvaluation,captured,true);require.NoError(t,err)
    receipts:=result.Receipts;document:=result.Document;parsed:=sparseParsed(t,document);require.Equal(t,before.Lanes[0].ImplementationStatus,parsed.Lanes[0].ImplementationStatus);require.Equal(t,before.Lanes[0].CompletedAt,parsed.Lanes[0].CompletedAt);require.Nil(t,parsed.Lanes[0].Forecast)
    require.Equal(t,"independent-reopen",parsed.NextFocus);require.Equal(t,"ready-owner",parsed.Lanes[0].Owner);require.False(t,*parsed.ScopeComplete)
    decoded,err:=decodeReportValue(document);require.NoError(t,err);lane:=decoded.(map[string]any)["lanes"].([]any)[0].(map[string]any);require.NotContains(t,lane,"awaits")
    if reactivate {require.Equal(t,"cancelled",lane["lifecycle"])}
    key:=syncGroupKey(1,1);pending,present:=parsed.StatusSync.Groups[key];require.True(t,present);require.Equal(t,1,pending.Line);require.Equal(t,1,pending.Ordinal)
    stored,err:=decodeSyncGroup(pending.Update);require.NoError(t,err);same,err:=sameBaselineOperation(stored,update);require.NoError(t,err);require.True(t,same)
    wanted:=syncOperationLocator{Kind:syncOperationUpdate,Line:1,Ordinal:1};require.NotContains(t,receipts,wanted)
    require.Contains(t,receipts,syncOperationLocator{Kind:syncOperationUpdate,Line:1,Ordinal:2})
    // Current sources arrive; normal replay retries the same operation without reappend.
    arrivedLedger,arrivedForecasts:=baselineActiveSources(t)
    if missing=="stuck-forecast-ref" {work:=arrivedLedger.Records["work:first"];work.Data.State=workStateStuck;arrivedLedger.Records["work:first"]=work}
    arrived:=reconciliationSnapshot{ledgerPresent:true,forecastPresent:true,ledger:arrivedLedger,forecasts:arrivedForecasts}
    retried,err:=projectDBReportResult(dir,"worker_progress",projectionSnapshot{Report:baselineReload(t,document),ReportPresent:true,Sources:arrived},sparseEvaluation,captured,true);require.NoError(t,err)
    complete:=sparseParsed(t,retried.Document);receipts=retried.Receipts;require.Equal(t,statusInProgress,complete.Lanes[0].ImplementationStatus);require.Empty(t,complete.Lanes[0].CompletedAt);require.NotNil(t,complete.Lanes[0].Forecast);require.Equal(t,"First result",complete.Lanes[0].Forecast.Outcome);require.NotContains(t,complete.StatusSync.Groups,key);require.Contains(t,receipts,wanted)
    require.Equal(t,"ready-owner",complete.Lanes[0].Owner);require.Equal(t,"independent-reopen",complete.NextFocus)
    // Owner divergence after the pending observation must not be silently rebased on source arrival.
    ownerEdit:=reportEditOperation{Op:"set",Path:[]string{"lanes","0","completed_at"},Expect:reportEditExpectation{Kind:"value",Value:json.RawMessage(`"2026-11-02T15:25:00Z"`)},Value:json.RawMessage(`"2026-11-02T15:25:30Z"`)}
    if reactivate {ownerEdit=reportEditOperation{Op:"delete",Path:[]string{"lanes","0","lifecycle"},Expect:reportEditExpectation{Kind:"value",Value:json.RawMessage(`"cancelled"`)}}}
    changed,err:=applyReportEdit(document,reportEdit{Ops:[]reportEditOperation{ownerEdit}});require.NoError(t,err)
    protected,err:=projectDBReportResult(dir,"worker_progress",projectionSnapshot{Report:baselineReload(t,changed),ReportPresent:true,Sources:arrived},sparseEvaluation,captured,true);require.NoError(t,err)
    owner:=sparseParsed(t,protected.Document);receipts=protected.Receipts;require.Equal(t,before.Lanes[0].ImplementationStatus,owner.Lanes[0].ImplementationStatus);require.Nil(t,owner.Lanes[0].Forecast);require.Contains(t,owner.StatusSync.Groups,key);require.NotContains(t,receipts,wanted)
    if !reactivate {require.Equal(t,"2026-11-02T15:25:30Z",owner.Lanes[0].CompletedAt)}
    raw,err:=decodeReportValue(protected.Document);require.NoError(t,err);extension:=raw.(map[string]any)["owner_extension"].(map[string]any);require.Equal(t,json.Number("1.10"),extension["precise"]);require.Equal(t,json.Number("1e400"),extension["large"])
    unchangedLog,err:=os.ReadFile(filepath.Join(dir,"high_level_log.jsonl"));require.NoError(t,err);require.Equal(t,string(log),string(unchangedLog))
   })
 }}
}

// TestBaselineReopeningDoesNotAwaitSparseNonApplicableForecast uses explicit canonical non-applicability.
//
// Example: A known empty role list does not require a lane forecast, regardless of reference spelling.
func TestBaselineReopeningDoesNotAwaitSparseNonApplicableForecast(t *testing.T) {
 t.Parallel()
 for _,referencePresent:=range []bool{false,true} {t.Run(fmt.Sprint(referencePresent),
  // The callback proves reference presence alone cannot create a forecast requirement.
  //
  // Example: Existing readiness updates remain eligible for explicitly non-applicable forecasting.
  func(t *testing.T) {
   ledger,forecasts:=baselineActiveSources(t);work:=ledger.Records["work:first"]
   var declared sourceRecord;require.NoError(t,yaml.Unmarshal([]byte(`{"type":"work","data":{"state":"active","reporting_roles":[]}}`),&declared));require.NotNil(t,declared.Data.Roles)
   work.Data.Roles=declared.Data.Roles;if !referencePresent {work.Data.ForecastRef=""};ledger.Records["work:first"]=work
   require.False(t,forecastApplicable(work,reportingRoleLane,false))
   update:=`{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"implementation_status":"IN PROGRESS"}}]}}}`+"\n"
   prepared,err:=prepareBaseline(baselineClosedNativeReport(t,false),[]byte(update),map[string]baselineMode{"1/u/0":baselinePending});require.NoError(t,err)
   dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(update),0600));captured,err:=captureReplayLog(dir);require.NoError(t,err)
   result,err:=projectDBReportResult(dir,"report_create",projectionSnapshot{Report:prepared,ReportPresent:true,Sources:reconciliationSnapshot{ledgerPresent:true,forecastPresent:true,ledger:ledger,forecasts:forecasts}},sparseEvaluation,captured,true);require.NoError(t,err)
   parsed:=sparseParsed(t,result.Document);require.Equal(t,statusInProgress,parsed.Lanes[0].ImplementationStatus);require.Empty(t,parsed.Lanes[0].CompletedAt);require.Nil(t,parsed.Lanes[0].Forecast);require.Empty(t,parsed.StatusSync.Groups);require.Contains(t,result.Receipts,syncOperationLocator{Kind:syncOperationUpdate,Line:1,Ordinal:0})
  })}
}
