package main

import (
 "encoding/json"
 "os"
 "path/filepath"
 "strings"
 "testing"

 "github.com/stretchr/testify/require"
 "gopkg.in/yaml.v3"
)

// TestBaselineRejectsInvalidEventUpdate prevents consuming malformed operations as history.
//
// Example: A correction without a predecessor cannot be retired as a valid update.
func TestBaselineRejectsInvalidEventUpdate(t *testing.T) {
 t.Parallel()
 log:=[]byte(`{"event":"correction","details":{"status_update":{"updates":[{"target":"report","id":"current","fields":{"next_focus":"bad"}}]}}}`+"\n")
 _,err:=prepareBaseline(baselineNativeFixture(t,linkedReport),log,map[string]baselineMode{"1/u/0":baselineRetire})
 require.Error(t,err)
}

// TestBaselinePendingBeforeRetireSurvives checks consumed versus projected locators.
//
// Example: Retiring a later occurrence does not supersede an explicitly pending earlier one.
func TestBaselinePendingBeforeRetireSurvives(t *testing.T) {
 t.Parallel()
 t.Run("group",func(t *testing.T){
  first:=`{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"pending-A"}}]}}}`+"\n"
  second:=strings.Replace(first,"pending-A","retired",1)
  report:=strings.Replace(linkedReport,"status: IN PROGRESS, work_refs:","status: IN PROGRESS, owner: B, work_refs:",1)
  prepared,err:=prepareBaseline(baselineNativeFixture(t,report),[]byte(first+second),map[string]baselineMode{"1/u/0":baselinePending,"2/u/0":baselineRetire});require.NoError(t,err)
  dir:=t.TempDir();log:=first+second+`{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"report","id":"current","fields":{"next_focus":"independent"}}]}}}`+"\n";require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(log),0600))
  held,_,err:=projectLogStatus(dir,baselineReload(t,prepared),ledgerSource{},forecastSource{});require.NoError(t,err);parsed,err:=parseDBReport(held);require.NoError(t,err);require.Equal(t,"B",parsed.Tasks[0].Owner);require.Equal(t,"independent",parsed.NextFocus);require.Contains(t,parsed.StatusSync.Groups,syncGroupKey(1,0))
  released,err:=adoptBaselineCurrent(held,syncUpdate{Target:syncTargetTask,ID:"1"},map[string]syncOwnership{"owner":scalarSyncStoredValue("B")});require.NoError(t,err);finished,_,err:=projectLogStatus(dir,baselineReload(t,released),ledgerSource{},forecastSource{});require.NoError(t,err);parsed,err=parseDBReport(finished);require.NoError(t,err);require.Equal(t,"pending-A",parsed.Tasks[0].Owner);require.Empty(t,parsed.StatusSync.Groups)
 })
 t.Run("addition",func(t *testing.T){
  log:=baselineTaskEvent("3")+baselineTaskEvent("3")+baselineTaskEvent("4")
  prepared,err:=prepareBaseline(baselineNativeFixture(t,linkedReport),[]byte(log),map[string]baselineMode{"1/a/0":baselinePending,"2/a/0":baselineRetire,"3/a/0":baselinePending});require.NoError(t,err)
  dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(log),0600));held,_,err:=projectLogStatus(dir,baselineReload(t,prepared),ledgerSource{},forecastSource{});require.NoError(t,err);parsed,err:=parseDBReport(held);require.NoError(t,err);require.Len(t,parsed.Tasks,3);require.Equal(t,taskID("4"),parsed.Tasks[2].ID);require.Len(t,parsed.StatusSync.PendingAdditions,1)
  for _,pending:=range parsed.StatusSync.PendingAdditions {require.Equal(t,1,pending.Line);require.Equal(t,"3",pending.ID)}
  released,err:=adoptBaselineCurrent(held,syncUpdate{Target:syncTargetTask,ID:"3"},map[string]syncOwnership{"record":syncAbsentValue});require.NoError(t,err);finished,_,err:=projectLogStatus(dir,baselineReload(t,released),ledgerSource{},forecastSource{});require.NoError(t,err);parsed,err=parseDBReport(finished);require.NoError(t,err);require.Len(t,parsed.Tasks,4);require.Empty(t,parsed.StatusSync.PendingAdditions)
 })
}

// TestBaselinePendingRootPayloadAndOrdinals checks the actual root pending form.
//
// Example: Completion/retirement and reopening keep their full operations and distinct ordinals.
func TestBaselinePendingRootPayloadAndOrdinals(t *testing.T) {
 t.Parallel()
 forecast1:=`{"forecast_ref":"forecasts.yaml#/records/f1","outcome":"First","target":"2026-11-02T16:00:00Z","assessment":"complete"}`
 completion1:=`{"work_ref":"project-understanding.yaml#/records/w1","outcome":"First","completed_at":"2026-11-02T15:00:00Z"}`
 forecast2:=`{"forecast_ref":"forecasts.yaml#/records/f2","outcome":"Second","target":"2026-11-02T17:00:00Z","assessment":"complete"}`
 completion2:=`{"work_ref":"project-understanding.yaml#/records/w2","outcome":"Second","completed_at":"2026-11-02T15:00:00Z"}`
 log:=`{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","evidence":"accepted","retire_forecast":true,"expected_prior":`+forecast1+`,"record":`+completion1+`},{"target":"root_forecast","evidence":"reopened","reopen_work_ref":"project-understanding.yaml#/records/w2","expected_prior":`+completion2+`,"record":`+forecast2+`}]}}}`+"\n"
 prepared,err:=prepareBaseline(baselineNativeFixture(t,valid+"root_forecasts: ["+forecast1+"]\nroot_completions: ["+completion2+"]\n"),[]byte(log),map[string]baselineMode{"1/a/0":baselinePending,"1/a/1":baselinePending});require.NoError(t,err);prepared=baselineReload(t,prepared)
 var saved struct{State syncState `yaml:"status_sync"`};require.NoError(t,yaml.Unmarshal(prepared,&saved));require.Len(t,saved.State.PendingRootAdditions,2);require.Empty(t,saved.State.PendingAdditions)
 first:=saved.State.PendingRootAdditions[syncGroupKey(1,0)];second:=saved.State.PendingRootAdditions[syncGroupKey(1,1)];require.True(t,first.Addition.RetireForecast);require.JSONEq(t,forecast1,string(first.Addition.ExpectedPrior));require.Equal(t,1,second.Locator.Ordinal);require.Equal(t,"project-understanding.yaml#/records/w2",second.Addition.ReopenWorkRef);require.JSONEq(t,completion2,string(second.Addition.ExpectedPrior))
 ledger:=ledgerSource{Records:map[string]sourceRecord{"w1":{Type:"work",OutcomeRefs:[]string{"o1"},Data:workData{State:workStateCompleted,CompletedAt:"2026-11-02T15:00:00Z",Roles:[]reportingRole{reportingRoleRoot},ForecastRef:"forecasts.yaml#/records/f1"}},"o1":{Type:"outcome",Outcome:"First"},"w2":{Type:"work",OutcomeRefs:[]string{"o2"},Data:workData{State:workStateActive,Roles:[]reportingRole{reportingRoleRoot},ForecastRef:"forecasts.yaml#/records/f2"}},"o2":{Type:"outcome",Outcome:"Second"}}}
 forecasts:=forecastSource{Records:map[string]map[string]any{"f1":{"kind":"root","status":"inactive"},"f2":{"kind":"root","status":"active"}}}
 dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(log),0600));finished,_,err:=projectLogStatus(dir,prepared,ledger,forecasts);require.NoError(t,err);parsed,err:=parseDBReport(finished);require.NoError(t,err);require.Len(t,parsed.RootCompletions,1);require.Len(t,parsed.RootForecasts,1);require.Equal(t,"project-understanding.yaml#/records/w1",parsed.RootCompletions[0].WorkRef);require.Equal(t,"forecasts.yaml#/records/f2",parsed.RootForecasts[0].ForecastRef);require.Empty(t,parsed.StatusSync.PendingRootAdditions)
}

// TestBaselinePendingLaneCompositeIdentity checks independently addressable lanes.
//
// Example: Two environments of one task/work keep separate source occurrences.
func TestBaselinePendingLaneCompositeIdentity(t *testing.T) {
 t.Parallel()
 addition:=`{"target":"lane","record":{"task_id":"1","work_ref":"project-understanding.yaml#/records/w","outcome":"Result","required_environment":"implementation","implementation_status":"NEW","test_status":"NEW","prod_status":"NEW","forecast":{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"complete"}}}`
 log:=`{"event":"requirement_change","details":{"status_update":{"additions":[`+addition+`,`+strings.Replace(addition,`"required_environment":"implementation"`,`"required_environment":"test"`,1)+`]}}}`+"\n"
 prepared,err:=prepareBaseline(baselineNativeFixture(t,valid),[]byte(log),map[string]baselineMode{"1/a/0":baselinePending,"1/a/1":baselinePending});require.NoError(t,err);prepared=baselineReload(t,prepared);var saved struct{State syncState `yaml:"status_sync"`};require.NoError(t,yaml.Unmarshal(prepared,&saved));require.Len(t,saved.State.PendingAdditions,2)
 ledger:=ledgerSource{Records:map[string]sourceRecord{"w":{Type:"work",OutcomeRefs:[]string{"o"},Data:workData{State:workStateActive,Roles:[]reportingRole{reportingRoleLane},ForecastRef:"forecasts.yaml#/records/f"}},"o":{Type:"outcome",Outcome:"Result"}}};forecasts:=forecastSource{Records:map[string]map[string]any{"f":{"kind":"lane","status":"active"}}}
 dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(log),0600));finished,_,err:=projectLogStatus(dir,prepared,ledger,forecasts);require.NoError(t,err);parsed,err:=parseDBReport(finished);require.NoError(t,err);require.Len(t,parsed.Lanes,2);require.NotEqual(t,parsed.Lanes[0].RequiredEnvironment,parsed.Lanes[1].RequiredEnvironment);require.Empty(t,parsed.StatusSync.PendingAdditions)
}

// TestBaselineDomainResolutionKeepsNarrativesAndNumericIDs checks current guard semantics.
//
// Example: Existing keyed progress and numeric 1.10 never become false absence.
func TestBaselineDomainResolutionKeepsNarrativesAndNumericIDs(t *testing.T) {
 t.Parallel()
 t.Run("narrative",func(t *testing.T){
  report:=strings.Replace(linkedReport,"progress: [Implementation started]","progress: [{subject_ref: n, text: B, evidence: eB}]",1)
  log:=`{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"progress","id":"n","fields":{"text":"A","evidence":"eA"}}]}}}`+"\n"
  prepared,err:=prepareBaseline(baselineNativeFixture(t,report),[]byte(log),map[string]baselineMode{"1/u/0":baselinePending});require.NoError(t,err);prepared=baselineReload(t,prepared);var saved struct{State syncState `yaml:"status_sync"`};require.NoError(t,yaml.Unmarshal(prepared,&saved));selector:=syncUpdate{Target:syncTargetProgress,ID:"n"};require.Equal(t,scalarSyncStoredValue("B"),saved.State.Applied[syncTargetKey(selector,"text")]);require.Equal(t,scalarSyncStoredValue("eB"),saved.State.Applied[syncTargetKey(selector,"evidence")])
  dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(log),0600));finished,_,err:=projectLogStatus(dir,prepared,ledgerSource{},forecastSource{});require.NoError(t,err);require.Contains(t,string(finished),`"text":"A"`)
  held,err:=prepareBaseline(baselineNativeFixture(t,report),[]byte(log),map[string]baselineMode{"1/u/0":baselineRetire});require.NoError(t,err);released,err:=adoptBaselineCurrent(baselineReload(t,held),selector,map[string]syncOwnership{"text":scalarSyncStoredValue("B"),"evidence":scalarSyncStoredValue("eB")});require.NoError(t,err);require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(log+log),0600));finished,_,err=projectLogStatus(dir,released,ledgerSource{},forecastSource{});require.NoError(t,err);require.Contains(t,string(finished),`"text":"A"`)
 })
 t.Run("numeric",func(t *testing.T){
  report:=strings.Replace(linkedReport,"top_priorities:","  - {id: 1.10, parent_id: '1', title: Ten, priority: high, impact: Useful, effort: {min_minutes: 1, max_minutes: 2, kind: elapsed, basis: guess}, status: NEW, owner: B}\n  - {id: 1.1, parent_id: '1', title: One, priority: high, impact: Useful, effort: {min_minutes: 1, max_minutes: 2, kind: elapsed, basis: guess}, status: NEW, owner: C}\ntop_priorities:",1)
  log:=`{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1.10","fields":{"owner":"A"}}]}}}`+"\n";prepared,err:=prepareBaseline(baselineNativeFixture(t,report),[]byte(log),map[string]baselineMode{"1/u/0":baselinePending});require.NoError(t,err);prepared=baselineReload(t,prepared);var saved struct{State syncState `yaml:"status_sync"`};require.NoError(t,yaml.Unmarshal(prepared,&saved));require.Equal(t,scalarSyncStoredValue("B"),saved.State.Applied[syncTargetKey(syncUpdate{Target:syncTargetTask,ID:"1.10"},"owner")])
  root,err:=decodeReportValue(prepared);require.NoError(t,err);absent,err:=baselineCurrent(root.(map[string]any),syncUpdate{Target:syncTargetTask,ID:"99"},"owner");require.NoError(t,err);require.Equal(t,syncAbsentValue,absent)
  dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(log),0600));finished,_,err:=projectLogStatus(dir,prepared,ledgerSource{},forecastSource{});require.NoError(t,err);parsed,err:=parseDBReport(finished);require.NoError(t,err);require.Equal(t,"A",parsed.Tasks[2].Owner);require.Equal(t,"C",parsed.Tasks[3].Owner);wire,err:=replayYAMLToReportJSON(finished);require.NoError(t,err);decoded,err:=decodeReportValue(wire);require.NoError(t,err);require.Equal(t,json.Number("1.10"),decoded.(map[string]any)["tasks"].([]any)[2].(map[string]any)["id"])
 })
 t.Run("duplicate",func(t *testing.T){
  report:=strings.Replace(linkedReport,"progress: [Implementation started]","progress: [{subject_ref: n, text: B}, {subject_ref: n, text: C}]",1);log:=`{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"progress","id":"n","fields":{"text":"A"}}]}}}`+"\n";_,err:=prepareBaseline(baselineNativeFixture(t,report),[]byte(log),map[string]baselineMode{"1/u/0":baselineAdopt});require.Error(t,err)
 })
}

// TestBaselinePendingPriorityRetirementKeepsTypedGuards checks the paired consumer.
//
// Example: Retiring high while reusing low preserves expected-prior payload and typed priority.
func TestBaselinePendingPriorityRetirementKeepsTypedGuards(t *testing.T) {
 t.Parallel()
 estimate:=`{"priority":"low","min_minutes":20,"max_minutes":40,"as_of":"2026-11-02T15:24:00Z","basis":"guess","assumptions":"none","dependencies":"none","critical_path":"latest"}`
 prior:=`{"priority":"high","min_minutes":20,"max_minutes":40,"as_of":"2026-11-02T15:24:00Z","basis":"guess","assumptions":"none","dependencies":"none","critical_path":"latest"}`
 report:=strings.Replace(linkedReport,"title: Second, priority: high","title: Second, priority: low",1)
 report=strings.Replace(report,"completion_estimate:","  - {priority: low, min_minutes: 20, max_minutes: 40, as_of: '2026-11-02T15:24:00Z', basis: guess, assumptions: none, dependencies: none, critical_path: latest}\ncompletion_estimate:",1)
 log:=`{"event":"decision","details":{"status_update":{"updates":[{"target":"task","id":"1","values":{"priority":"low"}}],"additions":[{"target":"priority_estimate","retire_priority":"high","expected_prior":`+prior+`,"record":`+estimate+`}]}}}`+"\n"
 prepared,err:=prepareBaseline(baselineNativeFixture(t,report),[]byte(log),map[string]baselineMode{"1/u/0":baselinePending,"1/a/0":baselinePending});require.NoError(t,err);prepared=baselineReload(t,prepared)
 var saved struct{State syncState `yaml:"status_sync"`};require.NoError(t,yaml.Unmarshal(prepared,&saved));require.Len(t,saved.State.PendingAdditions,1);for _,pending:=range saved.State.PendingAdditions {require.Equal(t,"high",pending.RetirePriority);require.JSONEq(t,prior,pending.ExpectedPrior)}
 dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(log),0600));finished,_,err:=projectLogStatus(dir,prepared,ledgerSource{},forecastSource{});require.NoError(t,err);parsed,err:=parseDBReport(finished);require.NoError(t,err);require.Equal(t,priorityLabel("low"),parsed.Tasks[0].Priority);require.Len(t,parsed.PriorityEstimates,1);require.Empty(t,parsed.StatusSync.PendingAdditions);require.Empty(t,parsed.StatusSync.Groups)
}

// TestBaselineAdoptedVersionIsAuthorizationNotProjection checks independent metadata roles.
//
// Example: Adopt-current can authorize a historical correction without creating a Latest receipt.
func TestBaselineAdoptedVersionIsAuthorizationNotProjection(t *testing.T) {
 t.Parallel()
 report:=strings.Replace(linkedReport,"status: IN PROGRESS, work_refs:","status: IN PROGRESS, owner: B, work_refs:",1)
 log:=`{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"historical-A"}}]}}}`+"\n"+`{"event":"correction","details":{"status_update":{"updates":[{"target":"task","id":"1","corrects":1,"fields":{"owner":"C"}}]}}}`+"\n"
 prepared,err:=prepareBaseline(baselineNativeFixture(t,report),[]byte(log),map[string]baselineMode{"1/u/0":baselineAdopt,"2/u/0":baselinePending});require.NoError(t,err);prepared=baselineReload(t,prepared)
 var saved struct{State syncState `yaml:"status_sync"`};require.NoError(t,yaml.Unmarshal(prepared,&saved));require.Empty(t,saved.State.Latest)
 dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(log),0600));finished,_,err:=projectLogStatus(dir,prepared,ledgerSource{},forecastSource{});require.NoError(t,err);parsed,err:=parseDBReport(finished);require.NoError(t,err);require.Equal(t,"C",parsed.Tasks[0].Owner);require.Empty(t,parsed.StatusSync.Groups)
}
