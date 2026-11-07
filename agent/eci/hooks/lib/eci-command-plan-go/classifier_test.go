package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWorkerGitDefersBeforeEffects leaves active Worker native routing to the provider.
//
// Example: a native branch operation and a mutation preview share the provider route.
func TestWorkerGitDefersBeforeEffects(t *testing.T) {
	for _, command := range []string{"git branch feature", "git fsck --lost-found", "git add --dry-run file"} {
		result := Classify(activeWorker(command))
		if result.Decision != DecisionDefer || result.Diagnostic != nil {
			t.Fatalf("%s: %+v, want provider defer", command, result)
		}
	}
}

func providerHome(provider Provider) string {
	home := os.Getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if provider == ProviderKimi {
		if configured := os.Getenv("KIMI_CODE_HOME"); configured != "" {
			return filepath.Clean(configured)
		}
		return filepath.Join(home, ".kimi-code")
	}
	return filepath.Join(home, ".omp", "agent", "eci")
}

func gateModePath(provider Provider) string {
	return filepath.Join(providerHome(provider), "bin", "eci-command-gate-mode")
}

// TestClassifyLedgerAppendRemediationUsesEciAuthority verifies that omp
// diagnostics name the native lifecycle executable literally.
//
// Example: a direct ledger write under an active marker names the literal
// $HOME/.omp/agent/eci/bin/eci-active lifecycle executable.
func TestClassifyLedgerAppendRemediationUsesEciAuthority(t *testing.T) {
	t.Parallel()

	realProofRoot := t.TempDir()
	proofRoot := filepath.Join(t.TempDir(), "proof-alias")
	if err := os.Symlink(realProofRoot, proofRoot); err != nil {
		t.Fatalf("create proof-root alias: %v", err)
	}
	sessionDir := filepath.Join(proofRoot, "session")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("create proof session: %v", err)
	}
	marker := filepath.Join(sessionDir, "eci_active")
	if err := os.WriteFile(marker, []byte("active\n"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	ledger := filepath.Join(sessionDir, "high_level_log.jsonl")
	if err := os.WriteFile(ledger, []byte("{\"event\":\"decision\",\"summary\":\"entry\"}\n"), 0o600); err != nil {
		t.Fatalf("write ledger: %v", err)
	}

	for _, testCase := range []struct {
		provider        Provider
		wantRemediation string
	}{
		// Kimi's remediation is the 575bb81 classifier.go:835-838 text; omp keeps its own
		// literal-path remediation, checked by eciActiveRemediationError.
		{provider: ProviderOmp},
		{provider: ProviderKimi, wantRemediation: "use eci-active ledger-append --json"},
	} {
		testCase := testCase
		t.Run(string(testCase.provider), func(t *testing.T) {
			result := Classify(Request{
				Provider:      testCase.provider,
				Role:          RoleCoordinator,
				CWD:           proofRoot,
				Marker:        MarkerActive,
				ActiveSession: "session",
				Command:       "sed -i '1p' " + ledger,
				ActiveMarkers: []string{marker},
			})
			if result.Decision != DecisionDeny || result.Diagnostic == nil {
				t.Fatalf("result: decision=%q diagnostic=%#v, want ledger denial", result.Decision, result.Diagnostic)
			}
			if result.Diagnostic.Code != CodeLedgerAppendOnly {
				t.Fatalf("diagnostic code: got %q, want %q", result.Diagnostic.Code, CodeLedgerAppendOnly)
			}
			if result.Diagnostic.Operation != "ledger-append-only" {
				t.Errorf("operation: got %q, want ledger-append-only", result.Diagnostic.Operation)
			}
			if testCase.wantRemediation != "" {
				if result.Diagnostic.Remediation != testCase.wantRemediation {
					t.Errorf("remediation: got %q, want %q", result.Diagnostic.Remediation, testCase.wantRemediation)
				}
				return
			}
			if err := eciActiveRemediationError(result.Diagnostic.Remediation); err != nil {
				t.Errorf("remediation: %v", err)
			}
		})
	}
}

// eciActivePathPattern captures the $HOME-relative path of the eci-active executable from a quoted remediation path.
//
// Example: "$HOME/.omp/agent/eci/bin/eci-active" captures /.omp/agent/eci/bin/eci-active.
var eciActivePathPattern = regexp.MustCompile(`"\$HOME(/[^"]*/eci-active)"`)

// eciActiveRemediationError checks that a remediation names the eci-active executable under $HOME
// and that the named path exists as an executable file.
//
// Example: a remediation naming "$HOME/.omp/agent/eci/bin/eci-active" passes once that file is installed.
func eciActiveRemediationError(remediation string) error {
	match := eciActivePathPattern.FindStringSubmatch(remediation)
	if match == nil {
		return fmt.Errorf("remediation names no quoted $HOME eci-active path: %q", remediation)
	}
	path := filepath.Join(os.Getenv("HOME"), match[1])
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("named executable %s: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("named executable %s is not an executable file: mode %v", path, info.Mode())
	}
	return nil
}

func TestClassifyFiniteCommandPlans(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		request   Request
		decision  DecisionKind
		code      DiagnosticCode
		segment   int
		predicate string
	}{
		{
			name:     "ordinary novel tool",
			request:  activeWorker("novel-tool --flag value"),
			decision: DecisionAllow,
		},
		{
			name:     "adb probe",
			request:  activeWorker("adb devices -l"),
			decision: DecisionAllow,
		},
		{
			name:     "quoted operator",
			request:  activeWorker("printf 'left && right'"),
			decision: DecisionAllow,
		},
		{
			name:     "literal environment wrapper",
			request:  activeWorker("env FOO=bar novel-tool --flag value"),
			decision: DecisionAllow,
		},
		{
			name:     "literal environment wrapper with module tool",
			request:  activeWorker("env FOO=bar python3 -m pytest tests"),
			decision: DecisionAllow,
		},
		{
			name:     "quoted environment repository context defers to Git",
			request:  activeWorker(`env "GIT_DIR=.git" git status`),
			decision: DecisionDefer,
		},
		{
			name:     "environment repository context defers to Git",
			request:  activeWorker("env GIT_DIR=.git git archive HEAD"),
			decision: DecisionDefer,
		},
		{
			name:     "stat access time inspection",
			request:  activeWorker("stat -c '%x' hooks/validate-bash.sh"),
			decision: DecisionAllow,
		},
		{
			name:     "stat name inspection",
			request:  activeWorker("stat -c '%n' hooks/validate-bash.sh"),
			decision: DecisionAllow,
		},
		{
			name:     "stat finite metadata combination",
			request:  activeWorker("stat -c '%x %s %n' hooks/validate-bash.sh"),
			decision: DecisionAllow,
		},
		{
			name:     "stat unknown format directive remains ordinary",
			request:  activeWorker("stat -c '%Q' hooks/validate-bash.sh"),
			decision: DecisionAllow,
		},
		{
			name:     "stat dynamic format payload remains ordinary",
			request:  activeWorker("stat -c '$(printf %s)' hooks/validate-bash.sh"),
			decision: DecisionAllow,
		},
		{
			name:     "stat context-sensitive option remains ordinary",
			request:  activeWorker("stat --printf='%s' hooks/validate-bash.sh"),
			decision: DecisionAllow,
		},
		{
			name:     "environment wrapper with interpreter eval",
			request:  activeWorker("env FOO=bar python3 -c 'print(1)'"),
			decision: DecisionAllow,
		},
		{
			name:     "environment wrapper with malformed option assignment",
			request:  activeWorker("env --unset=9FOO novel-tool"),
			decision: DecisionAllow,
		},
		{
			name:     "leading assignment",
			request:  activeWorker("FOO=bar novel-tool"),
			decision: DecisionAllow,
		},
		{
			name:     "redirection",
			request:  activeWorker("novel-tool > output.txt"),
			decision: DecisionAllow,
		},
		{
			name:     "ordinary middle pipeline segment",
			request:  activeWorker("printf before | env | printf after"),
			decision: DecisionAllow,
		},
		{
			name:     "wrapped git mutation",
			request:  activeWorker("timeout 5 git rebase topic"),
			decision: DecisionDefer,
		},
		{
			name:     "transparent wrapped git mutations",
			request:  activeWorker("nohup git rebase topic"),
			decision: DecisionDefer,
		},
		{
			name:     "git archive inspection",
			request:  activeWorker("git archive HEAD"),
			decision: DecisionDefer,
		},
		{
			name:     "git branch contains inspection",
			request:  activeWorker("git branch --all --contains HEAD"),
			decision: DecisionDefer,
		},
		{
			name:     "git branch list pattern inspection",
			request:  activeWorker("git branch --list 'release/*'"),
			decision: DecisionDefer,
		},
		{
			name:     "git branch merged inspection",
			request:  activeWorker("git branch --merged HEAD"),
			decision: DecisionDefer,
		},
		{
			name:     "git branch creation",
			request:  activeWorker("git branch feature"),
			decision: DecisionDefer,
		},
		{
			name:     "bounded unregistered environment name",
			request:  activeWorker("printenv ECI_UNREGISTERED_TEST_VALUE"),
			decision: DecisionAllow,
		},
		{
			name:     "printenv option",
			request:  activeWorker("printenv -- PATH"),
			decision: DecisionAllow,
		},
		{
			name:     "worker bare lifecycle state discovery",
			request:  activeWorker("eci-active status"),
			decision: DecisionDefer,
		},
		{
			name:     "wrapped worker bare lifecycle state discovery",
			request:  activeWorker("env -- eci-active status"),
			decision: DecisionDefer,
		},
		{
			name:     "relative lifecycle state discovery defers",
			request:  activeWorker("./eci-active status"),
			decision: DecisionDefer,
		},
		{
			name:      "broad destruction",
			request:   activeWorker("rm -rf /"),
			decision:  DecisionDeny,
			code:      CodeBroadDestructiveDenied,
			segment:   1,
			predicate: "broad-destructive-root",
		},
		{
			name:      "wrapped broad destruction",
			request:   activeWorker("env FOO=bar rm -rf /"),
			decision:  DecisionDeny,
			code:      CodeBroadDestructiveDenied,
			segment:   1,
			predicate: "broad-destructive-root",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result := Classify(testCase.request)
			if result.Decision != testCase.decision {
				t.Fatalf("decision: got %q, want %q", result.Decision, testCase.decision)
			}
			if testCase.code == "" {
				if result.Diagnostic != nil {
					t.Fatalf("unexpected diagnostic: %#v", result.Diagnostic)
				}
				return
			}
			if result.Diagnostic == nil {
				t.Fatal("missing diagnostic")
			}
			if result.Diagnostic.Code != testCase.code {
				t.Errorf("code: got %q, want %q", result.Diagnostic.Code, testCase.code)
			}
			if result.Diagnostic.Segment != testCase.segment {
				t.Errorf("segment: got %d, want %d", result.Diagnostic.Segment, testCase.segment)
			}
			if result.Diagnostic.Predicate != testCase.predicate {
				t.Errorf("predicate: got %q, want %q", result.Diagnostic.Predicate, testCase.predicate)
			}
			if result.Diagnostic.Reason == "" || result.Diagnostic.Remediation == "" {
				t.Fatalf("incomplete diagnostic: %#v", result.Diagnostic)
			}
		})
	}
}

// TestActiveFindActionsUseEffectDiagnostics verifies that active coordinator
// find actions are denied in direct and compound plans, while worker behavior
// and ordinary inspection remain admitted and broad-root deletion keeps its
// precedence.
//
// Example: an active coordinator may inspect with `find /tmp -print`, but
// `find /tmp -exec file {} \\; && printf done` receives the dynamic-action
// diagnostic before the following segment can run.
func TestActiveFindActionsUseEffectDiagnostics(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name      string
		command   string
		role      Role
		decision  DecisionKind
		code      DiagnosticCode
		predicate string
	}{
		{
			name:      "coordinator delete",
			command:   "find /tmp -maxdepth 1 -type f -delete",
			role:      RoleCoordinator,
			decision:  DecisionDeny,
			code:      CodePlanDynamicLaunchDenied,
			predicate: "dynamic-find-action",
		},
		{
			name:      "coordinator exec",
			command:   "find /tmp -maxdepth 1 -type f -exec rm -f {} \\;",
			role:      RoleCoordinator,
			decision:  DecisionDeny,
			code:      CodePlanDynamicLaunchDenied,
			predicate: "dynamic-find-action",
		},
		{
			name:      "coordinator exec printf",
			command:   "find /tmp -maxdepth 1 -type f -exec printf '%s' {} \\;",
			role:      RoleCoordinator,
			decision:  DecisionDeny,
			code:      CodePlanDynamicLaunchDenied,
			predicate: "dynamic-find-action",
		},
		{
			name:      "coordinator exec path-qualified reader aliases",
			command:   "find /tmp -maxdepth 1 -type f -exec ./file --brief {} \\;",
			role:      RoleCoordinator,
			decision:  DecisionDeny,
			code:      CodePlanDynamicLaunchDenied,
			predicate: "dynamic-find-action",
		},
		{
			name:      "coordinator exec absolute reader alias",
			command:   "find /tmp -maxdepth 1 -type f -exec /tmp/file --brief {} \\;",
			role:      RoleCoordinator,
			decision:  DecisionDeny,
			code:      CodePlanDynamicLaunchDenied,
			predicate: "dynamic-find-action",
		},
		{
			name:      "coordinator exec parent reader alias",
			command:   "find /tmp -maxdepth 1 -type f -exec ../file --brief {} \\;",
			role:      RoleCoordinator,
			decision:  DecisionDeny,
			code:      CodePlanDynamicLaunchDenied,
			predicate: "dynamic-find-action",
		},
		{
			name:      "coordinator exec file short compile option",
			command:   "find /tmp -maxdepth 1 -type f -exec file -C -m /tmp/magic {} \\;",
			role:      RoleCoordinator,
			decision:  DecisionDeny,
			code:      CodePlanDynamicLaunchDenied,
			predicate: "dynamic-find-action",
		},
		{
			name:      "coordinator exec file long compile option",
			command:   "find /tmp -maxdepth 1 -type f -exec file --compile -m /tmp/magic {} \\;",
			role:      RoleCoordinator,
			decision:  DecisionDeny,
			code:      CodePlanDynamicLaunchDenied,
			predicate: "dynamic-find-action",
		},
		{
			name:      "coordinator compound exec",
			command:   "find /tmp -maxdepth 1 -type f -exec printf '%s' {} \\; && printf done",
			role:      RoleCoordinator,
			decision:  DecisionDeny,
			code:      CodePlanDynamicLaunchDenied,
			predicate: "dynamic-find-action",
		},
		{
			name:      "coordinator compound delete",
			command:   "find /tmp -maxdepth 1 -type f -delete && printf done",
			role:      RoleCoordinator,
			decision:  DecisionDeny,
			code:      CodePlanDynamicLaunchDenied,
			predicate: "dynamic-find-action",
		},
		{
			name:     "coordinator safe compound inspection",
			command:  "find /tmp -maxdepth 1 -type f -print && printf done",
			role:     RoleCoordinator,
			decision: DecisionAllow,
		},
		{
			name:      "coordinator execdir",
			command:   "find /tmp -maxdepth 1 -type f -execdir rm -f {} \\;",
			role:      RoleCoordinator,
			decision:  DecisionDeny,
			code:      CodePlanDynamicLaunchDenied,
			predicate: "dynamic-find-action",
		},
		{
			name:      "coordinator ok",
			command:   "find /tmp -maxdepth 1 -type f -ok rm -f {} \\;",
			role:      RoleCoordinator,
			decision:  DecisionDeny,
			code:      CodePlanDynamicLaunchDenied,
			predicate: "dynamic-find-action",
		},
		{
			name:      "coordinator okdir",
			command:   "find /tmp -maxdepth 1 -type f -okdir rm -f {} \\;",
			role:      RoleCoordinator,
			decision:  DecisionDeny,
			code:      CodePlanDynamicLaunchDenied,
			predicate: "dynamic-find-action",
		},
		{
			name:     "worker exec behavior preserved",
			command:  "find /tmp -maxdepth 1 -type f -exec rm -f {} \\;",
			role:     RoleWorker,
			decision: DecisionAllow,
		},
		{
			name:     "worker exec printf behavior preserved",
			command:  "find /tmp -maxdepth 1 -type f -exec printf '%s' {} \\;",
			role:     RoleWorker,
			decision: DecisionAllow,
		},
		{
			name:     "coordinator print inspection",
			command:  "find /tmp -maxdepth 1 -type f -print",
			role:     RoleCoordinator,
			decision: DecisionAllow,
		},
		{
			name:      "coordinator metadata exec inspection",
			command:   "find /tmp -maxdepth 1 -type f -exec file --brief {} \\;",
			role:      RoleCoordinator,
			decision:  DecisionDeny,
			code:      CodePlanDynamicLaunchDenied,
			predicate: "dynamic-find-action",
		},
		{
			name:     "coordinator find read with temporary output",
			command:  "find /tmp -maxdepth 1 -type f -print > /tmp/find-output.txt",
			role:     RoleCoordinator,
			decision: DecisionAllow,
		},
		{
			name:     "worker print inspection",
			command:  "find /tmp -maxdepth 1 -type f -print",
			role:     RoleWorker,
			decision: DecisionAllow,
		},
		{
			name:      "coordinator broad root precedence",
			command:   "find / -delete",
			role:      RoleCoordinator,
			decision:  DecisionDeny,
			code:      CodeBroadDestructiveDenied,
			predicate: "broad-destructive-root",
		},
		{
			name:      "coordinator compound broad root precedence",
			command:   "find / -delete && printf done",
			role:      RoleCoordinator,
			decision:  DecisionDeny,
			code:      CodeBroadDestructiveDenied,
			predicate: "broad-destructive-root",
		},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			request := activeWorker(testCase.command)
			request.Role = testCase.role
			result := Classify(request)
			if result.Decision != testCase.decision {
				t.Fatalf("decision=%q diagnostic=%#v, want %q", result.Decision, result.Diagnostic, testCase.decision)
			}
			if testCase.code == "" {
				if result.Diagnostic != nil {
					t.Fatalf("unexpected diagnostic=%#v", result.Diagnostic)
				}
				return
			}
			if result.Diagnostic == nil {
				t.Fatal("missing diagnostic")
			}
			if result.Diagnostic.Code != testCase.code {
				t.Errorf("code=%q, want %q", result.Diagnostic.Code, testCase.code)
			}
			if result.Diagnostic.Predicate != testCase.predicate {
				t.Errorf("predicate=%q, want %q", result.Diagnostic.Predicate, testCase.predicate)
			}
			if result.Diagnostic.Reason == "" || result.Diagnostic.Remediation == "" {
				t.Fatalf("incomplete diagnostic=%#v", result.Diagnostic)
			}
		})
	}
}

// TestGitBranchInspectionOptionGrammar keeps the provider-side Git target
// resolver aligned with the planner's branch mutation recognizer.  Required
// value options consume only a following non-option; optional display values
// are attached with --option=value, so a following branch name remains a
// concrete mutation operand.  --list patterns remain inspection operands.
func TestGitBranchInspectionOptionGrammar(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		command  string
		decision DecisionKind
		code     DiagnosticCode
	}{
		{
			name:     "attached optional display values with list pattern",
			command:  "git branch --abbrev=12 --column=always --color=always --list 'release/*'",
			decision: DecisionDefer,
		},
		{
			name:     "short list pattern",
			command:  "git branch -l 'release/*'",
			decision: DecisionDefer,
		},
		{
			name:     "required inspection values",
			command:  "git branch --contains HEAD --format '%(refname)' --sort committerdate",
			decision: DecisionDefer,
		},
		{
			name:     "abbrev option leaves branch operand visible",
			command:  "git branch --abbrev feature",
			decision: DecisionDefer,
		},
		{
			name:     "column option leaves branch operand visible",
			command:  "git branch --column feature",
			decision: DecisionDefer,
		},
		{
			name:     "color option leaves branch operand visible",
			command:  "git branch --color feature",
			decision: DecisionDefer,
		},
		{
			name:     "required option does not hide mutation option",
			command:  "git branch --format --delete",
			decision: DecisionDefer,
		},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result := Classify(activeWorker(testCase.command))
			if result.Decision != testCase.decision {
				t.Fatalf("decision=%q diagnostic=%#v, want %q", result.Decision, result.Diagnostic, testCase.decision)
			}
			if testCase.code == "" {
				if result.Diagnostic != nil {
					t.Fatalf("unexpected diagnostic=%#v", result.Diagnostic)
				}
				return
			}
			if result.Diagnostic == nil || result.Diagnostic.Code != testCase.code {
				t.Fatalf("diagnostic=%#v, want code %q", result.Diagnostic, testCase.code)
			}
		})
	}
}

// TestTimeoutPrefixRequiresValidDurationsBeforeClassifyingGitChild verifies
// that timeout exposes a Git child only after its duration-bearing launch
// grammar is concrete and valid.
//
// Example: timeout -p 5 git rebase reaches the provider Git route, while
// timeout not-a-duration git rebase remains an ordinary timeout failure.
func TestTimeoutPrefixRequiresValidDurationsBeforeClassifyingGitChild(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name     string
		command  string
		decision DecisionKind
		code     DiagnosticCode
	}{
		{
			name:     "plain duration",
			command:  "timeout 5 git rebase topic",
			decision: DecisionDefer,
		},
		{
			name:     "positive signed duration",
			command:  "timeout +5 git rebase topic",
			decision: DecisionDefer,
		},
		{
			name:     "preserve status short",
			command:  "timeout -p 5 git rebase topic",
			decision: DecisionDefer,
		},
		{
			name:     "foreground short",
			command:  "timeout -f 5 git rebase topic",
			decision: DecisionDefer,
		},
		{
			name:     "preserve status long",
			command:  "timeout --preserve-status 5 git rebase topic",
			decision: DecisionDefer,
		},
		{
			name:     "foreground long",
			command:  "timeout --foreground 5 git rebase topic",
			decision: DecisionDefer,
		},
		{
			name:     "kill after duration",
			command:  "timeout --kill-after=1s 5 git status",
			decision: DecisionDefer,
		},
		{
			name:     "invalid primary duration",
			command:  "timeout not-a-duration git rebase topic",
			decision: DecisionAllow,
		},
		{
			name:     "invalid kill after duration",
			command:  "timeout --kill-after=not-a-duration 5 git rebase topic",
			decision: DecisionAllow,
		},
		{
			name:     "unknown option",
			command:  "timeout --unrecognized-timeout-option 5 git rebase topic",
			decision: DecisionAllow,
		},
		{
			name:     "incomplete option",
			command:  "timeout --kill-after",
			decision: DecisionAllow,
		},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result := Classify(activeWorker(testCase.command))
			if result.Decision != testCase.decision {
				t.Fatalf("decision=%q diagnostic=%#v, want %q", result.Decision, result.Diagnostic, testCase.decision)
			}
			if testCase.code == "" {
				if result.Diagnostic != nil {
					t.Fatalf("unexpected diagnostic=%#v", result.Diagnostic)
				}
				return
			}
			if result.Diagnostic == nil || result.Diagnostic.Code != testCase.code {
				t.Fatalf("diagnostic=%#v, want code %q", result.Diagnostic, testCase.code)
			}
		})
	}
}

// TestTimeoutLaunchRequiresObservedChild verifies that a timeout-wrapped Git
// child is visible only when the callback-selected timeout executable actually
// launches the planner's harmless replacement child.
//
// Example: a fake timeout that executes its replacement child exposes a worker
// Git commit, while fake timeout outcomes that accept or reject a signal
// without launching the child remain ordinary commands.
func TestTimeoutLaunchRequiresObservedChild(t *testing.T) {
	t.Parallel()

	timeoutDirectory := t.TempDir()
	timeoutPath := filepath.Join(timeoutDirectory, "timeout")
	timeoutScript := `#!/bin/sh
set -eu

signal=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --signal)
      signal="${2-}"
      shift 2
      ;;
    --signal=*)
      signal="${1#--signal=}"
      shift
      ;;
    -s)
      signal="${2-}"
      shift 2
      ;;
    -s*)
      signal="${1#-s}"
      shift
      ;;
    --preserve-status|--foreground|--verbose|-p|-f|-v)
      shift
      ;;
    --)
      shift
      break
      ;;
    *)
      duration="$1"
      shift
      break
      ;;
  esac
done

[ "${duration:-}" = 5 ] || exit 125
case "$signal" in
  0)
    exit 0
    ;;
  invalid-signal)
    exit 125
    ;;
esac

case "${1:-}" in
  */printf)
    exec "$@"
    ;;
  *)
    exit 126
    ;;
esac
`
	if err := os.WriteFile(timeoutPath, []byte(timeoutScript), 0o700); err != nil {
		t.Fatalf("write fake timeout: %v", err)
	}
	nonLaunchingDirectory := t.TempDir()
	nonLaunchingPath := filepath.Join(nonLaunchingDirectory, "timeout")
	if err := os.WriteFile(nonLaunchingPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write accepting non-launching timeout: %v", err)
	}
	redirectOutput := filepath.Join(t.TempDir(), "original-child-output")

	type timeoutLaunchFact struct {
		Segment int      `json:"segment"`
		Prefix  []string `json:"prefix"`
	}
	for _, testCase := range []struct {
		name           string
		command        string
		cwd            string
		commandPath    string
		decision       DecisionKind
		code           DiagnosticCode
		wantLaunches   []timeoutLaunchFact
		redirectOutput string
	}{
		{
			name:        "launches replacement child",
			command:     "timeout --signal TERM 5 git rebase topic",
			commandPath: timeoutDirectory,
			decision:    DecisionDefer,

			wantLaunches: []timeoutLaunchFact{{
				Segment: 1,
				Prefix:  []string{"timeout", "--signal", "TERM", "5"},
			}},
		},
		{
			name:        "slash qualified literal resolves from cwd",
			command:     "./timeout --signal TERM 5 git rebase topic",
			cwd:         timeoutDirectory,
			commandPath: timeoutDirectory,
			decision:    DecisionDefer,

			wantLaunches: []timeoutLaunchFact{{
				Segment: 1,
				Prefix:  []string{"./timeout", "--signal", "TERM", "5"},
			}},
		},
		{
			name:        "probe does not run original redirects",
			command:     "timeout --signal TERM 5 git rebase topic > " + redirectOutput,
			commandPath: timeoutDirectory,
			decision:    DecisionDefer,

			redirectOutput: redirectOutput,
			wantLaunches: []timeoutLaunchFact{{
				Segment: 1,
				Prefix:  []string{"timeout", "--signal", "TERM", "5"},
			}},
		},
		{
			name:        "unresolved relative callback path stays opaque",
			command:     "timeout --signal TERM 5 git rebase topic",
			commandPath: "relative",
			decision:    DecisionAllow,
		},
		{
			name:        "slash literal with relative cwd stays opaque",
			command:     "./timeout --signal TERM 5 git rebase topic",
			cwd:         "relative-cwd",
			commandPath: timeoutDirectory,
			decision:    DecisionAllow,
		},
		{
			name:     "missing callback path stays opaque",
			command:  "timeout --signal TERM 5 git rebase topic",
			decision: DecisionAllow,
		},
		{
			name:        "signal zero accepts without launch",
			command:     "timeout --signal 0 5 git rebase topic",
			commandPath: timeoutDirectory,
			decision:    DecisionAllow,
		},
		{
			name:        "invalid signal does not launch",
			command:     "timeout --signal invalid-signal 5 git rebase topic",
			commandPath: timeoutDirectory,
			decision:    DecisionAllow,
		},
		{
			name:        "environment wrapper stays opaque",
			command:     "env timeout --signal TERM 5 git rebase topic",
			commandPath: timeoutDirectory,
			decision:    DecisionAllow,
		},
		{
			name:        "assignment stays opaque",
			command:     "TIMEOUT_MODE=test timeout --signal TERM 5 git rebase topic",
			commandPath: timeoutDirectory,
			decision:    DecisionAllow,
		},
		{
			name:        "transparent wrapper stays opaque",
			command:     "nice timeout --signal TERM 5 git rebase topic",
			commandPath: timeoutDirectory,
			decision:    DecisionAllow,
		},
		{
			name:        "dynamic signal stays opaque",
			command:     "timeout --signal '$TIMEOUT_SIGNAL' 5 git rebase topic",
			commandPath: timeoutDirectory,
			decision:    DecisionAllow,
		},
		{
			name:        "accepting timeout does not launch",
			command:     "timeout --signal TERM 5 git rebase topic",
			commandPath: nonLaunchingDirectory,
			decision:    DecisionAllow,
		},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			request := activeWorker(testCase.command)
			if testCase.cwd != "" {
				request.CWD = testCase.cwd
			}
			request.CommandPath = testCase.commandPath
			result := Classify(request)
			if result.Decision != testCase.decision {
				t.Errorf("decision=%q diagnostic=%#v, want %q", result.Decision, result.Diagnostic, testCase.decision)
			}
			if testCase.code != "" && (result.Diagnostic == nil || result.Diagnostic.Code != testCase.code) {
				t.Errorf("diagnostic=%#v, want code %q", result.Diagnostic, testCase.code)
			}
			if testCase.code == "" && result.Diagnostic != nil {
				t.Errorf("diagnostic=%#v, want none", result.Diagnostic)
			}

			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatalf("marshal result: %v", err)
			}
			var output struct {
				TimeoutLaunches []timeoutLaunchFact `json:"timeout_launches"`
			}
			if err := json.Unmarshal(encoded, &output); err != nil {
				t.Fatalf("decode result: %v", err)
			}
			if len(output.TimeoutLaunches) != len(testCase.wantLaunches) {
				t.Errorf("timeout launches=%#v, want %#v", output.TimeoutLaunches, testCase.wantLaunches)
				return
			}
			for index, want := range testCase.wantLaunches {
				got := output.TimeoutLaunches[index]
				if got.Segment != want.Segment || strings.Join(got.Prefix, "\x00") != strings.Join(want.Prefix, "\x00") {
					t.Errorf("timeout launch[%d]=%#v, want %#v", index, got, want)
				}
			}
			if testCase.redirectOutput != "" {
				if _, err := os.Stat(testCase.redirectOutput); !os.IsNotExist(err) {
					t.Errorf("original redirect output=%q err=%v, want no file", testCase.redirectOutput, err)
				}
			}
		})
	}
}

// TestInactiveTimeoutDoesNotProbeCallbackExecutable verifies that ordinary
// inactive work never invokes a callback-selected timeout executable.
//
// Example: an inactive `timeout 5 git status` remains transparent even when
// its callback PATH resolves a literal timeout binary.
func TestInactiveTimeoutDoesNotProbeCallbackExecutable(t *testing.T) {
	t.Parallel()

	timeoutDirectory := t.TempDir()
	timeoutPath := filepath.Join(timeoutDirectory, "timeout")
	sentinelPath := timeoutPath + ".sentinel"
	timeoutScript := "#!/bin/sh\n: > \"$0.sentinel\"\nexit 0\n"
	if err := os.WriteFile(timeoutPath, []byte(timeoutScript), 0o700); err != nil {
		t.Fatalf("write sentinel timeout: %v", err)
	}

	request := activeWorker("timeout 5 git status")
	request.Marker = MarkerInactive
	request.CommandPath = timeoutDirectory
	result := Classify(request)
	if result.Decision != DecisionAllow || result.Diagnostic != nil {
		t.Fatalf("decision=%q diagnostic=%#v, want ordinary allow", result.Decision, result.Diagnostic)
	}
	if _, err := os.Stat(sentinelPath); !os.IsNotExist(err) {
		t.Fatalf("timeout probe sentinel=%q err=%v, want absent", sentinelPath, err)
	}
}

// TestTimeoutProbeUsesVerifiedCallbackContext verifies that an observed launch
// executes the replacement child with the callback's verified CWD and PATH.
//
// Example: a callback-selected timeout can expose a worker Git rebase only
// when its probe receives the same temporary CWD and PATH as the callback.
func TestTimeoutProbeUsesVerifiedCallbackContext(t *testing.T) {
	t.Parallel()

	callbackCWD := t.TempDir()
	timeoutDirectory := t.TempDir()
	timeoutPath := filepath.Join(timeoutDirectory, "timeout")
	contextPath := filepath.Join(t.TempDir(), "probe-context")
	timeoutScript := "#!/bin/sh\nset -eu\nprintf '%s\\n%s\\n' \"$PWD\" \"$PATH\" > " + strconv.Quote(contextPath) + "\n[ \"$1\" = 5 ]\nshift\n[ \"$PWD\" = " + strconv.Quote(callbackCWD) + " ]\n[ \"$PATH\" = " + strconv.Quote(timeoutDirectory) + " ]\nexec \"$@\"\n"
	if err := os.WriteFile(timeoutPath, []byte(timeoutScript), 0o700); err != nil {
		t.Fatalf("write context timeout: %v", err)
	}

	request := activeWorker("timeout 5 git rebase topic")
	request.CWD = callbackCWD
	request.CommandPath = timeoutDirectory
	result := Classify(request)
	context, err := os.ReadFile(contextPath)
	if err != nil {
		t.Fatalf("read probe context: %v", err)
	}
	wantContext := callbackCWD + "\n" + timeoutDirectory + "\n"
	if string(context) != wantContext {
		t.Fatalf("probe context=%q, want %q", context, wantContext)
	}
	if result.Decision != DecisionDefer || result.Diagnostic != nil {
		t.Fatalf("result=%#v, want observed worker Git provider defer", result)
	}
	if len(result.TimeoutLaunches) != 1 {
		t.Fatalf("timeout launches=%#v, want one observed launch", result.TimeoutLaunches)
	}
}

// TestTimeoutProbePreservesInheritedEnvironment verifies that the harmless
// timeout probe preserves inherited callback variables other than PATH and
// PWD.
//
// Example: a timeout implementation requiring PROBE_REQUIRED=present exposes
// its worker Git child only when that inherited variable reaches the probe.
func TestTimeoutProbePreservesInheritedEnvironment(t *testing.T) {
	callbackCWD := t.TempDir()
	timeoutDirectory := t.TempDir()
	timeoutPath := filepath.Join(timeoutDirectory, "timeout")
	timeoutScript := `#!/bin/sh
[ "${PROBE_REQUIRED-}" = present ] || exit 125
[ "${1-}" = 5 ] || exit 125
shift
exec "$@"
`
	if err := os.WriteFile(timeoutPath, []byte(timeoutScript), 0o700); err != nil {
		t.Fatalf("write required-environment timeout: %v", err)
	}

	t.Setenv("PROBE_REQUIRED", "present")
	result := classifyTimeoutJSONRequest(t, callbackCWD, timeoutDirectory, true, "timeout 5 git rebase topic")
	if result.Decision != DecisionDefer || result.Diagnostic != nil || len(result.TimeoutLaunches) != 1 {
		t.Fatalf("present inherited variable result=%#v, want observed worker Git provider defer", result)
	}

	t.Setenv("PROBE_REQUIRED", "wrong")
	result = classifyTimeoutJSONRequest(t, callbackCWD, timeoutDirectory, true, "timeout 5 git rebase topic")
	if result.Decision != DecisionAllow || result.Diagnostic != nil || len(result.TimeoutLaunches) != 0 {
		t.Fatalf("wrong inherited variable result=%#v, want ordinary opaque timeout", result)
	}

	if err := os.Unsetenv("PROBE_REQUIRED"); err != nil {
		t.Fatalf("unset required environment: %v", err)
	}
	result = classifyTimeoutJSONRequest(t, callbackCWD, timeoutDirectory, true, "timeout 5 git rebase topic")
	if result.Decision != DecisionAllow || result.Diagnostic != nil || len(result.TimeoutLaunches) != 0 {
		t.Fatalf("absent inherited variable result=%#v, want ordinary opaque timeout", result)
	}
}

// TestTimeoutResolvesRawCallbackPathEntries verifies that bare timeout follows
// the captured shell PATH order, including callback-CWD empty and relative
// entries.
//
// Example: PATH=/missing:bin reaches ./bin/timeout after the missing absolute
// candidate is skipped, while a first non-launching executable remains first.
func TestTimeoutResolvesRawCallbackPathEntries(t *testing.T) {
	t.Parallel()

	callbackCWD := t.TempDir()
	launchingTimeout := `#!/bin/sh
[ "${1-}" = 5 ] || exit 125
shift
exec "$@"
`
	for _, path := range []string{
		filepath.Join(callbackCWD, "timeout"),
		filepath.Join(callbackCWD, "bin", "timeout"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("create timeout directory %q: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(launchingTimeout), 0o700); err != nil {
			t.Fatalf("write launching timeout %q: %v", path, err)
		}
	}
	firstDirectory := filepath.Join(callbackCWD, "first")
	if err := os.MkdirAll(firstDirectory, 0o700); err != nil {
		t.Fatalf("create first timeout directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(firstDirectory, "timeout"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write non-launching first timeout: %v", err)
	}

	for _, testCase := range []struct {
		name     string
		path     string
		decision DecisionKind
	}{
		{name: "empty component resolves callback CWD", path: ":", decision: DecisionDefer},
		{name: "dot component resolves callback CWD", path: ".", decision: DecisionDefer},
		{name: "relative bin resolves callback CWD", path: "bin", decision: DecisionDefer},
		{name: "missing absolute prefix skips to relative bin", path: "/missing:bin", decision: DecisionDefer},
		{name: "first executable remains first", path: "first:bin", decision: DecisionAllow},
		{name: "unresolved absolute candidate stays opaque", path: "/missing", decision: DecisionAllow},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			result := classifyTimeoutJSONRequest(t, callbackCWD, testCase.path, true, "timeout 5 git rebase topic")
			if result.Decision != testCase.decision {
				t.Fatalf("PATH=%q decision=%q diagnostic=%#v, want %q", testCase.path, result.Decision, result.Diagnostic, testCase.decision)
			}
			if testCase.decision == DecisionDefer {
				if result.Diagnostic != nil || len(result.TimeoutLaunches) != 1 {
					t.Fatalf("PATH=%q result=%#v, want observed worker Git provider defer", testCase.path, result)
				}
				return
			}
			if result.Diagnostic != nil || len(result.TimeoutLaunches) != 0 {
				t.Fatalf("PATH=%q result=%#v, want ordinary opaque timeout", testCase.path, result)
			}
		})
	}
}

// TestTimeoutDirectLiteralsIgnoreEmptyOrUnsetCallbackPath verifies that direct
// absolute and slash-qualified timeout literals need only a valid callback
// CWD, while a bare timeout remains opaque for empty or unset PATH.
//
// Example: ./timeout 5 git rebase can launch with PATH unset, but bare timeout
// 5 git rebase cannot select a callback executable without a nonempty PATH.
func TestTimeoutDirectLiteralsIgnoreEmptyOrUnsetCallbackPath(t *testing.T) {
	t.Parallel()

	callbackCWD := t.TempDir()
	timeoutPath := filepath.Join(callbackCWD, "timeout")
	timeoutScript := `#!/bin/sh
[ "${1-}" = 5 ] || exit 125
shift
exec "$@"
`
	if err := os.WriteFile(timeoutPath, []byte(timeoutScript), 0o700); err != nil {
		t.Fatalf("write direct timeout: %v", err)
	}

	for _, testCase := range []struct {
		name           string
		command        string
		commandPath    string
		commandPathSet bool
		decision       DecisionKind
	}{
		{name: "absolute explicit empty PATH", command: timeoutPath + " 5 git rebase topic", commandPathSet: true, decision: DecisionDefer},
		{name: "absolute unset PATH", command: timeoutPath + " 5 git rebase topic", decision: DecisionDefer},
		{name: "slash explicit empty PATH", command: "./timeout 5 git rebase topic", commandPathSet: true, decision: DecisionDefer},
		{name: "slash unset PATH", command: "./timeout 5 git rebase topic", decision: DecisionDefer},
		{name: "bare explicit empty PATH", command: "timeout 5 git rebase topic", commandPathSet: true, decision: DecisionAllow},
		{name: "bare unset PATH", command: "timeout 5 git rebase topic", decision: DecisionAllow},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			result := classifyTimeoutJSONRequest(t, callbackCWD, testCase.commandPath, testCase.commandPathSet, testCase.command)
			if result.Decision != testCase.decision {
				t.Fatalf("PATH set=%t value=%q decision=%q diagnostic=%#v, want %q", testCase.commandPathSet, testCase.commandPath, result.Decision, result.Diagnostic, testCase.decision)
			}
			if testCase.decision == DecisionDefer {
				if result.Diagnostic != nil || len(result.TimeoutLaunches) != 1 {
					t.Fatalf("PATH set=%t value=%q result=%#v, want observed worker Git provider defer", testCase.commandPathSet, testCase.commandPath, result)
				}
				return
			}
			if result.Diagnostic != nil || len(result.TimeoutLaunches) != 0 {
				t.Fatalf("PATH set=%t value=%q result=%#v, want ordinary opaque timeout", testCase.commandPathSet, testCase.commandPath, result)
			}
		})
	}
}

// TestTimeoutProbeRejectsInvalidCallbackContext verifies that a malformed
// callback CWD or PATH leaves timeout opaque without launching its executable.
//
// Example: a missing callback directory or PATH entry cannot create a timeout
// launch fact for an otherwise literal timeout command.
func TestTimeoutProbeRejectsInvalidCallbackContext(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name        string
		callbackCWD func(t *testing.T) string
		commandPath func(t *testing.T, timeoutDirectory string) string
	}{
		{
			name: "missing callback directory",
			callbackCWD: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "missing")
			},
			commandPath: func(_ *testing.T, timeoutDirectory string) string {
				return timeoutDirectory
			},
		},
		{
			name: "unresolved callback PATH entry",
			callbackCWD: func(t *testing.T) string {
				return t.TempDir()
			},
			commandPath: func(t *testing.T, _ string) string {
				return filepath.Join(t.TempDir(), "missing")
			},
		},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			timeoutDirectory := t.TempDir()
			timeoutPath := filepath.Join(timeoutDirectory, "timeout")
			sentinelPath := timeoutPath + ".sentinel"
			timeoutScript := "#!/bin/sh\n: > \"$0.sentinel\"\nexit 0\n"
			if err := os.WriteFile(timeoutPath, []byte(timeoutScript), 0o700); err != nil {
				t.Fatalf("write sentinel timeout: %v", err)
			}

			request := activeWorker("timeout 5 git status")
			request.CWD = testCase.callbackCWD(t)
			request.CommandPath = testCase.commandPath(t, timeoutDirectory)
			result := Classify(request)
			if result.Decision != DecisionAllow || result.Diagnostic != nil || len(result.TimeoutLaunches) != 0 {
				t.Fatalf("result=%#v, want opaque ordinary timeout", result)
			}
			if _, err := os.Stat(sentinelPath); !os.IsNotExist(err) {
				t.Fatalf("timeout probe sentinel=%q err=%v, want absent", sentinelPath, err)
			}
		})
	}
}

// classifyTimeoutJSONRequest decodes one timeout request through the planner's
// JSON boundary so tests can distinguish an explicitly empty callback PATH
// from an unset callback PATH.
//
// Example: command_path_set=false with an empty command_path represents an
// unset PATH, while true with the same value represents an explicitly empty one.
func classifyTimeoutJSONRequest(
	t *testing.T,
	cwd string,
	commandPath string,
	commandPathSet bool,
	command string,
) Result {
	t.Helper()
	return classifyTimeoutReplayJSONRequest(t, cwd, commandPath, commandPathSet, commandPathSet, command)
}

// classifyTimeoutReplayJSONRequest decodes one timeout request with an explicit
// PATH export attribute through the planner JSON boundary.
//
// Example: a set-but-unexported PATH can still find timeout while the probe
// child receives no PATH environment entry.
func classifyTimeoutReplayJSONRequest(
	t *testing.T,
	cwd string,
	commandPath string,
	commandPathSet bool,
	commandPathExported bool,
	command string,
) Result {
	t.Helper()

	payload, err := json.Marshal(struct {
		Provider            Provider `json:"provider"`
		Role                Role     `json:"role"`
		CWD                 string   `json:"cwd"`
		CommandPath         string   `json:"command_path"`
		CommandPathSet      bool     `json:"command_path_set"`
		CommandPathExported bool     `json:"command_path_exported"`
		Marker              Marker   `json:"marker"`
		ActiveSession       string   `json:"active_session"`
		Command             string   `json:"command"`
	}{
		Provider:            ProviderOmp,
		Role:                RoleWorker,
		CWD:                 cwd,
		CommandPath:         commandPath,
		CommandPathSet:      commandPathSet,
		CommandPathExported: commandPathExported,
		Marker:              MarkerActive,
		ActiveSession:       "test-session",
		Command:             command,
	})
	if err != nil {
		t.Fatalf("encode timeout request: %v", err)
	}
	var request Request
	if err := json.Unmarshal(payload, &request); err != nil {
		t.Fatalf("decode timeout request: %v", err)
	}
	return Classify(request)
}

// TestTimeoutCompoundReplayUsesLiteralShellState verifies that one observed
// direct timeout child is classified from the literal CWD and PATH state
// reached through semicolon-only state prefixes.
//
// Example: cd /work/subdir; ./timeout 5 git rebase uses /work/subdir rather
// than the callback's outer working directory.
func TestTimeoutCompoundReplayUsesLiteralShellState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	downParent := filepath.Join(root, "down-parent")
	downChild := filepath.Join(downParent, "child")
	upParent := filepath.Join(root, "up-parent")
	upChild := filepath.Join(upParent, "child")
	launchDirectory := filepath.Join(root, "launch")
	nonLaunchingDirectory := filepath.Join(root, "nonlaunch")
	unexportedDirectory := filepath.Join(root, "unexported")
	for _, directory := range []string{
		downParent,
		downChild,
		upParent,
		upChild,
		launchDirectory,
		nonLaunchingDirectory,
		unexportedDirectory,
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("create %q: %v", directory, err)
		}
	}

	launchingTimeout := "#!/bin/sh\n[ \"${1-}\" = 5 ] || exit 125\nshift\nexec \"$@\"\n"
	unexportedTimeout := "#!/bin/sh\n/usr/bin/env | /usr/bin/grep -q '^PATH=' && exit 125\n[ \"${1-}\" = 5 ] || exit 125\nshift\nexec \"$@\"\n"
	for _, timeoutPath := range []string{
		filepath.Join(downParent, "timeout"),
		filepath.Join(downChild, "timeout"),
		filepath.Join(upParent, "timeout"),
		filepath.Join(launchDirectory, "timeout"),
	} {
		if err := os.WriteFile(timeoutPath, []byte(launchingTimeout), 0o700); err != nil {
			t.Fatalf("write launching timeout %q: %v", timeoutPath, err)
		}
	}
	if err := os.WriteFile(filepath.Join(nonLaunchingDirectory, "timeout"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write nonlaunching timeout: %v", err)
	}
	if err := os.WriteFile(filepath.Join(unexportedDirectory, "timeout"), []byte(unexportedTimeout), 0o700); err != nil {
		t.Fatalf("write unexported-PATH timeout: %v", err)
	}

	type timeoutReplayFact struct {
		Segment             int      `json:"segment"`
		ParentSegment       int      `json:"parent_segment"`
		Prefix              []string `json:"prefix"`
		CWD                 string   `json:"cwd"`
		CommandPath         string   `json:"command_path"`
		CommandPathSet      bool     `json:"command_path_set"`
		CommandPathExported bool     `json:"command_path_exported"`
		Disposition         string   `json:"disposition"`
	}

	for _, testCase := range []struct {
		name                string
		cwd                 string
		commandPath         string
		commandPathSet      bool
		commandPathExported bool
		command             string
		decision            DecisionKind
		wantReplay          *timeoutReplayFact
	}{
		{
			name:     "cd into child resolves dot timeout from child",
			cwd:      downParent,
			command:  "cd " + downChild + "; ./timeout 5 git rebase topic",
			decision: DecisionDefer,
			wantReplay: &timeoutReplayFact{
				Segment: 2, ParentSegment: 2, Prefix: []string{"./timeout", "5"}, CWD: downChild,
				Disposition: "observed",
			},
		},
		{
			name:     "cd back to parent resolves dot timeout from parent",
			cwd:      upChild,
			command:  "cd " + upParent + "; ./timeout 5 git rebase topic",
			decision: DecisionDefer,
			wantReplay: &timeoutReplayFact{
				Segment: 2, ParentSegment: 2, Prefix: []string{"./timeout", "5"}, CWD: upParent,
				Disposition: "observed",
			},
		},
		{
			name:                "literal PATH assignment retains exported state",
			cwd:                 downParent,
			commandPath:         nonLaunchingDirectory,
			commandPathSet:      true,
			commandPathExported: true,
			command:             "PATH=" + launchDirectory + "; timeout 5 git rebase topic",
			decision:            DecisionDefer,
			wantReplay: &timeoutReplayFact{
				Segment: 2, ParentSegment: 2, Prefix: []string{"timeout", "5"}, CWD: downParent,
				CommandPath: launchDirectory, CommandPathSet: true, CommandPathExported: true, Disposition: "observed",
			},
		},
		{
			name:                "literal PATH assignment records known nonlaunch",
			cwd:                 downParent,
			commandPath:         launchDirectory,
			commandPathSet:      true,
			commandPathExported: true,
			command:             "PATH=" + nonLaunchingDirectory + "; timeout 5 git rebase topic",
			decision:            DecisionAllow,
			wantReplay: &timeoutReplayFact{
				Segment: 2, ParentSegment: 2, Prefix: []string{"timeout", "5"}, CWD: downParent,
				CommandPath: nonLaunchingDirectory, CommandPathSet: true, CommandPathExported: true, Disposition: "opaque",
			},
		},
		{
			name:                "newline carries literal PATH assignment",
			cwd:                 downParent,
			commandPath:         nonLaunchingDirectory,
			commandPathSet:      true,
			commandPathExported: true,
			command:             "PATH=" + launchDirectory + "\ntimeout 5 git rebase topic",
			decision:            DecisionDefer,
			wantReplay: &timeoutReplayFact{
				Segment: 2, ParentSegment: 2, Prefix: []string{"timeout", "5"}, CWD: downParent,
				CommandPath: launchDirectory, CommandPathSet: true, CommandPathExported: true, Disposition: "observed",
			},
		},
		{
			name:     "export assignment creates exported PATH",
			cwd:      downParent,
			command:  "export PATH=" + launchDirectory + "; timeout 5 git rebase topic",
			decision: DecisionDefer,
			wantReplay: &timeoutReplayFact{
				Segment: 2, ParentSegment: 2, Prefix: []string{"timeout", "5"}, CWD: downParent,
				CommandPath: launchDirectory, CommandPathSet: true, CommandPathExported: true, Disposition: "observed",
			},
		},
		{
			name:                "unexported PATH still resolves lookup without reaching child",
			cwd:                 downParent,
			commandPath:         nonLaunchingDirectory,
			commandPathSet:      true,
			commandPathExported: true,
			command:             "PATH=" + unexportedDirectory + "; export -n PATH; timeout 5 git rebase topic",
			decision:            DecisionDefer,
			wantReplay: &timeoutReplayFact{
				Segment: 3, ParentSegment: 3, Prefix: []string{"timeout", "5"}, CWD: downParent,
				CommandPath: unexportedDirectory, CommandPathSet: true, CommandPathExported: false, Disposition: "observed",
			},
		},
		{
			name:                "export PATH restores child environment",
			cwd:                 downParent,
			commandPath:         nonLaunchingDirectory,
			commandPathSet:      true,
			commandPathExported: true,
			command:             "PATH=" + launchDirectory + "; export -n PATH; export PATH; timeout 5 git rebase topic",
			decision:            DecisionDefer,
			wantReplay: &timeoutReplayFact{
				Segment: 4, ParentSegment: 4, Prefix: []string{"timeout", "5"}, CWD: downParent,
				CommandPath: launchDirectory, CommandPathSet: true, CommandPathExported: true, Disposition: "observed",
			},
		},
		{
			name:                "set empty PATH differs from unset",
			cwd:                 downParent,
			commandPath:         launchDirectory,
			commandPathSet:      true,
			commandPathExported: true,
			command:             "PATH=; timeout 5 git rebase topic",
			decision:            DecisionAllow,
			wantReplay: &timeoutReplayFact{
				Segment: 2, ParentSegment: 2, Prefix: []string{"timeout", "5"}, CWD: downParent,
				CommandPath: "", CommandPathSet: true, CommandPathExported: true, Disposition: "opaque",
			},
		},
		{
			name:                "unset PATH is separate from set empty",
			cwd:                 downParent,
			commandPath:         launchDirectory,
			commandPathSet:      true,
			commandPathExported: true,
			command:             "unset PATH; timeout 5 git rebase topic",
			decision:            DecisionAllow,
			wantReplay: &timeoutReplayFact{
				Segment: 2, ParentSegment: 2, Prefix: []string{"timeout", "5"}, CWD: downParent,
				CommandPath: "", CommandPathSet: false, CommandPathExported: false, Disposition: "opaque",
			},
		},
		{
			name:                "conditional state change stays ordinary",
			cwd:                 downParent,
			commandPath:         launchDirectory,
			commandPathSet:      true,
			commandPathExported: true,
			command:             "PATH=" + nonLaunchingDirectory + " && timeout 5 git rebase topic",
			decision:            DecisionAllow,
		},
		{
			name:                "dynamic state change stays ordinary",
			cwd:                 downParent,
			commandPath:         launchDirectory,
			commandPathSet:      true,
			commandPathExported: true,
			command:             "PATH=$TIMEOUT_PATH; timeout 5 git rebase topic",
			decision:            DecisionAllow,
		},
		{
			name:                "relative cd carries verified CWD",
			cwd:                 downParent,
			commandPath:         launchDirectory,
			commandPathSet:      true,
			commandPathExported: true,
			command:             "cd child; timeout 5 git rebase topic",
			decision:            DecisionDefer,
			wantReplay: &timeoutReplayFact{
				Segment: 2, ParentSegment: 2, Prefix: []string{"timeout", "5"}, CWD: downChild,
				CommandPath: launchDirectory, CommandPathSet: true, CommandPathExported: true, Disposition: "observed",
			},
		},
		{
			name:                "ordinary prefix preserves verified state",
			cwd:                 downParent,
			commandPath:         launchDirectory,
			commandPathSet:      true,
			commandPathExported: true,
			command:             "printf harmless; timeout 5 git rebase topic",
			decision:            DecisionDefer,
			wantReplay: &timeoutReplayFact{
				Segment: 2, ParentSegment: 2, Prefix: []string{"timeout", "5"}, CWD: downParent,
				CommandPath: launchDirectory, CommandPathSet: true, CommandPathExported: true, Disposition: "observed",
			},
		},
		{
			name:     "true conditional split body carries verified CWD",
			cwd:      downParent,
			command:  "if true; then; cd " + downChild + "; fi; ./timeout 5 git rebase topic",
			decision: DecisionDefer,
			wantReplay: &timeoutReplayFact{
				Segment: 5, ParentSegment: 5, Prefix: []string{"./timeout", "5"}, CWD: downChild,
				Disposition: "observed",
			},
		},
		{
			name:     "false conditional split body keeps outer CWD",
			cwd:      downParent,
			command:  "if false; then; cd " + downChild + "; fi; ./timeout 5 git rebase topic",
			decision: DecisionDefer,
			wantReplay: &timeoutReplayFact{
				Segment: 5, ParentSegment: 5, Prefix: []string{"./timeout", "5"}, CWD: downParent,
				Disposition: "observed",
			},
		},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			result := classifyTimeoutReplayJSONRequest(
				t,
				testCase.cwd,
				testCase.commandPath,
				testCase.commandPathSet,
				testCase.commandPathExported,
				testCase.command,
			)
			if result.Decision != testCase.decision {
				t.Fatalf("decision=%q diagnostic=%#v, want %q", result.Decision, result.Diagnostic, testCase.decision)
			}
			if testCase.decision == DecisionDefer &&
				result.Diagnostic != nil {
				t.Fatalf("result=%#v, want observed worker Git provider defer", result)
			}
			if testCase.decision == DecisionAllow && result.Diagnostic != nil {
				t.Fatalf("result=%#v, want ordinary opaque timeout", result)
			}

			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatalf("marshal result: %v", err)
			}
			var output struct {
				TimeoutReplays []timeoutReplayFact `json:"timeout_replays"`
			}
			if err := json.Unmarshal(encoded, &output); err != nil {
				t.Fatalf("decode result: %v", err)
			}
			if testCase.wantReplay == nil {
				if len(output.TimeoutReplays) != 0 {
					t.Fatalf("timeout replays=%#v, want none", output.TimeoutReplays)
				}
				return
			}
			if len(output.TimeoutReplays) != 1 {
				t.Fatalf("timeout replays=%#v, want one", output.TimeoutReplays)
			}
			got := output.TimeoutReplays[0]
			want := *testCase.wantReplay
			if got.Segment != want.Segment || got.ParentSegment != want.ParentSegment ||
				strings.Join(got.Prefix, "\x00") != strings.Join(want.Prefix, "\x00") ||
				got.CWD != want.CWD || got.CommandPath != want.CommandPath ||
				got.CommandPathSet != want.CommandPathSet ||
				got.CommandPathExported != want.CommandPathExported || got.Disposition != want.Disposition {
				t.Fatalf("timeout replay=%#v, want %#v", got, want)
			}
		})
	}
}

// TestTimeoutReplayMissingOrMalformedDataStaysOrdinary verifies that recursive
// validation never falls back to a stale callback probe. Only one valid,
// observed parent record can expose timeout's Git child.
//
// Example: an absent replay remains ordinary even when the callback PATH would
// otherwise resolve a timeout executable that launches the harmless child.
func TestTimeoutReplayMissingOrMalformedDataStaysOrdinary(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	timeoutDirectory := filepath.Join(cwd, "bin")
	if err := os.MkdirAll(timeoutDirectory, 0o700); err != nil {
		t.Fatalf("create timeout directory: %v", err)
	}
	timeoutPath := filepath.Join(timeoutDirectory, "timeout")
	if err := os.WriteFile(timeoutPath, []byte("#!/bin/sh\n[ \"${1-}\" = 5 ] || exit 125\nshift\nexec \"$@\"\n"), 0o700); err != nil {
		t.Fatalf("write launching timeout: %v", err)
	}

	observed := TimeoutReplay{
		Segment:             1,
		ParentSegment:       2,
		Prefix:              []string{"timeout", "5"},
		CWD:                 cwd,
		CommandPath:         timeoutDirectory,
		CommandPathSet:      true,
		CommandPathExported: true,
		Disposition:         TimeoutReplayObserved,
	}
	malformed := observed
	malformed.CWD = "relative"
	opaque := observed
	opaque.Disposition = TimeoutReplayOpaque

	for _, testCase := range []struct {
		name        string
		replays     []TimeoutReplay
		decision    DecisionKind
		code        DiagnosticCode
		wantRecords int
	}{
		{
			name:     "missing replay suppresses stale probe",
			decision: DecisionAllow,
		},
		{
			name:     "malformed replay suppresses stale probe",
			replays:  []TimeoutReplay{malformed},
			decision: DecisionAllow,
		},
		{
			name:        "known nonlaunch replay stays opaque",
			replays:     []TimeoutReplay{opaque},
			decision:    DecisionAllow,
			wantRecords: 1,
		},
		{
			name:     "observed replay reaches Git child",
			replays:  []TimeoutReplay{observed},
			decision: DecisionDefer,

			wantRecords: 1,
		},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			request := activeWorker("timeout 5 git rebase topic")
			request.CWD = cwd
			request.CommandPath = timeoutDirectory
			request.TimeoutReplay = true
			request.TimeoutReplays = testCase.replays

			result := Classify(request)
			if result.Decision != testCase.decision {
				t.Fatalf("decision=%q diagnostic=%#v, want %q", result.Decision, result.Diagnostic, testCase.decision)
			}
			if testCase.code != "" && (result.Diagnostic == nil || result.Diagnostic.Code != testCase.code) {
				t.Fatalf("diagnostic=%#v, want code %q", result.Diagnostic, testCase.code)
			}
			if testCase.code == "" && result.Diagnostic != nil {
				t.Fatalf("diagnostic=%#v, want none", result.Diagnostic)
			}
			if len(result.TimeoutReplays) != testCase.wantRecords {
				t.Fatalf("timeout replays=%#v, want %d records", result.TimeoutReplays, testCase.wantRecords)
			}
		})
	}
}

// TestObservedTimeoutReplayUsesEffectiveCWDForLiveControl verifies that an
// observed timeout child resolves a relative writer target from its modeled
// execution CWD without moving the callback's outer scope anchor.
//
// Example: cd /inner; ./timeout 5 rm eci_active checks /inner/eci_active.
func TestObservedTimeoutReplayUsesEffectiveCWDForLiveControl(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outerDirectory := filepath.Join(root, "outer")
	actualControlDirectory := filepath.Join(root, "actual-control")
	ordinaryDirectory := filepath.Join(root, "ordinary")
	for _, directory := range []string{outerDirectory, actualControlDirectory, ordinaryDirectory} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("create directory %q: %v", directory, err)
		}
	}

	launchingTimeout := "#!/bin/sh\n[ \"${1-}\" = 5 ] || exit 125\nshift\nexec \"$@\"\n"
	for _, directory := range []string{actualControlDirectory, ordinaryDirectory} {
		if err := os.WriteFile(filepath.Join(directory, "timeout"), []byte(launchingTimeout), 0o700); err != nil {
			t.Fatalf("write timeout %q: %v", directory, err)
		}
	}

	actualMarker := filepath.Join(actualControlDirectory, "eci_active")
	outerMarker := filepath.Join(outerDirectory, "eci_active")
	ordinaryMarker := filepath.Join(ordinaryDirectory, "eci_active")
	for _, marker := range []string{actualMarker, outerMarker, ordinaryMarker} {
		if err := os.WriteFile(marker, []byte("active\n"), 0o600); err != nil {
			t.Fatalf("write marker %q: %v", marker, err)
		}
	}

	for _, testCase := range []struct {
		name         string
		outerCWD     string
		innerCWD     string
		activeMarker string
		decision     DecisionKind
		code         DiagnosticCode
	}{
		{
			name:         "inner actual control stays denied",
			outerCWD:     outerDirectory,
			innerCWD:     actualControlDirectory,
			activeMarker: actualMarker,
			decision:     DecisionDeny,
			code:         CodePlanLiveControlDenied,
		},
		{
			name:         "outer control does not falsely deny inner ordinary file",
			outerCWD:     outerDirectory,
			innerCWD:     ordinaryDirectory,
			activeMarker: outerMarker,
			decision:     DecisionAllow,
		},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			command := "cd " + testCase.innerCWD + "; ./timeout 5 rm eci_active"
			direct := activeWorker(command)
			direct.CWD = testCase.outerCWD
			direct.ActiveMarkers = []string{testCase.activeMarker}

			assertTimeoutControlDecision(t, Classify(direct), testCase.decision, testCase.code, testCase.activeMarker)

			recursive := activeWorker("./timeout 5 rm eci_active")
			recursive.CWD = testCase.outerCWD
			recursive.TimeoutReplay = true
			recursive.TimeoutReplays = []TimeoutReplay{{
				Segment:             1,
				ParentSegment:       2,
				Prefix:              []string{"./timeout", "5"},
				CWD:                 testCase.innerCWD,
				CommandPathSet:      false,
				CommandPathExported: false,
				Disposition:         TimeoutReplayObserved,
			}}
			recursive.ActiveMarkers = []string{testCase.activeMarker}

			assertTimeoutControlDecision(t, Classify(recursive), testCase.decision, testCase.code, testCase.activeMarker)
		})
	}
}

// assertTimeoutControlDecision verifies the concrete live-control outcome for
// both an outer compound command and its recursively replayed timeout child.
//
// Example: an inner eci_active marker produces CodePlanLiveControlDenied.
func assertTimeoutControlDecision(
	t *testing.T,
	result Result,
	decision DecisionKind,
	code DiagnosticCode,
	marker string,
) {
	t.Helper()
	if result.Decision != decision {
		t.Fatalf("decision=%q diagnostic=%#v, want %q", result.Decision, result.Diagnostic, decision)
	}
	if code == "" {
		if result.Diagnostic != nil {
			t.Fatalf("diagnostic=%#v, want none", result.Diagnostic)
		}
		return
	}
	if result.Diagnostic == nil || result.Diagnostic.Code != code || result.Diagnostic.Path != marker {
		t.Fatalf("diagnostic=%#v, want code=%q path=%q", result.Diagnostic, code, marker)
	}
}

// TestTimeoutProbeStopsAfterEarlierConcreteDenial verifies that ordered
// inspection returns before probing a later timeout after a concrete denial.
//
// Example: `rm -rf / && timeout 5 git status` denies the root deletion without
// invoking the later callback-selected timeout executable.
func TestTimeoutProbeStopsAfterEarlierConcreteDenial(t *testing.T) {
	t.Parallel()

	timeoutDirectory := t.TempDir()
	timeoutPath := filepath.Join(timeoutDirectory, "timeout")
	sentinelPath := timeoutPath + ".sentinel"
	timeoutScript := "#!/bin/sh\n: > \"$0.sentinel\"\nexit 0\n"
	if err := os.WriteFile(timeoutPath, []byte(timeoutScript), 0o700); err != nil {
		t.Fatalf("write sentinel timeout: %v", err)
	}

	request := activeWorker("rm -rf / && timeout 5 git status")
	request.CommandPath = timeoutDirectory
	result := Classify(request)
	if result.Decision != DecisionDeny || result.Diagnostic == nil ||
		result.Diagnostic.Code != CodeBroadDestructiveDenied {
		t.Fatalf("result=%#v, want earlier broad destructive denial", result)
	}
	if _, err := os.Stat(sentinelPath); !os.IsNotExist(err) {
		t.Fatalf("later timeout probe sentinel=%q err=%v, want absent", sentinelPath, err)
	}
}

// TestClassifyOrdinaryShellFormsDoNotBecomePermissionBoundaries verifies that
// syntax and launcher spelling alone do not turn routine work into a denial.
// Concrete destructive targets remain independently detectable.
//
// Example: a worker may run `printf "$(printf value)"`, while `rm -rf /`
// remains a resolved broad-destructive target.
func TestClassifyOrdinaryShellFormsDoNotBecomePermissionBoundaries(t *testing.T) {
	t.Parallel()

	ordinaryCommands := []string{
		`printf "%s\\n" "$(printf nested)"`,
		"printf updated > hooks/ordinary-target.txt",
		"novel-tool < input.txt",
		"novel-tool <(printf input)",
		"novel-tool &",
		"printf `printf nested`",
		"printf ${UNKNOWN_VALUE}/ordinary-path",
		"printf {one,two}",
		"python3 -c 'print(1)'",
		"file -C -m hooks/validate-bash.sh | head -n 20",
		"env GIT_DIR=.git git archive HEAD",
		"env",
		"printenv",
		"env --unset=9FOO novel-tool",
		"env FOO=bar python3 -c 'print(1)'",
		"FOO=bar novel-tool",
		"novel-tool > output.txt",
	}

	for _, role := range []Role{RoleCoordinator, RoleWorker} {
		for _, command := range ordinaryCommands {
			request := activeWorker(command)
			request.Role = role
			result := Classify(request)
			if result.Decision == DecisionDeny {
				t.Fatalf("ordinary command denied: role=%q command=%q diagnostic=%#v", role, command, result.Diagnostic)
			}
		}
	}

	for _, role := range []Role{RoleCoordinator, RoleWorker} {
		for _, command := range []string{
			"rm -rf /",
			"rm -rf / > ordinary-output.txt",
			"rm -rf / &",
			"find / -delete",
		} {
			request := activeWorker(command)
			request.Role = role
			result := Classify(request)
			if result.Decision != DecisionDeny || result.Diagnostic == nil {
				t.Fatalf("broad destructive target: role=%q command=%q result=%#v, want denial", role, command, result)
			}
			if result.Diagnostic.Code != CodeBroadDestructiveDenied {
				t.Fatalf("broad destructive target code: command=%q got %q, want %q", command, result.Diagnostic.Code, CodeBroadDestructiveDenied)
			}
		}
	}
}

// TestRedirectOutputRetainsConcreteControlTarget verifies that redirection is
// ordinary syntax while its resolved destination still reaches the existing
// active-control checks.
func TestRedirectOutputRetainsConcreteControlTarget(t *testing.T) {
	proofRoot := t.TempDir()
	foreignSession := filepath.Join(proofRoot, "foreign-session")
	if err := os.MkdirAll(foreignSession, 0o700); err != nil {
		t.Fatalf("create foreign session: %v", err)
	}
	foreignMarker := filepath.Join(foreignSession, "eci_active")
	if err := os.WriteFile(foreignMarker, []byte("active\n"), 0o600); err != nil {
		t.Fatalf("write foreign marker: %v", err)
	}

	for _, role := range []Role{RoleCoordinator, RoleWorker} {
		result := Classify(Request{
			Provider:      ProviderOmp,
			Role:          role,
			CWD:           proofRoot,
			Marker:        MarkerActive,
			ActiveSession: "session",
			ActiveMarkers: []string{foreignMarker},
			Command:       "printf marker > " + foreignMarker,
		})
		if result.Decision != DecisionDeny || result.Diagnostic == nil {
			t.Fatalf("role=%q result=%#v, want concrete control-target denial", role, result)
		}
		if result.Diagnostic.Code != CodePlanLiveControlDenied || result.Diagnostic.Path != foreignMarker {
			t.Fatalf("role=%q diagnostic=%#v, want live control path %q", role, result.Diagnostic, foreignMarker)
		}
	}
}

// TestOutputRedirectsRetainConcreteFileEffects verifies that output redirects
// preserve their concrete filesystem effect while descriptor duplication does
// not invent a file target.
//
// Example: &>> ledger is an append redirect, while 2>&1 has no ledger target.
func TestOutputRedirectsRetainConcreteFileEffects(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name       string
		command    string
		redirects  int
		effect     outputRedirectEffect
		target     string
		argvLength int
	}{
		{name: "overwrite", command: "printf note > ledger", redirects: 1, effect: outputRedirectOverwrite, target: "ledger", argvLength: 2},
		{name: "force overwrite", command: "printf note >| ledger", redirects: 1, effect: outputRedirectForceOverwrite, target: "ledger", argvLength: 2},
		{name: "append", command: "printf note >> ledger", redirects: 1, effect: outputRedirectAppend, target: "ledger", argvLength: 2},
		{name: "stderr append", command: "printf note 2>> ledger", redirects: 1, effect: outputRedirectAppend, target: "ledger", argvLength: 2},
		{name: "path output duplication spelling", command: "printf note >& ledger", redirects: 1, effect: outputRedirectOverwrite, target: "ledger", argvLength: 2},
		{name: "combined overwrite", command: "printf note &> ledger", redirects: 1, effect: outputRedirectOverwrite, target: "ledger", argvLength: 2},
		{name: "combined append", command: "printf note &>> ledger", redirects: 1, effect: outputRedirectAppend, target: "ledger", argvLength: 2},
		{name: "numeric descriptor duplication", command: "printf note 2>&1", redirects: 0, argvLength: 2},
		{name: "closed descriptor duplication", command: "printf note >&-", redirects: 0, argvLength: 2},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			parsed, err := parsePlan(testCase.command)
			if err != nil {
				t.Fatalf("parsePlan(%q): %v", testCase.command, err)
			}
			if len(parsed.segments) != 1 {
				t.Fatalf("segments=%d, want one", len(parsed.segments))
			}
			segment := parsed.segments[0]
			if len(segment.argv) != testCase.argvLength {
				t.Fatalf("argv=%v, want length %d", segment.argv, testCase.argvLength)
			}
			if len(segment.redirects) != testCase.redirects {
				t.Fatalf("redirects=%#v, want %d redirects", segment.redirects, testCase.redirects)
			}
			if testCase.redirects == 0 {
				return
			}
			redirect := segment.redirects[0]
			if redirect.effect != testCase.effect || redirect.target.value != testCase.target {
				t.Fatalf("redirect=%#v, want effect=%q target=%q", redirect, testCase.effect, testCase.target)
			}
		})
	}
}

// TestLedgerHardlinkAliasesKeepConcreteDiagnostics verifies that aliases of
// selected and sibling ledger artifacts retain their ledger-specific target
// diagnostic before a generic worker control-path rule can apply.
//
// Example: an external hardlink to the selected anchor remains an anchor
// denial instead of becoming a generic live-control denial.
func TestLedgerHardlinkAliasesKeepConcreteDiagnostics(t *testing.T) {
	proofRoot := t.TempDir()
	currentSession := filepath.Join(proofRoot, "current-session")
	foreignSession := filepath.Join(proofRoot, "foreign-session")
	for _, sessionDir := range []string{currentSession, foreignSession} {
		if err := os.MkdirAll(sessionDir, 0o700); err != nil {
			t.Fatalf("create session directory %q: %v", sessionDir, err)
		}
		if err := os.WriteFile(filepath.Join(sessionDir, "high_level_log.jsonl"), []byte("{\"event\":\"decision\",\"summary\":\"prior entry\"}\n"), 0o600); err != nil {
			t.Fatalf("write session log %q: %v", sessionDir, err)
		}
		if err := os.WriteFile(filepath.Join(sessionDir, "high_level_log.anchor"), []byte("anchor\n"), 0o600); err != nil {
			t.Fatalf("write session anchor %q: %v", sessionDir, err)
		}
	}
	marker := filepath.Join(currentSession, "eci_active")
	if err := os.WriteFile(marker, []byte("active\n"), 0o600); err != nil {
		t.Fatalf("write current marker: %v", err)
	}

	currentLog := filepath.Join(currentSession, "high_level_log.jsonl")
	currentAnchor := filepath.Join(currentSession, "high_level_log.anchor")
	foreignLog := filepath.Join(foreignSession, "high_level_log.jsonl")
	foreignAnchor := filepath.Join(foreignSession, "high_level_log.anchor")
	aliasRoot := t.TempDir()
	aliases := []struct {
		name    string
		target  string
		command string
		code    DiagnosticCode
	}{
		{name: "selected log rewrite", target: currentLog, command: "printf '%s\\n' '{\"event\":\"decision\",\"summary\":\"entry\"}' > ", code: CodeLedgerRewriteDenied},
		{name: "selected log shared append", target: currentLog, command: "printf '%s\\n' '{\"event\":\"decision\",\"summary\":\"entry\"}' >> ", code: CodeLedgerSharedInodeDenied},
		{name: "selected anchor", target: currentAnchor, command: "printf '%s\\n' '{\"event\":\"decision\",\"summary\":\"entry\"}' >> ", code: CodeLedgerAnchorWriteDenied},
		{name: "foreign log", target: foreignLog, command: "printf '%s\\n' '{\"event\":\"decision\",\"summary\":\"entry\"}' >> ", code: CodeLedgerForeignSessionDenied},
		{name: "foreign anchor", target: foreignAnchor, command: "printf '%s\\n' '{\"event\":\"decision\",\"summary\":\"entry\"}' >> ", code: CodeLedgerForeignSessionDenied},
	}

	for index := range aliases {
		aliases[index].target = filepath.Clean(aliases[index].target)
		aliasPath := filepath.Join(aliasRoot, strconv.Itoa(index)+"-ledger-alias")
		if err := os.Link(aliases[index].target, aliasPath); err != nil {
			t.Fatalf("create %s hardlink: %v", aliases[index].name, err)
		}
		aliases[index].command += aliasPath
	}

	for _, role := range []Role{RoleCoordinator, RoleWorker} {
		for _, testCase := range aliases {
			testCase := testCase
			t.Run(string(role)+"/"+testCase.name, func(t *testing.T) {
				result := Classify(Request{
					Provider:      ProviderOmp,
					Role:          role,
					CWD:           proofRoot,
					Marker:        MarkerActive,
					ActiveSession: "opaque-callback-context",
					Command:       testCase.command,
					ActiveMarkers: []string{marker},
				})
				if result.Decision != DecisionDeny || result.Diagnostic == nil || result.Diagnostic.Code != testCase.code {
					t.Fatalf("result=%#v, want %q", result, testCase.code)
				}
				if result.Diagnostic.Code == CodeControlOwnerRequired || result.Diagnostic.Code == CodePlanLiveControlDenied {
					t.Fatalf("result=%#v, want a ledger-specific diagnostic", result)
				}
			})
		}
	}
}

// TestCurrentSessionLedgerRedirectsResolveEffectAndOwnership verifies that a
// raw EOF append to the selected session log is ordinary work, while rewrites,
// anchors, foreign session records, escaping links, and shared inodes retain
// their concrete ownership diagnostics.
//
// Example: appending a JSON event to current/high_level_log.jsonl is allowed
// for either active role, while >| to that same file is a rewrite denial.
func TestCurrentSessionLedgerRedirectsResolveEffectAndOwnership(t *testing.T) {
	t.Parallel()

	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()

			proofRoot := t.TempDir()
			currentSession := filepath.Join(proofRoot, "current-session")
			foreignSession := filepath.Join(proofRoot, "foreign-session")
			for _, sessionDir := range []string{currentSession, foreignSession} {
				if err := os.MkdirAll(sessionDir, 0o700); err != nil {
					t.Fatalf("create session directory %q: %v", sessionDir, err)
				}
				if err := os.WriteFile(filepath.Join(sessionDir, "high_level_log.jsonl"), []byte("{\"event\":\"decision\",\"summary\":\"prior entry\"}\n"), 0o600); err != nil {
					t.Fatalf("write session log %q: %v", sessionDir, err)
				}
				if err := os.WriteFile(filepath.Join(sessionDir, "high_level_log.anchor"), []byte("anchor\n"), 0o600); err != nil {
					t.Fatalf("write session anchor %q: %v", sessionDir, err)
				}
			}
			marker := filepath.Join(currentSession, "eci_active")
			if err := os.WriteFile(marker, []byte("active\n"), 0o600); err != nil {
				t.Fatalf("write current marker: %v", err)
			}

			currentLog := filepath.Join(currentSession, "high_level_log.jsonl")
			currentAnchor := filepath.Join(currentSession, "high_level_log.anchor")
			foreignLog := filepath.Join(foreignSession, "high_level_log.jsonl")
			foreignAnchor := filepath.Join(foreignSession, "high_level_log.anchor")
			ordinaryOutput := filepath.Join(proofRoot, "ordinary-output.txt")
			outside := filepath.Join(proofRoot, "outside-log.txt")
			if err := os.WriteFile(outside, []byte("outside\n"), 0o600); err != nil {
				t.Fatalf("write outside target: %v", err)
			}
			escapingLink := filepath.Join(currentSession, "escaping-output")
			if err := os.Symlink(outside, escapingLink); err != nil {
				t.Fatalf("create escaping output link: %v", err)
			}
			proofAlias := filepath.Join(t.TempDir(), "proof-root-alias")
			if err := os.Symlink(proofRoot, proofAlias); err != nil {
				t.Fatalf("create proof root alias: %v", err)
			}
			aliasedCurrentLog := filepath.Join(proofAlias, "current-session", "high_level_log.jsonl")

			for _, role := range []Role{RoleCoordinator, RoleWorker} {
				role := role
				t.Run(string(role), func(t *testing.T) {
					request := func(command string) Request {
						return Request{
							Provider:      provider,
							Role:          role,
							CWD:           proofRoot,
							Marker:        MarkerActive,
							ActiveSession: "opaque-callback-context",
							Command:       command,
							ActiveMarkers: []string{marker},
						}
					}
					for _, testCase := range []struct {
						name       string
						command    string
						decision   DecisionKind
						code       DiagnosticCode
						detailPart string
					}{
						{name: "current append", command: "printf '%s\\n' '{\"event\":\"decision\",\"summary\":\"entry\"}' >> " + currentLog, decision: DecisionAllow},
						{name: "quoted current append", command: "printf '%s\\n' '{\"event\":\"decision\",\"summary\":\"quoted entry\"}' >> \"" + currentLog + "\"", decision: DecisionAllow},
						{name: "semicolon composed current append", command: "printf before; printf '%s\\n' '{\"event\":\"decision\",\"summary\":\"entry\"}' >> " + currentLog, decision: DecisionAllow},
						{name: "current append through proof alias", command: "printf '%s\\n' '{\"event\":\"decision\",\"summary\":\"entry\"}' >> " + aliasedCurrentLog, decision: DecisionAllow},
						{name: "current stderr append", command: "printf '%s\\n' '{\"event\":\"decision\",\"summary\":\"entry\"}' 2>> " + currentLog, decision: DecisionAllow},
						{name: "current rewrite", command: "printf '%s\\n' '{\"event\":\"decision\",\"summary\":\"entry\"}' > " + currentLog, decision: DecisionDeny, code: "ECI_LEDGER_REWRITE_DENIED", detailPart: "effect=overwrite"},
						{name: "current forced rewrite", command: "printf '%s\\n' '{\"event\":\"decision\",\"summary\":\"entry\"}' >| " + currentLog, decision: DecisionDeny, code: "ECI_LEDGER_REWRITE_DENIED", detailPart: "effect=force-overwrite"},
						{name: "current anchor", command: "printf '%s\\n' '{\"event\":\"decision\",\"summary\":\"entry\"}' >> " + currentAnchor, decision: DecisionDeny, code: "ECI_LEDGER_ANCHOR_WRITE_DENIED", detailPart: "target=high_level_log.anchor"},
						{name: "foreign log", command: "printf '%s\\n' '{\"event\":\"decision\",\"summary\":\"entry\"}' >> " + foreignLog, decision: DecisionDeny, code: "ECI_LEDGER_FOREIGN_SESSION_DENIED", detailPart: "target_session=foreign-session"},
						{name: "foreign anchor", command: "printf '%s\\n' '{\"event\":\"decision\",\"summary\":\"entry\"}' >> " + foreignAnchor, decision: DecisionDeny, code: "ECI_LEDGER_FOREIGN_SESSION_DENIED", detailPart: "target_session=foreign-session"},
						{name: "ordinary output through proof alias", command: "printf '%s\\n' '{\"event\":\"decision\",\"summary\":\"entry\"}' >> " + escapingLink, decision: DecisionAllow},
						{name: "ordinary overwrite", command: "printf '%s\\n' '{\"event\":\"decision\",\"summary\":\"entry\"}' > " + ordinaryOutput, decision: DecisionAllow},
						{name: "ordinary append", command: "printf '%s\\n' '{\"event\":\"decision\",\"summary\":\"entry\"}' >> " + ordinaryOutput, decision: DecisionAllow},
						{name: "ordinary forced overwrite", command: "printf '%s\\n' '{\"event\":\"decision\",\"summary\":\"entry\"}' >| " + ordinaryOutput, decision: DecisionAllow},
					} {
						testCase := testCase
						t.Run(testCase.name, func(t *testing.T) {
							result := Classify(request(testCase.command))
							if result.Decision != testCase.decision {
								t.Fatalf("decision: got %q diagnostic=%#v, want %q", result.Decision, result.Diagnostic, testCase.decision)
							}
							if testCase.code == "" {
								if result.Diagnostic != nil {
									t.Fatalf("unexpected diagnostic: %#v", result.Diagnostic)
								}
								return
							}
							if result.Diagnostic == nil || result.Diagnostic.Code != testCase.code {
								t.Fatalf("diagnostic: got %#v, want code %q", result.Diagnostic, testCase.code)
							}
							if testCase.detailPart != "" && !strings.Contains(result.Diagnostic.Reason, testCase.detailPart) {
								t.Fatalf("diagnostic reason: got %q, want %q", result.Diagnostic.Reason, testCase.detailPart)
							}
						})
					}
				})
			}

			sharedAlias := filepath.Join(proofRoot, "shared-log-alias")
			if err := os.Link(currentLog, sharedAlias); err != nil {
				t.Fatalf("create shared current log inode: %v", err)
			}
			for _, role := range []Role{RoleCoordinator, RoleWorker} {
				result := Classify(Request{
					Provider:      provider,
					Role:          role,
					CWD:           proofRoot,
					Marker:        MarkerActive,
					ActiveSession: "opaque-callback-context",
					Command:       "printf note >> " + currentLog,
					ActiveMarkers: []string{marker},
				})
				if result.Decision != DecisionDeny || result.Diagnostic == nil || result.Diagnostic.Code != "ECI_LEDGER_SHARED_INODE_DENIED" {
					t.Fatalf("shared current log: role=%q result=%#v, want shared-inode denial", role, result)
				}
				if !strings.Contains(result.Diagnostic.Reason, "nlink=2") {
					t.Fatalf("shared current log reason: got %q, want nlink=2", result.Diagnostic.Reason)
				}
			}
		})
	}
}

// TestActiveBareGitCloneSourceAcquisitionCapability verifies an active Git
// clone selects the typed source-acquisition capability with planner-attested
// launch metadata, without interpreting clone-specific options.
//
// Example: env -- /usr/bin/git clone source destination records both literal
// executable tokens so the provider can bind their actual identities.
// TODO: Revisit inherited env/clone Allow expectations when final ShellAnalysis
// defer policy changes; it currently produces Defer instead.
func TestActiveBareGitCloneSourceAcquisitionCapability(t *testing.T) {
	t.Parallel()

	type launchDescriptor struct {
		Class                string `json:"class"`
		GitArgvIndex         int    `json:"git_argv_index"`
		GitExecutable        string `json:"git_executable"`
		EnvironmentPreserved bool   `json:"environment_preserved"`
		EnvArgvIndex         *int   `json:"env_argv_index"`
		EnvExecutable        string `json:"env_executable"`
	}
	type resultEnvelope struct {
		GitCloneLaunch *launchDescriptor `json:"git_clone_launch"`
	}
	launchFromResult := func(t *testing.T, result Result) *launchDescriptor {
		t.Helper()

		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("marshal result: %v", err)
		}
		var envelope resultEnvelope
		if err := json.Unmarshal(encoded, &envelope); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		return envelope.GitCloneLaunch
	}
	indexPointer := func(value int) *int {
		return &value
	}

	const expectedCapability Capability = "git-clone-source-acquisition"
	const cloneCommand = "git clone --branch main --depth 1 https://example.invalid/owner/repo.git /home/pheona/tmp/eci-stop-gate-src"
	const opaqueCloneOptionsCommand = "git clone --template=/templates/clone --reference=/cache/repository --shared --upload-pack=/usr/lib/git-core/git-upload-pack --future-clone-option source destination"
	const quotedGitCloneCommand = "'git' clone source destination"
	launchCases := []struct {
		name    string
		command string
		launch  launchDescriptor
	}{
		{
			name:    "bare Git",
			command: cloneCommand,
			launch: launchDescriptor{
				Class: "direct", GitArgvIndex: 0, GitExecutable: "git", EnvironmentPreserved: true,
			},
		},
		{
			name:    "opaque clone options",
			command: opaqueCloneOptionsCommand,
			launch: launchDescriptor{
				Class: "direct", GitArgvIndex: 0, GitExecutable: "git", EnvironmentPreserved: true,
			},
		},
		{
			name:    "quoted Git",
			command: quotedGitCloneCommand,
			launch: launchDescriptor{
				Class: "direct", GitArgvIndex: 0, GitExecutable: "git", EnvironmentPreserved: true,
			},
		},
		{
			name:    "path qualified Git",
			command: "/usr/bin/git clone source destination",
			launch: launchDescriptor{
				Class: "direct", GitArgvIndex: 0, GitExecutable: "/usr/bin/git", EnvironmentPreserved: true,
			},
		},
		{
			name:    "transparent command",
			command: "command git clone source destination",
			launch: launchDescriptor{
				Class: "command", GitArgvIndex: 1, GitExecutable: "git", EnvironmentPreserved: true,
			},
		},
		{
			name:    "transparent command after options terminator",
			command: "command -- /usr/bin/git clone source destination",
			launch: launchDescriptor{
				Class: "command", GitArgvIndex: 2, GitExecutable: "/usr/bin/git", EnvironmentPreserved: true,
			},
		},
		{
			name:    "transparent env",
			command: "env git clone source destination",
			launch: launchDescriptor{
				Class: "env", GitArgvIndex: 1, GitExecutable: "git", EnvironmentPreserved: true,
				EnvArgvIndex: indexPointer(0), EnvExecutable: "env",
			},
		},
		{
			name:    "transparent env after options terminator",
			command: "env -- /usr/bin/git clone source destination",
			launch: launchDescriptor{
				Class: "env", GitArgvIndex: 2, GitExecutable: "/usr/bin/git", EnvironmentPreserved: true,
				EnvArgvIndex: indexPointer(0), EnvExecutable: "env",
			},
		},
	}

	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()

			for _, role := range []Role{RoleCoordinator, RoleWorker} {
				role := role
				for _, testCase := range launchCases {
					testCase := testCase
					t.Run(string(role)+"/"+testCase.name, func(t *testing.T) {
						request := activeWorker(testCase.command)
						request.Provider = provider
						request.Role = role

						result := Classify(request)
						if result.Decision != DecisionAllow || result.Diagnostic != nil || result.DeferredRoute != "" {
							t.Fatalf("%q: result=%#v, want direct allow without diagnostic or route", testCase.command, result)
						}
						if len(result.Capabilities) != 1 || result.Capabilities[0] != expectedCapability {
							t.Fatalf("%q: capabilities=%v, want [%q]", testCase.command, result.Capabilities, expectedCapability)
						}
						launch := launchFromResult(t, result)
						if launch == nil {
							t.Fatalf("%q: missing git_clone_launch metadata", testCase.command)
						}
						if launch.Class != testCase.launch.Class ||
							launch.GitArgvIndex != testCase.launch.GitArgvIndex ||
							launch.GitExecutable != testCase.launch.GitExecutable ||
							launch.EnvironmentPreserved != testCase.launch.EnvironmentPreserved ||
							launch.EnvExecutable != testCase.launch.EnvExecutable {
							t.Fatalf("%q: launch=%#v, want %#v", testCase.command, launch, testCase.launch)
						}
						if testCase.launch.EnvArgvIndex == nil {
							if launch.EnvArgvIndex != nil {
								t.Fatalf("%q: env argv index=%v, want absent", testCase.command, *launch.EnvArgvIndex)
							}
						} else if launch.EnvArgvIndex == nil || *launch.EnvArgvIndex != *testCase.launch.EnvArgvIndex {
							t.Fatalf("%q: env argv index=%v, want %d", testCase.command, launch.EnvArgvIndex, *testCase.launch.EnvArgvIndex)
						}
					})
				}
			}
		})
	}

	for _, testCase := range []struct {
		name    string
		command string
	}{
		{name: "compound plan", command: "git clone source destination && printf after"},
	} {
		testCase := testCase
		t.Run("no-capability/"+testCase.name, func(t *testing.T) {
			t.Parallel()

			for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
				for _, role := range []Role{RoleCoordinator, RoleWorker} {
					request := activeWorker(testCase.command)
					request.Provider = provider
					request.Role = role
					result := Classify(request)
					if len(result.Capabilities) != 0 {
						t.Fatalf("provider=%q role=%q %q: capabilities=%v, want none", provider, role, testCase.command, result.Capabilities)
					}
				}
			}
		})
	}

	for _, testCase := range []struct {
		name         string
		command      string
		wantDecision DecisionKind
	}{
		{name: "env assignment", command: "env FOO=bar git clone source destination"},
		{name: "env ignores inherited environment", command: "env -i git clone source destination"},
		{name: "env removes inherited variable", command: "env -u HOME git clone source destination"},
		{name: "env changes directory", command: "env -C /tmp git clone source destination"},
		{name: "env split string", command: "env -S 'git clone source destination'", wantDecision: DecisionAllow},
		{name: "git repository directory context", command: "git -C /tmp clone source destination"},
		{name: "git configuration context", command: "git -c user.name=test clone source destination"},
		{name: "git directory context", command: "git --git-dir=.git clone source destination"},
		{name: "command default path", command: "command -p git clone source destination"},
		{name: "command query option", command: "command -v git clone source destination"},
		{name: "command verbose query option", command: "command -V git clone source destination"},
		{name: "exec wrapper", command: "exec git clone source destination"},
		{name: "privilege wrapper", command: "sudo git clone source destination"},
		{name: "service wrapper", command: "systemd-run -- git clone source destination"},
	} {
		testCase := testCase
		t.Run("changed-launch-context/"+testCase.name, func(t *testing.T) {
			t.Parallel()

			for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
				for _, role := range []Role{RoleCoordinator, RoleWorker} {
					request := activeWorker(testCase.command)
					request.Provider = provider
					request.Role = role
					result := Classify(request)
					wantDecision := testCase.wantDecision
					if wantDecision == "" {
						wantDecision = DecisionDeny
					}
					if result.Decision != wantDecision {
						t.Fatalf("provider=%q role=%q %q: result=%#v, want decision %q", provider, role, testCase.command, result, wantDecision)
					}
					if wantDecision == DecisionDeny && result.Diagnostic == nil {
						t.Fatalf("provider=%q role=%q %q: result=%#v, want changed-context denial", provider, role, testCase.command, result)
					}
					if len(result.Capabilities) != 0 {
						t.Fatalf("provider=%q role=%q %q: capabilities=%v, want none", provider, role, testCase.command, result.Capabilities)
					}
					if launch := launchFromResult(t, result); launch != nil {
						t.Fatalf("provider=%q role=%q %q: launch=%#v, want absent", provider, role, testCase.command, launch)
					}
				}
			}
		})
	}
}

// TestRawGitPlansHaveNoGenericCapability verifies raw Git inspection never
// carries a generic fast capability; trusted lifecycle helpers own Git access.
//
// Example: git --no-pager rev-parse HEAD remains capability-free like status.
func TestRawGitPlansHaveNoGenericCapability(t *testing.T) {
	t.Parallel()

	readOnly := []string{
		"git status --short",
		"git --no-pager rev-parse HEAD",
		"git archive HEAD",
		// Git context spelling and exclude pathspecs do not create an
		// effect by themselves.  The planner defers these forms without a
		// diagnostic so the provider can resolve the concrete read target.
		"git -C /tmp/foreign status --short",
		"git -C /tmp/root -C /tmp/foreign status --short",
		"git -C /tmp/root diff -- :(exclude)AGENTS.md",
		"git -C /tmp/root diff -- ':(exclude)AGENTS.md'",
	}
	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()

			for _, role := range []Role{RoleCoordinator, RoleWorker} {
				for _, command := range readOnly {
					request := activeWorker(command)
					request.Provider = provider
					request.Role = role
					result := Classify(request)
					if result.Decision != DecisionDefer || result.Diagnostic != nil {
						t.Errorf("role=%q %q: decision=%q diagnostic=%#v, want defer without diagnostic", role, command, result.Decision, result.Diagnostic)
					}
					if len(result.Capabilities) != 0 {
						t.Errorf("role=%q %q: capabilities=%v, want none", role, command, result.Capabilities)
					}
				}
			}

			foreignContext := activeWorker("git -C /tmp/foreign status --short")
			foreignContext.Provider = provider
			if result := Classify(foreignContext); result.Decision != DecisionDefer || result.Diagnostic != nil {
				t.Errorf("foreign context: decision=%q diagnostic=%#v, want defer without diagnostic", result.Decision, result.Diagnostic)
			} else if len(result.Capabilities) != 0 {
				t.Errorf("foreign context: capabilities=%v, want none", result.Capabilities)
			}

			for _, testCase := range []struct {
				command string
				code    DiagnosticCode
			}{
				{command: "git rebase topic"},
				{command: "git checkout -- hooks/validate-bash.sh"},
				{command: "git branch feature"},
			} {
				request := activeWorker(testCase.command)
				request.Provider = provider
				result := Classify(request)
				wantDecision := DecisionDeny
				if testCase.code == "" {
					wantDecision = DecisionDefer
				}
				if result.Decision != wantDecision ||
					(testCase.code == "" && result.Diagnostic != nil) ||
					(testCase.code != "" && (result.Diagnostic == nil || result.Diagnostic.Code != testCase.code)) {
					t.Errorf("%q: decision=%q diagnostic=%#v, want %s/%q", testCase.command, result.Decision, result.Diagnostic, wantDecision, testCase.code)
				}
				if len(result.Capabilities) != 0 {
					t.Errorf("%q: capabilities=%v, want none", testCase.command, result.Capabilities)
				}
			}

			contextPlan := activeWorker("git --git-dir=.git status --short")
			contextPlan.Provider = provider
			if result := Classify(contextPlan); result.Decision != DecisionDefer || result.Diagnostic != nil {
				t.Errorf("Git context spelling: decision=%q diagnostic=%#v, want defer without diagnostic", result.Decision, result.Diagnostic)
			}

			for _, command := range []string{"env git status", "git status && printf after"} {
				request := activeWorker(command)
				request.Provider = provider
				result := Classify(request)
				if len(result.Capabilities) != 0 {
					t.Errorf("%q: capabilities=%v, want none", command, result.Capabilities)
				}
			}
		})
	}
}

// TestWorkerGitIndexOperationsDeferToResolvedTarget verifies that an active
// worker's index transition reaches the provider's repository/effect resolver
// instead of being denied from its Git verb alone.
//
// Example: git add README.md and git add -- hooks.json both defer so the
// provider can distinguish a local path from a foreign or whole-worktree one.
func TestWorkerGitIndexOperationsDeferToResolvedTarget(t *testing.T) {
	t.Parallel()

	const repository = "/workspace"
	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()

			for _, testCase := range []struct {
				name    string
				command string
			}{
				{name: "separator explicit path", command: "git add -- hooks.json"},
				{name: "plain explicit path", command: "git add README.md"},
				{name: "multiple explicit paths", command: "git add hooks.json README.md"},
				{name: "same repository context", command: "git -C " + repository + " add -- hooks.json"},
				{name: "foreign repository context", command: "git -C /workspace-foreign add -- file.txt"},
				{name: "whole worktree selector", command: "git add ."},
				{name: "working tree reset", command: "git reset --hard"},
			} {
				testCase := testCase
				t.Run(testCase.name, func(t *testing.T) {
					t.Parallel()

					result := Classify(Request{
						Provider:      provider,
						Role:          RoleWorker,
						CWD:           repository,
						Marker:        MarkerActive,
						ActiveSession: "test-session",
						Command:       testCase.command,
						ApprovedRoots: []string{repository},
					})
					if result.Decision != DecisionDefer || result.Diagnostic != nil {
						t.Fatalf("%q: decision=%q diagnostic=%#v, want target-aware defer without diagnostic", testCase.command, result.Decision, result.Diagnostic)
					}
					if len(result.Capabilities) != 0 || result.DeferredRoute != "" {
						t.Fatalf("%q: capabilities=%v deferred_route=%q, want no planner fast path", testCase.command, result.Capabilities, result.DeferredRoute)
					}
				})
			}
		})
	}
}

// TestRawGitPlansHaveNoCapabilityIncludingExecutionEscapes keeps all Git
// inspection and execution-adjacent forms off a generic planner fast path.
//
// Example: a direct archive and grep pager escape both remain capability-free.
func TestRawGitPlansHaveNoCapabilityIncludingExecutionEscapes(t *testing.T) {
	t.Parallel()

	controls := []string{
		"git --no-pager rev-parse HEAD",
		"git archive HEAD",
	}
	barePlans := []string{
		"git status --short",
		"git log -1",
		"git diff --check",
		"git grep -n needle -- hooks",
	}
	escapes := []string{
		"git --no-pager status --short",
		"git --no-pager log -1",
		"git --no-pager diff --check",
		"git --no-pager grep -n needle -- hooks",
		"git --no-pager ls-files",
		"git --no-pager branch --all --contains HEAD",
		"git grep --open-files-in-pager=/bin/sh needle",
		"git grep --open-files-in-pager /bin/sh needle",
		"git grep -O /bin/sh needle",
		"git cat-file --filters HEAD:README",
		"git remote show origin",
		"git log --show-signature -1",
		"git --paginate log -1",
		"git diff --check --no-index /etc/passwd /etc/hosts",
	}

	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()

			for _, role := range []Role{RoleCoordinator, RoleWorker} {
				for _, command := range append(append([]string(nil), controls...), append(barePlans, escapes...)...) {
					request := activeWorker(command)
					request.Provider = provider
					request.Role = role
					result := Classify(request)
					if len(result.Capabilities) != 0 {
						t.Errorf("role=%q %q: capabilities=%v, want no repository-default fast path", role, command, result.Capabilities)
					}
				}
			}
		})
	}
}

// TestGitFsckLostFoundDefersWithoutReadOnlyCapability verifies the option
// that writes dangling objects never receives the Git read-only fast path.
//
// Example: env FOO=bar git fsck --lost-found defers to the provider adapter.
func TestGitFsckLostFoundDefersWithoutReadOnlyCapability(t *testing.T) {
	t.Parallel()

	writerCommands := []string{
		"git fsck --lost-found",
		"env git fsck --lost-found",
		"env FOO=bar git fsck --lost-found",
		"git --no-pager fsck --lost-found",
	}
	readOnlyCommands := []string{
		"git fsck",
		"git --no-pager fsck",
		"git fsck --lost-found=ignored",
	}
	environmentReadOnlyCommands := []string{
		"env git fsck",
		"env FOO=bar git fsck",
	}

	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()

			for _, role := range []Role{RoleCoordinator, RoleWorker} {
				for _, command := range writerCommands {
					request := activeWorker(command)
					request.Provider = provider
					request.Role = role
					result := Classify(request)
					wantDecision := DecisionDefer
					wantDiagnostic := false
					if result.Decision != wantDecision || (result.Diagnostic != nil) != wantDiagnostic {
						t.Errorf("role=%q %q: decision=%q diagnostic=%#v, want %q diagnostic=%t", role, command, result.Decision, result.Diagnostic, wantDecision, wantDiagnostic)
					}
					if len(result.Capabilities) != 0 {
						t.Errorf("role=%q %q: capabilities=%v, want none", role, command, result.Capabilities)
					}
				}

				for _, command := range readOnlyCommands {
					request := activeWorker(command)
					request.Provider = provider
					request.Role = role
					result := Classify(request)
					if result.Decision != DecisionDefer || result.Diagnostic != nil {
						t.Errorf("role=%q %q: decision=%q diagnostic=%#v, want defer without diagnostic", role, command, result.Decision, result.Diagnostic)
					}
					if len(result.Capabilities) != 0 {
						t.Errorf("role=%q %q: capabilities=%v, want none", role, command, result.Capabilities)
					}
				}

				for _, command := range environmentReadOnlyCommands {
					request := activeWorker(command)
					request.Provider = provider
					request.Role = role
					result := Classify(request)
					if result.Decision != DecisionDefer || result.Diagnostic != nil {
						t.Errorf("role=%q %q: decision=%q diagnostic=%#v, want defer without diagnostic", role, command, result.Decision, result.Diagnostic)
					}
					if len(result.Capabilities) != 0 {
						t.Errorf("role=%q %q: capabilities=%v, want none", role, command, result.Capabilities)
					}
				}
			}
		})
	}
}

func TestGitFsckLostFoundNativeProviderDefer(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		command        string
		provider       Provider
		role           Role
		marker         Marker
		wantDecision   DecisionKind
		wantDiagnostic bool
		wantRoute      DeferredRoute
	}{
		{name: "raw Git", command: "git fsck --lost-found", provider: ProviderOmp, role: RoleWorker, marker: MarkerActive, wantDecision: DecisionDefer},
		{name: "path-qualified Git", command: "env /usr/bin/git fsck --lost-found", provider: ProviderOmp, role: RoleWorker, marker: MarkerActive, wantDecision: DecisionDefer},
		{name: "wrapped Git", command: "env command git fsck --lost-found", provider: ProviderOmp, role: RoleWorker, marker: MarkerActive, wantDecision: DecisionDefer},
		{name: "quoted environment assignment", command: `env "FOO=bar" git fsck --lost-found`, provider: ProviderOmp, role: RoleWorker, marker: MarkerActive, wantDecision: DecisionDefer},
		{name: "quoted wrapped Git", command: `env "command" git fsck --lost-found`, provider: ProviderOmp, role: RoleWorker, marker: MarkerActive, wantDecision: DecisionDefer},
		{name: "Git global option", command: "env git --no-pager fsck --lost-found", provider: ProviderOmp, role: RoleWorker, marker: MarkerActive, wantDecision: DecisionDefer},
		{name: "quoted Git global option", command: `env git "--no-pager" fsck --lost-found`, provider: ProviderOmp, role: RoleWorker, marker: MarkerActive, wantDecision: DecisionDefer},
		{name: "Git context option", command: "env git -C . fsck --lost-found", provider: ProviderOmp, role: RoleWorker, marker: MarkerActive, wantDecision: DecisionDefer},
		{name: "operator plan", command: "env git fsck --lost-found && printf after", provider: ProviderOmp, role: RoleWorker, marker: MarkerActive, wantDecision: DecisionDefer},
		{name: "quoted route", command: `env git fsck '--lost-found'`, provider: ProviderOmp, role: RoleWorker, marker: MarkerActive, wantDecision: DecisionDefer},
		{name: "bare fsck", command: "git fsck", provider: ProviderOmp, role: RoleWorker, marker: MarkerActive, wantDecision: DecisionDefer},
		{name: "equals option", command: "git fsck --lost-found=ignored", provider: ProviderOmp, role: RoleWorker, marker: MarkerActive, wantDecision: DecisionDefer},
		{name: "quoted equals option", command: `git fsck '--lost-found=ignored'`, provider: ProviderOmp, role: RoleWorker, marker: MarkerActive, wantDecision: DecisionDefer},
		{name: "coordinator raw Git", command: "git fsck --lost-found", provider: ProviderOmp, role: RoleCoordinator, marker: MarkerActive, wantDecision: DecisionDefer},
		{name: "inactive worker raw Git", command: "git fsck --lost-found", provider: ProviderOmp, role: RoleWorker, marker: MarkerInactive, wantDecision: DecisionDefer},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			request := activeWorker(testCase.command)
			request.Provider = testCase.provider
			request.Role = testCase.role
			request.Marker = testCase.marker
			if testCase.marker == MarkerInactive {
				request.ActiveSession = ""
			}
			result := Classify(request)
			if result.Decision != testCase.wantDecision {
				t.Fatalf("decision=%q, want %q (diagnostic=%#v)", result.Decision, testCase.wantDecision, result.Diagnostic)
			}
			if (result.Diagnostic != nil) != testCase.wantDiagnostic {
				t.Fatalf("diagnostic=%#v, want present=%t", result.Diagnostic, testCase.wantDiagnostic)
			}
			if result.DeferredRoute != testCase.wantRoute {
				t.Errorf("deferred route=%q, want %q", result.DeferredRoute, testCase.wantRoute)
			}
		})
	}
}

func TestGitFsckLostFoundGitContextDefersToNativeProvider(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		command string
	}{
		{name: "direct git-dir attached", command: "git --git-dir=.git fsck --lost-found"},
		{name: "direct work-tree attached", command: "git --work-tree=/tmp fsck --lost-found"},
		{name: "direct namespace attached", command: "git --namespace=foo fsck --lost-found"},
		{name: "direct exec-path attached", command: "git --exec-path=/tmp fsck --lost-found"},
		{name: "direct config-env attached", command: "git --config-env=GIT_CONFIG_COUNT=0 fsck --lost-found"},
		{name: "env git-dir attached", command: "env git --git-dir=.git fsck --lost-found"},
		{name: "env work-tree attached", command: "env git --work-tree=/tmp fsck --lost-found"},
		{name: "env namespace attached", command: "env git --namespace=foo fsck --lost-found"},
		{name: "env exec-path attached", command: "env git --exec-path=/tmp fsck --lost-found"},
		{name: "env config-env attached", command: "env git --config-env=GIT_CONFIG_COUNT=0 fsck --lost-found"},
		{name: "direct git-dir split", command: "git --git-dir .git fsck --lost-found"},
		{name: "direct work-tree split", command: "git --work-tree /tmp fsck --lost-found"},
		{name: "direct namespace split", command: "git --namespace foo fsck --lost-found"},
		{name: "direct exec-path split", command: "git --exec-path /tmp fsck --lost-found"},
		{name: "direct config-env split", command: "git --config-env GIT_CONFIG_COUNT fsck --lost-found"},
		{name: "env git-dir split", command: "env git --git-dir .git fsck --lost-found"},
		{name: "env work-tree split", command: "env git --work-tree /tmp fsck --lost-found"},
		{name: "env namespace split", command: "env git --namespace foo fsck --lost-found"},
		{name: "env exec-path split", command: "env git --exec-path /tmp fsck --lost-found"},
		{name: "env config-env split", command: "env git --config-env GIT_CONFIG_COUNT fsck --lost-found"},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			result := Classify(activeWorker(testCase.command))
			if result.Decision != DecisionDefer || result.Diagnostic != nil {
				t.Fatalf("decision=%q diagnostic=%#v, want native Git provider defer", result.Decision, result.Diagnostic)
			}
		})
	}
}

func TestGitFsckLostFoundDeferredRoute(t *testing.T) {
	t.Parallel()

	positiveCommands := []string{
		"env git fsck --lost-found",
		"env git fsck '--lost-found'",
		`"env" git fsck --lost-found`,
		`env "git" fsck --lost-found`,
		`env git "fsck" --lost-found`,
		"env FOO=bar BAR=baz git fsck --lost-found",
		"env -i git fsck --lost-found",
		"env -u FOO git fsck --lost-found",
		"env -C . git fsck --lost-found",
		"env -- git fsck --full --lost-found --no-progress",
	}
	for _, command := range positiveCommands {
		result := Classify(activeWorker(command))
		if result.Decision != DecisionDefer || result.Diagnostic != nil {
			t.Errorf("positive %q: decision=%q diagnostic=%#v, want defer without diagnostic", command, result.Decision, result.Diagnostic)
		}
		if result.DeferredRoute != "" {
			t.Errorf("positive %q: deferred route=%q, want %q", command, result.DeferredRoute, DeferredRoute(""))
		}
	}

	negativeCases := []struct {
		name    string
		request Request
	}{
		{name: "raw Git", request: activeWorker("git fsck --lost-found")},
		{name: "path-qualified Git", request: activeWorker("env /usr/bin/git fsck --lost-found")},
		{name: "wrapped Git", request: activeWorker("env command git fsck --lost-found")},
		{name: "diagnostic", request: activeWorker("env -S git fsck --lost-found")},
		{name: "Git global option", request: activeWorker("env git --no-pager fsck --lost-found")},
		{name: "equals option", request: activeWorker("env git fsck --lost-found=ignored")},
		{name: "operator plan", request: activeWorker("env git fsck --lost-found && printf after")},
		{name: "coordinator", request: func() Request {
			request := activeWorker("env git fsck --lost-found")
			request.Role = RoleCoordinator
			return request
		}()},
		{name: "coordinator direct", request: func() Request {
			request := activeWorker("git fsck --lost-found")
			request.Role = RoleCoordinator
			return request
		}()},
		{name: "inactive marker", request: func() Request {
			request := activeWorker("env git fsck --lost-found")
			request.Marker = MarkerInactive
			request.ActiveSession = ""
			return request
		}()},
	}
	for _, testCase := range negativeCases {
		result := Classify(testCase.request)
		if result.DeferredRoute != "" {
			t.Errorf("negative %s %q: deferred route=%q, want omitted", testCase.name, testCase.request.Command, result.DeferredRoute)
		}
	}

}

func TestDeferredRouteJSONEncoding(t *testing.T) {
	t.Parallel()

	for _, command := range []string{
		"env git fsck --lost-found",
		"env git fsck '--lost-found'",
		`"env" git fsck --lost-found`,
		`env "git" fsck --lost-found`,
		`env git "fsck" --lost-found`,
	} {
		encoded, err := json.Marshal(Classify(activeWorker(command)))
		if err != nil {
			t.Fatalf("marshal positive result %q: %v", command, err)
		}
		if containsBytes(encoded, []byte(`"deferred_route"`)) || !containsBytes(encoded, []byte(`"decision":"defer"`)) {
			t.Fatalf("positive result %q want provider defer without a special route: %s", command, encoded)
		}
	}

	for _, request := range []Request{
		activeWorker("git fsck --lost-found"),
		func() Request {
			request := activeWorker("git fsck --lost-found")
			request.Role = RoleCoordinator
			return request
		}(),
		activeWorker("env command git fsck --lost-found"),
		activeWorker("env git --no-pager fsck --lost-found"),
		activeWorker("env git fsck --lost-found=ignored"),
		func() Request {
			request := activeWorker("env git fsck --lost-found")
			request.Role = RoleCoordinator
			return request
		}(),
		func() Request {
			request := activeWorker("env git fsck --lost-found")
			request.Marker = MarkerInactive
			request.ActiveSession = ""
			return request
		}(),
	} {
		encoded, err := json.Marshal(Classify(request))
		if err != nil {
			t.Fatalf("marshal %q: %v", request.Command, err)
		}
		if containsBytes(encoded, []byte(`"deferred_route"`)) {
			t.Errorf("negative result %q unexpectedly contains deferred route: %s", request.Command, encoded)
		}
	}
}

func TestStatMetadataGrammarIsProviderParity(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		command   string
		decision  DecisionKind
		code      DiagnosticCode
		predicate string
	}{
		{name: "access time", command: "stat -c '%x' hooks/validate-bash.sh", decision: DecisionAllow},
		{name: "size and name", command: "stat --format='%s %n' hooks/validate-bash.sh", decision: DecisionAllow},
		{name: "unknown directive remains ordinary", command: "stat -c '%Q' hooks/validate-bash.sh", decision: DecisionAllow},
		{name: "dynamic format remains ordinary", command: "stat -c '$(printf %s)' hooks/validate-bash.sh", decision: DecisionAllow},
		{name: "context option remains ordinary", command: "stat --printf='%s' hooks/validate-bash.sh", decision: DecisionAllow},
		{name: "dynamic path remains ordinary", command: "stat -c '%s' '$HOME/tmp/file'", decision: DecisionAllow},
	}

	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()
			for _, testCase := range testCases {
				testCase := testCase
				t.Run(testCase.name, func(t *testing.T) {
					t.Parallel()
					request := activeWorker(testCase.command)
					request.Provider = provider
					result := Classify(request)
					if result.Decision != testCase.decision {
						t.Fatalf("%q: decision=%q, want %q; diagnostic=%#v", testCase.command, result.Decision, testCase.decision, result.Diagnostic)
					}
					if testCase.code == "" {
						if result.Diagnostic != nil {
							t.Fatalf("%q: unexpected diagnostic=%#v", testCase.command, result.Diagnostic)
						}
						return
					}
					if result.Diagnostic == nil || result.Diagnostic.Code != testCase.code || result.Diagnostic.Predicate != testCase.predicate {
						t.Fatalf("%q: diagnostic=%#v, want code=%q predicate=%q", testCase.command, result.Diagnostic, testCase.code, testCase.predicate)
					}
				})
			}
		})
	}
}

// TestFileInspectionGrammar keeps file option spelling ordinary. Any concrete
// target-aware writer handling belongs to the target layer, not this grammar.
func TestFileInspectionGrammar(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		command   string
		decision  DecisionKind
		code      DiagnosticCode
		predicate string
	}{
		{name: "plain inspection", command: "file hooks/validate-bash.sh", decision: DecisionAllow},
		{name: "brief inspection", command: "file -b hooks/validate-bash.sh", decision: DecisionAllow},
		{name: "literal magic file", command: "file -m hooks/validate-bash.sh hooks/validate-bash.sh", decision: DecisionAllow},
		{name: "compile short remains ordinary", command: "file -C -m hooks/validate-bash.sh", decision: DecisionAllow},
		{name: "compile long remains ordinary", command: "file --compile -m hooks/validate-bash.sh", decision: DecisionAllow},
		{name: "compile with attached short magic remains ordinary", command: "file -C -mhooks/validate-bash.sh", decision: DecisionAllow},
		{name: "compile with assigned long magic remains ordinary", command: "file --compile --magic-file=hooks/validate-bash.sh", decision: DecisionAllow},
		{name: "attached magic without compile remains ordinary", command: "file -mhooks/validate-bash.sh hooks/validate-bash.sh", decision: DecisionAllow},
		{name: "unknown option remains ordinary", command: "file --extension hooks/validate-bash.sh", decision: DecisionAllow},
	}

	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()
			for _, testCase := range testCases {
				testCase := testCase
				t.Run(testCase.name, func(t *testing.T) {
					t.Parallel()
					request := activeWorker(testCase.command)
					request.Provider = provider
					result := Classify(request)
					if result.Decision != testCase.decision {
						t.Fatalf("%q: decision=%q, want %q; diagnostic=%#v", testCase.command, result.Decision, testCase.decision, result.Diagnostic)
					}
					if testCase.code == "" {
						if result.Diagnostic != nil {
							t.Fatalf("%q: unexpected diagnostic=%#v", testCase.command, result.Diagnostic)
						}
						return
					}
					if result.Diagnostic == nil || result.Diagnostic.Code != testCase.code || result.Diagnostic.Predicate != testCase.predicate {
						t.Fatalf("%q: diagnostic=%#v, want code=%q predicate=%q", testCase.command, result.Diagnostic, testCase.code, testCase.predicate)
					}
				})
			}
		})
	}
}

// TestUniqInspectionGrammar keeps uniq argv shape ordinary. A concrete output
// target remains available to target-aware checks where applicable.
func TestUniqInspectionGrammar(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		command   string
		decision  DecisionKind
		code      DiagnosticCode
		predicate string
	}{
		{command: "uniq", decision: DecisionAllow},
		{command: "uniq hooks/validate-bash.sh", decision: DecisionAllow},
		{command: "uniq hooks/validate-bash.sh hooks/stop-gate.sh", decision: DecisionAllow},
	} {
		testCase := testCase
		t.Run(testCase.command, func(t *testing.T) {
			t.Parallel()
			result := Classify(activeWorker(testCase.command))
			if result.Decision != testCase.decision {
				t.Fatalf("decision=%q, want %q; diagnostic=%#v", result.Decision, testCase.decision, result.Diagnostic)
			}
			if testCase.code == "" {
				return
			}
			if result.Diagnostic == nil || result.Diagnostic.Code != testCase.code || result.Diagnostic.Predicate != testCase.predicate {
				t.Fatalf("diagnostic=%#v, want code=%q predicate=%q", result.Diagnostic, testCase.code, testCase.predicate)
			}
		})
	}
}

func TestInstalledBinaryStatMetadataGrammar(t *testing.T) {
	t.Parallel()

	binary, err := filepath.Abs("eci-command-plan")
	if err != nil {
		t.Fatalf("resolve installed binary: %v", err)
	}
	testCases := []struct {
		name      string
		command   string
		status    int
		code      DiagnosticCode
		predicate string
	}{
		{name: "access time", command: "stat -c '%x' hooks/validate-bash.sh", status: StatusAllow},
		{name: "name and size", command: "stat -c '%n %s' hooks/validate-bash.sh", status: StatusAllow},
		{name: "unknown directive", command: "stat -c '%Q' hooks/validate-bash.sh", status: StatusAllow},
		{name: "dynamic format", command: "stat -c '$(printf %s)' hooks/validate-bash.sh", status: StatusAllow},
		{name: "context option", command: "stat --printf='%s' hooks/validate-bash.sh", status: StatusAllow},
	}
	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		for _, testCase := range testCases {
			t.Run(string(provider)+"/"+testCase.name, func(t *testing.T) {
				request := activeWorker(testCase.command)
				request.Provider = provider
				input, err := json.Marshal(request)
				if err != nil {
					t.Fatalf("marshal %q request: %v", request.Command, err)
				}
				var output bytes.Buffer
				status := runBinary(t, binary, input, &output)
				var result Result
				if err := json.Unmarshal(output.Bytes(), &result); err != nil {
					t.Fatalf("decode %q result: %v; output=%s", request.Command, err, output.String())
				}
				if status != testCase.status {
					t.Fatalf("%q: status=%d output=%s, want %d", request.Command, status, output.String(), testCase.status)
				}
				if testCase.code == "" {
					if result.Diagnostic != nil {
						t.Fatalf("%q: unexpected diagnostic=%#v", request.Command, result.Diagnostic)
					}
					return
				}
				if result.Diagnostic == nil || result.Diagnostic.Code != testCase.code || result.Diagnostic.Predicate != testCase.predicate {
					t.Fatalf("%q: diagnostic=%#v, want code=%q predicate=%q", request.Command, result.Diagnostic, testCase.code, testCase.predicate)
				}
			})
		}
	}
}

// TestInstalledBinaryRejectsRawGitFastCapabilities verifies the shipped
// planner leaves raw Git calls on their provider-owned route.
func TestInstalledBinaryRejectsRawGitFastCapabilities(t *testing.T) {
	binary, err := filepath.Abs("eci-command-plan")
	if err != nil {
		t.Fatalf("resolve installed binary: %v", err)
	}
	for _, command := range []string{
		"git --no-pager rev-parse HEAD",
		"git archive HEAD",
		"/tmp/attacker/git status",
	} {
		request := activeWorker(command)
		input, err := json.Marshal(request)
		if err != nil {
			t.Fatalf("marshal %q request: %v", command, err)
		}
		var stdout bytes.Buffer
		status := runBinary(t, binary, input, &stdout)
		if status != StatusDefer {
			t.Fatalf("%q: status=%d output=%s, want %d", command, status, stdout.String(), StatusDefer)
		}
		var result Result
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatalf("decode %q result: %v; output=%s", command, err, stdout.String())
		}
		if len(result.Capabilities) != 0 {
			t.Fatalf("%q: capabilities=%v, want none", command, result.Capabilities)
		}
		if result.Decision != DecisionDefer || result.Diagnostic != nil {
			t.Fatalf("%q: decision=%q diagnostic=%#v, want defer without diagnostic", command, result.Decision, result.Diagnostic)
		}
	}
}

// TestNamedRuntimeFormsRemainOrdinary verifies that runtime input selectors
// are not treated as permission boundaries.
//
// Example: both node scripts/check.mjs and node --eval=code remain ordinary
// commands until a concrete target-specific boundary applies.
func TestNamedRuntimeFormsRemainOrdinary(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		command string
		deny    bool
	}{
		{name: "bare python", command: "python3", deny: true},
		{name: "python stdin dash", command: "python3 -", deny: true},
		{name: "python stdin switch", command: "python3 --stdin", deny: true},
		{name: "python inline code", command: "python3 -c print", deny: true},
		{name: "python inline code attached", command: "python3 -cprint", deny: true},
		{name: "versioned python bare", command: "python3.11", deny: true},
		{name: "versioned python inline code", command: "python3.11 -cprint", deny: true},
		{name: "python module", command: "python3 -m pytest"},
		{name: "python script", command: "python3 tools/check.py"},
		{name: "python script trailing argv", command: "python3 tools/check.py -c"},
		{name: "python warning option", command: "python3 -W ignore", deny: true},
		{name: "python implementation option", command: "python3 -X dev", deny: true},
		{name: "python interactive option", command: "python3 -i tools/check.py", deny: true},
		{name: "bare node", command: "node", deny: true},
		{name: "bare nodejs", command: "nodejs", deny: true},
		{name: "node eval", command: "node -e code", deny: true},
		{name: "node short eval attached", command: "node -e=code", deny: true},
		{name: "node long eval attached", command: "node --eval=code", deny: true},
		{name: "node print", command: "node -p code", deny: true},
		{name: "node short print attached", command: "node -p=code", deny: true},
		{name: "node long print attached", command: "node --print=code", deny: true},
		{name: "node script", command: "node scripts/check.mjs"},
		{name: "nodejs script", command: "nodejs scripts/check.mjs"},
		{name: "nodejs eval", command: "nodejs --eval=code", deny: true},
		{name: "node loader option", command: "node --loader loader.mjs", deny: true},
		{name: "bare perl", command: "perl", deny: true},
		{name: "perl eval", command: "perl -e code", deny: true},
		{name: "perl eval attached", command: "perl -ecode", deny: true},
		{name: "perl extended eval", command: "perl -E code", deny: true},
		{name: "perl extended eval attached", command: "perl -Ecode", deny: true},
		{name: "perl script", command: "perl tools/check.pl"},
		{name: "versioned perl eval", command: "perl5 -ecode", deny: true},
		{name: "perl include option", command: "perl -I /tmp", deny: true},
		{name: "bare ruby", command: "ruby", deny: true},
		{name: "ruby eval", command: "ruby -e code", deny: true},
		{name: "ruby eval attached", command: "ruby -ecode", deny: true},
		{name: "ruby script", command: "ruby tools/check.rb"},
		{name: "versioned ruby eval", command: "ruby3.3 -ecode", deny: true},
		{name: "ruby include option", command: "ruby -I lib", deny: true},
		{name: "bare php", command: "php", deny: true},
		{name: "php run", command: "php -r code", deny: true},
		{name: "php short run attached", command: "php -recho", deny: true},
		{name: "php run attached", command: "php --run=code", deny: true},
		{name: "php process begin", command: "php -B code", deny: true},
		{name: "php short process begin attached", command: "php -Becho", deny: true},
		{name: "php process begin attached", command: "php --process-begin=code", deny: true},
		{name: "php process code", command: "php -R code", deny: true},
		{name: "php short process code attached", command: "php -Recho", deny: true},
		{name: "php process code attached", command: "php --process-code=code", deny: true},
		{name: "php process end", command: "php -E code", deny: true},
		{name: "php short process end attached", command: "php -Eecho", deny: true},
		{name: "php process end attached", command: "php --process-end=code", deny: true},
		{name: "php interactive", command: "php -a", deny: true},
		{name: "php setting option", command: "php -d memory_limit=1G", deny: true},
		{name: "php script", command: "php tools/check.php"},
		{name: "php file", command: "php -f tools/check.php"},
		{name: "php file uppercase", command: "php -F tools/check.php"},
		{name: "versioned php run", command: "php8.2 -r=code", deny: true},
		{name: "ordinary unknown interpreter-like tool", command: "interpreter-tool --module test-suite --flag value"},
		{name: "ordinary python-prefixed helper", command: "python-tool --module test-suite --flag value"},
		{name: "unknown interpreter inline code", command: "interpreter-tool -c 'dynamic payload'", deny: true},
		{name: "unknown interpreter short eval", command: "interpreter-tool -ecode", deny: true},
		{name: "unknown interpreter stdin", command: "interpreter-tool -", deny: true},
		{name: "unknown runtime long eval", command: "novel-runtime --eval=code", deny: true},
		{name: "unknown tool long eval", command: "mystery-tool --eval=code", deny: true},
		{name: "unknown script exec", command: "script-runner --execute=code", deny: true},
		{name: "unknown repl command", command: "new-repl --command code", deny: true},
		{name: "ordinary ripgrep short count", command: "rg -c needle"},
		{name: "ordinary compiler short compile", command: "g++ -c source.cpp"},
	}

	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		for _, testCase := range testCases {
			testCase := testCase
			t.Run(string(provider)+"/"+testCase.name, func(t *testing.T) {
				t.Parallel()

				request := activeWorker(testCase.command)
				request.Provider = provider
				result := Classify(request)
				if result.Decision == DecisionDeny {
					t.Fatalf("%q: decision=%q diagnostic=%#v, want ordinary admission", testCase.command, result.Decision, result.Diagnostic)
				}
			})
		}
	}
}

func TestCoordinatorApprovedGitReadContextsDeferToProvider(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve home directory: %v", err)
	}
	approvedRoot := filepath.Join(home, "tmp", "eci-approved-repository")
	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()
			for _, command := range []string{
				"git -C " + approvedRoot + " status --short ../outside",
				"git -C " + approvedRoot + " diff --stat /etc/passwd",
				"git -C " + approvedRoot + " diff -- ../outside",
			} {
				request := Request{
					Provider:      provider,
					Role:          RoleCoordinator,
					CWD:           approvedRoot,
					Marker:        MarkerActive,
					ActiveSession: "test-session",
					Command:       command,
					ApprovedRoots: []string{approvedRoot},
				}
				result := Classify(request)
				if result.Decision != DecisionDefer || result.Diagnostic != nil {
					t.Errorf("%q: decision=%q diagnostic=%#v, want defer", command, result.Decision, result.Diagnostic)
				}
			}

			foreign := Request{
				Provider:      provider,
				Role:          RoleCoordinator,
				CWD:           approvedRoot,
				Marker:        MarkerActive,
				ActiveSession: "test-session",
				Command:       "git -C " + filepath.Join(home, "tmp", "foreign-repository") + " status --short ../outside",
				ApprovedRoots: []string{approvedRoot},
			}
			if result := Classify(foreign); result.Decision != DecisionDefer || result.Diagnostic != nil {
				t.Errorf("foreign repository context: decision=%q diagnostic=%#v, want defer", result.Decision, result.Diagnostic)
			}

			worker := Request{
				Provider:      provider,
				Role:          RoleWorker,
				CWD:           approvedRoot,
				Marker:        MarkerActive,
				ActiveSession: "test-session",
				Command:       "git -C " + approvedRoot + " status --short ../outside",
				ApprovedRoots: []string{approvedRoot},
			}
			if result := Classify(worker); result.Decision != DecisionDefer || result.Diagnostic != nil {
				t.Errorf("worker read context: decision=%q diagnostic=%#v, want defer", result.Decision, result.Diagnostic)
			}
		})
	}
}

// TestActiveControlFileIndexIgnoresOrdinarySessionRecords excludes ordinary records.
//
// Example: many ordinary files leave only the active marker in the inode index.
func TestActiveControlFileIndexIgnoresOrdinarySessionRecords(t *testing.T) {
	t.Parallel()

	temporaryRoot := t.TempDir()
	sessionDir := filepath.Join(temporaryRoot, "proof", "session")
	{
		err := os.MkdirAll(sessionDir, 0o700)
		require.NoErrorf(t, err, "create session directory: %v", err)
	}
	marker := filepath.Join(sessionDir, "eci_active")
	{
		err := os.WriteFile(marker, []byte("active\n"), 0o600)
		require.NoErrorf(t, err, "write marker: %v", err)
	}
	for entryIndex := 0; entryIndex <= manySessionFixtureEntries; entryIndex++ {
		path := filepath.Join(sessionDir, "ordinary-entry-"+strconv.Itoa(entryIndex))
		{
			err := os.WriteFile(path, []byte("ordinary\n"), 0o600)
			require.NoErrorf(t, err, "write session entry %d: %v", entryIndex, err)
		}
	}

	index := activeControlFileIndex([]string{marker})
	require.Falsef(t, len(index.files) != 1, "ordinary session records retained %d control entries; want marker only", len(index.files))

	result := Classify(Request{
		Provider:      ProviderOmp,
		Role:          RoleWorker,
		CWD:           temporaryRoot,
		Marker:        MarkerActive,
		ActiveSession: "session",
		Command:       "cat ordinary-entry-0",
		ActiveMarkers: []string{marker},
	})
	require.Falsef(t, result.Decision != DecisionAllow || result.Diagnostic != nil, "ordinary classification: decision=%q diagnostic=%#v, want allow without diagnostic", result.Decision, result.Diagnostic)
}

// TestWorkerReadOnlyControlDiscoveryRemainsOrdinary verifies that workers can
// inspect active ECI state without ownership metadata becoming a permission
// boundary.
//
// Example: `cat eci_active`, `sed -n '1p' eci_active`, and a copied
// `eci-active status` remain ordinary read-only work, while concrete writes
// continue through their target-specific checks.
func TestWorkerReadOnlyControlDiscoveryRemainsOrdinary(t *testing.T) {
	t.Parallel()

	temporaryRoot := t.TempDir()
	sessionDir := filepath.Join(temporaryRoot, "proof", "session")
	{
		err := os.MkdirAll(sessionDir, 0o700)
		require.NoErrorf(t, err, "create session directory: %v", err)
	}
	marker := filepath.Join(sessionDir, "eci_active")
	{
		err := os.WriteFile(marker, []byte("active\n"), 0o600)
		require.NoErrorf(t, err, "write marker: %v", err)
	}

	source := filepath.Join(providerHome(ProviderOmp), "bin", "eci-active")
	contents, err := os.ReadFile(source)
	require.NoErrorf(t, err, "read lifecycle source: %v", err)
	copyPath := filepath.Join(temporaryRoot, "eci-active-copy")
	{
		err := os.WriteFile(copyPath, contents, 0o755)
		require.NoErrorf(t, err, "write lifecycle copy: %v", err)
	}

	for _, testCase := range []struct {
		name     string
		command  string
		decision DecisionKind
	}{
		{name: "marker cat", command: "cat " + marker, decision: DecisionAllow},
		{name: "marker sed", command: "sed -n '1p' " + marker, decision: DecisionAllow},
		{name: "lifecycle status", command: "eci-active status", decision: DecisionDefer},
		{name: "lifecycle help", command: "eci-active --help", decision: DecisionDefer},
		{name: "copied lifecycle status", command: copyPath + " status", decision: DecisionAllow},
		{name: "copied lifecycle help", command: copyPath + " --help", decision: DecisionAllow},
	} {
		testCase := testCase
		t.Run(testCase.name,
			// Verify harmless control discovery retains its original route.
			//
			// Example: a copied lifecycle status command stays an ordinary read.
			func(t *testing.T) {
				request := Request{
					Provider:      ProviderOmp,
					Role:          RoleWorker,
					CWD:           temporaryRoot,
					Marker:        MarkerActive,
					ActiveSession: "session",
					Command:       testCase.command,
					ActiveMarkers: []string{marker},
				}
				result := Classify(request)
				require.Falsef(t, result.Decision != testCase.decision || result.Diagnostic != nil, "decision=%q diagnostic=%#v, want %q without diagnostic", result.Decision, result.Diagnostic, testCase.decision)
			})
	}

	overflowSessionDir := filepath.Join(temporaryRoot, "proof", "overflow-session")
	{
		err := os.MkdirAll(overflowSessionDir, 0o700)
		require.NoErrorf(t, err, "create overflow session directory: %v", err)
	}
	overflowMarker := filepath.Join(overflowSessionDir, "eci_active")
	{
		err := os.WriteFile(overflowMarker, []byte("active\n"), 0o600)
		require.NoErrorf(t, err, "write overflow marker: %v", err)
	}
	overflowReadPath := filepath.Join(overflowSessionDir, "ordinary-entry")
	{
		err := os.WriteFile(overflowReadPath, []byte("ordinary\n"), 0o600)
		require.NoErrorf(t, err, "write overflow ordinary entry: %v", err)
	}
	for controlIndex := 0; controlIndex < manySessionFixtureEntries; controlIndex++ {
		path := filepath.Join(overflowSessionDir, "eci-required-critics.json.control-"+strconv.Itoa(controlIndex))
		{
			err := os.WriteFile(path, []byte("control\n"), 0o600)
			require.NoErrorf(t, err, "write overflow control entry %d: %v", controlIndex, err)
		}
	}

	overflowResult := Classify(Request{
		Provider:      ProviderOmp,
		Role:          RoleWorker,
		CWD:           temporaryRoot,
		Marker:        MarkerActive,
		ActiveSession: "overflow-session",
		Command:       "cat " + overflowReadPath,
		ActiveMarkers: []string{overflowMarker},
	})
	require.Falsef(t, overflowResult.Decision != DecisionAllow || overflowResult.Diagnostic != nil, "overflow read: decision=%q diagnostic=%#v, want allow without diagnostic", overflowResult.Decision, overflowResult.Diagnostic)
}

// TestActiveControlFileIndexRetainsObservedControlRecords retains every inode fact.
//
// Example: many controls preserve the marker while ordinary files remain excluded.
func TestActiveControlFileIndexRetainsObservedControlRecords(t *testing.T) {
	t.Parallel()

	temporaryRoot := t.TempDir()
	sessionDir := filepath.Join(temporaryRoot, "proof", "session")
	{
		err := os.MkdirAll(sessionDir, 0o700)
		require.NoErrorf(t, err, "create session directory: %v", err)
	}
	marker := filepath.Join(sessionDir, "eci_active")
	{
		err := os.WriteFile(marker, []byte("active\n"), 0o600)
		require.NoErrorf(t, err, "write marker: %v", err)
	}
	ordinaryPath := filepath.Join(sessionDir, "ordinary-entry-0")
	{
		err := os.WriteFile(ordinaryPath, []byte("ordinary\n"), 0o600)
		require.NoErrorf(t, err, "write ordinary session entry: %v", err)
	}
	for controlIndex := 0; controlIndex < manySessionFixtureEntries; controlIndex++ {
		path := filepath.Join(sessionDir, "eci-required-critics.json.control-"+strconv.Itoa(controlIndex))
		{
			err := os.WriteFile(path, []byte("control\n"), 0o600)
			require.NoErrorf(t, err, "write control session entry %d: %v", controlIndex, err)
		}
	}

	index := activeControlFileIndex([]string{marker})
	require.Falsef(t, len(index.files) != manySessionFixtureEntries+1, "index retained %d entries; want every immediate control", len(index.files))

	result := Classify(Request{
		Provider:      ProviderOmp,
		Role:          RoleWorker,
		CWD:           temporaryRoot,
		Marker:        MarkerActive,
		ActiveSession: "session",
		Command:       "cat ordinary-entry-0",
		ActiveMarkers: []string{marker},
	})
	require.Falsef(t, result.Decision != DecisionAllow || result.Diagnostic != nil, "overflow classification: decision=%q diagnostic=%#v, want allow without diagnostic", result.Decision, result.Diagnostic)
}

// TestGateModeGetIsOrdinaryReadAcrossSpellings verifies that a visible get
// invocation remains ordinary discovery work without a path, role, or marker
// admission boundary.
//
// Example: a copied eci-command-gate-mode get can report its own status.
func TestGateModeGetIsOrdinaryReadAcrossSpellings(t *testing.T) {
	t.Parallel()

	eciPath := gateModePath(ProviderOmp)
	testCases := []struct {
		name     string
		provider Provider
		role     Role
		marker   Marker
		command  string
	}{
		{name: "canonical coordinator", provider: ProviderOmp, role: RoleCoordinator, marker: MarkerActive, command: eciPath + " get"},
		{name: "bare worker", provider: ProviderOmp, role: RoleWorker, marker: MarkerActive, command: "eci-command-gate-mode get"},
		{name: "quoted worker", provider: ProviderOmp, role: RoleWorker, marker: MarkerActive, command: `"` + eciPath + `" get`},
		{name: "copied coordinator", provider: ProviderOmp, role: RoleCoordinator, marker: MarkerActive, command: "/tmp/eci-command-gate-mode get"},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result := Classify(Request{
				Provider:      testCase.provider,
				Role:          testCase.role,
				CWD:           "/tmp",
				Marker:        testCase.marker,
				ActiveSession: "test-session",
				Command:       testCase.command,
			})
			if result.Decision != DecisionAllow || result.Diagnostic != nil {
				t.Fatalf("%q: decision=%q diagnostic=%#v, want ordinary allow without diagnostic", testCase.command, result.Decision, result.Diagnostic)
			}
			if len(result.Capabilities) != 0 || result.DeferredRoute != "" {
				t.Fatalf("%q: capabilities=%v route=%q, want ordinary discovery without protected routing", testCase.command, result.Capabilities, result.DeferredRoute)
			}
		})
	}
}

// TestGateModeSetRequiresExactProviderEnvelope verifies that a mode mutation
// remains on its existing target-aware control route.
//
// Example: the canonical provider set enforcing capability remains available.
func TestGateModeSetRequiresExactProviderEnvelope(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name      string
		provider  Provider
		command   string
		allowed   bool
		predicate string
	}
	eciPath := gateModePath(ProviderOmp)
	testCases := []testCase{
		{name: "omp set enforcing", provider: ProviderOmp, command: eciPath + " set enforcing", allowed: true},
		{name: "omp set permissive", provider: ProviderOmp, command: eciPath + " set permissive", allowed: true},
		{name: "invalid mode", provider: ProviderOmp, command: eciPath + " set invalid", predicate: "gate-mode-envelope"},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			result := Classify(Request{
				Provider:      testCase.provider,
				Role:          RoleCoordinator,
				CWD:           "/tmp",
				Marker:        MarkerActive,
				ActiveSession: "test-session",
				Command:       testCase.command,
			})
			if testCase.allowed {
				if result.Decision != DecisionAllow || result.Diagnostic != nil {
					t.Fatalf("%q: decision=%q diagnostic=%#v, want allow without diagnostic", testCase.command, result.Decision, result.Diagnostic)
				}
				if len(result.Capabilities) != 1 || result.Capabilities[0] != CapabilityGateMode {
					t.Fatalf("%q: capabilities=%v, want [%q]", testCase.command, result.Capabilities, CapabilityGateMode)
				}
				return
			}
			if result.Decision != DecisionDeny || result.Diagnostic == nil {
				t.Fatalf("%q: decision=%q diagnostic=%#v, want deny with diagnostic", testCase.command, result.Decision, result.Diagnostic)
			}
			if result.Diagnostic.Code != CodeControlIdentityDenied || result.Diagnostic.Predicate != testCase.predicate {
				t.Fatalf("%q: diagnostic=%#v, want %q/%q", testCase.command, result.Diagnostic, CodeControlIdentityDenied, testCase.predicate)
			}
			if len(result.Capabilities) != 0 {
				t.Fatalf("%q: capabilities=%v, want none", testCase.command, result.Capabilities)
			}
		})
	}
}

// TestEciAuthorityRootsIgnoreECIHOME verifies that only HOME/.omp/agent/eci
// and the configured Kimi root supply control identity. ECI_HOME may remain
// observable for broad deny-only checks, but cannot select a control root.
//
// Example: ECI_HOME=/tmp/foreign cannot make its eci-active or gate-mode
// binary canonical when HOME/.omp/agent/eci is present.
func TestEciAuthorityRootsIgnoreECIHOME(t *testing.T) {
	if os.Getenv("ECI_TEST_AUTHORITY_ROOTS") == "1" {
		runEciAuthorityRootsIgnoreECIHOMEScenario(t)
		return
	}

	directory := t.TempDir()
	physicalDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatalf("resolve fixture directory identity: %v", err)
	}
	directory = physicalDirectory
	home := filepath.Join(directory, "home")
	eciRoot := filepath.Join(home, ".omp", "agent", "eci")
	foreignRoot := filepath.Join(directory, "foreign-eci")
	kimiRoot := filepath.Join(directory, "configured-kimi")
	for _, root := range []string{eciRoot, foreignRoot, kimiRoot} {
		if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
			t.Fatalf("create %s bin: %v", root, err)
		}
	}
	for _, root := range []string{eciRoot, foreignRoot} {
		if err := os.MkdirAll(filepath.Join(root, "hooks"), 0o755); err != nil {
			t.Fatalf("create %s hooks: %v", root, err)
		}
	}

	writeExecutable := func(path, contents string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
			t.Fatalf("write executable %s: %v", path, err)
		}
	}
	eciGate := filepath.Join(eciRoot, "bin", "eci-command-gate-mode")
	kimiGate := filepath.Join(kimiRoot, "bin", "eci-command-gate-mode")
	foreignGate := filepath.Join(foreignRoot, "bin", "eci-command-gate-mode")
	writeExecutable(eciGate, "#!/bin/sh\nexit 0\n")
	for _, path := range []string{kimiGate, foreignGate} {
		if err := os.Link(eciGate, path); err != nil {
			t.Fatalf("hardlink canonical gate binary at %s: %v", path, err)
		}
	}
	writeExecutable(filepath.Join(eciRoot, "bin", "eci-active"), "#!/bin/sh\nexit canonical\n")
	writeExecutable(filepath.Join(kimiRoot, "bin", "eci-active"), "#!/bin/sh\nexit kimi\n")
	writeExecutable(filepath.Join(foreignRoot, "bin", "eci-active"), "#!/bin/sh\nexit foreign\n")
	for _, root := range []string{eciRoot, foreignRoot} {
		if err := os.WriteFile(filepath.Join(root, "hooks", "validate-bash.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write hook under %s: %v", root, err)
		}
	}

	child := exec.Command(os.Args[0], "-test.run=^TestEciAuthorityRootsIgnoreECIHOME$")
	child.Env = append(
		os.Environ(),
		"ECI_TEST_AUTHORITY_ROOTS=1",
		"HOME="+home,
		"ECI_HOME="+foreignRoot,
		"KIMI_CODE_HOME="+kimiRoot,
		"ECI_TEST_AUTHORITY_ROOTS_FOREIGN="+foreignRoot,
	)
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("run poisoned ECI_HOME authority scenario: %v\n%s", err, output)
	}
}

func runEciAuthorityRootsIgnoreECIHOMEScenario(t *testing.T) {
	home := os.Getenv("HOME")
	foreignRoot := os.Getenv("ECI_TEST_AUTHORITY_ROOTS_FOREIGN")
	kimiRoot := os.Getenv("KIMI_CODE_HOME")
	if home == "" || foreignRoot == "" || kimiRoot == "" {
		t.Fatalf("missing poisoned ECI_HOME fixture: home=%q foreign=%q kimi=%q", home, foreignRoot, kimiRoot)
	}
	eciRoot := filepath.Join(home, ".omp", "agent", "eci")
	eciGate := filepath.Join(eciRoot, "bin", "eci-command-gate-mode")
	kimiGate := filepath.Join(kimiRoot, "bin", "eci-command-gate-mode")
	foreignGate := filepath.Join(foreignRoot, "bin", "eci-command-gate-mode")

	identity := gateModeIdentityForEnvironment()
	if identity.failure != "" {
		t.Errorf("gate-mode identity failure=%q, want lexical HOME/.omp/agent/eci and configured Kimi roots", identity.failure)
	}
	if len(identity.canonicalPaths) != 2 || identity.canonicalPaths[0] != eciGate || identity.canonicalPaths[1] != kimiGate {
		t.Errorf("gate-mode canonical paths=%q, want [%q %q]", identity.canonicalPaths, eciGate, kimiGate)
	}
	if canonical, ok := gateModeProviderPath(ProviderOmp, identity); !ok || canonical != eciGate {
		t.Errorf("omp gate-mode canonical path=%q known=%t, want %q/true", canonical, ok, eciGate)
	}
	if canonical, ok := gateModeProviderPath(ProviderKimi, identity); !ok || canonical != kimiGate {
		t.Errorf("Kimi gate-mode canonical path=%q known=%t, want %q/true", canonical, ok, kimiGate)
	}

	for _, testCase := range []struct {
		name     string
		provider Provider
		command  string
		want     DecisionKind
	}{
		{name: "omp lexical gate", provider: ProviderOmp, command: eciGate + " get", want: DecisionAllow},
		{name: "omp foreign gate read", provider: ProviderOmp, command: foreignGate + " get", want: DecisionAllow},
		{name: "configured Kimi gate", provider: ProviderKimi, command: kimiGate + " get", want: DecisionAllow},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			result := Classify(Request{
				Provider:      testCase.provider,
				Role:          RoleCoordinator,
				CWD:           home,
				Marker:        MarkerActive,
				ActiveSession: "test-session",
				Command:       testCase.command,
			})
			if result.Decision != testCase.want {
				t.Errorf("%q: decision=%q diagnostic=%#v, want %q", testCase.command, result.Decision, result.Diagnostic, testCase.want)
			}
		})
	}

	contains := func(paths []string, want string) bool {
		for _, path := range paths {
			if path == want {
				return true
			}
		}
		return false
	}
	lifecycleRoots := canonicalLifecycleRoots()
	if !contains(lifecycleRoots, eciRoot) || !contains(lifecycleRoots, kimiRoot) || contains(lifecycleRoots, foreignRoot) {
		t.Errorf("lifecycle roots=%q, want lexical omp and configured Kimi roots without foreign ECI_HOME %q", lifecycleRoots, foreignRoot)
	}
	foreignLifecycle := filepath.Join(foreignRoot, "bin", "eci-active")
	lifecycleRequest := activeWorker(foreignLifecycle + " status")
	lifecycleRequest.CWD = home
	if result := Classify(lifecycleRequest); result.Decision != DecisionDefer || result.Diagnostic != nil {
		t.Errorf("foreign lifecycle executable: decision=%q diagnostic=%#v, want provider-adapter defer without canonical worker denial", result.Decision, result.Diagnostic)
	}

	eciHook := filepath.Join(eciRoot, "hooks", "validate-bash.sh")
	foreignHook := filepath.Join(foreignRoot, "hooks", "validate-bash.sh")
	if resolved, ok := protectedHookModeTargetPath(home, eciHook); !ok || resolved != eciHook {
		t.Errorf("lexical omp hook: resolved=%q protected=%t, want %q/true", resolved, ok, eciHook)
	}
	if resolved, ok := protectedHookModeTargetPath(home, foreignHook); ok || resolved != "" {
		t.Errorf("foreign ECI_HOME hook: resolved=%q protected=%t, want empty/false", resolved, ok)
	}
	if roots := protectedHookModeRoots(); !contains(roots, kimiRoot) {
		t.Errorf("protected hook roots=%q, want configured Kimi root %q retained", roots, kimiRoot)
	}
}

// TestActiveBenignEnvironmentWrappersRemainAllowed verifies that ordinary
// literal environment assignments retain the planner's generic allow result.
//
// Example: env FOO=bar novel-tool --flag remains allowed when no protected
// environment context or child operation is selected.
func TestActiveBenignEnvironmentWrappersRemainAllowed(t *testing.T) {
	t.Parallel()

	for _, command := range []string{
		"env FOO=bar novel-tool --flag",
		`env "FOO=bar" novel-tool --flag`,
		"env PATH=/tmp novel-tool --flag",
		"command env FOO=bar novel-tool --flag",
		"exec env FOO=bar novel-tool --flag",
		"stdbuf -oL env FOO=bar novel-tool --flag",
		"busybox env FOO=bar novel-tool --flag",
		"chronic env FOO=bar novel-tool --flag",
	} {
		result := Classify(Request{
			Provider:      ProviderOmp,
			Role:          RoleCoordinator,
			CWD:           "/tmp",
			Marker:        MarkerActive,
			ActiveSession: "test-session",
			Command:       command,
		})
		if result.Decision != DecisionAllow || result.Diagnostic != nil {
			t.Fatalf("%q: decision=%q diagnostic=%#v, want allow without diagnostic", command, result.Decision, result.Diagnostic)
		}
		if len(result.Capabilities) != 0 {
			t.Fatalf("%q: capabilities=%v, want none", command, result.Capabilities)
		}
	}
}

// TestActiveGitDefersToProviderAdapters verifies that active raw Git stays on
// the provider-owned route after Git-specific safety inspection.
//
// Example: git status and env -i git status both reach the provider's legacy
// Git route, while inactive Git and non-Git environment wrappers remain
// ordinary.
func TestActiveGitDefersToProviderAdapters(t *testing.T) {
	t.Parallel()

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
							result := Classify(Request{
								Provider:       provider,
								Role:           role,
								CWD:            "/tmp",
								CommandPath:    callbackPathForTimeoutTests(),
								CommandPathSet: true,
								Marker:         MarkerActive,
								ActiveSession:  "test-session",
								Command:        testCase.command,
							})
							if result.Decision != DecisionDefer || result.Diagnostic != nil {
								t.Fatalf("%q: decision=%q diagnostic=%#v, want defer without diagnostic", testCase.command, result.Decision, result.Diagnostic)
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
		decision DecisionKind
		code     DiagnosticCode
		allRoles bool
	}{
		{
			name:     "inactive direct Git remains allowed",
			marker:   MarkerInactive,
			command:  "git status",
			decision: DecisionAllow,
			allRoles: true,
		},
		{
			name:     "inactive environment Git remains allowed",
			marker:   MarkerInactive,
			command:  "env FOO=bar git status",
			decision: DecisionAllow,
			allRoles: true,
		},
		{
			name:     "Git environment context defers to Git",
			marker:   MarkerActive,
			command:  "env GIT_DIR=/tmp/git-dir git status",
			decision: DecisionDefer,
			allRoles: true,
		},
		{
			name:     "worker checkpoint defers to repository effect checks",
			marker:   MarkerActive,
			role:     RoleWorker,
			command:  "git commit -qm --amend",
			decision: DecisionDefer,
		},
		{
			name:     "coordinator checkpoint defers to producer ownership checks",
			marker:   MarkerActive,
			role:     RoleCoordinator,
			command:  "git commit -m nope",
			decision: DecisionDefer,
		},
		{
			name:     "non-Git environment wrapper remains allowed",
			marker:   MarkerActive,
			command:  "env FOO=bar novel-tool --flag",
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
					result := Classify(Request{
						Provider:      provider,
						Role:          role,
						CWD:           "/tmp",
						Marker:        testCase.marker,
						ActiveSession: "test-session",
						Command:       testCase.command,
					})
					if result.Decision != testCase.decision {
						t.Fatalf("%q: decision=%q, want %q; diagnostic=%#v", testCase.command, result.Decision, testCase.decision, result.Diagnostic)
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

// TestActiveTransparentWrappersRemainAllowed verifies that child-specific
// checks, rather than wrapper presence alone, decide ordinary active commands.
//
// Example: stdbuf -oL novel-tool --flag remains allowed.
func TestActiveTransparentWrappersRemainAllowed(t *testing.T) {
	t.Parallel()

	for _, command := range []string{
		"stdbuf -oL novel-tool --flag",
		"busybox -- novel-tool --flag",
		"chronic novel-tool --flag",
	} {
		result := Classify(Request{
			Provider:      ProviderOmp,
			Role:          RoleCoordinator,
			CWD:           "/tmp",
			Marker:        MarkerActive,
			ActiveSession: "test-session",
			Command:       command,
		})
		if result.Decision != DecisionAllow || result.Diagnostic != nil {
			t.Fatalf("%q: decision=%q diagnostic=%#v, want allow without diagnostic", command, result.Decision, result.Diagnostic)
		}
		if len(result.Capabilities) != 0 {
			t.Fatalf("%q: capabilities=%v, want none", command, result.Capabilities)
		}
	}
}

// TestActiveGenericExecutionRemainsAllowedWithoutCapability verifies that an
// active permission-only callback keeps ordinary literal execution generic.
//
// Example: active `env -i novel-tool --flag` remains allowed without a capability,
// while the same inactive command preserves its ordinary allow result.
func TestActiveGenericExecutionRemainsAllowedWithoutCapability(t *testing.T) {
	baseRequest := Request{
		Provider:      ProviderOmp,
		Role:          RoleCoordinator,
		CWD:           "/tmp",
		Marker:        MarkerActive,
		ActiveSession: "test-session",
	}
	for _, command := range []string{
		"novel-tool literal.js",
		`"FOO=bar" novel-tool`,
		`env "FOO=bar" novel-tool`,
		"env -i novel-tool --flag",
		"command novel-tool --flag",
		"stdbuf -oL novel-tool --flag",
		"busybox -- novel-tool --flag",
		"chronic novel-tool --flag",
	} {
		result := Classify(Request{
			Provider:      baseRequest.Provider,
			Role:          baseRequest.Role,
			CWD:           baseRequest.CWD,
			Marker:        baseRequest.Marker,
			ActiveSession: baseRequest.ActiveSession,
			Command:       command,
		})
		if result.Decision != DecisionAllow || result.Diagnostic != nil {
			t.Fatalf("%q: decision=%q diagnostic=%#v, want allow without diagnostic", command, result.Decision, result.Diagnostic)
		}
		if result.DeferredRoute != "" {
			t.Fatalf("%q: deferred route=%q, want no generic execution route", command, result.DeferredRoute)
		}
		if len(result.Capabilities) != 0 {
			t.Fatalf("%q: capabilities=%v, want none", command, result.Capabilities)
		}
	}

	inactive := baseRequest
	inactive.Marker = MarkerInactive
	inactive.Command = "novel-tool literal.js"
	result := Classify(inactive)
	if result.Decision != DecisionAllow || result.Diagnostic != nil || result.DeferredRoute != "" || len(result.Capabilities) != 0 {
		t.Fatalf("inactive result=%#v, want unchanged generic allow without route or capability", result)
	}
}

// TestActiveInheritedRuntimeContextRemainsOrdinary verifies that inherited
// runtime environment spelling is not a planner permission boundary.
func TestActiveInheritedRuntimeContextRemainsOrdinary(t *testing.T) {
	t.Setenv("NODE_OPTIONS", "--require=/dev/null")

	for _, command := range []string{
		"node literal.js",
		"novel-tool literal.js",
		"exec -a novel-tool node /dev/null",
	} {
		result := Classify(Request{
			Provider:      ProviderOmp,
			Role:          RoleCoordinator,
			CWD:           "/tmp",
			Marker:        MarkerActive,
			ActiveSession: "test-session",
			Command:       command,
		})
		if result.Decision != DecisionAllow || result.Diagnostic != nil {
			t.Fatalf("%q: decision=%q diagnostic=%#v, want ordinary allow", command, result.Decision, result.Diagnostic)
		}
	}
}

// TestActiveInheritedJavaContextRemainsOrdinary applies the same rule to Java
// startup options.
func TestActiveInheritedJavaContextRemainsOrdinary(t *testing.T) {
	t.Setenv("JAVA_TOOL_OPTIONS", "-agentpath:/definitely/missing/eci-review-agent.so")

	result := Classify(Request{
		Provider:      ProviderOmp,
		Role:          RoleCoordinator,
		CWD:           "/tmp",
		Marker:        MarkerActive,
		ActiveSession: "test-session",
		Command:       "java -version",
	})
	if result.Decision != DecisionAllow || result.Diagnostic != nil {
		t.Fatalf("decision=%q diagnostic=%#v, want ordinary allow", result.Decision, result.Diagnostic)
	}
}

// repositoryDefaultGitArchiveCapabilityCase defines one raw command grammar
// case shared by classifier and installed-binary capability tests.
type repositoryDefaultGitArchiveCapabilityCase struct {
	name                string
	command             string
	targetsActiveMarker bool
	wantDecision        DecisionKind
	wantDiagnosticCode  DiagnosticCode
}

// repositoryDefaultGitArchiveCapabilityCases lists archive shapes that must
// never receive a generic Git fast capability.
var repositoryDefaultGitArchiveCapabilityCases = []repositoryDefaultGitArchiveCapabilityCase{
	{
		name:         "direct HEAD has no fast capability",
		command:      "git archive HEAD",
		wantDecision: DecisionDefer,
	},
	{
		name:    "tar output requires legacy route",
		command: "git archive --format=tar --output=artifact.tar HEAD",
	},
	{
		name:                "active marker output uses legacy route",
		command:             "git archive --format=tar --output=<active-marker> HEAD",
		targetsActiveMarker: true,
		wantDecision:        DecisionDefer,
	},
	{
		name:    "quoted revision",
		command: "git archive 'HEAD'",
	},
	{
		name:         "quoted executable",
		command:      "'git' archive HEAD",
		wantDecision: DecisionDefer,
	},
	{
		name:    "escaped executable",
		command: "g\\it archive HEAD",
	},
	{
		name:         "absolute executable path",
		command:      "/usr/bin/git archive HEAD",
		wantDecision: DecisionDefer,
	},
	{
		name:         "relative executable path",
		command:      "./git archive HEAD",
		wantDecision: DecisionDefer,
	},
	{
		name:    "environment wrapper requires legacy route",
		command: "env git archive HEAD",
	},
	{
		name:    "environment assignment wrapper requires legacy route",
		command: "env FOO=bar git archive HEAD",
	},
	{
		name:    "environment ignore wrapper requires legacy route",
		command: "env -i git archive HEAD",
	},
	{
		name:    "environment end-options wrapper requires legacy route",
		command: "env -- git archive HEAD",
	},
	{
		name:    "environment preload assignment",
		command: "env LD_PRELOAD=/tmp/libevil.so git archive HEAD",
	},
	{
		name:    "environment audit assignment",
		command: "env LD_AUDIT=/tmp/libevil.so git archive HEAD",
	},
	{
		name:         "environment repository context assignment",
		command:      "env GIT_DIR=.git git archive HEAD",
		wantDecision: DecisionDefer,
	},
	{
		name:    "environment working-directory context",
		command: "env -C /tmp git archive HEAD",
	},
	{
		name:    "compound plan",
		command: "git archive HEAD && printf done",
	},
	{
		name:    "redirection",
		command: "git archive HEAD > artifact.tar",
	},
	{
		name:    "repository context",
		command: "git -C /tmp archive HEAD",
	},
	{
		name:    "configuration context",
		command: "git -c core.pager=cat archive HEAD",
	},
	{
		name:    "git directory context",
		command: "git --git-dir=.git archive HEAD",
	},
	{
		name:    "work tree context",
		command: "git --work-tree=. archive HEAD",
	},
	{
		name:    "configuration environment context",
		command: "git --config-env=core.foo=FOO archive HEAD",
	},
	{
		name:    "execution path context",
		command: "git --exec-path=/tmp archive HEAD",
	},
	{
		name:    "namespace context",
		command: "git --namespace=namespace archive HEAD",
	},
	{
		name:    "super prefix context",
		command: "git --super-prefix=prefix archive HEAD",
	},
	{
		name:    "text conversion context",
		command: "git --textconv archive HEAD",
	},
	{
		name:    "external diff context",
		command: "git --ext-diff archive HEAD",
	},
	{
		name:    "archive remote attached",
		command: "git archive --remote=origin HEAD",
	},
	{
		name:    "archive remote separate",
		command: "git archive --remote origin HEAD",
	},
	{
		name:    "archive exec attached",
		command: "git archive --exec=git-upload-archive HEAD",
	},
	{
		name:    "archive exec separate",
		command: "git archive --exec git-upload-archive HEAD",
	},
	{
		name:    "output inference",
		command: "git archive --output=artifact.tar HEAD",
	},
	{
		name:    "separate output argument",
		command: "git archive --format=tar --output artifact.tar HEAD",
	},
	{
		name:    "empty output value",
		command: "git archive --format=tar --output= HEAD",
	},
	{
		name:    "quoted output value",
		command: "git archive --format=tar --output='artifact.tar' HEAD",
	},
	{
		name:    "custom archive format",
		command: "git archive --format=zip --output=artifact.zip HEAD",
	},
	{
		name:    "additional file",
		command: "git archive --format=tar --output=artifact.tar --add-file=README HEAD",
	},
	{
		name:    "pathspec",
		command: "git archive --format=tar --output=artifact.tar HEAD README",
	},
	{
		name:    "extra archive option",
		command: "git archive --format=tar --output=artifact.tar HEAD --prefix=release/",
	},
	{
		name:    "output option before format",
		command: "git archive --output=artifact.tar --format=tar HEAD",
	},
}

// TestRepositoryDefaultGitArchiveHasNoFastCapability verifies that no archive
// argv carries a generic fast capability because configured archive formats
// can execute a shell command.
//
// Example: git archive HEAD is deferred just like an output or wrapped form.
func TestRepositoryDefaultGitArchiveHasNoFastCapability(t *testing.T) {
	t.Parallel()

	for _, testCase := range repositoryDefaultGitArchiveCapabilityCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			request := repositoryDefaultGitArchiveCapabilityRequest(t, testCase)
			result := Classify(request)
			if testCase.wantDecision != "" && result.Decision != testCase.wantDecision {
				t.Fatalf("%q: decision=%q, want %q; diagnostic=%#v", request.Command, result.Decision, testCase.wantDecision, result.Diagnostic)
			}
			if testCase.wantDiagnosticCode != "" && (result.Diagnostic == nil || result.Diagnostic.Code != testCase.wantDiagnosticCode) {
				t.Fatalf("%q: diagnostic=%#v, want code %q", request.Command, result.Diagnostic, testCase.wantDiagnosticCode)
			}
			if len(result.Capabilities) != 0 {
				t.Fatalf("%q: capabilities=%v, want none", testCase.command, result.Capabilities)
			}
		})
	}
}

// TestInstalledBinaryRejectsRepositoryDefaultGitArchiveFastCapability verifies
// the shipped planner leaves all archive forms off the generic fast path.
//
// Example: a hook can consume the binary response without re-tokenizing any
// accepted or rejected original shell command.
func TestInstalledBinaryRejectsRepositoryDefaultGitArchiveFastCapability(t *testing.T) {
	binary, err := filepath.Abs("eci-command-plan")
	if err != nil {
		t.Fatalf("resolve installed binary: %v", err)
	}

	for _, testCase := range repositoryDefaultGitArchiveCapabilityCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			request := repositoryDefaultGitArchiveCapabilityRequest(t, testCase)
			input, err := json.Marshal(request)
			if err != nil {
				t.Fatalf("marshal %q request: %v", request.Command, err)
			}

			var stdout bytes.Buffer
			status := runBinary(t, binary, input, &stdout)
			var result Result
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatalf("decode %q response: %v; output=%s", request.Command, err, stdout.String())
			}
			if testCase.wantDecision != "" && result.Decision != testCase.wantDecision {
				t.Fatalf("%q: decision=%q, want %q; diagnostic=%#v", request.Command, result.Decision, testCase.wantDecision, result.Diagnostic)
			}
			if testCase.wantDiagnosticCode != "" && (result.Diagnostic == nil || result.Diagnostic.Code != testCase.wantDiagnosticCode) {
				t.Fatalf("%q: diagnostic=%#v, want code %q", request.Command, result.Diagnostic, testCase.wantDiagnosticCode)
			}
			if len(result.Capabilities) != 0 {
				t.Fatalf("%q: capabilities=%v, want none", request.Command, result.Capabilities)
			}
			if testCase.wantDecision == DecisionDeny && status != StatusDeny {
				t.Fatalf("%q: status=%d output=%s, want %d", request.Command, status, stdout.String(), StatusDeny)
			}
			if testCase.wantDecision == DecisionDefer && status != StatusDefer {
				t.Fatalf("%q: status=%d output=%s, want %d", request.Command, status, stdout.String(), StatusDefer)
			}
		})
	}
}

// repositoryDefaultGitArchiveCapabilityRequest materializes an active marker
// output target when a shared grammar case needs to prove raw Git reaches the
// provider-owned route before generic local control-path handling.
func repositoryDefaultGitArchiveCapabilityRequest(t *testing.T, testCase repositoryDefaultGitArchiveCapabilityCase) Request {
	t.Helper()

	request := activeWorker(testCase.command)
	if !testCase.targetsActiveMarker {
		return request
	}

	marker := filepath.Join(t.TempDir(), "eci_active")
	if err := os.WriteFile(marker, []byte("active\n"), 0o600); err != nil {
		t.Fatalf("write active marker %s: %v", marker, err)
	}
	request.ActiveMarkers = []string{marker}
	request.Command = "git archive --format=tar --output=" + marker + " HEAD"
	return request
}

// directPathGitStatusCapabilityCase defines one raw direct-Git shape that must
// remain outside the generic fast path until executable identity is proven.
//
// Example: /tmp/fake-bin/git status cannot inherit a capability from basename.
type directPathGitStatusCapabilityCase struct {
	name               string
	command            string
	wantDecision       DecisionKind
	wantDiagnosticCode DiagnosticCode
}

// directPathGitStatusCapabilityCases lists direct-status shapes that must
// remain outside a generic fast capability.
//
// Example: a status flag or wrapper around /tmp/fake-bin/git stays uncategorized.
var directPathGitStatusCapabilityCases = []directPathGitStatusCapabilityCase{
	{
		name:         "direct status has no fast capability",
		command:      "/tmp/task/git status",
		wantDecision: DecisionDefer,
	},
	{
		name:         "relative direct status has no fast capability",
		command:      "./tools/git status",
		wantDecision: DecisionDefer,
	},
	{
		name:         "no pager status has no fast capability",
		command:      "git --no-pager status",
		wantDecision: DecisionDefer,
	},
	{
		name:         "quoted executable",
		command:      "\"/tmp/task/git\" status",
		wantDecision: DecisionDefer,
	},
	{
		name:         "status short flag",
		command:      "/tmp/task/git status --short",
		wantDecision: DecisionDefer,
	},
	{
		name:         "status porcelain flag",
		command:      "/tmp/task/git status --porcelain=v1",
		wantDecision: DecisionDefer,
	},
	{
		name:         "no pager status",
		command:      "/tmp/task/git --no-pager status",
		wantDecision: DecisionDefer,
	},
	{
		name:         "direct archive",
		command:      "/tmp/task/git archive HEAD",
		wantDecision: DecisionDefer,
	},
	{
		name:         "direct no pager archive",
		command:      "/tmp/task/git --no-pager archive HEAD",
		wantDecision: DecisionDefer,
	},
	{
		name:         "environment wrapper",
		command:      "env /tmp/task/git status",
		wantDecision: DecisionDefer,
	},
	{
		name:         "compound",
		command:      "/tmp/task/git status && printf after",
		wantDecision: DecisionDefer,
	},
	{
		name:         "mutation",
		command:      "/tmp/task/git rebase topic",
		wantDecision: DecisionDefer,
	},
	{
		name:         "direct notes mutation",
		command:      "/tmp/task/git notes add -m note",
		wantDecision: DecisionDefer,
	},
	{
		name:         "direct stash mutation",
		command:      "/tmp/task/git stash push",
		wantDecision: DecisionDefer,
	},
	{
		name:         "direct clean mutation",
		command:      "/tmp/task/git clean -f",
		wantDecision: DecisionDefer,
	},
	{
		name:         "direct reflog mutation",
		command:      "/tmp/task/git reflog expire",
		wantDecision: DecisionDefer,
	},
}

// TestDirectPathGitStatusHasNoFastCapability verifies direct Git basenames do
// not receive a generic capability without an executable-identity binding.
//
// Example: /tmp/fake-bin/git status stays capability-free.
func TestDirectPathGitStatusHasNoFastCapability(t *testing.T) {
	t.Parallel()

	for _, testCase := range directPathGitStatusCapabilityCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result := Classify(activeWorker(testCase.command))
			if result.Decision != testCase.wantDecision {
				t.Fatalf("%q: decision=%q, want %q; diagnostic=%#v", testCase.command, result.Decision, testCase.wantDecision, result.Diagnostic)
			}
			if testCase.wantDiagnosticCode != "" && (result.Diagnostic == nil || result.Diagnostic.Code != testCase.wantDiagnosticCode) {
				t.Fatalf("%q: diagnostic=%#v, want code %q", testCase.command, result.Diagnostic, testCase.wantDiagnosticCode)
			}
			if len(result.Capabilities) != 0 {
				t.Fatalf("%q: capabilities=%v, want none", testCase.command, result.Capabilities)
			}
		})
	}
}

// TestInstalledBinaryRejectsDirectPathGitStatusFastCapability verifies the
// shipped planner leaves path-selected Git status on its identity-aware route.
//
// Example: /tmp/fake-bin/git status emits no generic capability.
func TestInstalledBinaryRejectsDirectPathGitStatusFastCapability(t *testing.T) {
	binary, err := filepath.Abs("eci-command-plan")
	if err != nil {
		t.Fatalf("resolve installed binary: %v", err)
	}

	for _, testCase := range directPathGitStatusCapabilityCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			input, err := json.Marshal(activeWorker(testCase.command))
			if err != nil {
				t.Fatalf("marshal %q request: %v", testCase.command, err)
			}

			var stdout bytes.Buffer
			status := runBinary(t, binary, input, &stdout)
			var result Result
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatalf("decode %q response: %v; output=%s", testCase.command, err, stdout.String())
			}
			if result.Decision != testCase.wantDecision {
				t.Fatalf("%q: decision=%q, want %q; diagnostic=%#v", testCase.command, result.Decision, testCase.wantDecision, result.Diagnostic)
			}
			if len(result.Capabilities) != 0 {
				t.Fatalf("%q: capabilities=%v, want none", testCase.command, result.Capabilities)
			}
			if testCase.wantDecision == DecisionDeny && status != StatusDeny {
				t.Fatalf("%q: status=%d output=%s, want %d", testCase.command, status, stdout.String(), StatusDeny)
			}
			if testCase.wantDecision == DecisionDefer && status != StatusDefer {
				t.Fatalf("%q: status=%d output=%s, want %d", testCase.command, status, stdout.String(), StatusDefer)
			}
		})
	}
}

func TestGateModeIdentityAndWorkerOwnershipAreCompiled(t *testing.T) {
	t.Parallel()

	canonical := gateModePath(ProviderOmp)
	coordinator := Classify(Request{
		Provider:      ProviderOmp,
		Role:          RoleCoordinator,
		CWD:           "/tmp",
		Marker:        MarkerActive,
		ActiveSession: "test-session",
		Command:       canonical + " set enforcing",
	})
	if coordinator.Decision != DecisionAllow || coordinator.Diagnostic != nil {
		t.Fatalf("canonical coordinator gate mode: decision=%q diagnostic=%#v, want allow", coordinator.Decision, coordinator.Diagnostic)
	}

	worker := coordinator
	workerRequest := Request{
		Provider:      ProviderOmp,
		Role:          RoleWorker,
		CWD:           "/tmp",
		Marker:        MarkerActive,
		ActiveSession: "test-session",
		Command:       canonical + " set enforcing",
	}
	worker = Classify(workerRequest)
	if worker.Decision != DecisionDeny || worker.Diagnostic == nil {
		t.Fatalf("canonical worker gate mode: decision=%q diagnostic=%#v, want deny", worker.Decision, worker.Diagnostic)
	}
	if worker.Diagnostic.Code != CodeControlOwnerRequired || worker.Diagnostic.Predicate != "gate-mode-mutation" {
		t.Fatalf("canonical worker diagnostic: code=%q predicate=%q, want %q/gate-mode-mutation", worker.Diagnostic.Code, worker.Diagnostic.Predicate, CodeControlOwnerRequired)
	}

	altered := filepath.Join(t.TempDir(), "eci-command-gate-mode")
	contents, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatalf("read canonical gate-mode executable: %v", err)
	}
	if err := os.WriteFile(altered, append(contents, '\n'), 0o755); err != nil {
		t.Fatalf("write altered gate-mode executable: %v", err)
	}
	identity := Classify(Request{
		Provider:      ProviderOmp,
		Role:          RoleCoordinator,
		CWD:           "/tmp",
		Marker:        MarkerActive,
		ActiveSession: "test-session",
		Command:       altered + " set enforcing",
	})
	if identity.Decision != DecisionDeny || identity.Diagnostic == nil {
		t.Fatalf("altered coordinator gate mode: decision=%q diagnostic=%#v, want deny", identity.Decision, identity.Diagnostic)
	}
	if identity.Diagnostic.Code != CodeControlIdentityDenied || identity.Diagnostic.Predicate != "gate-mode-identity" {
		t.Fatalf("altered coordinator diagnostic: code=%q predicate=%q, want %q/gate-mode-identity", identity.Diagnostic.Code, identity.Diagnostic.Predicate, CodeControlIdentityDenied)
	}
}

func TestInactiveWorkerGateModeMutationIsDeniedForBothProviders(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		provider Provider
		command  string
	}{
		{provider: ProviderOmp, command: gateModePath(ProviderOmp) + " set enforcing"},
	} {
		testCase := testCase
		t.Run(string(testCase.provider), func(t *testing.T) {
			t.Parallel()
			result := Classify(Request{
				Provider:      testCase.provider,
				Role:          RoleWorker,
				CWD:           "/tmp",
				Marker:        MarkerInactive,
				ActiveSession: "test-session",
				Command:       testCase.command,
			})
			if result.Decision != DecisionDeny || result.Diagnostic == nil {
				t.Fatalf("inactive worker gate mode command=%q: decision=%q diagnostic=%#v, want deny with diagnostic", testCase.command, result.Decision, result.Diagnostic)
			}
			if result.Diagnostic.Code != CodeControlOwnerRequired || result.Diagnostic.Predicate != "gate-mode-mutation" {
				t.Fatalf("inactive worker diagnostic: code=%q predicate=%q, want %q/gate-mode-mutation", result.Diagnostic.Code, result.Diagnostic.Predicate, CodeControlOwnerRequired)
			}
			if result.Diagnostic.Token != "set" || result.Diagnostic.ArgvIndex != 1 || result.Diagnostic.Segment != 1 {
				t.Fatalf("inactive worker location: token=%q argv_index=%d segment=%d, want set/1/1", result.Diagnostic.Token, result.Diagnostic.ArgvIndex, result.Diagnostic.Segment)
			}
		})
	}
}

func TestTransparentWrappersPreserveChildClassification(t *testing.T) {
	t.Parallel()

	commands := []string{
		"nohup novel-tool --flag value",
		"setsid --wait novel-tool --flag value",
		"sudo -n novel-tool --flag value",
		"doas -n novel-tool --flag value",
		"systemd-run --unit eci novel-tool --flag value",
		"time --format %E novel-tool --flag value",
		"prlimit --nofile=1024 novel-tool --flag value",
		"chronic -- novel-tool --flag value",
	}
	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()
			for _, command := range commands {
				result := Classify(Request{Provider: provider, Role: RoleCoordinator, CWD: "/tmp", Marker: MarkerActive, ActiveSession: "test", Command: command})
				if result.Decision != DecisionAllow || result.Diagnostic != nil {
					t.Errorf("%s: got decision=%q diagnostic=%#v, want allow", command, result.Decision, result.Diagnostic)
				}
			}
			for _, command := range []string{
				"nohup git rebase topic",
				"setsid --wait git rebase topic",
				"sudo -n git rebase topic",
				"doas -n git rebase topic",
				"systemd-run --unit eci git rebase topic",
				"time --format %E git rebase topic",
				"prlimit --nofile=1024 git rebase topic",
				"chronic -- git rebase topic",
			} {
				result := Classify(Request{Provider: provider, Role: RoleWorker, CWD: "/tmp", Marker: MarkerActive, ActiveSession: "test", Command: command})
				if result.Decision != DecisionDefer || result.Diagnostic != nil {
					t.Errorf("%s: decision=%q diagnostic=%#v, want native Git provider defer", command, result.Decision, result.Diagnostic)
				}
			}
		})
	}
}

func TestBroadDestructionIsProviderNeutral(t *testing.T) {
	t.Parallel()

	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()

			for _, command := range []string{"rm -rf /", "env FOO=bar rm -rf /"} {
				result := Classify(Request{
					Provider: provider,
					Role:     RoleWorker,
					CWD:      "/tmp",
					Marker:   MarkerActive,
					Command:  command,
				})
				if result.Decision != DecisionDeny {
					t.Fatalf("%s: decision: got %q, want %q; diagnostic=%#v", command, result.Decision, DecisionDeny, result.Diagnostic)
				}
				if result.Diagnostic == nil {
					t.Fatalf("%s: missing diagnostic", command)
				}
				if result.Diagnostic.Code != CodeBroadDestructiveDenied {
					t.Errorf("%s: code: got %q, want %q", command, result.Diagnostic.Code, CodeBroadDestructiveDenied)
				}
				if result.Diagnostic.Operation != "broad-destructive" {
					t.Errorf("%s: operation: got %q, want broad-destructive", command, result.Diagnostic.Operation)
				}
				if result.Diagnostic.Segment != 1 || result.Diagnostic.ArgvIndex < 0 || result.Diagnostic.ByteOffset < 0 || result.Diagnostic.Token == "" || result.Diagnostic.Path == "n/a" || result.Diagnostic.Predicate == "" || result.Diagnostic.Reason == "" || result.Diagnostic.Remediation == "" {
					t.Errorf("%s: incomplete compiler-style diagnostic: %#v", command, result.Diagnostic)
				}
			}
		})
	}
}

func TestGitExecutionContextSpellingDefersWithoutDiagnostic(t *testing.T) {
	t.Parallel()

	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()

			request := activeWorker("git -C /tmp -c user.name=test status --short")
			request.Provider = provider
			result := Classify(request)
			if result.Decision != DecisionDefer || result.Diagnostic != nil {
				t.Fatalf("decision=%q diagnostic=%#v, want Git-context defer", result.Decision, result.Diagnostic)
			}

			foreign := Classify(Request{
				Provider:      provider,
				Role:          RoleWorker,
				CWD:           "/tmp",
				Marker:        MarkerActive,
				ActiveSession: "test-session",
				Command:       "git -C /tmp/foreign-repo status --short",
			})
			if foreign.Decision != DecisionDefer || foreign.Diagnostic != nil {
				t.Fatalf("foreign Git context: decision=%q diagnostic=%#v, want defer without diagnostic", foreign.Decision, foreign.Diagnostic)
			}
		})
	}
}

func TestProofPathOwnership(t *testing.T) {
	t.Parallel()

	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()

			temporaryRoot := t.TempDir()
			proofSession := filepath.Join(temporaryRoot, "proof", "session")
			evidenceDirectory := filepath.Join(proofSession, "evidence")
			if err := os.MkdirAll(evidenceDirectory, 0o700); err != nil {
				t.Fatalf("create evidence directory: %v", err)
			}
			marker := filepath.Join(proofSession, "eci_active")
			inside := filepath.Join(evidenceDirectory, "inside.txt")
			missingInside := filepath.Join(evidenceDirectory, "missing", "instruction.md")
			outside := filepath.Join(temporaryRoot, "outside.txt")
			missingOutside := filepath.Join(temporaryRoot, "missing", "ordinary.txt")
			outsideLink := filepath.Join(evidenceDirectory, "outside-link")
			for path, content := range map[string][]byte{
				marker:  []byte("active\n"),
				inside:  []byte("inside\n"),
				outside: []byte("outside\n"),
			} {
				if err := os.WriteFile(path, content, 0o600); err != nil {
					t.Fatalf("write %s: %v", path, err)
				}
			}
			if err := os.Symlink(outside, outsideLink); err != nil {
				t.Fatalf("create outside link: %v", err)
			}
			liveControl := Classify(Request{
				Provider:      provider,
				Role:          RoleWorker,
				CWD:           temporaryRoot,
				Marker:        MarkerActive,
				ActiveSession: "session",
				Command:       "cat " + marker,
				ActiveMarkers: []string{marker},
			})
			if liveControl.Decision != DecisionAllow || liveControl.Diagnostic != nil {
				t.Fatalf("live control read: decision=%q diagnostic=%#v, want allow without diagnostic", liveControl.Decision, liveControl.Diagnostic)
			}

			controlAlias := filepath.Join(temporaryRoot, "arbitrary-control-alias")
			if err := os.Link(marker, controlAlias); err != nil {
				t.Fatalf("create arbitrary control hardlink alias: %v", err)
			}
			controlAliasResult := Classify(Request{
				Provider:      provider,
				Role:          RoleWorker,
				CWD:           temporaryRoot,
				Marker:        MarkerActive,
				ActiveSession: "session",
				Command:       "cat " + controlAlias,
				ActiveMarkers: []string{marker},
			})
			if controlAliasResult.Decision != DecisionAllow || controlAliasResult.Diagnostic != nil {
				t.Fatalf("arbitrary control hardlink read: decision=%q diagnostic=%#v, want allow without diagnostic", controlAliasResult.Decision, controlAliasResult.Diagnostic)
			}
			controlAliasWrite := Classify(Request{
				Provider:      provider,
				Role:          RoleWorker,
				CWD:           temporaryRoot,
				Marker:        MarkerActive,
				ActiveSession: "session",
				Command:       "touch " + controlAlias,
				ActiveMarkers: []string{marker},
			})
			if controlAliasWrite.Decision != DecisionDeny || controlAliasWrite.Diagnostic == nil {
				t.Fatalf("arbitrary control hardlink write: decision=%q diagnostic=%#v, want concrete control-target denial", controlAliasWrite.Decision, controlAliasWrite.Diagnostic)
			}
			if controlAliasWrite.Diagnostic.Code != CodePlanLiveControlDenied || controlAliasWrite.Diagnostic.Path != marker {
				t.Fatalf("arbitrary control hardlink write diagnostic=%#v, want live control path %q", controlAliasWrite.Diagnostic, marker)
			}

			ordinaryHelper := filepath.Join(temporaryRoot, "eci-environment-command.sh")
			ordinaryHelperAlias := filepath.Join(temporaryRoot, "arbitrary-helper-alias")
			if err := os.WriteFile(ordinaryHelper, []byte("#!/bin/sh\nexit 0\n"), 0o600); err != nil {
				t.Fatalf("write ordinary helper: %v", err)
			}
			if err := os.Link(ordinaryHelper, ordinaryHelperAlias); err != nil {
				t.Fatalf("create ordinary helper hardlink alias: %v", err)
			}
			ordinaryAliasResult := Classify(Request{
				Provider:      provider,
				Role:          RoleWorker,
				CWD:           temporaryRoot,
				Marker:        MarkerActive,
				ActiveSession: "session",
				Command:       "cat " + ordinaryHelperAlias,
				ActiveMarkers: []string{marker},
			})
			if ordinaryAliasResult.Decision != DecisionAllow || ordinaryAliasResult.Diagnostic != nil {
				t.Fatalf("ordinary helper hardlink: decision=%q diagnostic=%#v, want allow without diagnostic", ordinaryAliasResult.Decision, ordinaryAliasResult.Diagnostic)
			}
			proofAlias := filepath.Join(t.TempDir(), "proof-alias")
			if err := os.Symlink(filepath.Dir(proofSession), proofAlias); err != nil {
				t.Fatalf("create proof-root alias: %v", err)
			}
			aliasedInside := filepath.Join(proofAlias, "session", "evidence", "inside.txt")
			aliasedOutsideLink := filepath.Join(proofAlias, "session", "evidence", "outside-link")

			for _, testCase := range []struct {
				name     string
				path     string
				decision DecisionKind
			}{
				{name: "contained regular path", path: inside, decision: DecisionAllow},
				{name: "missing path in active proof tree", path: missingInside, decision: DecisionAllow},
				{name: "contained path through proof-root alias", path: aliasedInside, decision: DecisionAllow},
				{name: "ordinary outside path", path: outside, decision: DecisionAllow},
				{name: "missing ordinary outside path", path: missingOutside, decision: DecisionAllow},
				{name: "lexically contained symlink read", path: outsideLink, decision: DecisionAllow},
				{name: "aliased proof-root symlink read", path: aliasedOutsideLink, decision: DecisionAllow},
			} {
				t.Run(testCase.name, func(t *testing.T) {
					request := Request{
						Provider:      provider,
						Role:          RoleCoordinator,
						CWD:           temporaryRoot,
						Marker:        MarkerActive,
						ActiveSession: "session",
						Command:       "cat " + testCase.path,
						ActiveMarkers: []string{marker},
					}
					result := Classify(request)
					if result.Decision != testCase.decision {
						t.Fatalf("decision: got %q, want %q; diagnostic=%#v", result.Decision, testCase.decision, result.Diagnostic)
					}
					if testCase.decision != DecisionDeny {
						if result.Diagnostic != nil {
							t.Fatalf("unexpected diagnostic: %#v", result.Diagnostic)
						}
						return
					}
					if result.Diagnostic == nil {
						t.Fatal("missing proof escape diagnostic")
					}
					if result.Diagnostic.Code != DiagnosticCode("ECI_PROOF_PATH_ESCAPE_DENIED") {
						t.Errorf("code: got %q, want ECI_PROOF_PATH_ESCAPE_DENIED", result.Diagnostic.Code)
					}
					if result.Diagnostic.Operation != "proof-path-ownership" {
						t.Errorf("operation: got %q, want proof-path-ownership", result.Diagnostic.Operation)
					}
					if result.Diagnostic.Predicate != "proof-symlink-escape" {
						t.Errorf("predicate: got %q, want proof-symlink-escape", result.Diagnostic.Predicate)
					}
					if result.Diagnostic.Path != testCase.path {
						t.Errorf("path: got %q, want %q", result.Diagnostic.Path, testCase.path)
					}
				})
			}

			escapingWrite := Classify(Request{
				Provider:      provider,
				Role:          RoleCoordinator,
				CWD:           temporaryRoot,
				Marker:        MarkerActive,
				ActiveSession: "session",
				Command:       "touch " + outsideLink,
				ActiveMarkers: []string{marker},
			})
			if escapingWrite.Decision != DecisionAllow || escapingWrite.Diagnostic != nil {
				t.Fatalf("ordinary write through proof alias: decision=%q diagnostic=%#v, want allow", escapingWrite.Decision, escapingWrite.Diagnostic)
			}

			workerResult := Classify(Request{
				Provider:      provider,
				Role:          RoleWorker,
				CWD:           temporaryRoot,
				Marker:        MarkerActive,
				ActiveSession: "session",
				Command:       "find -P " + missingInside + " -maxdepth 1 -print",
				ActiveMarkers: []string{marker},
			})
			if workerResult.Decision != DecisionAllow || workerResult.Diagnostic != nil {
				t.Fatalf("missing worker proof path: decision=%q diagnostic=%#v, want allow without diagnostic", workerResult.Decision, workerResult.Diagnostic)
			}
		})
	}
}

func TestProtectedOperationsRouteByRole(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		role     Role
		cwd      string
		command  string
		decision DecisionKind
		code     DiagnosticCode
	}{
		{name: "worker checkpoint", role: RoleWorker, command: "git commit -m checkpoint", decision: DecisionDefer},
		{name: "worker lifecycle state discovery", role: RoleWorker, command: "eci-active status", decision: DecisionDefer},
		{name: "coordinator lifecycle control", role: RoleCoordinator, command: "eci-active status", decision: DecisionDefer},
		{name: "worker source write", role: RoleWorker, command: "touch source.txt", decision: DecisionAllow},
		{name: "coordinator source write", role: RoleCoordinator, command: "touch source.txt", decision: DecisionAllow},
		{name: "coordinator outside-CWD source write", role: RoleCoordinator, cwd: "/workspace", command: "touch /tmp/source.txt", decision: DecisionAllow},
	}
	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()

			for _, testCase := range testCases {
				testCase := testCase
				t.Run(testCase.name, func(t *testing.T) {
					t.Parallel()

					cwd := testCase.cwd
					if cwd == "" {
						cwd = "/tmp"
					}
					request := Request{
						Provider:      provider,
						Role:          testCase.role,
						CWD:           cwd,
						Marker:        MarkerActive,
						ActiveSession: "test",
						Command:       testCase.command,
					}
					result := Classify(request)
					if result.Decision != testCase.decision {
						t.Fatalf("decision: got %q, want %q; diagnostic=%#v", result.Decision, testCase.decision, result.Diagnostic)
					}
					if testCase.code == "" {
						if result.Diagnostic != nil {
							t.Fatalf("unexpected diagnostic: %#v", result.Diagnostic)
						}
						return
					}
					if result.Diagnostic == nil {
						t.Fatal("missing Git ownership diagnostic")
					}
					if result.Diagnostic.Code != testCase.code {
						t.Fatalf("diagnostic code: got %q, want %q", result.Diagnostic.Code, testCase.code)
					}
				})
			}
		})
	}
}

// TestCurrentControlCopyOperandRoles distinguishes copy inputs from concrete
// destinations in both proof-path and live-inode ownership checks.
//
// Example: copying out a marker hardlink is ordinary; copying onto it is not.
func TestCurrentControlCopyOperandRoles(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	session := filepath.Join(directory, "proof", "session")
	if err := os.MkdirAll(session, 0o700); err != nil {
		t.Fatalf("create proof session: %v", err)
	}
	marker := filepath.Join(session, "eci_active")
	if err := os.WriteFile(marker, []byte("active\n"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	alias := filepath.Join(directory, "marker-hardlink")
	if err := os.Link(marker, alias); err != nil {
		t.Fatalf("link marker: %v", err)
	}
	ordinary := filepath.Join(directory, "ordinary")

	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		for _, role := range []Role{RoleCoordinator, RoleWorker} {
			for _, testCase := range []struct {
				name       string
				command    string
				target     string
				segment    int
				argvIndex  int
				workerOnly bool
			}{
				{name: "marker input", command: "cp " + marker + " " + ordinary},
				{name: "flagged marker input", command: "cp -f -- " + marker + " " + ordinary},
				{name: "quoted marker input", command: "cp '" + marker + "' " + ordinary},
				{name: "marker redirect input", command: "cp " + ordinary + " < " + marker + " " + ordinary + "-two"},
				{name: "attached marker redirect input", command: "cp " + ordinary + "<" + marker + " " + ordinary + "-two"},
				{name: "marker source with redirect input", command: "cp " + marker + " 0< " + ordinary + " " + ordinary + "-two"},
				{name: "marker destination", command: "cp " + ordinary + " " + marker, target: marker, argvIndex: 2},
				{name: "flagged destination", command: "cp -f -- " + ordinary + " " + marker, target: marker, argvIndex: 4},
				{name: "terminator after source", command: "cp " + ordinary + " -- " + marker, target: marker, argvIndex: 3},
				{name: "finite flags destination", command: "cp -frR -v --force --recursive --verbose " + ordinary + " " + marker, target: marker, argvIndex: 7},
				{name: "bare redirect before destination", command: "cp " + ordinary + " < " + ordinary + "-input " + marker, target: marker, argvIndex: 2},
				{name: "attached redirect before destination", command: "cp " + ordinary + "<" + ordinary + "-input " + marker, target: marker, argvIndex: 2},
				{name: "stdin redirect before destination", command: "cp " + ordinary + " 0< " + ordinary + "-input " + marker, target: marker, argvIndex: 2},
				{name: "stdout input redirect before destination", command: "cp " + ordinary + " 1< " + ordinary + "-input " + marker, target: marker, argvIndex: 2},
				{name: "stderr input redirect before destination", command: "cp " + ordinary + " 2< " + ordinary + "-input " + marker, target: marker, argvIndex: 2},
				{name: "unsupported option-looking input before destination", command: "cp " + ordinary + " < --unknown " + marker, target: marker, argvIndex: 2},
				{name: "terminator with input before destination", command: "cp -- " + ordinary + " 0< " + ordinary + "-input " + marker, target: marker, argvIndex: 3},
				{name: "unknown option", command: "cp --unknown " + ordinary + " " + marker},
				{name: "too many operands", command: "cp " + ordinary + " " + marker + " elsewhere"},
				{name: "one operand", command: "cp " + marker},
				{name: "heredoc remains unsupported", command: "cp " + ordinary + " << " + marker + " " + marker},
				{name: "descriptor duplication remains unsupported", command: "cp " + ordinary + " <& " + marker + " " + marker},
				{name: "process substitution remains visible", command: "cp " + ordinary + " <(printf) " + marker},
				{name: "brace grouping remains visible", command: "cp " + ordinary + " < { printf } " + marker},
				{name: "invalid generic output assignment", command: "cp --output=" + marker + " ordinary elsewhere"},
				{name: "invalid generic output pair", command: "cp --output " + marker + " ordinary elsewhere"},
				{name: "invalid short output", command: "cp -o" + marker + " ordinary elsewhere"},
				{name: "independent redirect", command: "cp --output=" + marker + " ordinary elsewhere > " + marker, target: marker, argvIndex: 1},
				{name: "input reset by output redirect", command: "cp " + ordinary + " < > " + marker, target: marker, argvIndex: 1},
				{name: "input reset by later segment", command: "cp " + ordinary + " <; cp " + ordinary + " " + marker, target: marker, segment: 2, argvIndex: 2},
				{name: "later copy destination", command: "cp " + marker + " 0< " + ordinary + " " + ordinary + "-two; cp " + ordinary + " " + marker, target: marker, segment: 2, argvIndex: 2},
				{name: "evidenced output retained", command: "sort --output=" + marker, target: marker, argvIndex: 1},
				{name: "touch marker redirect input", command: "touch < " + marker + " " + ordinary},
				{name: "touch marker target after input", command: "touch < " + ordinary + " " + marker, target: marker, argvIndex: 1},
				{name: "move still mutates source", command: "mv " + marker + " " + ordinary, target: marker, argvIndex: 1},
				{name: "alias input", command: "cp " + alias + " " + ordinary, workerOnly: true},
				{name: "alias redirect input", command: "cp " + ordinary + " 0< " + alias + " " + ordinary + "-two", workerOnly: true},
				{name: "alias destination", command: "cp " + ordinary + " " + alias, target: alias, argvIndex: 2, workerOnly: true},
				{name: "flagged alias destination", command: "cp -f -- " + ordinary + " " + alias, target: alias, argvIndex: 4, workerOnly: true},
				{name: "alias destination after input", command: "cp " + ordinary + " 0< " + ordinary + "-input " + alias, target: alias, argvIndex: 2, workerOnly: true},
				{name: "invalid alias output", command: "cp --output=" + alias + " ordinary elsewhere", workerOnly: true},
				{name: "move still mutates alias source", command: "mv " + alias + " " + ordinary, target: alias, argvIndex: 1, workerOnly: true},
			} {
				if testCase.workerOnly && role != RoleWorker {
					continue
				}
				// Each case runs through Classify, including wrapper and ownership routing.
				t.Run(string(provider)+"/"+string(role)+"/"+testCase.name, func(t *testing.T) {
					result := Classify(Request{
						Provider:      provider,
						Role:          role,
						CWD:           directory,
						Marker:        MarkerActive,
						ActiveSession: "session",
						Command:       testCase.command,
						ActiveMarkers: []string{marker},
					})
					if testCase.target == "" {
						if result.Decision != DecisionAllow || result.Diagnostic != nil {
							t.Fatalf("input/data command: decision=%s diagnostic=%#v, want allow", result.Decision, result.Diagnostic)
						}
						return
					}
					if result.Decision != DecisionDeny || result.Diagnostic == nil {
						t.Fatalf("write command: decision=%s diagnostic=%#v, want deny", result.Decision, result.Diagnostic)
					}
					diagnostic := result.Diagnostic
					if diagnostic.Code != CodePlanLiveControlDenied || diagnostic.Path != marker {
						t.Errorf("write diagnostic=%#v, want live control path %s", diagnostic, marker)
					}
					segment := testCase.segment
					if segment == 0 {
						segment = 1
					}
					if diagnostic.Token != testCase.target || diagnostic.ArgvIndex != testCase.argvIndex ||
						diagnostic.Segment != segment || diagnostic.ByteOffset != strings.LastIndex(testCase.command, testCase.target) {
						t.Errorf("write coordinates=%#v, want token=%s segment=%d argv_index=%d byte_offset=%d", diagnostic,
							testCase.target, segment, testCase.argvIndex, strings.LastIndex(testCase.command, testCase.target))
					}
				})
			}
		}
	}
}

// TestFiniteWriterOperandPositionsPreserveInputs keeps sed script files and
// truncate size operands out of the writer target set while retaining the
// concrete in-place/destination denial.
//
// Example: `sed -f eci_active -i ordinary` reads the marker as a script, but
// `sed -i ordinary eci_active` writes the marker.
func TestFiniteWriterOperandPositionsPreserveInputs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	marker := filepath.Join(root, "eci_active")
	ordinary := filepath.Join(t.TempDir(), "ordinary")
	if err := os.WriteFile(marker, []byte("scope: active\nsession_id: session\ncwd: "+root+"\n"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if err := os.WriteFile(ordinary, []byte("ordinary\n"), 0o600); err != nil {
		t.Fatalf("write ordinary: %v", err)
	}
	for _, testCase := range []struct {
		name    string
		command string
		want    DecisionKind
		code    DiagnosticCode
	}{
		{name: "sed script file input", command: "sed -f " + marker + " -i " + ordinary, want: DecisionAllow},
		{name: "sed attached expression input", command: "sed -e's/a/b/' -i " + ordinary, want: DecisionAllow},
		{name: "sed marker destination", command: "sed -i " + ordinary + " " + marker, want: DecisionDeny, code: CodePlanLiveControlDenied},
		{name: "truncate size value input", command: "truncate -s " + marker + " " + ordinary, want: DecisionAllow},
		{name: "truncate marker destination", command: "truncate -s 0 " + marker, want: DecisionDeny, code: CodePlanLiveControlDenied},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			result := Classify(Request{
				Provider:      ProviderOmp,
				Role:          RoleWorker,
				CWD:           root,
				Marker:        MarkerActive,
				ActiveSession: "session",
				Command:       testCase.command,
				ActiveMarkers: []string{marker},
			})
			if result.Decision != testCase.want {
				t.Fatalf("decision=%q diagnostic=%#v, want %q", result.Decision, result.Diagnostic, testCase.want)
			}
			if testCase.code != "" && (result.Diagnostic == nil || result.Diagnostic.Code != testCase.code) {
				t.Fatalf("diagnostic=%#v, want %q", result.Diagnostic, testCase.code)
			}
		})
	}
}

func TestDDOutputDestinationsHonorControlOwnership(t *testing.T) {
	t.Parallel()

	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()

			temporaryRoot := t.TempDir()
			sessionDir := filepath.Join(temporaryRoot, "proof", "session")
			if err := os.MkdirAll(sessionDir, 0o700); err != nil {
				t.Fatalf("create session directory: %v", err)
			}
			marker := filepath.Join(sessionDir, "eci_active")
			if err := os.WriteFile(marker, []byte("active\n"), 0o600); err != nil {
				t.Fatalf("write active marker: %v", err)
			}

			protected := Classify(Request{
				Provider:      provider,
				Role:          RoleWorker,
				CWD:           temporaryRoot,
				Marker:        MarkerActive,
				ActiveSession: "session",
				Command:       "dd if=/dev/null of=" + marker,
				ActiveMarkers: []string{marker},
			})
			if protected.Decision != DecisionDeny || protected.Diagnostic == nil {
				t.Fatalf("protected dd output: decision=%q diagnostic=%#v, want deny with diagnostic", protected.Decision, protected.Diagnostic)
			}
			if protected.Diagnostic.Code != CodePlanLiveControlDenied {
				t.Fatalf("protected dd output code=%q, want %q", protected.Diagnostic.Code, CodePlanLiveControlDenied)
			}
			if protected.Diagnostic.Token != marker {
				t.Fatalf("protected dd output token=%q, want %q", protected.Diagnostic.Token, marker)
			}

			reservedState := filepath.Join(sessionDir, "eci_wait")
			reserved := Classify(Request{
				Provider:      provider,
				Role:          RoleWorker,
				CWD:           temporaryRoot,
				Marker:        MarkerActive,
				ActiveSession: "session",
				Command:       "dd if=/dev/null of=" + reservedState,
				ActiveMarkers: []string{marker},
			})
			if reserved.Decision != DecisionDeny || reserved.Diagnostic == nil {
				t.Fatalf("reserved dd output: decision=%q diagnostic=%#v, want deny with diagnostic", reserved.Decision, reserved.Diagnostic)
			}
			if reserved.Diagnostic.Code != CodePlanLiveControlDenied {
				t.Fatalf("reserved dd output code=%q, want %q", reserved.Diagnostic.Code, CodePlanLiveControlDenied)
			}
			if reserved.Diagnostic.Token != reservedState {
				t.Fatalf("reserved dd output token=%q, want %q", reserved.Diagnostic.Token, reservedState)
			}

			coordinatorControl := Classify(Request{
				Provider:      provider,
				Role:          RoleCoordinator,
				CWD:           temporaryRoot,
				Marker:        MarkerActive,
				ActiveSession: "session",
				Command:       "touch " + marker,
				ActiveMarkers: []string{marker},
			})
			if coordinatorControl.Decision != DecisionDeny || coordinatorControl.Diagnostic == nil {
				t.Fatalf("coordinator control write: decision=%q diagnostic=%#v, want deny with diagnostic", coordinatorControl.Decision, coordinatorControl.Diagnostic)
			}
			if coordinatorControl.Diagnostic.Code != CodePlanLiveControlDenied || coordinatorControl.Diagnostic.Predicate != "coordinator-proof-control" {
				t.Fatalf("coordinator control diagnostic=%#v, want %q/coordinator-proof-control", coordinatorControl.Diagnostic, CodePlanLiveControlDenied)
			}

			ordinaryPath := filepath.Join(t.TempDir(), "dd-output")
			ordinary := Classify(Request{
				Provider:      provider,
				Role:          RoleWorker,
				CWD:           temporaryRoot,
				Marker:        MarkerActive,
				ActiveSession: "session",
				Command:       "dd if=/dev/null of=" + ordinaryPath,
				ActiveMarkers: []string{marker},
			})
			if ordinary.Decision != DecisionAllow || ordinary.Diagnostic != nil {
				t.Fatalf("ordinary dd output: decision=%q diagnostic=%#v, want allow without diagnostic", ordinary.Decision, ordinary.Diagnostic)
			}
		})
	}
}

func TestGitArchiveOutputsDeferToProvider(t *testing.T) {
	t.Parallel()

	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()

			temporaryRoot := t.TempDir()
			sessionDir := filepath.Join(temporaryRoot, "proof", "session")
			if err := os.MkdirAll(sessionDir, 0o700); err != nil {
				t.Fatalf("create session directory: %v", err)
			}
			marker := filepath.Join(sessionDir, "eci_active")
			if err := os.WriteFile(marker, []byte("active\n"), 0o600); err != nil {
				t.Fatalf("write active marker: %v", err)
			}

			ordinaryPath := filepath.Join(sessionDir, "archive.tar")
			ordinary := Classify(Request{
				Provider:      provider,
				Role:          RoleWorker,
				CWD:           temporaryRoot,
				Marker:        MarkerActive,
				ActiveSession: "session",
				Command:       "git archive --output=" + ordinaryPath + " HEAD",
				ActiveMarkers: []string{marker},
			})
			if ordinary.Decision != DecisionDefer || ordinary.Diagnostic != nil {
				t.Fatalf("ordinary archive output: decision=%q diagnostic=%#v, want defer without diagnostic", ordinary.Decision, ordinary.Diagnostic)
			}

			control := Classify(Request{
				Provider:      provider,
				Role:          RoleWorker,
				CWD:           temporaryRoot,
				Marker:        MarkerActive,
				ActiveSession: "session",
				Command:       "git archive --output=" + marker + " HEAD",
				ActiveMarkers: []string{marker},
			})
			if control.Decision != DecisionDefer || control.Diagnostic != nil {
				t.Fatalf("control archive output: decision=%q diagnostic=%#v, want defer without diagnostic", control.Decision, control.Diagnostic)
			}

			missingControlPath := filepath.Join(sessionDir, "missing", "eci_active")
			deferredControl := Classify(Request{
				Provider:      provider,
				Role:          RoleWorker,
				CWD:           temporaryRoot,
				Marker:        MarkerActive,
				ActiveSession: "session",
				Command:       "git archive --output=" + missingControlPath + " HEAD",
				ActiveMarkers: []string{marker},
			})
			if deferredControl.Decision != DecisionDefer || deferredControl.Diagnostic != nil {
				t.Fatalf("missing control archive output: decision=%q diagnostic=%#v, want defer without diagnostic", deferredControl.Decision, deferredControl.Diagnostic)
			}

			mutation := Classify(Request{
				Provider:      provider,
				Role:          RoleWorker,
				CWD:           temporaryRoot,
				Marker:        MarkerActive,
				ActiveSession: "session",
				Command:       "git rebase topic",
				ActiveMarkers: []string{marker},
			})
			if mutation.Decision != DecisionDefer || mutation.Diagnostic != nil {
				t.Fatalf("Git mutation: decision=%q diagnostic=%#v, want provider defer", mutation.Decision, mutation.Diagnostic)
			}
		})
	}
}

func TestGenericOutputWriterProofOwnership(t *testing.T) {
	t.Parallel()

	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()

			temporaryRoot := t.TempDir()
			sessionDir := filepath.Join(temporaryRoot, "proof", "session")
			if err := os.MkdirAll(sessionDir, 0o700); err != nil {
				t.Fatalf("create session directory: %v", err)
			}
			marker := filepath.Join(sessionDir, "eci_active")
			if err := os.WriteFile(marker, []byte("active\n"), 0o600); err != nil {
				t.Fatalf("write active marker: %v", err)
			}
			temporaryOutputRoot := t.TempDir()

			commands := []struct {
				name  string
				proof string
				other string
			}{
				{
					name:  "gitleaks report path",
					proof: "gitleaks --report-path=" + filepath.Join(sessionDir, "gitleaks-report.json"),
					other: "gitleaks --report-path=" + filepath.Join(temporaryOutputRoot, "gitleaks-report.json"),
				},
				{
					name:  "sort short output",
					proof: "sort -o " + filepath.Join(sessionDir, "sort.out") + " input.txt",
					other: "sort -o " + filepath.Join(temporaryOutputRoot, "sort.out") + " input.txt",
				},
			}

			for _, testCase := range commands {
				testCase := testCase
				t.Run(testCase.name, func(t *testing.T) {
					proofResult := Classify(Request{
						Provider:      provider,
						Role:          RoleWorker,
						CWD:           temporaryRoot,
						Marker:        MarkerActive,
						ActiveSession: "session",
						Command:       testCase.proof,
						ActiveMarkers: []string{marker},
					})
					if proofResult.Decision != DecisionDefer || proofResult.Diagnostic != nil {
						t.Fatalf("proof output: decision=%q diagnostic=%#v, want defer without diagnostic", proofResult.Decision, proofResult.Diagnostic)
					}

					otherResult := Classify(Request{
						Provider:      provider,
						Role:          RoleWorker,
						CWD:           temporaryRoot,
						Marker:        MarkerActive,
						ActiveSession: "session",
						Command:       testCase.other,
						ActiveMarkers: []string{marker},
					})
					if otherResult.Decision != DecisionAllow || otherResult.Diagnostic != nil {
						t.Fatalf("ordinary output: decision=%q diagnostic=%#v, want allow without diagnostic", otherResult.Decision, otherResult.Diagnostic)
					}
				})
			}
		})
	}
}

// TestResolvedProofControlNames verifies that destination identity, not an
// alias basename, selects reserved-control and ledger classification.
//
// Example: high_level_log.jsonl resolving to an ordinary file is not a ledger.
func TestResolvedProofControlNames(t *testing.T) {
	t.Parallel()
	sessions := []proofSession{{lexical: "/proof/session", resolved: "/proof/session"}}
	for _, classify := range []struct {
		name string
		call func(string, string, []proofSession) bool
	}{
		{"control", isReservedProofControlPath},
		{"ledger", isAppendOnlyLedgerPath},
	} {
		if classify.call("/proof/session/high_level_log.jsonl", "/proof/session/ordinary", sessions) {
			t.Errorf("%s: an ordinary resolved file inherited its alias basename", classify.name)
		}
		if !classify.call("/ordinary/alias", "/proof/session/high_level_log.jsonl", sessions) {
			t.Errorf("%s: a resolved ledger lost its actual basename", classify.name)
		}
		if classify.call("/ordinary/alias", "/proof/session/high_level_log.md", sessions) {
			t.Errorf("%s: the obsolete log basename remains a recognized alias", classify.name)
		}
	}
}

// TestResolvedOutputTargetEffects verifies actual targets and original token
// coordinates across output forms, input redirects, aliases, and caller roles.
//
// Example: sort's output alias to a marker is denied while its input stays readable.
func TestResolvedOutputTargetEffects(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	session := filepath.Join(root, "proof", "session")
	foreign := filepath.Join(root, "proof", "foreign")
	for _, dir := range []string{session, foreign} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"eci_active", "high_level_log.jsonl", "high_level_log.anchor"} {
			contents := []byte("fixture\n")
			if name == "high_level_log.jsonl" {
				contents = []byte("{\"event\":\"decision\",\"summary\":\"entry\"}\n")
			}
			if err := os.WriteFile(filepath.Join(dir, name), contents, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	marker := filepath.Join(session, "eci_active")
	ordinary := filepath.Join(root, "ordinary")
	if err := os.WriteFile(ordinary, []byte("ordinary\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	controlAlias := filepath.Join(root, "control-alias")
	controlHardlink := filepath.Join(root, "control-hardlink")
	ordinaryAlias := filepath.Join(session, "ordinary-alias")
	foreignAlias := filepath.Join(session, "foreign-alias")
	for alias, target := range map[string]string{
		controlAlias: marker, ordinaryAlias: ordinary, foreignAlias: filepath.Join(foreign, "eci_active"),
	} {
		if err := os.Symlink(target, alias); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Link(marker, controlHardlink); err != nil {
		t.Fatal(err)
	}
	for _, role := range []Role{RoleCoordinator, RoleWorker} {
		t.Run(string(role),
			// Exercise the same resolved-target contract for one actual role.
			//
			// Example: both callers may read a marker but may not overwrite it.
			func(t *testing.T) {
				classify :=
					// Classify one command against the current and foreign fixtures.
					//
					// Example: a foreign output alias retains its foreign control target.
					func(command string) Result {
						return Classify(Request{Provider: ProviderOmp, Role: role, CWD: root,
							Marker: MarkerActive, ActiveSession: "session", Command: command,
							ActiveMarkers: []string{marker, filepath.Join(foreign, "eci_active")}})
					}
				for _, note := range []string{"instructions.md", "project-understanding.yaml", "latest-status-report.md"} {
					result := classify("touch " + filepath.Join(session, note))
					if result.Decision != DecisionDefer || result.Diagnostic != nil {
						t.Errorf("coordination note %s: got %s %#v, want ordinary provider route", note, result.Decision, result.Diagnostic)
					}
				}
				for _, link := range []struct {
					name string
					call func(string, string) error
				}{
					{"symlink", os.Symlink}, {"hardlink", os.Link},
				} {
					notePath := filepath.Join(session, "instructions.md")
					if err := link.call(marker, notePath); err != nil {
						t.Fatal(err)
					}
					for _, command := range []string{"touch " + notePath, "sort -o " + notePath + " < " + ordinary} {
						result := classify(command)
						if result.Diagnostic == nil || result.Diagnostic.Code != CodePlanLiveControlDenied || result.Diagnostic.Path != marker {
							t.Errorf("note-named %s: %q got %s %#v, want actual marker denial", link.name, command, result.Decision, result.Diagnostic)
						}
					}
					if role == RoleWorker {
						result := classify("env touch " + notePath)
						if result.Diagnostic == nil || result.Diagnostic.Code != CodePlanLiveControlDenied || result.Diagnostic.Path != marker {
							t.Errorf("wrapped note-named %s: got %s %#v, want actual marker denial", link.name, result.Decision, result.Diagnostic)
						}
					}
					if err := os.Remove(notePath); err != nil {
						t.Fatal(err)
					}
				}
				for _, target := range []string{marker, controlAlias, controlHardlink, foreignAlias} {
					for _, form := range []struct {
						command string
						index   int
					}{
						{"touch " + target, 1},
						{"touch " + filepath.Join(session, "ordinary-output") + " " + target, 2},
						{"sort -o " + target + " < " + ordinary, 2},
						{"sort -o < " + ordinary + " " + target, 2},
						{"sort < " + ordinary + " -o " + target, 2},
						{"sort -o" + target + " < " + ordinary, 1},
						{"sort --output=" + target + " < " + ordinary, 1},
						{"sort --output='" + target + "' < " + ordinary, 1},
						{"sort \"--output=" + target + "\" < " + ordinary, 1},
						{"sort --out'put='" + target + " < " + ordinary, 1},
						{"sort --output=\\" + target + " < " + ordinary, 1},
						{"sort 'é' --output='" + target + "' < " + ordinary, 2},
						{"sort --output='" + target + "' | cat", 1},
						{"gitleaks --report-path " + filepath.Join(session, "ordinary-output") + " --report-path " + target, 4},
					} {
						result := classify(form.command)
						if result.Decision != DecisionDeny || result.Diagnostic == nil {
							t.Errorf("%q: got %s, want concrete control denial", form.command, result.Decision)
							continue
						}
						diagnostic := result.Diagnostic
						wantPath := marker
						if target == foreignAlias {
							wantPath = filepath.Join(foreign, "eci_active")
						}
						if diagnostic.Code != CodePlanLiveControlDenied || diagnostic.Path != wantPath ||
							diagnostic.ArgvIndex != form.index || diagnostic.Token != target ||
							diagnostic.ByteOffset != strings.Index(form.command, target) {
							t.Errorf("%q: wrong target or coordinates: %#v", form.command, diagnostic)
						}
					}
				}
				for _, command := range []string{
					"touch " + ordinaryAlias,
					"sort -o " + ordinaryAlias + " < " + marker,
					"sort < --output=" + marker + " " + ordinary,
					"sort -- -o " + controlAlias,
					"sort < -- -o " + ordinaryAlias,
					"diff --to-file=" + marker + " " + ordinary,
					"diff --to-file " + marker + " " + ordinary,
					"cp " + marker + " 0<" + marker + " " + ordinary,
					"grep -o " + controlAlias + " " + ordinary,
					"rg -o " + controlAlias + " " + ordinary,
					"printf '%s\\n' --output " + controlAlias,
					"tool --output " + controlAlias,
					"wipefs --output " + controlAlias,
				} {
					result := classify(command)
					if result.Decision != DecisionAllow || result.Diagnostic != nil {
						t.Errorf("ordinary/read %q: got %s %#v", command, result.Decision, result.Diagnostic)
					}
				}
				for _, test := range []struct {
					command string
					code    DiagnosticCode
				}{
					{"sort -o " + filepath.Join(session, "high_level_log.jsonl") + " < " + ordinary, CodeLedgerRewriteDenied},
					{"sort -o " + filepath.Join(foreign, "high_level_log.jsonl") + " < " + ordinary, CodeLedgerForeignSessionDenied},
					{"diff --to-file=" + ordinary + " " + ordinary + " > " + marker, CodePlanLiveControlDenied},
					{"grep -o " + ordinary + " " + ordinary + " > " + marker, CodePlanLiveControlDenied},
					{"tool --output " + filepath.Join(session, "ordinary-output") + " > " + marker, CodePlanLiveControlDenied},
					{"touch " + marker + " > " + filepath.Join(session, "high_level_log.anchor"), CodeLedgerAnchorWriteDenied},
				} {
					result := classify(test.command)
					if result.Diagnostic == nil || result.Diagnostic.Code != test.code {
						t.Errorf("%q: got %s %#v, want %s", test.command, result.Decision, result.Diagnostic, test.code)
					}
				}
			},
		)
	}
}

// TestFilesystemInformationalModes verifies that documented non-writing modes
// do not inherit the destructive effect of the utility's other operations.
//
// Example: mkfs --help is ordinary, but mkfs -V /dev/example still formats.
func TestFilesystemInformationalModes(t *testing.T) {
	for _, role := range []Role{RoleCoordinator, RoleWorker} {
		for _, command := range []string{"mkfs --help", "mkfs --version", "wipefs --help", "wipefs -V", "wipefs /dev/example", "wipefs --no-act --all /dev/example"} {
			result := Classify(Request{Provider: ProviderOmp, Role: role, CWD: t.TempDir(), Marker: MarkerActive, Command: command})
			if result.Decision == DecisionDeny {
				t.Errorf("%s %q: unexpected denial %#v", role, command, result.Diagnostic)
			}
		}
		for _, command := range []string{"mkfs -V /dev/example", "wipefs --all /dev/example", "wipefs --offset 0 /dev/example"} {
			result := Classify(Request{Provider: ProviderOmp, Role: role, CWD: t.TempDir(), Marker: MarkerActive, Command: command})
			if result.Diagnostic == nil || result.Diagnostic.Code != CodeBroadDestructiveDenied {
				t.Errorf("%s %q: want destructive effect, got %#v", role, command, result.Diagnostic)
			}
		}
	}
}

func TestReviewGateCapabilityDefersToProviderAdapters(t *testing.T) {
	t.Parallel()

	protectedCommands := []string{
		"eci-review-gate.sh commit test-session",
		"/opt/eci/hooks/eci-review-gate.sh commit test-session",
		"hooks/eci-review-gate.sh final test-session",
		"bash /opt/eci/hooks/eci-review-gate.sh commit test-session",
		"bash -e hooks/eci-review-gate.sh final test-session",
		"bash -x hooks/eci-review-gate.sh off test-session",
		"bash -O extglob hooks/eci-review-gate.sh prewrite test-session",
		"env FOO=bar bash -- hooks/eci-review-gate.sh prewrite test-session",
		"bash /opt/eci/hooks/eci-review-gate.sh unknown test-session",
		"bash /opt/eci/hooks/eci-review-gate.sh commit",
	}
	ordinaryCommands := []string{
		"./tools/eci-review-gate.sh verify",
		"bash ./tools/eci-review-gate.sh verify",
		"bash /provider/eci-review-gate.sh verify",
		"bash scripts/ordinary-review.sh",
		"printf hooks/eci-review-gate.sh",
		"bash -O eci-review-gate.sh commit test-session",
	}
	malformedInterpreterCommands := []string{
		"bash -c eci-review-gate.sh commit test-session",
	}

	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()

			for _, command := range protectedCommands {
				result := Classify(Request{
					Provider:      provider,
					Role:          RoleWorker,
					CWD:           "/opt/eci",
					Marker:        MarkerActive,
					ActiveSession: "test-session",
					Command:       command,
				})
				if result.Decision != DecisionDefer || result.Diagnostic != nil {
					t.Fatalf("protected %q: decision=%q diagnostic=%#v, want deferred without diagnostic", command, result.Decision, result.Diagnostic)
				}
			}

			for _, command := range ordinaryCommands {
				result := Classify(Request{
					Provider:      provider,
					Role:          RoleWorker,
					CWD:           "/opt/eci",
					Marker:        MarkerActive,
					ActiveSession: "test-session",
					Command:       command,
				})
				if result.Decision != DecisionAllow || result.Diagnostic != nil {
					t.Fatalf("ordinary %q: decision=%q diagnostic=%#v, want allowed without diagnostic", command, result.Decision, result.Diagnostic)
				}
			}

			for _, command := range malformedInterpreterCommands {
				result := Classify(Request{
					Provider:      provider,
					Role:          RoleWorker,
					CWD:           "/opt/eci",
					Marker:        MarkerActive,
					ActiveSession: "test-session",
					Command:       command,
				})
				if result.Decision != DecisionAllow || result.Diagnostic != nil {
					t.Fatalf("ordinary interpreter form %q: decision=%q diagnostic=%#v, want allow", command, result.Decision, result.Diagnostic)
				}
			}
		})
	}
}

func TestLifecycleScriptCapabilityDefersToProviderAdapters(t *testing.T) {
	t.Parallel()

	protectedCommands := []string{
		"bash -O extglob /opt/eci/hooks/stop-gate.sh",
		"bash --noprofile /opt/eci/hooks/stop-gate.sh",
		"bash -n hooks/stop-gate.sh",
		"sh -e /opt/eci/hooks/stop-gate.sh",
		"bash /opt/eci/hooks/eci-active-gate.sh",
		"env -u ECI_ROLE bash /opt/eci/bin/eci-active status",
	}
	ordinaryCommands := []string{
		"bash scripts/stop-gate.sh",
		"bash scripts/eci-active status",
		"bash scripts/ordinary-control.sh",
		"printf hooks/stop-gate.sh",
		"printf /opt/eci/bin/eci-active",
	}

	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()

			for _, command := range protectedCommands {
				result := Classify(Request{
					Provider:      provider,
					Role:          RoleWorker,
					CWD:           "/opt/eci",
					Marker:        MarkerActive,
					ActiveSession: "test-session",
					Command:       command,
				})
				if result.Decision != DecisionDefer || result.Diagnostic != nil {
					t.Fatalf("protected %q: decision=%q diagnostic=%#v, want deferred without diagnostic", command, result.Decision, result.Diagnostic)
				}
			}

			for _, command := range ordinaryCommands {
				result := Classify(Request{
					Provider:      provider,
					Role:          RoleWorker,
					CWD:           "/opt/eci",
					Marker:        MarkerActive,
					ActiveSession: "test-session",
					Command:       command,
				})
				if result.Decision != DecisionAllow || result.Diagnostic != nil {
					t.Fatalf("ordinary %q: decision=%q diagnostic=%#v, want allowed without diagnostic", command, result.Decision, result.Diagnostic)
				}
			}
		})
	}
}

// TestActiveWorkerLifecycleIdentityAllowsReadOnlyCopies verifies that copied
// lifecycle executable identity does not turn status/help discovery into a
// worker ownership denial.
//
// Example: env FOO=bar /tmp/eci-active-copy status is ordinary read-only
// work, while nested-exit remains a lifecycle control operation.
func TestActiveWorkerLifecycleIdentityAllowsReadOnlyCopies(t *testing.T) {
	if os.Getenv("ECI_TEST_LIFECYCLE_IDENTITY_HOME_SYMLINK_CASE") == "1" {
		runLifecycleIdentityHomeSymlinkScenario(t)
		return
	}

	source := filepath.Join(providerHome(ProviderOmp), "bin", "eci-active")
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read canonical lifecycle executable: %v", err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatalf("stat canonical lifecycle executable: %v", err)
	}
	directory := t.TempDir()
	providerRoot := filepath.Join(directory, "provider-root")
	providerBin := filepath.Join(providerRoot, "bin")
	if err := os.MkdirAll(providerBin, 0o755); err != nil {
		t.Fatalf("create provider bin: %v", err)
	}
	canonicalActive := filepath.Join(providerBin, "eci-active")
	if err := os.WriteFile(canonicalActive, contents, info.Mode().Perm()); err != nil {
		t.Fatalf("write canonical lifecycle executable: %v", err)
	}
	for _, alias := range []string{"eci-review-gate", "eci-stage"} {
		path := filepath.Join(providerBin, alias)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write canonical %s: %v", alias, err)
		}
	}
	homeRoot := filepath.Join(directory, "home")
	if err := os.MkdirAll(homeRoot, 0o755); err != nil {
		t.Fatalf("create lexical home root: %v", err)
	}
	providerAliasParent := filepath.Join(homeRoot, ".omp", "agent")
	if err := os.MkdirAll(providerAliasParent, 0o755); err != nil {
		t.Fatalf("create provider home parent: %v", err)
	}
	providerAlias := filepath.Join(providerAliasParent, "eci")
	if err := os.Symlink(providerRoot, providerAlias); err != nil {
		t.Fatalf("create provider home alias: %v", err)
	}
	copyPath := filepath.Join(directory, "eci-active-copy")
	if err := os.WriteFile(copyPath, contents, info.Mode().Perm()); err != nil {
		t.Fatalf("write lifecycle copy: %v", err)
	}
	ordinaryPath := filepath.Join(directory, "ordinary-worker")
	if err := os.WriteFile(ordinaryPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write ordinary worker: %v", err)
	}
	arbitraryStage := filepath.Join(directory, "eci-stage")
	arbitraryReviewGate := filepath.Join(directory, "eci-review-gate")
	for _, path := range []string{arbitraryStage, arbitraryReviewGate} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write arbitrary basename control: %v", err)
		}
	}

	child := exec.Command(os.Args[0], "-test.run=^TestActiveWorkerLifecycleIdentityAllowsReadOnlyCopies$")
	child.Env = append(
		os.Environ(),
		"ECI_TEST_LIFECYCLE_IDENTITY_HOME_SYMLINK_CASE=1",
		"HOME="+homeRoot,
		"ECI_HOME=",
		"ECI_TEST_LIFECYCLE_IDENTITY_DIRECTORY="+directory,
		"ECI_TEST_LIFECYCLE_IDENTITY_COPY="+copyPath,
		"ECI_TEST_LIFECYCLE_IDENTITY_ORDINARY="+ordinaryPath,
		"ECI_TEST_LIFECYCLE_IDENTITY_ARBITRARY_STAGE="+arbitraryStage,
		"ECI_TEST_LIFECYCLE_IDENTITY_ARBITRARY_REVIEW_GATE="+arbitraryReviewGate,
	)
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("run symlink-home lifecycle identity scenario: %v\n%s", err, output)
	}
}

// runLifecycleIdentityHomeSymlinkScenario verifies that lifecycle identity
// remains relevant to control mutations after a child process configures
// HOME/.omp/agent/eci as a symlink, but not to status/help discovery.
//
// Example: env -- <symlink-home>/bin/eci-active status remains ordinary.
func runLifecycleIdentityHomeSymlinkScenario(t *testing.T) {
	directory := os.Getenv("ECI_TEST_LIFECYCLE_IDENTITY_DIRECTORY")
	copyPath := os.Getenv("ECI_TEST_LIFECYCLE_IDENTITY_COPY")
	ordinaryPath := os.Getenv("ECI_TEST_LIFECYCLE_IDENTITY_ORDINARY")
	arbitraryStage := os.Getenv("ECI_TEST_LIFECYCLE_IDENTITY_ARBITRARY_STAGE")
	arbitraryReviewGate := os.Getenv("ECI_TEST_LIFECYCLE_IDENTITY_ARBITRARY_REVIEW_GATE")
	for name, value := range map[string]string{
		"directory":             directory,
		"copy":                  copyPath,
		"ordinary":              ordinaryPath,
		"arbitrary stage":       arbitraryStage,
		"arbitrary review gate": arbitraryReviewGate,
	} {
		if value == "" {
			t.Fatalf("missing symlink lifecycle test %s", name)
		}
	}

	providerBin := filepath.Join(providerHome(ProviderOmp), "bin")
	protectedCommands := []struct {
		name    string
		command string
	}{
		{name: "copied env no args", command: "env " + copyPath},
		{name: "copied timeout no args", command: "timeout 5 " + copyPath},
		{name: "copied direct nested exit", command: copyPath + " nested-exit"},
		{name: "copied env isolated", command: "env -i " + copyPath + " nested-exit"},
		{name: "canonical active env no args", command: "env " + filepath.Join(providerBin, "eci-active")},
	}
	readOnlyLifecycleCommands := []struct {
		name    string
		command string
	}{
		{name: "copied direct status", command: copyPath + " status"},
		{name: "copied direct help", command: copyPath + " --help"},
		{name: "copied env assignment", command: "env FOO=bar " + copyPath + " status"},
		{name: "copied env separator", command: "env -- " + copyPath + " status"},
		{name: "copied env unset", command: "env -u PATH " + copyPath + " status"},
		{name: "copied stdbuf", command: "stdbuf -oL " + copyPath + " status"},
		{name: "copied busybox", command: "busybox -- " + copyPath + " status"},
		{name: "copied prlimit", command: "prlimit --nofile=1024 " + copyPath + " status"},
		{name: "copied chronic", command: "chronic " + copyPath + " status"},
		{name: "canonical active status", command: filepath.Join(providerBin, "eci-active") + " status"},
		{name: "canonical active help", command: filepath.Join(providerBin, "eci-active") + " --help"},
		{name: "canonical active env status", command: "env FOO=bar " + filepath.Join(providerBin, "eci-active") + " status"},
		{name: "canonical review gate status", command: filepath.Join(providerBin, "eci-review-gate") + " status"},
		{name: "canonical stage status", command: filepath.Join(providerBin, "eci-stage") + " status"},
	}
	ordinaryCommands := []struct {
		name    string
		command string
	}{
		{name: "ordinary direct", command: ordinaryPath},
		{name: "ordinary env assignment", command: "env FOO=bar " + ordinaryPath},
		{name: "ordinary env separator", command: "env -- " + ordinaryPath},
		{name: "ordinary env isolated", command: "env -i " + ordinaryPath},
		{name: "ordinary env unset", command: "env -u PATH " + ordinaryPath},
		{name: "ordinary stdbuf", command: "stdbuf -oL " + ordinaryPath},
		{name: "ordinary busybox", command: "busybox -- " + ordinaryPath},
		{name: "ordinary prlimit", command: "prlimit --nofile=1024 " + ordinaryPath},
		{name: "ordinary chronic", command: "chronic " + ordinaryPath},
		{name: "arbitrary eci stage basename", command: arbitraryStage + " status"},
		{name: "arbitrary eci review gate basename", command: arbitraryReviewGate + " status"},
	}

	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		for _, testCase := range protectedCommands {
			testCase := testCase
			t.Run(string(provider)+"/protected/"+testCase.name, func(t *testing.T) {
				request := activeWorker(testCase.command)
				request.Provider = provider
				request.CWD = directory
				result := Classify(request)
				if result.Decision != DecisionDeny || result.Diagnostic == nil {
					t.Fatalf("%q: decision=%q diagnostic=%#v, want worker lifecycle denial", testCase.command, result.Decision, result.Diagnostic)
				}
				if result.Diagnostic.Code != CodeControlOwnerRequired || result.Diagnostic.Operation != "worker-control" || result.Diagnostic.Predicate != "worker-lifecycle-control" {
					t.Fatalf("%q: diagnostic=%#v, want worker lifecycle control denial", testCase.command, result.Diagnostic)
				}
				if len(result.Capabilities) != 0 {
					t.Fatalf("%q: capabilities=%v, want none for protected lifecycle control", testCase.command, result.Capabilities)
				}
			})
		}
		for _, testCase := range readOnlyLifecycleCommands {
			testCase := testCase
			t.Run(string(provider)+"/read-only/"+testCase.name, func(t *testing.T) {
				request := activeWorker(testCase.command)
				request.Provider = provider
				request.CWD = directory
				result := Classify(request)
				if result.Decision == DecisionDeny || result.Diagnostic != nil {
					t.Fatalf("%q: decision=%q diagnostic=%#v, want ordinary allow or defer", testCase.command, result.Decision, result.Diagnostic)
				}
			})
		}
		for _, testCase := range ordinaryCommands {
			testCase := testCase
			t.Run(string(provider)+"/ordinary/"+testCase.name, func(t *testing.T) {
				request := activeWorker(testCase.command)
				request.Provider = provider
				request.CWD = directory
				result := Classify(request)
				if result.Decision != DecisionAllow || result.Diagnostic != nil {
					t.Fatalf("%q: decision=%q diagnostic=%#v, want ordinary wrapped allow", testCase.command, result.Decision, result.Diagnostic)
				}
			})
		}
	}
}

// TestCompoundPlanTopologyPreservesLosslessOrder verifies that an admitted
// compound command returns its original ordered direct-command slices and
// operators for the Bash adapter to validate through existing direct routes.
//
// Example: `sed && printf || sed; printf | sha256sum` preserves each operator
// and the exact bytes surrounding it rather than reconstructing shell words.
func TestCompoundPlanTopologyPreservesLosslessOrder(t *testing.T) {
	t.Parallel()

	command := "sed -n '1p' hooks/validate-bash.sh && printf '&& literal' || sed -n '1p' hooks/validate-bash.sh; printf '| literal' | sha256sum"
	result := Classify(Request{
		Provider:      ProviderOmp,
		Role:          RoleCoordinator,
		CWD:           "/workspace/repo",
		Marker:        MarkerActive,
		ActiveSession: "test-session",
		Command:       command,
	})
	if result.Decision != DecisionAllow || result.Diagnostic != nil {
		t.Fatalf("decision=%q diagnostic=%#v, want admitted compound plan", result.Decision, result.Diagnostic)
	}
	if result.Plan == nil {
		t.Fatal("missing compound plan topology")
	}
	wantSegments := []string{
		"sed -n '1p' hooks/validate-bash.sh ",
		" printf '&& literal' ",
		" sed -n '1p' hooks/validate-bash.sh",
		" printf '| literal' ",
		" sha256sum",
	}
	if len(result.Plan.Segments) != len(wantSegments) {
		t.Fatalf("segment count=%d, want %d: %#v", len(result.Plan.Segments), len(wantSegments), result.Plan.Segments)
	}
	for index, want := range wantSegments {
		if got := result.Plan.Segments[index].Command; got != want {
			t.Fatalf("segment %d command=%q, want %q", index+1, got, want)
		}
	}
	wantOperators := []string{"&&", "||", ";", "|"}
	if len(result.Plan.Operators) != len(wantOperators) {
		t.Fatalf("operator count=%d, want %d: %#v", len(result.Plan.Operators), len(wantOperators), result.Plan.Operators)
	}
	for index, want := range wantOperators {
		if got := result.Plan.Operators[index]; got != want {
			t.Fatalf("operator %d=%q, want %q", index+1, got, want)
		}
	}
}

// TestCompoundPlanSegmentCWDUsesBoundedLiteralState verifies that compound
// topology carries an explicit, planner-owned CWD fact for every segment.
// Only a verified literal cd reached after an unconditional semicolon or
// newline can establish the next segment's directory; uncertain shell state
// is represented as unknown rather than borrowing the callback CWD.
//
// Example: an unsearchable absolute `cd` is unknown even when its directory
// entry exists.
func TestCompoundPlanSegmentCWDUsesBoundedLiteralState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	inner := filepath.Join(root, "inner")
	if err := os.Mkdir(inner, 0o700); err != nil {
		t.Fatalf("create inner directory: %v", err)
	}
	logicalLink := filepath.Join(root, "logical-link")
	if err := os.Symlink(inner, logicalLink); err != nil {
		t.Fatalf("create logical directory link: %v", err)
	}
	physicalInner, err := filepath.EvalSymlinks(inner)
	if err != nil {
		t.Fatalf("resolve physical inner directory: %v", err)
	}
	physicalParent := filepath.Join(root, "real")
	if err := os.Mkdir(physicalParent, 0o700); err != nil {
		t.Fatalf("create physical parent: %v", err)
	}
	if err := os.Symlink(filepath.Join(physicalParent, "child"), filepath.Join(root, "link")); err != nil {
		t.Fatalf("create physical traversal link: %v", err)
	}
	if err := os.Mkdir(filepath.Join(physicalParent, "child"), 0o700); err != nil {
		t.Fatalf("create physical traversal child: %v", err)
	}
	physicalParent, err = filepath.EvalSymlinks(physicalParent)
	if err != nil {
		t.Fatalf("resolve physical traversal parent: %v", err)
	}
	unsearchable := filepath.Join(root, "unsearchable")
	if err := os.Mkdir(unsearchable, 0o700); err != nil {
		t.Fatalf("create unsearchable directory: %v", err)
	}
	if err := os.Chmod(unsearchable, 0o600); err != nil {
		t.Fatalf("remove search permission from directory: %v", err)
	}
	cdPathRoot := filepath.Join(root, "cdpath")
	cdPathTarget := filepath.Join(cdPathRoot, "project")
	if err := os.MkdirAll(cdPathTarget, 0o700); err != nil {
		t.Fatalf("create CDPATH target: %v", err)
	}
	type wantFact struct {
		cwd        string
		known      bool
		candidates []string
	}
	testCases := []struct {
		name      string
		cwd       string
		cdPath    string
		cdPathSet bool
		command   string
		want      []wantFact
	}{
		{
			name:    "semicolon literal absolute cd",
			cwd:     root,
			command: "cd " + inner + "; printf after",
			want:    []wantFact{{cwd: root, known: true, candidates: []string{root}}, {cwd: inner, known: true, candidates: []string{inner}}},
		},
		{
			name:    "newline literal absolute cd",
			cwd:     root,
			command: "cd " + inner + "\nprintf after",
			want:    []wantFact{{cwd: root, known: true, candidates: []string{root}}, {cwd: inner, known: true, candidates: []string{inner}}},
		},
		{
			name:    "leading assignment literal cd",
			cwd:     root,
			command: "ECI_TRACE=present cd " + inner + "; printf after",
			want:    []wantFact{{cwd: root, known: true, candidates: []string{root}}, {cwd: inner, known: true, candidates: []string{inner}}},
		},
		{
			name:    "relative literal cd",
			cwd:     root,
			command: "cd inner; printf after",
			want:    []wantFact{{cwd: root, known: true, candidates: []string{root}}, {cwd: inner, known: true, candidates: []string{inner}}},
		},
		{
			name:      "CDPATH resolves relative cd",
			cwd:       root,
			cdPath:    cdPathRoot,
			cdPathSet: true,
			command:   "cd project; printf after",
			want:      []wantFact{{cwd: root, known: true, candidates: []string{root}}, {cwd: cdPathTarget, known: true, candidates: []string{cdPathTarget}}},
		},
		{
			name:    "logical cd preserves symlink spelling",
			cwd:     root,
			command: "cd -L " + logicalLink + "; printf after",
			want:    []wantFact{{cwd: root, known: true, candidates: []string{root}}, {cwd: logicalLink, known: true, candidates: []string{logicalLink}}},
		},
		{
			name:    "physical cd resolves symlink",
			cwd:     root,
			command: "cd -P " + logicalLink + "; printf after",
			want:    []wantFact{{cwd: root, known: true, candidates: []string{root}}, {cwd: physicalInner, known: true, candidates: []string{physicalInner}}},
		},
		{
			name:    "physical cd resolves before lexical parent",
			cwd:     root,
			command: "cd -P link/..; printf after",
			want:    []wantFact{{cwd: root, known: true, candidates: []string{root}}, {cwd: physicalParent, known: true, candidates: []string{physicalParent}}},
		},
		{
			name:    "slash qualified cd is external",
			cwd:     root,
			command: "/tmp/cd " + inner + "; printf after",
			want:    []wantFact{{cwd: root, known: true, candidates: []string{root}}, {cwd: root, known: true, candidates: []string{root}}},
		},
		{
			name:    "argument mentioning cd does not change state",
			cwd:     root,
			command: "helper cd " + inner + "; printf after",
			want:    []wantFact{{cwd: root, known: true, candidates: []string{root}}, {cwd: root, known: true, candidates: []string{root}}},
		},
		{
			name:    "redirected cd keeps reachable state conservative",
			cwd:     root,
			command: "cd " + inner + " > redirect-output; printf after",
			want:    []wantFact{{cwd: root, known: true, candidates: []string{root}}, {cwd: "", known: false, candidates: []string{root, inner}}},
		},
		{
			name:    "pushd and popd restore directory stack",
			cwd:     root,
			command: "pushd " + inner + "; printf inside; popd; printf outside",
			want: []wantFact{
				{cwd: root, known: true},
				{cwd: inner, known: true},
				{cwd: inner, known: true},
				{cwd: root, known: true},
			},
		},
		{
			name:    "parenthesized directory change restores outer state",
			cwd:     root,
			command: "(cd " + inner + "; printf inside); printf outside",
			want: []wantFact{
				{cwd: root, known: true},
				{cwd: inner, known: true},
				{cwd: root, known: true},
			},
		},
		{
			name:    "brace directory change keeps outer state",
			cwd:     root,
			command: "{ cd " + inner + "; printf inside; }; printf outside",
			want: []wantFact{
				{cwd: root, known: true},
				{cwd: inner, known: true},
				{cwd: inner, known: true},
			},
		},
		{
			name:    "conditional cd poisons state",
			cwd:     root,
			command: "cd " + inner + " && printf after",
			want:    []wantFact{{cwd: root, known: true}, {cwd: "", known: false}},
		},
		{
			name:    "conditional or cd poisons state",
			cwd:     root,
			command: "cd " + inner + " || printf after",
			want:    []wantFact{{cwd: root, known: true}, {cwd: "", known: false}},
		},
		{
			name:    "pipeline cd poisons state",
			cwd:     root,
			command: "cd " + inner + " | printf after",
			want:    []wantFact{{cwd: root, known: true}, {cwd: "", known: false}},
		},
		{
			name:    "background cd poisons state",
			cwd:     root,
			command: "cd " + inner + " & printf after",
			want:    []wantFact{{cwd: root, known: true}, {cwd: "", known: false}},
		},
		{
			name:    "dynamic cd remains unknown",
			cwd:     root,
			command: "cd \"$ECI_TARGET\"; printf after",
			want:    []wantFact{{cwd: root, known: true}, {cwd: "", known: false}},
		},
		{
			name:    "unsupported pushd option remains unknown",
			cwd:     root,
			command: "pushd -n " + inner + "; printf after",
			want:    []wantFact{{cwd: root, known: true}, {cwd: "", known: false}},
		},
		{
			name:    "unsearchable literal cd remains unknown",
			cwd:     root,
			command: "cd " + unsearchable + "; printf after",
			want:    []wantFact{{cwd: root, known: true}, {cwd: "", known: false}},
		},
		{
			name:    "command cd remains parent builtin",
			cwd:     root,
			command: "command cd " + inner + "; printf after",
			want:    []wantFact{{cwd: root, known: true, candidates: []string{root}}, {cwd: inner, known: true, candidates: []string{inner}}},
		},
		{
			name:    "skipped prior cd remains unknown",
			cwd:     root,
			command: "false && cd " + inner + "; printf after",
			want: []wantFact{
				{cwd: root, known: true, candidates: []string{root}},
				{cwd: root, known: true, candidates: []string{root}},
				{cwd: root, known: true, candidates: []string{root}},
			},
		},
		{
			name:    "unknown callback CWD stays unknown",
			cwd:     filepath.Join(root, "missing"),
			command: "printf before; printf after",
			want:    []wantFact{{cwd: "", known: false}, {cwd: "", known: false}},
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			request := activeWorker(testCase.command)
			request.CWD = testCase.cwd
			request.CDPath = testCase.cdPath
			request.CDPathSet = testCase.cdPathSet
			result := Classify(request)
			if result.Decision != DecisionAllow || result.Diagnostic != nil {
				t.Fatalf("decision=%q diagnostic=%#v, want allow", result.Decision, result.Diagnostic)
			}
			if result.Plan == nil {
				t.Fatal("missing compound plan topology")
			}
			if len(result.Plan.Segments) != len(testCase.want) {
				t.Fatalf("segments=%#v, want %d", result.Plan.Segments, len(testCase.want))
			}
			for index, want := range testCase.want {
				got := result.Plan.Segments[index]
				if got.CWD != want.cwd || got.CWDKnown != want.known ||
					want.candidates != nil && !slices.Equal(got.CWDCandidates, want.candidates) {
					t.Errorf("segment %d CWD=(%q, known=%t, candidates=%#v), want (%q, known=%t, candidates=%#v)", index+1, got.CWD, got.CWDKnown, got.CWDCandidates, want.cwd, want.known, want.candidates)
				}
			}
		})
	}
}

// TestCompoundPlanSegmentDecisionsMatchDirectPlans verifies that a coordinator
// compound preserves every segment that has a standalone planner allow, so
// the Bash adapter can send each raw segment through the same named route.
//
// Example: `go test ./...; id` is admitted when both direct argv forms are
// admitted, and the returned topology retains both direct command slices.
func TestCompoundPlanSegmentDecisionsMatchDirectPlans(t *testing.T) {
	t.Parallel()

	directCommands := []string{"go test ./...", "id"}
	for _, role := range []Role{RoleCoordinator, RoleWorker} {
		request := Request{
			Provider:      ProviderOmp,
			Role:          role,
			CWD:           "/workspace/repo",
			Marker:        MarkerActive,
			ActiveSession: "test-session",
		}
		for _, command := range directCommands {
			directRequest := request
			directRequest.Command = command
			directResult := Classify(directRequest)
			if directResult.Decision != DecisionAllow || directResult.Diagnostic != nil {
				t.Fatalf("%s direct command %q: decision=%q diagnostic=%#v, want allow", role, command, directResult.Decision, directResult.Diagnostic)
			}
		}

		request.Command = strings.Join(directCommands, "; ")
		compound := Classify(request)
		if compound.Decision != DecisionAllow || compound.Diagnostic != nil {
			t.Fatalf("%s compound decision=%q diagnostic=%#v, want allow", role, compound.Decision, compound.Diagnostic)
		}
		if compound.Plan == nil {
			t.Fatalf("%s missing compound plan topology", role)
		}
		if len(compound.Plan.Segments) != len(directCommands) {
			t.Fatalf("%s segment count=%d, want %d", role, len(compound.Plan.Segments), len(directCommands))
		}
	}
}

// TestCompoundConditionalCWDJoinsReachableBranchExits verifies that a bounded
// literal if form prunes a known-false branch before joining the reachable
// directory exit instead of inventing an unreachable candidate.
//
// Example: `if false; then cd inner; else cd outer; fi; printf ok` carries
// only outer into printf.
func TestCompoundConditionalCWDJoinsReachableBranchExits(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	inner := filepath.Join(root, "inner")
	if err := os.Mkdir(inner, 0o700); err != nil {
		t.Fatalf("create inner directory: %v", err)
	}

	request := activeWorker("if false; then cd inner; else cd " + root + "; fi; printf after")
	request.CWD = root
	result := Classify(request)
	if result.Decision != DecisionAllow || result.Diagnostic != nil {
		t.Fatalf("decision=%q diagnostic=%#v, want allow", result.Decision, result.Diagnostic)
	}
	if result.Plan == nil || len(result.Plan.Segments) < 2 {
		t.Fatalf("plan=%#v, want conditional topology", result.Plan)
	}
	last := result.Plan.Segments[len(result.Plan.Segments)-1]
	if !last.CWDKnown || last.CWD != root || !slices.Equal(last.CWDCandidates, []string{root}) {
		t.Fatalf("final conditional CWD=(%q, known=%t, candidates=%#v), want exact %q", last.CWD, last.CWDKnown, last.CWDCandidates, root)
	}
}

// TestCompoundConditionalSplitBodyCWDTransitions carries state through body
// segments that follow a standalone `then` or `else` marker. The branch exit
// cache must receive each actual body transition before `fi` joins it.
//
// Example: `if true; then; cd inner; fi; printf after` keeps inner for the
// final segment, while a false branch keeps the outer directory.
func TestCompoundConditionalSplitBodyCWDTransitions(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	inner := filepath.Join(root, "inner")
	if err := os.Mkdir(inner, 0o700); err != nil {
		t.Fatalf("create inner directory: %v", err)
	}
	for _, testCase := range []struct {
		name       string
		command    string
		known      bool
		cwd        string
		candidates []string
	}{
		{
			name:    "true split body",
			command: "if true; then; cd inner; fi; printf after",
			known:   true,
			cwd:     inner,
		},
		{
			name:    "false split body with else",
			command: "if false; then; cd inner; else; cd " + root + "; fi; printf after",
			known:   true,
			cwd:     root,
		},
		{
			name:       "unknown split body union",
			command:    "if test -e flag; then; cd inner; else; cd " + root + "; fi; printf after",
			candidates: []string{inner, root},
		},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			request := activeWorker(testCase.command)
			request.CWD = root
			result := Classify(request)
			if result.Decision != DecisionAllow || result.Diagnostic != nil || result.Plan == nil {
				t.Fatalf("decision=%q diagnostic=%#v plan=%#v, want allow with topology", result.Decision, result.Diagnostic, result.Plan)
			}
			last := result.Plan.Segments[len(result.Plan.Segments)-1]
			if last.CWDKnown != testCase.known || last.CWD != testCase.cwd ||
				testCase.candidates != nil && !slices.Equal(last.CWDCandidates, testCase.candidates) {
				t.Fatalf("final segment=%#v, want known=%t cwd=%q candidates=%#v", last, testCase.known, testCase.cwd, testCase.candidates)
			}
		})
	}
}

// TestCompoundNestedConditionalReachabilityKeepsOuterState verifies that a
// nested bounded conditional updates its enclosing branch only at the inner
// `fi`, without leaking a dead inner branch into the outer exit.
//
// Example: a true nested branch carries inner through both closing markers.
func TestCompoundNestedConditionalReachabilityKeepsOuterState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	inner := filepath.Join(root, "inner")
	if err := os.Mkdir(inner, 0o700); err != nil {
		t.Fatalf("create inner directory: %v", err)
	}
	for _, testCase := range []struct {
		name    string
		command string
		wantCWD string
	}{
		{
			name:    "nested true",
			command: "if true; then; if true; then; cd inner; fi; fi",
			wantCWD: inner,
		},
		{
			name:    "nested false",
			command: "if true; then; if false; then; cd inner; fi; fi",
			wantCWD: root,
		},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			request := activeWorker(testCase.command)
			request.CWD = root
			result := Classify(request)
			if result.Decision != DecisionAllow || result.Diagnostic != nil || result.Plan == nil {
				t.Fatalf("decision=%q diagnostic=%#v plan=%#v, want allow with topology", result.Decision, result.Diagnostic, result.Plan)
			}
			last := result.Plan.Segments[len(result.Plan.Segments)-1]
			if !last.CWDKnown || last.CWD != testCase.wantCWD {
				t.Fatalf("final segment=%#v, want known CWD %q", last, testCase.wantCWD)
			}
		})
	}
}

// TestCompoundConditionalCWDRecognizesElifAndLiteralStatus keeps the branch
// stack bounded while pruning only the literal false/true conditions.
//
// Example: false selects the elif condition, true selects its branch, and the
// unreachable else directory does not become a candidate.
func TestCompoundConditionalCWDRecognizesElifAndLiteralStatus(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	inner := filepath.Join(root, "inner")
	other := filepath.Join(root, "other")
	if err := os.MkdirAll(inner, 0o700); err != nil {
		t.Fatalf("create inner: %v", err)
	}
	if err := os.MkdirAll(other, 0o700); err != nil {
		t.Fatalf("create other: %v", err)
	}
	request := activeWorker("if false; then cd " + inner + "; elif true; then cd " + root + "; else cd " + other + "; fi; printf after")
	request.CWD = root
	result := Classify(request)
	if result.Decision != DecisionAllow || result.Plan == nil {
		t.Fatalf("decision=%q diagnostic=%#v plan=%#v, want allow", result.Decision, result.Diagnostic, result.Plan)
	}
	last := result.Plan.Segments[len(result.Plan.Segments)-1]
	if !last.CWDKnown || last.CWD != root || !slices.Equal(last.CWDCandidates, []string{root}) {
		t.Fatalf("final segment=%#v, want exact elif directory %q", last, root)
	}
	for index, want := range []SegmentReachability{
		segmentReachable,
		segmentUnreachable,
		segmentReachable,
		segmentReachable,
		segmentUnreachable,
		segmentReachable,
		segmentReachable,
	} {
		if got := result.Plan.Segments[index].Reachability; got != want {
			t.Errorf("segment %d reachability=%q, want %q", index+1, got, want)
		}
	}
}

// TestCompoundConditionalCWDJoinsUnknownConditionExits keeps both reachable
// branch directories when the condition status is not a literal constant.
//
// Example: `test -e flag` may run either branch, so both exits remain checked.
func TestCompoundConditionalCWDJoinsUnknownConditionExits(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	inner := filepath.Join(root, "inner")
	if err := os.Mkdir(inner, 0o700); err != nil {
		t.Fatalf("create inner: %v", err)
	}
	request := activeWorker("if test -e flag; then cd " + inner + "; else cd " + root + "; fi; printf after")
	request.CWD = root
	result := Classify(request)
	if result.Decision != DecisionAllow || result.Plan == nil {
		t.Fatalf("decision=%q diagnostic=%#v plan=%#v, want allow", result.Decision, result.Diagnostic, result.Plan)
	}
	last := result.Plan.Segments[len(result.Plan.Segments)-1]
	if last.CWDKnown || !slices.Equal(last.CWDCandidates, []string{inner, root}) || last.CWDUnknown {
		t.Fatalf("final segment=%#v, want two known candidates without unknown overflow", last)
	}
	if len(result.Plan.Segments) != 5 {
		t.Fatalf("segments=%#v, want five conditional segments", result.Plan.Segments)
	}
	for index, want := range []SegmentReachability{
		segmentReachable,
		segmentReachabilityUnknown,
		segmentReachabilityUnknown,
		segmentReachable,
		segmentReachable,
	} {
		if got := result.Plan.Segments[index].Reachability; got != want {
			t.Errorf("segment %d reachability=%q, want %q", index+1, got, want)
		}
	}
}

// TestCompoundConditionalReachabilityControlsInlineEffects verifies that a
// literal branch status controls the effect-bearing body while preserving the
// raw conditional topology for later adapter replay.
//
// Example: `if true; then rm eci_active; fi` reaches the current marker, while
// the identical writer under `if false` is structurally retained but inert.
func TestCompoundConditionalReachabilityControlsInlineEffects(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	session := filepath.Join(root, "session")
	if err := os.Mkdir(session, 0o700); err != nil {
		t.Fatalf("create session directory: %v", err)
	}
	marker := filepath.Join(session, "eci_active")
	if err := os.WriteFile(marker, []byte("active\n"), 0o600); err != nil {
		t.Fatalf("write active marker: %v", err)
	}

	for _, testCase := range []struct {
		name         string
		command      string
		decision     DecisionKind
		reachability []SegmentReachability
	}{
		{name: "reachable inline body", command: "if true; then cd session; fi; rm eci_active", decision: DecisionDeny, reachability: []SegmentReachability{segmentReachable, segmentReachable, segmentReachable, segmentReachable}},
		{name: "dead inline body", command: "if false; then cd session; fi; rm eci_active", decision: DecisionAllow, reachability: []SegmentReachability{segmentReachable, segmentUnreachable, segmentReachable, segmentReachable}},
		{name: "reachable inline writer", command: "if true; then rm session/eci_active; fi", decision: DecisionDeny, reachability: []SegmentReachability{segmentReachable, segmentReachable, segmentReachable}},
		{name: "dead inline writer", command: "if false; then rm session/eci_active; fi", decision: DecisionAllow, reachability: []SegmentReachability{segmentReachable, segmentUnreachable, segmentReachable}},
		{name: "reachable split writer", command: "if true; then; rm session/eci_active; fi", decision: DecisionDeny, reachability: []SegmentReachability{segmentReachable, segmentReachable, segmentReachable, segmentReachable}},
		{name: "dead split writer", command: "if false; then; rm session/eci_active; fi", decision: DecisionAllow, reachability: []SegmentReachability{segmentReachable, segmentUnreachable, segmentUnreachable, segmentReachable}},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			request := activeWorker(testCase.command)
			request.CWD = root
			request.ActiveSession = "session"
			request.ActiveMarkers = []string{marker}
			result := Classify(request)
			if result.Decision != testCase.decision {
				t.Fatalf("command=%q decision=%q diagnostic=%#v, want %q", testCase.command, result.Decision, result.Diagnostic, testCase.decision)
			}
			if result.Plan == nil || len(result.Plan.Segments) != len(testCase.reachability) {
				t.Fatalf("command=%q plan=%#v, want %d lossless segments", testCase.command, result.Plan, len(testCase.reachability))
			}
			for index, want := range testCase.reachability {
				if got := result.Plan.Segments[index].Reachability; got != want {
					t.Errorf("command=%q segment %d reachability=%q, want %q", testCase.command, index+1, got, want)
				}
			}
		})
	}
}

// TestCompoundUnknownEffectsRetainConcreteCandidates marks source/eval effects
// unknown without replacing a verified pre-effect candidate with callback
// CWD or an empty list.
//
// Example: `source script; rm eci_active` keeps root as a candidate while
// routing the unresolved relative effect conservatively.
func TestCompoundUnknownEffectsRetainConcreteCandidates(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	request := activeWorker("source script; printf after")
	request.CWD = root
	result := Classify(request)
	if result.Decision != DecisionAllow || result.Plan == nil {
		t.Fatalf("decision=%q diagnostic=%#v plan=%#v, want allow", result.Decision, result.Diagnostic, result.Plan)
	}
	last := result.Plan.Segments[1]
	if last.CWDKnown || !last.CWDUnknown || !slices.Equal(last.CWDCandidates, []string{root}) {
		t.Fatalf("segment=%#v, want retained candidate with explicit unknown", last)
	}
}

// TestCompoundReachableCWDChecksEachWriterCandidate verifies that a relative
// writer is checked against every planner-reachable directory, not only the
// first candidate emitted for a redirected state-changing segment.
//
// Example: `cd session > log; rm eci_active` remains denied because session
// is one reachable post-redirection directory.
func TestCompoundReachableCWDChecksEachWriterCandidate(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	session := filepath.Join(root, "session")
	if err := os.Mkdir(session, 0o700); err != nil {
		t.Fatalf("create session directory: %v", err)
	}
	marker := filepath.Join(session, "eci_active")
	if err := os.WriteFile(marker, []byte("active\n"), 0o600); err != nil {
		t.Fatalf("write active marker: %v", err)
	}

	request := activeWorker("cd session > redirect-output; rm eci_active")
	request.CWD = root
	request.ActiveSession = "session"
	request.ActiveMarkers = []string{marker}
	result := Classify(request)
	if result.Decision != DecisionDeny || result.Diagnostic == nil {
		t.Fatalf("decision=%q diagnostic=%#v, want live-control denial", result.Decision, result.Diagnostic)
	}
	if result.Diagnostic.Code != CodePlanLiveControlDenied || result.Diagnostic.Path != marker {
		t.Fatalf("diagnostic=%#v, want %q for %s", result.Diagnostic, CodePlanLiveControlDenied, marker)
	}
}

// TestCompoundPlanAlwaysSerializesCWDUnknownAndCandidateArray keeps the
// planner/adapter contract explicit for an unresolved callback directory.
// Unknown state is data, not a denial, and the candidate array is never
// omitted from a serialized segment.
//
// Example: a missing callback directory produces cwd_unknown=true and []
// rather than borrowing the process CWD.
func TestCompoundPlanAlwaysSerializesCWDUnknownAndCandidateArray(t *testing.T) {
	t.Parallel()

	request := activeWorker("printf before; printf after")
	request.CWD = filepath.Join(t.TempDir(), "missing")
	result := Classify(request)
	if result.Decision != DecisionAllow || result.Plan == nil {
		t.Fatalf("decision=%q diagnostic=%#v plan=%#v, want ordinary compound allow", result.Decision, result.Diagnostic, result.Plan)
	}
	encoded, err := json.Marshal(result.Plan.Segments[0])
	if err != nil {
		t.Fatalf("marshal segment: %v", err)
	}
	text := string(encoded)
	if !strings.Contains(text, `"cwd_candidates":[]`) {
		t.Fatalf("serialized segment=%s, want an explicit empty candidate array", text)
	}
	if !strings.Contains(text, `"cwd_unknown":true`) {
		t.Fatalf("serialized segment=%s, want explicit unknown CWD", text)
	}
}

// TestCompoundPlanRecognizesBoundedDirectBuiltinEffects verifies that quoted
// builtin names and the finite command/builtin wrappers retain parent-shell
// CWD effects, while a child launcher does not mutate its parent.
//
// Example: `builtin cd inner; env pwd; command cd root` carries inner through
// the child launcher and then resolves the final direct command to root.
func TestCompoundPlanRecognizesBoundedDirectBuiltinEffects(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	inner := filepath.Join(root, "inner")
	if err := os.Mkdir(inner, 0o700); err != nil {
		t.Fatalf("create inner: %v", err)
	}
	result := Classify(func() Request {
		request := activeWorker("builtin cd inner; env pwd; command cd " + root + "; printf after")
		request.CWD = root
		return request
	}())
	if result.Decision != DecisionAllow || result.Plan == nil {
		t.Fatalf("decision=%q diagnostic=%#v plan=%#v, want allow", result.Decision, result.Diagnostic, result.Plan)
	}
	want := []string{root, inner, inner, root}
	if len(result.Plan.Segments) != len(want) {
		t.Fatalf("segments=%#v, want %d", result.Plan.Segments, len(want))
	}
	for index, expected := range want {
		segment := result.Plan.Segments[index]
		if !segment.CWDKnown || segment.CWD != expected || !slices.Equal(segment.CWDCandidates, []string{expected}) {
			t.Errorf("segment %d=%#v, want known %q", index+1, segment, expected)
		}
	}
}

// TestCompoundPlanCDPATHScopeAndDotNames follows Bash's finite CDPATH rules:
// a single component, including .hidden, may search CDPATH; explicit slash
// forms bypass it, and a leading assignment is temporary for that cd.
//
// Example: CDPATH=/search; cd .hidden resolves /search/.hidden without
// persisting CDPATH for the following relative cd.
func TestCompoundPlanCDPATHScopeAndDotNames(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	search := filepath.Join(root, "search")
	if err := os.MkdirAll(filepath.Join(search, ".hidden"), 0o700); err != nil {
		t.Fatalf("create CDPATH directories: %v", err)
	}
	localHidden := filepath.Join(root, ".hidden")
	if err := os.Mkdir(localHidden, 0o700); err != nil {
		t.Fatalf("create local hidden directory: %v", err)
	}
	localHiddenSlash := filepath.Join(root, "hidden")
	if err := os.Mkdir(localHiddenSlash, 0o700); err != nil {
		t.Fatalf("create local slash directory: %v", err)
	}
	for _, testCase := range []struct {
		name    string
		command string
		want    []string
	}{
		{name: "dot hidden searches CDPATH", command: "CDPATH=" + search + " cd .hidden; printf after", want: []string{root, filepath.Join(search, ".hidden")}},
		{name: "explicit slash bypasses CDPATH", command: "CDPATH=" + search + " cd ./hidden; printf after", want: []string{root, localHiddenSlash}},
		{name: "prefix assignment is temporary", command: "CDPATH=" + search + " cd .hidden; cd " + root + "; cd .hidden; printf after", want: []string{root, filepath.Join(search, ".hidden"), root, localHidden}},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			request := activeWorker(testCase.command)
			request.CWD = root
			result := Classify(request)
			if result.Decision != DecisionAllow || result.Plan == nil {
				t.Fatalf("decision=%q diagnostic=%#v plan=%#v, want allow", result.Decision, result.Diagnostic, result.Plan)
			}
			if len(result.Plan.Segments) != len(testCase.want) {
				t.Fatalf("segments=%#v, want %d", result.Plan.Segments, len(testCase.want))
			}
			for index, expected := range testCase.want {
				segment := result.Plan.Segments[index]
				if !segment.CWDKnown || segment.CWD != expected {
					t.Errorf("segment %d=%#v, want known %q", index+1, segment, expected)
				}
			}
		})
	}
}

// TestCompoundPlanBoundsReachableCandidatesWithExplicitOverflow verifies that
// the 128-state cap produces an unknown fact without dropping known candidate
// checks or turning ordinary work into a denial.
//
// Example: an absolute marker in the retained/absolute target route is still
// protected even when the compound CWD candidate list overflows.
func TestCompoundPlanBoundsReachableCandidatesWithExplicitOverflow(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	candidates := make([]string, 0, maxReachableStates+1)
	for index := 0; index <= maxReachableStates; index++ {
		candidate := filepath.Join(root, fmt.Sprintf("candidate-%03d", index))
		if err := os.Mkdir(candidate, 0o700); err != nil {
			t.Fatalf("create candidate %d: %v", index, err)
		}
		candidates = append(candidates, candidate)
	}
	request := activeWorker("printf before; printf after")
	request.CWD = candidates[0]
	request.CWDKnown = true
	request.CWDCandidates = candidates
	result := Classify(request)
	if result.Decision != DecisionAllow || result.Plan == nil {
		t.Fatalf("decision=%q diagnostic=%#v plan=%#v, want ordinary allow", result.Decision, result.Diagnostic, result.Plan)
	}
	segment := result.Plan.Segments[0]
	if !segment.CWDUnknown || segment.CWDKnown || len(segment.CWDCandidates) != maxReachableStates {
		t.Fatalf("segment=%#v, want unknown with %d candidates", segment, maxReachableStates)
	}
	marker := filepath.Join(candidates[len(candidates)-1], "eci_active")
	if err := os.WriteFile(marker, []byte("scope: active\n"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	request.Command = "rm " + marker
	request.ActiveMarkers = []string{marker}
	request.CWDCandidates = candidates
	result = Classify(request)
	if result.Decision != DecisionDeny || result.Diagnostic == nil || result.Diagnostic.Code != CodePlanLiveControlDenied {
		t.Fatalf("absolute protected target result: decision=%q diagnostic=%#v, want live-control denial", result.Decision, result.Diagnostic)
	}
}

// TestCompoundTimeoutReplayStaysOpaqueForMultipleCWDStates prevents a probe
// record from borrowing one callback directory when planner reachability has
// more than one concrete candidate.
//
// Example: two verified initial directories keep timeout metadata opaque even
// though timeout itself is a supported direct prefix.
func TestCompoundTimeoutReplayStaysOpaqueForMultipleCWDStates(t *testing.T) {
	t.Parallel()

	first := t.TempDir()
	second := t.TempDir()
	request := activeWorker("printf before; timeout 5 printf child")
	request.CWD = first
	request.CWDKnown = true
	request.CWDCandidates = []string{first, second}
	result := Classify(request)
	if result.Decision != DecisionAllow || result.Diagnostic != nil {
		t.Fatalf("decision=%q diagnostic=%#v, want allow", result.Decision, result.Diagnostic)
	}
	if len(result.TimeoutReplays) != 0 {
		t.Fatalf("timeout replays=%#v, want opaque multi-state handoff", result.TimeoutReplays)
	}
	if result.Plan == nil || result.Plan.Segments[1].CWDKnown ||
		!slices.Equal(result.Plan.Segments[1].CWDCandidates, []string{first, second}) {
		t.Fatalf("plan=%#v, want both candidate directories", result.Plan)
	}
}

// TestCompoundPlanEnvironmentInspectionRemainsOrdinary verifies that a bare
// environment inspection in a compound command is not a permission boundary.
//
// Example: `printf before; env` retains its two-segment topology without a
// synthetic environment denial.
func TestCompoundPlanEnvironmentInspectionRemainsOrdinary(t *testing.T) {
	t.Parallel()

	result := Classify(activeWorker("printf before; env"))
	if result.Decision != DecisionAllow || result.Diagnostic != nil {
		t.Fatalf("decision=%q diagnostic=%#v, want ordinary allow", result.Decision, result.Diagnostic)
	}
	if result.Plan == nil || len(result.Plan.Segments) != 2 {
		t.Fatalf("topology=%#v, want two parsed segments", result.Plan)
	}
}

// TestCompoundPlanSyntaxDoesNotBecomePermissionBoundary verifies that shell
// syntax remains ordinary and parser capacity falls through to the existing
// target-aware route.
//
// Example: `printf ok; printf $(date)` is admitted rather than blocked for
// dynamic expansion alone.
func TestCompoundPlanSyntaxDoesNotBecomePermissionBoundary(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name     string
		command  string
		decision DecisionKind
	}{
		{name: "malformed", command: "printf ok;", decision: DecisionAllow},
		{name: "dynamic", command: "printf ok; printf $(date)", decision: DecisionAllow},
		{name: "redirection", command: "printf ok; sed -n '1p' > output", decision: DecisionAllow},
		{name: "capacity", command: "a;b;c;d;e;f;g;h;i", decision: DecisionDefer},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			result := Classify(activeWorker(testCase.command))
			if result.Decision != testCase.decision {
				t.Fatalf("decision=%q diagnostic=%#v, want %q", result.Decision, result.Diagnostic, testCase.decision)
			}
			if result.Diagnostic != nil {
				t.Fatalf("syntax/capacity diagnostic=%#v, want nil", result.Diagnostic)
			}
		})
	}
}

// TestRunWritesOneCompoundTopologyAndDoesNotExecute verifies that the planner
// emits one JSON response with topology metadata while treating parsed argv as
// data rather than executing an embedded command.
//
// Example: a parsed `touch` segment leaves its temporary sentinel absent.
func TestRunWritesOneCompoundTopologyAndDoesNotExecute(t *testing.T) {
	t.Parallel()

	sentinel := filepath.Join(t.TempDir(), "planner-must-not-execute")
	request := Request{
		Provider:      ProviderOmp,
		Role:          RoleCoordinator,
		CWD:           "/workspace/repo",
		Marker:        MarkerActive,
		ActiveSession: "test-session",
		Command:       "printf planner-only; touch " + sentinel,
	}
	encodedRequest, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	var output bytes.Buffer
	status := Run(bytes.NewReader(encodedRequest), &output)
	if status != StatusAllow {
		t.Fatalf("status=%d output=%q, want allow", status, output.String())
	}
	if bytes.Count(output.Bytes(), []byte("\n")) != 1 {
		t.Fatalf("output=%q, want exactly one JSON response", output.String())
	}
	var result Result
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v; output=%q", err, output.String())
	}
	if result.Plan == nil || len(result.Plan.Segments) != 2 {
		t.Fatalf("topology=%#v, want two segments", result.Plan)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("planner executed touch sentinel: stat error=%v", err)
	}
}

func TestWorkerCompoundPlansRouteByOperator(t *testing.T) {
	t.Parallel()

	commands := []struct {
		name                string
		command             string
		decision            DecisionKind
		coordinatorDecision DecisionKind
	}{
		{name: "and", command: "printf left && printf right", decision: DecisionAllow},
		{name: "or", command: "printf left || printf right", decision: DecisionAllow},
		{name: "semicolon", command: "printf left ; printf right", decision: DecisionAllow},
		{name: "pipeline", command: "printf left | printf right", decision: DecisionAllow},
		{name: "bounded read-only pipeline", command: "git diff --binary -- hooks/validate-bash.sh | sha256sum", decision: DecisionDefer, coordinatorDecision: DecisionDefer},
	}
	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()

			for _, testCase := range commands {
				testCase := testCase
				t.Run(testCase.name, func(t *testing.T) {
					t.Parallel()

					workerRequest := activeWorker(testCase.command)
					workerRequest.Provider = provider
					workerResult := Classify(workerRequest)
					if workerResult.Decision != testCase.decision || workerResult.Diagnostic != nil {
						t.Fatalf("worker compound plan: decision=%q diagnostic=%#v, want %q without diagnostic", workerResult.Decision, workerResult.Diagnostic, testCase.decision)
					}

					coordinatorRequest := workerRequest
					coordinatorRequest.Role = RoleCoordinator
					wantCoordinatorDecision := testCase.coordinatorDecision
					if wantCoordinatorDecision == "" {
						wantCoordinatorDecision = DecisionAllow
					}
					coordinatorResult := Classify(coordinatorRequest)
					if coordinatorResult.Decision != wantCoordinatorDecision || coordinatorResult.Diagnostic != nil {
						t.Fatalf("coordinator compound plan: decision=%q diagnostic=%#v, want %q without diagnostic", coordinatorResult.Decision, coordinatorResult.Diagnostic, wantCoordinatorDecision)
					}

					inactiveWorkerRequest := workerRequest
					inactiveWorkerRequest.Marker = MarkerInactive
					inactiveWorkerResult := Classify(inactiveWorkerRequest)
					if inactiveWorkerResult.Decision != DecisionAllow || inactiveWorkerResult.Diagnostic != nil {
						t.Fatalf("inactive worker compound plan: decision=%q diagnostic=%#v, want allowed without diagnostic", inactiveWorkerResult.Decision, inactiveWorkerResult.Diagnostic)
					}
				})
			}

			for _, command := range []string{"printf left\nprintf right", "printf left\rprintf right"} {
				request := activeWorker(command)
				request.Provider = provider
				result := Classify(request)
				if result.Decision != DecisionAllow || result.Diagnostic != nil {
					t.Fatalf("newline/carriage-return %q: decision=%q diagnostic=%#v, want ordinary command admission", command, result.Decision, result.Diagnostic)
				}
			}

			protectedRequest := activeWorker("printf left | env")
			protectedRequest.Provider = provider
			protectedResult := Classify(protectedRequest)
			if protectedResult.Decision != DecisionAllow || protectedResult.Diagnostic != nil {
				t.Fatalf("ordinary compound plan: decision=%q diagnostic=%#v, want allow", protectedResult.Decision, protectedResult.Diagnostic)
			}

			protectedSemicolon := activeWorker("printf left; env")
			protectedSemicolon.Provider = provider
			protectedSemicolonResult := Classify(protectedSemicolon)
			if protectedSemicolonResult.Decision != DecisionAllow || protectedSemicolonResult.Diagnostic != nil {
				t.Fatalf("ordinary semicolon plan: decision=%q diagnostic=%#v, want allow", protectedSemicolonResult.Decision, protectedSemicolonResult.Diagnostic)
			}

			lifecycleSemicolon := activeWorker("printf left; eci-active status")
			lifecycleSemicolon.Provider = provider
			lifecycleSemicolonResult := Classify(lifecycleSemicolon)
			if lifecycleSemicolonResult.Decision != DecisionDefer || lifecycleSemicolonResult.Diagnostic != nil {
				t.Fatalf("lifecycle semicolon plan: decision=%q diagnostic=%#v, want ordinary lifecycle defer", lifecycleSemicolonResult.Decision, lifecycleSemicolonResult.Diagnostic)
			}
		})
	}
}

func TestCoordinatorCompoundMutationRemainsProviderOwned(t *testing.T) {
	t.Parallel()

	commands := []struct {
		name    string
		command string
	}{
		{
			name:    "cleanup after inspection",
			command: "realpath -e evidence.txt && rm -f /home/pheona/tmp/compound-cleanup",
		},
		{
			name:    "source writer after inspection",
			command: "realpath -e evidence.txt && touch /home/pheona/tmp/compound-touch",
		},
		{
			name:    "source writer in cwd after inspection",
			command: "realpath -e evidence.txt && touch source.txt",
		},
		{
			name:    "source writer in cwd pipeline",
			command: "printf source | tee source.txt",
		},
	}
	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()
			for _, testCase := range commands {
				testCase := testCase
				t.Run(testCase.name, func(t *testing.T) {
					t.Parallel()
					request := activeWorker(testCase.command)
					request.Provider = provider
					request.Role = RoleCoordinator
					request.CWD = "/workspace/repo"
					result := Classify(request)
					if result.Decision != DecisionAllow || result.Diagnostic != nil {
						t.Fatalf("decision=%q diagnostic=%#v, want planner allow for provider-owned compound mutation", result.Decision, result.Diagnostic)
					}
				})
			}

			readOnly := activeWorker("git rev-parse HEAD && git status --short")
			readOnly.Provider = provider
			readOnly.Role = RoleCoordinator
			readOnly.CWD = "/workspace/repo"
			result := Classify(readOnly)
			if result.Decision != DecisionDefer || result.Diagnostic != nil {
				t.Fatalf("read-only compound decision=%q diagnostic=%#v, want provider defer", result.Decision, result.Diagnostic)
			}
		})
	}
}

func TestCoordinatorScriptBatchesDeferToProviderAdapters(t *testing.T) {
	t.Parallel()

	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve module working directory: %v", err)
	}
	repositoryRoot := filepath.Clean(filepath.Join(workingDirectory, "..", "..", ".."))
	commands := []string{
		"bash hooks/eci-active-gate.sh && bash hooks/stop-gate.sh",
		"sh -n hooks/eci-active-gate.sh && sh hooks/stop-gate.sh",
	}
	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()
			for _, command := range commands {
				result := Classify(Request{
					Provider:      provider,
					Role:          RoleCoordinator,
					CWD:           repositoryRoot,
					Marker:        MarkerActive,
					ActiveSession: "test-session",
					Command:       command,
				})
				if result.Decision != DecisionDefer || result.Diagnostic != nil {
					t.Fatalf("%q: decision=%q diagnostic=%#v, want provider-adapter defer", command, result.Decision, result.Diagnostic)
				}
			}
		})
	}
}

func TestCoordinatorHookRepairDefersToProviderAdapters(t *testing.T) {
	t.Parallel()

	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()

			result := Classify(Request{
				Provider:      provider,
				Role:          RoleCoordinator,
				CWD:           "/tmp",
				Marker:        MarkerActive,
				ActiveSession: "test-session",
				Command:       "bash hooks/install-pre-commit-go-mod.sh --repair-hardlink /tmp/peer",
			})
			if result.Decision != DecisionDefer || result.Diagnostic != nil {
				t.Fatalf("decision: got %q diagnostic=%#v", result.Decision, result.Diagnostic)
			}
		})
	}
}

func TestActivePreCommitHookModeRepairRoutesByRole(t *testing.T) {
	t.Parallel()

	const command = "chmod 755 hooks/pre-commit-go-mod.sh"
	negativeCases := []struct {
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
	}
	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()
			providerRoot := providerHome(provider)

			worker := Classify(Request{
				Provider:      provider,
				Role:          RoleWorker,
				CWD:           providerRoot,
				Marker:        MarkerActive,
				ActiveSession: "test-session",
				Command:       command,
			})
			if worker.Decision != DecisionDeny || worker.Diagnostic == nil {
				t.Fatalf("worker decision: got %q diagnostic=%#v, want deny with diagnostic", worker.Decision, worker.Diagnostic)
			}
			diagnostic := worker.Diagnostic
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

			coordinatorRequest := Request{
				Provider:      provider,
				Role:          RoleCoordinator,
				CWD:           providerRoot,
				Marker:        MarkerActive,
				ActiveSession: "test-session",
				Command:       command,
			}
			coordinator := Classify(coordinatorRequest)
			if coordinator.Decision != DecisionDefer || coordinator.Diagnostic != nil {
				t.Fatalf("coordinator decision: got %q diagnostic=%#v, want defer without diagnostic", coordinator.Decision, coordinator.Diagnostic)
			}

			for _, testCase := range negativeCases {
				testCase := testCase
				t.Run(testCase.name, func(t *testing.T) {
					result := Classify(Request{
						Provider:      provider,
						Role:          RoleWorker,
						CWD:           providerRoot,
						Marker:        testCase.marker,
						ActiveSession: "test-session",
						Command:       testCase.command,
					})
					if result.Diagnostic != nil && result.Diagnostic.Predicate == "hook-mode-repair" {
						t.Fatalf("%s selected hook-mode-repair: decision=%q diagnostic=%#v", testCase.command, result.Decision, result.Diagnostic)
					}
					if testCase.marker == MarkerInactive && (result.Decision != DecisionAllow || result.Diagnostic != nil) {
						t.Fatalf("inactive exact command: decision=%q diagnostic=%#v, want allow without diagnostic", result.Decision, result.Diagnostic)
					}
				})
			}
		})
	}
}

func TestActiveWorkerProtectedHookModeMutationsUseGenericControlDenial(t *testing.T) {
	t.Parallel()

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

					result := Classify(Request{
						Provider:      provider,
						Role:          RoleWorker,
						CWD:           providerRoot,
						Marker:        MarkerActive,
						ActiveSession: "test-session",
						Command:       command,
					})
					if result.Decision != DecisionDeny || result.Diagnostic == nil {
						t.Fatalf("decision: got %q diagnostic=%#v, want generic denial", result.Decision, result.Diagnostic)
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

// TestChmodOptionIsRecursiveRecognizesOnlyGNUUnambiguousLongAbbreviations verifies that only
// the unique GNU prefixes of --recursive enable recursive protected-target detection.
//
// Example: --rec enables recursion while ambiguous --re does not.
func TestChmodOptionIsRecursiveRecognizesOnlyGNUUnambiguousLongAbbreviations(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"--rec",
		"--recu",
		"--recur",
		"--recurs",
		"--recursi",
		"--recursiv",
		"--recursive",
	} {
		if !chmodOptionIsRecursive(value) {
			t.Errorf("%q: got false, want true", value)
		}
	}
	for _, value := range []string{
		"--",
		"--r",
		"--re",
		"--reference",
		"--rec=ursive",
		"--recursion",
		"--recursive=value",
	} {
		if chmodOptionIsRecursive(value) {
			t.Errorf("%q: got true, want false", value)
		}
	}
}

// TestChmodOptionIsReferenceRecognizesOnlyGNUUnambiguousLongAbbreviations verifies that only
// the unique GNU prefixes of --reference select a reference source operand.
//
// Example: --ref=source selects a reference source while ambiguous --re=source does not.
func TestChmodOptionIsReferenceRecognizesOnlyGNUUnambiguousLongAbbreviations(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"--ref",
		"--refe",
		"--refer",
		"--refere",
		"--referen",
		"--referenc",
		"--reference",
		"--ref=source",
		"--reference=source",
	} {
		if !chmodOptionIsReference(value) {
			t.Errorf("%q: got false, want true", value)
		}
	}
	for _, value := range []string{
		"--",
		"--r",
		"--re",
		"--re=source",
		"--rec",
		"--referencee",
		"--referee=source",
		"--ref=",
		"--reference=",
	} {
		if chmodOptionIsReference(value) {
			t.Errorf("%q: got true, want false", value)
		}
	}
}

// TestChmodMutationTargetsSeparatesGNUReferenceSources verifies that GNU reference operands
// never appear in the mutation-target set, including when the option follows a target.
//
// Example: chmod hooks/validate-bash.sh --ref=ordinary.txt mutates the hook path.
func TestChmodMutationTargetsSeparatesGNUReferenceSources(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name      string
		argv      []token
		targets   []string
		recursive bool
	}{
		{
			name: "inline abbreviated reference",
			argv: []token{
				{value: "chmod"},
				{value: "--ref=hooks/validate-bash.sh"},
				{value: "ordinary.txt"},
			},
			targets: []string{"ordinary.txt"},
		},
		{
			name: "separate abbreviated reference",
			argv: []token{
				{value: "chmod"},
				{value: "--ref"},
				{value: "hooks/validate-bash.sh"},
				{value: "ordinary.txt"},
			},
			targets: []string{"ordinary.txt"},
		},
		{
			name: "reference after target",
			argv: []token{
				{value: "chmod"},
				{value: "hooks/validate-bash.sh"},
				{value: "--ref=ordinary.txt"},
			},
			targets: []string{"hooks/validate-bash.sh"},
		},
		{
			name: "recursive reference source",
			argv: []token{
				{value: "chmod"},
				{value: "-R"},
				{value: "--ref=hooks/validate-bash.sh"},
				{value: "ordinary-dir"},
			},
			targets:   []string{"ordinary-dir"},
			recursive: true,
		},
		{
			name: "end of options keeps abbreviation literal",
			argv: []token{
				{value: "chmod"},
				{value: "755"},
				{value: "--"},
				{value: "--ref"},
				{value: "hooks"},
			},
			targets: []string{"--ref", "hooks"},
		},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			targets, recursive := chmodMutationTargets(testCase.argv)
			if recursive != testCase.recursive {
				t.Fatalf("recursive: got %t, want %t", recursive, testCase.recursive)
			}
			if len(targets) != len(testCase.targets) {
				t.Fatalf("target count: got %d (%#v), want %d (%#v)", len(targets), targets, len(testCase.targets), testCase.targets)
			}
			for index, target := range targets {
				if target.value != testCase.targets[index] {
					t.Errorf("target %d: got %q, want %q", index, target.value, testCase.targets[index])
				}
			}
		})
	}
}

func TestActiveCoordinatorProtectedHookModeMutationsDefer(t *testing.T) {
	t.Parallel()

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

					result := Classify(Request{
						Provider:      provider,
						Role:          RoleCoordinator,
						CWD:           providerRoot,
						Marker:        MarkerActive,
						ActiveSession: "test-session",
						Command:       command,
					})
					if result.Decision != DecisionDefer || result.Diagnostic != nil {
						t.Fatalf("decision: got %q diagnostic=%#v, want defer without diagnostic", result.Decision, result.Diagnostic)
					}
				})
			}
		})
	}
}

func TestActiveWorkerAllowsSameNamedHookPathOutsideProviderRoot(t *testing.T) {
	t.Parallel()

	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()

			for _, command := range []string{
				"chmod 644 hooks/validate-bash.sh",
				"chmod -R 644 .",
			} {
				result := Classify(Request{
					Provider:      provider,
					Role:          RoleWorker,
					CWD:           "/tmp",
					Marker:        MarkerActive,
					ActiveSession: "test-session",
					Command:       command,
				})
				if result.Decision != DecisionAllow || result.Diagnostic != nil {
					t.Fatalf("%q: decision: got %q diagnostic=%#v, want allow without diagnostic", command, result.Decision, result.Diagnostic)
				}
			}
		})
	}
}

func TestActiveWorkerOrdinaryChmodRemainsAllowed(t *testing.T) {
	t.Parallel()

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
				result := Classify(Request{
					Provider:      provider,
					Role:          RoleWorker,
					CWD:           providerRoot,
					Marker:        MarkerActive,
					ActiveSession: "test-session",
					Command:       command,
				})
				if result.Decision != DecisionAllow || result.Diagnostic != nil {
					t.Fatalf("%q: decision: got %q diagnostic=%#v, want allow without diagnostic", command, result.Decision, result.Diagnostic)
				}
			}
		})
	}
}

func TestActiveMktempDefersToProviderAdapters(t *testing.T) {
	t.Parallel()

	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()

			for _, command := range []string{
				"mktemp -d /tmp/eci-probe.XXXXXX",
				"mktemp -d /tmp/eci-probe.XXXXXX extra",
			} {
				result := Classify(Request{
					Provider:      provider,
					Role:          RoleCoordinator,
					CWD:           "/tmp",
					Marker:        MarkerActive,
					ActiveSession: "test-session",
					Command:       command,
				})
				if result.Decision != DecisionDefer || result.Diagnostic != nil {
					t.Fatalf("%s: decision: got %q diagnostic=%#v", command, result.Decision, result.Diagnostic)
				}
			}
		})
	}
}

func TestInactiveLeadingAssignmentRemainsOrdinary(t *testing.T) {
	t.Parallel()

	request := activeWorker("FOO=bar novel-tool")
	request.Marker = MarkerInactive
	result := Classify(request)
	if result.Decision != DecisionAllow || result.Diagnostic != nil {
		t.Fatalf("decision=%q diagnostic=%#v, want ordinary allow", result.Decision, result.Diagnostic)
	}
}

func TestQuotedCommandSubstitutionRemainsOrdinary(t *testing.T) {
	t.Parallel()

	for _, provider := range []Provider{ProviderOmp, ProviderKimi} {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()

			inactive := activeWorker(`printf "%s\\n" "$(printf nested)"`)
			inactive.Provider = provider
			inactive.Marker = MarkerInactive
			inactiveResult := Classify(inactive)
			if inactiveResult.Decision != DecisionAllow || inactiveResult.Diagnostic != nil {
				t.Fatalf("inactive decision=%q diagnostic=%#v, want ordinary allow", inactiveResult.Decision, inactiveResult.Diagnostic)
			}

			active := activeWorker(inactive.Command)
			active.Provider = provider
			activeResult := Classify(active)
			if activeResult.Decision != DecisionAllow || activeResult.Diagnostic != nil {
				t.Fatalf("active decision=%q diagnostic=%#v, want ordinary allow", activeResult.Decision, activeResult.Diagnostic)
			}
		})
	}
}

func TestInactiveOpaqueShellContextDoesNotDeny(t *testing.T) {
	t.Parallel()

	for _, command := range []string{
		"PATH=/tmp git commit",
		"env git commit",
		"bash -c 'git commit'",
	} {
		result := Classify(Request{
			Provider: ProviderOmp,
			Role:     RoleCoordinator,
			CWD:      "/tmp",
			Marker:   MarkerInactive,
			Command:  command,
		})
		if result.Decision == DecisionDeny {
			t.Errorf("%s: decision=%q diagnostic=%#v, want ordinary continuation", command, result.Decision, result.Diagnostic)
		}
	}
}

func TestInactiveOrdinaryLiteralRemainsAllowed(t *testing.T) {
	t.Parallel()

	result := Classify(Request{
		Provider: ProviderOmp,
		Role:     RoleCoordinator,
		CWD:      "/tmp",
		Marker:   MarkerInactive,
		Command:  "novel-tool --flag value",
	})
	if result.Decision != DecisionAllow || result.Diagnostic != nil {
		t.Fatalf("decision=%q diagnostic=%#v, want allow without diagnostic", result.Decision, result.Diagnostic)
	}
}

// TestPlannerCapacityFallsBackWithoutAdmissionDenial verifies that parser
// capacity is an implementation detail rather than a command boundary.
//
// Example: a generated 129-argument printf request stays on the existing
// target-aware fallback instead of being told to split its command.
func TestPlannerCapacityFallsBackWithoutAdmissionDenial(t *testing.T) {
	t.Parallel()

	arguments := append([]string{"printf"}, make([]string, maxArguments)...)
	for index := 1; index < len(arguments); index++ {
		arguments[index] = "x"
	}

	for _, testCase := range []struct {
		name    string
		command string
	}{
		{
			name:    "nine segments",
			command: strings.Repeat("printf ok;", maxSegments) + "printf ok",
		},
		{
			name:    "129 argv elements",
			command: strings.Join(arguments, " "),
		},
		{
			name:    "oversize argv element",
			command: "printf " + strings.Repeat("x", maxArgumentBytes+1),
		},
		{
			name:    "oversize command",
			command: "printf " + strings.Repeat("x", maxCommandBytes),
		},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result := Classify(activeWorker(testCase.command))
			if result.Decision != DecisionDefer || result.Diagnostic != nil ||
				len(result.Capabilities) != 0 || result.DeferredRoute != "" || result.Plan != nil {
				t.Fatalf("decision=%q diagnostic=%#v capabilities=%v route=%q plan=%#v, want transparent capacity fallback", result.Decision, result.Diagnostic, result.Capabilities, result.DeferredRoute, result.Plan)
			}
		})
	}

	result := Classify(activeWorker("rm -rf /"))
	if result.Decision != DecisionDeny || result.Diagnostic == nil || result.Diagnostic.Code != CodeBroadDestructiveDenied {
		t.Fatalf("concrete broad deletion: decision=%q diagnostic=%#v, want existing target-aware denial", result.Decision, result.Diagnostic)
	}
}

func TestSegmentCapacityFallsBack(t *testing.T) {
	t.Parallel()

	coordinatorRequest := activeWorker("a;b;c;d;e;f;g;h")
	coordinatorRequest.Role = RoleCoordinator
	allowed := Classify(coordinatorRequest)
	if allowed.Decision != DecisionAllow {
		t.Fatalf("eight segments: got %#v", allowed)
	}

	fallback := Classify(activeWorker("a;b;c;d;e;f;g;h;i"))
	if fallback.Decision != DecisionDefer || fallback.Diagnostic != nil {
		t.Fatalf("nine segments: got %#v, want transparent fallback", fallback)
	}
}

func TestDeniedJSONCarriesCompilerFields(t *testing.T) {
	t.Parallel()

	result := Classify(activeWorker("rm -rf /"))
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}

	for _, fragment := range []string{
		`"decision":"deny"`,
		`"code":"ECI_BROAD_DESTRUCTIVE_DENIED"`,
		`"operation":"broad-destructive"`,
		`"segment":1`,
		`"argv_index":2`,
		`"byte_offset":7`,
		`"token":"/"`,
		`"path":"/"`,
		`"predicate":"broad-destructive-root"`,
		`"reason":`,
		`"remediation":`,
		`"permissionDecision":"deny"`,
		`"rejected_segment":"rm -rf /"`,
		`rejected segment=rm -rf /`,
	} {
		if !containsBytes(encoded, []byte(fragment)) {
			t.Errorf("encoded result missing %s: %s", fragment, encoded)
		}
	}
}

func TestRunReadsOneJSONRequestAndWritesOneJSONDecision(t *testing.T) {
	t.Parallel()

	request := activeWorker("adb devices -l")
	encodedRequest, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	var output bytes.Buffer
	status := Run(bytes.NewReader(encodedRequest), &output)
	if status != StatusAllow {
		t.Fatalf("status: got %d, want %d", status, StatusAllow)
	}
	if output.String() != "{\"decision\":\"allow\"}\n" {
		t.Fatalf("output: got %q", output.String())
	}
}

func TestRunRejectsTrailingJSON(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	status := Run(strings.NewReader("{} {}"), &output)
	if status != StatusInternal {
		t.Fatalf("status: got %d, want %d", status, StatusInternal)
	}
	if !strings.Contains(output.String(), `"decision":"error"`) {
		t.Fatalf("output: got %q", output.String())
	}
}

func TestEnvironmentUnsetOptions(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		command string
	}{
		{
			name:    "attached valid identifier",
			command: "env --unset=FOO novel-tool",
		},
		{
			name:    "attached empty identifier",
			command: "env --unset= novel-tool",
		},
		{
			name:    "attached invalid identifier",
			command: "env --unset=9FOO novel-tool",
		},
		{
			name:    "attached chdir remains structural",
			command: "env --chdir=/tmp novel-tool",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result := Classify(activeWorker(testCase.command))
			if result.Decision != DecisionAllow || result.Diagnostic != nil {
				t.Fatalf("decision=%q diagnostic=%#v, want ordinary allow", result.Decision, result.Diagnostic)
			}
		})
	}
}

func TestWorkerRepositoryAllowLifecycleRouteDefers(t *testing.T) {
	t.Parallel()

	for _, command := range []string{
		`"$HOME/.omp/agent/eci/bin/eci-active" repository-allow-status`,
		`"$HOME/.omp/agent/eci/bin/eci-active" repository-allow-on /tmp/dependency "owned dependency repair"`,
		`"$HOME/.omp/agent/eci/bin/eci-active" repository-allow-off /tmp/dependency`,
	} {
		result := Classify(activeWorker(command))
		if result.Decision != DecisionDefer || result.Diagnostic != nil {
			t.Fatalf("command=%q decision=%q diagnostic=%#v, want defer without diagnostic", command, result.Decision, result.Diagnostic)
		}
	}

	for _, command := range []string{
		`"$HOME/.omp/agent/eci/bin/eci-active" repository-allow-on /tmp/dependency "line
break"`,
		`"$HOME/.omp/agent/eci/bin/eci-active" repository-allow-on /tmp/dependency`,
		`"$HOME/.omp/agent/eci/bin/eci-active" repository-allow-off relative/dependency`,
	} {
		result := Classify(activeWorker(command))
		if result.Decision == DecisionDefer && result.Diagnostic == nil {
			t.Fatalf("malformed command=%q was deferred as a repository-allow route", command)
		}
	}
}

func activeWorker(command string) Request {
	return Request{
		Provider:       ProviderOmp,
		Role:           RoleWorker,
		CWD:            "/tmp",
		CommandPath:    callbackPathForTimeoutTests(),
		CommandPathSet: true,
		Marker:         MarkerActive,
		ActiveSession:  "test-session",
		Command:        command,
	}
}

func callbackPathForTimeoutTests() string {
	timeoutPath, _ := exec.LookPath("timeout")
	if timeoutPath == "" {
		return ""
	}
	return filepath.Dir(timeoutPath)
}

// TestEciLifecycleTargetCandidatesDeferWithoutLexicalAuthority keeps
// lifecycle target resolution in the provider adapter. Equivalent shell
// spellings must not be rejected before that adapter can compare the actual
// executable identity with the current omp lifecycle target.
//
// Example: a bare PATH-resolved eci-active and a $HOME/.omp/agent/eci path can both
// resolve to the same current-home executable.
func TestEciLifecycleTargetCandidatesDeferWithoutLexicalAuthority(t *testing.T) {
	t.Parallel()

	for _, command := range []string{
		`"$HOME"/.omp/agent/eci/bin/eci-active --help`,
		`${HOME}/.omp/agent/eci/bin/eci-active --help`,
		`$ECI_HOME/bin/eci-active --help`,
		`eci-active --help`,
		`~/.omp/agent/eci/bin/eci-active --help`,
		`/tmp/eci-active --help`,
		`env -- ECI_SESSION_ID=test-session "$HOME"/.omp/agent/eci/bin/eci-active --help`,
	} {
		result := Classify(Request{
			Provider:      ProviderOmp,
			Role:          RoleCoordinator,
			CWD:           "/workspace",
			Marker:        MarkerActive,
			ActiveSession: "test-session",
			Command:       command,
		})
		if result.Decision != DecisionDefer || result.DeferredRoute != DeferredRouteEciLifecycle || result.Diagnostic != nil {
			t.Errorf("%q: decision=%q route=%q diagnostic=%#v, want lifecycle target-resolution defer", command, result.Decision, result.DeferredRoute, result.Diagnostic)
		}
	}
}

// TestEciLifecycleHelpDefersWithoutActiveCoordinator keeps help usable
// before a session is active and from non-coordinator callbacks. The lifecycle
// program itself owns help argument validation after target resolution.
//
// Example: a fresh session can ask the resolved eci-active executable for
// --help without first creating an ECI marker.
func TestEciLifecycleHelpDefersWithoutActiveCoordinator(t *testing.T) {
	t.Parallel()

	for _, request := range []Request{
		{
			Provider: ProviderOmp,
			Role:     RoleWorker,
			CWD:      "/workspace",
			Marker:   MarkerInactive,
			Command:  `$HOME/.omp/agent/eci/bin/eci-active --help`,
		},
		{
			Provider: ProviderOmp,
			Role:     RoleWorker,
			CWD:      "/workspace",
			Marker:   MarkerInactive,
			Command:  `eci-active -h`,
		},
	} {
		result := Classify(request)
		if result.Decision != DecisionDefer || result.DeferredRoute != DeferredRouteEciLifecycle || result.Diagnostic != nil {
			t.Errorf("%q: decision=%q route=%q diagnostic=%#v, want lifecycle help defer", request.Command, result.Decision, result.DeferredRoute, result.Diagnostic)
		}
	}
}

// TestEciLifecycleHelpCandidatesDefer keeps all direct lifecycle target
// candidates on the provider-bound route. The adapter resolves executable
// identity and the CLI, rather than the hook, validates its own arguments.
//
// Example: a mount-root-looking eci-active path defers so the adapter
// can distinguish a real different target from an unresolved spelling.
func TestEciLifecycleHelpCandidatesDefer(t *testing.T) {
	t.Parallel()

	for _, command := range []string{
		`$HOME/.omp/agent/eci/bin/eci-active --help`,
		`"$HOME/.omp/agent/eci/bin/eci-active" -h`,
		`"$HOME/.omp/agent/eci/bin/eci-active" --help extra`,
	} {
		result := Classify(Request{
			Provider:      ProviderOmp,
			Role:          RoleCoordinator,
			CWD:           "/workspace",
			Marker:        MarkerActive,
			ActiveSession: "test-session",
			Command:       command,
		})
		if result.Decision != DecisionDefer || result.Diagnostic != nil {
			t.Errorf("%q: decision=%q diagnostic=%#v, want defer without diagnostic", command, result.Decision, result.Diagnostic)
		}
	}

	for _, command := range []string{
		`$HOME/not-omp/bin/eci-active --help`,
		`"$HOME/.omp/agent/other/bin/eci-active" --help`,
		`/mnt/nvme0n1/home/pheona/.omp/agent/eci/bin/eci-active --help`,
	} {
		result := Classify(Request{
			Provider:      ProviderOmp,
			Role:          RoleCoordinator,
			CWD:           "/workspace",
			Marker:        MarkerActive,
			ActiveSession: "test-session",
			Command:       command,
		})
		if result.Decision != DecisionDefer || result.DeferredRoute != DeferredRouteEciLifecycle || result.Diagnostic != nil {
			t.Errorf("%q: decision=%q route=%q diagnostic=%#v, want lifecycle target-resolution defer", command, result.Decision, result.DeferredRoute, result.Diagnostic)
		}
	}
}

// TestKnownHomeExpansionReachesTargetAwareRoutes verifies that the canonical
// HOME shorthand is resolved as one known callback value, not treated like an
// arbitrary dynamic payload. This lets ordinary paths and lifecycle ownership
// reach their actual-effect routes while unknown variables remain ordinary
// shell evaluation rather than a planner denial.
func TestKnownHomeExpansionReachesTargetAwareRoutes(t *testing.T) {
	if home := os.Getenv("HOME"); home == "" || !filepath.IsAbs(home) {
		t.Skip("test requires an absolute HOME")
	}

	for _, command := range []string{
		`printf '%s\n' "$HOME/ordinary-path"`,
		`printf '%s\n' "${HOME}/ordinary-path"`,
	} {
		result := Classify(Request{
			Provider:      ProviderOmp,
			Role:          RoleCoordinator,
			CWD:           "/workspace",
			Marker:        MarkerActive,
			ActiveSession: "test-session",
			Command:       command,
		})
		if result.Decision != DecisionAllow || result.Diagnostic != nil {
			t.Errorf("%q: result=%#v, want ordinary allow", command, result)
		}
	}

	parsed, parseErr := parsePlan(`env ECI_SESSION_ID=test-session "$HOME/.omp/agent/eci/bin/eci-active" sync-runtime`)
	if parseErr != nil || len(parsed.segments) != 1 || len(parsed.segments[0].argv) != 4 {
		t.Fatalf("HOME lifecycle parse: plan=%#v err=%v", parsed, parseErr)
	}
	if got, want := parsed.segments[0].argv[2].value, filepath.Join(os.Getenv("HOME"), ".omp", "agent", "eci", "bin", "eci-active"); got != want {
		t.Fatalf("HOME lifecycle executable=%q, want %q", got, want)
	}

	unknown := activeWorker(`printf '%s\n' "$HOME_SUFFIX/ordinary-path"`)
	unknownResult := Classify(unknown)
	if unknownResult.Decision != DecisionAllow || unknownResult.Diagnostic != nil {
		t.Fatalf("unknown HOME-like variable result=%#v, want ordinary allow", unknownResult)
	}
}

// TestEciLifecycleTargetResolutionDoesNotUseRawSpelling verifies that raw
// quotes and expansions never create a lifecycle-specific denial. Only the
// provider adapter can establish whether a target is the current executable,
// another real target, or unresolved.
//
// Example: a command wrapper may remain subject to ordinary plan handling,
// but it cannot be rejected merely for a noncanonical eci-active spelling.
func TestEciLifecycleTargetResolutionDoesNotUseRawSpelling(t *testing.T) {
	t.Parallel()

	requestFor := func(command string) Request {
		return Request{
			Provider:      ProviderOmp,
			Role:          RoleCoordinator,
			CWD:           "/workspace",
			Marker:        MarkerActive,
			ActiveSession: "test-session",
			Command:       command,
		}
	}

	verbs := []string{
		"--help", "-h", "status", "on", "off", "wait", "wait-repair", "resume", "ledger-append",
		"manifest-write", "aggregate-migrate", "aggregate-stage", "aggregate-manifest-write",
		"aggregate-review", "aggregate-commit", "aggregate-off", "approve-commit", "maintain-planner",
		"accidental-override-cleanup", "nested-enter", "nested-accept", "nested-exit", "permissive-status",
		"permissive-on", "permissive-off", "sync-runtime",
	}
	for _, executable := range []string{
		`$HOME/.omp/agent/eci/bin/eci-active`,
		`"$HOME/.omp/agent/eci/bin/eci-active"`,
	} {
		for _, verb := range verbs {
			result := Classify(requestFor(executable + " " + verb))
			if result.Decision != DecisionDefer || result.DeferredRoute != DeferredRoute("eci-lifecycle") || result.Diagnostic != nil {
				t.Errorf("%q: decision=%q route=%q diagnostic=%#v, want typed eci lifecycle defer", executable+" "+verb, result.Decision, result.DeferredRoute, result.Diagnostic)
			}
		}
	}

	for _, command := range []string{
		`env ECI_SESSION_ID=test-session $HOME/.omp/agent/eci/bin/eci-active --help`,
		`env -- ECI_SESSION_ID=test-session "$HOME/.omp/agent/eci/bin/eci-active" status`,
		`env ECI_SESSION_ID=test-session ECI_ROLE=coordinator "$HOME/.omp/agent/eci/bin/eci-active" maintain-planner`,
		`env ECI_ROLE=coordinator ECI_SESSION_ID=test-session $HOME/.omp/agent/eci/bin/eci-active sync-runtime`,
	} {
		result := Classify(requestFor(command))
		if result.Decision != DecisionDefer || result.DeferredRoute != DeferredRoute("eci-lifecycle") || result.Diagnostic != nil {
			t.Errorf("%q: decision=%q route=%q diagnostic=%#v, want typed bounded-env lifecycle defer", command, result.Decision, result.DeferredRoute, result.Diagnostic)
		}
	}

	for _, command := range []string{
		`'$HOME/.omp/agent/eci/bin/eci-active' --help`,
		`\$HOME/.omp/agent/eci/bin/eci-active --help`,
		`"$HOME"/.omp/agent/eci/bin/eci-active --help`,
		`$ECI_HOME/bin/eci-active --help`,
		`eci-active --help`,
		`~/.omp/agent/eci/bin/eci-active --help`,
		`/home/pheona/.omp/agent/eci/bin/eci-active --help`,
		`env ECI_SESSION_ID=test-session '$HOME/.omp/agent/eci/bin/eci-active' --help`,
		`env ECI_SESSION_ID=test-session ECI_ROLE=worker "$HOME/.omp/agent/eci/bin/eci-active" maintain-planner`,
		`env ECI_SESSION_ID=test-session ECI_ROLE=reviewer "$HOME/.omp/agent/eci/bin/eci-active" maintain-planner`,
	} {
		result := Classify(requestFor(command))
		if result.Decision != DecisionDefer || result.DeferredRoute != DeferredRouteEciLifecycle || result.Diagnostic != nil {
			t.Errorf("%q: decision=%q route=%q diagnostic=%#v, want lifecycle target-resolution defer", command, result.Decision, result.DeferredRoute, result.Diagnostic)
		}
	}

	result := Classify(requestFor(`command "$HOME/.omp/agent/eci/bin/eci-active" --help`))
	if result.Diagnostic != nil && result.Diagnostic.Code == CodeLifecycleCanonicalPathDenied {
		t.Errorf("command wrapper: diagnostic=%#v, must not deny raw lifecycle spelling", result.Diagnostic)
	}
}

// TestEciLifecycleDispatcherHelpIsOrdinaryForEveryActiveRole verifies that
// dispatcher usage discovery does not become a role or path admission gate.
//
// Example: a worker can invoke eci-active-dispatch --help while ECI is active.
func TestEciLifecycleDispatcherHelpIsOrdinaryForEveryActiveRole(t *testing.T) {
	t.Parallel()

	for _, role := range []Role{RoleCoordinator, RoleWorker} {
		role := role
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()

			for _, command := range []string{
				`$HOME/.omp/agent/eci/bin/eci-active-dispatch --help`,
				`eci-active-dispatch -h`,
				`/tmp/eci-active-dispatch --help`,
				`env -- ECI_SESSION_ID=test-session "$HOME/.omp/agent/eci/bin/eci-active-dispatch" --help`,
			} {
				result := Classify(Request{
					Provider:      ProviderOmp,
					Role:          role,
					CWD:           "/workspace",
					Marker:        MarkerActive,
					ActiveSession: "test-session",
					Command:       command,
				})
				if result.Decision != DecisionAllow || result.Diagnostic != nil || result.DeferredRoute != "" {
					t.Errorf("%q: decision=%q route=%q diagnostic=%#v, want ordinary help allow", command, result.Decision, result.DeferredRoute, result.Diagnostic)
				}
			}
		})
	}
}

// TestEciLifecycleDispatcherControlStillDenies verifies that changing
// lifecycle control through the dispatcher remains a protected operation.
//
// Example: eci-active-dispatch on scope is not help discovery.
func TestEciLifecycleDispatcherControlStillDenies(t *testing.T) {
	t.Parallel()

	for _, role := range []Role{RoleCoordinator, RoleWorker} {
		role := role
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()

			result := Classify(Request{
				Provider:      ProviderOmp,
				Role:          role,
				CWD:           "/workspace",
				Marker:        MarkerActive,
				ActiveSession: "test-session",
				Command:       "eci-active-dispatch on scope",
			})
			if result.Decision != DecisionDeny || result.Diagnostic == nil ||
				result.Diagnostic.Code != CodeLifecycleCanonicalPathDenied || result.DeferredRoute != "" {
				t.Fatalf("decision=%q route=%q diagnostic=%#v, want control denial", result.Decision, result.DeferredRoute, result.Diagnostic)
			}
		})
	}
}

// TestActiveCoordinatorCompoundShellScriptDefersToReviewedAdapter keeps a
// finite shell-script invocation in a compound plan out of fast admission.
//
// Example: bash -x hooks/tests/test-validate-bash-classifier.sh | tail -n 1
// reaches the existing reviewed-script adapter for complete validation.
func TestActiveCoordinatorCompoundShellScriptDefersToReviewedAdapter(t *testing.T) {
	t.Parallel()

	result := Classify(Request{
		Provider:      ProviderOmp,
		Role:          RoleCoordinator,
		CWD:           "/workspace",
		Marker:        MarkerActive,
		ActiveSession: "test-session",
		Command:       "bash -x hooks/tests/test-validate-bash-classifier.sh | tail -n 1",
	})
	if result.Decision != DecisionDefer || result.DeferredRoute != DeferredRouteReviewedScriptCompound || result.Diagnostic != nil {
		t.Fatalf("decision=%q route=%q diagnostic=%#v, want typed compound-script defer without diagnostic", result.Decision, result.DeferredRoute, result.Diagnostic)
	}
	if result.Plan == nil || len(result.Plan.Segments) != 2 || len(result.Plan.Operators) != 1 ||
		result.Plan.Operators[0] != "|" || result.Plan.Segments[0].Command != "bash -x hooks/tests/test-validate-bash-classifier.sh " ||
		result.Plan.Segments[1].Command != " tail -n 1" {
		t.Fatalf("plan=%#v, want lossless two-segment pipe topology", result.Plan)
	}
}

// TestActiveCoordinatorReviewedScriptTraceUsesTypedRoute keeps the exact
// stderr-to-tail diagnostic topology on its narrower existing route.
//
// Example: bash -x test.sh 2>&1 | tail -n 1 is not the generic compound route.
func TestActiveCoordinatorReviewedScriptTraceUsesTypedRoute(t *testing.T) {
	t.Parallel()

	result := Classify(Request{
		Provider:      ProviderOmp,
		Role:          RoleCoordinator,
		CWD:           "/workspace",
		Marker:        MarkerActive,
		ActiveSession: "test-session",
		Command:       "bash -x hooks/tests/test-validate-bash-classifier.sh 2>&1 | tail -n 1",
	})
	if result.Decision != DecisionDefer || result.DeferredRoute != DeferredRouteReviewedScriptTrace ||
		result.Diagnostic != nil || result.Plan == nil || result.Plan.Trace == nil {
		t.Fatalf("decision=%q route=%q diagnostic=%#v plan=%#v, want typed reviewed trace defer", result.Decision, result.DeferredRoute, result.Diagnostic, result.Plan)
	}
}

// TestActiveCoordinatorDirectShellScriptRemainsAllowed keeps a finite direct
// shell-script invocation on the existing ordinary planner path.
//
// Example: bash -x hooks/tests/test-validate-bash-classifier.sh remains a
// direct finite argv for the provider adapter to validate as usual.
func TestActiveCoordinatorDirectShellScriptRemainsAllowed(t *testing.T) {
	t.Parallel()

	result := Classify(Request{
		Provider:      ProviderOmp,
		Role:          RoleCoordinator,
		CWD:           "/workspace",
		Marker:        MarkerActive,
		ActiveSession: "test-session",
		Command:       "bash -x hooks/tests/test-validate-bash-classifier.sh",
	})
	if result.Decision != DecisionAllow || result.Diagnostic != nil {
		t.Fatalf("decision=%q diagnostic=%#v, want allow without diagnostic", result.Decision, result.Diagnostic)
	}
}

// TestCompoundPlanCarriesLogicalAndPhysicalCWDIdentity verifies that a
// logical shell directory remains available for relative lookup while the
// canonical physical directory is carried for ownership checks.
//
// Example: `cd -L logical; printf after` keeps logical in cwd and real in
// cwd_physical.
func TestCompoundPlanCarriesLogicalAndPhysicalCWDIdentity(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	physical := filepath.Join(root, "physical")
	logical := filepath.Join(root, "logical")
	if err := os.Mkdir(physical, 0o700); err != nil {
		t.Fatalf("create physical directory: %v", err)
	}
	if err := os.Symlink(physical, logical); err != nil {
		t.Fatalf("create logical symlink: %v", err)
	}
	physicalIdentity, err := filepath.EvalSymlinks(physical)
	if err != nil {
		t.Fatalf("resolve physical directory: %v", err)
	}

	request := activeWorker("cd -L " + logical + "; printf after")
	request.CWD = root
	result := Classify(request)
	if result.Decision != DecisionAllow || result.Diagnostic != nil || result.Plan == nil {
		t.Fatalf("decision=%q diagnostic=%#v plan=%#v, want logical compound allow", result.Decision, result.Diagnostic, result.Plan)
	}
	if len(result.Plan.Segments) != 2 {
		t.Fatalf("segments=%#v, want two segments", result.Plan.Segments)
	}
	segment := result.Plan.Segments[1]
	if segment.CWD != logical || !segment.CWDKnown {
		t.Fatalf("segment cwd=%q known=%t, want logical %q", segment.CWD, segment.CWDKnown, logical)
	}
	encoded, err := json.Marshal(segment)
	if err != nil {
		t.Fatalf("marshal segment: %v", err)
	}
	var identity struct {
		CWDPhysical string `json:"cwd_physical"`
	}
	if err := json.Unmarshal(encoded, &identity); err != nil {
		t.Fatalf("decode segment identity: %v", err)
	}
	if identity.CWDPhysical != physicalIdentity {
		t.Fatalf("cwd_physical=%q, want %q in %s", identity.CWDPhysical, physicalIdentity, encoded)
	}
}

// TestCompoundPlanGitWorktreeRoleUsesExecutableIdentity verifies that the
// compound routing hint follows an actual Git executable and mutation verb,
// not incidental path or operand text.
//
// Example: `printf git rm; printf done` is ordinary, while `git rm file` has
// a planner-derived worktree mutation role.
func TestCompoundPlanGitWorktreeRoleUsesExecutableIdentity(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	testCases := []struct {
		name         string
		command      string
		wantEffect   string
		wantDecision DecisionKind
	}{
		{
			name:         "operand text is not a Git role",
			command:      "printf 'eci-normal-git-admission rm'; printf done",
			wantEffect:   "",
			wantDecision: DecisionAllow,
		},
		{
			name:         "ordinary path text is not a Git role",
			command:      "rm " + filepath.Join(root, "git-worktree-data") + "; printf done",
			wantEffect:   "",
			wantDecision: DecisionAllow,
		},
		{
			name:         "direct Git mutation is a role",
			command:      "git rm file.txt; printf done",
			wantEffect:   "worktree-mutation",
			wantDecision: DecisionDefer,
		},
		{
			name:         "env Git mutation is a role",
			command:      "env -- git mv old.txt new.txt; printf done",
			wantEffect:   "worktree-mutation",
			wantDecision: DecisionDefer,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			request := activeWorker(testCase.command)
			request.CWD = root
			result := Classify(request)
			if result.Decision != testCase.wantDecision || result.Diagnostic != nil || result.Plan == nil {
				t.Fatalf("decision=%q diagnostic=%#v plan=%#v, want %q with compound routing", result.Decision, result.Diagnostic, result.Plan, testCase.wantDecision)
			}
			if len(result.Plan.Segments) == 0 {
				t.Fatal("missing compound segments")
			}
			encoded, err := json.Marshal(result.Plan.Segments[0])
			if err != nil {
				t.Fatalf("marshal first segment: %v", err)
			}
			var role struct {
				GitWorktreeEffect string `json:"git_worktree_effect"`
			}
			if err := json.Unmarshal(encoded, &role); err != nil {
				t.Fatalf("decode Git role: %v", err)
			}
			if role.GitWorktreeEffect != testCase.wantEffect {
				t.Fatalf("git_worktree_effect=%q, want %q in %s", role.GitWorktreeEffect, testCase.wantEffect, encoded)
			}
		})
	}
}

func containsBytes(haystack []byte, needle []byte) bool {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if string(haystack[index:index+len(needle)]) == string(needle) {
			return true
		}
	}
	return false
}

// TestProtectedHookRootsFromExternalExecutable keeps temporary executables from protecting ordinary HOME task hooks.
//
// Example: a planner under HOME/tmp/task/eci still protects HOME/.omp/agent/eci, not HOME/hooks.
func TestProtectedHookRootsFromExternalExecutable(t *testing.T) {
	if os.Getenv("ECI_TEST_EXTERNAL_HOOK_ROOTS") == "1" {
		home := os.Getenv("HOME")
		executable, err := os.Executable()
		require.NoError(t, err)
		artifactParent := filepath.Clean(filepath.Join(filepath.Dir(executable), "..", "..", ".."))
		ordinary := filepath.Join(artifactParent, "hooks", "validate-bash.sh")
		wantProtected := os.Getenv("ECI_TEST_SOURCE_HOOK_ROOTS") == "1"
		if resolved, protected := protectedHookModeTargetPath(home, ordinary); protected != wantProtected {
			t.Errorf("ordinary task hook %q unexpectedly protected as %q", ordinary, resolved)
		}
		for _, root := range []string{filepath.Join(home, ".omp", "agent", "eci")} {
			hook := filepath.Join(root, "hooks", "validate-bash.sh")
			if _, protected := protectedHookModeTargetPath(home, hook); !protected {
				t.Errorf("provider hook %q lost protection", hook)
			}
		}
		request := activeWorker("chmod 644 " + ordinary)
		request.CWD = home
		wantDecision := DecisionAllow
		if wantProtected {
			wantDecision = DecisionDeny
		}
		if result := Classify(request); result.Decision != wantDecision {
			t.Errorf("ordinary hook chmod decision=%q diagnostic=%#v, want allow", result.Decision, result.Diagnostic)
		}
		return
	}

	root, err := os.MkdirTemp(filepath.Join(os.Getenv("HOME"), "tmp"), "hook-root-executable-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(root)) })
	executable, err := os.Executable()
	require.NoError(t, err)
	data, err := os.ReadFile(executable)
	require.NoError(t, err)
	dir := filepath.Join(root, "eci")
	require.NoError(t, os.Mkdir(dir, 0o700))
	child := filepath.Join(dir, "eci-command-plan")
	require.NoError(t, os.WriteFile(child, data, 0o700))
	command := exec.Command(child, "-test.run=^TestProtectedHookRootsFromExternalExecutable$", "-test.v")
	command.Env = append(os.Environ(), "ECI_TEST_EXTERNAL_HOOK_ROOTS=1")
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	t.Logf("external executable child: %s", output)
}

// TestProtectedHookRootsFromSourceExecutable retains the existing source-module provider fallback.
//
// Example: provider/hooks/lib/eci-command-plan-go/planner protects provider/hooks.
func TestProtectedHookRootsFromSourceExecutable(t *testing.T) {
	root, err := os.MkdirTemp(filepath.Join(os.Getenv("HOME"), "tmp"), "hook-root-source-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(root)) })
	executable, err := os.Executable()
	require.NoError(t, err)
	data, err := os.ReadFile(executable)
	require.NoError(t, err)
	dir := filepath.Join(root, "hooks", "lib", "eci-command-plan-go")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	child := filepath.Join(dir, "eci-command-plan")
	require.NoError(t, os.WriteFile(child, data, 0o700))
	command := exec.Command(child, "-test.run=^TestProtectedHookRootsFromExternalExecutable$", "-test.v")
	command.Env = append(os.Environ(), "ECI_TEST_EXTERNAL_HOOK_ROOTS=1", "ECI_TEST_SOURCE_HOOK_ROOTS=1")
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	t.Logf("source-layout executable child: %s", output)
}

// TestUnderstandingControlBasename recognizes only the canonical YAML snapshot.
//
// Example: an old Markdown snapshot name remains an ordinary file.
func TestUnderstandingControlBasename(t *testing.T) {
	for _, name := range []string{"project-understanding.yaml", "project-understanding.yaml.tmp"} {
		if !isECIControlBasename(name) {
			t.Errorf("canonical control %q was not recognized", name)
		}
	}
	for _, name := range []string{"project-understanding.md", "project-understanding.json", "project-understanding.yml", ".understanding-snapshot.ABCDEF"} {
		if isECIControlBasename(name) {
			t.Errorf("ordinary file %q inherited control identity", name)
		}
	}
}

// TestStatusReportControlIdentity preserves both current and legacy report ownership.
//
// Example: a hardlink to either report resolves to its actual coordination file.
func TestStatusReportControlIdentity(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "eci_active")
	for _, name := range []string{"eci_active", "latest-status-report.md", "latest-status-report.yaml"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("fixture\n"), 0o600))
	}
	index := activeControlFileIndex([]string{marker})
	for _, name := range []string{"latest-status-report.md", "latest-status-report.yaml"} {
		path := filepath.Join(dir, name)
		alias := filepath.Join(t.TempDir(), "alias")
		require.NoError(t, os.Link(path, alias))
		assert.Equal(t, path, activeControlResolvedPath(alias, index.files), name)
		assert.True(t, isCanonicalWorkerHandoffPath(path, []string{marker}), name)
	}
	assert.False(t, isCanonicalWorkerHandoffPath(filepath.Join(dir, "ordinary.yaml"), []string{marker}))
}
