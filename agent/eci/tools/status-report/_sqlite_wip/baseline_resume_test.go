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

// baselineUpdateEvent preserves actual update ordinals through the canonical event decoder.
//
// Example: A subject update at ordinal one follows an unrelated sibling at ordinal zero.
func baselineUpdateEvent(t *testing.T,event string,updates ...syncUpdate) string {
 t.Helper();value:=syncEvent{Event:event};value.Details.StatusUpdate=&syncEventStatusUpdate{Updates:updates};raw,err:=json.Marshal(value);require.NoError(t,err);return string(raw)+"\n"
}

// TestBaselineResumeRebasesDivergedCurrentGuard checks all non-Held guard variants.
//
// Example: Exact owner D authorization replaces the selected stale B guard, not another subject's guard.
func TestBaselineResumeRebasesDivergedCurrentGuard(t *testing.T) {
 t.Parallel()
 for _,kind:=range []string{"value","null","absent"} {for _,correct:=range []bool{false,true} {for _,ordinal:=range []int{0,1} {t.Run(fmt.Sprintf("%s/correct=%t/ordinal=%d",kind,correct,ordinal),func(t *testing.T){
  native:=baselineNativeFixture(t,linkedReport);value,err:=decodeReportValue(native);require.NoError(t,err);root:=value.(map[string]any);tasks:=root["tasks"].([]any);firstTask:=tasks[0].(map[string]any);tasks[1].(map[string]any)["owner"]="B2";expect:=reportEditExpectation{Kind:kind}
  switch kind {case "value":firstTask["owner"]="B1";expect.Value=json.RawMessage(`"B1"`);case "null":firstTask["owner"]=nil;case "absent":delete(firstTask,"owner")}
  native,err=json.Marshal(root);require.NoError(t,err)
  selected:=syncUpdate{Target:syncTargetTask,ID:"1",Fields:map[string]string{"owner":"A"}};other:=syncUpdate{Target:syncTargetTask,ID:"2",Fields:map[string]string{"owner":"unrelated-A2"}};updates:=[]syncUpdate{selected,other};if ordinal==1 {updates=[]syncUpdate{other,selected}}
  first:=baselineUpdateEvent(t,"assignment_change",updates...);decisions:=map[string]baselineMode{"1/u/0":baselinePending,"1/u/1":baselinePending};if correct {decisions[fmt.Sprintf("1/u/%d",ordinal)]=baselineAdopt}
  prepared,err:=prepareBaseline(native,[]byte(first),decisions);require.NoError(t,err);prepared=baselineReload(t,prepared)
  edited,err:=applyReportEdit(prepared,reportEdit{Ops:[]reportEditOperation{{Op:"set",Path:[]string{"tasks","0","owner"},Expect:expect,Value:json.RawMessage(`"D1"`)},{Op:"set",Path:[]string{"tasks","1","owner"},Expect:reportEditExpectation{Kind:"value",Value:json.RawMessage(`"B2"`)},Value:json.RawMessage(`"D2"`)}}});require.NoError(t,err)
  log:=first;pending:=syncOperationLocator{Kind:syncOperationUpdate,Line:1,Ordinal:ordinal};var predecessor *syncOperationLocator;wanted:="A"
  if correct {parentOrdinal:=ordinal;log+=baselineUpdateEvent(t,"correction",syncUpdate{Target:syncTargetTask,ID:"1",Corrects:1,CorrectsOrdinal:&parentOrdinal,Fields:map[string]string{"owner":"C"}});pending=syncOperationLocator{Kind:syncOperationUpdate,Line:2,Ordinal:0};predecessor=&syncOperationLocator{Kind:syncOperationUpdate,Line:1,Ordinal:ordinal};wanted="C"}
  log+=baselineUpdateEvent(t,"worker_progress",syncUpdate{Target:syncTargetReport,ID:"current",Fields:map[string]string{"next_focus":"independent-rebase"}});dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(log),0600))
  awaiting,_,err:=projectLogStatus(dir,baselineReload(t,edited),ledgerSource{},forecastSource{});require.NoError(t,err);parsed,err:=parseDBReport(awaiting);require.NoError(t,err);require.Equal(t,"D1",parsed.Tasks[0].Owner);require.Equal(t,"D2",parsed.Tasks[1].Owner);require.Equal(t,"independent-rebase",parsed.NextFocus);require.Len(t,parsed.StatusSync.Groups,2)
  request:=baselineReconciliation{Action:baselineResume,Pending:pending,Predecessor:predecessor,Expected:map[string]reportEditExpectation{"owner":{Kind:"value",Value:json.RawMessage(`"D1"`)}}};awaitingJSON,err:=replayYAMLToReportJSON(awaiting);require.NoError(t,err)
  bad:=cloneBaselineReconciliation(t,request);bad.Expected["owner"]=reportEditExpectation{Kind:"value",Value:json.RawMessage(`"B1"`)};_,err=reconcileBaselinePending(awaitingJSON,[]byte(log),bad);require.Error(t,err)
  bad=cloneBaselineReconciliation(t,request);bad.Expected["owner"]=reportEditExpectation{Kind:"absent"};_,err=reconcileBaselinePending(awaitingJSON,[]byte(log),bad);require.Error(t,err)
  bad=cloneBaselineReconciliation(t,request);bad.Pending.Ordinal=3;_,err=reconcileBaselinePending(awaitingJSON,[]byte(log),bad);require.Error(t,err)
  authorized,err:=reconcileBaselinePending(awaitingJSON,[]byte(log),cloneBaselineReconciliation(t,request));require.NoError(t,err);authorized=baselineReload(t,authorized);var saved struct{State syncState `yaml:"status_sync"`};require.NoError(t,yaml.Unmarshal(authorized,&saved));fieldKey:=syncTargetKey(selected,"owner");require.Zero(t,saved.State.Latest[fieldKey]);require.Equal(t,scalarSyncStoredValue("B2"),saved.State.Applied[syncTargetKey(other,"owner")])
  if kind=="value"&&correct&&ordinal==1 {
   changed,err:=applyReportEdit(authorized,reportEdit{Ops:[]reportEditOperation{{Op:"set",Path:[]string{"tasks","0","owner"},Expect:reportEditExpectation{Kind:"value",Value:json.RawMessage(`"D1"`)},Value:json.RawMessage(`"E1"`)}}});require.NoError(t,err)
   guarded,_,err:=projectLogStatus(dir,baselineReload(t,changed),ledgerSource{},forecastSource{});require.NoError(t,err);guardedReport,err:=parseDBReport(guarded);require.NoError(t,err);require.Equal(t,"E1",guardedReport.Tasks[0].Owner);require.Len(t,guardedReport.StatusSync.Groups,2);require.Zero(t,guardedReport.StatusSync.Latest[fieldKey])
   guardedJSON,err:=replayYAMLToReportJSON(guarded);require.NoError(t,err);_,err=reconcileBaselinePending(guardedJSON,[]byte(log),request);require.Error(t,err);request.Expected["owner"]=reportEditExpectation{Kind:"value",Value:json.RawMessage(`"E1"`)};authorized,err=reconcileBaselinePending(guardedJSON,[]byte(log),cloneBaselineReconciliation(t,request));require.NoError(t,err);authorized=baselineReload(t,authorized)
  }
  finished,_,err:=projectLogStatus(dir,authorized,ledgerSource{},forecastSource{});require.NoError(t,err);parsed,err=parseDBReport(finished);require.NoError(t,err);require.Equal(t,wanted,parsed.Tasks[0].Owner);require.Equal(t,"D2",parsed.Tasks[1].Owner);require.Equal(t,"independent-rebase",parsed.NextFocus);require.Len(t,parsed.StatusSync.Groups,1);require.NotContains(t,parsed.StatusSync.Groups,syncGroupKey(pending.Line,pending.Ordinal))
 })}}}
}

// TestBaselineRetractionUsesSubjectPredecessor checks partial-field narrative history.
//
// Example: A latest evidence-only version names the subject, while current text and evidence are both guarded.
func TestBaselineRetractionUsesSubjectPredecessor(t *testing.T) {
 t.Parallel()
 for _,shape:=range []string{"text-only","split-text-latest","split-evidence-latest"} {for _,ordinal:=range []int{0,1} {t.Run(fmt.Sprintf("%s/ordinal=%d",shape,ordinal),func(t *testing.T){
  native:=baselineNativeFixture(t,linkedReport);value,err:=decodeReportValue(native);require.NoError(t,err);root:=value.(map[string]any);subject:=map[string]any{"subject_ref":"n","text":"B"};expected:=map[string]reportEditExpectation{"text":{Kind:"value",Value:json.RawMessage(`"B"`)},"evidence":{Kind:"absent"}}
  if shape!="text-only" {subject["evidence"]="eB";expected["evidence"]=reportEditExpectation{Kind:"value",Value:json.RawMessage(`"eB"`)}}
  root["progress"]=[]any{subject,"keep unrelated"};native,err=json.Marshal(root);require.NoError(t,err)
  text:=syncUpdate{Target:syncTargetProgress,ID:"n",Fields:map[string]string{"text":"A"}};evidence:=syncUpdate{Target:syncTargetProgress,ID:"n",Fields:map[string]string{"evidence":"eA"}};last:=text;log:="";parentLine:=1;decisions:=map[string]baselineMode{}
  if shape!="text-only" {prior:=evidence;if shape=="split-evidence-latest" {prior=text;last=evidence};log=baselineUpdateEvent(t,"worker_progress",prior);decisions["1/u/0"]=baselineRetire;parentLine=2}
  updates:=[]syncUpdate{last};if ordinal==1 {updates=[]syncUpdate{{Target:syncTargetReport,ID:"current",Fields:map[string]string{"state":"Working"}},last};decisions[fmt.Sprintf("%d/u/0",parentLine)]=baselineAdopt};decisions[fmt.Sprintf("%d/u/%d",parentLine,ordinal)]=baselineRetire;log+=baselineUpdateEvent(t,"worker_progress",updates...)
  prepared,err:=prepareBaseline(native,[]byte(log),decisions);require.NoError(t,err);parentOrdinal:=ordinal;pendingLine:=parentLine+1;log+=baselineUpdateEvent(t,"correction",syncUpdate{Target:syncTargetProgress,ID:"n",Corrects:parentLine,CorrectsOrdinal:&parentOrdinal,Retract:true});log+=baselineUpdateEvent(t,"worker_progress",syncUpdate{Target:syncTargetReport,ID:"current",Fields:map[string]string{"next_focus":"independent-subject"}})
  dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(log),0600));awaiting,_,err:=projectLogStatus(dir,baselineReload(t,prepared),ledgerSource{},forecastSource{});require.NoError(t,err);parsed,err:=parseDBReport(awaiting);require.NoError(t,err);require.Equal(t,"independent-subject",parsed.NextFocus);require.Len(t,parsed.StatusSync.Groups,1);awaitingJSON,err:=replayYAMLToReportJSON(awaiting);require.NoError(t,err)
  if shape=="text-only" {current,err:=opaqueReportMap(awaitingJSON);require.NoError(t,err);_,present:=current["progress"].([]any)[0].(map[string]any)["evidence"];require.False(t,present)}
  request:=baselineReconciliation{Action:baselineResume,Pending:syncOperationLocator{Kind:syncOperationUpdate,Line:pendingLine,Ordinal:0},Predecessor:&syncOperationLocator{Kind:syncOperationUpdate,Line:parentLine,Ordinal:ordinal},Expected:expected}
  bad:=cloneBaselineReconciliation(t,request);bad.Predecessor.Ordinal=1-ordinal;_,err=reconcileBaselinePending(awaitingJSON,[]byte(log),bad);require.Error(t,err)
  bad=cloneBaselineReconciliation(t,request);bad.Expected["text"]=reportEditExpectation{Kind:"value",Value:json.RawMessage(`"wrong"`)};_,err=reconcileBaselinePending(awaitingJSON,[]byte(log),bad);require.Error(t,err)
  bad=cloneBaselineReconciliation(t,request);bad.Expected["evidence"]=reportEditExpectation{Kind:"null"};_,err=reconcileBaselinePending(awaitingJSON,[]byte(log),bad);require.Error(t,err)
  bad=cloneBaselineReconciliation(t,request);delete(bad.Expected,"evidence");_,err=reconcileBaselinePending(awaitingJSON,[]byte(log),bad);require.Error(t,err)
  authorized,err:=reconcileBaselinePending(awaitingJSON,[]byte(log),cloneBaselineReconciliation(t,request));require.NoError(t,err);authorized=baselineReload(t,authorized);var saved struct{State syncState `yaml:"status_sync"`};require.NoError(t,yaml.Unmarshal(authorized,&saved));for _,field:=range []string{"text","evidence"} {fieldKey:=syncTargetKey(last,field);require.Zero(t,saved.State.Latest[fieldKey]);require.Zero(t,saved.State.BaselineAdopted[fieldKey].Line)}
  version:=saved.State.Groups[syncGroupKey(pendingLine,0)].RetractionVersion;require.NotNil(t,version);require.Equal(t,parentLine,version.Line);require.Equal(t,ordinal,version.Ordinal)
  if shape=="text-only"&&ordinal==1 {
   changed,err:=applyReportEdit(authorized,reportEdit{Ops:[]reportEditOperation{{Op:"set",Path:[]string{"progress","0","evidence"},Expect:reportEditExpectation{Kind:"absent"},Value:json.RawMessage(`"late-owner-evidence"`)}}});require.NoError(t,err)
   guarded,_,err:=projectLogStatus(dir,baselineReload(t,changed),ledgerSource{},forecastSource{});require.NoError(t,err);guardedReport,err:=parseDBReport(guarded);require.NoError(t,err);require.Len(t,guardedReport.StatusSync.Groups,1);guardedRoot,err:=opaqueReportMap(guarded);require.NoError(t,err);require.Equal(t,"late-owner-evidence",guardedRoot["progress"].([]any)[0].(map[string]any)["evidence"])
   guardedJSON,err:=replayYAMLToReportJSON(guarded);require.NoError(t,err);_,err=reconcileBaselinePending(guardedJSON,[]byte(log),request);require.Error(t,err);request.Expected["evidence"]=reportEditExpectation{Kind:"value",Value:json.RawMessage(`"late-owner-evidence"`)};authorized,err=reconcileBaselinePending(guardedJSON,[]byte(log),cloneBaselineReconciliation(t,request));require.NoError(t,err);authorized=baselineReload(t,authorized)
  }
  finished,_,err:=projectLogStatus(dir,authorized,ledgerSource{},forecastSource{});require.NoError(t,err);parsed,err=parseDBReport(finished);require.NoError(t,err);require.Empty(t,parsed.StatusSync.Groups);after,err:=opaqueReportMap(finished);require.NoError(t,err);require.Equal(t,[]any{"keep unrelated"},after["progress"]);require.Equal(t,"independent-subject",parsed.NextFocus)
 })}}
}
