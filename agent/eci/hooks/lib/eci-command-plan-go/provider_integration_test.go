package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCanonicalPlannerPublicationMatchesReceipt checks the planner published into the canonical bin/ against its
// <name>.sha256 receipt, the file hooks/lib/eci-native-binary.sh verifies before it runs the tool.
func TestCanonicalPlannerPublicationMatchesReceipt(t *testing.T) {
	t.Parallel()

	home := os.Getenv("HOME")
	if home == "" {
		t.Fatal("HOME must be set to locate the published planner")
	}
	publishedBinary := filepath.Join(home, ".omp", "agent", "eci", "bin", "eci-command-plan")
	receipt, err := os.ReadFile(publishedBinary + ".sha256")
	if err != nil {
		t.Fatalf("read planner receipt: %v", err)
	}
	if want := fileSHA256(t, publishedBinary) + "  eci-command-plan\n"; string(receipt) != want {
		t.Errorf("planner receipt: got %q, want %q", receipt, want)
	}
	info, err := os.Stat(publishedBinary)
	if err != nil {
		t.Fatalf("stat published planner binary: %v", err)
	}
	if got := fmt.Sprintf("%03o", info.Mode().Perm()); got != "755" {
		t.Errorf("published planner binary mode: got %s, want 755", got)
	}
}

// fileSHA256 returns a regular fixture file's SHA-256 content digest.
//
// Example: publication parity compares a source file to its receipt digest.
func fileSHA256(t *testing.T, path string) string {
	t.Helper()

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents))
}

// TestProviderValidatorsUseCompiledJSONInterface runs the source omp validator
// against the TestMain-provisioned planner, republished with its receipt in a
// fixture home. The callback JSON on stdin reaches the compiled planner: the
// active marker and the role change the verdict, the validator output is the
// planner's own --hook output, and an unverified planner fails closed. The
// validator holds no command routing of its own (hooks/validate-bash.sh runs the
// verified planner with --hook and passes its decision through); the Git fast-capability checks are
// TestInstalledBinaryRejectsRepositoryDefaultGitArchiveFastCapability and
// TestInstalledBinaryRejectsDirectPathGitStatusFastCapability.
//
// Example: an active coordinator's "rm -rf /" is denied with the planner's broad
// destructive code; with no marker the same callback is admitted.
func TestProviderValidatorsUseCompiledJSONInterface(t *testing.T) {
	t.Parallel()

	planner := testPlannerBinary
	sourceRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	fixtureRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("canonicalize fixture root: %v", err)
	}
	// The native loader rejects a bin directory with a group/other-writable ancestor.
	if err := os.Chmod(fixtureRoot, 0o700); err != nil {
		t.Fatalf("restrict fixture root: %v", err)
	}
	home := filepath.Join(fixtureRoot, "home")
	canonicalRoot := filepath.Join(home, ".omp", "agent", "eci")
	binDirectory := filepath.Join(canonicalRoot, "bin")
	proofRoot := filepath.Join(fixtureRoot, "proof")
	sessionsProject := filepath.Join(home, ".omp", "agent", "sessions", "project")
	temporaryRoot := filepath.Join(fixtureRoot, "tmp")
	const sessionID = "validator-interface"
	for _, directory := range []string{binDirectory, filepath.Join(proofRoot, sessionID), sessionsProject, temporaryRoot} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("create %s: %v", directory, err)
		}
	}
	copyStopGateSyntaxFixtureTree(t, filepath.Join(sourceRoot, "hooks"), filepath.Join(canonicalRoot, "hooks"))
	contents, err := os.ReadFile(planner)
	if err != nil {
		t.Fatalf("read provisioned planner: %v", err)
	}
	published := filepath.Join(binDirectory, "eci-command-plan")
	if err := os.WriteFile(published, contents, 0o755); err != nil {
		t.Fatalf("publish planner: %v", err)
	}
	receipt := published + ".sha256"
	if err := os.WriteFile(receipt, []byte(fileSHA256(t, published)+"  eci-command-plan\n"), 0o644); err != nil {
		t.Fatalf("write planner receipt: %v", err)
	}
	transcript := filepath.Join(sessionsProject, "main.jsonl")
	if err := os.WriteFile(transcript, []byte(`{"type":"session","version":3,"id":"`+sessionID+`"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write main transcript: %v", err)
	}
	marker := filepath.Join(proofRoot, sessionID, "eci_active")

	// run feeds one callback envelope to argv (the validator or the planner) and
	// returns stdout; a non-zero exit fails the test.
	run := func(worker bool, command string, argv ...string) string {
		t.Helper()
		request, err := json.Marshal(map[string]any{
			"session_id":      sessionID,
			"tool_name":       "Bash",
			"transcript_path": transcript,
			"cwd":             fixtureRoot,
			"tool_input":      map[string]string{"command": command},
		})
		if err != nil {
			t.Fatalf("marshal callback: %v", err)
		}
		hook := exec.Command(argv[0], argv[1:]...)
		hook.Dir = fixtureRoot
		hook.Env = []string{
			"HOME=" + home,
			"PATH=/usr/local/bin:/usr/bin:/bin",
			"ECI_PROOF_ROOT=" + proofRoot,
			fmt.Sprintf("ECI_HOOK_IS_SUBAGENT=%t", worker),
			"TMPDIR=" + temporaryRoot,
			"ECI_TMPDIR=" + temporaryRoot,
			"XDG_CONFIG_HOME=" + filepath.Join(fixtureRoot, "xdg-config"),
		}
		hook.Stdin = bytes.NewReader(request)
		var stdout, stderr bytes.Buffer
		hook.Stdout, hook.Stderr = &stdout, &stderr
		if err := hook.Run(); err != nil {
			t.Fatalf("%v %q: %v; stdout=%s stderr=%s", argv, command, err, stdout.String(), stderr.String())
		}
		return stdout.String()
	}
	validator := []string{"/usr/bin/bash", filepath.Join(canonicalRoot, "hooks", "validate-bash.sh")}
	// denial returns the permission decision and reason of a validator output.
	denial := func(output string) (string, string) {
		t.Helper()
		var result struct {
			HookSpecificOutput struct {
				PermissionDecision       string `json:"permissionDecision"`
				PermissionDecisionReason string `json:"permissionDecisionReason"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("decode validator output: %v; output=%s", err, output)
		}
		return result.HookSpecificOutput.PermissionDecision, result.HookSpecificOutput.PermissionDecisionReason
	}

	if output := run(false, "rm -rf /", validator...); strings.TrimSpace(output) != "" {
		t.Fatalf("inactive scope: validator output=%s, want admission", output)
	}
	if err := os.WriteFile(marker, []byte("scope: validator interface\ncwd: "+fixtureRoot+"\nsession_id: "+sessionID+"\ncreated_utc: 2026-08-27T00:00:00Z\n"), 0o600); err != nil {
		t.Fatalf("write active marker: %v", err)
	}
	for _, testCase := range []struct {
		name     string
		worker   bool
		command  string
		wantCode DiagnosticCode
	}{
		{name: "coordinator ordinary", command: "true"},
		{name: "coordinator broad destructive", command: "rm -rf /", wantCode: CodeBroadDestructiveDenied},
		{name: "worker broad live control", worker: true, command: "rm -rf /", wantCode: CodeControlOwnerRequired},
		{name: "coordinator marker write", command: "touch " + marker, wantCode: CodePlanLiveControlDenied},
	} {
		output := run(testCase.worker, testCase.command, validator...)
		if direct := run(testCase.worker, testCase.command, published, "--hook"); output != direct {
			t.Errorf("%s: validator output=%q, want the planner's --hook output %q", testCase.name, output, direct)
		}
		if testCase.wantCode == "" {
			if strings.TrimSpace(output) != "" {
				t.Errorf("%s: validator output=%s, want admission", testCase.name, output)
			}
			continue
		}
		decision, reason := denial(output)
		if decision != "deny" || !strings.HasPrefix(reason, "["+string(testCase.wantCode)+"]") {
			t.Errorf("%s: decision=%q reason=%q, want deny with code %s", testCase.name, decision, reason, testCase.wantCode)
		}
	}

	if err := os.Remove(receipt); err != nil {
		t.Fatalf("remove planner receipt: %v", err)
	}
	if decision, _ := denial(run(false, "true", validator...)); decision != "deny" {
		t.Fatalf("unverified planner: decision=%q, want deny", decision)
	}
}

func TestInstalledBinaryProviderParity(t *testing.T) {
	t.Parallel()

	binary := testPlannerBinary
	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		request := activeWorker("printf before | env | printf after")
		request.Provider = provider
		input, err := json.Marshal(request)
		if err != nil {
			t.Fatalf("marshal %s request: %v", provider, err)
		}

		var stdout bytes.Buffer
		status := runBinary(t, binary, input, &stdout)
		if status != StatusAllow {
			t.Fatalf("%s status: got %d, want %d; output=%s", provider, status, StatusAllow, stdout.String())
		}
		var result Result
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatalf("decode %s result: %v", provider, err)
		}
		if result.Diagnostic != nil {
			t.Fatalf("%s diagnostic: %#v", provider, result.Diagnostic)
		}
	}
}

// TestInstalledBinaryRoutesReadOnlyLifecycleStateByRole verifies that the
// installed planner routes read-only lifecycle discovery without treating a
// worker's status command as coordinator-owned control.
//
// Example: env -- eci-active status is a provider-owned read-only defer for
// both worker and coordinator callbacks.
func TestInstalledBinaryRoutesReadOnlyLifecycleStateByRole(t *testing.T) {
	t.Parallel()

	binary := testPlannerBinary
	testCases := []struct {
		name            string
		role            Role
		command         string
		status          int
		decision        DecisionKind
		diagnosticCode  DiagnosticCode
		diagnosticMatch string
	}{
		{
			name:     "worker direct",
			role:     RoleWorker,
			command:  "eci-active status",
			status:   StatusDefer,
			decision: DecisionDefer,
		},
		{
			name:     "worker transparent wrapper",
			role:     RoleWorker,
			command:  "env -- eci-active status",
			status:   StatusDefer,
			decision: DecisionDefer,
		},
		{
			name:     "coordinator",
			role:     RoleCoordinator,
			command:  "eci-active status",
			status:   StatusDefer,
			decision: DecisionDefer,
		},
	}
	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()
			for _, testCase := range testCases {
				testCase := testCase
				t.Run(testCase.name, func(t *testing.T) {
					t.Parallel()
					input, err := json.Marshal(Request{
						Provider:      provider,
						Role:          testCase.role,
						CWD:           providerHome(provider),
						Marker:        MarkerActive,
						ActiveSession: "test-session",
						Command:       testCase.command,
					})
					if err != nil {
						t.Fatalf("marshal request: %v", err)
					}
					var output bytes.Buffer
					status := runBinary(t, binary, input, &output)
					if status != testCase.status {
						t.Fatalf("status: got %d, want %d; output=%s", status, testCase.status, output.String())
					}
					var result Result
					if err := json.Unmarshal(output.Bytes(), &result); err != nil {
						t.Fatalf("decode result: %v", err)
					}
					if result.Decision != testCase.decision {
						t.Fatalf("decision: got %q, want %q; diagnostic=%#v", result.Decision, testCase.decision, result.Diagnostic)
					}
					if testCase.diagnosticCode == "" {
						if result.Diagnostic != nil {
							t.Fatalf("diagnostic: got %#v, want nil", result.Diagnostic)
						}
						return
					}
					if result.Diagnostic == nil {
						t.Fatal("diagnostic: got nil, want worker lifecycle denial")
					}
					if result.Diagnostic.Code != testCase.diagnosticCode {
						t.Errorf("diagnostic code: got %q, want %q", result.Diagnostic.Code, testCase.diagnosticCode)
					}
					if result.Diagnostic.Predicate != testCase.diagnosticMatch {
						t.Errorf("diagnostic predicate: got %q, want %q", result.Diagnostic.Predicate, testCase.diagnosticMatch)
					}
				})
			}
		})
	}
}

// TestInstalledBinaryRoutesActiveGitByRole verifies that the shipped planner
// returns status 3 for active raw Git forms, leaving their final policy
// decision to each provider validator.
//
// Example: git status and env --chdir /tmp git status must not become generic
// literal admissions merely because their unwrapped Git child is supported.
func TestInstalledBinaryRoutesActiveGitByRole(t *testing.T) {
	t.Parallel()

	binary := testPlannerBinary
	runInstalled := func(t *testing.T, request Request) (int, Result) {
		t.Helper()
		input, err := json.Marshal(request)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		var output bytes.Buffer
		status := runBinary(t, binary, input, &output)
		var result Result
		if err := json.Unmarshal(output.Bytes(), &result); err != nil {
			t.Fatalf("decode result: %v; output=%s", err, output.String())
		}
		return status, result
	}
	activeCommands := []struct {
		name    string
		command string
	}{
		{name: "direct status", command: "git status"},
		{name: "direct archive", command: "git archive HEAD"},
		{name: "quoted status", command: "'git' status"},
		{name: "quoted archive", command: `"git" archive HEAD`},
		{name: "path-qualified status", command: "/usr/bin/git status"},
		{name: "path-qualified archive", command: "/usr/bin/git archive HEAD"},
		{name: "bare environment", command: "env git status"},
		{name: "assignment", command: "env FOO=bar git status"},
		{name: "ignore environment short", command: "env -i git status"},
		{name: "ignore environment long", command: "env --ignore-environment git status"},
		{name: "unset short", command: "env -u FOO git status"},
		{name: "unset long", command: "env --unset FOO git status"},
		{name: "chdir short", command: "env -C /tmp git status"},
		{name: "chdir long", command: "env --chdir /tmp git status"},
		{name: "end options", command: "env -- git status"},
		{name: "path-qualified environment", command: "/usr/bin/env git status"},
		{name: "path-qualified child", command: "env /usr/bin/git status"},
		{name: "quoted environment", command: `"env" git status`},
		{name: "quoted path-qualified environment", command: `"/usr/bin/env" git status`},
		{name: "quoted child", command: `env "git" status`},
		{name: "nested transparent wrapper", command: "timeout 5 env FOO=bar git status"},
		{name: "archive child", command: "env FOO=bar git archive HEAD"},
		{name: "stdbuf status", command: "stdbuf -oL git status"},
		{name: "busybox archive", command: "busybox -- git archive HEAD"},
		{name: "chronic status", command: "chronic git status"},
		{name: "systemd-run archive", command: "systemd-run --unit eci git archive HEAD"},
		{name: "sudo status", command: "sudo -n git status"},
		{name: "environment stdbuf archive", command: "env FOO=bar stdbuf -oL git archive HEAD"},
		{name: "environment busybox status", command: "env FOO=bar busybox -- git status"},
		{name: "environment chronic archive", command: "env FOO=bar chronic git archive HEAD"},
		{name: "environment systemd-run status", command: "env FOO=bar systemd-run --unit eci git status"},
		{name: "environment sudo archive", command: "env FOO=bar sudo -n git archive HEAD"},
	}

	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()
			for _, role := range []Role{RoleCoordinator, RoleWorker} {
				role := role
				t.Run(string(role), func(t *testing.T) {
					t.Parallel()
					for _, testCase := range activeCommands {
						testCase := testCase
						t.Run(testCase.name, func(t *testing.T) {
							status, result := runInstalled(t, Request{
								Provider:       provider,
								Role:           role,
								CWD:            providerHome(provider),
								Marker:         MarkerActive,
								ActiveSession:  "test-session",
								Command:        testCase.command,
								CommandPath:    os.Getenv("PATH"),
								CommandPathSet: true,
							})
							if status != StatusDefer || result.Decision != DecisionDefer || result.Diagnostic != nil {
								t.Fatalf("%q: status=%d decision=%q diagnostic=%#v, want status 3 defer without diagnostic", testCase.command, status, result.Decision, result.Diagnostic)
							}
							if len(result.Capabilities) != 0 || result.DeferredRoute != "" {
								t.Fatalf("%q: capabilities=%v deferred_route=%q, want no generic admission", testCase.command, result.Capabilities, result.DeferredRoute)
							}
						})
					}
				})
			}
		})
	}

	counterexamples := []struct {
		name     string
		marker   Marker
		role     Role
		command  string
		status   int
		decision DecisionKind
		code     DiagnosticCode
		allRoles bool
	}{
		{
			name:     "inactive direct Git remains allowed",
			marker:   MarkerInactive,
			command:  "git status",
			status:   StatusAllow,
			decision: DecisionAllow,
			allRoles: true,
		},
		{
			name:     "inactive environment Git remains allowed",
			marker:   MarkerInactive,
			command:  "env FOO=bar git status",
			status:   StatusAllow,
			decision: DecisionAllow,
			allRoles: true,
		},
		{
			name:     "Git environment context defers to Git",
			marker:   MarkerActive,
			command:  "env GIT_DIR=/tmp/git-dir git status",
			status:   StatusDefer,
			decision: DecisionDefer,
			allRoles: true,
		},
		{
			name:     "worker checkpoint defers to repository effect checks",
			marker:   MarkerActive,
			role:     RoleWorker,
			command:  "git commit -m nope",
			status:   StatusDefer,
			decision: DecisionDefer,
		},
		{
			name:     "coordinator checkpoint defers to producer ownership checks",
			marker:   MarkerActive,
			role:     RoleCoordinator,
			command:  "git commit -m nope",
			status:   StatusDefer,
			decision: DecisionDefer,
		},
		{
			name:     "non-Git environment wrapper remains allowed",
			marker:   MarkerActive,
			command:  "env FOO=bar novel-tool --flag",
			status:   StatusAllow,
			decision: DecisionAllow,
			allRoles: true,
		},
	}
	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		for _, testCase := range counterexamples {
			testCase := testCase
			roles := []Role{testCase.role}
			if testCase.allRoles {
				roles = []Role{RoleCoordinator, RoleWorker}
			}
			for _, role := range roles {
				role := role
				t.Run(string(provider)+"/"+testCase.name+"/"+string(role), func(t *testing.T) {
					status, result := runInstalled(t, Request{
						Provider:      provider,
						Role:          role,
						CWD:           providerHome(provider),
						Marker:        testCase.marker,
						ActiveSession: "test-session",
						Command:       testCase.command,
					})
					if status != testCase.status || result.Decision != testCase.decision {
						t.Fatalf("%q: status=%d decision=%q, want status=%d decision=%q; diagnostic=%#v", testCase.command, status, result.Decision, testCase.status, testCase.decision, result.Diagnostic)
					}
					if testCase.code == "" {
						if result.Diagnostic != nil {
							t.Fatalf("%q: diagnostic=%#v, want nil", testCase.command, result.Diagnostic)
						}
					} else if result.Diagnostic == nil || result.Diagnostic.Code != testCase.code {
						var code DiagnosticCode
						if result.Diagnostic != nil {
							code = result.Diagnostic.Code
						}
						t.Fatalf("%q: diagnostic code=%q, want %q", testCase.command, code, testCase.code)
					}
					if len(result.Capabilities) != 0 || result.DeferredRoute != "" {
						t.Fatalf("%q: capabilities=%v deferred_route=%q, want no generic admission", testCase.command, result.Capabilities, result.DeferredRoute)
					}
				})
			}
		}
	}
}

func TestInstalledBinaryRoutesActivePreCommitHookModeRepairByRole(t *testing.T) {
	t.Parallel()

	binary := testPlannerBinary
	const command = "chmod 755 hooks/pre-commit-go-mod.sh"
	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()
			providerRoot := providerHome(provider)

			workerRequest := Request{
				Provider:      provider,
				Role:          RoleWorker,
				CWD:           providerRoot,
				Marker:        MarkerActive,
				ActiveSession: "test-session",
				Command:       command,
			}
			workerInput, err := json.Marshal(workerRequest)
			if err != nil {
				t.Fatalf("marshal worker request: %v", err)
			}
			var workerOutput bytes.Buffer
			workerStatus := runBinary(t, binary, workerInput, &workerOutput)
			if workerStatus != StatusDeny {
				t.Fatalf("worker status: got %d, want %d; output=%s", workerStatus, StatusDeny, workerOutput.String())
			}
			var workerResult Result
			if err := json.Unmarshal(workerOutput.Bytes(), &workerResult); err != nil {
				t.Fatalf("decode worker result: %v", err)
			}
			if workerResult.Decision != DecisionDeny || workerResult.Diagnostic == nil {
				t.Fatalf("worker result: decision=%q diagnostic=%#v, want deny with diagnostic", workerResult.Decision, workerResult.Diagnostic)
			}
			diagnostic := workerResult.Diagnostic
			if diagnostic.Code != CodeControlOwnerRequired {
				t.Errorf("worker code: got %q, want %q", diagnostic.Code, CodeControlOwnerRequired)
			}
			if diagnostic.Operation != "worker-control" {
				t.Errorf("worker operation: got %q, want worker-control", diagnostic.Operation)
			}
			if diagnostic.Predicate != "hook-mode-repair" {
				t.Errorf("worker predicate: got %q, want hook-mode-repair", diagnostic.Predicate)
			}
			if diagnostic.Token != "hooks/pre-commit-go-mod.sh" {
				t.Errorf("worker token: got %q, want hooks/pre-commit-go-mod.sh", diagnostic.Token)
			}
			if diagnostic.ArgvIndex != 2 {
				t.Errorf("worker argv index: got %d, want 2", diagnostic.ArgvIndex)
			}

			coordinatorRequest := workerRequest
			coordinatorRequest.Role = RoleCoordinator
			coordinatorInput, err := json.Marshal(coordinatorRequest)
			if err != nil {
				t.Fatalf("marshal coordinator request: %v", err)
			}
			var coordinatorOutput bytes.Buffer
			coordinatorStatus := runBinary(t, binary, coordinatorInput, &coordinatorOutput)
			if coordinatorStatus != StatusDefer {
				t.Fatalf("coordinator status: got %d, want %d; output=%s", coordinatorStatus, StatusDefer, coordinatorOutput.String())
			}
			var coordinatorResult Result
			if err := json.Unmarshal(coordinatorOutput.Bytes(), &coordinatorResult); err != nil {
				t.Fatalf("decode coordinator result: %v", err)
			}
			if coordinatorResult.Decision != DecisionDefer || coordinatorResult.Diagnostic != nil {
				t.Fatalf("coordinator result: decision=%q diagnostic=%#v, want defer without diagnostic", coordinatorResult.Decision, coordinatorResult.Diagnostic)
			}

			for _, testCase := range []struct {
				name    string
				command string
				marker  Marker
			}{
				{name: "wrapper", command: "env chmod 755 hooks/pre-commit-go-mod.sh", marker: MarkerActive},
				{name: "executable alias", command: "/bin/chmod 755 hooks/pre-commit-go-mod.sh", marker: MarkerActive},
				{name: "alternate target spelling", command: "chmod 755 ./hooks/pre-commit-go-mod.sh", marker: MarkerActive},
				{name: "quoted executable", command: `ch"mod" 755 hooks/pre-commit-go-mod.sh`, marker: MarkerActive},
				{name: "quoted mode", command: `chmod "755" hooks/pre-commit-go-mod.sh`, marker: MarkerActive},
				{name: "quoted target", command: `chmod 755 "hooks/pre-commit-go-mod.sh"`, marker: MarkerActive},
				{name: "another target", command: "chmod 755 hooks/install-pre-commit-go-mod.sh", marker: MarkerActive},
				{name: "and compound", command: command + " && printf after", marker: MarkerActive},
				{name: "semicolon compound", command: "printf before; " + command, marker: MarkerActive},
				{name: "pipeline compound", command: command + " | printf after", marker: MarkerActive},
				{name: "pipeline preceding compound", command: "printf before | " + command, marker: MarkerActive},
				{name: "interpreter wrapper", command: "bash -c '" + command + "'", marker: MarkerActive},
				{name: "inactive exact", command: command, marker: MarkerInactive},
			} {
				testCase := testCase
				t.Run(testCase.name, func(t *testing.T) {
					request := workerRequest
					request.Marker = testCase.marker
					request.Command = testCase.command
					input, err := json.Marshal(request)
					if err != nil {
						t.Fatalf("marshal request: %v", err)
					}
					var output bytes.Buffer
					status := runBinary(t, binary, input, &output)
					if status == StatusInternal {
						t.Fatalf("status: got internal error; output=%s", output.String())
					}
					var result Result
					if err := json.Unmarshal(output.Bytes(), &result); err != nil {
						t.Fatalf("decode result: %v", err)
					}
					if result.Diagnostic != nil && result.Diagnostic.Predicate == "hook-mode-repair" {
						t.Fatalf("%s selected hook-mode-repair: status=%d result=%#v", testCase.command, status, result)
					}
					if testCase.marker == MarkerInactive && (status != StatusAllow || result.Decision != DecisionAllow || result.Diagnostic != nil) {
						t.Fatalf("inactive exact command: status=%d result=%#v, want allow without diagnostic", status, result)
					}
				})
			}
		})
	}
}

func TestInstalledBinaryDeniesActiveWorkerProtectedHookModeMutations(t *testing.T) {
	t.Parallel()

	binary := testPlannerBinary
	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()
			providerRoot := providerHome(provider)

			for _, command := range []string{
				"env chmod 755 hooks/pre-commit-go-mod.sh",
				"command chmod 755 hooks/pre-commit-go-mod.sh",
				`ch"mod" 755 hooks/pre-commit-go-mod.sh`,
				`ch"mod" 644 hooks/pre-commit-go-mod.sh`,
				`chmod "755" hooks/pre-commit-go-mod.sh`,
				`chmod 755 "hooks/pre-commit-go-mod.sh"`,
				"/bin/chmod 755 hooks/pre-commit-go-mod.sh",
				"chmod 755 ./hooks/pre-commit-go-mod.sh",
				"chmod 644 hooks/pre-commit-go-mod.sh",
				"chmod 755 hooks/install-pre-commit-go-mod.sh",
				"chmod 644 hooks/validate-bash.sh",
				"chmod 755 hooks/tests/test-pre-commit-go-mod.sh",
				"chmod 644 " + filepath.Join(providerRoot, "hooks", "validate-bash.sh"),
				"stdbuf -oL chmod 644 hooks/validate-bash.sh",
				"busybox chmod 644 hooks/validate-bash.sh",
				"busybox -- chmod 644 hooks/validate-bash.sh",
				"chmod -R 644 hooks",
				"chmod --recursive 644 hooks",
				"chmod -R 644 .",
				"chmod -R 644 ..",
				"chmod -vR 644 hooks",
				"chmod --rec 755 hooks",
				"chmod 755 -R hooks",
				"chmod 755 --rec hooks",
				"chmod 755 --recursive hooks",
				"chmod 755 -vR hooks",
				"chmod 755 hooks --rec",
				"chmod 755 hooks -R",
				"chmod --ref ordinary.txt hooks/validate-bash.sh",
				"chmod --ref=ordinary.txt hooks/validate-bash.sh",
				"chmod hooks/validate-bash.sh --ref=ordinary.txt",
				"chmod --ref ordinary.txt -R hooks",
				"chmod -R --ref=ordinary.txt hooks",
				"env chmod --ref=ordinary.txt hooks/validate-bash.sh",
				"stdbuf -oL chmod --ref=ordinary.txt hooks/validate-bash.sh",
				"busybox -- chmod --ref=ordinary.txt hooks/validate-bash.sh",
				"stdbuf -oL chmod --rec 755 hooks",
				"busybox -- chmod 755 hooks --rec",
				"stdbuf -oL chmod -R 644 hooks",
				"busybox -- chmod --recursive 644 hooks",
			} {
				command := command
				t.Run(command, func(t *testing.T) {
					t.Parallel()

					input, err := json.Marshal(Request{
						Provider:      provider,
						Role:          RoleWorker,
						CWD:           providerRoot,
						Marker:        MarkerActive,
						ActiveSession: "test-session",
						Command:       command,
					})
					if err != nil {
						t.Fatalf("marshal request: %v", err)
					}
					var output bytes.Buffer
					status := runBinary(t, binary, input, &output)
					if status != StatusDeny {
						t.Fatalf("status: got %d, want %d; output=%s", status, StatusDeny, output.String())
					}
					var result Result
					if err := json.Unmarshal(output.Bytes(), &result); err != nil {
						t.Fatalf("decode result: %v", err)
					}
					if result.Diagnostic == nil {
						t.Fatal("diagnostic: got nil, want generic control denial")
					}
					if result.Diagnostic.Code != CodeControlOwnerRequired {
						t.Errorf("code: got %q, want %q", result.Diagnostic.Code, CodeControlOwnerRequired)
					}
					if result.Diagnostic.Operation != "worker-control" {
						t.Errorf("operation: got %q, want worker-control", result.Diagnostic.Operation)
					}
					if result.Diagnostic.Predicate != "worker-hook-mode-ownership" {
						t.Errorf("predicate: got %q, want worker-hook-mode-ownership", result.Diagnostic.Predicate)
					}
				})
			}
		})
	}
}

func TestInstalledBinaryDefersActiveCoordinatorProtectedHookModeMutations(t *testing.T) {
	t.Parallel()

	binary := testPlannerBinary
	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()
			providerRoot := providerHome(provider)

			for _, command := range []string{
				"chmod 644 hooks/validate-bash.sh",
				`chmod "644" hooks/pre-commit-go-mod.sh`,
				"chmod 755 hooks/pre-commit-go-mod.sh && printf after",
				"env chmod 644 hooks/validate-bash.sh",
				"stdbuf -oL chmod 644 hooks/validate-bash.sh",
				"busybox -- chmod 644 hooks/validate-bash.sh",
				"chmod -R 644 hooks",
				"chmod --recursive 644 hooks",
				"chmod -R 644 .",
				"chmod -R 644 ..",
				"chmod -vR 644 hooks",
				"chmod --rec 755 hooks",
				"chmod 755 -R hooks",
				"chmod 755 --rec hooks",
				"chmod 755 --recursive hooks",
				"chmod 755 -vR hooks",
				"chmod 755 hooks --rec",
				"chmod 755 hooks -R",
				"chmod --ref ordinary.txt hooks/validate-bash.sh",
				"chmod --ref=ordinary.txt hooks/validate-bash.sh",
				"chmod hooks/validate-bash.sh --ref=ordinary.txt",
				"chmod --ref ordinary.txt -R hooks",
				"chmod -R --ref=ordinary.txt hooks",
				"env chmod --ref=ordinary.txt hooks/validate-bash.sh",
				"stdbuf -oL chmod --ref=ordinary.txt hooks/validate-bash.sh",
				"busybox -- chmod --ref=ordinary.txt hooks/validate-bash.sh",
				"stdbuf -oL chmod --rec 755 hooks",
				"busybox -- chmod 755 hooks --rec",
				"stdbuf -oL chmod -R 644 hooks",
				"busybox -- chmod --recursive 644 hooks",
			} {
				command := command
				t.Run(command, func(t *testing.T) {
					t.Parallel()

					input, err := json.Marshal(Request{
						Provider:      provider,
						Role:          RoleCoordinator,
						CWD:           providerRoot,
						Marker:        MarkerActive,
						ActiveSession: "test-session",
						Command:       command,
					})
					if err != nil {
						t.Fatalf("marshal request: %v", err)
					}
					var output bytes.Buffer
					status := runBinary(t, binary, input, &output)
					if status != StatusDefer {
						t.Fatalf("status: got %d, want %d; output=%s", status, StatusDefer, output.String())
					}
					var result Result
					if err := json.Unmarshal(output.Bytes(), &result); err != nil {
						t.Fatalf("decode result: %v", err)
					}
					if result.Decision != DecisionDefer || result.Diagnostic != nil {
						t.Fatalf("result: decision=%q diagnostic=%#v, want defer without diagnostic", result.Decision, result.Diagnostic)
					}
				})
			}
		})
	}
}

func TestInstalledBinaryAllowsSameNamedHookPathOutsideProviderRoot(t *testing.T) {
	t.Parallel()

	binary := testPlannerBinary
	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()

			for _, command := range []string{
				"chmod 644 hooks/validate-bash.sh",
				"chmod -R 644 .",
			} {
				input, err := json.Marshal(Request{
					Provider:      provider,
					Role:          RoleWorker,
					CWD:           "/tmp",
					Marker:        MarkerActive,
					ActiveSession: "test-session",
					Command:       command,
				})
				if err != nil {
					t.Fatalf("marshal request: %v", err)
				}
				var output bytes.Buffer
				status := runBinary(t, binary, input, &output)
				if status != StatusAllow {
					t.Fatalf("%q: status: got %d, want %d; output=%s", command, status, StatusAllow, output.String())
				}
				var result Result
				if err := json.Unmarshal(output.Bytes(), &result); err != nil {
					t.Fatalf("decode result: %v", err)
				}
				if result.Decision != DecisionAllow || result.Diagnostic != nil {
					t.Fatalf("%q: result: decision=%q diagnostic=%#v, want allow without diagnostic", command, result.Decision, result.Diagnostic)
				}
			}
		})
	}
}

func TestInstalledBinaryAllowsActiveWorkerOrdinaryChmod(t *testing.T) {
	t.Parallel()

	binary := testPlannerBinary
	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()
			providerRoot := providerHome(provider)

			for _, command := range []string{
				"chmod 644 ordinary.txt",
				"chmod -R 644 ordinary-dir",
				"chmod 644 -R ordinary-dir",
				"chmod 644 ordinary-dir -R",
				"chmod --rec 644 ordinary-dir",
				"chmod 755 -- -R hooks",
				"chmod 755 hooks -- -R",
				"chmod 755 hooks -- --rec",
				"chmod --ref=hooks/validate-bash.sh ordinary.txt",
				"chmod --ref hooks/validate-bash.sh ordinary.txt",
				"chmod ordinary.txt --ref=hooks/validate-bash.sh",
				"chmod -R --ref=hooks/validate-bash.sh ordinary-dir",
				"chmod --reference=hooks/validate-bash.sh ordinary.txt",
				"chmod -R --reference=hooks/validate-bash.sh ordinary-dir",
				"chmod --reference hooks/validate-bash.sh ordinary.txt",
				"chmod -R --reference hooks/validate-bash.sh ordinary-dir",
			} {
				input, err := json.Marshal(Request{
					Provider:      provider,
					Role:          RoleWorker,
					CWD:           providerRoot,
					Marker:        MarkerActive,
					ActiveSession: "test-session",
					Command:       command,
				})
				if err != nil {
					t.Fatalf("marshal request: %v", err)
				}
				var output bytes.Buffer
				status := runBinary(t, binary, input, &output)
				if status != StatusAllow {
					t.Fatalf("%q: status: got %d, want %d; output=%s", command, status, StatusAllow, output.String())
				}
				var result Result
				if err := json.Unmarshal(output.Bytes(), &result); err != nil {
					t.Fatalf("decode result: %v", err)
				}
				if result.Decision != DecisionAllow || result.Diagnostic != nil {
					t.Fatalf("%q: result: decision=%q diagnostic=%#v, want allow without diagnostic", command, result.Decision, result.Diagnostic)
				}
			}
		})
	}
}

func TestInstalledBinaryDeniesActiveLedgerWritersForBothProviders(t *testing.T) {
	t.Parallel()

	binary := testPlannerBinary
	proofRoot := t.TempDir()
	sessionDir := filepath.Join(proofRoot, "proof", "session")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("create active session: %v", err)
	}
	marker := filepath.Join(sessionDir, "eci_active")
	if err := os.WriteFile(marker, []byte("active\n"), 0o600); err != nil {
		t.Fatalf("write active marker: %v", err)
	}
	ordinaryRoot := t.TempDir()
	ordinaryPath := filepath.Join(ordinaryRoot, "ordinary.md")
	if err := os.WriteFile(ordinaryPath, []byte("ordinary\n"), 0o600); err != nil {
		t.Fatalf("write ordinary file: %v", err)
	}
	// The read and ordinary-write cases run from an existing cwd outside the session
	// directory; the planner treats a cwd that is not a verified directory as unknown and defers.
	unrelatedCWD := filepath.Join(proofRoot, "unrelated-cwd")
	if err := os.MkdirAll(unrelatedCWD, 0o700); err != nil {
		t.Fatalf("create unrelated cwd: %v", err)
	}

	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()

			for _, name := range []string{"high_level_log.jsonl", "high_level_log.anchor"} {
				name := name
				t.Run(name, func(t *testing.T) {
					path := filepath.Join(sessionDir, name)
					if err := os.WriteFile(path, []byte("{\"event\":\"decision\",\"summary\":\"entry\"}\n"), 0o600); err != nil {
						t.Fatalf("write %s: %v", name, err)
					}

					request := Request{
						Provider:      provider,
						Role:          RoleCoordinator,
						CWD:           proofRoot,
						Marker:        MarkerActive,
						ActiveSession: "session",
						Command:       "sed -i '1p' " + path,
						ActiveMarkers: []string{marker},
					}
					input, err := json.Marshal(request)
					if err != nil {
						t.Fatalf("marshal %s request: %v", name, err)
					}
					var stdout bytes.Buffer
					status := runBinary(t, binary, input, &stdout)
					if status != StatusDeny {
						t.Fatalf("%s status: got %d, want %d; output=%s", name, status, StatusDeny, stdout.String())
					}
					var result Result
					if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
						t.Fatalf("decode %s result: %v", name, err)
					}
					if result.Diagnostic == nil {
						t.Fatalf("%s diagnostic: missing diagnostic in %#v", name, result)
					}
					diagnostic := result.Diagnostic
					if diagnostic.Code != DiagnosticCode("ECI_LEDGER_APPEND_ONLY") {
						t.Errorf("%s code: got %q, want ECI_LEDGER_APPEND_ONLY", name, diagnostic.Code)
					}
					if diagnostic.Operation != "ledger-append-only" {
						t.Errorf("%s operation: got %q, want ledger-append-only", name, diagnostic.Operation)
					}
					if diagnostic.Path != path {
						t.Errorf("%s path: got %q, want %q", name, diagnostic.Path, path)
					}
					if diagnostic.Predicate != "append-only-ledger" {
						t.Errorf("%s predicate: got %q, want append-only-ledger", name, diagnostic.Predicate)
					}
					// omp names its literal current-home executable; Kimi intentionally names no
					// omp home path (ledgerAppendRemediation in classifier.go).
					if provider == ProviderOmp {
						if err := eciActiveRemediationError(diagnostic.Remediation); err != nil {
							t.Errorf("%s remediation: %v", name, err)
						}
					} else if diagnostic.Remediation == "" || eciActivePathPattern.MatchString(diagnostic.Remediation) {
						t.Errorf("%s remediation: got %q, want non-empty guidance naming no $HOME executable path", name, diagnostic.Remediation)
					}
				})
			}

			for _, testCase := range []struct {
				name    string
				command string
			}{
				{name: "ledger read", command: "sed -n '1p' " + filepath.Join(sessionDir, "high_level_log.jsonl")},
				{name: "ordinary write", command: "touch " + ordinaryPath},
			} {
				testCase := testCase
				t.Run(testCase.name, func(t *testing.T) {
					request := Request{
						Provider:      provider,
						Role:          RoleCoordinator,
						CWD:           unrelatedCWD,
						Marker:        MarkerActive,
						ActiveSession: "session",
						Command:       testCase.command,
						ActiveMarkers: []string{marker},
					}
					input, err := json.Marshal(request)
					if err != nil {
						t.Fatalf("marshal %s request: %v", testCase.name, err)
					}
					var stdout bytes.Buffer
					status := runBinary(t, binary, input, &stdout)
					if status != StatusAllow {
						t.Fatalf("%s status: got %d, want %d; output=%s", testCase.name, status, StatusAllow, stdout.String())
					}
				})
			}
		})
	}
}

// TestEciCoordinatorStopGateSyntaxRoute verifies that syntax-only reads use
// ordinary routing without executing the inspected control script.
//
// Example: a compound is admitted only when the syntax check and every other
// parser-attested direct segment are admitted by their ordinary routes.
func TestEciCoordinatorStopGateSyntaxRoute(t *testing.T) {
	root := providerHome(ProviderOmp)

	fixtureRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("canonicalize fixture root: %v", err)
	}
	home := filepath.Join(fixtureRoot, "home")
	canonicalRoot := filepath.Join(home, ".omp", "agent", "eci")
	proofRoot := filepath.Join(fixtureRoot, "proof")
	poisonedEciHome := filepath.Join(fixtureRoot, "poisoned-eci-home")
	xdgConfigHome := filepath.Join(fixtureRoot, "xdg-config")
	xdgStateHome := filepath.Join(fixtureRoot, "xdg-state")
	temporaryRoot := filepath.Join(fixtureRoot, "tmp")
	const sessionID = "stop-gate-syntax"
	sessionsProject := filepath.Join(home, ".omp", "agent", "sessions", "project")
	mainTranscript := filepath.Join(sessionsProject, "main.jsonl")

	for _, directory := range []string{
		canonicalRoot,
		filepath.Join(proofRoot, sessionID),
		filepath.Join(poisonedEciHome, "hooks"),
		filepath.Join(xdgConfigHome, "eci"),
		xdgStateHome,
		temporaryRoot,
		sessionsProject,
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("create %s: %v", directory, err)
		}
	}
	// A depth-two transcript with a session header and no parentSession is the main
	// (coordinator) transcript; worker cases still set ECI_HOOK_IS_SUBAGENT=true.
	if err := os.WriteFile(mainTranscript, []byte(`{"type":"session","version":3,"id":"`+sessionID+`"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write main transcript: %v", err)
	}

	copyStopGateSyntaxFixtureTree(t, filepath.Join(root, "hooks"), filepath.Join(canonicalRoot, "hooks"))
	copyStopGateSyntaxFixtureTree(t, filepath.Join(root, "bin"), filepath.Join(canonicalRoot, "bin"))

	marker := filepath.Join(proofRoot, sessionID, "eci_active")
	markerContents := "scope: stop-gate syntax route\n" +
		"cwd: " + canonicalRoot + "\n" +
		"session_id: " + sessionID + "\n" +
		"created_utc: 2026-08-27T00:00:00Z\n"
	if err := os.WriteFile(marker, []byte(markerContents), 0o600); err != nil {
		t.Fatalf("write active marker: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(xdgConfigHome, "eci", "command-gate-mode"),
		[]byte("enforcing\n"),
		0o600,
	); err != nil {
		t.Fatalf("write enforcing command-gate mode: %v", err)
	}

	sentinel := filepath.Join(fixtureRoot, "stop-gate-executed")
	stopGate := filepath.Join(canonicalRoot, "hooks", "stop-gate.sh")
	if err := os.WriteFile(stopGate, []byte("#!/usr/bin/env bash\ntouch \"$ECI_TEST_STOP_GATE_SENTINEL\"\n"), 0o700); err != nil {
		t.Fatalf("write changed Stop hook: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(poisonedEciHome, "hooks", "stop-gate.sh"),
		[]byte("#!/usr/bin/env bash\nexit 0\n"),
		0o700,
	); err != nil {
		t.Fatalf("write poisoned Stop hook: %v", err)
	}

	baseEnvironment := func(worker bool, bashEnvironment string, compoundSegment bool) []string {
		role := "coordinator"
		subagent := "false"
		if worker {
			role = "worker"
			subagent = "true"
		}
		environment := []string{
			"HOME=" + home,
			"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			"ECI_HOME=" + poisonedEciHome,
			"ECI_PROOF_ROOT=" + proofRoot,
			"ECI_HOOK_IS_SUBAGENT=" + subagent,
			"ECI_ROLE=" + role,
			"XDG_CONFIG_HOME=" + xdgConfigHome,
			"XDG_STATE_HOME=" + xdgStateHome,
			"TMPDIR=" + temporaryRoot,
			"ECI_TMPDIR=" + temporaryRoot,
		}
		if bashEnvironment != "" {
			environment = append(environment, "BASH_ENV="+bashEnvironment)
		}
		if compoundSegment {
			environment = append(environment, "ECI_COMPOUND_SEGMENT_VALIDATION=true")
		}
		return environment
	}

	runHook := func(cwd, command string, worker bool, bashEnvironment string, compoundSegment bool) string {
		request, err := json.Marshal(map[string]any{
			"session_id":      sessionID,
			"tool_name":       "Bash",
			"transcript_path": mainTranscript,
			"cwd":             cwd,
			"tool_input":      map[string]string{"command": command},
		})
		if err != nil {
			t.Fatalf("marshal hook request: %v", err)
		}

		hook := exec.Command("/usr/bin/bash", filepath.Join(canonicalRoot, "hooks", "validate-bash.sh"))
		hook.Dir = cwd
		hook.Env = baseEnvironment(worker, bashEnvironment, compoundSegment)
		hook.Stdin = bytes.NewReader(request)
		output, err := hook.CombinedOutput()
		if err != nil {
			t.Fatalf("run hook for %q: %v; output=%s", command, err, output)
		}
		return string(output)
	}

	assertAllowed := func(command string) {
		t.Helper()
		if output := runHook(canonicalRoot, command, false, "", false); strings.TrimSpace(output) != "" {
			t.Fatalf("%q: hook output=%s, want admission", command, output)
		}
	}
	assertDenied := func(name, cwd, command string, worker bool, bashEnvironment string, compoundSegment bool, reasonContains string) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			output := runHook(cwd, command, worker, bashEnvironment, compoundSegment)
			var result struct {
				HookSpecificOutput struct {
					PermissionDecision       string `json:"permissionDecision"`
					PermissionDecisionReason string `json:"permissionDecisionReason"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal([]byte(output), &result); err != nil {
				t.Fatalf("%q: decode denial output: %v; output=%s", command, err, output)
			}
			if result.HookSpecificOutput.PermissionDecision != "deny" {
				t.Fatalf("%q: permission=%q, want deny; output=%s", command, result.HookSpecificOutput.PermissionDecision, output)
			}
			if reasonContains != "" && !strings.Contains(result.HookSpecificOutput.PermissionDecisionReason, reasonContains) {
				t.Fatalf("%q: denial reason=%q, want %q", command, result.HookSpecificOutput.PermissionDecisionReason, reasonContains)
			}
		})
	}

	recursiveFlagBashEnvironment := filepath.Join(fixtureRoot, "bash-recursive-flag-env")
	if err := os.WriteFile(recursiveFlagBashEnvironment, []byte("export ECI_COMPOUND_SEGMENT_VALIDATION=true\n"), 0o600); err != nil {
		t.Fatalf("write Bash recursive-flag environment: %v", err)
	}
	if output := runHook(canonicalRoot, "bash -n hooks/stop-gate.sh ", false, recursiveFlagBashEnvironment, false); strings.TrimSpace(output) != "" {
		t.Fatalf("recursive flag alone changed a syntax-only effect: %s", output)
	}

	assertAllowed("bash -n hooks/stop-gate.sh")
	for _, command := range []string{
		"bash -n hooks/stop-gate.sh; true",
		"bash -n hooks/stop-gate.sh && true",
		"bash -n hooks/stop-gate.sh || true",
		"bash -n hooks/stop-gate.sh | true",
		"bash -n hooks/stop-gate.sh && printf '|'",
		"bash -n hooks/stop-gate.sh && touch hooks/compound-mutation",
	} {
		assertAllowed(command)
	}

	syntaxCheck := exec.Command("/usr/bin/bash", "-n", "hooks/stop-gate.sh")
	syntaxCheck.Dir = canonicalRoot
	syntaxCheck.Env = append(baseEnvironment(false, "", false), "ECI_TEST_STOP_GATE_SENTINEL="+sentinel)
	if output, err := syntaxCheck.CombinedOutput(); err != nil {
		t.Fatalf("run canonical Bash syntax check: %v; output=%s", err, output)
	}
	if _, err := os.Stat(sentinel); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Bash -n executed Stop-hook sentinel: stat error=%v", err)
	}

	functionBashEnvironment := filepath.Join(fixtureRoot, "bash-function-env")
	if err := os.WriteFile(functionBashEnvironment, []byte("bash() { /usr/bin/bash \"$@\"; }\n"), 0o600); err != nil {
		t.Fatalf("write Bash function environment: %v", err)
	}
	aliasBashEnvironment := filepath.Join(fixtureRoot, "bash-alias-env")
	if err := os.WriteFile(aliasBashEnvironment, []byte("shopt -s expand_aliases\nalias bash='/usr/bin/bash'\n"), 0o600); err != nil {
		t.Fatalf("write Bash alias environment: %v", err)
	}
	foreignCWD := filepath.Join(fixtureRoot, "foreign")
	if err := os.MkdirAll(foreignCWD, 0o700); err != nil {
		t.Fatalf("create foreign cwd: %v", err)
	}

	for _, testCase := range []struct {
		name            string
		cwd             string
		command         string
		worker          bool
		bashEnvironment string
		compoundSegment bool
		reasonContains  string
	}{
		{name: "worker ownership", cwd: canonicalRoot, command: "bash -n hooks/stop-gate.sh", worker: true},
		{name: "direct execution", cwd: canonicalRoot, command: "hooks/stop-gate.sh"},
		{name: "execution tracing", cwd: canonicalRoot, command: "bash -x hooks/stop-gate.sh"},
		{name: "combined tracing and syntax", cwd: canonicalRoot, command: "bash -x -n hooks/stop-gate.sh"},
		{name: "alternative shell", cwd: canonicalRoot, command: "sh -n hooks/stop-gate.sh"},
		{name: "absolute shell", cwd: canonicalRoot, command: "/bin/bash -n hooks/stop-gate.sh"},
		{name: "command wrapper", cwd: canonicalRoot, command: "command bash -n hooks/stop-gate.sh"},
		{name: "environment wrapper", cwd: canonicalRoot, command: "env bash -n hooks/stop-gate.sh"},
		{name: "extra argument", cwd: canonicalRoot, command: "bash -n hooks/stop-gate.sh extra"},
		{name: "dot path", cwd: canonicalRoot, command: "bash -n ./hooks/stop-gate.sh"},
		{name: "absolute target", cwd: canonicalRoot, command: "bash -n " + stopGate},
		{name: "parent path", cwd: canonicalRoot, command: "bash -n hooks/../hooks/stop-gate.sh"},
		{name: "leading whitespace", cwd: canonicalRoot, command: " bash -n hooks/stop-gate.sh"},
		{name: "trailing whitespace", cwd: canonicalRoot, command: "bash -n hooks/stop-gate.sh "},
		{name: "foreign cwd", cwd: foreignCWD, command: "bash -n " + stopGate},
		{name: "poisoned ECI_HOME cwd", cwd: poisonedEciHome, command: "bash -n " + stopGate},
		{name: "compound direct execution", cwd: canonicalRoot, command: "bash -n hooks/stop-gate.sh; bash hooks/stop-gate.sh", reasonContains: "reviewed digest manifest"},
		{name: "Bash function", cwd: canonicalRoot, command: "bash -n hooks/stop-gate.sh", bashEnvironment: functionBashEnvironment},
		{name: "Bash alias", cwd: canonicalRoot, command: "bash -n hooks/stop-gate.sh", bashEnvironment: aliasBashEnvironment},
		{name: "ordinary reviewed digest", cwd: canonicalRoot, command: "bash -n hooks/tests/test-validate-bash-classifier.sh", reasonContains: "reviewed digest manifest"},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			output := runHook(testCase.cwd, testCase.command, testCase.worker,
				testCase.bashEnvironment, testCase.compoundSegment)
			if strings.TrimSpace(output) != "" {
				t.Fatalf("ordinary %q: hook output=%s, want admission", testCase.command, output)
			}
		})
	}
	// Worker execution of the Supervisor-owned control script is denied as control
	// ownership; the worker's syntax-only check of it is admitted above ("worker ownership").
	assertDenied("worker control execution", canonicalRoot, "bash hooks/stop-gate.sh", true, "", false,
		"["+string(CodeControlOwnerRequired)+"]")
	for _, worker := range []bool{false, true} {
		assertDenied(fmt.Sprintf("syntax with control redirect worker=%t", worker), canonicalRoot,
			"bash -n hooks/stop-gate.sh > "+marker+" | true", worker, "", false, "eci_active")
		// The broad segment is denied for both roles. For a worker, "/" is an ancestor of
		// the session's marker, so the control-ownership check reports the denial first.
		broadReason := "[" + string(CodeControlOwnerRequired) + "]"
		if !worker {
			broadReason = "ECI_BROAD_DESTRUCTIVE_DENIED"
		}
		assertDenied(fmt.Sprintf("syntax with broad segment worker=%t", worker), canonicalRoot,
			"bash -n hooks/stop-gate.sh | rm -rf /", worker, "", false, broadReason)
	}
}

// copyStopGateSyntaxFixtureTree copies the hook fixture without sharing a
// mutable inode with the working tree.
//
// Example: a changed copied Stop hook cannot alter the repository source.
func copyStopGateSyntaxFixtureTree(t *testing.T, sourceRoot, destinationRoot string) {
	t.Helper()

	if err := filepath.WalkDir(sourceRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(destinationRoot, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(target, destination)
		}
		if entry.IsDir() {
			if err := os.MkdirAll(destination, info.Mode().Perm()); err != nil {
				return err
			}
			return os.Chmod(destination, info.Mode().Perm())
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return err
		}
		source, err := os.Open(path)
		if err != nil {
			return err
		}
		defer source.Close()
		destinationFile, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(destinationFile, source)
		closeErr := destinationFile.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		return os.Chmod(destination, info.Mode().Perm())
	}); err != nil {
		t.Fatalf("copy %s to %s: %v", sourceRoot, destinationRoot, err)
	}
}

func runBinary(t *testing.T, path string, input []byte, stdout *bytes.Buffer) int {
	t.Helper()

	command := exec.Command(path)
	command.Stdin = bytes.NewReader(input)
	command.Stdout = stdout
	var stderr bytes.Buffer
	command.Stderr = &stderr
	err := command.Run()
	if err == nil {
		return StatusAllow
	}
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		t.Fatalf("run %s: %v; stderr=%s", path, err, stderr.String())
	}
	return exitError.ExitCode()
}
