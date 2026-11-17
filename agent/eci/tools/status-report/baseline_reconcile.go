package main

import (
 "bytes"
 "encoding/json"
 "fmt"
)

// syncOperationKind distinguishes independently indexed operations on one log line.
//
// Example: Update ordinal zero is not addition ordinal zero.
type syncOperationKind int

// Operation kinds are closed domain values carried by reconciliation requests.
//
// Example: A timeline locator cannot be interpreted as an update locator.
const (
 syncOperationUpdate syncOperationKind = iota+1
 syncOperationAddition
 syncOperationTimeline
)

// syncOperationLocator identifies a canonical operation without accepting writable sync metadata.
//
// Example: Reconcile the update at line two, ordinal one.
type syncOperationLocator struct { Kind syncOperationKind `json:"kind"`; Line int `json:"line"`; Ordinal int `json:"ordinal"` }

// baselineReconciliationAction chooses prospective resumption or explicit consumption.
//
// Example: Retire keeps owner C instead of forcing an obsolete insertion A.
type baselineReconciliationAction int

// Reconciliation actions never masquerade as successful projection receipts.
//
// Example: Retiring pending work changes no report content or Latest receipt.
const (
 baselineResume baselineReconciliationAction = iota+1
 baselineRetirePending
)

// baselineReconciliation binds authorization to one still-pending operation and exact current values.
//
// Example: A correction names its retired predecessor rather than fabricating a projection receipt.
type baselineReconciliation struct { Action baselineReconciliationAction `json:"action"`; Pending syncOperationLocator `json:"pending"`; Predecessor *syncOperationLocator `json:"predecessor,omitempty"`; Expected map[string]reportEditExpectation `json:"expected"` }

// resolveBaselineOperation uses the actual event decoder at the requested canonical locator.
//
// Example: Reject a wrong ordinal even when another operation has the same target.
func resolveBaselineOperation(log []byte,locator syncOperationLocator) (baselineOccurrence,error) {
 if locator.Line<1||locator.Ordinal<0{return baselineOccurrence{},fmt.Errorf("invalid operation locator")}
 lines:=bytes.Split(log,[]byte("\n"));if locator.Line>len(lines){return baselineOccurrence{},fmt.Errorf("operation line missing")}
 var event syncEvent;if err:=json.Unmarshal(lines[locator.Line-1],&event);err!=nil{return baselineOccurrence{},err};if event.InvalidStatus||event.Details.StatusUpdate==nil{return baselineOccurrence{},fmt.Errorf("invalid operation source")}
 occurrence:=baselineOccurrence{Line:locator.Line,Ordinal:locator.Ordinal}
 switch locator.Kind {
 case syncOperationUpdate:
  if locator.Ordinal>=len(event.Details.StatusUpdate.Updates){return occurrence,fmt.Errorf("update ordinal missing")};occurrence.Update=&event.Details.StatusUpdate.Updates[locator.Ordinal]
 case syncOperationAddition:
  if locator.Ordinal>=len(event.Details.StatusUpdate.Additions){return occurrence,fmt.Errorf("addition ordinal missing")};occurrence.Addition=&event.Details.StatusUpdate.Additions[locator.Ordinal]
 case syncOperationTimeline:
  if locator.Ordinal!=0||event.Details.StatusUpdate.Timeline==nil{return occurrence,fmt.Errorf("timeline missing")};occurrence.Timeline=event.Details.StatusUpdate.Timeline
 default:return occurrence,fmt.Errorf("unsupported operation kind")
 }
 return occurrence,nil
}

// sameBaselineOperation compares complete typed payloads including exact numeric spellings.
//
// Example: Replaced canonical bytes cannot authorize a different pending body at the same locator.
func sameBaselineOperation(a,b any) (bool,error) {
 first,err:=json.Marshal(a);if err!=nil{return false,err};second,err:=json.Marshal(b);if err!=nil{return false,err};return rootChainJSONEqual(first,second),nil
}

// reconcileBaselinePending authorizes one exact pending transaction without claiming historical projection.
//
// Example: Release B for correction C, or explicitly rebase a pending insertion to current absence.
func reconcileBaselinePending(document,log []byte,request baselineReconciliation) ([]byte,error) {
 if request.Action!=baselineResume&&request.Action!=baselineRetirePending{return nil,fmt.Errorf("invalid reconciliation action")}
 value,err:=decodeReportValue(document);if err!=nil{return nil,err};root,ok:=value.(map[string]any);if !ok{return nil,fmt.Errorf("report object required")}
 encoded,err:=json.Marshal(root["status_sync"]);if err!=nil{return nil,err};var state syncState;if err:=json.Unmarshal(encoded,&state);err!=nil{return nil,err}
 occurrence,err:=resolveBaselineOperation(log,request.Pending);if err!=nil{return nil,err};key:=syncGroupKey(request.Pending.Line,request.Pending.Ordinal)
 var selector syncUpdate;var fields []string;var predecessor *syncOperationLocator
 switch request.Pending.Kind {
 case syncOperationUpdate:
  pending,present:=state.Groups[key];if !present||pending.Line!=request.Pending.Line||pending.Ordinal!=request.Pending.Ordinal{return nil,fmt.Errorf("update is not pending")}
  stored,err:=decodeSyncGroup(pending.Update);if err!=nil{return nil,err};same,err:=sameBaselineOperation(stored,*occurrence.Update);if err!=nil{return nil,err};if !same{return nil,fmt.Errorf("pending update source mismatch")}
  selector=stored;fields=baselineFields(stored)
  if stored.Corrects>0&&request.Action==baselineResume {
   if request.Predecessor==nil{return nil,fmt.Errorf("predecessor authorization required")};wantedOrdinal:=0;if stored.CorrectsOrdinal!=nil{wantedOrdinal=*stored.CorrectsOrdinal}
   wanted:=syncOperationLocator{Kind:syncOperationUpdate,Line:stored.Corrects,Ordinal:wantedOrdinal};if *request.Predecessor!=wanted{return nil,fmt.Errorf("predecessor locator mismatch")}
   prior,err:=resolveBaselineOperation(log,wanted);if err!=nil{return nil,err};if syncTargetKey(*prior.Update,"")!=syncTargetKey(stored,""){return nil,fmt.Errorf("predecessor target mismatch")}
   if stored.Retract {
    if !validNarrativeSyncUpdate(*prior.Update)||len(prior.Update.Values)>0||len(prior.Update.Clear)>0{return nil,fmt.Errorf("invalid subject predecessor")}
   } else {
    priorFields:=map[string]bool{};for _,field:=range baselineFields(*prior.Update){priorFields[field]=true};for _,field:=range fields{if !priorFields[field]{return nil,fmt.Errorf("predecessor field mismatch")}}
   }
   predecessor=&wanted
  } else if request.Predecessor!=nil {return nil,fmt.Errorf("unexpected predecessor")}
 case syncOperationAddition:
  selector,ok=syncAdditionSelector(*occurrence.Addition);if !ok{return nil,fmt.Errorf("invalid addition identity")};fields=[]string{"record"};if request.Predecessor!=nil{return nil,fmt.Errorf("unexpected predecessor")}
  var stored syncAddition
  if selector.Target==syncTargetRootCompletion||selector.Target==syncTargetRootForecast {pending,present:=state.PendingRootAdditions[key];if !present{return nil,fmt.Errorf("root is not pending")};stored=pending.Addition} else {pending,present:=state.PendingAdditions[key];if !present||pending.Line!=request.Pending.Line||pending.Ordinal!=request.Pending.Ordinal{return nil,fmt.Errorf("addition is not pending")};stored=pendingAdditionOperation(pending)}
  same,err:=sameBaselineOperation(stored,*occurrence.Addition);if err!=nil{return nil,err};if !same{return nil,fmt.Errorf("pending addition source mismatch")}
 case syncOperationTimeline:
  pending,present:=state.PendingTimelines[fmt.Sprint(request.Pending.Line)];if !present||pending.Line!=request.Pending.Line{return nil,fmt.Errorf("timeline is not pending")};var stored syncTimelineSnapshot;if err:=json.Unmarshal([]byte(pending.Snapshot),&stored);err!=nil{return nil,err};same,err:=sameBaselineOperation(stored,*occurrence.Timeline);if err!=nil{return nil,err};if !same{return nil,fmt.Errorf("pending timeline source mismatch")};if request.Predecessor!=nil{return nil,fmt.Errorf("unexpected predecessor")};selector=syncUpdate{Target:syncTargetReport,ID:"current"};fields=[]string{"timeline","timeline_as_of","timeline_state"}
 }
 if len(request.Expected)!=len(fields){return nil,fmt.Errorf("complete current expectations required")}
 target,present,err:=baselineTarget(root,selector);if err!=nil{return nil,err};current:=make(map[string]syncOwnership,len(fields))
 for _,field:=range fields {
  expectation,provided:=request.Expected[field];if !provided{return nil,fmt.Errorf("missing current expectation %s",field)}
  var actual any;exists:=false;if present {if field=="record"{actual,exists=target,true}else{actual,exists=target[field]}}
  if err:=verifyReportEditExpectation(actual,exists,expectation);err!=nil{return nil,fmt.Errorf("current %s: %w",field,err)}
  ownership,err:=baselineCurrent(root,selector,field);if err!=nil{return nil,err};current[field]=ownership
 }
 if state.BaselineAdopted==nil{state.BaselineAdopted=map[string]syncBaselineVersion{}}
 if request.Action==baselineRetirePending {
  if request.Predecessor!=nil{return nil,fmt.Errorf("unexpected predecessor for retirement")}
  for _,field:=range fields {fieldKey:=syncTargetKey(selector,field);state.Applied[fieldKey]=syncOwnership{Kind:syncOwnershipHeld};delete(state.BaselineAdopted,fieldKey)}
  switch request.Pending.Kind {case syncOperationUpdate:delete(state.Groups,key);case syncOperationAddition:delete(state.PendingAdditions,key);delete(state.PendingRootAdditions,key);case syncOperationTimeline:delete(state.PendingTimelines,fmt.Sprint(request.Pending.Line))}
  root["status_sync"]=state;return json.Marshal(root)
 }
 for _,field:=range fields {
  fieldKey:=syncTargetKey(selector,field)
  if request.Pending.Kind!=syncOperationAddition||state.Applied[fieldKey].Kind==syncOwnershipHeld {state.Applied[fieldKey]=current[field]}
  if predecessor!=nil&&!selector.Retract {state.BaselineAdopted[fieldKey]=syncBaselineVersion{Line:predecessor.Line,Ordinal:predecessor.Ordinal}}
 }
 if predecessor!=nil&&selector.Retract {pending:=state.Groups[key];pending.RetractionVersion=&syncBaselineVersion{Line:predecessor.Line,Ordinal:predecessor.Ordinal};state.Groups[key]=pending}
 if request.Pending.Kind==syncOperationAddition {guard:=current["record"];if selector.Target==syncTargetRootCompletion||selector.Target==syncTargetRootForecast {pending:=state.PendingRootAdditions[key];pending.RecordGuard=&guard;state.PendingRootAdditions[key]=pending} else {pending:=state.PendingAdditions[key];pending.RecordGuard=&guard;state.PendingAdditions[key]=pending}}
 root["status_sync"]=state;return json.Marshal(root)
}

