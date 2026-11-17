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
 "gopkg.in/yaml.v3"
)

// baselineTaskEvent encodes a complete real replay addition.
//
// Example: A second occurrence cannot release a retired task identity.
func baselineTaskEvent(id string) string {
 return `{"event":"requirement_change","details":{"status_update":{"additions":[{"target":"task","record":{"id":"`+id+`","title":"New","priority":"high","impact":"Useful","effort":{"min_minutes":10,"max_minutes":20,"kind":"elapsed","basis":"guess"},"status":"NEW"}}]}}}`+"\n"
}

// baselineTimelineEvent supplies an ordinary complete timeline through the event parser.
//
// Example: A later timeline remains pending until explicit prospective release.
func baselineTimelineEvent(t *testing.T, label string) string {
 t.Helper()
 anchor:=time.Date(2026,11,2,15,24,0,0,time.UTC)
 snapshot:=syncTimelineSnapshot{AsOf:anchor.Format(time.RFC3339),State:timelinePresent}
 for i:=range 8 {start:=anchor.Add(-4*time.Hour+time.Duration(i)*30*time.Minute);snapshot.Entries=append(snapshot.Entries,timelineEntry{Job:"root",ParentChain:"root",Start:start.Format(time.RFC3339),End:start.Add(30*time.Minute).Format(time.RFC3339),Fast:fmt.Sprint(i),Normal:label})}
 raw,err:=json.Marshal(snapshot);require.NoError(t,err)
 return `{"event":"worker_progress","details":{"status_update":{"timeline":`+string(raw)+`}}}`+"\n"
}

// TestBaselineSameIdentityPendingCannotReleaseHeldRecord checks constructor monotonicity.
//
// Example: Retire task 3 then queue task 3, while independent task 4 still advances.
func TestBaselineSameIdentityPendingCannotReleaseHeldRecord(t *testing.T) {
 t.Parallel()
 log:=baselineTaskEvent("3")+baselineTaskEvent("3")+baselineTaskEvent("4")
 prepared,err:=prepareBaseline(baselineNativeFixture(t,linkedReport),[]byte(log),map[string]baselineMode{"1/a/0":baselineRetire,"2/a/0":baselinePending,"3/a/0":baselinePending});require.NoError(t,err)
 prepared=baselineReload(t,prepared)
 var metadata struct{State syncState `yaml:"status_sync"`};require.NoError(t,yaml.Unmarshal(prepared,&metadata))
 selector:=syncUpdate{Target:syncTargetTask,ID:"3"}
 require.Equal(t,syncOwnershipHeld,metadata.State.Applied[syncTargetKey(selector,"record")].Kind)
 dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(log),0600))
 result,_,err:=projectLogStatus(dir,prepared,ledgerSource{},forecastSource{});require.NoError(t,err)
 parsed,err:=parseDBReport(result);require.NoError(t,err);require.Len(t,parsed.Tasks,3);require.Equal(t,taskID("4"),parsed.Tasks[2].ID);require.NotEmpty(t,parsed.StatusSync.PendingAdditions);require.NotNil(t,parsed.ScopeComplete);require.False(t,*parsed.ScopeComplete)
 released,err:=adoptBaselineCurrent(baselineReload(t,result),selector,map[string]syncOwnership{"record":syncAbsentValue});require.NoError(t,err)
 result,_,err=projectLogStatus(dir,released,ledgerSource{},forecastSource{});require.NoError(t,err);parsed,err=parseDBReport(result);require.NoError(t,err);require.Len(t,parsed.Tasks,4);require.Empty(t,parsed.StatusSync.PendingAdditions)
}

// TestBaselineTimelineAdmissionProtectsHeldSnapshot checks constructor and later direct replay.
//
// Example: Owner timeline and anchor remain stable while independent next focus advances.
func TestBaselineTimelineAdmissionProtectsHeldSnapshot(t *testing.T) {
 t.Parallel()
 for _,mixed:=range []bool{false,true} {t.Run(fmt.Sprint(mixed),func(t *testing.T){
  first:=baselineTimelineEvent(t,"historical");log:=first;decisions:=map[string]baselineMode{"1/t/0":baselineRetire}
  if mixed {log+=baselineTimelineEvent(t,"queued");decisions["2/t/0"]=baselinePending}
  native:=baselineNativeFixture(t,linkedReport+"timeline_as_of: '2026-11-02T15:24:00Z'\ntimeline_state: verified_empty\ntimeline: []\n")
  prepared,err:=prepareBaseline(native,[]byte(log),decisions);require.NoError(t,err);prepared=baselineReload(t,prepared)
  var metadata struct{State syncState `yaml:"status_sync"`};require.NoError(t,yaml.Unmarshal(prepared,&metadata));selector:=syncUpdate{Target:syncTargetReport,ID:"current"}
  require.Equal(t,syncOwnershipHeld,metadata.State.Applied[syncTargetKey(selector,"timeline")].Kind)
  log+=baselineTimelineEvent(t,"later")+`{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"report","id":"current","fields":{"next_focus":"independent"}}]}}}`+"\n"
  dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(log),0600))
  result,_,err:=projectLogStatus(dir,prepared,ledgerSource{},forecastSource{});require.NoError(t,err);result=baselineReload(t,result)
  parsed,err:=parseDBReport(result);require.NoError(t,err);require.Empty(t,parsed.Timeline);require.Equal(t,timelineVerifiedEmpty,parsed.TimelineState);require.Equal(t,"2026-11-02T15:24:00Z",parsed.TimelineAsOf);require.Equal(t,"independent",parsed.NextFocus);require.NotNil(t,parsed.ScopeComplete);require.False(t,*parsed.ScopeComplete);require.NotEmpty(t,parsed.StatusSync.PendingTimelines)
  rootValue,err:=decodeReportValue(result);require.NoError(t,err);expected:=map[string]syncOwnership{}
  for _,field:=range []string{"timeline","timeline_as_of","timeline_state"} {current,err:=baselineCurrent(rootValue.(map[string]any),selector,field);require.NoError(t,err);expected[field]=current;require.Equal(t,syncOwnershipHeld,parsed.StatusSync.Applied[syncTargetKey(selector,field)].Kind)}
  released,err:=adoptBaselineCurrent(result,selector,expected);require.NoError(t,err);result,_,err=projectLogStatus(dir,released,ledgerSource{},forecastSource{});require.NoError(t,err);parsed,err=parseDBReport(result);require.NoError(t,err);require.Len(t,parsed.Timeline,8);require.Equal(t,"later",parsed.Timeline[0].Normal);require.Empty(t,parsed.StatusSync.PendingTimelines)
 })}
}

// TestBaselineCanonicalProjectionRemainsAuthoritative compares replay with full DB projection.
//
// Example: A held replay forecast is projected from canonical sources, not made a second SSOT.
func TestBaselineCanonicalProjectionRemainsAuthoritative(t *testing.T) {
 t.Parallel()
 reopen:=`{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"implementation_status":"IN PROGRESS"}}]}}}`+"\n"
 ownerB:=strings.Replace(linkedReport,"outcome: First result, target:","outcome: Owner-B, owner_note: keep, opaque: 1e400, target:",1)
 native:=baselineNativeFixture(t,ownerB);value,err:=decodeReportValue(native);require.NoError(t,err);value.(map[string]any)["lanes"].([]any)[0].(map[string]any)["forecast"].(map[string]any)["opaque"]=json.Number("1e400");native,err=json.Marshal(value);require.NoError(t,err)
 prepared,err:=prepareBaseline(native,[]byte(reopen),map[string]baselineMode{"1/u/0":baselineRetire});require.NoError(t,err);prepared=baselineReload(t,prepared)
 dir:=t.TempDir();logBytes:=[]byte(reopen+`{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"2","fields":{"owner":"independent"}}]}}}`+"\n");require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),logBytes,0600));captured,err:=captureReplayLog(dir);require.NoError(t,err)
 var ledger ledgerSource;var forecasts forecastSource;require.NoError(t,yaml.Unmarshal([]byte(linkedLedger),&ledger));require.NoError(t,yaml.Unmarshal([]byte(linkedForecasts),&forecasts))
 sourcesBefore,err:=json.Marshal(forecasts);require.NoError(t,err)
 replayOnly,_,err:=projectLogStatus(dir,prepared,ledger,forecasts);require.NoError(t,err);replayed,err:=parseDBReport(replayOnly);require.NoError(t,err);require.Equal(t,"Owner-B",replayed.Lanes[0].Forecast.Outcome)
 snapshot:=projectionSnapshot{Report:prepared,Sources:reconciliationSnapshot{ledger:ledger,ledgerPresent:true,forecasts:forecasts,forecastPresent:true}}
 freezeIssues:=reconcileSnapshotAt(replayed,snapshot.Sources,time.Date(2026,11,2,15,24,0,0,time.UTC));require.Contains(t,strings.Join(freezeIssues,"\n"),"outcome")
 projected,err:=projectDBReport(dir,"assignment_change",snapshot,time.Date(2026,11,2,15,24,0,0,time.UTC),captured);require.NoError(t,err)
 yamlProjected,err:=reportJSONToReplayYAML(projected);require.NoError(t,err);parsed,err:=parseDBReport(yamlProjected);require.NoError(t,err);require.Equal(t,"First result",parsed.Lanes[0].Forecast.Outcome);require.Equal(t,"independent",parsed.Tasks[1].Owner)
 selector:=syncUpdate{Target:syncTargetLane,ID:"1",WorkRef:"project-understanding.yaml#/records/work:first",RequiredEnvironment:syncEnvironmentImplementation};require.Equal(t,syncOwnershipHeld,parsed.StatusSync.Applied[syncTargetKey(selector,"forecast")].Kind)
 decoded,err:=decodeReportValue(projected);require.NoError(t,err);forecast:=decoded.(map[string]any)["lanes"].([]any)[0].(map[string]any)["forecast"].(map[string]any);require.Equal(t,"keep",forecast["owner_note"]);require.Equal(t,json.Number("1e400"),forecast["opaque"])
 sourcesAfter,err:=json.Marshal(forecasts);require.NoError(t,err);require.JSONEq(t,string(sourcesBefore),string(sourcesAfter))
 forecasts.Records["first"]["outcome"]="Current-source-C";snapshot.Report=projected;snapshot.Sources.forecasts=forecasts
 projected,err=projectDBReport(dir,"assignment_change",snapshot,time.Date(2026,11,2,15,24,0,0,time.UTC),captured);require.NoError(t,err);yamlProjected,err=reportJSONToReplayYAML(projected);require.NoError(t,err);parsed,err=parseDBReport(yamlProjected);require.NoError(t,err);require.Equal(t,"Current-source-C",parsed.Lanes[0].Forecast.Outcome);require.Equal(t,syncOwnershipHeld,parsed.StatusSync.Applied[syncTargetKey(selector,"forecast")].Kind)
 close:=`{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}},{"target":"report","id":"current","fields":{"next_focus":"after-canonical-projection"}}]}}}`+"\n"
 require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),append(logBytes,[]byte(close)...),0600));captured,err=captureReplayLog(dir);require.NoError(t,err)
 completedLedger,_:=baselineSources(t);forecasts.Records["first"]["status"]="inactive";snapshot.Report=projected;snapshot.Sources.ledger=completedLedger;snapshot.Sources.forecasts=forecasts
 held,err:=projectDBReport(dir,"lifecycle_change",snapshot,time.Date(2026,11,2,15,26,0,0,time.UTC),captured);require.NoError(t,err);heldYAML,err:=reportJSONToReplayYAML(held);require.NoError(t,err);parsed,err=parseDBReport(heldYAML);require.NoError(t,err);require.Equal(t,"Current-source-C",parsed.Lanes[0].Forecast.Outcome);require.Equal(t,statusInProgress,parsed.Lanes[0].ImplementationStatus);require.Equal(t,"after-canonical-projection",parsed.NextFocus);require.NotEmpty(t,parsed.StatusSync.Groups)
 heldRoot,err:=decodeReportValue(held);require.NoError(t,err);expected:=map[string]syncOwnership{}
 for _,field:=range []string{"forecast","completed_at","implementation_status"} {current,err:=baselineCurrent(heldRoot.(map[string]any),selector,field);require.NoError(t,err);expected[field]=current}
 _,err=adoptBaselineCurrent(held,selector,map[string]syncOwnership{"forecast":scalarSyncStoredValue(`{"outcome":"Owner-B"}`)});require.Error(t,err)
 snapshot.Report,err=adoptBaselineCurrent(held,selector,expected);require.NoError(t,err)
 finished,err:=projectDBReport(dir,"lifecycle_change",snapshot,time.Date(2026,11,2,15,26,0,0,time.UTC),captured);require.NoError(t,err);finishedYAML,err:=reportJSONToReplayYAML(finished);require.NoError(t,err);parsed,err=parseDBReport(finishedYAML);require.NoError(t,err);require.Equal(t,statusClosed,parsed.Lanes[0].ImplementationStatus);require.Nil(t,parsed.Lanes[0].Forecast);require.Empty(t,parsed.StatusSync.Groups)
 t.Log("replay-only retains Owner-B; full DB refresh projects canonical First result then Current-source-C, preserves opaque extensions and replay hold, never modifies canonical sources")
}

// TestBaselineAdmissionRejectsHoldRelease checks the durable state half of admission.
//
// Example: A metadata-only replay transition cannot silently release held ownership.
func TestBaselineAdmissionRejectsHoldRelease(t *testing.T) {
 t.Parallel()
 log:=[]byte(`{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"report","id":"current","fields":{"next_focus":"historical"}}]}}}`+"\n")
 prepared,err:=prepareBaseline(baselineNativeFixture(t,linkedReport),log,map[string]baselineMode{"1/u/0":baselineRetire});require.NoError(t,err)
 var saved struct{State syncState `yaml:"status_sync"`};require.NoError(t,yaml.Unmarshal(prepared,&saved));selector:=syncUpdate{Target:syncTargetReport,ID:"current"}
 saved.State.Applied[syncTargetKey(selector,"next_focus")]=syncAbsentValue
 root,err:=decodeReportValue(prepared);require.NoError(t,err);root.(map[string]any)["status_sync"]=saved.State;candidate,err:=json.Marshal(root);require.NoError(t,err)
 require.Error(t,verifyBaselineAdmission(prepared,candidate,[]syncEffect{{Operation:syncEffectMetadata,Value:saved.State}}))
 require.NoError(t,verifyBaselineAdmission(prepared,prepared,nil))
 unchanged,err:=decodeReportValue(prepared);require.NoError(t,err);unchanged.(map[string]any)["next_focus"]="silently changed";changed,err:=json.Marshal(unchanged);require.NoError(t,err)
 require.Error(t,verifyBaselineAdmission(prepared,changed,nil))
 unchanged.(map[string]any)["next_focus"]="Finish";unchanged.(map[string]any)["independent_extension"]="allowed";independent,err:=json.Marshal(unchanged);require.NoError(t,err);require.NoError(t,verifyBaselineAdmission(prepared,independent,nil))
}

// TestBaselineRootChainUsesWholeRecordAdmission checks a noncanonical replay verifier.
//
// Example: Root lifecycle chain verification cannot discard a retired owner-held forecast.
func TestBaselineRootChainUsesWholeRecordAdmission(t *testing.T) {
 t.Parallel()
 prior:=json.RawMessage(`{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"complete"}`)
 update:=syncUpdate{Target:syncTargetRootForecast,ID:"forecasts.yaml#/records/f",WorkRef:"project-understanding.yaml#/records/w",Evidence:"cancelled",Values:map[string]json.RawMessage{"expected_prior":prior},Clear:[]string{"record"}}
 encoded,err:=json.Marshal(update);require.NoError(t,err);log:=[]byte(`{"event":"lifecycle_change","details":{"status_update":{"updates":[`+string(encoded)+`]}}}`+"\n")
 ledger:=ledgerSource{Records:map[string]sourceRecord{"w":{Type:"work",OutcomeRefs:[]string{"o"},Data:workData{State:workStateCancelled,Roles:[]reportingRole{reportingRoleRoot},ForecastRef:"forecasts.yaml#/records/f"}},"o":{Type:"outcome",Outcome:"Result"}}}
 forecasts:=forecastSource{Records:map[string]map[string]any{"f":{"kind":"root","status":"inactive"}}}
 prepared,err:=prepareBaseline(baselineNativeFixture(t,valid+"root_forecasts: ["+string(prior)+"]\n"),log,map[string]baselineMode{"1/u/0":baselineRetire});require.NoError(t,err);prepared=baselineReload(t,prepared)
 candidate,effects,ok:=applyRootRetirementSyncUpdate(prepared,update,ledger,forecasts,nil);require.True(t,ok)
 locator:=[]rootLifecycleLocator{{Line:1,Ordinal:0,Target:syncTargetRootForecast,Update:true}}
 require.Error(t,verifyRootLifecycleChain(prepared,candidate,log,locator,effects,ledger,forecasts))
 root,err:=decodeReportValue(prepared);require.NoError(t,err);current,err:=baselineCurrent(root.(map[string]any),update,"record");require.NoError(t,err);require.Equal(t,syncOwnershipValue,current.Kind)
 released,err:=adoptBaselineCurrent(prepared,update,map[string]syncOwnership{"record":current});require.NoError(t,err)
 dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),append(append([]byte{},log...),log...),0600))
 finished,_,err:=projectLogStatus(dir,baselineReload(t,released),ledger,forecasts);require.NoError(t,err);parsed,err:=parseDBReport(finished);require.NoError(t,err);require.Empty(t,parsed.RootForecasts);require.Empty(t,parsed.StatusSync.Groups)
}

// TestBaselineImplicitAnchorCannotBypassHeldTimeline checks the automatic clock path.
//
// Example: A new timestamp does not insert a held anchor while independent work advances.
func TestBaselineImplicitAnchorCannotBypassHeldTimeline(t *testing.T) {
 t.Parallel()
 first:=baselineTimelineEvent(t,"historical");prepared,err:=prepareBaseline(baselineNativeFixture(t,linkedReport+"timeline_state: verified_empty\ntimeline: []\n"),[]byte(first),map[string]baselineMode{"1/t/0":baselineRetire});require.NoError(t,err)
 log:=first+`{"timestamp":"2026-11-02T15:25:00Z","event":"worker_progress","details":{"status_update":{"updates":[{"target":"report","id":"current","fields":{"next_focus":"independent-clock"}}]}}}`+"\n"
 dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(log),0600));result,_,err:=projectLogStatus(dir,baselineReload(t,prepared),ledgerSource{},forecastSource{});require.NoError(t,err)
 parsed,err:=parseDBReport(result);require.NoError(t,err);require.Equal(t,"independent-clock",parsed.NextFocus);require.Equal(t,"2026-11-02T15:24:00Z",parsed.Timestamp);require.Empty(t,parsed.TimelineAsOf);require.Empty(t,parsed.Timeline);require.NotNil(t,parsed.ScopeComplete);require.False(t,*parsed.ScopeComplete);require.Contains(t,string(result),"clock owner-held")
}

// TestBaselineRetiredTimelineDoesNotClaimPublication preserves explicit earlier pending work.
//
// Example: Consuming a later snapshot is not evidence that it was actually projected.
func TestBaselineRetiredTimelineDoesNotClaimPublication(t *testing.T) {
 t.Parallel()
 historical:=baselineTimelineEvent(t,"pending earlier")+baselineTimelineEvent(t,"retired later")
 native:=baselineNativeFixture(t,linkedReport+"timeline_as_of: '2026-11-02T15:24:00Z'\ntimeline_state: verified_empty\ntimeline: []\n")
 prepared,err:=prepareBaseline(native,[]byte(historical),map[string]baselineMode{"1/t/0":baselinePending,"2/t/0":baselineRetire});require.NoError(t,err)
 dir:=t.TempDir();log:=historical+`{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"report","id":"current","fields":{"next_focus":"independent-order"}}]}}}`+"\n";require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(log),0600))
 held,_,err:=projectLogStatus(dir,baselineReload(t,prepared),ledgerSource{},forecastSource{});require.NoError(t,err);parsed,err:=parseDBReport(held);require.NoError(t,err);require.Empty(t,parsed.Timeline);require.Equal(t,"independent-order",parsed.NextFocus);require.Contains(t,parsed.StatusSync.PendingTimelines,"1");require.Zero(t,parsed.StatusSync.TimelineAppliedLine)
 root,err:=decodeReportValue(held);require.NoError(t,err);selector:=syncUpdate{Target:syncTargetReport,ID:"current"};expected:=map[string]syncOwnership{}
 for _,field:=range []string{"timeline","timeline_as_of","timeline_state"} {current,err:=baselineCurrent(root.(map[string]any),selector,field);require.NoError(t,err);expected[field]=current}
 released,err:=adoptBaselineCurrent(held,selector,expected);require.NoError(t,err);finished,_,err:=projectLogStatus(dir,baselineReload(t,released),ledgerSource{},forecastSource{});require.NoError(t,err);parsed,err=parseDBReport(finished);require.NoError(t,err);require.Len(t,parsed.Timeline,8);require.Equal(t,"pending earlier",parsed.Timeline[0].Normal);require.Empty(t,parsed.StatusSync.PendingTimelines);require.Equal(t,1,parsed.StatusSync.TimelineAppliedLine)
}
