package main

import (
 "encoding/json"
 "sort"
)

// syncAdditionSelector resolves identity with the same section and lexical rules as replay.
//
// Example: One task's implementation and test lanes retain different composite selectors.
func syncAdditionSelector(addition syncAddition) (syncUpdate,bool) {
 value,err:=decodeReportValue(addition.Record);if err!=nil{return syncUpdate{},false};record,ok:=value.(map[string]any);if !ok{return syncUpdate{},false}
 _,identity,err:=syncEffectSection(addition.Target);if err!=nil{return syncUpdate{},false};id,ok:=syncPriorityLabelValue(record[identity]);if !ok{return syncUpdate{},false}
 selector:=syncUpdate{Target:addition.Target,ID:id}
 if addition.Target==syncTargetLane {selector.WorkRef,_=record["work_ref"].(string);environment,_:=record["required_environment"].(string);selector.RequiredEnvironment=parseSyncEnvironment(environment);if selector.WorkRef==""||selector.RequiredEnvironment==syncEnvironmentUnknown{return syncUpdate{},false}}
 return selector,true
}

// queueSyncAddition retains one complete domain operation at its exact source occurrence.
//
// Example: Native birth and later ingestion preserve the same root flags and lane selectors.
func queueSyncAddition(state *syncState,line,ordinal int,addition syncAddition) bool {
 if line<1||ordinal<0 {return false}
 if addition.Target!=syncTargetRootCompletion&&addition.Target!=syncTargetRootForecast&&addition.Target!=syncTargetPriorityEstimate&&(addition.RetireForecast||addition.ReopenWorkRef!=""||len(addition.ExpectedPrior)>0||addition.RetirePriority!="") {return false}
 if addition.Target!=syncTargetPriorityEstimate&&addition.RetirePriority!="" {return false}
 if addition.Target==syncTargetPriorityEstimate&&(addition.RetireForecast||addition.ReopenWorkRef!=""||(len(addition.ExpectedPrior)>0&&addition.RetirePriority=="")) {return false}
 switch addition.Target {
 case syncTargetRootCompletion,syncTargetRootForecast:
  return queueRootAddition(state.PendingRootAdditions,line,ordinal,addition)
 case syncTargetTask,syncTargetLane,syncTargetReview,syncTargetPriorityEstimate:
 default:return false
 }
 selector,ok:=syncAdditionSelector(addition);if !ok{return false}
 key:=syncGroupKey(line,ordinal);if _,exists:=state.PendingAdditions[key];exists{return false}
 state.PendingAdditions[key]=syncPendingAddition{Line:line,Ordinal:ordinal,Target:addition.Target,Record:string(addition.Record),Evidence:addition.Evidence,ID:selector.ID,WorkRef:selector.WorkRef,RequiredEnvironment:selector.RequiredEnvironment,RetireForecast:addition.RetireForecast,ReopenWorkRef:addition.ReopenWorkRef,ExpectedPrior:string(addition.ExpectedPrior),RetirePriority:addition.RetirePriority}
 return true
}

// pendingAdditionKeys orders retained operations without collapsing subject identity.
//
// Example: Ordinal one remains behind ordinal zero on the same canonical log line.
func pendingAdditionKeys(pending map[string]syncPendingAddition) []string {
 keys:=make([]string,0,len(pending));for key:=range pending {keys=append(keys,key)}
 sort.Slice(keys,func(i,j int)bool{a,b:=pending[keys[i]],pending[keys[j]];if a.Line!=b.Line{return a.Line<b.Line};return a.Ordinal<b.Ordinal})
 return keys
}

// findPendingAddition locates one same-event dependency without selecting an ambiguous sibling.
//
// Example: Reprioritization finds its explicitly supplied estimate on the same source line.
func findPendingAddition(pending map[string]syncPendingAddition,target syncTarget,id string,line int) (string,syncPendingAddition,bool) {
 var key string;var found syncPendingAddition;count:=0
 for candidate,item:=range pending {if item.Target==target&&item.ID==id&&item.Line==line {key,found=candidate,item;count++}}
 return key,found,count==1
}

// earlierPendingAddition preserves a subject's unresolved occurrence order.
//
// Example: A later retry cannot overtake a held insertion while another subject advances.
func earlierPendingAddition(state syncState,pending syncPendingAddition) bool {
 selector,ok:=pendingAdditionSelector("",pending);if !ok{return true};identity:=syncTargetKey(selector,"")
 for _,prior:=range state.PendingAdditions {
  if prior.Line>pending.Line||(prior.Line==pending.Line&&prior.Ordinal>=pending.Ordinal) {continue}
  before,ok:=pendingAdditionSelector("",prior);if ok&&syncTargetKey(before,"")==identity{return true}
 }
 return false
}

// pendingAdditionOperation reconstructs the complete operation represented by engine state.
//
// Example: A priority retirement retains its expected prior instead of becoming a plain insertion.
func pendingAdditionOperation(pending syncPendingAddition) syncAddition {
 return syncAddition{Target:pending.Target,Record:json.RawMessage(pending.Record),Evidence:pending.Evidence,RetireForecast:pending.RetireForecast,ReopenWorkRef:pending.ReopenWorkRef,ExpectedPrior:json.RawMessage(pending.ExpectedPrior),RetirePriority:pending.RetirePriority}
}
