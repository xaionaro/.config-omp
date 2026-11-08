package ecihooke2e

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestRegisteredBashHook exercises the installed ECI process with a local model.
//
// Example: ECI_E2E=1 go test -v verifies the registered shell hook without API access.
func TestRegisteredBashHook(t *testing.T) {
	if os.Getenv("ECI_E2E") != "1" {
		t.Skip("set ECI_E2E=1 to run the real ECI runtime")
	}
	started := time.Now()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	evidence := os.Getenv("ECI_E2E_EVIDENCE")
	if evidence == "" {
		evidence = t.TempDir()
	}
	if err := os.MkdirAll(evidence, 0700); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(evidence, "home")
	eciHome := filepath.Join(home, ".omp", "agent", "eci")
	if err := os.MkdirAll(eciHome, 0700); err != nil {
		t.Fatal(err)
	}
	copyCommand := exec.Command("cp", "-a", filepath.Join(root, "hooks"), filepath.Join(root, "bin"), filepath.Join(root, "hooks.json"), eciHome)
	if output, err := copyCommand.CombinedOutput(); err != nil {
		t.Fatalf("copy fixture: %v: %s", err, output)
	}
	if os.Getenv("ECI_E2E_MUTATE_HOOK_FAILURE") == "1" {
		if err := os.WriteFile(filepath.Join(eciHome, "hooks", "validate-bash.sh"), []byte("#!/bin/bash\nexit 1\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	mode := filepath.Join(home, "config", "eci", "command-gate-mode")
	if err := os.MkdirAll(filepath.Dir(mode), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mode, []byte("enforcing\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// The local model requests an allowed read, a protected control mutation, then finishes.

	var requests atomic.Int32
	// The handler supplies deterministic model output and never contacts an upstream API.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		var item map[string]any
		switch requests.Add(1) {
		case 1, 2:
			args, err := json.Marshal(map[string]any{"cmd": "printf 'ECI_HOOK_E2E_READ_OK\\n'", "yield_time_ms": 1000, "max_output_tokens": 100})
			if err != nil {
				t.Error(err)
				return
			}
			if requests.Load() == 2 {
				args, err = json.Marshal(map[string]any{"cmd": filepath.Join(eciHome, "bin", "eci-command-gate-mode") + " set enforcing", "yield_time_ms": 1000, "max_output_tokens": 100})
				if err != nil {
					t.Error(err)
					return
				}
			}
			item = map[string]any{"type": "function_call", "id": "fc_e2e", "call_id": fmt.Sprintf("call_e2e_%d", requests.Load()), "name": "exec_command", "arguments": string(args)}
		default:
			item = map[string]any{"type": "message", "id": "msg_e2e", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "Fixture complete."}}}
		}
		for _, event := range []map[string]any{
			{"type": "response.created", "response": map[string]any{"id": "resp_e2e", "status": "in_progress", "output": []any{}}},
			{"type": "response.output_item.done", "output_index": 0, "item": item},
			{"type": "response.completed", "response": map[string]any{"id": "resp_e2e", "status": "completed", "output": []any{item}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}},
		} {
			data, err := json.Marshal(event)
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], data); err != nil {
				t.Error(err)
				return
			}
		}
	}))
	defer server.Close()
	config := fmt.Sprintf("model = 'fixture'\nmodel_provider = 'fixture'\napproval_policy = 'never'\nsandbox_mode = 'danger-full-access'\n[features]\nhooks = true\n[model_providers.fixture]\nname = 'Local fixture'\nbase_url = '%s'\nwire_api = 'responses'\nrequires_openai_auth = false\n", server.URL)
	// Reuse tracked trust for identical registered commands. Tracked keys use the
	// portable "~/.omp/agent/eci" root; rewrite it to the fixture location.
	liveConfig, err := os.ReadFile(filepath.Join(root, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(liveConfig), "\n")
	const trackedRoot = "~/.omp/agent/eci"
	for i, line := range lines {
		if strings.HasPrefix(line, "[hooks.state.\""+trackedRoot+"/hooks.json:") {
			config += strings.Replace(line, trackedRoot, eciHome, 1) + "\n"
			for j := i + 1; j < len(lines) && !strings.HasPrefix(lines[j], "["); j++ {
				config += lines[j] + "\n"
			}
		}
	}
	if err := os.WriteFile(filepath.Join(eciHome, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	binary := os.Getenv("ECI_E2E_BINARY")
	if binary == "" {
		binary = "/usr/bin/eci"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "app-server", "--stdio")
	command.Dir = evidence
	command.Env = append(os.Environ(), "HOME="+home, "ECI_HOME="+eciHome, "XDG_CONFIG_HOME="+filepath.Join(home, "config"), "XDG_STATE_HOME="+filepath.Join(home, "state"), "XDG_CACHE_HOME="+filepath.Join(home, "cache"), "ECI_HOOK_IS_SUBAGENT=true")
	var stdout, stderr bytes.Buffer
	command.Stderr = &stderr
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	// Cleanup cancels and reaps the app-server after any assertion failure.
	t.Cleanup(func() {
		cancel()
		if command.ProcessState == nil {
			if err := stdin.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
				t.Errorf("close app-server stdin: %v", err)
			}
			var exitError *exec.ExitError
			if err := command.Wait(); err != nil && !errors.As(err, &exitError) {
				t.Errorf("reap app-server: %v", err)
			}
		}
	})
	// send writes one protocol message to the real app-server.
	send := func(value any) {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprintln(stdin, string(data)); err != nil {
			t.Fatal(err)
		}
	}
	send(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]any{"name": "hook-e2e", "version": "1"}, "capabilities": map[string]any{"experimentalApi": true}}})
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), 4*1024*1024)
	allowed, denied := false, false
	shellResult, deniedExecution := false, false
	var hookFailure string
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		stdout.Write(line)
		stdout.WriteByte(10)
		var message map[string]any
		if err := json.Unmarshal(line, &message); err != nil {
			t.Fatal(err)
		}
		if message["method"] == "hook/completed" {
			params := message["params"].(map[string]any)
			run := params["run"].(map[string]any)
			if run["eventName"] == "preToolUse" {
				if run["sourcePath"] != filepath.Join(eciHome, "hooks.json") {
					hookFailure = "wrong hook source: " + string(line)
				}
				switch run["status"] {
				case "completed":
					allowed = true
				case "blocked":
					denied = true
				default:
					hookFailure = "unexpected hook status: " + string(line)
				}
			}
		}
		if message["method"] == "item/completed" {
			params := message["params"].(map[string]any)
			item := params["item"].(map[string]any)
			if item["type"] == "commandExecution" {
				switch item["id"] {
				case "call_e2e_1":
					shellResult = item["aggregatedOutput"] == "ECI_HOOK_E2E_READ_OK\n" && item["exitCode"] == float64(0) && item["status"] == "completed"
				case "call_e2e_2":
					deniedExecution = true
				}
			}
		}
		switch {
		case message["id"] == float64(1):
			send(map[string]any{"method": "initialized"})
			send(map[string]any{"id": 2, "method": "thread/start", "params": map[string]any{"cwd": evidence, "ephemeral": true, "approvalPolicy": "never", "sandbox": "danger-full-access"}})
		case message["id"] == float64(2):
			result, ok := message["result"].(map[string]any)
			if !ok {
				t.Fatalf("thread start: %s", line)
			}
			thread := result["thread"].(map[string]any)
			send(map[string]any{"id": 3, "method": "turn/start", "params": map[string]any{"threadId": thread["id"], "input": []any{map[string]any{"type": "text", "text": "Execute the fixture shell call, then finish.", "text_elements": []any{}}}}})
		case message["method"] == "turn/completed":
			if err := stdin.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	runErr := command.Wait()
	for name, data := range map[string][]byte{"events.jsonl": stdout.Bytes(), "stderr.log": stderr.Bytes()} {
		if err := os.WriteFile(filepath.Join(evidence, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	version, err := exec.Command(binary, "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	revision := exec.Command("git", "-C", root, "rev-parse", "HEAD")
	revisionOutput, err := revision.Output()
	if err != nil {
		t.Fatal(err)
	}
	sourceHash := sha256.New()
	for _, name := range []string{"hooks.json", "hooks/validate-bash.sh", "hooks/lib/eci-runtime-sync.sh", "hooks/lib/eci-native-binary.sh", "bin/eci-runtime-sync"} {
		content, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(sourceHash, "%s\x00%s\x00", name, content)
	}
	metadata := map[string]any{"started_at_utc": started.UTC().Format(time.RFC3339Nano), "finished_at_utc": time.Now().UTC().Format(time.RFC3339Nano), "elapsed_monotonic_seconds": time.Since(started).Seconds(), "environment": runtime.GOOS + "/" + runtime.GOARCH, "go_runtime": runtime.Version(), "runtime": strings.TrimSpace(string(version)), "command": command.Args, "scope": "registered Bash hook, ordinary shell allow and ECI control-owner JSON deny, local Responses SSE", "tested_revision": strings.TrimSpace(string(revisionOutput)), "runtime_route_sha256": fmt.Sprintf("%x", sourceHash.Sum(nil)), "source_root": root, "requests": requests.Load()}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evidence, "metadata.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if runErr != nil {
		t.Fatalf("ECI failed: %v\n%s\n%s", runErr, stdout.String(), stderr.String())
	}
	if !shellResult || deniedExecution {
		t.Fatalf("shell result absent:\n%s\n%s", stdout.String(), stderr.String())
	}
	if hookFailure != "" {
		t.Fatal(hookFailure)
	}
	if !allowed || !denied || !strings.Contains(stdout.String(), "ECI_CONTROL_OWNER_REQUIRED") {
		t.Fatalf("hook completion absent:\n%s\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), `"status":"failed"`) || strings.Contains(stdout.String(), "Hook failed") {
		t.Fatalf("hook failure:\n%s", stdout.String())
	}
	t.Logf("evidence: %s", evidence)
}
