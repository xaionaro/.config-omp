package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSyncAdditionPreservesExistingRecords verifies complete additions and duplicate protection.
//
// Example: A new task and its review preserve prior comments and entries.
func TestSyncAdditionPreservesExistingRecords(t *testing.T) {
	data := []byte(strings.Replace(valid, "reviews: []", "reviews: [] # existing reviews", 1))
	task := syncAddition{Target: syncTargetTask, Record: json.RawMessage(`{"id":"3","title":"New","priority":"high","impact":"Useful","effort":{"min_minutes":1,"max_minutes":2,"kind":"work","basis":"guess"},"status":"NEW"}`)}
	got, ok := applySyncAddition(data, task)
	if !ok || !strings.Contains(string(got), "# existing reviews") || !strings.Contains(string(got), `"New"`) {
		t.Fatalf("task addition failed or lost content: %s", got)
	}
	if _, ok := applySyncAddition(got, task); ok {
		t.Fatal("duplicate task accepted")
	}
	review := syncAddition{Target: syncTargetReview, Record: json.RawMessage(`{"task_id":"3","reporter":"A","critic":"B","rounds":1,"estimate":"checked","impact":"checked","priority":"checked","unresolved":"none"}`)}
	got, ok = applySyncAddition(got, review)
	if !ok || !strings.Contains(string(got), "# existing reviews") {
		t.Fatalf("review addition failed: %s", got)
	}
	if _, ok := applySyncAddition(got, review); ok {
		t.Fatal("duplicate review accepted")
	}
}

// TestSyncAdditionRejectsIncompleteRecords verifies no fields are fabricated.
//
// Example: Missing critic or effort leaves the report unchanged.
func TestSyncAdditionRejectsIncompleteRecords(t *testing.T) {
	for _, addition := range []syncAddition{
		{Target: syncTargetTask, Record: json.RawMessage(`{"id":"3","title":"New","priority":"high","impact":"Useful"}`)},
		{Target: syncTargetTask, Record: json.RawMessage(`{"id":"3","title":"New","priority":"high","impact":"Useful","effort":{"min_minutes":1,"max_minutes":2,"kind":"work","basis":"guess"},"status":"CLOSED"}`)},
		{Target: syncTargetReview, Record: json.RawMessage(`{"task_id":"1.1","reporter":"A","rounds":1,"estimate":"checked","impact":"checked","priority":"checked","unresolved":"none"}`)},
		{Target: syncTargetReview, Record: json.RawMessage(`{"task_id":"1.1","reporter":"A","critic":"B","rounds":6,"estimate":"checked","impact":"checked","priority":"checked","unresolved":"none"}`)},
	} {
		data := []byte(valid)
		got, ok := applySyncAddition(data, addition)
		if ok || string(got) != string(data) {
			t.Fatalf("invalid addition accepted: %s", addition.Record)
		}
	}
}

// TestSyncReviewAdditionCreatesMissingList verifies optional reviews can be introduced.
//
// Example: A report without reviews receives its first supplied review.
func TestSyncReviewAdditionCreatesMissingList(t *testing.T) {
	data := []byte(strings.Replace(valid, "reviews: []", "# reviews were omitted", 1))
	addition := syncAddition{Target: syncTargetReview, Record: json.RawMessage(`{"task_id":"1.1","reporter":"A","critic":"B","rounds":1,"estimate":"checked","impact":"checked","priority":"checked","unresolved":"none"}`)}
	got, ok := applySyncAddition(data, addition)
	if !ok || !strings.Contains(string(got), "# reviews were omitted") {
		t.Fatalf("review list not added: %s", got)
	}
	if _, ok := applySyncAddition(got, addition); ok {
		t.Fatal("duplicate review accepted")
	}
}

// TestSyncAdditionGroupRequiresMatchingEstimate verifies atomic novel-priority projection.
//
// Example: A task and its complete estimate publish together.
func TestSyncAdditionGroupRequiresMatchingEstimate(t *testing.T) {
	task := syncAddition{Target: syncTargetTask, Record: json.RawMessage(`{"id":"3","title":"New","priority":"urgent","impact":"Useful","effort":{"min_minutes":1,"max_minutes":2,"kind":"work","basis":"guess"},"status":"NEW"}`)}
	estimate := syncAddition{Target: syncTargetPriorityEstimate, Record: json.RawMessage(`{"priority":"urgent","as_of":"2026-11-02T15:24:00Z","min_minutes":1,"max_minutes":2,"basis":"guess","assumptions":"none","dependencies":"none","critical_path":"task"}`)}
	data := []byte(valid)
	got, ok := applySyncAdditionGroup(data, []syncAddition{task, estimate})
	if !ok || !strings.Contains(string(got), `"urgent"`) {
		t.Fatalf("valid group rejected: %s", got)
	}
	for _, additions := range [][]syncAddition{{task}, {task, {Target: syncTargetPriorityEstimate, Record: json.RawMessage(`{"priority":"urgent"}`)}}, {task, {Target: syncTargetPriorityEstimate, Record: json.RawMessage(`{"priority":"other","as_of":"2026-11-02T15:24:00Z","min_minutes":1,"max_minutes":2,"basis":"guess","assumptions":"none","dependencies":"none","critical_path":"task"}`)}}} {
		got, ok := applySyncAdditionGroup(data, additions)
		if ok || string(got) != string(data) {
			t.Fatalf("invalid group partially published: %s", got)
		}
	}
}
