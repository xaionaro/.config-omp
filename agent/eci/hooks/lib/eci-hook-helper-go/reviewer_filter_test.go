package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

type reviewerFilterTestResult struct {
	Verdict    string `json:"verdict"`
	Violations []struct {
		Rule     string `json:"rule"`
		Evidence string `json:"evidence"`
	} `json:"violations"`
}

func reviewerFilterTestRun(t *testing.T, stdin string, args ...string) (int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runReviewerFilter(args, strings.NewReader(stdin), &stdout, &stderr)
	return code, stdout.String()
}

func reviewerFilterTestDecode(t *testing.T, out string) reviewerFilterTestResult {
	t.Helper()
	var result reviewerFilterTestResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	return result
}

func reviewerFilterTestRules(result reviewerFilterTestResult) []string {
	rules := make([]string, 0, len(result.Violations))
	for _, v := range result.Violations {
		rules = append(rules, v.Rule)
	}
	return rules
}

func TestReviewerFilterDropsFabricatedRuleAndPassesVerdict(t *testing.T) {
	stdin := `{"verdict":"fail","violations":[{"rule":"Always whistle three times before stopping.","evidence":"the agent skipped the whistle"}]}`
	code, out := reviewerFilterTestRun(t, stdin, "Commit the completed changes before stopping.\n", "", "0.15")
	if code != statusOK {
		t.Fatalf("exit code = %d, want %d", code, statusOK)
	}
	result := reviewerFilterTestDecode(t, out)
	if result.Verdict != "pass" {
		t.Fatalf("verdict = %q, want pass", result.Verdict)
	}
	if len(result.Violations) != 0 {
		t.Fatalf("violations = %v, want none", reviewerFilterTestRules(result))
	}
}

func TestReviewerFilterKeepsGroundedRuleAndDropsFabricatedRule(t *testing.T) {
	stdin := `{"verdict":"fail","violations":[` +
		`{"rule":"Always whistle three times before stopping.","evidence":"the agent skipped the whistle"},` +
		`{"rule":"Commit the completed changes before stopping.","evidence":"changes were left uncommitted"}]}`
	code, out := reviewerFilterTestRun(t, stdin, "Commit the completed changes before stopping.\n", "", "0.15")
	if code != statusOK {
		t.Fatalf("exit code = %d, want %d", code, statusOK)
	}
	result := reviewerFilterTestDecode(t, out)
	if result.Verdict != "fail" {
		t.Fatalf("verdict = %q, want fail", result.Verdict)
	}
	rules := reviewerFilterTestRules(result)
	if len(rules) != 1 || rules[0] != "Commit the completed changes before stopping." {
		t.Fatalf("kept rules = %v, want only the grounded rule", rules)
	}
}

func TestReviewerFilterThresholdBoundary(t *testing.T) {
	// The rule has two word trigrams; the corpus contains one of them, so the
	// grounding ratio is exactly 0.5.
	stdin := `{"verdict":"fail","violations":[{"rule":"Alpha beta gamma delta","evidence":"alpha beta gamma"}]}`
	corpus := "Alpha beta gamma\n"

	code, out := reviewerFilterTestRun(t, stdin, corpus, "", "0.5")
	if code != statusOK {
		t.Fatalf("threshold 0.5: exit code = %d, want %d", code, statusOK)
	}
	if rules := reviewerFilterTestRules(reviewerFilterTestDecode(t, out)); len(rules) != 1 {
		t.Fatalf("threshold 0.5 (ratio at threshold): kept rules = %v, want the rule kept", rules)
	}

	code, out = reviewerFilterTestRun(t, stdin, corpus, "", "0.51")
	if code != statusOK {
		t.Fatalf("threshold 0.51: exit code = %d, want %d", code, statusOK)
	}
	result := reviewerFilterTestDecode(t, out)
	if len(result.Violations) != 0 || result.Verdict != "pass" {
		t.Fatalf("threshold 0.51 (ratio below threshold): verdict = %q, violations = %v; want pass and none",
			result.Verdict, reviewerFilterTestRules(result))
	}
}

func TestReviewerFilterKeepsUserHistoryAgreement(t *testing.T) {
	// The rule shares no trigram with the corpus, so only USER_HISTORY agreement
	// (with evidence grounded in CURRENT_TURN) can keep it.
	stdin := `{"verdict":"fail","violations":[{"rule":"Integration tests must pass before commit.","evidence":"integration tests failed"}]}`
	currentTurn := "## CURRENT_TURN\nThe integration tests failed on commit.\n"
	history := "## USER_HISTORY\n<entry>Run the integration tests before commit.</entry>\n\n"

	code, out := reviewerFilterTestRun(t, stdin, "", history+currentTurn, "0.15")
	if code != statusOK {
		t.Fatalf("with history: exit code = %d, want %d", code, statusOK)
	}
	result := reviewerFilterTestDecode(t, out)
	if result.Verdict != "fail" || len(result.Violations) != 1 {
		t.Fatalf("with history: verdict = %q, violations = %v; want fail with the rule kept",
			result.Verdict, reviewerFilterTestRules(result))
	}

	code, out = reviewerFilterTestRun(t, stdin, "", currentTurn, "0.15")
	if code != statusOK {
		t.Fatalf("without history: exit code = %d, want %d", code, statusOK)
	}
	result = reviewerFilterTestDecode(t, out)
	if result.Verdict != "pass" || len(result.Violations) != 0 {
		t.Fatalf("without history: verdict = %q, violations = %v; want pass with none",
			result.Verdict, reviewerFilterTestRules(result))
	}
}

func TestReviewerFilterRejectsMalformedInput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runReviewerFilter([]string{"", "", "0.15"}, strings.NewReader("not json"), &stdout, &stderr)
	if code != statusRejected {
		t.Fatalf("exit code = %d, want %d", code, statusRejected)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty on rejection", stdout.String())
	}
}
