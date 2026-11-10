package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// State holds the current agent's completed skill reads.
//
// Example: a navigation event replaces the state with an empty value.
type State struct {
	Reads    map[string]bool `json:"reads"`
	Hydrated bool            `json:"hydrated"`
}

// Request contains one host event and the agent state supplied by the adapter.
//
// Example: a successful read result records skill evidence in State.
type Request struct {
	Action      string           `json:"action"`
	Tool        string           `json:"tool"`
	Input       map[string]any   `json:"input"`
	Details     map[string]any   `json:"details"`
	EditTargets []string         `json:"editTargets"`
	IsError     bool             `json:"isError"`
	CWD         string           `json:"cwd"`
	Branch      []map[string]any `json:"branch"`
	State       State            `json:"state"`
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
	if state.Reads == nil {
		state.Reads = map[string]bool{}
	}
	if !state.Hydrated {
		if err := hydrate(&state, req); err != nil {
			return Response{}, err
		}
		state.Hydrated = true
	}
	if req.Action == "result" {
		if req.Tool == "read" && !req.IsError {
			skill := verifiedSkill(req.Input, req.Details)
			if skill != "" {
				state.Reads[skill] = true
			}
		}
		return Response{State: state}, nil
	}
	if req.Action != "call" {
		return Response{}, fmt.Errorf("unknown event action %q", req.Action)
	}
	paths, err := writeTargets(req)
	if err != nil {
		return Response{}, err
	}
	for _, path := range paths {
		lang := languageForPath(path)
		if lang == "" {
			continue
		}
		var missing []string
		for _, skill := range []string{"coding-common", "coding-" + lang} {
			if !state.Reads[skill] {
				missing = append(missing, "skill://"+skill)
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
) error {
	calls := map[string]map[string]any{}
	for _, entry := range req.Branch {
		if asString(entry["type"]) != "message" {
			continue
		}
		message := asMap(entry["message"])
		switch asString(message["role"]) {
		case "assistant":
			for _, part := range mapsFrom(message["content"]) {
				if asString(part["type"]) == "toolCall" && asString(part["name"]) == "read" {
					calls[asString(part["id"])] = asMap(part["arguments"])
				}
			}
		case "toolResult":
			if asString(message["toolName"]) != "read" || asBool(message["isError"]) {
				continue
			}
			input, ok := calls[asString(message["toolCallId"])]
			if !ok {
				continue
			}
			skill := verifiedSkill(input, asMap(message["details"]))
			if skill != "" {
				state.Reads[skill] = true
			}
		}
	}
	return nil
}

// verifiedSkill recognizes a complete, successful unselected skill URI read.
//
// Example: an incomplete two-line result does not credit a longer skill.
func verifiedSkill(input map[string]any, details map[string]any) string {
	path := asString(input["path"])
	raw := strings.HasSuffix(path, ":raw")
	if raw {
		path = strings.TrimSuffix(path, ":raw")
	}
	display := asMap(details["displayContent"])
	meta := asMap(details["meta"])
	if details["summary"] != nil || details["truncation"] != nil || meta["truncation"] != nil || meta["limits"] != nil || display["startLine"] != float64(1) {
		return ""
	}
	numbers, ok := display["lineNumbers"].([]any)
	text, textOK := display["text"].(string)
	total, totalOK := details["totalLines"].(float64)
	if raw && strings.HasSuffix(text, "\n") {
		total++
	}
	if !ok || !textOK || !totalOK || raw != asBool(details["raw"]) || float64(len(numbers)) != total || len(strings.Split(text, "\n")) != len(numbers) {
		return ""
	}
	for i, number := range numbers {
		if number != float64(i+1) {
			return ""
		}
	}
	for _, name := range []string{"coding-common", "coding-go", "coding-python"} {
		if path == "skill://"+name || path == "skill://"+name+"/SKILL.md" {
			return name
		}
	}
	return ""
}

// languageForPath maps a file extension to a coding skill name.
//
// Example: a.go requires coding-go and coding-common.
func languageForPath(path string) string {
	if match := patchHeader.FindStringSubmatch(path); len(match) == 2 {
		path = match[1]
	}
	if strings.HasPrefix(path, "local://") || strings.HasPrefix(path, "vault://") {
		path = strings.TrimSuffix(strings.TrimSuffix(path, ":raw"), ":conflicts")
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".py":
		return "python"
	default:
		return ""
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
