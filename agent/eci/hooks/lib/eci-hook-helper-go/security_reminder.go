package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// securityReminderTools are the edit tools the reminder inspects.
var securityReminderTools = map[string]bool{
	"apply_patch":  true,
	"Edit":         true,
	"Write":        true,
	"MultiEdit":    true,
	"NotebookEdit": true,
}

type securityPattern struct {
	rule         string
	pathContains string
	pathSuffixes []string
	substrings   []string
	reminder     string
}

var securityPatterns = []securityPattern{
	{
		rule:         "github_actions_workflow",
		pathContains: ".github/workflows/",
		pathSuffixes: []string{".yml", ".yaml"},
		reminder:     "Security check: GitHub Actions workflow edits can introduce command injection. Do not place untrusted github.event values directly in run commands; pass them through env vars and quote shell expansions.",
	},
	{
		rule:       "child_process_exec",
		substrings: []string{"child_process.exec", "exec(", "execSync("},
		reminder:   "Security check: shell exec with dynamic input can create command injection. Prefer execFile/spawn with argv arrays.",
	},
	{
		rule:       "new_function",
		substrings: []string{"new Function"},
		reminder:   "Security check: new Function with dynamic strings can execute arbitrary code. Use a non-eval design unless explicitly required.",
	},
	{
		rule:       "eval",
		substrings: []string{"eval("},
		reminder:   "Security check: eval executes arbitrary code. Prefer JSON.parse or a structured parser.",
	},
	{
		rule:       "dangerously_set_html",
		substrings: []string{"dangerouslySetInnerHTML"},
		reminder:   "Security check: dangerouslySetInnerHTML can create XSS. Sanitize trusted HTML with a proven sanitizer, or avoid raw HTML.",
	},
	{
		rule:       "document_write",
		substrings: []string{"document.write"},
		reminder:   "Security check: document.write can create XSS and performance issues. Prefer safe DOM APIs.",
	},
	{
		rule:       "inner_html",
		substrings: []string{".innerHTML =", ".innerHTML="},
		reminder:   "Security check: assigning innerHTML can create XSS. Prefer textContent or sanitized HTML.",
	},
	{
		rule:       "pickle",
		substrings: []string{"pickle"},
		reminder:   "Security check: Python pickle can execute code when loading untrusted data. Prefer JSON or a safe serialization format.",
	},
	{
		rule:       "os_system",
		substrings: []string{"os.system", "from os import system"},
		reminder:   "Security check: os.system should not receive user-controlled input. Prefer subprocess with argv arrays.",
	},
}

// securityReminder prints one advisory per (paths, rule) pair to stderr for
// an edit tool event on stdin. It is opt-in via ENABLE_SECURITY_REMINDER=1.
func securityReminder(stdin io.Reader, stderr io.Writer) int {
	if os.Getenv("ENABLE_SECURITY_REMINDER") != "1" {
		return statusOK
	}
	if rand.Float64() < 0.1 {
		cleanupOldSecurityState(securityProofRoot())
	}

	raw, err := io.ReadAll(stdin)
	if err != nil || !utf8.Valid(raw) {
		return statusOK
	}
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return statusOK
	}
	event, ok := data.(map[string]any)
	if !ok {
		return statusOK
	}
	toolName, ok := event["tool_name"].(string)
	if !ok || !securityReminderTools[toolName] {
		return statusOK
	}

	toolInput, present := event["tool_input"]
	if !present {
		toolInput = map[string]any{}
	}
	filePath, content := securityToolContent(toolName, toolInput)
	paths := []string{filePath}
	if filePath == "" {
		paths = securityPatchPaths(content)
	}

	for _, pattern := range securityPatterns {
		if !securityPatternMatches(pattern, paths, content) {
			continue
		}
		sessionID := "default"
		if truthy(event["session_id"]) {
			sessionID = pyStr(event["session_id"])
		}
		statePath := securityStatePath(securityProofRoot(), sessionID)
		key := strings.Join(paths, ",") + ":" + pattern.rule
		state := loadSecurityState(statePath)
		if state[key] {
			return statusOK
		}
		state[key] = true
		saveSecurityState(statePath, state)
		message, err := json.Marshal(pattern.reminder)
		if err != nil {
			return statusFailed
		}
		fmt.Fprintf(stderr, "{\"systemMessage\": %s}\n", message)
		return statusOK
	}
	return statusOK
}

func securityPatternMatches(pattern securityPattern, paths []string, content string) bool {
	if pattern.pathContains != "" {
		for _, path := range paths {
			if !strings.Contains(path, pattern.pathContains) {
				continue
			}
			for _, suffix := range pattern.pathSuffixes {
				if strings.HasSuffix(path, suffix) {
					return true
				}
			}
		}
	}
	for _, substring := range pattern.substrings {
		if strings.Contains(content, substring) {
			return true
		}
	}
	return false
}

func securityProofRoot() string {
	if root := os.Getenv("ECI_PROOF_ROOT"); root != "" {
		return root
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("tmp", "agent", "proof")
	}
	return filepath.Join(home, "tmp", "agent", "proof")
}

// securityStatePath maps the session id to a file name using only letters,
// digits, "_" and "-"; an empty result becomes "default".
func securityStatePath(root, sessionID string) string {
	var safe strings.Builder
	for _, r := range sessionID {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-' {
			safe.WriteRune(r)
		}
	}
	name := safe.String()
	if name == "" {
		name = "default"
	}
	return filepath.Join(root, "security-warnings-"+name+".json")
}

// loadSecurityState returns the recorded keys. Any read or decode failure,
// or a non-list document, yields an empty set.
func loadSecurityState(path string) map[string]bool {
	state := map[string]bool{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return state
	}
	var items []any
	if err := json.Unmarshal(raw, &items); err != nil {
		return state
	}
	for _, item := range items {
		if s, ok := item.(string); ok {
			state[s] = true
		}
	}
	return state
}

func saveSecurityState(path string, state map[string]bool) {
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return
	}
	keys := make([]string, 0, len(state))
	for key := range state {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	data, err := json.Marshal(keys)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o666)
}

// cleanupOldSecurityState removes security-warnings-* regular files whose
// mtime is more than 30 days old. The first removal error stops the sweep.
func cleanupOldSecurityState(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "security-warnings-") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(path); err != nil {
				return
			}
		}
	}
}

// securityToolContent returns (file path, content) for a tool input, matching
// the Python tool_content contract.
func securityToolContent(toolName string, toolInput any) (string, string) {
	if s, ok := toolInput.(string); ok {
		return "", s
	}
	input, ok := toolInput.(map[string]any)
	if !ok {
		return "", ""
	}
	switch toolName {
	case "apply_patch":
		for _, key := range []string{"command", "patch", "input"} {
			if truthy(input[key]) {
				return "", pyStr(input[key])
			}
		}
		return "", ""
	case "Write":
		return pyStr(defaultOf(input, "file_path")), pyStr(defaultOf(input, "content"))
	case "Edit":
		return pyStr(defaultOf(input, "file_path")), pyStr(defaultOf(input, "new_string"))
	case "MultiEdit":
		var parts []string
		if edits, ok := input["edits"].([]any); ok {
			for _, edit := range edits {
				if m, ok := edit.(map[string]any); ok {
					parts = append(parts, pyStr(defaultOf(m, "new_string")))
				}
			}
		}
		return pyStr(defaultOf(input, "file_path")), strings.Join(parts, " ")
	case "NotebookEdit":
		return pyStr(defaultOf(input, "notebook_path")), ""
	}
	return "", ""
}

// defaultOf returns m[key], or "" when the key is absent.
func defaultOf(m map[string]any, key string) any {
	if v, ok := m[key]; ok {
		return v
	}
	return ""
}

// securityPatchPaths extracts file paths from apply_patch headers.
func securityPatchPaths(content string) []string {
	prefixes := []string{"*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: "}
	var paths []string
	for _, line := range pySplitLines(content) {
		for _, prefix := range prefixes {
			if strings.HasPrefix(line, prefix) {
				paths = append(paths, strings.TrimFunc(line[len(prefix):], pyIsSpace))
			}
		}
	}
	return paths
}

// pySplitLines splits on the same boundaries as Python's str.splitlines.
func pySplitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch r {
		case '\n', '\v', '\f', '\r', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			lines = append(lines, s[start:i])
			if r == '\r' && i+1 < len(s) && s[i+1] == '\n' {
				size = 2
			}
			i += size
			start = i
		default:
			i += size
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// pyIsSpace matches Python's str.isspace for the strip() used on patch paths.
func pyIsSpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
}

// truthy follows Python's truthiness for decoded JSON values.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0
	case json.Number:
		return x.String() != "0" && x.String() != ""
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return false
}

// pyStr approximates Python's str() for decoded JSON values. Containers use
// compact JSON rather than Python's repr; the reminder patterns match either.
func pyStr(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case string:
		return x
	case float64:
		return fmt.Sprint(x)
	case json.Number:
		return x.String()
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(raw)
}
