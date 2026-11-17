package main

import (
 "context"
 "database/sql"
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "strings"
 "testing"

 "github.com/stretchr/testify/require"
 "gopkg.in/yaml.v3"
 "time"
)

// TestBaselineRetiredPredecessorReleaseCompletesPending checks post-publication dependency release.
//
// Example: A retired parent can be explicitly authorized for a queued correction or retraction.
func TestBaselineRetiredPredecessorReleaseCompletesPending(t *testing.T) {
 t.Parallel()
 for _,testcase:=range []struct{retract bool;ordinal int}{{false,0},{false,1},{true,0},{true,1}} {t.Run(fmt.Sprintf("retract=%t/ordinal=%d",testcase.retract,testcase.ordinal),func(t *testing.T){retract:=testcase.retract
  report:=strings.Replace(linkedReport,"status: IN PROGRESS, work_refs:","status: IN PROGRESS, owner: B, work_refs:",1)
  first:=`{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"A"}}]}}}`+"\n"
  second:=`{"event":"correction","details":{"status_update":{"updates":[{"target":"task","id":"1","corrects":1,"fields":{"owner":"C"}}]}}}`+"\n"
  selector:=syncUpdate{Target:syncTargetTask,ID:"1"};expected:=map[string]syncOwnership{"owner":scalarSyncStoredValue("B")}
  if retract {report=strings.Replace(linkedReport,"progress: [Implementation started]","progress: [{subject_ref: n, text: B, evidence: eB}]",1);first=`{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"progress","id":"n","fields":{"text":"A","evidence":"eA"}}]}}}`+"\n";second=`{"event":"correction","details":{"status_update":{"updates":[{"target":"progress","id":"n","corrects":1,"retract":true}]}}}`+"\n";selector=syncUpdate{Target:syncTargetProgress,ID:"n"};expected=map[string]syncOwnership{"text":scalarSyncStoredValue("B"),"evidence":scalarSyncStoredValue("eB")}}
  decisions:=map[string]baselineMode{"1/u/0":baselineRetire}
  if testcase.ordinal==1 {first=strings.Replace(first,`"updates":[`,`"updates":[{"target":"report","id":"current","fields":{"state":"Working"}},`,1);second=strings.Replace(second,`"corrects":1`,`"corrects":1,"corrects_ordinal":1`,1);decisions=map[string]baselineMode{"1/u/0":baselineAdopt,"1/u/1":baselineRetire}}
  prepared,err:=prepareBaseline(baselineNativeFixture(t,report),[]byte(first),decisions);require.NoError(t,err)
  log:=first+second+`{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"report","id":"current","fields":{"next_focus":"independent"}}]}}}`+"\n"
  dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(log),0600));held,_,err:=projectLogStatus(dir,baselineReload(t,prepared),ledgerSource{},forecastSource{});require.NoError(t,err);parsed,err:=parseDBReport(held);require.NoError(t,err);require.Equal(t,"independent",parsed.NextFocus);require.Len(t,parsed.StatusSync.Groups,1)
  nativeHeld,err:=replayYAMLToReportJSON(held);require.NoError(t,err)
  request:=baselineReconciliation{Action:baselineResume,Pending:syncOperationLocator{Kind:syncOperationUpdate,Line:2,Ordinal:0},Predecessor:&syncOperationLocator{Kind:syncOperationUpdate,Line:1,Ordinal:testcase.ordinal},Expected:map[string]reportEditExpectation{}}
  for field,ownership:=range expected {raw,err:=json.Marshal(ownership.Value);require.NoError(t,err);request.Expected[field]=reportEditExpectation{Kind:"value",Value:raw}}
  bad:=cloneBaselineReconciliation(t,request);bad.Predecessor.Ordinal=1-testcase.ordinal;_,err=reconcileBaselinePending(nativeHeld,[]byte(log),bad);require.Error(t,err)
  bad=cloneBaselineReconciliation(t,request);bad.Pending.Ordinal=1;_,err=reconcileBaselinePending(nativeHeld,[]byte(log),bad);require.Error(t,err)
  bad=cloneBaselineReconciliation(t,request);bad.Predecessor=nil;_,err=reconcileBaselinePending(nativeHeld,[]byte(log),bad);require.Error(t,err)
  bad=cloneBaselineReconciliation(t,request);bad.Predecessor.Kind=syncOperationAddition;_,err=reconcileBaselinePending(nativeHeld,[]byte(log),bad);require.Error(t,err)
  bad=cloneBaselineReconciliation(t,request);for field:=range bad.Expected {bad.Expected[field]=reportEditExpectation{Kind:"value",Value:json.RawMessage(`"wrong"`)};break};_,err=reconcileBaselinePending(nativeHeld,[]byte(log),bad);require.Error(t,err)
  wrongLog:=strings.Replace(log,`"corrects":1`,`"corrects":9`,1);_,err=reconcileBaselinePending(nativeHeld,[]byte(wrongLog),request);require.Error(t,err)
  released,err:=reconcileBaselinePending(nativeHeld,[]byte(log),cloneBaselineReconciliation(t,request));require.NoError(t,err)
  var authorization struct{State syncState `yaml:"status_sync"`};require.NoError(t,yaml.Unmarshal(released,&authorization));for field:=range expected {fieldKey:=syncTargetKey(selector,field);require.Zero(t,authorization.State.Latest[fieldKey]);if !retract {line,ordinal:=syncVersionForCorrection(authorization.State,fieldKey);require.Equal(t,1,line);require.Equal(t,testcase.ordinal,ordinal)}}
  if retract {version:=authorization.State.Groups[syncGroupKey(2,0)].RetractionVersion;require.NotNil(t,version);require.Equal(t,1,version.Line);require.Equal(t,testcase.ordinal,version.Ordinal)}
  db,err:=sql.Open("sqlite",filepath.Join(dir,"reconcile.db"));require.NoError(t,err);defer func(){require.NoError(t,db.Close())}()
  captured,err:=captureReplayLog(dir);require.NoError(t,err);require.NoError(t,publishBaseline(context.Background(),db,dir,baselineObservation{Initial:true,DatabaseID:"native-proof",Generation:0},nativeHeld,captured))
  token:=baselineObservation{DatabaseID:"native-proof",Generation:1}
  require.Error(t,publishBaselineReconciliation(context.Background(),db,dir,baselineObservation{DatabaseID:"wrong",Generation:1},captured,request))
  require.Error(t,publishBaselineReconciliation(context.Background(),db,dir,baselineObservation{DatabaseID:"native-proof",Generation:0},captured,request))
  require.Error(t,publishBaselineReconciliation(context.Background(),db,dir,token,captured,bad))
  logFile,err:=os.OpenFile(filepath.Join(dir,"high_level_log.jsonl"),os.O_WRONLY|os.O_APPEND,0600);require.NoError(t,err);_,err=logFile.WriteString("{}\n");require.NoError(t,err);require.NoError(t,logFile.Close())
  require.Error(t,publishBaselineReconciliation(context.Background(),db,dir,token,captured,request))
  var saved string;var generation,revision int;require.NoError(t,db.QueryRow("SELECT document_json,revision FROM report WHERE id=1").Scan(&saved,&revision));require.JSONEq(t,string(nativeHeld),saved);require.Equal(t,1,revision);require.NoError(t,db.QueryRow("SELECT generation FROM ledger_meta WHERE id=1").Scan(&generation));require.Equal(t,1,generation)
  captured,err=captureReplayLog(dir);require.NoError(t,err);require.NoError(t,publishBaselineReconciliation(context.Background(),db,dir,token,captured,request));require.NoError(t,db.QueryRow("SELECT document_json,revision FROM report WHERE id=1").Scan(&saved,&revision));require.Equal(t,2,revision);released=[]byte(saved)
  finished,_,err:=projectLogStatus(dir,baselineReload(t,released),ledgerSource{},forecastSource{});require.NoError(t,err);parsed,err=parseDBReport(finished);require.NoError(t,err);require.Empty(t,parsed.StatusSync.Groups)
  if retract {root,err:=opaqueReportMap(finished);require.NoError(t,err);require.Empty(t,root["progress"])} else {require.Equal(t,"C",parsed.Tasks[0].Owner)}
 })}
}

// TestBaselineKnownPendingRecordProtectsOwnerChanges checks prepared insertion preconditions.
//
// Example: Removing or changing B never authorizes resurrection of historical A.
func TestBaselineKnownPendingRecordProtectsOwnerChanges(t *testing.T) {
 t.Parallel()
 for _,remove:=range []bool{true,false} {t.Run(map[bool]string{true:"delete",false:"change"}[remove],func(t *testing.T){
  ownerB:=`{"id":"3","title":"Owner-B","priority":"high","impact":"Useful","effort":{"min_minutes":10,"max_minutes":20,"kind":"elapsed","basis":"guess"},"status":"NEW"}`
  native:=baselineNativeFixture(t,linkedReport);value,err:=decodeReportValue(native);require.NoError(t,err);record,err:=decodeReportValue([]byte(ownerB));require.NoError(t,err);root:=value.(map[string]any);root["tasks"]=append(root["tasks"].([]any),record);native,err=json.Marshal(root);require.NoError(t,err)
  log:=baselineTaskEvent("3");prepared,err:=prepareBaseline(native,[]byte(log),map[string]baselineMode{"1/a/0":baselinePending});require.NoError(t,err)
  dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(log),0600));pending,_,err:=projectLogStatus(dir,baselineReload(t,prepared),ledgerSource{},forecastSource{});require.NoError(t,err);parsed,err:=parseDBReport(pending);require.NoError(t,err);require.Len(t,parsed.StatusSync.PendingAdditions,1)
  pendingJSON,err:=replayYAMLToReportJSON(pending);require.NoError(t,err)
  edit:=reportEdit{Ops:[]reportEditOperation{{Op:"set",Path:[]string{"tasks","2","title"},Expect:reportEditExpectation{Kind:"value",Value:json.RawMessage(`"Owner-B"`)},Value:json.RawMessage(`"Owner-C"`)}}}
  if remove {edit=reportEdit{Ops:[]reportEditOperation{{Op:"delete",Path:[]string{"tasks","2"},Expect:reportEditExpectation{Kind:"value",Value:json.RawMessage(ownerB)}}}}}
  edited,err:=applyReportEdit(pendingJSON,edit);require.NoError(t,err)
  next:=log+`{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"report","id":"current","fields":{"next_focus":"independent"}}]}}}`+"\n";require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(next),0600))
  finished,_,err:=projectLogStatus(dir,baselineReload(t,edited),ledgerSource{},forecastSource{});require.NoError(t,err);parsed,err=parseDBReport(finished);require.NoError(t,err);require.Equal(t,"independent",parsed.NextFocus);require.Len(t,parsed.StatusSync.PendingAdditions,1)
  if remove {require.Len(t,parsed.Tasks,2)} else {require.Equal(t,"Owner-C",parsed.Tasks[2].Title)}
  nativeFinished,err:=replayYAMLToReportJSON(finished);require.NoError(t,err)
  request:=baselineReconciliation{Action:baselineResume,Pending:syncOperationLocator{Kind:syncOperationAddition,Line:1,Ordinal:0},Expected:map[string]reportEditExpectation{"record":{Kind:"absent"}}}
  bad:=cloneBaselineReconciliation(t,request);bad.Expected["record"]=reportEditExpectation{Kind:"value",Value:json.RawMessage(ownerB)};_,err=reconcileBaselinePending(nativeFinished,[]byte(next),bad);require.Error(t,err)
  bad=cloneBaselineReconciliation(t,request);bad.Pending.Ordinal=1;_,err=reconcileBaselinePending(nativeFinished,[]byte(next),bad);require.Error(t,err)
  if !remove {current,err:=opaqueReportMap(nativeFinished);require.NoError(t,err);raw,err:=json.Marshal(current["tasks"].([]any)[2]);require.NoError(t,err);request.Action=baselineRetirePending;request.Expected["record"]=reportEditExpectation{Kind:"value",Value:raw}}
  authorized,err:=reconcileBaselinePending(nativeFinished,[]byte(next),request);require.NoError(t,err);done,_,err:=projectLogStatus(dir,baselineReload(t,authorized),ledgerSource{},forecastSource{});require.NoError(t,err);parsed,err=parseDBReport(done);require.NoError(t,err);require.Empty(t,parsed.StatusSync.PendingAdditions);require.Equal(t,"independent",parsed.NextFocus)
  if remove {require.Len(t,parsed.Tasks,3);require.Equal(t,"New",parsed.Tasks[2].Title)} else {require.Equal(t,"Owner-C",parsed.Tasks[2].Title)}
 })}
}

// cloneBaselineReconciliation exercises the typed external request wire format without shared maps.
//
// Example: A negative request mutation cannot alter the later positive authorization.
func cloneBaselineReconciliation(t *testing.T,request baselineReconciliation) baselineReconciliation {
 t.Helper();raw,err:=json.Marshal(request);require.NoError(t,err);var cloned baselineReconciliation;require.NoError(t,json.Unmarshal(raw,&cloned));return cloned
}

// TestBaselinePreparedRecordGuardTracksOnlyVerifiedProjection checks source-owned guard advancement.
//
// Example: Canonical forecast edits advance B's record expectation, but later owner deletion never does.
func TestBaselinePreparedRecordGuardTracksOnlyVerifiedProjection(t *testing.T) {
 t.Parallel()
 native:=baselineNativeFixture(t,linkedReport);value,err:=decodeReportValue(native);require.NoError(t,err);root:=value.(map[string]any);lane:=root["lanes"].([]any)[0].(map[string]any);lane["owner"]="B";lane["owner_note"]="keep";native,err=json.Marshal(root);require.NoError(t,err)
 old,err:=decodeReportValue(native);require.NoError(t,err);additionRecord:=old.(map[string]any)["lanes"].([]any)[0].(map[string]any);additionRecord["owner"]="A";recordBytes,err:=json.Marshal(additionRecord);require.NoError(t,err)
 log:=[]byte(`{"event":"requirement_change","details":{"status_update":{"additions":[{"target":"lane","record":`+string(recordBytes)+`}]}}}`+"\n")
 prepared,err:=prepareBaseline(native,log,map[string]baselineMode{"1/a/0":baselinePending});require.NoError(t,err);dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),log,0600));captured,err:=captureReplayLog(dir);require.NoError(t,err)
 var originalMetadata struct{State syncState `yaml:"status_sync"`};require.NoError(t,yaml.Unmarshal(prepared,&originalMetadata));originalGuard:=*originalMetadata.State.PendingAdditions[syncGroupKey(1,0)].RecordGuard
 var ledger ledgerSource;var forecasts forecastSource;require.NoError(t,yaml.Unmarshal([]byte(linkedLedger),&ledger));require.NoError(t,yaml.Unmarshal([]byte(linkedForecasts),&forecasts));snapshot:=projectionSnapshot{Report:baselineReload(t,prepared),Sources:reconciliationSnapshot{ledger:ledger,ledgerPresent:true,forecasts:forecasts,forecastPresent:true}};evaluation:=time.Date(2026,11,2,15,24,0,0,time.UTC)
 projected,err:=projectDBReport(dir,"requirement_change",snapshot,evaluation,captured);require.NoError(t,err);var metadata struct{State syncState `yaml:"status_sync"`};require.NoError(t,yaml.Unmarshal(projected,&metadata));pending:=metadata.State.PendingAdditions[syncGroupKey(1,0)];require.NotNil(t,pending.RecordGuard)
 projectedValue,err:=decodeReportValue(projected);require.NoError(t,err);selector:=syncUpdate{Target:syncTargetLane,ID:"1",WorkRef:"project-understanding.yaml#/records/work:first",RequiredEnvironment:syncEnvironmentImplementation};expected,err:=baselineCurrent(projectedValue.(map[string]any),selector,"record");require.NoError(t,err);require.Equal(t,expected,*pending.RecordGuard)
 require.NotEqual(t,originalGuard,*pending.RecordGuard)
 projectedLane:=projectedValue.(map[string]any)["lanes"].([]any)[0];raw,err:=json.Marshal(projectedLane);require.NoError(t,err);edited,err:=applyReportEdit(projected,reportEdit{Ops:[]reportEditOperation{{Op:"delete",Path:[]string{"lanes","0"},Expect:reportEditExpectation{Kind:"value",Value:raw}}}});require.NoError(t,err)
 next:=append(append([]byte{},log...),[]byte(`{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"report","id":"current","fields":{"next_focus":"independent-guard"}}]}}}`+"\n")...);require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),next,0600));captured,err=captureReplayLog(dir);require.NoError(t,err);snapshot.Report=baselineReload(t,edited)
 protected,err:=projectDBReport(dir,"worker_progress",snapshot,evaluation,captured);require.NoError(t,err);protectedYAML,err:=reportJSONToReplayYAML(protected);require.NoError(t,err);parsed,err:=parseDBReport(protectedYAML);require.NoError(t,err);require.Len(t,parsed.Lanes,1);require.Equal(t,"independent-guard",parsed.NextFocus);require.Len(t,parsed.StatusSync.PendingAdditions,1);require.Equal(t,expected,*parsed.StatusSync.PendingAdditions[syncGroupKey(1,0)].RecordGuard)
 request:=baselineReconciliation{Action:baselineResume,Pending:syncOperationLocator{Kind:syncOperationAddition,Line:1,Ordinal:0},Expected:map[string]reportEditExpectation{"record":{Kind:"absent"}}};snapshot.Report,err=reconcileBaselinePending(protected,next,request);require.NoError(t,err);finished,err:=projectDBReport(dir,"worker_progress",snapshot,evaluation,captured);require.NoError(t,err);finishedYAML,err:=reportJSONToReplayYAML(finished);require.NoError(t,err);parsed,err=parseDBReport(finishedYAML);require.NoError(t,err);require.Len(t,parsed.Lanes,2);require.Empty(t,parsed.StatusSync.PendingAdditions)
}
