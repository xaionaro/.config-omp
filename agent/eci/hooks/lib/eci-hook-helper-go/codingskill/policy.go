package codingskill

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Skill identifies one skill whose successful read the policy can require.
//
// Example: skillGo corresponds to skill://coding-go.
type Skill int

const (
	// skillUnknown represents a path that does not name a supported skill.
	//
	// Example: a text file has no required language skill.
	skillUnknown Skill = iota
	// skillCommon identifies the language-independent coding skill.
	//
	// Example: Go and Python writes both require skillCommon.
	skillCommon
	// skillGo identifies the Go coding skill.
	//
	// Example: editing a Go file requires skillGo.
	skillGo
	// skillPython identifies the Python coding skill.
	//
	// Example: writing a Python file requires skillPython.
	skillPython
	// skillHarness identifies the prompt and instruction writing skill.
	//
	// Example: editing a SKILL.md under ~/.omp/agent requires skillHarness.
	skillHarness
	// skillECIRouter identifies the explore-critique-implement router every ECI preset agent reads.
	//
	// Example: a helper preset needs only skillECIRouter.
	skillECIRouter
	// skillECIExplore identifies the ECI explorer module.
	//
	// Example: an explorer preset needs skillECIExplore.
	skillECIExplore
	// skillECICritique identifies the ECI Step 2 critique module.
	//
	// Example: a critic-step2 preset needs skillECICritique.
	skillECICritique
	// skillECIImplement identifies the ECI Producer module.
	//
	// Example: an implementer preset needs skillECIImplement.
	skillECIImplement
	// skillECIFastPath identifies the ECI fast-path module.
	//
	// Example: a fast-owner preset needs skillECIFastPath.
	skillECIFastPath
	// skillECIReview identifies the ECI review module.
	//
	// Example: reviewer and independent-e2e presets need skillECIReview.
	skillECIReview
	// skillECIOrchestrator identifies the ECI Job Orchestrator module.
	//
	// Example: an orchestrator preset needs skillECIOrchestrator.
	skillECIOrchestrator
	// skillEnd bounds the supported skills; it names no skill.
	//
	// Example: creditedSkill scans skillCommon through skillEnd-1.
	skillEnd
)

// This blank array's length turns negative, failing compilation, when supported skills outgrow ReadSkills' 64 bits.
//
// Example: a 64th supported skill breaks the build instead of losing its bit.
var _ [64 - int(skillEnd)]struct{}

// name returns the host URI name of a supported skill, without the skill:// scheme.
//
// Example: skillGo.name() returns coding-go.
func (skill Skill) name() string {
	switch skill {
	case skillCommon:
		return "coding-common"
	case skillGo:
		return "coding-go"
	case skillPython:
		return "coding-python"
	case skillHarness:
		return "harness-tuning"
	case skillECIRouter:
		return "explore-critique-implement"
	case skillECIExplore:
		return "explore-critique-implement/references/explore.md"
	case skillECICritique:
		return "explore-critique-implement/references/critique.md"
	case skillECIImplement:
		return "explore-critique-implement/references/implement.md"
	case skillECIFastPath:
		return "explore-critique-implement/references/fast-path.md"
	case skillECIReview:
		return "explore-critique-implement/references/review.md"
	case skillECIOrchestrator:
		return "explore-critique-implement/references/task-orchestrator.md"
	default:
		return ""
	}
}

// ReadSkills is the set of credited skill reads, one bit per Skill.
//
// Example: the skillCommon bit is set after a skill://coding-common read without a line range.
type ReadSkills uint64

// mark records a credited read of a supported skill.
//
// Example: marking skillGo sets its bit.
func (reads *ReadSkills) mark(skill Skill) {
	*reads |= 1 << skill
}

// has reports whether a supported skill has been read.
//
// Example: has(skillGo) is true after marking skillGo.
func (reads ReadSkills) has(skill Skill) bool {
	return reads&(1<<skill) != 0
}

// UnmarshalJSON accepts the bitset number and the earlier name-to-bool object.
//
// Example: bridges started before the bitset change still send {"coding-common":true}; it decodes to the skillCommon bit.
func (reads *ReadSkills) UnmarshalJSON(data []byte) error {
	// TODO: drop the object form once no omp process started before the bitset helper (da346be) runs; its bridge resends that state until its next skill read.
	if !bytes.HasPrefix(data, []byte("{")) {
		var bits uint64
		if err := json.Unmarshal(data, &bits); err != nil {
			return fmt.Errorf("reads is not a bitset: %w", err)
		}
		*reads = ReadSkills(bits)
		return nil
	}
	var legacy map[string]bool
	if err := json.Unmarshal(data, &legacy); err != nil {
		return fmt.Errorf("reads is not a skill-name object: %w", err)
	}
	*reads = 0
	for skill := skillCommon; skill < skillEnd; skill++ {
		if legacy[skill.name()] {
			reads.mark(skill)
		}
	}
	return nil
}

// missing returns the skill:// URIs of required skills not yet read, in first-listed order, each once.
//
// Example: with only coding-common read, [skillCommon, skillGo] yields skill://coding-go.
func (reads ReadSkills) missing(required []Skill) []string {
	var uris []string
	listed := reads
	for _, skill := range required {
		if !listed.has(skill) {
			uris = append(uris, "skill://"+skill.name())
			listed.mark(skill)
		}
	}
	return uris
}

// State holds the current agent's credited skill reads.
//
// Example: a navigation event replaces the state with an empty value.
type State struct {
	Reads    ReadSkills `json:"reads"`
	Hydrated bool       `json:"hydrated"`
}

// Action identifies which kind of host event the policy processes.
//
// Example: a completed read uses resultAction.
type Action int

const (
	// resultAction records the outcome of a completed tool call.
	//
	// Example: a successful read can add skill evidence.
	resultAction Action = iota
	// callAction checks a proposed tool call.
	//
	// Example: an edit is checked before it reaches the host.
	callAction
	// hydrateAction restores evidence without processing a tool result.
	//
	// Example: the first proposed write loads the active branch.
	hydrateAction
)

// Request contains one host event and the agent state supplied by the adapter.
//
// Example: a successful read result records skill evidence in State.
type Request struct {
	Action      Action
	Tool        string
	Input       map[string]any
	Details     map[string]any
	EditTargets []string
	Cwd         string
	AgentName   string
	IsError     bool
	Branch      []map[string]any
	State       State
}

// Response returns the next opaque state and any actionable denial.
//
// Example: a missing Go skill produces a reason naming its read URI.
type Response struct {
	State  State  `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// callTools lists the tool calls decide checks: write and edit for their targets, and all three for ECI role skills.
//
// Example: an orchestrator's task call is checked before it spawns workers.
var callTools = []string{"write", "edit", "task"}

// decide applies one result or call to the current agent state.
//
// Example: a completed read is recorded before a later write is checked.
func decide(req Request) (Response, error) {
	state := req.State
	// A zero State has no credited reads; its first event hydrates it from any supplied transcript branch.
	if !state.Hydrated {
		hydrate(&state, req)
		state.Hydrated = true
	}
	switch req.Action {
	case resultAction:
		if req.Tool == "read" && !req.IsError {
			skill := creditedSkill(req.Input, req.Details)
			if skill != skillUnknown {
				state.Reads.mark(skill)
			}
		}
		return Response{State: state}, nil
	case hydrateAction:
		return Response{State: state}, nil
	case callAction:
	default:
		return Response{}, fmt.Errorf("unknown event action %d", req.Action)
	}
	if !slices.Contains(callTools, req.Tool) {
		return Response{State: state}, nil
	}
	required := slices.Clone(requiredRoleSkills(req.AgentName))
	actor := ""
	if len(state.Reads.missing(required)) > 0 {
		actor = req.AgentName + " "
	}
	var gatedPaths []string
	for _, path := range writeTargets(req) {
		targetSkill, err := skillForPath(path, req.Cwd)
		if err != nil {
			return Response{}, err
		}
		if targetSkill == skillUnknown {
			continue
		}
		pathSkills := []Skill{skillCommon, targetSkill}
		if targetSkill == skillCommon {
			pathSkills = pathSkills[:1]
		}
		if len(state.Reads.missing(pathSkills)) == 0 || slices.Contains(gatedPaths, path) {
			continue
		}
		gatedPaths = append(gatedPaths, path)
		required = append(required, pathSkills...)
	}
	missing := state.Reads.missing(required)
	if len(missing) == 0 {
		return Response{State: state}, nil
	}
	subject := actor + "calling " + req.Tool
	if len(gatedPaths) > 0 {
		subject = actor + "writing " + strings.Join(gatedPaths, ", ")
	}
	// TODO: rename the gate (extension files, re-export, catalog row, messages); it also enforces harness-tuning and ECI role reads.
	return Response{State: state, Reason: fmt.Sprintf("Coding skill gate: %s requires %s. Read each named skill without a line range and let the reads finish in an earlier tool batch. Then retry.", subject, strings.Join(missing, " and "))}, nil
}

// hydrate restores credited reads from the active host transcript branch.
//
// Example: an assistant skill:// read call paired with its successful result restores credit.
func hydrate(
	state *State,
	req Request,
) {
	calls := map[string]map[string]any{}
	for _, entry := range req.Branch {
		if asString(entry["type"]) != "message" {
			continue
		}
		message := asMap(entry["message"])
		switch asString(message["role"]) {
		case "assistant":
			collectReadCalls(calls, mapsFrom(message["content"]))
		case "toolResult":
			if asString(message["toolName"]) != "read" || asBool(message["isError"]) {
				continue
			}
			input, ok := calls[asString(message["toolCallId"])]
			if !ok {
				continue
			}
			skill := creditedSkill(input, asMap(message["details"]))
			if skill != skillUnknown {
				state.Reads.mark(skill)
			}
		}
	}
}

// collectReadCalls records read inputs for later result pairing.
//
// Example: an assistant read call can be matched to its tool result.
func collectReadCalls(
	calls map[string]map[string]any,
	parts []map[string]any,
) {
	for _, part := range parts {
		if asString(part["type"]) == "toolCall" && asString(part["name"]) == "read" {
			calls[asString(part["id"])] = asMap(part["arguments"])
		}
	}
}

// creditedSkill returns the supported skill named by a successful read of exactly skill://<name>[/SKILL.md][:raw], or skillUnknown; host output truncation still credits, because no such read can show more, while a host summary does not, because a :raw read returns the verbatim skill.
//
// Example: skill://coding-go, skill://coding-go/SKILL.md and skill://coding-go:raw credit coding-go; skill://coding-go:1-20 and a filesystem path do not.
func creditedSkill(
	input map[string]any,
	details map[string]any,
) Skill {
	path, ok := strings.CutPrefix(asString(input["path"]), "skill://")
	if !ok || details["summary"] != nil {
		return skillUnknown
	}
	path = strings.TrimSuffix(strings.TrimSuffix(path, ":raw"), "/SKILL.md") // TODO: also credit skill://<name>/, which the host resolves to the whole SKILL.md; trim a trailing "/" first.
	for skill := skillCommon; skill < skillEnd; skill++ {
		if path == skill.name() {
			return skill
		}
	}
	return skillUnknown
}

// codeExtensions maps lowercase source-code extensions to their most specific coding skill.
//
// Example: ".go" needs coding-go plus coding-common; ".cpp" needs only coding-common.
var codeExtensions = func() map[string]Skill {
	m := map[string]Skill{".go": skillGo, ".py": skillPython}
	for _, ext := range []string{
		".c", ".h", ".cc", ".cpp", ".cxx", ".hh", ".hpp", ".hxx",
		".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts",
		".rs", ".java", ".kt", ".kts", ".swift", ".rb", ".php", ".cs",
		".sh", ".bash", ".zsh", ".lua", ".scala", ".m", ".mm", ".dart",
		".pl", ".pm", ".r", ".jl", ".zig", ".hs", ".ex", ".exs", ".erl",
		".clj", ".fs", ".ml", ".vue", ".svelte", ".sql", ".proto", ".qml",
	} {
		m[ext] = skillCommon
	}
	return m
}()

// instructionExtensions lists extensions that can hold agent instructions.
//
// Example: SKILL.md and preset .tmpl files are instruction candidates.
var instructionExtensions = []string{".md", ".tmpl"}

// instructionFiles names instruction files relative to ~/.omp/agent.
//
// Example: RULES.md and the ECI stop checklist are instruction files.
var instructionFiles = []string{
	"RULES.md", "AGENTS.md", "eci-binding-contract.md",
	"eci/hooks/reviewer-rules.md", "eci/hooks/stop-checklist.md", "eci/hooks/stop-verification.md",
}

// instructionDirs names directories under ~/.omp/agent whose Markdown and templates are instructions.
//
// Example: agent/skills/x/references/y.md is under an instruction directory.
var instructionDirs = []string{"skills/", "agents/", "eci/tools/eci-presets/templates/"}

// skillForPath maps a write target to the most specific required skill.
//
// Example: a.go requires coding-go and coding-common; ~/.omp/agent/RULES.md requires harness-tuning and coding-common.
func skillForPath(
	path string,
	cwd string,
) (Skill, error) {
	if match := patchHeader.FindStringSubmatch(path); len(match) == 2 {
		path = match[1]
	}
	if strings.HasPrefix(path, "local://") || strings.HasPrefix(path, "vault://") {
		path = strings.TrimSuffix(strings.TrimSuffix(path, ":raw"), ":conflicts")
	}
	instruction, err := isInstructionPath(path, cwd)
	if err != nil {
		return skillUnknown, err
	}
	if instruction {
		return skillHarness, nil
	}
	return codeExtensions[strings.ToLower(filepath.Ext(path))], nil
}

// isInstructionPath reports whether path names an instruction file under ~/.omp/agent.
//
// Example: with cwd ~/.omp, agent/skills/a/SKILL.md is an instruction path.
func isInstructionPath(
	path string,
	cwd string,
) (bool, error) {
	// TODO: writes through the agent/eci/skills -> ../skills symlink match lexically
	// as eci/skills/... and are not gated; resolve symlinks of possibly missing
	// targets if edits start using that alias.
	if !slices.Contains(instructionExtensions, strings.ToLower(filepath.Ext(path))) {
		return false, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false, fmt.Errorf("locating ~/.omp/agent for %s: %w", path, err)
	}
	rest, tilde := strings.CutPrefix(path, "~/")
	switch {
	case tilde:
		path = filepath.Join(home, rest)
	case filepath.IsAbs(path):
	case cwd == "":
		return false, nil
	default:
		path = filepath.Join(cwd, path)
	}
	rel, err := filepath.Rel(filepath.Join(home, ".omp", "agent"), filepath.Clean(path))
	if err != nil {
		return false, fmt.Errorf("relating %s to ~/.omp/agent: %w", path, err)
	}
	if slices.Contains(instructionFiles, rel) {
		return true, nil
	}
	for _, dir := range instructionDirs {
		if strings.HasPrefix(rel, dir) {
			return true, nil
		}
	}
	return false, nil
}

// asString obtains a JSON string without inventing a value for other types.
//
// Example: a missing path becomes an empty string.
func asString(v any) string { s, _ := v.(string); return s }

// asBool obtains a JSON boolean.
//
// Example: an absent error flag is false.
func asBool(v any) bool { b, _ := v.(bool); return b }

// asMap obtains a JSON object.
//
// Example: missing details act as an empty object.
func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }

// stringsFrom extracts strings from a decoded JSON array.
//
// Example: edit input file paths become a Go slice.
func stringsFrom(v any) []string {
	var result []string
	for _, item := range asArray(v) {
		if value, ok := item.(string); ok {
			result = append(result, value)
		}
	}
	return result
}

// mapsFrom extracts objects from a decoded JSON array.
//
// Example: assistant message parts become tool-call records.
func mapsFrom(v any) []map[string]any {
	var result []map[string]any
	for _, item := range asArray(v) {
		if value, ok := item.(map[string]any); ok {
			result = append(result, value)
		}
	}
	return result
}

// asArray obtains a decoded JSON array.
//
// Example: a missing branch content yields no entries.
func asArray(v any) []any { a, _ := v.([]any); return a }
