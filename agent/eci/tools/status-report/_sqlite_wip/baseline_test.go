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
)

// baselineNativeFixture keeps the existing complete report semantics in native JSON.
//
// Example: Unrelated lanes and owner fields survive construction.
func baselineNativeFixture(t *testing.T, report string) []byte {t.Helper();data,err:=replayYAMLToReportJSON([]byte(report));require.NoError(t,err);return data}

// baselineReload exercises both the wire encoder and the actual replay decoder.
//
// Example: Held cannot disappear or turn into absence across process boundaries.
func baselineReload(t *testing.T, data []byte) []byte {t.Helper();value,err:=decodeReportValue(data);require.NoError(t,err);raw,err:=json.Marshal(value);require.NoError(t,err);var document struct{State syncState `yaml:"status_sync"`};require.NoError(t,yaml.Unmarshal(raw,&document));require.NotNil(t,document.State.Applied);return raw}

// baselineSources gives the real replay engine a completed lane and inactive forecast.
//
// Example: Only owner protection, rather than invalid canonical work, holds back closure.
func baselineSources(t *testing.T) (ledgerSource,forecastSource) {t.Helper();var ledger ledgerSource;var forecasts forecastSource;require.NoError(t,yaml.Unmarshal([]byte(strings.Replace(linkedLedger,"owner: one, state: active, next_action: Finish","owner: one, state: completed, completed_at: '2026-11-02T15:25:00Z', next_action: Finish",1)),&ledger));require.NoError(t,yaml.Unmarshal([]byte(strings.Replace(linkedForecasts,"status: active","status: inactive",1)),&forecasts));return ledger,forecasts}

// TestBaselineHeldImplicitForecastHoldsBackAllReplays checks the missing historical A case.
//
// Example: Retire protects owner B without reconstructing A from current sources.
func TestBaselineHeldImplicitForecastHoldsBackAllReplays(t *testing.T) {
 t.Parallel()
 reopen:=`{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"implementation_status":"IN PROGRESS"}}]}}}`+"\n"
 close:=`{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}`
 ownerB:=strings.Replace(linkedReport,"forecast_ref: 'forecasts.yaml#/records/first', outcome: First result","forecast_ref: 'forecasts.yaml#/records/first', outcome: Owner-B",1)
 prepared,err:=prepareBaseline(baselineNativeFixture(t,ownerB),[]byte(reopen),map[string]baselineMode{"1/u/0":baselineRetire});require.NoError(t,err)
 prepared=baselineReload(t,prepared)
 var state struct{State syncState `yaml:"status_sync"`};require.NoError(t,yaml.Unmarshal(prepared,&state))
 selector:=syncUpdate{Target:syncTargetLane,ID:"1",WorkRef:"project-understanding.yaml#/records/work:first",RequiredEnvironment:syncEnvironmentImplementation}
 require.NotContains(t,string(prepared),`"value":"historical-A"`)
 ledger,forecasts:=baselineSources(t)
 var terminal syncUpdate;require.NoError(t,json.Unmarshal([]byte(close),&terminal))
 _,_,applied:=applyLaneSyncUpdate(prepared,terminal,ledger,forecasts,syncGroupPriorValues(state.State,terminal));require.False(t,applied)
 require.Equal(t,syncOwnershipHeld,state.State.Applied[syncTargetKey(selector,"forecast")].Kind)
 log:=reopen+`{"event":"lifecycle_change","details":{"status_update":{"updates":[`+close+`,{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"owner":"independent"}}]}}}`+"\n"
 correction:=strings.Replace(close,`"evidence":"accepted"`,`"corrects":2,"corrects_ordinal":0,"evidence":"accepted"`,1)
 log+=`{"event":"correction","details":{"status_update":{"updates":[`+correction+`]}}}`+"\n"
 log+=reopen
 log+=`{"event":"lifecycle_change","details":{"status_update":{"updates":[`+close+`]}}}`+"\n"
 dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(log),0600))
 replayed,_,err:=projectLogStatus(dir,prepared,ledger,forecasts);require.NoError(t,err)
 parsed,err:=parseDBReport(replayed);require.NoError(t,err)
 require.Equal(t,"Owner-B",parsed.Lanes[0].Forecast.Outcome);require.Equal(t,statusInProgress,parsed.Lanes[0].ImplementationStatus);require.Equal(t,"independent",parsed.Lanes[0].Owner);require.Len(t,parsed.StatusSync.Groups,4)
 replayed=baselineReload(t,replayed)
 value,err:=decodeReportValue(replayed);require.NoError(t,err);root:=value.(map[string]any)
 expected:=map[string]syncOwnership{}
 for _,field:=range []string{"forecast","completed_at","implementation_status"} {current,err:=baselineCurrent(root,selector,field);require.NoError(t,err);expected[field]=current}
 wrong:=map[string]syncOwnership{"forecast":scalarSyncStoredValue(`{"outcome":"wrong"}`)}
 _,err=adoptBaselineCurrent(replayed,selector,wrong);require.Error(t,err)
 adopted,err:=adoptBaselineCurrent(replayed,selector,expected);require.NoError(t,err)
 finished,_,err:=projectLogStatus(dir,adopted,ledger,forecasts);require.NoError(t,err)
 parsed,err=parseDBReport(finished);require.NoError(t,err);require.Equal(t,statusClosed,parsed.Lanes[0].ImplementationStatus);require.Nil(t,parsed.Lanes[0].Forecast)
 t.Log("held implicit forecast survives reload/direct/chain/correction/new-terminal attempts; independent field advances; explicit prospective adoption permits closure")
}

// TestBaselineDeletedAdditionAndPendingRemainDifferent checks explicit owner intent.
//
// Example: A consumed deleted task stays absent while a genuinely pending task appears.
func TestBaselineDeletedAdditionAndPendingRemainDifferent(t *testing.T) {
 t.Parallel()
 addition:=func(id string) string{return `{"event":"requirement_change","details":{"status_update":{"additions":[{"target":"task","record":{"id":"`+id+`","title":"New","priority":"high","impact":"Useful","effort":{"min_minutes":10,"max_minutes":20,"kind":"elapsed","basis":"guess"},"status":"NEW"}}]}}}`+"\n"}
 log:=addition("3")+addition("4")
 prepared,err:=prepareBaseline(baselineNativeFixture(t,linkedReport),[]byte(log),map[string]baselineMode{"1/a/0":baselineRetire,"2/a/0":baselinePending});require.NoError(t,err)
 dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(log),0600))
 result,_,err:=projectLogStatus(dir,baselineReload(t,prepared),ledgerSource{},forecastSource{});require.NoError(t,err)
 parsed,err:=parseDBReport(result);require.NoError(t,err);require.Len(t,parsed.Tasks,3);require.Equal(t,taskID("4"),parsed.Tasks[2].ID)
 require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(log+addition("3")),0600))
 result,_,err=projectLogStatus(dir,result,ledgerSource{},forecastSource{});require.NoError(t,err)
 parsed,err=parseDBReport(result);require.NoError(t,err);require.Len(t,parsed.Tasks,3);require.Equal(t,taskID("4"),parsed.Tasks[2].ID);require.NotEmpty(t,parsed.StatusSync.PendingAdditions)
 selector:=syncUpdate{Target:syncTargetTask,ID:"3"}
 released,err:=adoptBaselineCurrent(result,selector,map[string]syncOwnership{"record":syncAbsentValue});require.NoError(t,err)
 result,_,err=projectLogStatus(dir,released,ledgerSource{},forecastSource{});require.NoError(t,err)
 parsed,err=parseDBReport(result);require.NoError(t,err);require.Len(t,parsed.Tasks,4)
}

// TestBaselineNullAndAbsenceStayTyped checks prospective guards, not fabricated history.
//
// Example: Owner changes from null to the string null must remain protected.
func TestBaselineNullAndAbsenceStayTyped(t *testing.T) {
 t.Parallel()
 report:=strings.Replace(linkedReport,"status: IN PROGRESS, work_refs:","status: IN PROGRESS, owner: null, work_refs:",1)
 log:=`{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"historical"}},{"target":"task","id":"2","fields":{"owner":"historical"}}]}}}`+"\n"
 prepared,err:=prepareBaseline(baselineNativeFixture(t,report),[]byte(log),map[string]baselineMode{"1/u/0":baselineAdopt,"1/u/1":baselineAdopt});require.NoError(t,err)
 var state struct{State syncState `yaml:"status_sync"`};require.NoError(t,yaml.Unmarshal(baselineReload(t,prepared),&state))
 require.Equal(t,syncNullValue,state.State.Applied[syncTargetKey(syncUpdate{Target:syncTargetTask,ID:"1"},"owner")]);require.Equal(t,syncAbsentValue,state.State.Applied[syncTargetKey(syncUpdate{Target:syncTargetTask,ID:"2"},"owner")])
 edited,err:=applyReportEdit(prepared,reportEdit{Ops:[]reportEditOperation{{Op:"set",Path:[]string{"tasks","0","owner"},Expect:reportEditExpectation{Kind:"null"},Value:json.RawMessage(`"null"`)},{Op:"set",Path:[]string{"tasks","1","owner"},Expect:reportEditExpectation{Kind:"absent"},Value:json.RawMessage(`"owner"`)}}});require.NoError(t,err)
 next:=`{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"later"}},{"target":"task","id":"2","fields":{"owner":"later"}}]}}}`+"\n"
 dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(log+next),0600))
 result,_,err:=projectLogStatus(dir,edited,ledgerSource{},forecastSource{});require.NoError(t,err)
 parsed,err:=parseDBReport(result);require.NoError(t,err);require.Equal(t,"null",parsed.Tasks[0].Owner);require.Equal(t,"owner",parsed.Tasks[1].Owner);require.Len(t,parsed.StatusSync.Groups,2)
 unchanged,_,err:=projectLogStatus(dir,prepared,ledgerSource{},forecastSource{});require.NoError(t,err)
 parsed,err=parseDBReport(unchanged);require.NoError(t,err);require.Equal(t,"later",parsed.Tasks[0].Owner);require.Equal(t,"later",parsed.Tasks[1].Owner)
}

// TestBaselineSameLineCorrectionAndRetraction checks dependency identity through reload.
//
// Example: A correction targets ordinal one, not another update on the same line.
func TestBaselineSameLineCorrectionAndRetraction(t *testing.T) {
 t.Parallel()
 log:=`{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"progress","id":"n","fields":{"text":"old-zero","evidence":"e0"}},{"target":"progress","id":"n","fields":{"text":"old-one","evidence":"e1"}}]}}}`+"\n"
 prepared,err:=prepareBaseline(baselineNativeFixture(t,linkedReport),[]byte(log),map[string]baselineMode{"1/u/0":baselinePending,"1/u/1":baselinePending});require.NoError(t,err)
 log+=`{"event":"correction","details":{"status_update":{"updates":[{"target":"progress","id":"n","corrects":1,"corrects_ordinal":1,"fields":{"text":"corrected","evidence":"ec"}}]}}}`+"\n"
 dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(log),0600))
 result,_,err:=projectLogStatus(dir,baselineReload(t,prepared),ledgerSource{},forecastSource{});require.NoError(t,err);require.Contains(t,string(result),"corrected");require.NotContains(t,string(result),"old-one")
 log+=`{"event":"correction","details":{"status_update":{"updates":[{"target":"progress","id":"n","corrects":2,"retract":true}]}}}`+"\n"
 require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte(log),0600))
 result,_,err=projectLogStatus(dir,baselineReload(t,result),ledgerSource{},forecastSource{});require.NoError(t,err)
 parsed,err:=parseDBReport(result);require.NoError(t,err);require.Empty(t,parsed.StatusSync.Groups)
 var doc map[string]any;require.NoError(t,yaml.Unmarshal(result,&doc));require.NotContains(t,fmt.Sprint(doc["progress"]),"corrected")
}

// TestBaselineInitialLateAndConflicts checks actual SQLite rollback and both creation paths.
//
// Example: A changed log cannot publish even part of a newly initialized schema.
func TestBaselineInitialLateAndConflicts(t *testing.T) {
 t.Parallel()
 for _,initial:=range []bool{true,false} {for _,conflict:=range []string{"none","log","identity","generation","report"} {t.Run(fmt.Sprintf("initial=%t/%s",initial,conflict),func(t *testing.T){
  dir:=t.TempDir();log:=[]byte("{}\n");require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),log,0600));captured,err:=captureReplayLog(dir);require.NoError(t,err)
  prepared,err:=prepareBaseline(baselineNativeFixture(t,linkedReport),log,map[string]baselineMode{});require.NoError(t,err);prepared=baselineReload(t,prepared)
  db,err:=sql.Open("sqlite",filepath.Join(dir,"test.db")+"?_txlock=immediate");require.NoError(t,err);defer db.Close()
  observation:=baselineObservation{Initial:initial,DatabaseID:"native",Generation:0}
  preexisting:=!initial||conflict=="identity"||conflict=="report"
  if preexisting {_,err=db.Exec("CREATE TABLE ledger_meta(id INTEGER PRIMARY KEY,database_id TEXT,generation INTEGER);CREATE TABLE report(id INTEGER PRIMARY KEY,document_json TEXT,revision INTEGER);INSERT INTO ledger_meta VALUES(1,'native',0)");require.NoError(t,err)}
  switch conflict {
  case "log":require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),[]byte("{}\n{}\n"),0600))
  case "identity":_,err=db.Exec("UPDATE ledger_meta SET database_id='replacement'");require.NoError(t,err)
  case "generation":observation.Generation=1
  case "report":_,err=db.Exec("INSERT INTO report VALUES(1,'{}',3)");require.NoError(t,err)
  }
  err=publishBaseline(context.Background(),db,dir,observation,prepared,captured)
  var tables int;require.NoError(t,db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table'").Scan(&tables))
  if conflict!="none" {
   require.Error(t,err)
   if !preexisting {require.Zero(t,tables)} else {
    require.Equal(t,2,tables)
    var count int;require.NoError(t,db.QueryRow("SELECT count(*) FROM report").Scan(&count))
    if conflict=="report" {require.Equal(t,1,count);var doc string;require.NoError(t,db.QueryRow("SELECT document_json FROM report").Scan(&doc));require.Equal(t,"{}",doc)} else {require.Zero(t,count)}
    if conflict=="identity" {var identity string;require.NoError(t,db.QueryRow("SELECT database_id FROM ledger_meta").Scan(&identity));require.Equal(t,"replacement",identity)}
   }
   return
  }
  require.NoError(t,err);var saved string;var generation int;require.NoError(t,db.QueryRow("SELECT document_json FROM report").Scan(&saved));require.JSONEq(t,string(prepared),saved);require.NoError(t,db.QueryRow("SELECT generation FROM ledger_meta").Scan(&generation));require.Equal(t,1,generation)
  next:=append(append([]byte{},log...),[]byte(`{"event":"decision","details":{"status_update":{"updates":[{"target":"report","id":"current","fields":{"next_focus":"after-creation"}}]}}}`+"\n")...)
  require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),next,0600))
  replayed,_,err:=projectLogStatus(dir,baselineReload(t,[]byte(saved)),ledgerSource{},forecastSource{});require.NoError(t,err)
  parsed,err:=parseDBReport(replayed);require.NoError(t,err);require.Equal(t,"after-creation",parsed.NextFocus);require.Equal(t,2,parsed.StatusSync.Cursor)
 })}}
}

// TestBaselineCurrentReportOccurrenceReplays exercises a supplied report update.
//
// Example: An explicit pending decision applies independent current report fields.
func TestBaselineCurrentReportOccurrenceReplays(t *testing.T) {
 t.Parallel()
 log:=[]byte(`{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"report","id":"current","fields":{"state":"review","next_focus":"verify"}}]}}}`+"\n")
 prepared,err:=prepareBaseline(baselineNativeFixture(t,linkedReport),log,map[string]baselineMode{"1/u/0":baselinePending});require.NoError(t,err)
 dir:=t.TempDir();require.NoError(t,os.WriteFile(filepath.Join(dir,"high_level_log.jsonl"),log,0600));result,_,err:=projectLogStatus(dir,baselineReload(t,prepared),ledgerSource{},forecastSource{});require.NoError(t,err)
 parsed,err:=parseDBReport(result);require.NoError(t,err);require.Equal(t,"review",parsed.State);require.Equal(t,"verify",parsed.NextFocus);require.Empty(t,parsed.StatusSync.Groups)
}
