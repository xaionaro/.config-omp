package codingskill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// wholeReadBudget is the largest supported skill file, in bytes, that one read without a line range should show whole.
//
// Example: omp spills read output above tools.artifactSpillThreshold (default 50 KiB), so 40 KiB leaves room for the header line and for review before a skill outgrows one read.
const wholeReadBudget = 40 << 10

// completeRead models host read metadata for a successful two-line skill read.
//
// Example: both line numbers and totalLines describe the entire result.
func completeRead() map[string]any {
	return map[string]any{"totalLines": float64(2), "displayContent": map[string]any{"startLine": float64(1), "lineNumbers": []any{float64(1), float64(2)}, "text": "first\nsecond"}}
}

// TestOneDenialListsEveryMissingSkill names every gated path and skill of one call.
//
// Example: an edit of a.go and b.py names coding-common, coding-go, and coding-python together.
func TestOneDenialListsEveryMissingSkill(t *testing.T) {
	req := Request{Action: callAction, Tool: "edit", EditTargets: []string{"a.go", "b.py"}}
	got, err := decide(req)
	require.NoError(t, err)
	require.Equal(t, "Coding skill gate: writing a.go, b.py requires skill://coding-common and skill://coding-go and skill://coding-python. Read each named skill without a line range and let the reads finish in an earlier tool batch. Then retry.", got.Reason)
	for _, name := range []string{"coding-common", "coding-go"} {
		got, err = decide(Request{Action: resultAction, Tool: "read", Input: map[string]any{"path": "skill://" + name}, Details: completeRead(), State: req.State})
		require.NoError(t, err)
		req.State = got.State
	}
	got, err = decide(req)
	require.NoError(t, err)
	require.Equal(t, "Coding skill gate: writing b.py requires skill://coding-python. Read each named skill without a line range and let the reads finish in an earlier tool batch. Then retry.", got.Reason)
	got, err = decide(Request{Action: resultAction, Tool: "read", Input: map[string]any{"path": "skill://coding-python"}, Details: completeRead(), State: req.State})
	require.NoError(t, err)
	req.State = got.State
	got, err = decide(req)
	require.NoError(t, err)
	require.Empty(t, got.Reason)
	got, err = decide(Request{Action: callAction, Tool: "edit", EditTargets: []string{"a.go", "a.go"}})
	require.NoError(t, err)
	require.Equal(t, "Coding skill gate: writing a.go requires skill://coding-common and skill://coding-go. Read each named skill without a line range and let the reads finish in an earlier tool batch. Then retry.", got.Reason)
}

// TestSupportedSkillsFitOneRead checks every gated skill exists and one read without a line range can show it whole.
//
// Example: renaming references/explore.md or growing the router past wholeReadBudget fails here.
func TestSupportedSkillsFitOneRead(t *testing.T) {
	for skill := skillCommon; skill < skillEnd; skill++ {
		path := filepath.Join("..", "..", "..", "..", "..", "skills", skill.name())
		info, err := os.Stat(path)
		require.NoError(t, err, skill.name())
		if info.IsDir() {
			info, err = os.Stat(filepath.Join(path, "SKILL.md"))
			require.NoError(t, err, skill.name())
		}
		require.LessOrEqual(t, info.Size(), int64(wholeReadBudget), skill.name())
	}
}

// TestDirectWritesRequirePriorReads covers both ordinary editing tools.
//
// Example: Go writes remain denied until common and Go reads finish.
func TestDirectWritesRequirePriorReads(t *testing.T) {
	state := State{}
	for _, tc := range []struct {
		tool  string
		input map[string]any
	}{
		{"write", map[string]any{"path": "file.go"}},
		{"edit", map[string]any{"input": "*** Begin Patch\n[file.go#ABCD]\nCUT 1.=1\n*** End Patch"}},
	} {
		got, err := decide(Request{Action: callAction, Tool: tc.tool, Input: tc.input, State: state})
		require.NoError(t, err)
		require.Contains(t, got.Reason, "skill://coding-common")
		require.Contains(t, got.Reason, "skill://coding-go")
	}
	for _, name := range []string{"coding-common", "coding-go"} {
		got, err := decide(Request{Action: resultAction, Tool: "read", Input: map[string]any{"path": "skill://" + name}, Details: completeRead(), State: state})
		require.NoError(t, err)
		state = got.State
	}
	for _, tc := range []struct {
		tool  string
		input map[string]any
	}{
		{"write", map[string]any{"path": "file.go"}},
		{"edit", map[string]any{"input": "*** Begin Patch\n[file.go#ABCD]\nCUT 1.=1\n*** End Patch"}},
	} {
		got, err := decide(Request{Action: callAction, Tool: tc.tool, Input: tc.input, State: state})
		require.NoError(t, err)
		require.Empty(t, got.Reason)
	}
	got, err := decide(Request{Action: callAction, Tool: "write", Input: map[string]any{"path": "a.py"}, State: state})
	require.NoError(t, err)
	require.Contains(t, got.Reason, "skill://coding-python")
}

// TestWriteRequirementsByExtension covers code, language, and non-code targets.
//
// Example: a C++ write needs only coding-common; Markdown needs nothing.
func TestWriteRequirementsByExtension(t *testing.T) {
	for _, tc := range []struct {
		path string
		want []string
	}{
		{"a.cpp", []string{"skill://coding-common"}},
		{"a.ts", []string{"skill://coding-common"}},
		{"src/a.RS", []string{"skill://coding-common"}},
		{"a.sh", []string{"skill://coding-common"}},
		{"a.go", []string{"skill://coding-common", "skill://coding-go"}},
		{"a.py", []string{"skill://coding-common", "skill://coding-python"}},
		{"a.md", nil},
		{"a.txt", nil},
		{"a.yml", nil},
		{"Makefile", nil},
	} {
		got, err := decide(Request{Action: callAction, Tool: "write", Input: map[string]any{"path": tc.path}})
		require.NoError(t, err)
		if tc.want == nil {
			require.Empty(t, got.Reason, tc.path)
			continue
		}
		require.Contains(t, got.Reason, "requires "+strings.Join(tc.want, " and ")+".", tc.path)
	}
	state := State{}
	got, err := decide(Request{Action: resultAction, Tool: "read", Input: map[string]any{"path": "skill://coding-common"}, Details: completeRead(), State: state})
	require.NoError(t, err)
	got, err = decide(Request{Action: callAction, Tool: "write", Input: map[string]any{"path": "a.cpp"}, State: got.State})
	require.NoError(t, err)
	require.Empty(t, got.Reason)
}

// TestInstructionWritesRequireHarnessTuning covers instruction targets in ~/.omp.
//
// Example: a SKILL.md write needs coding-common and harness-tuning; a session log needs nothing.
func TestInstructionWritesRequireHarnessTuning(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	omp := filepath.Join(home, ".omp")
	want := "requires skill://coding-common and skill://harness-tuning."
	for _, tc := range []struct {
		path, cwd string
		gated     bool
	}{
		{"agent/skills/x/SKILL.md", omp, true},
		{"agent/skills/x/references/y.md", omp, true},
		{"~/.omp/agent/RULES.md", "", true},
		{filepath.Join(omp, "agent", "AGENTS.md"), "", true},
		{"eci-binding-contract.md", filepath.Join(omp, "agent"), true},
		{"agent/agents/eci-preset-helper.md", omp, true},
		{"agent/eci/tools/eci-presets/templates/helper.tmpl", omp, true},
		{"[agent/RULES.md#ABCD]", omp, true},
		{"agent/WATCHDOG.yml", omp, true},
		{"[agent/WATCHDOG.yml#ABCD]", omp, true},
		{"~/.omp/agent/WATCHDOG.yaml", "", true},
		{"WATCHDOG.md", filepath.Join(omp, "agent"), true},
		{"agent/SYSTEM.md", omp, true},
		{"agent/SYSTEM_TEMPLATE.md", omp, true},
		{"agent/APPEND_SYSTEM.md", omp, true},
		{"agent/TITLE_SYSTEM.md", omp, true},
		{"agent/PERSONALITY.md", omp, true},
		{"agent/managed-skills/x/SKILL.md", omp, true},
		{"agent/commands/x.md", omp, true},
		{"agent/prompts/x.md", omp, true},
		{"agent/instructions/x.md", omp, true},
		{"agent/rules/x.md", omp, true},
		{"agent/rules/x.mdc", omp, true},
		{"agent/skills/x/agents/openai.yaml", omp, true},
		{"agent/skills/x/flow.dot", omp, true},
		{"agent/eci/AGENTS.md", omp, true},
		{"x/.claude/CLAUDE.md", omp, true},
		{"agent/x/.gemini/GEMINI.md", omp, true},
		{"~/.omp/agent/x/.github/copilot-instructions.md", "", true},
		{"agent/sessions/x.md", omp, false},
		{"agent/skills/x/notes.txt", omp, false},
		{"agent/RULES.md", home, false},
		{"agent/WATCHDOG.yml", home, false},
		{"agent/config.yml", omp, false},
		{"agent/eci/model-selection.yaml", omp, false},
		{"agent/eci/README.md", omp, false},
		{"agent/tools/x.md", omp, false},
		{"agent/eci/flow.dot", omp, false},
		{"agent/AGENTS.md", home, false},
		{filepath.Join(home, ".ompx", "AGENTS.md"), "", false},
		{"local://AGENTS.md", omp, false},
		{"vault://AGENTS.md", omp, false},
		{"RULES.md", omp, false},
		{"agent/eci/XAGENTS.md", omp, false},
		{"agent/eci/agents.md", omp, false},
		{"agent/skills/x/SKILL.md", "", false},
	} {
		got, err := decide(Request{Action: callAction, Tool: "write", Input: map[string]any{"path": tc.path}, Cwd: tc.cwd})
		require.NoError(t, err)
		if !tc.gated {
			require.Empty(t, got.Reason, tc.path)
			continue
		}
		require.Contains(t, got.Reason, want, tc.path)
	}
	state := State{}
	for _, name := range []string{"coding-common", "harness-tuning"} {
		got, err := decide(Request{Action: resultAction, Tool: "read", Input: map[string]any{"path": "skill://" + name}, Details: completeRead(), State: state})
		require.NoError(t, err)
		state = got.State
	}
	got, err := decide(Request{Action: callAction, Tool: "write", Input: map[string]any{"path": "agent/RULES.md"}, Cwd: omp, State: state})
	require.NoError(t, err)
	require.Empty(t, got.Reason)
}

// TestGateCatalogListsInstructionTargets binds the instruction-target sentence of the gate catalog's coding-skill row to the gated names in both directions.
//
// Example: a name added to instructionFiles fails here until eci/hooks/gate-escape-hatches.md lists it.
func TestGateCatalogListsInstructionTargets(t *testing.T) {
	catalog, err := os.ReadFile(filepath.Join("..", "..", "..", "gate-escape-hatches.md"))
	require.NoError(t, err)
	_, sentence, ok := strings.Cut(string(catalog), "Instruction targets require ")
	require.True(t, ok)
	sentence, _, ok = strings.Cut(sentence, " Relative paths resolve")
	require.True(t, ok)
	want := []string{"skill://coding-common", "skill://harness-tuning", "~/.omp", "~/.omp/agent"}
	for _, names := range [][]string{contextFileNames, instructionFiles, instructionExtensions, instructionDirs} {
		want = append(want, names...)
	}
	var got []string
	for i, part := range strings.Split(sentence, "`") {
		if i%2 == 1 {
			got = append(got, part)
		}
	}
	require.ElementsMatch(t, want, got)
}

// TestSkillCreditRequiresSkillURIWithoutLineRange credits successful skill://<name>[/SKILL.md][:raw] reads, even when the host truncates them.
//
// Example: a line range or a host summary earns no credit; host truncation at the spill limit does.
func TestSkillCreditRequiresSkillURIWithoutLineRange(t *testing.T) {
	for _, path := range []string{"skill://coding-go:1-2", "skill://coding-go:raw:1-2", "skill://coding-go:conflicts", "/tmp/coding-go/SKILL.md"} {
		require.Equal(t, skillUnknown, creditedSkill(map[string]any{"path": path}, completeRead()), path)
	}
	summarized := completeRead()
	summarized["summary"] = map[string]any{"elided": float64(10)}
	require.Equal(t, skillUnknown, creditedSkill(map[string]any{"path": "skill://coding-go"}, summarized))
	for _, path := range []string{"skill://coding-go", "skill://coding-go/SKILL.md", "skill://coding-go:raw", "skill://coding-go/SKILL.md:raw"} {
		require.Equal(t, skillGo, creditedSkill(map[string]any{"path": path}, completeRead()), path)
	}
	truncated := completeRead()
	truncated["truncation"] = map[string]any{"truncated": true}
	hostTruncated := completeRead()
	hostTruncated["meta"] = map[string]any{"truncation": map[string]any{"direction": "middle", "truncatedBy": "middle", "maxBytes": float64(16384)}}
	limited := completeRead()
	limited["meta"] = map[string]any{"limits": map[string]any{"maxBytes": float64(16384)}}
	for i, details := range []map[string]any{truncated, hostTruncated, limited} {
		require.Equal(t, skillGo, creditedSkill(map[string]any{"path": "skill://coding-go"}, details), i)
	}
}

// TestBranchHydrationRequiresSuccessfulPairs restores only the active transcript.
//
// Example: a failed read cannot authorize a subsequent edit.
func TestBranchHydrationRequiresSuccessfulPairs(t *testing.T) {
	branch := []map[string]any{
		{"type": "message", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "name": "read", "id": "1", "arguments": map[string]any{"path": "skill://coding-common"}}, map[string]any{"type": "toolCall", "name": "read", "id": "2", "arguments": map[string]any{"path": "skill://coding-go"}}}}},
		{"type": "message", "message": map[string]any{"role": "toolResult", "toolName": "read", "toolCallId": "1", "details": completeRead()}},
		{"type": "message", "message": map[string]any{"role": "toolResult", "toolName": "read", "toolCallId": "2", "details": completeRead(), "isError": true}},
	}
	got, err := decide(Request{Action: callAction, Tool: "write", Input: map[string]any{"path": "a.go"}, Branch: branch})
	require.NoError(t, err)
	require.NotContains(t, got.Reason, "coding-common")
	require.Contains(t, got.Reason, "coding-go")
}
