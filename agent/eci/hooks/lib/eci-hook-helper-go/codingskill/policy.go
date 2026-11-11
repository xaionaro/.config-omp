package codingskill

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Skill identifies one supported coding skill.
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
)

// name returns the host URI name of a supported skill.
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
	default:
		return ""
	}
}

// ReadSkills tracks completed reads using the existing state JSON keys.
//
// Example: Common is true after a complete coding-common read.
type ReadSkills struct {
	Common bool `json:"coding-common,omitempty"`
	Go     bool `json:"coding-go,omitempty"`
	Python bool `json:"coding-python,omitempty"`
}

// mark records a completed read of a supported skill.
//
// Example: marking skillGo sets the Go flag.
func (reads *ReadSkills) mark(skill Skill) {
	switch skill {
	case skillCommon:
		reads.Common = true
	case skillGo:
		reads.Go = true
	case skillPython:
		reads.Python = true
	}
}

// has reports whether a supported skill has been read.
//
// Example: has(skillGo) is true after marking skillGo.
func (reads ReadSkills) has(skill Skill) bool {
	switch skill {
	case skillCommon:
		return reads.Common
	case skillGo:
		return reads.Go
	case skillPython:
		return reads.Python
	default:
		return false
	}
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
	paths := writeTargets(req)
	for _, path := range paths {
		targetSkill := skillForPath(path)
		if targetSkill == skillUnknown {
			continue
		}
		var missing []string
		required := []Skill{skillCommon, targetSkill}
		if targetSkill == skillCommon {
			required = required[:1]
		}
		for _, skill := range required {
			if !state.Reads.has(skill) {
				missing = append(missing, "skill://"+skill.name())
			}
		}
		if len(missing) > 0 {
			return Response{State: state, Reason: fmt.Sprintf("Coding skill gate: writing %s requires %s. Read each named skill without a line range and let the reads finish in an earlier tool batch. Then retry.", path, strings.Join(missing, " and "))}, nil
		}
	}
	return Response{State: state}, nil
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
	for _, name := range []Skill{skillCommon, skillGo, skillPython} {
		if path == name.name() {
			return name
		}
	}
	return skillUnknown
}

// skillForPath maps a file extension to the most specific coding skill.
//
// Example: a.go requires coding-go and coding-common; a.cpp requires only coding-common.
func skillForPath(path string) Skill {
	if match := patchHeader.FindStringSubmatch(path); len(match) == 2 {
		path = match[1]
	}
	if strings.HasPrefix(path, "local://") || strings.HasPrefix(path, "vault://") {
		path = strings.TrimSuffix(strings.TrimSuffix(path, ":raw"), ":conflicts")
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return skillGo
	case ".py":
		return skillPython
	case ".c", ".h", ".cc", ".cpp", ".cxx", ".hh", ".hpp", ".hxx",
		".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts",
		".rs", ".java", ".kt", ".kts", ".swift", ".rb", ".php", ".cs",
		".sh", ".bash", ".zsh", ".lua", ".scala", ".m", ".mm", ".dart",
		".pl", ".pm", ".r", ".jl", ".zig", ".hs", ".ex", ".exs", ".erl",
		".clj", ".fs", ".ml", ".vue", ".svelte", ".sql", ".proto", ".qml":
		return skillCommon
	default:
		return skillUnknown
	}
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
