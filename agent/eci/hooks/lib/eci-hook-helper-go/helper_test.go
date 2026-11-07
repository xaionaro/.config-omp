package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runMode(t *testing.T, args []string, stdin string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	status := run(args, strings.NewReader(stdin), &out, &out)
	return status, out.String()
}

func TestStdinBoundsAndValidity(t *testing.T) {
	if status, out := runMode(t, []string{"stdin"}, `{"a":1}`); status != 0 || out != `{"a":1}` {
		t.Fatalf("valid stdin: status=%d out=%q", status, out)
	}
	if status, _ := runMode(t, []string{"stdin"}, strings.Repeat("a", inputBudget)); status != 0 {
		t.Fatalf("exactly-budget stdin rejected: %d", status)
	}
	if status, _ := runMode(t, []string{"stdin"}, strings.Repeat("a", inputBudget+1)); status != 2 {
		t.Fatalf("oversized stdin: want 2, got %d", status)
	}
	if status, _ := runMode(t, []string{"stdin"}, "a\x00b"); status != 2 {
		t.Fatalf("NUL stdin: want 2, got %d", status)
	}
	if status, _ := runMode(t, []string{"stdin"}, "\xff"); status != 2 {
		t.Fatalf("invalid UTF-8 stdin: want 2, got %d", status)
	}
}

func TestUnknownModeRejected(t *testing.T) {
	for _, args := range [][]string{{}, {"first-record"}, {"stdin", "extra"}, {"bogus"}} {
		if status, _ := runMode(t, args, ""); status != 2 {
			t.Fatalf("args %v: want 2, got %d", args, status)
		}
	}
}

func TestHookTranscriptPathRules(t *testing.T) {
	cases := map[string]bool{
		`{"transcript_path":"/a/b.jsonl"}`:   true,
		`{"transcript_path":""}`:             false,
		`{"transcript_path":5}`:              false,
		`{}`:                                 false,
		`null`:                               false,
		`[]`:                                 false,
		`not json`:                           false,
		`{"transcript_path":"/a/./b.jsonl"}`: true, // raw rule is applied later, not here
	}
	for input, wantOK := range cases {
		_, status := hookTranscriptPath(strings.NewReader(input))
		if (status == 0) != wantOK {
			t.Fatalf("input %q: status=%d wantOK=%v", input, status, wantOK)
		}
	}
}

func TestRawAbsolutePathRules(t *testing.T) {
	good := []string{"/a/b.jsonl", "/x"}
	bad := []string{"", "a/b", "/a//b", "/a/./b", "/a/../b", "/a/", "/", "/a\x7fb", "/a\x01", "/a\u0085b"}
	for _, p := range good {
		if !rawAbsolutePathAllowed(p) {
			t.Fatalf("want allowed: %q", p)
		}
	}
	for _, p := range bad {
		if rawAbsolutePathAllowed(p) {
			t.Fatalf("want rejected: %q", p)
		}
	}
}

func TestContainedTranscriptRejectsSymlinkAndOutside(t *testing.T) {
	base := t.TempDir()
	sessions := filepath.Join(base, "sessions")
	if err := os.MkdirAll(filepath.Join(sessions, "2024"), 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(sessions, "2024", "t.jsonl")
	if err := os.WriteFile(real, []byte(`{"type":"session"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside.jsonl")
	if err := os.WriteFile(outside, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(sessions, "2024", "link.jsonl")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	dirLink := filepath.Join(sessions, "linkdir")
	if err := os.Symlink(filepath.Join(sessions, "2024"), dirLink); err != nil {
		t.Fatal(err)
	}

	if f, ok := openContainedTranscript(sessions, real); !ok {
		t.Fatal("regular contained transcript rejected")
	} else {
		f.Close()
	}
	for _, raw := range []string{link, filepath.Join(dirLink, "t.jsonl"), outside, filepath.Join(sessions, "2024", "t.txt")} {
		if f, ok := openContainedTranscript(sessions, raw); ok {
			f.Close()
			t.Fatalf("want rejected: %s", raw)
		}
	}
	if status, _ := runMode(t, []string{"hook-transcript-first-record", sessions},
		`{"transcript_path":"`+link+`"}`); status != 2 {
		t.Fatalf("symlinked transcript: want exit 2, got %d", status)
	}
}

func TestTranscriptFirstRecord(t *testing.T) {
	sessions := t.TempDir()
	path := filepath.Join(sessions, "r.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"session"}`+"\nsecond\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, out := runMode(t, []string{"hook-transcript-first-record", sessions},
		`{"transcript_path":"`+path+`"}`)
	if status != 0 || out != `{"type":"session"}` {
		t.Fatalf("first record: status=%d out=%q", status, out)
	}
	bad := filepath.Join(sessions, "bad.jsonl")
	if err := os.WriteFile(bad, []byte("[1]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if status, _ := runMode(t, []string{"hook-transcript-first-record", sessions},
		`{"transcript_path":"`+bad+`"}`); status != 2 {
		t.Fatalf("non-object record: want 2, got %d", status)
	}
}

func TestFirstRecordPrefixBound(t *testing.T) {
	budget := inputBudget
	cases := []struct {
		name string
		in   string
		ok   bool
		want int
	}{
		{name: "oversize without newline", in: strings.Repeat("x", budget+1)},
		{name: "oversize with newline beyond bound", in: strings.Repeat("x", budget+1) + "\n"},
		{name: "newline inside bound", in: strings.Repeat("x", 1000) + "\n" + strings.Repeat("y", budget), ok: true, want: 1000},
		{name: "record exactly at limit", in: strings.Repeat("x", budget) + "\nz", ok: true, want: budget},
		{name: "record exactly at limit at EOF", in: strings.Repeat("x", budget), ok: true, want: budget},
		{name: "short record at EOF", in: "abc", ok: true, want: 3},
		{name: "empty input", in: "", ok: true, want: 0},
	}
	for _, tc := range cases {
		rec, ok := readFirstRecord(strings.NewReader(tc.in))
		if ok != tc.ok {
			t.Fatalf("%s: ok=%v want %v", tc.name, ok, tc.ok)
		}
		if ok && len(rec) != tc.want {
			t.Fatalf("%s: len=%d want %d", tc.name, len(rec), tc.want)
		}
	}
}

func TestMetadataParentIDInsideBound(t *testing.T) {
	const head = `{"type":"session","version":3,"id":"child","parentSession":"/x/parent.jsonl","padding":"`
	const tail = `"}`
	history := `{"padding":"` + strings.Repeat("h", 1<<20) + `"}` + "\n"
	for _, padding := range []int{4000, inputBudget - len(head) - len(tail) - 1} {
		record := head + strings.Repeat("p", padding) + tail
		path := filepath.Join(t.TempDir(), "m.jsonl")
		if err := os.WriteFile(path, []byte(record+"\n"+history), 0o644); err != nil {
			t.Fatal(err)
		}
		status, out := runMode(t, []string{"hook-transcript-first-record", filepath.Dir(path)},
			`{"transcript_path":"`+path+`"}`)
		if status != 0 || out != record {
			t.Fatalf("padding %d: status=%d out=%q", padding, status, out)
		}
	}
}

func TestMetadataRejectsOversizeWithoutFinalNewline(t *testing.T) {
	const head = `{"type":"session","version":3,"id":"child","parentSession":"/x/parent.jsonl","padding":"`
	const tail = `"}`
	record := head + strings.Repeat("p", 1<<20) + tail
	path := filepath.Join(t.TempDir(), "big.jsonl")
	if err := os.WriteFile(path, []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	status, out := runMode(t, []string{"hook-transcript-first-record", filepath.Dir(path)},
		`{"transcript_path":"`+path+`"}`)
	if status != 2 || out != "" {
		t.Fatalf("oversize without newline: status=%d out=%q", status, out)
	}
}

func TestTranscriptStatus(t *testing.T) {
	sessions := t.TempDir()
	write := func(rel, content string) string {
		t.Helper()
		path := filepath.Join(sessions, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	cases := []struct {
		name    string
		rel     string
		content string
		want    string
	}{
		{"plain session is main", "2024/main.jsonl", `{"type":"session","id":"s"}` + "\n" + `{"type":"message"}` + "\n", "main"},
		{"string parentSession at depth three is subagent", "2024/child/sub.jsonl", `{"type":"session","id":"s","parentSession":"/p.jsonl"}` + "\n", "subagent"},
		{"string parentSession at depth two is unknown", "2024/parent-two.jsonl", `{"type":"session","id":"s","parentSession":"/p.jsonl"}` + "\n", "unknown"},
		{"non-string parentSession is main", "2024/nonstring.jsonl", `{"type":"session","parentSession":5}` + "\n", "main"},
		{"session_init type at depth two is unknown", "2024/init.jsonl", `{"type":"session_init"}` + "\n", "unknown"},
		{"session_init customType at depth two is unknown", "2024/custom.jsonl", `{"type":"custom","customType":"session_init"}` + "\n", "unknown"},
		{"depth three without parentSession is unknown", "a/b/deep.jsonl", `{"type":"session","id":"s"}` + "\n", "unknown"},
		{"depth three with parentSession is subagent", "a/b/deep-child.jsonl", `{"type":"session","id":"s","parentSession":"/p.jsonl"}` + "\n", "subagent"},
		{"duplicate type is unknown", "2024/duptype.jsonl", `{"type":"session","parentSession":"/p.jsonl","type":"message"}` + "\n", "unknown"},
		{"oversize header is unknown", "2024/oversize.jsonl", `{"type":"session","pad":"` + strings.Repeat("x", inputBudget) + `"}` + "\n", "unknown"},
		{"long post-header record is main", "2024/longmsg.jsonl", `{"type":"session","id":"s"}` + "\n" + `{"type":"message","pad":"` + strings.Repeat("m", inputBudget) + `"}` + "\n", "main"},
		{"invalid line is unknown", "2024/bad.jsonl", `{"type":"session"}` + "\nnot json\n", "unknown"},
		{"empty file is unknown", "2024/empty.jsonl", "", "unknown"},
	}
	for _, tc := range cases {
		path := write(tc.rel, tc.content)
		status, out := runMode(t, []string{"hook-transcript-status", sessions}, `{"transcript_path":"`+path+`"}`)
		if status != 0 || out != tc.want+"\n" {
			t.Fatalf("%s: status=%d out=%q want %s", tc.name, status, out, tc.want)
		}
	}
	if got := classifyTranscript(sessions, ""); got != "unknown" {
		t.Fatalf("empty raw path: want unknown, got %s", got)
	}
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, []byte(`{"type":"session"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(sessions, "2024", "link.jsonl")
	if err := os.Symlink(write("2024/target.jsonl", `{"type":"session"}`+"\n"), link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, outside} {
		status, out := runMode(t, []string{"hook-transcript-status", sessions}, `{"transcript_path":"`+path+`"}`)
		if status != 0 || out != "unknown\n" {
			t.Fatalf("%s: status=%d out=%q", path, status, out)
		}
	}
}

func TestSecurityReminderOptIn(t *testing.T) {
	t.Setenv("ENABLE_SECURITY_REMINDER", "")
	var stderr bytes.Buffer
	status := run([]string{"security-reminder"}, strings.NewReader(`{"tool_name":"Write"}`), &bytes.Buffer{}, &stderr)
	if status != 0 || stderr.Len() != 0 {
		t.Fatalf("opt-out: status=%d stderr=%q", status, stderr.String())
	}
}

func TestSecurityReminderFiresOnceAndRecords(t *testing.T) {
	proof := t.TempDir()
	t.Setenv("ENABLE_SECURITY_REMINDER", "1")
	t.Setenv("ECI_PROOF_ROOT", proof)
	input := `{"tool_name":"Write","session_id":"s1","tool_input":{"file_path":"/x/a.js","content":"eval(x)"}}`

	var first bytes.Buffer
	if status := run([]string{"security-reminder"}, strings.NewReader(input), &bytes.Buffer{}, &first); status != 0 {
		t.Fatalf("first run status %d", status)
	}
	if !strings.HasPrefix(first.String(), `{"systemMessage": "Security check: eval`) {
		t.Fatalf("missing advisory: %q", first.String())
	}
	var second bytes.Buffer
	if status := run([]string{"security-reminder"}, strings.NewReader(input), &bytes.Buffer{}, &second); status != 0 || second.Len() != 0 {
		t.Fatalf("second run should be silent: status=%d out=%q", status, second.String())
	}
	if _, err := os.Stat(filepath.Join(proof, "security-warnings-s1.json")); err != nil {
		t.Fatalf("state file not written: %v", err)
	}
}

func TestPatchPathsSplitLikePython(t *testing.T) {
	got := securityPatchPaths("*** Add File: a.txt\r\n*** Move to:  b.txt \n")
	if len(got) != 2 || got[0] != "a.txt" || got[1] != "b.txt" {
		t.Fatalf("patch paths: %q", got)
	}
}
