package main

import (
 "bytes"
 "encoding/json"
 "errors"
 "fmt"
 "sort"
 "strconv"
 "strings"

 "gopkg.in/yaml.v3"
)

// errBaselineHeld distinguishes an expected owner reconciliation conflict from malformed state.
//
// Example: One held timeline stays pending without discarding unrelated replay work.
var errBaselineHeld = errors.New("owner-held replay requires reconciliation")

// baselineMode is an explicit current owner's prospective replay decision.
//
// Example: Retire consumes an old event but does not claim its unknown value.
type baselineMode int
const (
 baselineAdopt baselineMode = iota+1
 baselinePending
 baselineRetire
)

// baselineOccurrence identifies one canonical log operation without copying sync metadata.
//
// Example: Two updates on one line retain distinct ordinals.
type baselineOccurrence struct {
 Ref string `json:"ref"`
 Line int `json:"line"`
 Ordinal int `json:"ordinal"`
 Update *syncUpdate `json:"update,omitempty"`
 Addition *syncAddition `json:"addition,omitempty"`
 Timeline *syncTimelineSnapshot `json:"timeline,omitempty"`
}

// baselineCatalog enumerates complete source occurrences and leaves incomplete tails unread.
//
// Example: A complete final JSON object need not end in a newline.
func baselineCatalog(log []byte) ([]baselineOccurrence,int,error) {
 lines:=bytes.Split(log,[]byte("\n")); terminated:=len(log)>0&&log[len(log)-1]=='\n'; if len(lines)>0 && len(lines[len(lines)-1])==0 { lines=lines[:len(lines)-1] }
 var out []baselineOccurrence; complete:=0
 for index,line:=range lines {
  if !json.Valid(line) { if index==len(lines)-1&&!terminated { break }; return nil,0,fmt.Errorf("invalid complete log line %d",index+1) }
  var event syncEvent; if err:=json.Unmarshal(line,&event); err!=nil { return nil,0,err }; complete++
  if event.InvalidStatus { return nil,0,fmt.Errorf("invalid status update line %d",index+1) }
  if event.Details.StatusUpdate==nil { continue }
  if len(event.Details.StatusUpdate.Updates)==0 && len(event.Details.StatusUpdate.Additions)==0 && event.Details.StatusUpdate.Timeline==nil { return nil,0,fmt.Errorf("empty status update line %d",index+1) }
  for ordinal,update:=range event.Details.StatusUpdate.Updates {
   if !validBaselineEventUpdate(event.Event,update,index+1,ordinal) { return nil,0,fmt.Errorf("invalid update %d/u/%d",index+1,ordinal) }
   item:=update; out=append(out,baselineOccurrence{Ref:fmt.Sprintf("%d/u/%d",index+1,ordinal),Line:index+1,Ordinal:ordinal,Update:&item})
  }
  for ordinal,addition:=range event.Details.StatusUpdate.Additions { item:=addition; out=append(out,baselineOccurrence{Ref:fmt.Sprintf("%d/a/%d",index+1,ordinal),Line:index+1,Ordinal:ordinal,Addition:&item}) }
  if event.Details.StatusUpdate.Timeline!=nil { out=append(out,baselineOccurrence{Ref:fmt.Sprintf("%d/t/0",index+1),Line:index+1,Timeline:event.Details.StatusUpdate.Timeline}) }
 }
 return out,complete,nil
}

// validBaselineEventUpdate enforces the replay decoder's event and operation rules.
//
// Example: A correction must actually identify an earlier operation.
func validBaselineEventUpdate(event string, update syncUpdate, line, ordinal int) bool {
 if !syncCorrectionLocatorPrecedes(update,line,ordinal)||!syncUpdateSelectorValid(update) { return false }
 if update.Retract {
  probe:=update;probe.Fields=map[string]string{"text":"probe"};probe.Retract=false
  return event=="correction"&&len(update.Fields)==0&&len(update.Values)==0&&len(update.Clear)==0&&validNarrativeSyncUpdate(probe)&&update.Corrects>0&&(update.CorrectsOrdinal==nil||*update.CorrectsOrdinal>=0)
 }
 valid:=validSyncUpdate(update)||validNarrativeSyncUpdate(update)||validStructuredSyncUpdate(update)||validLaneSyncUpdate(update)||validRootRetirementSyncUpdate(update)
 return valid&&update.Corrects>=0&&(update.CorrectsOrdinal==nil||(*update.CorrectsOrdinal>=0&&update.Corrects!=0))&&((event=="correction"&&update.Corrects!=0)||(event!="correction"&&update.Corrects==0))
}

// baselineFields includes fields a transition may affect implicitly.
//
// Example: Closing a lane may remove its whole forecast.
func baselineFields(update syncUpdate) []string {
 fields:=map[string]bool{}
 for _,field:=range syncGroupFields(update) { fields[field]=true }
 if update.Retract {fields["text"],fields["evidence"]=true,true}
 if update.Target==syncTargetLane {
  statusField:=update.RequiredEnvironment.String()+"_status"
  _,status:=update.Fields[statusField]; lifecycle:=update.Fields["lifecycle"]!=""
  for _,field:=range update.Clear { lifecycle=lifecycle||field=="lifecycle" }
  if status||lifecycle { fields["forecast"],fields["completed_at"]=true,true }
 }
 out:=make([]string,0,len(fields)); for field:=range fields { out=append(out,field) }; sort.Strings(out); return out
}

// syncBaselineVersion authorizes a current-value predecessor without claiming actual projection.
//
// Example: Adopt-current can bind a correction's predecessor but cannot supersede pending work.
type syncBaselineVersion struct { Line int `json:"line" yaml:"line"`; Ordinal int `json:"ordinal" yaml:"ordinal"` }

// syncVersionForCorrection chooses observed projection before explicit prospective authorization.
//
// Example: Retiring an occurrence never creates a successful-projection receipt.
func syncVersionForCorrection(state syncState,key string) (int,int) {
 if line:=state.Latest[key];line>0 {return line,state.AppliedOrdinals[key]}
 if state.Applied[key].Kind!=syncOwnershipHeld {version:=state.BaselineAdopted[key];return version.Line,version.Ordinal}
 return 0,0
}

// baselineAwaitsPredecessor retains corrections whose baseline predecessor was not projected.
//
// Example: A correction to a retired historical version requires explicit reconciliation.
func baselineAwaitsPredecessor(state syncState,update syncUpdate) bool {
 if state.BaselineCursor==nil||update.Corrects<=0||update.Corrects>*state.BaselineCursor{return false}
 for _,field:=range baselineFields(update) {line,_:=syncVersionForCorrection(state,syncTargetKey(update,field));if line==0{return true}}
 return false
}

// baselineTarget locates a semantic target in the supplied native report.
//
// Example: A lane includes work and environment in its identity.
func baselineTarget(root map[string]any, update syncUpdate) (map[string]any,bool,error) {
 if update.Target==syncTargetReport {if update.ID!="current"{return nil,false,fmt.Errorf("invalid report identity")};return root,true,nil}
 section,identity,err:=syncEffectSection(update.Target);if err!=nil{return nil,false,err}
 value:=root[section];if value==nil{return nil,false,nil};items,ok:=value.([]any);if !ok{return nil,false,fmt.Errorf("invalid target collection %s",section)}
 index,err:=syncEffectIndex(items,identity,syncEffect{Target:update.Target,ID:update.ID,WorkRef:update.WorkRef,RequiredEnvironment:update.RequiredEnvironment});if err!=nil{return nil,false,err};if index<0{return nil,false,nil}
 return items[index].(map[string]any),true,nil
}

// baselineCurrent records a fresh explicitly adopted guard, never a reconstructed historical value.
//
// Example: Null, absence and the literal string null remain different.
func baselineCurrent(root map[string]any, update syncUpdate, field string) (syncOwnership,error) {
 target,exists,err:=baselineTarget(root,update);if err!=nil{return syncOwnership{},err};if !exists {return syncAbsentValue,nil}
 var value any;var present bool
 if field=="record" {value,present=target,true} else {value,present=target[field]}
 if !present {return syncAbsentValue,nil}
 if policy:=syncOwnershipFields(update.Target);policy!=nil {
  for _,typed:=range policy.Typed {if typed==field {raw,err:=json.Marshal(value);if err!=nil{return syncOwnership{},err};return scalarSyncStoredValue(string(raw)),nil}}
 }
 if value==nil {return syncNullValue,nil}
 if text,ok:=value.(string);ok {return scalarSyncStoredValue(text),nil}
 raw,err:=json.Marshal(value);if err!=nil {return syncOwnership{},err};return scalarSyncStoredValue(string(raw)),nil
}

// seedBaselineOwnership applies one prospective decision without weakening an earlier hold.
//
// Example: Pending for the same record or timeline cannot release a retired occurrence.
func seedBaselineOwnership(state *syncState,root map[string]any,selector syncUpdate,field string,mode baselineMode) error {
 key:=syncTargetKey(selector,field)
 if mode==baselineRetire {state.Applied[key]=syncOwnership{Kind:syncOwnershipHeld};delete(state.BaselineAdopted,key);return nil}
 if mode==baselinePending&&state.Applied[key].Kind==syncOwnershipHeld {return nil}
 current,err:=baselineCurrent(root,selector,field);if err!=nil{return err};state.Applied[key]=current;return nil
}

// prepareBaseline constructs durable prospective ownership and pending state from explicit decisions.
//
// Example: Retire creates held ownership for an implicit value that cannot be recovered.
func prepareBaseline(document,log []byte, decisions map[string]baselineMode) ([]byte,error) {
 value,err:=decodeReportValue(document);if err!=nil {return nil,err};root,ok:=value.(map[string]any);if !ok {return nil,fmt.Errorf("report must be object")}
 if _,present:=root["status_sync"];present {return nil,fmt.Errorf("caller may not supply sync state")}
 items,cursor,err:=baselineCatalog(log);if err!=nil {return nil,err};if len(decisions)!=len(items) {return nil,fmt.Errorf("unresolved baseline occurrences: %d",len(items)-len(decisions))}
 state:=syncState{Cursor:cursor,Applied:map[string]syncOwnership{},Latest:map[string]int{},AppliedOrdinals:map[string]int{},BaselineAdopted:map[string]syncBaselineVersion{},Groups:map[string]syncPendingGroup{},PendingAdditions:map[string]syncPendingAddition{},PendingRootAdditions:map[string]syncPendingRootAddition{},PendingTimelines:map[string]syncPendingTimeline{}}
 state.BaselineCursor=&cursor
 for _,item:=range items {
  mode,exists:=decisions[item.Ref];if !exists||(mode!=baselineAdopt&&mode!=baselinePending&&mode!=baselineRetire) {return nil,fmt.Errorf("missing decision %s",item.Ref)}
  if item.Update!=nil {
   update:=*item.Update
   if !syncUpdateSelectorValid(update)||!syncCorrectionLocatorPrecedes(update,item.Line,item.Ordinal) {return nil,fmt.Errorf("invalid occurrence %s",item.Ref)}
   for _,field:=range baselineFields(update) {
    key:=syncTargetKey(update,field)
    if err:=seedBaselineOwnership(&state,root,update,field,mode);err!=nil{return nil,err}
    if mode==baselineAdopt {state.BaselineAdopted[key]=syncBaselineVersion{Line:item.Line,Ordinal:item.Ordinal}}
   }
   if mode==baselinePending {encoded,e:=encodeSyncGroup(update);if e!=nil{return nil,e};state.Groups[syncGroupKey(item.Line,item.Ordinal)]=syncPendingGroup{Line:item.Line,Ordinal:item.Ordinal,Update:encoded}}
   continue
  }
  if item.Addition!=nil {
   addition:=*item.Addition
   selector,valid:=syncAdditionSelector(addition);if !valid{return nil,fmt.Errorf("invalid addition %s",item.Ref)}
   if mode!=baselinePending {if err:=seedBaselineOwnership(&state,root,selector,"record",mode);err!=nil{return nil,err}}
   if mode==baselinePending {
    if !queueSyncAddition(&state,item.Line,item.Ordinal,addition){return nil,fmt.Errorf("invalid pending addition %s",item.Ref)}
    guard,err:=baselineCurrent(root,selector,"record");if err!=nil{return nil,err};key:=syncGroupKey(item.Line,item.Ordinal)
    if addition.Target==syncTargetRootCompletion||addition.Target==syncTargetRootForecast {pending:=state.PendingRootAdditions[key];pending.RecordGuard=&guard;state.PendingRootAdditions[key]=pending} else {pending:=state.PendingAdditions[key];pending.RecordGuard=&guard;state.PendingAdditions[key]=pending}
   }
   continue
  }
  selector:=syncUpdate{Target:syncTargetReport,ID:"current"}
  for _,field:=range []string{"timeline","timeline_as_of","timeline_state"} {if err:=seedBaselineOwnership(&state,root,selector,field,mode);err!=nil{return nil,err}}
  if mode==baselinePending {raw,e:=json.Marshal(item.Timeline);if e!=nil{return nil,e};state.PendingTimelines[strconv.Itoa(item.Line)]=syncPendingTimeline{Line:item.Line,Snapshot:string(raw)}}
 }
 root["status_sync"]=state;return json.Marshal(root)
}

// baselineGroupHeld prevents every fallback path from bypassing an owner-held effect.
//
// Example: A held forecast blocks closure but not an unrelated task update.
func baselineGroupHeld(state syncState, update syncUpdate) bool {
 if state.Applied[syncTargetKey(update,"record")].Kind==syncOwnershipHeld{return true}
 for _,field:=range baselineFields(update) {if state.Applied[syncTargetKey(update,field)].Kind==syncOwnershipHeld{return true}}
 return false
}

// baselineImplicitHistoryUnavailable refuses to treat current guards as historical implicit values.
//
// Example: A source-line pointer before native birth cannot prove forecast A's value.
func baselineImplicitHistoryUnavailable(state syncState,update syncUpdate) bool {
 if state.BaselineCursor==nil||update.Target!=syncTargetLane{return false}
 for _,field:=range []string{"forecast","completed_at"} {key:=syncTargetKey(update,field);if _,known:=state.Applied[key];known&&state.Latest[key]==0{return true}}
 return false
}

// verifyBaselineHolds guards actual effects, including implicit effects and whole-record insertions.
//
// Example: Retiring an old task addition never permits a later implicit reinsertion.
func verifyBaselineHolds(state syncState,effects []syncEffect) error {
 for _,effect:=range effects {
  field:=effect.Field
  switch effect.Operation {
  case syncEffectMetadata,syncEffectDiagnostic,syncEffectProjected:continue
  case syncEffectTimestamp:field="timestamp"
  case syncEffectTimelineAnchor:field="timeline_as_of"
  case syncEffectForecastRetire,syncEffectLaneForecastRestore:field="forecast"
  case syncEffectCompletedAtClear:field="completed_at"
  case syncEffectAdd,syncEffectRemove,syncEffectCancelledRootForecastRetire,syncEffectRootCompletionCorrection,syncEffectRootForecastReplace:if field=="" {field="record"}
  case syncEffectTimeline:field="timeline"
  case syncEffectPriorityRemove:field="top_priorities"
  case syncEffectSet,syncEffectClear:
  default:return fmt.Errorf("unknown effect %d",effect.Operation)
  }
  selector:=syncUpdate{Target:effect.Target,ID:effect.ID,WorkRef:effect.WorkRef,RequiredEnvironment:effect.RequiredEnvironment}
  if field=="record" {
   prefix:=syncTargetKey(selector,"")
   for key,ownership:=range state.Applied {if ownership.Kind==syncOwnershipHeld&&strings.HasPrefix(key,prefix){return fmt.Errorf("%w: record footprint",errBaselineHeld)}}
  }
  if effect.Operation==syncEffectTimeline||effect.Operation==syncEffectTimelineAnchor||effect.Operation==syncEffectTimestamp||effect.Operation==syncEffectPriorityRemove {selector=syncUpdate{Target:syncTargetReport,ID:"current"}}
  fields:=[4]string{field,"record"}
  if effect.Operation==syncEffectTimeline {fields[2],fields[3]="timeline_as_of","timeline_state"}
  for _,keyField:=range fields {if keyField!=""&&state.Applied[syncTargetKey(selector,keyField)].Kind==syncOwnershipHeld{return fmt.Errorf("%w: effect %s",errBaselineHeld,keyField)}}
 }
 return nil
}

// verifyBaselineAdmission checks the complete semantic delta as well as declared effect footprints.
//
// Example: A root chain cannot bypass a held field omitted from its aggregate effect metadata.
func verifyBaselineAdmission(original,candidate []byte,effects []syncEffect) error {
 var document struct{State syncState `yaml:"status_sync"`};if err:=yaml.Unmarshal(original,&document);err!=nil{return err}
 if err:=verifyBaselineHolds(document.State,effects);err!=nil{return err}
 hasHeld:=false;for _,ownership:=range document.State.Applied {if ownership.Kind==syncOwnershipHeld {hasHeld=true;break}};if !hasHeld{return nil}
 var nextDocument struct{State syncState `yaml:"status_sync"`};if err:=yaml.Unmarshal(candidate,&nextDocument);err!=nil{return err}
 before,err:=opaqueReportMap(original);if err!=nil{return err};after,err:=opaqueReportMap(candidate);if err!=nil{return err}
 for key,ownership:=range document.State.Applied {
  if ownership.Kind!=syncOwnershipHeld {continue}
  if nextDocument.State.Applied[key].Kind!=syncOwnershipHeld{return fmt.Errorf("%w: ownership transition",errBaselineHeld)}
  split:=strings.LastIndexByte(key,'/');if split<0{return fmt.Errorf("invalid held ownership key")}
  var identity []string;if err:=json.Unmarshal([]byte(key[:split]),&identity);err!=nil{return err};if len(identity)!=4{return fmt.Errorf("invalid held ownership identity")}
  selector:=syncUpdate{Target:parseSyncTarget(identity[0]),ID:identity[1],WorkRef:identity[2],RequiredEnvironment:parseSyncEnvironment(identity[3])};if selector.Target==syncTargetUnknown{return fmt.Errorf("invalid held target")}
  old,err:=baselineCurrent(before,selector,key[split+1:]);if err!=nil{return err};next,err:=baselineCurrent(after,selector,key[split+1:]);if err!=nil{return err}
  if old!=next{return fmt.Errorf("%w: semantic field %s",errBaselineHeld,key[split+1:])}
 }
 return nil
}
