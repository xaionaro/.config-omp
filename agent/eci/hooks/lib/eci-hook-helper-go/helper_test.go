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

// TestCodingSkillModeDispatch checks that the shared executable serves the skill bridge.
//
// Example: describe reports the edit and write tools.
func TestCodingSkillModeDispatch(t *testing.T) {
	status, out := runMode(t, []string{"coding-skill", "describe"}, "")
	if status != 0 || !strings.Contains(out, `"write"`) || !strings.Contains(out, `"read"`) {
		t.Fatalf("status=%d output=%q", status, out)
	}
	if status, _ := runMode(t, []string{"coding-skill", "unknown"}, ""); status == 0 {
		t.Fatal("unknown skill mode accepted")
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

func TestMetadataParse(t *testing.T) {
	const withParent = `{"type":"session_meta","payload":{"source":{"subagent":{"thread_spawn":{"parent_thread_id":"thr-1"}}}}}`
	const nullParent = `{"type":"session_meta","payload":{"source":{"subagent":{"thread_spawn":{"other":1}}}}}`
	const emptySpawn = `{"type":"session_meta","payload":{"source":{"subagent":{"thread_spawn":{}}}}}`
	const payloadFirst = `{"payload":{"source":{"subagent":{"thread_spawn":{"parent_thread_id":"p"}}}},"type":"session_meta"}`
	const notSession = `{"type":"event","payload":{"source":{"subagent":{"thread_spawn":{"parent_thread_id":"x"}}}}}`
	const noSubagent = `{"type":"session_meta","payload":{"source":"cli"}}`

	cases := []struct {
		name   string
		in     string
		want   *string
		isNone bool
		isErr  bool
	}{
		{name: "parent", in: withParent, want: strPtr("thr-1")},
		{name: "null parent", in: nullParent},
		{name: "empty thread_spawn object", in: emptySpawn, isErr: true},
		{name: "payload before type", in: payloadFirst, want: strPtr("p")},
		{name: "non session_meta", in: notSession, isNone: true},
		{name: "non-object source", in: noSubagent, isErr: true},
		{name: "empty object", in: `{}`, isErr: true},
		{name: "truncated", in: `{"type":"session_meta","payload":{`, isErr: true},
		{name: "bad escape in skipped string", in: `{"type":"x","k":"\q"}`, isErr: true},
		{name: "NaN in skipped value", in: `{"type":"x","k":NaN}`, isErr: true},
	}
	for _, tc := range cases {
		meta, err := parseThreadSpawnMetadata([]byte(tc.in))
		if tc.isErr {
			if err == nil {
				t.Fatalf("%s: want error", tc.name)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: unexpected error %v", tc.name, err)
		}
		if tc.isNone {
			if meta != nil {
				t.Fatalf("%s: want None", tc.name)
			}
			continue
		}
		if meta == nil {
			t.Fatalf("%s: want metadata, got None", tc.name)
		}
		switch {
		case tc.want == nil && meta.parentThreadID != nil:
			t.Fatalf("%s: want null parent", tc.name)
		case tc.want != nil && (meta.parentThreadID == nil || *meta.parentThreadID != *tc.want):
			t.Fatalf("%s: wrong parent", tc.name)
		}
	}
}

func TestMetadataModeOutput(t *testing.T) {
	sessions := t.TempDir()
	path := filepath.Join(sessions, "m.jsonl")
	line := `{"type":"session_meta","payload":{"source":{"subagent":{"thread_spawn":{"parent_thread_id":"thr-9"}}}}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	status, out := runMode(t, []string{"hook-transcript-thread-spawn-metadata", sessions},
		`{"transcript_path":"`+path+`"}`)
	if status != 0 || out != `{"parent_thread_id":"thr-9"}` {
		t.Fatalf("metadata: status=%d out=%q", status, out)
	}
}

func TestThreadSpawnParentIDInsideBound(t *testing.T) {
	const head = `{"type":"session_meta","payload":{"source":{"subagent":{"thread_spawn":{"parent_thread_id":"thr-in"}}}},"padding":"`
	const tail = `"}}`
	history := `{"padding":"` + strings.Repeat("h", 1<<20) + `"}` + "\n"
	for _, padding := range []int{4000, inputBudget - len(head) - len(tail) - 1} {
		record := head + strings.Repeat("p", padding) + tail + "\n"
		path := filepath.Join(t.TempDir(), "m.jsonl")
		if err := os.WriteFile(path, []byte(record+history), 0o644); err != nil {
			t.Fatal(err)
		}
		status, out := runMode(t, []string{"hook-transcript-thread-spawn-metadata", filepath.Dir(path)},
			`{"transcript_path":"`+path+`"}`)
		if status != 0 || out != `{"parent_thread_id":"thr-in"}` {
			t.Fatalf("padding %d: status=%d out=%q", padding, status, out)
		}
	}
}

func TestThreadSpawnRejectsOversizeWithoutFinalNewline(t *testing.T) {
	const head = `{"type":"session_meta","payload":{"source":{"subagent":{"thread_spawn":{"parent_thread_id":"thr-big"}}}},"padding":"`
	const tail = `"}`
	record := head + strings.Repeat("p", 1<<20) + tail
	path := filepath.Join(t.TempDir(), "big.jsonl")
	if err := os.WriteFile(path, []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	status, out := runMode(t, []string{"hook-transcript-thread-spawn-metadata", filepath.Dir(path)},
		`{"transcript_path":"`+path+`"}`)
	if status != 2 || out != "" {
		t.Fatalf("oversize without newline: status=%d out=%q", status, out)
	}
}

func strPtr(s string) *string { return &s }

// TestTranscriptStatusModeRetired rejects the retired hook-transcript-status mode;
// role classification lives in eci-command-plan and eci-proof-state.sh.
func TestTranscriptStatusModeRetired(t *testing.T) {
	if status := run([]string{"hook-transcript-status", t.TempDir()}, strings.NewReader(`{"transcript_path":"/x.jsonl"}`), &bytes.Buffer{}, &bytes.Buffer{}); status != statusRejected {
		t.Fatalf("status=%d, want %d", status, statusRejected)
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
