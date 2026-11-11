package codingskill

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Skill identifies one skill whose completed read the policy can require.
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
	// Example: verifiedSkill scans skillCommon through skillEnd-1.
	skillEnd
)

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

// ReadSkills is the set of completed skill reads, one bit per Skill.
//
// Example: the skillCommon bit is set after a complete coding-common read.
type ReadSkills uint64

// mark records a completed read of a supported skill.
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

// missing returns the skill:// URIs of required skills not yet read, in order.
//
// Example: with only coding-common read, [skillCommon, skillGo] yields skill://coding-go.
func (reads ReadSkills) missing(required []Skill) []string {
	var uris []string
	for _, skill := range required {
		if !reads.has(skill) {
			uris = append(uris, "skill://"+skill.name())
		}
	}
	return uris
}

// State holds the current agent's completed skill reads.
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

// decide applies one result or call to the current agent state.
//
// Example: a completed read is recorded before a later write is checked.
func decide(req Request) (Response, error) {
	state := req.State
	// Zero-valued flags represent an agent with no completed reads.
	if !state.Hydrated {
		hydrate(&state, req)
		state.Hydrated = true
	}
	switch req.Action {
	case resultAction:
		if req.Tool == "read" && !req.IsError {
			skill := verifiedSkill(req.Input, req.Details)
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
	missing := state.Reads.missing(requiredRoleSkills(req.AgentName))
	actor := ""
	if len(missing) > 0 {
		actor = req.AgentName + " "
	}
	subject := actor + "calling " + req.Tool
	for _, path := range writeTargets(req) {
		targetSkill, err := skillForPath(path, req.Cwd)
		if err != nil {
			return Response{}, err
		}
		if targetSkill == skillUnknown {
			continue
		}
		required := []Skill{skillCommon, targetSkill}
		if targetSkill == skillCommon {
			required = required[:1]
		}
		pathMissing := state.Reads.missing(required)
		if len(pathMissing) == 0 {
			continue
		}
		// Name the first gated path; role skills stay first so one denial lists every read.
		subject = actor + "writing " + path
		missing = append(missing, pathMissing...)
		break
	}
	if len(missing) == 0 {
		return Response{State: state}, nil
	}
	return Response{State: state, Reason: fmt.Sprintf("Coding skill gate: %s requires %s. Read each named skill without a line range and let the reads finish in an earlier tool batch. Then retry.", subject, strings.Join(missing, " and "))}, nil
}

// hydrate restores completed reads from the active host transcript branch.
//
// Example: an assistant read call paired with a complete result restores credit.
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
			skill := verifiedSkill(input, asMap(message["details"]))
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

// verifiedSkill recognizes a complete, successful unselected skill URI read.
//
// Example: an incomplete two-line result does not credit a longer skill.
func verifiedSkill(
	input map[string]any,
	details map[string]any,
) Skill {
	path := asString(input["path"])
	raw := strings.HasSuffix(path, ":raw")
	if raw {
		path = strings.TrimSuffix(path, ":raw")
	}
	display := asMap(details["displayContent"])
	meta := asMap(details["meta"])
	if details["summary"] != nil || details["truncation"] != nil || meta["truncation"] != nil || meta["limits"] != nil || display["startLine"] != float64(1) {
		return skillUnknown
	}
	numbers, ok := display["lineNumbers"].([]any)
	text, textOK := display["text"].(string)
	total, totalOK := details["totalLines"].(float64)
	if raw && strings.HasSuffix(text, "\n") {
		total++
	}
	if !ok || !textOK || !totalOK || raw != asBool(details["raw"]) || float64(len(numbers)) != total || strings.Count(text, "\n")+1 != len(numbers) {
		return skillUnknown
	}
	for i, number := range numbers {
		if number != float64(i+1) {
			return skillUnknown
		}
	}
	if !strings.HasPrefix(path, "skill://") {
		return skillUnknown
	}
	path = strings.TrimSuffix(strings.TrimPrefix(path, "skill://"), "/SKILL.md")
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
func skillForPath(path, cwd string) (Skill, error) {
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
//
// TODO: writes through the agent/eci/skills -> ../skills symlink match lexically
// as eci/skills/... and are not gated; resolve symlinks of possibly missing
// targets if edits start using that alias.
func isInstructionPath(path, cwd string) (bool, error) {
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
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return false, nil
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
