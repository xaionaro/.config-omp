package codingskill

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// eciRouterURI is the router read every ECI preset agent must earn credit for.
//
// Example: a helper needs only this read.
const eciRouterURI = "skill://explore-critique-implement"

// eciModuleURI returns the skill URI of an ECI role module.
//
// Example: eciModuleURI("explore.md") names the explorer module.
func eciModuleURI(module string) string {
	return eciRouterURI + "/references/" + module
}

// roleCases lists each preset role with its required reads in denial order.
//
// Example: the explorer needs the router and explore.md.
var roleCases = []struct {
	role string
	want []string
}{
	{"explorer", []string{eciRouterURI, eciModuleURI("explore.md")}},
	{"critic-step2", []string{eciRouterURI, eciModuleURI("critique.md")}},
	{"implementer", []string{eciRouterURI, eciModuleURI("implement.md")}},
	{"fast-owner", []string{eciRouterURI, eciModuleURI("fast-path.md")}},
	{"reviewer", []string{eciRouterURI, eciModuleURI("review.md")}},
	{"independent-e2e", []string{eciRouterURI, eciModuleURI("review.md")}},
	{"orchestrator", []string{eciRouterURI, eciModuleURI("task-orchestrator.md")}},
	{"worker", []string{eciRouterURI}},
	{"helper", []string{eciRouterURI}},
}

// readAll records successful reads of each path.
//
// Example: readAll(t, State{}, eciRouterURI) credits the router.
func readAll(
	t *testing.T,
	state State,
	paths ...string,
) State {
	for _, path := range paths {
		got, err := decide(Request{Action: resultAction, Tool: "read", Input: map[string]any{"path": path}, Details: completeRead(), State: state})
		require.NoError(t, err)
		state = got.State
	}
	return state
}

// TestECIRolesRequireTheirSkills checks each role's denial and non-ECI agents' freedom.
//
// Example: an explorer writing agent://O must read the router and explore.md.
func TestECIRolesRequireTheirSkills(t *testing.T) {
	for _, tc := range roleCases {
		got, err := decide(Request{Action: callAction, Tool: "write", Input: map[string]any{"path": "agent://O"}, AgentName: "eci-preset-" + tc.role + "-openai-max"})
		require.NoError(t, err)
		require.Contains(t, got.Reason, "requires "+strings.Join(tc.want, " and ")+".", tc.role)
	}
	for _, name := range []string{"", "main", "explore", "advisor", "sub", "eci-preset"} {
		got, err := decide(Request{Action: callAction, Tool: "write", Input: map[string]any{"path": "agent://O"}, AgentName: name})
		require.NoError(t, err)
		require.Empty(t, got.Reason, name)
	}
}

// TestECIReadsRequireSkillURIsWithoutLineRange credits skill://<name>[/SKILL.md][:raw] reads of the router, including host-truncated ones.
//
// Example: a filesystem-path or line-range read of the router earns no credit.
func TestECIReadsRequireSkillURIsWithoutLineRange(t *testing.T) {
	agent := "eci-preset-explorer-anthropic-default"
	call := func(state State) string {
		got, err := decide(Request{Action: callAction, Tool: "write", Input: map[string]any{"path": "agent://O"}, AgentName: agent, State: state})
		require.NoError(t, err)
		return got.Reason
	}
	for _, router := range []string{eciRouterURI, eciRouterURI + "/SKILL.md", eciRouterURI + ":raw", eciRouterURI + "/SKILL.md:raw"} {
		state := readAll(t, State{}, router, eciModuleURI("explore.md"))
		require.Empty(t, call(state), router)
	}
	hostTruncated := completeRead()
	hostTruncated["meta"] = map[string]any{"truncation": map[string]any{"direction": "middle", "truncatedBy": "middle", "maxBytes": float64(16384)}}
	got, err := decide(Request{Action: resultAction, Tool: "read", Input: map[string]any{"path": eciRouterURI}, Details: hostTruncated})
	require.NoError(t, err)
	require.Empty(t, call(readAll(t, got.State, eciModuleURI("explore.md"))))
	state := readAll(t, State{}, eciRouterURI+":1-100", eciModuleURI("explore.md"))
	require.Contains(t, call(state), "requires "+eciRouterURI+".")
	skills := "/home/x/.omp/agent/skills/explore-critique-implement/"
	state = readAll(t, State{}, skills+"SKILL.md", skills+"references/explore.md")
	require.Contains(t, call(state), "requires "+eciRouterURI+" and "+eciModuleURI("explore.md")+".")
}

// TestECIOrchestratorTaskRequiresSkills gates task spawning for ECI agents only.
//
// Example: an orchestrator cannot spawn workers before reading its module.
func TestECIOrchestratorTaskRequiresSkills(t *testing.T) {
	agent := "eci-preset-orchestrator-anthropic-default"
	task := Request{Action: callAction, Tool: "task", Input: map[string]any{"tasks": []any{}}, AgentName: agent}
	got, err := decide(task)
	require.NoError(t, err)
	require.Equal(t, "Coding skill gate: eci-preset-orchestrator-anthropic-default calling task requires skill://explore-critique-implement and skill://explore-critique-implement/references/task-orchestrator.md. Read each named skill without a line range and let the reads finish in an earlier tool batch. Then retry.", got.Reason)
	task.State = readAll(t, State{}, eciRouterURI, eciModuleURI("task-orchestrator.md"))
	got, err = decide(task)
	require.NoError(t, err)
	require.Empty(t, got.Reason)
	got, err = decide(Request{Action: callAction, Tool: "task", Input: map[string]any{"tasks": []any{}}, AgentName: "main"})
	require.NoError(t, err)
	require.Empty(t, got.Reason)
	got, err = decide(Request{Action: callAction, Tool: "bash", Input: map[string]any{"command": "true"}, AgentName: agent})
	require.NoError(t, err)
	require.Empty(t, got.Reason)
}

// TestECIRoleAndCodeSkillsCombine lists role skills before code skills in one denial.
//
// Example: an implementer writing Go needs its role reads plus coding-common and coding-go.
func TestECIRoleAndCodeSkillsCombine(t *testing.T) {
	req := Request{Action: callAction, Tool: "write", Input: map[string]any{"path": "a.go"}, AgentName: "eci-preset-implementer-anthropic-default"}
	got, err := decide(req)
	require.NoError(t, err)
	require.Contains(t, got.Reason, "requires "+strings.Join([]string{eciRouterURI, eciModuleURI("implement.md"), "skill://coding-common", "skill://coding-go"}, " and ")+".")
	req.State = readAll(t, State{}, eciRouterURI, eciModuleURI("implement.md"))
	got, err = decide(req)
	require.NoError(t, err)
	require.Contains(t, got.Reason, "requires skill://coding-common and skill://coding-go.")
	require.NotContains(t, got.Reason, eciRouterURI)
}

// TestUnknownECIRoleRequiresRouter keeps unknown roles moving with the router alone.
//
// Example: eci-preset-zz-openai-max needs only the router.
func TestUnknownECIRoleRequiresRouter(t *testing.T) {
	for _, name := range []string{"eci-preset-zz-openai-max", "eci-preset-zz"} {
		got, err := decide(Request{Action: callAction, Tool: "write", Input: map[string]any{"path": "agent://O"}, AgentName: name})
		require.NoError(t, err)
		require.Contains(t, got.Reason, "requires "+eciRouterURI+".", name)
	}
}

// templateURI matches ECI skill URIs named by a preset template.
//
// Example: it finds skill://explore-critique-implement/references/explore.md.
var templateURI = regexp.MustCompile("skill://explore-critique-implement[^`\\s]*")

// TestECITemplatesNameRoleSkills binds the role map to the preset templates in both directions.
//
// Example: explorer.tmpl names exactly the router and explore.md.
func TestECITemplatesNameRoleSkills(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "..", "tools", "eci-presets", "templates")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var templates []string
	for _, entry := range entries {
		role, ok := strings.CutSuffix(entry.Name(), ".tmpl")
		if !ok || role == "task-base" {
			continue
		}
		templates = append(templates, role)
		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		require.NoError(t, err)
		var want []string
		for _, skill := range roleSkills[presetRole(role)] {
			want = append(want, "skill://"+skill.name())
		}
		got := slices.Compact(slices.Sorted(slices.Values(templateURI.FindAllString(string(body), -1))))
		slices.Sort(want)
		require.Equal(t, want, got, role)
		require.NotContains(t, string(body), "~/.omp/agent/skills/explore-critique-implement", role)
	}
	var mapped []string
	for role := range roleSkills {
		mapped = append(mapped, string(role))
	}
	require.ElementsMatch(t, mapped, templates)
}

// TestBranchHydrationCreditsECIModules restores role reads from the transcript.
//
// Example: a hydrated explore.md read satisfies the explorer.
func TestBranchHydrationCreditsECIModules(t *testing.T) {
	branch := []map[string]any{
		{"type": "message", "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "toolCall", "name": "read", "id": "1", "arguments": map[string]any{"path": eciRouterURI}},
			map[string]any{"type": "toolCall", "name": "read", "id": "2", "arguments": map[string]any{"path": eciModuleURI("explore.md")}},
		}}},
		{"type": "message", "message": map[string]any{"role": "toolResult", "toolName": "read", "toolCallId": "1", "details": completeRead()}},
		{"type": "message", "message": map[string]any{"role": "toolResult", "toolName": "read", "toolCallId": "2", "details": completeRead()}},
	}
	req := Request{Action: callAction, Tool: "write", Input: map[string]any{"path": "agent://O"}, AgentName: "eci-preset-explorer-openai-default", Branch: branch}
	got, err := decide(req)
	require.NoError(t, err)
	require.Empty(t, got.Reason)
	req.Branch = branch[:2]
	got, err = decide(req)
	require.NoError(t, err)
	require.Contains(t, got.Reason, "requires "+eciModuleURI("explore.md")+".")
}

// TestECIHookPromptsAreInstructions gates hook prompt files but not hook catalogs or fixtures.
//
// Example: eci/hooks/stop-checklist.md needs harness-tuning; eci/hooks/tests/a.md does not.
func TestECIHookPromptsAreInstructions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	agent := filepath.Join(home, ".omp", "agent")
	for _, tc := range []struct {
		path  string
		gated bool
	}{
		{"eci/hooks/reviewer-rules.md", true},
		{"eci/hooks/stop-checklist.md", true},
		{"eci/hooks/stop-verification.md", true},
		{"eci/hooks/gate-escape-hatches.md", false},
		{"eci/hooks/tests/a.md", false},
	} {
		got, err := decide(Request{Action: callAction, Tool: "write", Input: map[string]any{"path": tc.path}, Cwd: agent})
		require.NoError(t, err)
		if !tc.gated {
			require.Empty(t, got.Reason, tc.path)
			continue
		}
		require.Contains(t, got.Reason, "skill://harness-tuning", tc.path)
	}
}

// TestInstructionCheckReportsMissingHome propagates the home lookup failure.
//
// Example: an empty HOME cannot classify a Markdown target.
func TestInstructionCheckReportsMissingHome(t *testing.T) {
	t.Setenv("HOME", "")
	_, err := decide(Request{Action: callAction, Tool: "write", Input: map[string]any{"path": "a.md"}, Cwd: "/x"})
	require.Error(t, err)
	_, err = decide(Request{Action: callAction, Tool: "write", Input: map[string]any{"path": "a.txt"}, Cwd: "/x"})
	require.NoError(t, err)
}
