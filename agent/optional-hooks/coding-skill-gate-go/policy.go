package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// State holds the current agent's completed reads and pending structural previews.
//
// Example: a navigation event replaces the state with an empty value.
type State struct {
	Reads map[string]bool `json:"reads"`
	Pending [][]string `json:"pending"`
	Hydrated bool `json:"hydrated"`
}

// Request contains one host event and the agent state supplied by the adapter.
//
// Example: a successful read result records skill evidence in State.
type Request struct {
	Action string `json:"action"`
	Tool string `json:"tool"`
	Input map[string]any `json:"input"`
	Details map[string]any `json:"details"`
	EditTargets []string `json:"editTargets"`
	IsError bool `json:"isError"`
	CWD string `json:"cwd"`
	SkillsRoot string `json:"skillsRoot"`
	Branch []map[string]any `json:"branch"`
	State State `json:"state"`
}

// Response returns the next opaque state and any actionable denial.
//
// Example: a missing Go skill produces a reason naming its read URI.
type Response struct {
	State State `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// decide applies one result or call to the current agent state.
//
// Example: a completed read is recorded before a later write is checked.
func decide(req Request) (Response, error) {
	state := req.State
	if state.Reads == nil { state.Reads = map[string]bool{} }
	if !state.Hydrated {
		if err := hydrate(&state, req); err != nil { return Response{}, err }
		state.Hydrated = true
	}
	if req.Action == "result" {
		if req.Tool == "read" && !req.IsError {
			skill, err := verifiedSkill(req.Input, req.Details, req.CWD, req.SkillsRoot)
			if err != nil { return Response{}, err }
			if skill != "" { state.Reads[skill] = true }
		}
		if req.Tool == "ast_edit" && !req.IsError {
			files := stringsFrom(req.Details["files"])
			if len(files) != 0 { state.Pending = append(state.Pending, files) }
		}
		if req.Tool == "write" && (asString(req.Input["path"]) == "xd://resolve" || asString(req.Input["path"]) == "xd://reject") {
			inner := asMap(asMap(req.Details["xdev"])["inner"])
			if asString(inner["sourceToolName"]) == "ast_edit" && (asString(inner["action"]) == "apply" || asString(inner["action"]) == "discard") && len(state.Pending) > 0 { state.Pending = state.Pending[:len(state.Pending)-1] }
		}
		return Response{State: state}, nil
	}
	if req.Action != "call" { return Response{}, fmt.Errorf("unknown event action %q", req.Action) }
	paths, err := writeTargets(req)
	if err != nil { return Response{}, err }
	for _, path := range paths {
		lang, err := languageForPath(path, req.SkillsRoot)
		if err != nil { return Response{}, err }
		if lang == "" { continue }
		var missing []string
		for _, skill := range []string{"coding-common", "coding-"+lang} {
			if !state.Reads[skill] { missing = append(missing, fmt.Sprintf("skill://%s (or %s)", skill, filepath.Join(req.SkillsRoot, skill, "SKILL.md"))) }
		}
		if len(missing) > 0 {
			retry := "Then retry."
			if asString(req.Input["path"]) == "xd://resolve" { retry = "First write xd://reject, then read the skills and rerun the preview." }
			return Response{State: state, Reason: fmt.Sprintf("Coding skill gate: writing %s requires %s. Read each named skill without a line range and let the reads finish in an earlier tool batch. %s", path, strings.Join(missing, " and "), retry)}, nil
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
		if asString(entry["type"]) != "message" { continue }
		message := asMap(entry["message"])
		switch asString(message["role"]) {
		case "assistant":
			for _, part := range mapsFrom(message["content"]) {
				if asString(part["type"]) == "toolCall" && asString(part["name"]) == "read" { calls[asString(part["id"])] = asMap(part["arguments"]) }
			}
		case "toolResult":
			if asString(message["toolName"]) != "read" || asBool(message["isError"]) { continue }
			input, ok := calls[asString(message["toolCallId"])]
			if !ok { continue }
			skill, err := verifiedSkill(input, asMap(message["details"]), req.CWD, req.SkillsRoot)
			if err != nil { return err }
			if skill != "" { state.Reads[skill] = true }
		}
	}
	return nil
}

// verifiedSkill checks that the actual displayed result equals the entire skill file.
//
// Example: a successful two-line read cannot satisfy a whole-file prerequisite.
func verifiedSkill(
	input map[string]any,
	details map[string]any,
	cwd string,
	root string,
) (string, error) {
	path := asString(input["path"])
	display := asMap(details["displayContent"])
	meta := asMap(details["meta"])
	if path == "" || details["summary"] != nil || details["truncation"] != nil || meta["truncation"] != nil || meta["limits"] != nil || display["startLine"] != float64(1) { return "", nil }
	if numbers, ok := display["lineNumbers"].([]any); ok {
		for i, number := range numbers { if number != float64(i+1) { return "", nil } }
	}
	raw := strings.HasSuffix(path, ":raw")
	if raw != asBool(details["raw"]) { return "", nil }
	if raw { path = strings.TrimSuffix(path, ":raw") }
	if strings.ContainsAny(path, "?;") || regexp.MustCompile(`:(?:[0-9]|raw)`).MatchString(path) { return "", nil }
	for _, name := range []string{"coding-common", "coding-go", "coding-python"} {
		canonical := filepath.Join(root, name, "SKILL.md")
		if strings.HasPrefix(path, "skill://") {
			if path != "skill://"+name && path != "skill://"+name+"/SKILL.md" { continue }
		} else {
			actual := path
			switch hostPath := asString(details["resolvedPath"]); {
			case hostPath != "":
				actual = hostPath
			case asString(asMap(meta["source"])["value"]) != "":
				actual = asString(asMap(meta["source"])["value"])
			}
			if !filepath.IsAbs(actual) { actual = filepath.Join(cwd, actual) }
			resolved, err := filepath.EvalSymlinks(actual)
			if os.IsNotExist(err) { continue }; if err != nil { return "", err }
			expected, err := filepath.EvalSymlinks(canonical)
			if os.IsNotExist(err) { continue }; if err != nil { return "", err }
			if resolved != expected { continue }
		}
		data, err := os.ReadFile(canonical)
		if os.IsNotExist(err) { continue }; if err != nil { return "", err }
		text := strings.ReplaceAll(string(data), "\r\n", "\n")
		if !raw { text = strings.TrimSuffix(text, "\n") }
		if strings.ReplaceAll(asString(display["text"]), "\r\n", "\n") == text { return name, nil }
	}
	return "", nil
}

// languageForPath maps a file extension to an installed coding skill.
//
// Example: a.go requires coding-go when both Go and common skills exist.
func languageForPath(
	path string,
	root string,
) (string, error) {
	if match := patchHeader.FindStringSubmatch(path); len(match) == 2 { path = match[1] }
	if strings.HasPrefix(path,"local://") || strings.HasPrefix(path,"vault://") { path = strings.TrimSuffix(strings.TrimSuffix(path,":raw"),":conflicts") }
	name := ""
	switch strings.ToLower(filepath.Ext(path)) { case ".go": name = "go"; case ".py": name = "python" }
	if name == "" { return "", nil }
	for _, skill := range []string{"common", name} {
		_, err := os.Stat(filepath.Join(root,"coding-"+skill,"SKILL.md"))
		if os.IsNotExist(err) { return "", nil }; if err != nil { return "", err }
	}
	return name, nil
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
// Example: structural preview file paths become a Go slice.
func stringsFrom(v any) []string {
	var result []string
	for _, item := range asArray(v) { if value, ok := item.(string); ok { result = append(result,value) } }
	return result
}

// mapsFrom extracts objects from a decoded JSON array.
//
// Example: assistant message parts become tool-call records.
func mapsFrom(v any) []map[string]any {
	var result []map[string]any
	for _, item := range asArray(v) { if value, ok := item.(map[string]any); ok { result = append(result,value) } }
	return result
}

// asArray obtains a decoded JSON array.
//
// Example: a missing branch content yields no entries.
func asArray(v any) []any { a, _ := v.([]any); return a }
