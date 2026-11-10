package plan

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// largeHistoryBytes is the history appended past the header in the latency rows.
const largeHistoryBytes = 8 << 20

// maxClassifyReadBytes bounds the bytes one classification may read.
const maxClassifyReadBytes = 1 << 20

// titleSlot returns an omp line-zero title record padded to the 256-byte slot.
//
// Example: titleSlot() is 255 bytes of JSON followed by a newline.
func titleSlot() string {
	head := `{"type":"title","v":1,"title":"t","pad":"`
	return head + strings.Repeat(" ", 255-len(head)-2) + `"}` + "\n"
}

// historyTail returns newline-terminated message records of about size bytes.
//
// Example: historyTail(1<<20) holds sixteen 64 KiB message lines.
func historyTail(size int) string {
	line := `{"type":"message","pad":"` + strings.Repeat("x", 64<<10) + `"}` + "\n"
	return strings.Repeat(line, size/len(line)+1)
}

// readChars returns this process's rchar counter from /proc/self/io.
//
// Example: a 4 KiB read raises the counter by 4096.
func readChars(t *testing.T) int64 {
	t.Helper()
	data, err := os.ReadFile("/proc/self/io")
	if err != nil {
		t.Skipf("no /proc/self/io: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if value, ok := strings.CutPrefix(line, "rchar: "); ok {
			n, err := strconv.ParseInt(value, 10, 64)
			require.NoError(t, err)
			return n
		}
	}
	t.Skip("no rchar in /proc/self/io")
	return 0
}

// TestHookTranscriptPrefixLargeMainReadsPrefix keeps a large main transcript coordinator while reading only a bounded prefix.
//
// Example: an 8 MiB depth-two history is coordinator after reading under 1 MiB.
func TestHookTranscriptPrefixLargeMainReadsPrefix(t *testing.T) {
	_, cwd := contextFixture(t)
	sessions := sessionsFixture(t)
	transcript := filepath.Join(sessions, "main.jsonl")
	content := titleSlot() + `{"type":"session","version":3,"id":"owner"}` + "\n" + historyTail(largeHistoryBytes)
	require.NoError(t, os.WriteFile(transcript, []byte(content), 0600))
	before := readChars(t)
	got, err := hookRequest(HookInput{SessionID: "owner", CWD: cwd, TranscriptPath: transcript})
	read := readChars(t) - before
	require.NoError(t, err)
	require.Equal(t, RoleCoordinator, got.Role)
	require.LessOrEqual(t, read, int64(maxClassifyReadBytes))
}

// TestHookTranscriptPrefixLargeSubagentReadsPrefix keeps a large child a worker that inherits its large parent while reading only bounded prefixes.
//
// Example: an 8 MiB child naming an 8 MiB parent inherits the parent marker after reading under 1 MiB.
func TestHookTranscriptPrefixLargeSubagentReadsPrefix(t *testing.T) {
	root, cwd := contextFixture(t)
	sessions := sessionsFixture(t)
	contextMarker(t, root, "parent", cwd)
	parent := filepath.Join(sessions, "parent.jsonl")
	require.NoError(t, os.WriteFile(parent, []byte(titleSlot()+`{"type":"session","version":3,"id":"parent"}`+"\n"+historyTail(largeHistoryBytes)), 0600))
	transcript := filepath.Join(subagentDirectory(t, sessions), "child.jsonl")
	writeSubagentTranscript(t, transcript, "child", parent, historyTail(largeHistoryBytes))
	before := readChars(t)
	got, err := hookRequest(HookInput{SessionID: "child", CWD: cwd, TranscriptPath: transcript})
	read := readChars(t) - before
	require.NoError(t, err)
	require.Equal(t, RoleWorker, got.Role)
	require.Equal(t, MarkerInactive, got.Marker)
	require.LessOrEqual(t, read, int64(maxClassifyReadBytes))
}

// TestHookTranscriptPrefixTornLinesStayMain keeps a main transcript coordinator when a line is torn or malformed outside the classified records.
//
// Example: an unterminated last append does not demote a main transcript.
func TestHookTranscriptPrefixTornLinesStayMain(t *testing.T) {
	_, cwd := contextFixture(t)
	sessions := sessionsFixture(t)
	header := `{"type":"session","version":3,"id":"owner"}` + "\n"
	torn := titleSlot()[:200] + strings.Repeat(" ", 55) + "\n"
	cases := map[string]string{
		"torn-tail-inside-prefix": titleSlot() + header + `{"type":"message","content":"par`,
		"torn-tail-beyond-prefix": titleSlot() + header + historyTail(2<<20) + `{"type":"message","content":"par`,
		"garbage-beyond-16-lines": titleSlot() + header + strings.Repeat(`{"type":"message"}`+"\n", 20) + "not json\n",
		"garbage-beyond-64-kib":   titleSlot() + header + historyTail(1<<20) + "not json\n",
		"torn-title-slot":         torn + header,
	}
	require.Len(t, torn, 256)
	for name, content := range cases {
		transcript := filepath.Join(sessions, name+".jsonl")
		require.NoError(t, os.WriteFile(transcript, []byte(content), 0600))
		status, err := hookTranscriptStatus(transcript)
		require.NoError(t, err, name)
		require.Equal(t, "main", status, name)
		got, err := hookRequest(HookInput{SessionID: "owner", CWD: cwd, TranscriptPath: transcript})
		require.NoError(t, err, name)
		require.Equal(t, RoleCoordinator, got.Role, name)
	}
}

// TestHookTranscriptPrefixUnreadableHeaderIsWorker fails closed when the session header is torn, malformed, late, or unreadable.
//
// Example: a header on line seventeen lies past the bounded prefix, so the callback runs as a worker.
func TestHookTranscriptPrefixUnreadableHeaderIsWorker(t *testing.T) {
	_, cwd := contextFixture(t)
	sessions := sessionsFixture(t)
	header := `{"type":"session","version":3,"id":"owner"}`
	cases := map[string]string{
		"torn-header-no-newline":   titleSlot() + header[:20],
		"torn-header-newline":      titleSlot() + header[:20] + "\n" + `{"type":"message"}` + "\n",
		"unterminated-header":      titleSlot() + header,
		"header-after-16-lines":    strings.Repeat(`{"type":"message"}`+"\n", 16) + header + "\n",
		"header-after-64-kib":      historyTail(70<<10) + header + "\n",
		"malformed-first-line":     "not json\n" + header + "\n",
		"malformed-title-not-slot": titleSlot()[:100] + "\n" + header + "\n",
		"no-header":                titleSlot() + `{"type":"message"}` + "\n",
	}
	for name, content := range cases {
		transcript := filepath.Join(sessions, name+".jsonl")
		require.NoError(t, os.WriteFile(transcript, []byte(content), 0600))
		status, err := hookTranscriptStatus(transcript)
		require.ErrorIs(t, err, errHookTranscriptUnknown, name)
		require.Empty(t, status, name)
		got, err := hookRequest(HookInput{SessionID: "owner", CWD: cwd, TranscriptPath: transcript})
		require.NoError(t, err, name)
		require.Equal(t, RoleWorker, got.Role, name)
	}
	if os.Geteuid() != 0 {
		locked := filepath.Join(sessions, "locked.jsonl")
		require.NoError(t, os.WriteFile(locked, []byte(header+"\n"), 0000))
		_, err := hookTranscriptStatus(locked)
		require.ErrorIs(t, err, errHookTranscriptUnknown)
	}
}

// TestHookTranscriptPrefixLayoutParity keeps the roles of real omp layouts with and without long history.
//
// Example: a depth-three child with session_init on record four stays a worker after 2 MiB of history.
func TestHookTranscriptPrefixLayoutParity(t *testing.T) {
	_, _ = contextFixture(t)
	sessions := sessionsFixture(t)
	parent := filepath.Join(sessions, "parent.jsonl")
	require.NoError(t, os.WriteFile(parent, []byte(titleSlot()+`{"type":"session","version":3,"id":"parent"}`+"\n"), 0600))
	deep := subagentDirectory(t, sessions)
	deeper := filepath.Join(deep, "nested")
	require.NoError(t, os.MkdirAll(deeper, 0700))
	child := `{"type":"session","version":3,"id":"c","parentSession":"` + parent + `"}` + "\n" +
		`{"type":"model_change"}` + "\n" + `{"type":"custom","customType":"session_init"}` + "\n"
	layouts := []struct {
		dir, body, want string
	}{
		{sessions, `{"type":"session","version":3,"id":"m"}` + "\n" + `{"type":"model_change"}` + "\n", "main"},
		{deep, child, "subagent"},
		{deeper, child, "subagent"},
	}
	for i, layout := range layouts {
		for _, history := range []int{0, 2 << 20} {
			var content bytes.Buffer
			content.WriteString(titleSlot() + layout.body)
			if history > 0 {
				content.WriteString(historyTail(history))
			}
			transcript := filepath.Join(layout.dir, "layout-"+strconv.Itoa(i)+"-"+strconv.Itoa(history)+".jsonl")
			require.NoError(t, os.WriteFile(transcript, content.Bytes(), 0600))
			status, err := hookTranscriptStatus(transcript)
			require.NoError(t, err, transcript)
			require.Equal(t, layout.want, status, transcript)
		}
	}
}

// prefixCase is one row of testdata/transcript-prefix-cases.json, shared with the omp
// hook runner test (agent/hooks-disabled/lib/eci-hook-runner.test.ts).
type prefixCase struct {
	Name  string            `json:"name"`
	Depth int               `json:"depth"`
	Lines []json.RawMessage `json:"lines"`
	Tail  string            `json:"tail"`
	Want  string            `json:"want"`
}

// content builds the case's transcript bytes; the runner test builds the same bytes.
//
// Example: [{"title":"slot"},"{\"type\":\"session\"}"] is a title slot and a header line.
func (c prefixCase) content(t *testing.T) string {
	t.Helper()
	slot := strings.TrimSuffix(titleSlot(), "\n")
	var out strings.Builder
	for _, raw := range c.Lines {
		var line string
		if json.Unmarshal(raw, &line) == nil {
			out.WriteString(line + "\n")
			continue
		}
		var element struct {
			Title  string `json:"title"`
			Repeat string `json:"repeat"`
			Count  int    `json:"count"`
			Pad    int    `json:"pad"`
		}
		require.NoError(t, json.Unmarshal(raw, &element), c.Name)
		switch {
		case element.Title == "slot":
			out.WriteString(slot + "\n")
		case element.Title == "torn":
			out.WriteString(slot[:200] + strings.Repeat(" ", 55) + "\n")
		case element.Title == "short":
			out.WriteString(slot[:100] + "\n")
		case element.Repeat != "":
			out.WriteString(strings.Repeat(element.Repeat+"\n", element.Count))
		default:
			out.WriteString(`{"type":"message","pad":"` + strings.Repeat("x", element.Pad) + `"}` + "\n")
		}
	}
	return out.String() + c.Tail
}

// TestHookTranscriptPrefixSharedCases classifies the shared cases; the omp hook runner
// must mark exactly the cases whose status is not main.
//
// Example: a header past sixteen lines is unknown.
func TestHookTranscriptPrefixSharedCases(t *testing.T) {
	_, _ = contextFixture(t)
	sessions := sessionsFixture(t)
	data, err := os.ReadFile(filepath.Join("testdata", "transcript-prefix-cases.json"))
	require.NoError(t, err)
	var table struct {
		Cases []prefixCase `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(data, &table))
	require.Greater(t, len(table.Cases), 10)
	for _, c := range table.Cases {
		dir := sessions
		for range c.Depth - 2 {
			dir = filepath.Join(dir, "nested")
		}
		require.NoError(t, os.MkdirAll(dir, 0700))
		transcript := filepath.Join(dir, c.Name+".jsonl")
		require.NoError(t, os.WriteFile(transcript, []byte(c.content(t)), 0600))
		status, err := hookTranscriptStatus(transcript)
		if c.Want == "unknown" {
			require.ErrorIs(t, err, errHookTranscriptUnknown, c.Name)
			continue
		}
		require.NoError(t, err, c.Name)
		require.Equal(t, c.Want, status, c.Name)
	}
}
