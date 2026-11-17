package main

import (
 "errors"
 "fmt"
)

// errPreparedRecordConflict keeps a changed owner record pending without aborting independent work.
//
// Example: Deleting a previously present record does not authorize historical reinsertion.
var errPreparedRecordConflict = errors.New("prepared current-record precondition conflict")

// verifyPreparedRecord compares one occurrence's prospective guard against its actual current subject.
//
// Example: An absent guard permits insertion while a present B guard rejects owner deletion.
func verifyPreparedRecord(data []byte,selector syncUpdate,guard *syncOwnership) error {
 if guard==nil{return nil}
 root,err:=opaqueReportMap(data);if err!=nil{return err};current,err:=baselineCurrent(root,selector,"record");if err!=nil{return err}
 if current!=*guard{return errPreparedRecordConflict};return nil
}

// advancePreparedRecordGuard advances only a matching guard across verified engine changes.
//
// Example: Canonical forecast projection updates its expectation without adopting an owner edit.
func advancePreparedRecordGuard(before,after map[string]any,selector syncUpdate,guard **syncOwnership) (bool,error) {
 if *guard==nil{return false,nil};previous,err:=baselineCurrent(before,selector,"record");if err!=nil{return false,err};if previous!=**guard{return false,nil}
 next,err:=baselineCurrent(after,selector,"record");if err!=nil{return false,err};if previous==next{return false,nil};*guard=&next;return true,nil
}

// advancePreparedRecordGuards preserves causality for still-pending prepared occurrences.
//
// Example: Verified replay may advance a matching expectation; an already diverged owner value never does.
func advancePreparedRecordGuards(original,candidate []byte,state *syncState) (bool,error) {
 if len(state.PendingAdditions)+len(state.PendingRootAdditions)==0{return false,nil}
 before,err:=opaqueReportMap(original);if err!=nil{return false,err};after,err:=opaqueReportMap(candidate);if err!=nil{return false,err};changed:=false
 for key,pending:=range state.PendingAdditions {
  selector,ok:=pendingAdditionSelector(key,pending);if !ok{return false,fmt.Errorf("invalid prepared addition identity")}
  updated,err:=advancePreparedRecordGuard(before,after,selector,&pending.RecordGuard);if err!=nil{return false,err};if updated {state.PendingAdditions[key]=pending;changed=true}
 }
 for key,pending:=range state.PendingRootAdditions {
  selector,ok:=syncAdditionSelector(pending.Addition);if !ok{return false,fmt.Errorf("invalid prepared root identity")}
  updated,err:=advancePreparedRecordGuard(before,after,selector,&pending.RecordGuard);if err!=nil{return false,err};if updated {state.PendingRootAdditions[key]=pending;changed=true}
 }
 return changed,nil
}
