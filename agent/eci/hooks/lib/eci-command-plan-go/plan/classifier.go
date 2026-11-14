package plan

// TODO: Retire ECI marker-dependent classification while preserving concrete target checks.

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxCommandBytes             = 16 * 1024
	maxSegments                 = 8
	maxArguments                = 128
	maxArgumentBytes            = 4 * 1024
	maxWrapperDepth             = 8
	maxReachableStates          = 128
	maxProofAnchors             = 128
	timeoutProbeTimeout         = 5 * time.Second
	timeoutProbeWaitDelay       = 25 * time.Millisecond
	timeoutProbeChild           = "/usr/bin/printf"
	timeoutProbeAcknowledgement = "eci-timeout-child-launch-v1"
	// segmentReachable marks a segment that has a concrete reachable branch.
	//
	// Example: `if true; then printf ok; fi` marks its body reachable.
	segmentReachable SegmentReachability = "reachable"
	// segmentUnreachable marks a segment proven dead by literal shell status.
	//
	// Example: `if false; then printf skipped; fi` marks its body unreachable.
	segmentUnreachable SegmentReachability = "unreachable"
	// segmentReachabilityUnknown marks a segment that may execute.
	//
	// Example: `if test -e flag; then printf maybe; fi` remains unknown.
	segmentReachabilityUnknown SegmentReachability = "unknown"
	// executePermission is the POSIX X_OK access mode used to verify a
	// directory before modeling a shell transition into it.
	//
	// Example: syscall.Access(path, executePermission) checks search access.
	executePermission = 1
)

// Provider identifies the provider adapter requesting command-plan admission.
type Provider string

const (
	// ProviderOmp selects the omp adapter contract.
	ProviderOmp Provider = "omp"
	// ProviderKimi selects the Kimi adapter contract.
	ProviderKimi Provider = "kimi"
)

// Role identifies whether the callback belongs to the coordinator or a worker.
type Role string

const (
	// RoleCoordinator identifies the main/orchestrator owner.
	RoleCoordinator Role = "coordinator"
	// RoleWorker identifies a delegated worker callback.
	RoleWorker Role = "worker"
)

// Marker identifies whether the callback has a validated active ECI marker.
type Marker string

const (
	// MarkerActive indicates that ECI ownership checks are active.
	MarkerActive Marker = "active"
	// MarkerInactive indicates that no active ECI marker was discovered.
	MarkerInactive Marker = "inactive"
)

// DecisionKind is the compiled planner's admission disposition.
type DecisionKind string

const (
	// DecisionAllow admits the finite command plan.
	DecisionAllow DecisionKind = "allow"
	// DecisionDefer routes the capability to a provider-specific adapter.
	DecisionDefer DecisionKind = "defer"
	// DecisionDeny rejects the command plan with a structured diagnostic.
	DecisionDeny DecisionKind = "deny"
	// DecisionError reports an invalid planner request or internal failure.
	DecisionError DecisionKind = "error"
)

// Capability identifies a recognized protected command capability.
type Capability string

const (
	// CapabilityGateMode identifies a command-gate mode mutation.
	CapabilityGateMode Capability = "gate-mode"
	// CapabilityGitCloneSourceAcquisition identifies a direct Git clone
	// whose clone options and operands remain owned by Git.
	//
	// Example: git clone --branch release source destination selects this capability.
	CapabilityGitCloneSourceAcquisition Capability = "git-clone-source-acquisition"
)

// GitCloneLaunchClass identifies the finite launcher form that selects a Git
// clone source-acquisition capability.
type GitCloneLaunchClass string

const (
	// GitCloneLaunchDirect identifies a direct Git executable argv.
	GitCloneLaunchDirect GitCloneLaunchClass = "direct"
	// GitCloneLaunchCommand identifies command [--] Git argv.
	GitCloneLaunchCommand GitCloneLaunchClass = "command"
	// GitCloneLaunchEnv identifies env [--] Git argv with no environment change.
	GitCloneLaunchEnv GitCloneLaunchClass = "env"
)

// GitCloneLaunch records the literal executable tokens that the provider must
// bind before it can fast-admit an otherwise opaque Git clone argv.
//
// Git continues to own clone option and operand parsing. This metadata only
// describes the executable and inherited-environment launch boundary.
type GitCloneLaunch struct {
	Class                GitCloneLaunchClass `json:"class"`
	GitArgvIndex         int                 `json:"git_argv_index"`
	GitExecutable        string              `json:"git_executable"`
	EnvironmentPreserved bool                `json:"environment_preserved"`
	EnvArgvIndex         *int                `json:"env_argv_index,omitempty"`
	EnvExecutable        string              `json:"env_executable,omitempty"`
}

// DeferredRoute identifies one provider-owned route selected after complete
// command-plan classification.
type DeferredRoute string

const (
	// DeferredRouteEciLifecycle identifies a candidate whose executable must
	// be resolved by the provider adapter. The adapter compares the actual
	// target with the current eci lifecycle executable; spelling alone does
	// not grant or remove lifecycle access.
	DeferredRouteEciLifecycle DeferredRoute = "eci-lifecycle"
	// DeferredRouteReviewedScriptTrace identifies the one bounded coordinator
	// trace diagnostic that hands a reviewed test to the existing script route.
	DeferredRouteReviewedScriptTrace DeferredRoute = "reviewed-script-trace"
	// DeferredRouteReviewedScriptCompound identifies a compound plan containing
	// a finite shell-script invocation. The provider adapter validates the
	// complete raw topology before a reviewed script can run.
	DeferredRouteReviewedScriptCompound DeferredRoute = "reviewed-script-compound"
)

// DiagnosticCode is the stable machine-readable reason for a denial.
type DiagnosticCode string

const (
	// CodeLifecycleCanonicalPathDenied reports an eci lifecycle-looking
	// executable that is not one of the literal current-home spellings.
	CodeLifecycleCanonicalPathDenied DiagnosticCode = "ECI_LIFECYCLE_CANONICAL_PATH_DENIED"
	// CodePlanSyntaxDenied reports unsupported shell-plan syntax.
	CodePlanSyntaxDenied DiagnosticCode = "ECI_PLAN_SYNTAX_DENIED"
	// CodePlanLimitDenied reports a parser capacity result. Classify maps it to
	// transparent target-aware fallback rather than exposing it as a denial.
	CodePlanLimitDenied DiagnosticCode = "ECI_PLAN_LIMIT_DENIED"
	// CodePlanWrapperDenied reports an incomplete transparent wrapper.
	CodePlanWrapperDenied DiagnosticCode = "ECI_PLAN_WRAPPER_DENIED"
	// CodePlanDynamicLaunchDenied reports dynamic executable launching.
	CodePlanDynamicLaunchDenied DiagnosticCode = "ECI_PLAN_DYNAMIC_LAUNCH_DENIED"
	// CodePlanStatFormatDenied reports a stat inspection outside the bounded
	// literal metadata grammar.
	CodePlanStatFormatDenied DiagnosticCode = "ECI_PLAN_STAT_FORMAT_DENIED"
	// CodePlanFileOptionDenied reports a file invocation that can compile a
	// magic database or otherwise falls outside its bounded inspection grammar.
	CodePlanFileOptionDenied DiagnosticCode = "ECI_PLAN_FILE_OPTION_DENIED"
	// CodePlanUniqArgumentsDenied reports uniq's optional output operand or an
	// unsupported option outside its bounded filter grammar.
	CodePlanUniqArgumentsDenied DiagnosticCode = "ECI_PLAN_UNIQ_ARGUMENTS_DENIED"
	// CodeEnvironmentEnumerationDenied reports unbounded environment enumeration.
	CodeEnvironmentEnumerationDenied DiagnosticCode = "ECI_ENVIRONMENT_ENUMERATION_DENIED"
	// CodeEnvironmentNameDenied reports an unregistered environment name.
	CodeEnvironmentNameDenied DiagnosticCode = "ECI_ENVIRONMENT_NAME_DENIED"
	// CodeEnvironmentOptionDenied reports an unsupported environment option.
	CodeEnvironmentOptionDenied DiagnosticCode = "ECI_ENVIRONMENT_OPTION_DENIED"
	// CodeEnvironmentContextDenied reports unsafe environment execution context.
	CodeEnvironmentContextDenied DiagnosticCode = "ECI_ENVIRONMENT_CONTEXT_DENIED"
	// CodeWorkerGitOwnershipDenied reports worker-owned Git mutation.
	CodeWorkerGitOwnershipDenied DiagnosticCode = "ECI_WORKER_GIT_OWNERSHIP_DENIED"
	// CodeGitExecutionContextDenied reports unsafe Git execution context.
	CodeGitExecutionContextDenied DiagnosticCode = "ECI_GIT_EXECUTION_CONTEXT_DENIED"
	// CodeControlOwnerRequired reports a coordinator-owned control mutation.
	CodeControlOwnerRequired DiagnosticCode = "ECI_CONTROL_OWNER_REQUIRED"
	// CodeControlIdentityDenied reports an untrusted control executable identity.
	CodeControlIdentityDenied DiagnosticCode = "ECI_CONTROL_IDENTITY_DENIED"
	// CodeBroadDestructiveDenied reports a broad destructive operation.
	CodeBroadDestructiveDenied DiagnosticCode = "ECI_BROAD_DESTRUCTIVE_DENIED"
	// CodeLedgerAppendOnly reports a direct mutation of an append-only ledger.
	CodeLedgerAppendOnly DiagnosticCode = "ECI_LEDGER_APPEND_ONLY"
	// CodeLedgerRewriteDenied reports a redirect that would replace the
	// selected session's high-level ledger instead of appending at EOF.
	CodeLedgerRewriteDenied DiagnosticCode = "ECI_LEDGER_REWRITE_DENIED"
	// CodeLedgerAnchorWriteDenied reports a redirect that targets the selected
	// session's high-level ledger anchor control artifact.
	CodeLedgerAnchorWriteDenied DiagnosticCode = "ECI_LEDGER_ANCHOR_WRITE_DENIED"
	// CodeLedgerForeignSessionDenied reports a redirect that targets a sibling
	// proof session's ledger record.
	CodeLedgerForeignSessionDenied DiagnosticCode = "ECI_LEDGER_FOREIGN_SESSION_DENIED"
	// CodeLedgerSharedInodeDenied reports an append target whose inode has more
	// than one link and therefore cannot be uniquely owned by the session log.
	CodeLedgerSharedInodeDenied DiagnosticCode = "ECI_LEDGER_SHARED_INODE_DENIED"
	// CodeProofPathEscapeDenied reports a proof-path ownership escape.
	CodeProofPathEscapeDenied DiagnosticCode = "ECI_PROOF_PATH_ESCAPE_DENIED"
	// CodePlanLiveControlDenied reports a live control-file ownership violation.
	CodePlanLiveControlDenied DiagnosticCode = "ECI_PLAN_LIVE_CONTROL_DENIED"
	// CodePlanInternalDenied reports malformed planner input or internal failure.
	CodePlanInternalDenied DiagnosticCode = "ECI_PLAN_INTERNAL_DENIED"
)

// Request is the bounded JSON request consumed by the compiled planner.
type Request struct {
	// HookMode selects callback-owned effect routing without changing the planner CLI.
	HookMode bool `json:"-"`
	// HookSessionID preserves the callback session independently of its parent owner.
	HookSessionID       string          `json:"-"`
	Provider            Provider        `json:"provider"`
	Role                Role            `json:"role"`
	CWD                 string          `json:"cwd"`
	CommandPath         string          `json:"command_path,omitempty"`
	CommandPathSet      bool            `json:"command_path_set"`
	CommandPathExported *bool           `json:"command_path_exported,omitempty"`
	CDPath              string          `json:"cd_path,omitempty"`
	CDPathSet           bool            `json:"cd_path_set"`
	TimeoutReplay       bool            `json:"timeout_replay,omitempty"`
	TimeoutReplays      []TimeoutReplay `json:"timeout_replays,omitempty"`
	Marker              Marker          `json:"marker"`
	ActiveSession       string          `json:"active_session"`
	Command             string          `json:"command"`
	ActiveMarkers       []string        `json:"active_markers"`
	ApprovedRoots       []string        `json:"approved_roots"`
	// SegmentCWD and SegmentCWDKnown are an internal adapter handoff for a
	// recursively validated compound segment. The outer callback CWD remains
	// the marker-ownership anchor while this verified planner fact resolves
	// relative segment effects.
	SegmentCWD                   string   `json:"segment_cwd,omitempty"`
	SegmentCWDKnown              *bool    `json:"segment_cwd_known,omitempty"`
	SegmentCWDUnknown            *bool    `json:"segment_cwd_unknown,omitempty"`
	SegmentCWDCandidates         []string `json:"segment_cwd_candidates,omitempty"`
	SegmentCWDPhysical           string   `json:"segment_cwd_physical,omitempty"`
	SegmentCWDPhysicalCandidates []string `json:"segment_cwd_physical_candidates,omitempty"`
	CWDCandidates                []string `json:"-"`
	CWDPhysical                  string   `json:"-"`
	CWDPhysicalCandidates        []string `json:"-"`
	// CWDKnown is derived by Classify from the verified callback or segment
	// directory. The Bash adapter authenticates SegmentCWDKnown before
	// forwarding that internal handoff to a recursive planner callback.
	CWDKnown             bool                `json:"-"`
	CWDUnknown           bool                `json:"-"`
	CollectShellCommands bool                `json:"-"`
	ShellDepth           int                 `json:"-"`
	ShellEnvironment     []shellArgument     `json:"-"`
	ShellInputDynamic    bool                `json:"-"`
	ShellExpansion       shellExpansionState `json:"-"`
}

// TimeoutReplayDisposition describes whether a direct timeout prefix was
// observed launching the harmless probe child or remains opaque.
//
// Example: a timeout executable that exits before running its child has the
// opaque disposition.
type TimeoutReplayDisposition string

const (
	// TimeoutReplayObserved marks a timeout prefix whose probe child launched.
	//
	// Example: timeout 5 git status records observed when its replacement
	// printf child acknowledges execution.
	TimeoutReplayObserved TimeoutReplayDisposition = "observed"
	// TimeoutReplayOpaque marks a known timeout nonlaunch with a complete
	// literal execution context.
	//
	// Example: PATH=; timeout 5 git status records opaque because bare timeout
	// cannot resolve from the explicitly empty PATH.
	TimeoutReplayOpaque TimeoutReplayDisposition = "opaque"
)

// TimeoutReplay records the direct literal timeout prefix, its modeled shell
// execution state, and whether the harmless replacement child was observed.
// Segment is local to the request plan while ParentSegment preserves the
// original compound-plan coordinate during recursive direct-route replay.
//
// Example: a top-level segment two record becomes segment one with
// ParentSegment two when the Bash adapter validates that segment recursively.
type TimeoutReplay struct {
	Segment             int                      `json:"segment"`
	ParentSegment       int                      `json:"parent_segment"`
	Prefix              []string                 `json:"prefix"`
	CWD                 string                   `json:"cwd"`
	CWDPhysical         string                   `json:"cwd_physical,omitempty"`
	CommandPath         string                   `json:"command_path"`
	CommandPathSet      bool                     `json:"command_path_set"`
	CommandPathExported bool                     `json:"command_path_exported"`
	Disposition         TimeoutReplayDisposition `json:"disposition"`
}

// TimeoutLaunch records the callback-selected timeout prefix whose harmless
// replacement child was observed running before the planner examines its
// original child argv.
//
// Example: `timeout --signal TERM 5 git status` records the prefix
// ["timeout", "--signal", "TERM", "5"] for its command segment.
type TimeoutLaunch struct {
	Segment int      `json:"segment"`
	Prefix  []string `json:"prefix"`
}

// Diagnostic describes one denied command-plan coordinate and remediation.
type Diagnostic struct {
	Code            DiagnosticCode `json:"code"`
	Operation       string         `json:"operation"`
	Segment         int            `json:"segment"`
	ArgvIndex       int            `json:"argv_index"`
	ByteOffset      int            `json:"byte_offset"`
	Token           string         `json:"token"`
	Path            string         `json:"path"`
	Predicate       string         `json:"predicate"`
	Reason          string         `json:"reason"`
	Remediation     string         `json:"remediation"`
	RejectedSegment string         `json:"rejected_segment"`
}

// HookSpecificOutput carries the provider-neutral PreToolUse denial payload.
type HookSpecificOutput struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason"`
}

// PlanSegment records one lossless direct-command slice from a bounded
// compound plan. The Bash adapter revalidates Command through the same direct
// route that a standalone callback would use.
//
// Example: the middle segment of `sed && printf ok` is ` printf ok`.
type PlanSegment struct {
	Command               string              `json:"command"`
	CWD                   string              `json:"cwd"`
	CWDPhysical           string              `json:"cwd_physical"`
	CWDKnown              bool                `json:"cwd_known"`
	CWDUnknown            bool                `json:"cwd_unknown"`
	CWDCandidates         []string            `json:"cwd_candidates"`
	CWDPhysicalCandidates []string            `json:"cwd_physical_candidates"`
	Reachability          SegmentReachability `json:"reachability"`
	GitWorktreeEffect     GitWorktreeEffect   `json:"git_worktree_effect"`
}

// GitWorktreeEffect identifies a planner-derived Git executable role that
// must remain on the concrete Git ownership route.
//
// Example: `git rm file.txt` has a worktree mutation effect, while `printf
// git rm` has no Git executable role.
type GitWorktreeEffect string

const (
	// GitWorktreeEffectNone marks a segment without a supported Git worktree role.
	//
	// Example: `git status` has no worktree mutation effect.
	GitWorktreeEffectNone GitWorktreeEffect = ""
	// GitWorktreeEffectMutation marks a direct Git worktree mutation role.
	//
	// Example: `git restore file.txt` has a worktree mutation effect.
	GitWorktreeEffectMutation GitWorktreeEffect = "worktree-mutation"
)

// SegmentReachability records whether a lossless compound segment can execute.
// Unknown keeps conservative candidate effects visible; only unreachable skips
// planner effect inspection.
//
// Example: a `false` branch body is `unreachable`, while `test -e flag` is
// `unknown` and remains subject to target checks.
type SegmentReachability string

// ReviewedScriptTraceTopology records the complete literal grammar for the
// one reviewed-test trace diagnostic. Command keeps the original bytes while
// the remaining fields preserve each bounded shell topology component.
//
// Example: `bash -x hooks/tests/example.sh 2>&1 | tail -n 20` has a bash
// shell, `-x` flag, literal script, stderr redirect, pipe, trusted tail sink,
// `-n` flag, and `20` line count.
type ReviewedScriptTraceTopology struct {
	Command   string `json:"command"`
	Shell     string `json:"shell"`
	ShellFlag string `json:"shell_flag"`
	Script    string `json:"script"`
	Redirect  string `json:"redirect"`
	Operator  string `json:"operator"`
	Sink      string `json:"sink"`
	SinkFlag  string `json:"sink_flag"`
	Lines     string `json:"lines"`
}

// PlanTopology records either the ordered direct-command slices and shell
// operators parsed from one finite compound plan, or the one typed reviewed
// trace topology. A raw punctuation plan is never represented as Trace.
//
// Example: `printf left && printf right` has two segments and one `&&` operator.
type PlanTopology struct {
	Segments  []PlanSegment                `json:"segments,omitempty"`
	Operators []string                     `json:"operators,omitempty"`
	Trace     *ReviewedScriptTraceTopology `json:"trace,omitempty"`
}

// Result is the compiled planner's structured admission response.
type Result struct {
	Decision             DecisionKind        `json:"decision"`
	Capabilities         []Capability        `json:"capabilities,omitempty"`
	GitCloneLaunch       *GitCloneLaunch     `json:"git_clone_launch,omitempty"`
	DeferredRoute        DeferredRoute       `json:"deferred_route,omitempty"`
	Diagnostic           *Diagnostic         `json:"diagnostic,omitempty"`
	HookSpecificOutput   *HookSpecificOutput `json:"hookSpecificOutput,omitempty"`
	LedgerRedirectAppend bool                `json:"ledger_redirect_append,omitempty"`
	TimeoutLaunches      []TimeoutLaunch     `json:"timeout_launches,omitempty"`
	TimeoutReplays       []TimeoutReplay     `json:"timeout_replays,omitempty"`
	Plan                 *PlanTopology       `json:"plan,omitempty"`
	ShellAnalysis        *ShellAnalysis      `json:"shell_analysis,omitempty"`
}

type token struct {
	value  string
	offset int
	// sourceOffsets maps decoded bytes to their original command-byte positions.
	sourceOffsets       []int
	quoted              bool
	inputRedirectTarget bool
}

// outputRedirectEffect identifies the actual filesystem effect of one shell
// output redirect independently of the command that produced its bytes.
//
// Example: `>> log` is append while `>| log` is force-overwrite.
type outputRedirectEffect string

const (
	// outputRedirectAppend records an EOF append effect.
	outputRedirectAppend outputRedirectEffect = "append"
	// outputRedirectOverwrite records a normal truncating overwrite effect.
	outputRedirectOverwrite outputRedirectEffect = "overwrite"
	// outputRedirectForceOverwrite records an overwrite that bypasses noclobber.
	outputRedirectForceOverwrite outputRedirectEffect = "force-overwrite"
)

// outputRedirect records the target token and resolved shell effect of one
// output redirect without presenting the target as an executable argv word.
//
// Example: `printf note 2>> log` records log with the append effect.
type outputRedirect struct {
	target                token
	effect                outputRedirectEffect
	descriptorDuplication bool
}

// lifecycleLexicalWord retains a decoded shell word and its source offset for
// the provider adapter. Its original spelling is diagnostic context only;
// executable identity, not quote or expansion spelling, determines lifecycle
// authority.
type lifecycleLexicalWord struct {
	raw    string
	value  string
	offset int
}

type segment struct {
	argv            []token
	redirects       []outputRedirect
	inputRedirects  []token
	offset          int
	sourceStart     int
	command         string
	groupOpens      []compoundGroupKind
	groupCloses     []compoundGroupKind
	groupSyntaxSafe bool
}

type plan struct {
	segments             []segment
	operators            []string
	projection           *shellProjection
	projectionIncomplete bool
}

// compoundPlanTopology copies the lossless ordered parser output for an
// adapter that must validate compound segments through direct command routes.
//
// Example: a direct argv has no topology, while `left && right` has two
// command slices and one operator.
func compoundPlanTopology(
	parsed plan,
	segmentStates []compoundSegmentState,
) *PlanTopology {
	if parsed.projection != nil || len(parsed.operators) == 0 {
		return nil
	}
	segments := make([]PlanSegment, 0, len(parsed.segments))
	for index, current := range parsed.segments {
		state := compoundSegmentState{}
		if index < len(segmentStates) {
			state = segmentStates[index]
		}
		segments = append(segments, PlanSegment{
			Command:               current.command,
			CWD:                   state.cwd.cwd,
			CWDPhysical:           state.cwd.physical,
			CWDKnown:              state.cwd.known,
			CWDUnknown:            state.cwd.unknown,
			CWDCandidates:         append([]string{}, state.cwd.candidates...),
			CWDPhysicalCandidates: append([]string{}, state.cwd.physicalCandidates...),
			Reachability:          state.reachability,
			GitWorktreeEffect:     compoundGitWorktreeEffect(current),
		})
	}
	return &PlanTopology{
		Segments:  segments,
		Operators: append([]string(nil), parsed.operators...),
	}
}

// withCompoundPlan attaches parser-owned compound topology to one planner
// result without changing the result's decision or diagnostic.
//
// Example: a denial in segment two still returns the complete two-segment
// topology so an adapter can preserve parser diagnostic precedence.
func withCompoundPlan(
	result Result,
	parsed plan,
	segmentStates []compoundSegmentState,
) Result {
	result.Plan = compoundPlanTopology(parsed, segmentStates)
	return result
}

// compoundGitWorktreeEffect derives the concrete Git worktree role for one
// parsed segment without searching command text or operands for substrings.
//
// Example: `env -- git mv old new` selects a mutation role, while `printf
// git mv` remains role-free.
func compoundGitWorktreeEffect(current segment) GitWorktreeEffect {
	if len(current.argv) == 0 {
		return GitWorktreeEffectNone
	}
	unwrapped, diagnostic := unwrapWithMetadata(current.argv, 1, nil)
	if diagnostic != nil || len(unwrapped.argv) == 0 || filepath.Base(unwrapped.argv[0].value) != "git" {
		return GitWorktreeEffectNone
	}
	subcommandIndex := gitSubcommandIndex(unwrapped.argv)
	if subcommandIndex >= len(unwrapped.argv) {
		return GitWorktreeEffectNone
	}
	switch unwrapped.argv[subcommandIndex].value {
	case "checkout", "mv", "restore", "rm", "worktree":
		return GitWorktreeEffectMutation
	default:
		return GitWorktreeEffectNone
	}
}

type proofSession struct {
	lexical  string
	resolved string
}

type gateModeIdentity struct {
	canonicalPaths []string
	failure        string
}

// eciControlBasenames lists the session control filenames used for target discovery.
//
// Example: latest-status-report.yaml is indexed as a session control document.
var eciControlBasenames = [...]string{
	"eci_active", "goal_state", "eci_wait", "eci_user_owned_wait.md", "eci-additional-repository",
	"eci-required-critics.json", "eci-critic-identities.ledger",
	"eci-acceptance-anchor", "eci-acceptance-transaction",
	"eci-teardown-complete", "eci-baseline-binding", "baseline_head",
	"eci-commit-admitted", "eci-user-closed.ledger", "proof.md",
	"eci-aggregate", "eci-aggregate-plan.json", "eci-aggregate-teardown-complete",
	"instructions.md", "stop_timestamps", "stop_loop_state",
	"disengage.md", "user-closed.md", "project-understanding.yaml",
	"high_level_log.jsonl", "latest-status-report.md", "latest-status-report.yaml", "high_level_log.anchor",
	"forecasts.yaml",
}

type planError struct {
	diagnostic Diagnostic
}

func (err *planError) Error() string {
	return err.diagnostic.Reason
}

// classifyEciLifecycleTargetCandidate selects a direct eci-active target or
// an env-launched target for provider-owned identity resolution. It does not
// authorize a raw command spelling: the provider compares the executable the
// shell would select with the current eci target.
//
// Example: `"$HOME"/.omp/agent/eci/bin/eci-active status` and `eci-active status`
// both defer so the provider can compare their resolved executable identities.
func classifyEciLifecycleTargetCandidate(command string) (lifecycleLexicalWord, string, bool) {
	words, ok := lexLifecycleWords(command)
	if !ok || len(words) == 0 {
		return lifecycleLexicalWord{}, "", false
	}

	childIndex := 0
	if lifecycleEnvironmentLauncherCandidate(words[0].value) {
		childIndex = 1
		if childIndex < len(words) && words[childIndex].value == "--" {
			childIndex++
		}
		for childIndex < len(words) {
			name, _, hasAssignment := strings.Cut(words[childIndex].value, "=")
			if !hasAssignment || !isIdentifier(name) {
				break
			}
			childIndex++
		}
	}

	if childIndex >= len(words) {
		return lifecycleLexicalWord{}, "", false
	}
	child := words[childIndex]
	if !lifecycleExecutableCandidate(child.value) {
		return lifecycleLexicalWord{}, "", false
	}
	verb := ""
	if childIndex+1 < len(words) {
		verb = words[childIndex+1].value
	}
	return child, verb, true
}

// isLifecycleReadOnlyVerb reports whether verb only discovers lifecycle state
// or CLI usage and therefore has no visible control mutation target.
//
// Example: status and --help are read-only, while teardown is not.
func isLifecycleReadOnlyVerb(verb string) bool {
	switch verb {
	case "status", "--help", "-h":
		return true
	default:
		return false
	}
}

// isLifecycleReadOnlyInvocation reports whether an unwrapped lifecycle argv
// starts with a state-discovery or usage verb.
//
// Example: a copied eci-active status command stays ordinary even when the
// executable bytes match a provider lifecycle binary.
func isLifecycleReadOnlyInvocation(argv []token) bool {
	return len(argv) >= 2 && isLifecycleReadOnlyVerb(argv[1].value)
}

// isWorkerRepositoryAllowInvocation recognizes the one lifecycle route that
// an owning worker may use to declare an additional dependency repository.
// The lifecycle binary performs the marker, repository, and reason checks;
// the planner only keeps this exact route from becoming a generic control
// denial.  Other lifecycle verbs remain coordinator-owned.
func isWorkerRepositoryAllowInvocation(argv []token) bool {
	if len(argv) < 2 || filepath.Base(argv[0].value) != "eci-active" {
		return false
	}
	switch argv[1].value {
	case "repository-allow-status":
		return len(argv) == 2
	case "repository-allow-off":
		return len(argv) == 3 && filepath.IsAbs(argv[2].value) && filepath.Clean(argv[2].value) == argv[2].value
	case "repository-allow-on":
		if len(argv) != 4 || !filepath.IsAbs(argv[2].value) || filepath.Clean(argv[2].value) != argv[2].value || argv[3].value == "" {
			return false
		}
		return !strings.ContainsAny(argv[3].value, "\r\n") &&
			!strings.ContainsFunc(argv[3].value, func(r rune) bool { return r < 0x20 || r == 0x7f })
	default:
		return false
	}
}

// lifecycleEnvironmentLauncherCandidate recognizes an env-looking first
// word for later identity verification. The provider rejects a PATH shadow
// unless it resolves to the real system env executable.
//
// Example: `/usr/bin/env eci-active status` is a candidate, while `command
// eci-active status` remains outside this direct-launch route.
func lifecycleEnvironmentLauncherCandidate(value string) bool {
	return filepath.Base(value) == "env"
}

// lifecycleExecutableCandidate recognizes a target that may resolve to
// eci-active. The provider performs the actual same-file comparison and lets
// unknown syntax fall through without a lifecycle-spelling denial.
//
// Example: `$ECI_HOME/bin/eci-active` is a candidate even though its value
// depends on the caller's environment.
func lifecycleExecutableCandidate(value string) bool {
	return filepath.Base(value) == "eci-active"
}

func isEciLifecycleDispatcherLookalike(value string) bool {
	if value == "eci-active-dispatch" {
		return true
	}
	return (filepath.IsAbs(value) || strings.HasPrefix(value, "~") || strings.HasPrefix(value, "$")) &&
		filepath.Base(value) == "eci-active-dispatch"
}

// classifyEciLifecycleDispatcherInvocation finds an explicit dispatcher
// target and its visible verb for the separate control boundary. It
// intentionally does not inspect eci-active candidates, which use same-target
// identity resolution instead.
//
// Example: `eci-active-dispatch --help` returns the dispatcher and --help.
func classifyEciLifecycleDispatcherInvocation(command string) (lifecycleLexicalWord, string, bool) {
	words, ok := lexLifecycleWords(command)
	if !ok {
		return lifecycleLexicalWord{}, "", false
	}
	for index, word := range words {
		if isEciLifecycleDispatcherLookalike(word.value) {
			verb := ""
			if index+1 < len(words) {
				verb = words[index+1].value
			}
			return word, verb, true
		}
	}
	return lifecycleLexicalWord{}, "", false
}

// lexLifecycleWords implements only the finite shell-word subset needed to
// retain a raw lifecycle executable token. Shell controls and malformed
// quoting intentionally return false so the general planner can deny them.
func lexLifecycleWords(command string) ([]lifecycleLexicalWord, bool) {
	if !utf8.ValidString(command) || strings.IndexAny(command, "\x00\r\n") >= 0 {
		return nil, false
	}

	words := make([]lifecycleLexicalWord, 0, 4)
	for index := 0; index < len(command); {
		for index < len(command) && (command[index] == ' ' || command[index] == '\t') {
			index++
		}
		if index == len(command) {
			break
		}
		start := index
		var value strings.Builder
		quote := byte(0)
		escaped := false
		for index < len(command) {
			character := command[index]
			if escaped {
				value.WriteByte(character)
				escaped = false
				index++
				continue
			}
			if quote == '\'' {
				if character == '\'' {
					quote = 0
				} else {
					value.WriteByte(character)
				}
				index++
				continue
			}
			if character == '\\' {
				escaped = true
				index++
				continue
			}
			if character == '\'' || character == '"' {
				if quote == character {
					quote = 0
				} else if quote == 0 {
					quote = character
				} else {
					value.WriteByte(character)
				}
				index++
				continue
			}
			if quote == 0 {
				if character == ' ' || character == '\t' {
					break
				}
				if strings.ContainsRune(";|&()<>", rune(character)) {
					return nil, false
				}
			}
			value.WriteByte(character)
			index++
		}
		if escaped || quote != 0 || start == index {
			return nil, false
		}
		words = append(words, lifecycleLexicalWord{raw: command[start:index], value: value.String(), offset: start})
	}
	return words, true
}

// eciLifecycleDispatcherDiagnostic reports an eci-active-dispatch control
// target. The dispatcher is distinct from eci-active and does not participate
// in same-target lifecycle compatibility resolution.
func eciLifecycleDispatcherDiagnostic(command string, word lifecycleLexicalWord) Diagnostic {
	return Diagnostic{
		Code:            CodeLifecycleCanonicalPathDenied,
		Operation:       "eci-lifecycle",
		Segment:         1,
		ArgvIndex:       0,
		ByteOffset:      word.offset,
		Token:           word.raw,
		Path:            "n/a",
		Predicate:       "distinct-lifecycle-dispatcher-target",
		Reason:          "eci-active-dispatch is a distinct lifecycle control target, not the eci-active command",
		Remediation:     "the lifecycle control route is present but rejects lifecycle commands without publishing state; native goals own continuation and the Supervisor owns ECI lifecycle; report the unavailable control route instead of invoking another executable",
		RejectedSegment: shellEscape(command),
	}
}

// ledgerAppendRemediation returns copy-paste-safe ledger guidance for a provider.
//
// Example: an omp denial names the literal current-home lifecycle executable.
func ledgerAppendRemediation(provider Provider) string {
	if provider == ProviderOmp {
		return "append the entry with \"$HOME/.omp/agent/eci/bin/eci-active\" ledger-append --json <event-object>; do not edit the ledger directly"
	}
	// Kimi does not share omp's literal current-home lifecycle grammar.
	return "use eci-active ledger-append --json"
}

// reviewedScriptTraceTopology recognizes exactly the coordinator diagnostic
// grammar `bash|sh -x REVIEWED_TEST 2>&1 | tail -n N`. It deliberately runs
// before parsePlan because the redirect and pipe are part of this one typed
// topology, not a general shell-syntax admission.
func reviewedScriptTraceTopology(command string) (*ReviewedScriptTraceTopology, bool) {
	words := strings.Split(command, " ")
	if len(words) != 8 || words[0] != "bash" && words[0] != "sh" ||
		words[1] != "-x" || !literalReviewedTestPath(words[2]) ||
		words[3] != "2>&1" || words[4] != "|" || words[5] != "tail" ||
		words[6] != "-n" || !boundedTraceLineCount(words[7]) {
		return nil, false
	}
	return &ReviewedScriptTraceTopology{
		Command:   command,
		Shell:     words[0],
		ShellFlag: words[1],
		Script:    words[2],
		Redirect:  words[3],
		Operator:  words[4],
		Sink:      words[5],
		SinkFlag:  words[6],
		Lines:     words[7],
	}, true
}

// literalReviewedTestPath admits only the literal path-token alphabet used by
// the reviewed-test adapter. Canonical root, regular-file, non-symlink,
// reviewed-membership, and digest checks remain adapter-owned.
func literalReviewedTestPath(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || !strings.HasSuffix(value, ".sh") ||
		strings.Contains(value, "..") {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '.' || character == '_' ||
			character == '-' || character == '/' {
			continue
		}
		return false
	}
	return true
}

// boundedTraceLineCount accepts the documented inclusive decimal range 1..200
// without accepting alternate spellings such as a leading-zero count.
func boundedTraceLineCount(value string) bool {
	if len(value) == 0 || len(value) > 3 || value[0] == '0' {
		return false
	}
	count := 0
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
		count = count*10 + int(character-'0')
	}
	return count >= 1 && count <= 200
}

// Classify parses and admits one bounded command plan.
func Classify(request Request) Result {
	request.CWDUnknown = false
	request.CWDPhysical = ""
	request.CWDPhysicalCandidates = nil
	if request.SegmentCWDKnown != nil {
		var overflow bool
		request.CWDCandidates, overflow = verifiedUniqueDirectoriesBounded(request.SegmentCWDCandidates)
		if request.SegmentCWDUnknown != nil {
			request.CWDUnknown = *request.SegmentCWDUnknown
		}
		request.CWDUnknown = request.CWDUnknown || overflow
		if *request.SegmentCWDKnown && !request.CWDUnknown && isVerifiedAbsoluteTimeoutCWD(request.SegmentCWD) {
			request.CWD = request.SegmentCWD
			request.CWDCandidates = verifiedUniqueDirectories(append(request.CWDCandidates, request.SegmentCWD))
			request.CWDPhysical, _ = verifiedPhysicalCWD(request.CWD)
		} else {
			request.CWD = ""
		}
	}
	request.CWDKnown = isVerifiedAbsoluteTimeoutCWD(request.CWD)
	if request.CWDKnown {
		var overflow bool
		request.CWDCandidates, overflow = verifiedUniqueDirectoriesBounded(append(request.CWDCandidates, request.CWD))
		request.CWDUnknown = request.CWDUnknown || overflow
		request.CWDPhysical, _ = verifiedPhysicalCWD(request.CWD)
		for _, candidate := range request.CWDCandidates {
			if physical, ok := verifiedPhysicalCWD(candidate); ok {
				request.CWDPhysicalCandidates = append(request.CWDPhysicalCandidates, physical)
			}
		}
	} else if request.SegmentCWDKnown == nil {
		request.CWDUnknown = true
	}
	if request.Provider == ProviderOmp && !request.HookMode {
		if _, verb, candidate := classifyEciLifecycleTargetCandidate(request.Command); candidate &&
			(isLifecycleReadOnlyVerb(verb) || request.Marker == MarkerActive && request.Role == RoleCoordinator) {
			return Result{Decision: DecisionDefer, DeferredRoute: DeferredRouteEciLifecycle}
		}
		if request.Marker == MarkerActive {
			if word, verb, dispatcher := classifyEciLifecycleDispatcherInvocation(request.Command); dispatcher &&
				verb != "--help" && verb != "-h" {
				return deniedResult(request, eciLifecycleDispatcherDiagnostic(request.Command, word))
			}
		}
	}
	if !request.HookMode && request.Provider == ProviderOmp && request.Marker == MarkerActive && request.Role == RoleCoordinator {
		if trace, ok := reviewedScriptTraceTopology(request.Command); ok {
			return Result{
				Decision:      DecisionDefer,
				DeferredRoute: DeferredRouteReviewedScriptTrace,
				Plan:          &PlanTopology{Trace: trace},
			}
		}
	}

	parsed, err := parseShellPlan(request.Command, request.CollectShellCommands)
	if err != nil && parsed.projection != nil && len(parsed.segments) > 0 {
		// Retain completed source-grounded commands from a partial projection.
		// Parser uncertainty is advisory, not proof of another executable.
		parsed.projectionIncomplete = true
		err = nil
	}
	if err != nil {
		if err.diagnostic.Code == CodePlanLimitDenied {
			// Parser capacity alone establishes no effect. Retain the planner's
			// advisory result; Go callback checks use any established typed facts.
			if request.Marker == MarkerActive {
				return Result{Decision: DecisionDefer}
			}
			return Result{Decision: DecisionAllow}
		}
		if err.diagnostic.Code == CodePlanSyntaxDenied {
			// A parser's incomplete shell grammar is not a concrete target or
			// accidental-damage finding. Leave ordinary shell evaluation to the
			// caller instead of turning punctuation or expansion into a gate.
			return Result{Decision: DecisionAllow}
		}
		if request.Marker == MarkerInactive && strings.HasPrefix(string(err.diagnostic.Code), "ECI_PLAN_") {
			// Inactive callbacks remain transparent to shell syntax, while
			// deferring dynamic expansion lets the provider adapter retain
			// ownership of protected children such as Git mutations.
			if err.diagnostic.Predicate == "dynamic-expansion" {
				return Result{Decision: DecisionDefer}
			}
			return Result{Decision: DecisionAllow}
		}
		err.diagnostic.RejectedSegment = shellEscape(request.Command)
		return deniedResult(request, err.diagnostic)
	}

	wholeSingleSegmentPlan := len(parsed.segments) == 1 && len(parsed.operators) == 0
	segmentStates := compoundSegmentStates(request, parsed)
	shellAnalysis, shellDenial := inspectShellAnalysis(request, parsed, segmentStates)
	if shellDenial != nil {
		return *shellDenial
	}
	var timeoutLaunches []TimeoutLaunch
	var timeoutReplays []TimeoutReplay
	decision := DecisionAllow
	ledgerRedirectAppend := false
	for index, current := range parsed.segments {
		segmentRequest := request
		segmentState := segmentStates[index]
		segmentCWD := segmentState.cwd
		if segmentCWD.known {
			segmentRequest.CWD = segmentCWD.cwd
			segmentRequest.CWDPhysical = segmentCWD.physical
			segmentRequest.CWDKnown = true
		} else {
			// An unknown segment directory must not inherit the callback CWD.
			// Relative target checks defer to the provider adapter, while
			// absolute target checks remain available to the planner.
			segmentRequest.CWD = ""
			segmentRequest.CWDKnown = false
		}
		segmentRequest.CWDUnknown = segmentCWD.unknown
		segmentRequest.CWDCandidates = append([]string(nil), segmentCWD.candidates...)
		segmentRequest.CWDPhysicalCandidates = append([]string(nil), segmentCWD.physicalCandidates...)
		if !request.HookMode && request.Marker == MarkerActive && segmentState.reachability != segmentUnreachable &&
			segmentCWD.known && !segmentCWD.unknown &&
			len(segmentCWD.candidates) == 1 && segmentState.replayKnown {
			// Observe this segment only after all earlier segments have had a
			// chance to return their concrete diagnostic.
			replay, recorded := timeoutReplayForSegment(request, current, index+1, segmentState.replayState)
			if recorded {
				timeoutReplays = append(timeoutReplays, replay)
				if replay.Disposition == TimeoutReplayObserved {
					// Preserve the callback request as the outer scope anchor while
					// resolving this observed child from timeout's effective CWD.
					segmentRequest.CWD = replay.CWD
					segmentRequest.CWDPhysical = replay.CWDPhysical
					segmentRequest.CWDKnown = true
					segmentRequest.CWDCandidates = []string{replay.CWD}
					timeoutLaunches = append(timeoutLaunches, TimeoutLaunch{
						Segment: index + 1,
						Prefix:  append([]string(nil), replay.Prefix...),
					})
				}
			}
		}
		if segmentState.reachability == segmentUnreachable {
			continue
		}
		inspectionSegment, inspect := effectSegmentAfterKeywords(current)
		if !inspect {
			continue
		}
		segmentDecision, diagnostic := inspectSegment(
			segmentRequest,
			inspectionSegment,
			index+1,
			wholeSingleSegmentPlan,
			timeoutLaunches,
			&ledgerRedirectAppend,
		)
		if diagnostic != nil {
			diagnostic.RejectedSegment = rejectedSegment(current, diagnostic.Code)
			if suppressInactiveDiagnostic(request, diagnostic) {
				// Inactive syntax diagnostics can hide a protected child
				// capability behind shell context (assignments, wrappers,
				// or interpreter payloads). Defer the complete literal to
				// the provider adapter; ordinary inactive commands remain
				// admitted when that adapter finds no protected operation.
				decision = DecisionDefer
				continue
			}
			result := withCompoundPlan(deniedResult(request, *diagnostic), parsed, segmentStates)
			result.TimeoutLaunches = timeoutLaunches
			result.TimeoutReplays = timeoutReplays
			return result
		}
		if segmentDecision == DecisionDefer {
			decision = DecisionDefer
		}
	}
	if request.Marker == MarkerActive && request.Role == RoleCoordinator &&
		compoundPlanContainsFiniteShellScriptInvocation(parsed) {
		// A compound shell-script invocation needs the provider's reviewed-script
		// adapter to validate the complete raw topology before it can execute.
		decision = DecisionDefer
	}
	gitCloneLaunch := gitCloneSourceAcquisitionLaunchForPlan(request, parsed, decision, timeoutLaunches)
	capabilities := capabilitiesForPlan(request, parsed, decision, gitCloneLaunch, timeoutLaunches)
	topology := compoundPlanTopology(parsed, segmentStates)
	deferredRoute := deferredRouteForPlan(request, parsed, decision, capabilities)
	if shellAnalysis != nil {
		// Provider effect readers consume typed argv/context records. The raw
		// lossless compound topology retains Git routing hints independently;
		// compoundPlanTopology already excludes projected multiline text.
		decision = DecisionDefer
		capabilities = nil
		gitCloneLaunch = nil
		deferredRoute = ""
	}
	return Result{
		Decision:             decision,
		Capabilities:         capabilities,
		GitCloneLaunch:       gitCloneLaunch,
		DeferredRoute:        deferredRoute,
		LedgerRedirectAppend: ledgerRedirectAppend,
		TimeoutLaunches:      timeoutLaunches,
		TimeoutReplays:       timeoutReplays,
		Plan:                 topology,
		ShellAnalysis:        shellAnalysis,
	}
}

// deferredRouteForPlan selects a provider route only after the complete plan
// has been classified. The route intentionally inspects one raw env argv and
// the child selected by the existing env grammar; it does not inherit any
// executable basename, generic wrapper, or Git option interpretation.
func deferredRouteForPlan(request Request, parsed plan, decision DecisionKind, capabilities []Capability) DeferredRoute {
	if request.Provider != ProviderOmp || request.Marker != MarkerActive {
		return ""
	}
	if decision != DecisionDefer || len(capabilities) != 0 {
		return ""
	}
	if request.Role == RoleCoordinator && compoundPlanContainsFiniteShellScriptInvocation(parsed) {
		return DeferredRouteReviewedScriptCompound
	}
	if request.Role != RoleWorker {
		return ""
	}
	if len(parsed.segments) != 1 || len(parsed.operators) != 0 {
		return ""
	}

	return ""
}

// isWorkerEnvGitFsckLostFound recognizes the one direct env child grammar that
// the worker Git fsck gate exempts from denial. It deliberately uses the
// env-specific unwrapping rules and direct child positions; generic wrapper and
// Git-option handling remain outside this predicate.
func isWorkerEnvGitFsckLostFound(argv []token) bool {
	if len(argv) == 0 || argv[0].value != "env" {
		return false
	}
	child, diagnostic := unwrapEnv(argv, 1)
	if diagnostic != nil || len(child) < 3 || child[0].value != "git" || child[1].value != "fsck" {
		return false
	}
	// The transparent worker route is only the direct `env` launch. Git
	// global/context options remain on the ordinary ownership route so the
	// concrete fsck writer cannot be hidden by a changed repository context.
	if gitSubcommandIndex(child) != 1 || hasAttachedGitContextOption(child) {
		return false
	}
	_, ok := gitFsckLostFoundOption(child, 1)
	return ok
}

// capabilitiesForPlan returns the one protected capability that the planner
// can fast-admit after every segment and envelope predicate has succeeded.
//
// Example: the exact current-provider gate-mode binary followed by `set`
// returns CapabilityGateMode; a direct Git clone returns
// CapabilityGitCloneSourceAcquisition; a bare command name returns no capability.
func capabilitiesForPlan(
	request Request,
	parsed plan,
	decision DecisionKind,
	gitCloneLaunch *GitCloneLaunch,
	timeoutLaunches []TimeoutLaunch,
) []Capability {
	if request.Marker != MarkerActive || decision != DecisionAllow ||
		len(parsed.segments) != 1 || len(parsed.operators) != 0 {
		return nil
	}
	if gitCloneLaunch != nil {
		return []Capability{CapabilityGitCloneSourceAcquisition}
	}
	original := parsed.segments[0].argv
	unwrapped, diagnostic := unwrapWithMetadata(original, 1, timeoutLaunches)
	if diagnostic != nil {
		return nil
	}
	if !isExactGateModeEnvelope(request, original, unwrapped.argv, true) ||
		len(unwrapped.argv) < 2 || unwrapped.argv[1].value != "set" {
		return nil
	}
	return []Capability{CapabilityGateMode}
}

// gitCloneSourceAcquisitionLaunchForPlan returns launch metadata only for one
// active, fully allowed direct plan. Compound topology remains planner-owned:
// its adapter recursively classifies each segment rather than carrying this
// top-level capability across shell operators.
func gitCloneSourceAcquisitionLaunchForPlan(
	request Request,
	parsed plan,
	decision DecisionKind,
	timeoutLaunches []TimeoutLaunch,
) *GitCloneLaunch {
	if request.Marker != MarkerActive || decision != DecisionAllow ||
		len(parsed.segments) != 1 || len(parsed.operators) != 0 {
		return nil
	}
	original := parsed.segments[0].argv
	unwrapped, diagnostic := unwrapWithMetadata(original, 1, timeoutLaunches)
	if diagnostic != nil {
		return nil
	}
	launch, _ := gitCloneSourceAcquisitionLaunch(original, unwrapped, true)
	return launch
}

// gitCloneSourceAcquisitionLaunch recognizes the finite launch forms whose
// executable and inherited environment can be bound by the provider without
// interpreting Git clone options or operands. Its boolean reports a visible
// Git clone whose launcher changed that boundary and therefore must not fall
// through to generic active admission.
func gitCloneSourceAcquisitionLaunch(
	original []token,
	unwrapped unwrappedCommand,
	wholeSingleSegmentPlan bool,
) (*GitCloneLaunch, bool) {
	if !wholeSingleSegmentPlan {
		return nil, false
	}
	if !isGitCloneSourceAcquisitionArgv(unwrapped.argv) {
		return nil, isGitCloneLaunchContextCandidate(unwrapped.argv) || isCommandQueryGitCloneLaunch(original)
	}
	if launch := directGitCloneLaunch(original, unwrapped); launch != nil {
		return launch, true
	}
	if launch := commandGitCloneLaunch(original); launch != nil {
		return launch, true
	}
	if launch := envGitCloneLaunch(original); launch != nil {
		return launch, true
	}
	return nil, true
}

func isGitCloneSourceAcquisitionArgv(argv []token) bool {
	return len(argv) >= 2 && filepath.Base(argv[0].value) == "git" && argv[1].value == "clone"
}

// isGitCloneLaunchContextCandidate recognizes a Git clone whose global
// context options alter where Git resolves the operation or how it starts.
// Those options are a concrete target/execution-context change, not a
// punctuation or spelling boundary: the provider must inspect them before
// admitting the clone.  Plain `git clone` remains the typed source-acquisition
// capability above.
func isGitCloneLaunchContextCandidate(argv []token) bool {
	if len(argv) < 3 || filepath.Base(argv[0].value) != "git" {
		return false
	}
	for index := 1; index < len(argv); index++ {
		value := argv[index].value
		if value == "clone" {
			return index > 1
		}
		switch {
		// TODO: this repeats Git's global-option lists in isGitSplitContextOption and
		// isGitContextOption; reuse them so each option is listed once.
		case value == "-C" || value == "-c" || value == "--config-env" ||
			value == "--git-dir" || value == "--work-tree" || value == "--exec-path" ||
			value == "--namespace" || value == "--super-prefix":
			if index+1 >= len(argv) {
				return false
			}
			index++
		case strings.HasPrefix(value, "-C") && len(value) > 2,
			strings.HasPrefix(value, "--config-env="),
			strings.HasPrefix(value, "--git-dir="),
			strings.HasPrefix(value, "--work-tree="),
			strings.HasPrefix(value, "--exec-path="),
			strings.HasPrefix(value, "--namespace="),
			strings.HasPrefix(value, "--super-prefix="):
			// Attached context values are already consumed by this token.
		case value == "--":
			return false
		}
	}
	return false
}

// isCommandQueryGitCloneLaunch recognizes command's query-only forms when
// they visibly name a Git clone. They do not launch Git, so they cannot carry
// the source-acquisition capability or fall through to generic active allow.
func isCommandQueryGitCloneLaunch(original []token) bool {
	return len(original) >= 4 && original[0].value == "command" &&
		(original[1].value == "-v" || original[1].value == "-V") &&
		isGitCloneSourceAcquisitionArgv(original[2:])
}

func directGitCloneLaunch(original []token, unwrapped unwrappedCommand) *GitCloneLaunch {
	if unwrapped.hasEnvironment || unwrapped.hasTransparentWrapper ||
		len(original) != len(unwrapped.argv) || !isGitCloneSourceAcquisitionArgv(original) {
		return nil
	}
	return &GitCloneLaunch{
		Class:                GitCloneLaunchDirect,
		GitArgvIndex:         0,
		GitExecutable:        original[0].value,
		EnvironmentPreserved: true,
	}
}

func commandGitCloneLaunch(original []token) *GitCloneLaunch {
	if len(original) < 3 || original[0].value != "command" {
		return nil
	}
	gitIndex := 1
	if original[gitIndex].value == "--" {
		gitIndex++
	}
	if !isGitCloneSourceAcquisitionArgv(original[gitIndex:]) {
		return nil
	}
	return &GitCloneLaunch{
		Class:                GitCloneLaunchCommand,
		GitArgvIndex:         gitIndex,
		GitExecutable:        original[gitIndex].value,
		EnvironmentPreserved: true,
	}
}

func envGitCloneLaunch(original []token) *GitCloneLaunch {
	if len(original) < 3 || filepath.Base(original[0].value) != "env" {
		return nil
	}
	gitIndex := 1
	if original[gitIndex].value == "--" {
		gitIndex++
	}
	if !isGitCloneSourceAcquisitionArgv(original[gitIndex:]) {
		return nil
	}
	envIndex := 0
	return &GitCloneLaunch{
		Class:                GitCloneLaunchEnv,
		GitArgvIndex:         gitIndex,
		GitExecutable:        original[gitIndex].value,
		EnvironmentPreserved: true,
		EnvArgvIndex:         &envIndex,
		EnvExecutable:        original[envIndex].value,
	}
}

func gitCloneLaunchContextDiagnostic(original []token, segmentIndex int) *Diagnostic {
	return diagnosticForToken(
		CodeGitExecutionContextDenied,
		"Git clone launch changes the executable or inherited environment context",
		segmentIndex,
		0,
		original[0],
		"use direct Git clone, command [--] Git clone, or env [--] Git clone without launcher options or environment changes",
		"git-clone-launch-context",
	)
}

func rejectedSegment(current segment, code DiagnosticCode) string {
	if code == CodeEnvironmentContextDenied {
		return "<environment-context command; assignment value omitted>"
	}
	arguments := make([]string, 0, len(current.argv))
	for _, argument := range current.argv {
		arguments = append(arguments, shellEscape(argument.value))
	}
	return strings.Join(arguments, " ")
}

func canonicalLifecycleTildePrefix(command string, offset int) bool {
	for _, alias := range []string{"~/.omp/agent/eci/bin/eci-active", "~/.kimi-code/bin/eci-active"} {
		if !strings.HasPrefix(command[offset:], alias) {
			continue
		}
		end := offset + len(alias)
		return end == len(command) || strings.ContainsRune(" \t;|&<>", rune(command[end]))
	}
	return false
}

// knownHomeExpansion decodes only the shell's unambiguous HOME shorthand.
// This is not a general expansion evaluator: arbitrary variables, command
// substitutions, arithmetic, and globs remain unknown to the planner. HOME
// is already the callback's concrete home authority and must not turn a
// normal "$HOME/.omp/agent/eci/..." lifecycle or ordinary-path command into a raw
// syntax denial before its target-aware route can inspect it.
//
// Example: `$HOME/.omp/agent/eci/bin/eci-active` becomes the current HOME-prefixed
// path, while `$HOME_SUFFIX` and `$(date)` remain dynamic.
func knownHomeExpansion(command string, offset int) (string, int, bool) {
	if offset < 0 || offset >= len(command) || command[offset] != '$' {
		return "", 0, false
	}
	home := os.Getenv("HOME")
	if home == "" || !filepath.IsAbs(home) {
		return "", 0, false
	}
	if strings.HasPrefix(command[offset:], "${HOME}") {
		return home, len("${HOME}"), true
	}
	if !strings.HasPrefix(command[offset:], "$HOME") {
		return "", 0, false
	}
	end := offset + len("$HOME")
	if end < len(command) {
		next := command[end]
		if next == '_' || next >= 'a' && next <= 'z' || next >= 'A' && next <= 'Z' || next >= '0' && next <= '9' {
			return "", 0, false
		}
	}
	return home, len("$HOME"), true
}

func parsePlan(command string) (plan, *planError) {
	return parseShellPlan(command, false)
}

// parseShellPlan preserves the bounded plan parser while separating heredoc
// data and, for recursive children, executable substitution expansions.
//
// Example: a heredoc child containing another $(...) retains both commands.
func parseShellPlan(
	command string,
	substitutions bool,
) (plan, *planError) {
	if !utf8.ValidString(command) {
		return plan{}, newPlanError(
			CodePlanSyntaxDenied,
			"command is not valid UTF-8",
			0,
			1,
			0,
			"<command>",
			"replace invalid bytes with one literal UTF-8 argv plan",
			"invalid-utf8",
		)
	}
	if !substitutions && len(command) > maxCommandBytes {
		return plan{}, newPlanError(
			CodePlanLimitDenied,
			fmt.Sprintf("command is %d bytes; maximum is %d", len(command), maxCommandBytes),
			maxCommandBytes,
			1,
			0,
			"<command>",
			"continue through the ordinary target-aware fallback",
			"command-byte-limit",
		)
	}
	if index := strings.IndexByte(command, '\x00'); index >= 0 {
		return plan{}, newPlanError(
			CodePlanSyntaxDenied,
			"NUL bytes are not command-plan syntax",
			index,
			1,
			0,
			command[index:index+1],
			"remove the NUL byte and retry one literal plan",
			"forbidden-byte",
		)
	}

	var parsed plan
	if substitutions || strings.Contains(command, "<<") {
		projection, projectionErr := projectShellCommands(command, substitutions)
		if projection.Changed {
			parsed.projection = &projection
			parsed.projectionIncomplete = projectionErr != nil
			command = projection.Command
		}
	}
	var current []token
	var redirects []outputRedirect
	var inputRedirects []token
	var value strings.Builder
	var sourceOffsets []int
	segmentStart := 0
	tokenOffset := 0
	tokenStarted := false
	tokenQuoted := false
	var redirectTargetPending *outputRedirect
	inputRedirectTargetPending := false
	var groupStack []compoundGroupKind
	var segmentGroupOpens []compoundGroupKind
	var segmentGroupCloses []compoundGroupKind
	segmentGroupSyntaxSafe := true
	groupClosePending := false
	quote := byte(0)
	escaped := false

	flushToken := func() *planError {
		if !tokenStarted {
			return nil
		}
		argument := value.String()
		if !substitutions && parsed.projection == nil && len(argument) > maxArgumentBytes {
			return newPlanError(
				CodePlanLimitDenied,
				fmt.Sprintf("argv element is larger than %d bytes", maxArgumentBytes),
				tokenOffset,
				len(parsed.segments)+1,
				len(current),
				argument,
				"continue through the ordinary target-aware fallback",
				"argv-byte-limit",
			)
		}
		parsedToken := token{value: argument, offset: tokenOffset, quoted: tokenQuoted, sourceOffsets: sourceOffsets}
		if redirectTargetPending != nil {
			redirect := *redirectTargetPending
			redirect.target = parsedToken
			if !redirect.descriptorDuplication || !isFileDescriptorDuplicationTarget(parsedToken.value) {
				redirects = append(redirects, redirect)
			}
			redirectTargetPending = nil
		} else if inputRedirectTargetPending {
			inputRedirects = append(inputRedirects, parsedToken)
		} else {
			current = append(current, parsedToken)
		}
		inputRedirectTargetPending = false
		value.Reset()
		sourceOffsets = nil
		tokenStarted = false
		tokenQuoted = false
		return nil
	}

	flushSegment := func(operator string, offset int) *planError {
		if err := flushToken(); err != nil {
			return err
		}
		if len(current) == 0 {
			if (substitutions || parsed.projection != nil) && operator == "\n" {
				// Blank lines do not end projected analysis before a later
				// concrete command. Raw source remains bounded by command bytes.
				segmentStart = offset + len(operator)
				return nil
			}
			if groupClosePending && len(parsed.segments) > 0 {
				segmentStart = offset + len(operator)
				groupClosePending = false
				return nil
			}
			return newPlanError(
				CodePlanSyntaxDenied,
				"a command-plan operator has an empty adjacent segment",
				offset,
				len(parsed.segments)+1,
				0,
				operator,
				"supply one nonempty literal argv on each side of the operator",
				"empty-segment",
			)
		}
		if !substitutions && parsed.projection == nil && len(parsed.segments) >= maxSegments {
			return newPlanError(
				CodePlanLimitDenied,
				fmt.Sprintf("command plan exceeds %d segments", maxSegments),
				offset,
				len(parsed.segments)+1,
				0,
				operator,
				"continue through the ordinary target-aware fallback",
				"segment-limit",
			)
		}
		copied := append([]token(nil), current...)
		parsed.segments = append(parsed.segments, segment{
			argv:            copied,
			redirects:       append([]outputRedirect(nil), redirects...),
			inputRedirects:  append([]token(nil), inputRedirects...),
			offset:          copied[0].offset,
			sourceStart:     segmentStart,
			command:         command[segmentStart:offset],
			groupOpens:      append([]compoundGroupKind(nil), segmentGroupOpens...),
			groupCloses:     append([]compoundGroupKind(nil), segmentGroupCloses...),
			groupSyntaxSafe: segmentGroupSyntaxSafe && len(groupStack) >= len(segmentGroupOpens),
		})
		parsed.operators = append(parsed.operators, operator)
		current = current[:0]
		redirects = redirects[:0]
		inputRedirects = inputRedirects[:0]
		segmentGroupOpens = segmentGroupOpens[:0]
		segmentGroupCloses = segmentGroupCloses[:0]
		segmentGroupSyntaxSafe = true
		redirectTargetPending = nil
		inputRedirectTargetPending = false
		segmentStart = offset + len(operator)
		return nil
	}

	for index := 0; index < len(command); index++ {
		character := command[index]
		if escaped {
			value.WriteByte(character)
			sourceOffsets = append(sourceOffsets, index)
			tokenStarted = true
			tokenQuoted = true
			escaped = false
			continue
		}
		if quote == '\'' {
			if character == '\'' {
				quote = 0
				continue
			}
			value.WriteByte(character)
			sourceOffsets = append(sourceOffsets, index)
			tokenStarted = true
			tokenQuoted = true
			continue
		}
		if character == '\\' {
			if !tokenStarted {
				tokenOffset = index
			}
			tokenStarted = true
			tokenQuoted = true
			escaped = true
			continue
		}
		if character == '\'' && quote == 0 {
			if !tokenStarted {
				tokenOffset = index
			}
			tokenStarted = true
			tokenQuoted = true
			quote = '\''
			continue
		}
		if character == '"' {
			if !tokenStarted {
				tokenOffset = index
			}
			tokenStarted = true
			tokenQuoted = true
			if quote == '"' {
				quote = 0
			} else {
				quote = '"'
			}
			continue
		}

		inDoubleQuote := quote == '"'
		if !inDoubleQuote && (character == ' ' || character == '\t') {
			if err := flushToken(); err != nil {
				return parsed, err
			}
			continue
		}
		if !inDoubleQuote {
			if character == '&' && index+1 < len(command) && command[index+1] == '>' {
				if err := flushToken(); err != nil {
					return parsed, err
				}
				inputRedirectTargetPending = false
				effect := outputRedirectOverwrite
				next := index + 2
				if next < len(command) && command[next] == '>' {
					effect = outputRedirectAppend
					next++
				}
				redirectTargetPending = &outputRedirect{effect: effect}
				index = next - 1
				continue
			}
			// Shell spelling is not itself an accidental-mistake finding. Keep
			// enough word boundaries to inspect visible concrete targets below,
			// but do not turn ordinary punctuation into a denial.
			operator := ""
			switch {
			case index+1 < len(command) && command[index:index+2] == "&&":
				operator = "&&"
			case index+1 < len(command) && command[index:index+2] == "||":
				operator = "||"
			case character == ';', character == '|', character == '\n', character == '&':
				operator = string(character)
			}
			if operator != "" {
				if err := flushSegment(operator, index); err != nil {
					return parsed, err
				}
				index += len(operator) - 1
				continue
			}
			if character == '<' || character == '>' {
				// A redirection is ordinary shell syntax. Treat it as a word
				// boundary so a preceding visible `rm -rf /` remains detectable.
				fileDescriptor := tokenStarted && !tokenQuoted &&
					tokenOffset+value.Len() == index && isDecimalFileDescriptor(value.String())
				if fileDescriptor {
					value.Reset()
					sourceOffsets = nil
					tokenStarted = false
					tokenQuoted = false
				} else if err := flushToken(); err != nil {
					return parsed, err
				}
				inputRedirectTargetPending = false
				if character == '<' {
					if index+1 < len(command) && (command[index+1] == '<' || command[index+1] == '&') {
						index++
					} else if index+1 >= len(command) || command[index+1] != '(' {
						inputRedirectTargetPending = true
					}
					continue
				}

				effect := outputRedirectOverwrite
				next := index + 1
				if next < len(command) && command[next] == '>' {
					effect = outputRedirectAppend
					next++
				}
				if next < len(command) && command[next] == '|' {
					effect = outputRedirectForceOverwrite
					next++
				}
				descriptorDuplication := false
				if next < len(command) && command[next] == '&' {
					descriptorDuplication = true
					next++
				}
				redirectTargetPending = &outputRedirect{
					effect:                effect,
					descriptorDuplication: descriptorDuplication,
				}
				index = next - 1
				continue
			}
			if character == '(' || character == ')' {
				// Grouping and process substitution do not authorize or deny an
				// action. Their word boundary lets visible nested direct targets
				// remain available to the ordinary target checks.
				if err := flushToken(); err != nil {
					return parsed, err
				}
				if character == '(' {
					if inputRedirectTargetPending || len(current) != 0 {
						segmentGroupSyntaxSafe = false
					} else {
						segmentGroupOpens = append(segmentGroupOpens, compoundGroupParenthesized)
						groupStack = append(groupStack, compoundGroupParenthesized)
					}
				} else {
					if len(groupStack) == 0 || groupStack[len(groupStack)-1] != compoundGroupParenthesized {
						segmentGroupSyntaxSafe = false
					} else {
						groupStack = groupStack[:len(groupStack)-1]
						if len(current) == 0 && len(parsed.segments) > 0 {
							last := len(parsed.segments) - 1
							parsed.segments[last].groupCloses = append(parsed.segments[last].groupCloses, compoundGroupParenthesized)
							groupClosePending = true
						} else {
							segmentGroupCloses = append(segmentGroupCloses, compoundGroupParenthesized)
						}
					}
				}
				inputRedirectTargetPending = false
				continue
			}
			if (character == '{' || character == '}') && !tokenStarted {
				// Shell groups use bare braces, while parameter expansion has a
				// started '$' word. Split only the former so `{ rm -rf /; }`
				// retains its visible target without interpreting `${name}`.
				if err := flushToken(); err != nil {
					return parsed, err
				}
				if character == '{' {
					if inputRedirectTargetPending || len(current) != 0 {
						segmentGroupSyntaxSafe = false
					} else {
						segmentGroupOpens = append(segmentGroupOpens, compoundGroupBrace)
						groupStack = append(groupStack, compoundGroupBrace)
					}
				} else {
					if len(groupStack) == 0 || groupStack[len(groupStack)-1] != compoundGroupBrace {
						segmentGroupSyntaxSafe = false
					} else {
						groupStack = groupStack[:len(groupStack)-1]
						if len(current) == 0 && len(parsed.segments) > 0 {
							last := len(parsed.segments) - 1
							parsed.segments[last].groupCloses = append(parsed.segments[last].groupCloses, compoundGroupBrace)
							groupClosePending = true
						} else {
							segmentGroupCloses = append(segmentGroupCloses, compoundGroupBrace)
						}
					}
				}
				inputRedirectTargetPending = false
				continue
			}
		}
		if character == '$' && quote != '\'' {
			if home, consumed, ok := knownHomeExpansion(command, index); ok {
				if !tokenStarted {
					tokenOffset = index
				}
				value.WriteString(home)
				for range []byte(home) {
					sourceOffsets = append(sourceOffsets, index)
				}
				tokenStarted = true
				index += consumed - 1
				continue
			}
		}
		if !tokenStarted {
			tokenOffset = index
		}
		value.WriteByte(character)
		sourceOffsets = append(sourceOffsets, index)
		tokenStarted = true
	}

	if escaped {
		value.WriteByte('\\')
		sourceOffsets = append(sourceOffsets, len(command)-1)
		tokenStarted = true
		tokenQuoted = true
	}
	if err := flushToken(); err != nil {
		return parsed, err
	}
	if len(current) == 0 {
		if len(parsed.segments) > 0 {
			// A trailing shell operator is not a target. Retain the completed
			// segments for their ordinary target checks rather than denying the
			// command for punctuation alone.
			return parsed, nil
		}
		tokenValue := "<empty>"
		if len(parsed.operators) > 0 {
			tokenValue = parsed.operators[len(parsed.operators)-1]
		}
		return parsed, newPlanError(
			CodePlanSyntaxDenied,
			"command plan has no final literal argv",
			len(command),
			len(parsed.segments)+1,
			0,
			tokenValue,
			"supply one nonempty literal argv after the operator",
			"empty-segment",
		)
	}
	parsed.segments = append(parsed.segments, segment{
		argv:            append([]token(nil), current...),
		redirects:       append([]outputRedirect(nil), redirects...),
		inputRedirects:  append([]token(nil), inputRedirects...),
		offset:          current[0].offset,
		sourceStart:     segmentStart,
		command:         command[segmentStart:],
		groupOpens:      append([]compoundGroupKind(nil), segmentGroupOpens...),
		groupCloses:     append([]compoundGroupKind(nil), segmentGroupCloses...),
		groupSyntaxSafe: segmentGroupSyntaxSafe && len(groupStack) == 0,
	})
	argumentCount := 0
	for _, currentSegment := range parsed.segments {
		argumentCount += len(currentSegment.argv)
	}
	if !substitutions && parsed.projection == nil && (len(parsed.segments) > maxSegments || argumentCount > maxArguments) {
		return parsed, newPlanError(
			CodePlanLimitDenied,
			fmt.Sprintf(
				"command plan contains %d segments and %d argv elements; limits are %d and %d",
				len(parsed.segments),
				argumentCount,
				maxSegments,
				maxArguments,
			),
			len(command),
			len(parsed.segments),
			len(parsed.segments[len(parsed.segments)-1].argv)-1,
			"<plan>",
			"continue through the ordinary target-aware fallback",
			"plan-limit",
		)
	}

	return parsed, nil
}

// isDecimalFileDescriptor reports whether value is the unquoted numeric prefix
// of a shell output redirection rather than an argv operand.
//
// Example: `2>> log` has file descriptor prefix 2, while `two>> log` does not.
func isDecimalFileDescriptor(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

// isFileDescriptorDuplicationTarget reports whether a `>&` target denotes an
// existing descriptor or close marker instead of a filesystem path.
//
// Example: 2>&1 and >&- return true, while >& ledger returns false.
func isFileDescriptorDuplicationTarget(value string) bool {
	return value == "-" || isDecimalFileDescriptor(value)
}

// compoundGroupKind identifies a bounded shell group whose directory state is
// either restored or retained when the group closes.
//
// Example: `(cd /tmp; printf ok)` uses a parenthesized group while `{ cd /tmp;
// printf ok; }` uses a brace group.
type compoundGroupKind string

const (
	// compoundGroupParenthesized restores directory state after a subshell group.
	//
	// Example: `(cd /tmp; printf ok); pwd` restores the outer directory for pwd.
	compoundGroupParenthesized compoundGroupKind = "parenthesized"
	// compoundGroupBrace retains directory state after a brace group.
	//
	// Example: `{ cd /tmp; }; pwd` keeps /tmp as pwd's modeled directory.
	compoundGroupBrace compoundGroupKind = "brace"
)

// compoundGroupFrame stores the state needed when a bounded group closes.
//
// Example: a parenthesized frame restores both the current directory and the
// directory stack, while a brace frame only tracks matching delimiters.
type compoundGroupFrame struct {
	kind           compoundGroupKind
	cwd            string
	cwdPhysical    string
	commandPath    string
	pathSet        bool
	pathExported   bool
	cdPath         string
	cdPathSet      bool
	known          bool
	directoryStack []string
}

// timeoutReplayState is the shared bounded shell transition state used by
// compound CWD facts and direct timeout replay.
//
// Example: PATH=/tools; cd /tmp; timeout 5 git status carries both the PATH
// lookup state and /tmp CWD into the timeout record.
type timeoutReplayState struct {
	cwd            string
	cwdPhysical    string
	commandPath    string
	pathSet        bool
	pathExported   bool
	cdPath         string
	cdPathSet      bool
	known          bool
	skipped        bool
	directoryStack []string
	groupStack     []compoundGroupFrame
}

// compoundSegmentCWDState is the planner-owned directory fact attached to one
// parsed compound segment. Unknown is explicit: the Bash adapter must not
// substitute the outer callback CWD when this fact is unavailable.
//
// Example: a failed or conditional `cd` produces `{known:false}` for the
// following segment instead of reusing the callback directory.
type compoundSegmentCWDState struct {
	cwd                string
	physical           string
	known              bool
	unknown            bool
	candidates         []string
	physicalCandidates []string
}

// compoundSegmentState joins the planner-owned CWD fact with the typed
// reachability fact that controls effect inspection and adapter replay.
//
// Example: a dead `if false` body keeps its CWD metadata but is marked
// `unreachable` so its writer is not rechecked by the Bash adapter.
type compoundSegmentState struct {
	cwd          compoundSegmentCWDState
	reachability SegmentReachability
	replayState  timeoutReplayState
	replayKnown  bool
}

// compoundReachableCWDState keeps the bounded set of shell states that may
// reach the next compound segment. Unknown marks CWD uncertainty; untracked
// branches retain possible execution when a branch could not be represented.
//
// Example: a redirected `cd /tmp` carries both the pre-redirect and
// post-redirect directories as reachable candidates.
type compoundReachableCWDState struct {
	states            []timeoutReplayState
	unknown           bool
	untrackedBranches bool
}

// compoundSegmentStates derives bounded CWD and reachability facts for each
// parsed segment. The initial callback directory must be verified, and only
// bounded direct builtins with literal reachable operands can advance it.
// Conditional branches, pipelines, wrappers, redirects, or dynamic spellings
// retain concrete candidates while carrying an explicit unknown fact.
//
// Example: `cd /tmp; printf ok` records `/tmp` for the second segment.
func compoundSegmentStates(
	request Request,
	parsed plan,
) []compoundSegmentState {
	states := make([]compoundSegmentState, len(parsed.segments))
	reachable := initialCompoundReachableCWDState(request)
	conditionalStack := []compoundConditionalCWDFrame{}
	for index, current := range parsed.segments {
		operator := ";"
		if index < len(parsed.operators) {
			operator = parsed.operators[index]
		}
		segmentState := compoundSegmentState{
			cwd:          describeCompoundSegmentCWDState(reachable),
			reachability: compoundReachabilityOf(reachable),
		}
		if !reachable.unknown && len(reachable.states) == 1 && !reachable.states[0].skipped {
			segmentState.replayState = cloneTimeoutReplayState(reachable.states[0])
			segmentState.replayKnown = true
		}
		states[index] = segmentState
		if next, handled := advanceCompoundConditionalState(
			reachable,
			current,
			conditionalStack,
			operator,
		); handled {
			reachable = next.reachable
			conditionalStack = next.stack
			states[index].reachability = next.reachability
			continue
		}
		if index < len(parsed.operators) {
			reachable = advanceCompoundReachableCWDState(reachable, current, operator)
		}
	}
	return states
}

// compoundReachabilityOf reports the typed fact for an ordinary segment whose
// shell operator may have made every concrete state skip the next command.
//
// Example: `false && rm target` marks the rm segment unreachable without
// allowing CWD uncertainty to erase its literal branch cancellation.
func compoundReachabilityOf(reachable compoundReachableCWDState) SegmentReachability {
	if reachable.untrackedBranches || len(reachable.states) == 0 {
		return segmentReachabilityUnknown
	}
	for _, state := range reachable.states {
		if !state.skipped {
			if reachable.unknown {
				return segmentReachabilityUnknown
			}
			return segmentReachable
		}
	}
	return segmentUnreachable
}

// compoundConditionalCWDFrame tracks one bounded if/then/elif/else branch
// join, including which conditions remain reachable.
//
// Example: an `if` frame retains its condition exits until an elif, else, or
// closing fi supplies the reachable join.
type compoundConditionalCWDFrame struct {
	conditionState    compoundReachableCWDState
	exits             compoundReachableCWDState
	activeState       compoundReachableCWDState
	remainingPossible bool
	branchCanRun      bool
	branchStarted     bool
	hasThen           bool
	sawElse           bool
	suppressed        bool
}

// compoundConditionalCWDResult carries the updated branch state and stack.
//
// Example: processing `else cd /tmp` returns a joined state for the following
// `fi` segment.
type compoundConditionalCWDResult struct {
	reachable    compoundReachableCWDState
	stack        []compoundConditionalCWDFrame
	reachability SegmentReachability
}

// advanceCompoundConditionalState handles the supported literal if grammar
// emitted by the bounded parser. Unsupported control flow returns unknown
// rather than preserving a stale branch directory.
//
// Example: `if false; then cd /tmp; else cd /work; fi` joins both exits.
func advanceCompoundConditionalState(
	reachable compoundReachableCWDState,
	current segment,
	stack []compoundConditionalCWDFrame,
	operator string,
) (compoundConditionalCWDResult, bool) {
	trimmed := strings.TrimSpace(current.command)
	keyword, body := conditionalSegmentKeyword(trimmed)
	if keyword == "" {
		if len(stack) == 0 {
			return compoundConditionalCWDResult{}, false
		}
		frameIndex := len(stack) - 1
		updatedStack := append([]compoundConditionalCWDFrame(nil), stack...)
		frame := updatedStack[frameIndex]
		if isConditionalReservedSegment(trimmed) {
			return compoundConditionalCWDResult{
				reachable:    compoundReachableCWDState{unknown: true},
				stack:        updatedStack,
				reachability: segmentReachabilityUnknown,
			}, true
		}
		if !frame.branchStarted {
			return compoundConditionalCWDResult{
				reachable:    compoundReachableCWDState{unknown: true},
				stack:        updatedStack,
				reachability: segmentReachabilityUnknown,
			}, true
		}
		if frame.suppressed || !frame.branchCanRun {
			return compoundConditionalCWDResult{
				reachable:    frame.activeState,
				stack:        updatedStack,
				reachability: segmentUnreachable,
			}, true
		}
		frame.activeState = advanceCompoundReachableCWDState(frame.activeState, current, operator)
		updatedStack[frameIndex] = frame
		return compoundConditionalCWDResult{
			reachable:    frame.activeState,
			stack:        updatedStack,
			reachability: conditionalBranchReachability(frame, frame.activeState),
		}, true
	}
	updatedStack := append([]compoundConditionalCWDFrame(nil), stack...)
	switch keyword {
	case "if":
		if !conditionalParentCanRun(updatedStack) {
			updatedStack = append(updatedStack, compoundConditionalCWDFrame{
				conditionState: reachable,
				activeState:    reachable,
				suppressed:     true,
			})
			return compoundConditionalCWDResult{
				reachable:    reachable,
				stack:        updatedStack,
				reachability: segmentUnreachable,
			}, true
		}
		condition := current
		condition.argv = append([]token(nil), current.argv[1:]...)
		condition.command = body
		condition.groupOpens = nil
		condition.groupCloses = nil
		condition.groupSyntaxSafe = true
		conditionReachable := advanceCompoundReachableCWDState(reachable, condition, operator)
		conditionStatus, conditionKnown := literalSegmentStatus(condition)
		updatedStack = append(updatedStack, compoundConditionalCWDFrame{
			conditionState:    conditionReachable,
			activeState:       conditionReachable,
			remainingPossible: !conditionKnown || !conditionStatus,
			branchCanRun:      !conditionKnown || conditionStatus,
		})
		return compoundConditionalCWDResult{
			reachable:    conditionReachable,
			stack:        updatedStack,
			reachability: compoundReachabilityOf(reachable),
		}, true
	case "then":
		if len(updatedStack) == 0 || updatedStack[len(updatedStack)-1].hasThen || updatedStack[len(updatedStack)-1].sawElse {
			return compoundConditionalCWDResult{
				reachable:    compoundReachableCWDState{unknown: true},
				stack:        updatedStack,
				reachability: segmentReachabilityUnknown,
			}, true
		}
		frameIndex := len(updatedStack) - 1
		frame := updatedStack[frameIndex]
		branchExit := frame.conditionState
		if !frame.suppressed && frame.branchCanRun && body != "" {
			branch := keywordBodySegment(current, body)
			branchExit = advanceCompoundReachableCWDState(branchExit, branch, operator)
		}
		frame.activeState = branchExit
		frame.branchStarted = true
		frame.hasThen = true
		updatedStack[frameIndex] = frame
		reachability := conditionalBranchReachability(frame, branchExit)
		return compoundConditionalCWDResult{
			reachable:    branchExit,
			stack:        updatedStack,
			reachability: reachability,
		}, true
	case "elif":
		if len(updatedStack) == 0 || !updatedStack[len(updatedStack)-1].hasThen || updatedStack[len(updatedStack)-1].sawElse {
			return compoundConditionalCWDResult{
				reachable:    compoundReachableCWDState{unknown: true},
				stack:        updatedStack,
				reachability: segmentReachabilityUnknown,
			}, true
		}
		frameIndex := len(updatedStack) - 1
		frame := updatedStack[frameIndex]
		if frame.branchStarted && frame.branchCanRun && !frame.suppressed {
			frame.exits = joinCompoundReachableCWDStates(frame.exits, frame.activeState)
		}
		condition := keywordBodySegment(current, body)
		conditionReachable := frame.conditionState
		conditionStatus, conditionKnown := literalSegmentStatus(condition)
		canEvaluate := frame.remainingPossible && !frame.suppressed
		if canEvaluate {
			conditionReachable = advanceCompoundReachableCWDState(frame.conditionState, condition, operator)
		}
		frame.branchCanRun = canEvaluate && (!conditionKnown || conditionStatus)
		frame.remainingPossible = canEvaluate && (!conditionKnown || !conditionStatus)
		frame.conditionState = conditionReachable
		frame.activeState = conditionReachable
		frame.branchStarted = false
		frame.hasThen = false
		updatedStack[frameIndex] = frame
		reachability := compoundReachabilityOf(conditionReachable)
		if !canEvaluate {
			reachability = segmentUnreachable
		}
		return compoundConditionalCWDResult{
			reachable:    conditionReachable,
			stack:        updatedStack,
			reachability: reachability,
		}, true
	case "else":
		if len(updatedStack) == 0 || !updatedStack[len(updatedStack)-1].hasThen || updatedStack[len(updatedStack)-1].sawElse {
			return compoundConditionalCWDResult{
				reachable:    compoundReachableCWDState{unknown: true},
				stack:        updatedStack,
				reachability: segmentReachabilityUnknown,
			}, true
		}
		frameIndex := len(updatedStack) - 1
		frame := updatedStack[frameIndex]
		if frame.branchStarted && frame.branchCanRun && !frame.suppressed {
			frame.exits = joinCompoundReachableCWDStates(frame.exits, frame.activeState)
		}
		elseExit := frame.conditionState
		possibleElse := frame.remainingPossible
		canRun := possibleElse && !frame.suppressed
		if canRun && body != "" {
			branch := keywordBodySegment(current, body)
			elseExit = advanceCompoundReachableCWDState(elseExit, branch, operator)
		}
		frame.activeState = elseExit
		frame.branchStarted = true
		frame.branchCanRun = canRun
		frame.hasThen = true
		frame.sawElse = true
		frame.remainingPossible = false
		updatedStack[frameIndex] = frame
		reachability := segmentUnreachable
		if canRun {
			reachability = compoundReachabilityOf(elseExit)
			if possibleElse {
				reachability = segmentReachabilityUnknown
			}
		}
		return compoundConditionalCWDResult{
			reachable:    elseExit,
			stack:        updatedStack,
			reachability: reachability,
		}, true
	case "fi":
		if len(updatedStack) == 0 {
			return compoundConditionalCWDResult{
				reachable:    compoundReachableCWDState{unknown: true},
				stack:        updatedStack,
				reachability: segmentReachabilityUnknown,
			}, true
		}
		frame := updatedStack[len(updatedStack)-1]
		updatedStack = updatedStack[:len(updatedStack)-1]
		if frame.suppressed {
			return compoundConditionalCWDResult{
				reachable:    reachable,
				stack:        updatedStack,
				reachability: segmentReachable,
			}, true
		}
		if frame.branchStarted && frame.branchCanRun {
			frame.exits = joinCompoundReachableCWDStates(frame.exits, frame.activeState)
		}
		result := frame.exits
		if frame.remainingPossible {
			result = joinCompoundReachableCWDStates(result, frame.conditionState)
		}
		if len(updatedStack) > 0 {
			parentIndex := len(updatedStack) - 1
			parent := updatedStack[parentIndex]
			if parent.branchStarted && parent.branchCanRun && !parent.suppressed {
				parent.activeState = result
				updatedStack[parentIndex] = parent
			}
			if parent.branchStarted && (!parent.branchCanRun || parent.suppressed) {
				result = parent.activeState
			}
		}
		return compoundConditionalCWDResult{
			reachable:    result,
			stack:        updatedStack,
			reachability: segmentReachable,
		}, true
	default:
		return compoundConditionalCWDResult{
			reachable:    compoundReachableCWDState{unknown: true},
			stack:        updatedStack,
			reachability: segmentReachabilityUnknown,
		}, true
	}
}

// conditionalParentCanRun reports whether every enclosing conditional body is
// reachable before a nested `if` is interpreted.
//
// Example: a nested conditional under `if false; then` is retained in raw
// topology but its effect-bearing segments remain unreachable.
func conditionalParentCanRun(stack []compoundConditionalCWDFrame) bool {
	for _, frame := range stack {
		if frame.suppressed {
			return false
		}
		if frame.branchStarted && !frame.branchCanRun {
			return false
		}
	}
	return true
}

// conditionalBranchReachability distinguishes a known live branch from one
// that may or may not execute. A possible branch is never treated as dead.
//
// Example: an unknown `if test` marks both branch bodies `unknown`, while
// `if true` marks its body `reachable`.
func conditionalBranchReachability(
	frame compoundConditionalCWDFrame,
	state compoundReachableCWDState,
) SegmentReachability {
	if frame.suppressed || !frame.branchCanRun {
		return segmentUnreachable
	}
	if frame.remainingPossible {
		return segmentReachabilityUnknown
	}
	return compoundReachabilityOf(state)
}

// conditionalSegmentKeyword splits the parser's lossless segment around one
// shell conditional keyword. A segment without one returns an empty keyword.
//
// Example: ` then cd /tmp` returns then and `cd /tmp`.
func conditionalSegmentKeyword(command string) (string, string) {
	word, body := leadingSegmentWord(command)
	if !conditionalKeyword(word) {
		return "", ""
	}
	return word, body
}

// conditionalKeyword reports whether word belongs to the if grammar that CWD
// tracking follows.
//
// Example: then is a conditional keyword, while do is not.
func conditionalKeyword(word string) bool {
	switch word {
	case "if", "then", "elif", "else", "fi":
		return true
	default:
		return false
	}
}

// commandPrefixKeyword reports whether word is a loop keyword or pipeline
// negation after which the shell still starts the segment's command. Only
// effect inspection removes these words; CWD tracking follows the if grammar
// alone. for, select, case, and in are excluded because the next word is a
// name or pattern, not a command.
//
// Example: do and ! are command prefixes, while for is not.
func commandPrefixKeyword(word string) bool {
	switch word {
	case "do", "while", "until", "!":
		return true
	default:
		return false
	}
}

// leadingSegmentWord splits a segment into its first word and the trimmed rest.
//
// Example: `while read -r line` returns while and `read -r line`.
func leadingSegmentWord(command string) (string, string) {
	trimmed := strings.TrimSpace(command)
	end := strings.IndexFunc(trimmed, unicode.IsSpace)
	if end < 0 {
		return trimmed, ""
	}
	return trimmed[:end], strings.TrimSpace(trimmed[end:])
}

// effectSegmentAfterKeywords repeatedly removes leading if-grammar keywords,
// loop keywords, and pipeline negation for effect inspection, so `if ! cmd`
// and `do ! cmd` expose cmd, while PlanTopology retains the original segment
// bytes. Condition and loop bodies remain visible; a bare `fi` or keyword has
// no effect.
//
// Example: ` then rm eci_active` and ` do ! rm eci_active` are inspected as `rm eci_active`.
func effectSegmentAfterKeywords(current segment) (segment, bool) {
	// TODO: a keyword or ! right after a ( or { opener, as in `( ! python3 x )`,
	// a { group right after then or do, as in `do { rm -rf /; }`, and a coproc
	// prefix are not removed, so effect checks miss those commands. Handle them
	// in this loop, keeping argv and group bookkeeping aligned.
	effect := current
	for {
		word, body := leadingSegmentWord(effect.command)
		switch {
		case !conditionalKeyword(word) && !commandPrefixKeyword(word):
			return effect, true
		case word == "fi" || body == "":
			return segment{}, false
		}
		effect = keywordBodySegment(effect, body)
	}
}

// isConditionalReservedSegment detects an unsupported control-flow marker
// whose stale CWD fact must not be carried into a later segment.
//
// Example: a `while` segment invalidates the bounded compound CWD state.
func isConditionalReservedSegment(command string) bool {
	words := strings.Fields(command)
	if len(words) == 0 {
		return false
	}
	switch words[0] {
	case "case", "while", "until", "for", "function", "coproc":
		return true
	default:
		return false
	}
}

// keywordBodySegment returns the segment after one leading shell keyword,
// dropping that keyword's argv token and the segment's group syntax.
//
// Example: `then cd /tmp` becomes `cd /tmp`, and `do python3 x` becomes `python3 x`.
func keywordBodySegment(
	current segment,
	body string,
) segment {
	branch := current
	branch.command = body
	branch.argv = append([]token(nil), current.argv[1:]...)
	branch.groupOpens = nil
	branch.groupCloses = nil
	branch.groupSyntaxSafe = true
	return branch
}

// joinCompoundReachableCWDStates joins branch states without losing an
// explicit unknown branch.
//
// Example: joining /tmp and /work produces two CWD candidates.
func joinCompoundReachableCWDStates(
	left compoundReachableCWDState,
	right compoundReachableCWDState,
) compoundReachableCWDState {
	states, overflow := boundedTimeoutReplayStates(append(append([]timeoutReplayState(nil), left.states...), right.states...))
	return compoundReachableCWDState{
		states:  states,
		unknown: left.unknown || right.unknown || overflow,
		untrackedBranches: left.untrackedBranches || right.untrackedBranches || overflow ||
			left.unknown && len(left.states) == 0 || right.unknown && len(right.states) == 0,
	}
}

// initialCompoundReachableCWDState builds the first bounded shell state from
// verified callback metadata and never substitutes an unverified directory.
//
// Example: a compound handoff with two verified candidates starts with both
// candidates while retaining an explicit unknown branch when supplied.
func initialCompoundReachableCWDState(request Request) compoundReachableCWDState {
	base := timeoutReplayState{
		commandPath: request.CommandPath,
		pathSet:     request.CommandPathSet,
		cdPath:      request.CDPath,
		cdPathSet:   request.CDPathSet,
		known:       true,
	}
	if request.CommandPathExported != nil {
		base.pathExported = request.CommandPathSet && *request.CommandPathExported
	} else {
		base.pathExported = request.CommandPathSet
	}
	candidates := append([]string(nil), request.CWDCandidates...)
	if request.CWDKnown && isVerifiedAbsoluteTimeoutCWD(request.CWD) {
		candidates = append(candidates, request.CWD)
	}
	candidates, overflow := verifiedUniqueDirectoriesBounded(candidates)
	if len(candidates) == 0 {
		return compoundReachableCWDState{unknown: true}
	}
	states := make([]timeoutReplayState, 0, len(candidates))
	for _, candidate := range candidates {
		state := base
		state.cwd = candidate
		state.cwdPhysical, _ = verifiedPhysicalCWD(candidate)
		states = append(states, state)
	}
	return compoundReachableCWDState{states: states, unknown: overflow || request.CWDUnknown, untrackedBranches: overflow}
}

// describeCompoundSegmentCWDState converts reachable shell states into the
// adapter-facing exact-or-candidate directory fact.
//
// Example: one reachable directory is exact; two directories are candidates
// and therefore leave CWDKnown false.
func describeCompoundSegmentCWDState(
	reachable compoundReachableCWDState,
) compoundSegmentCWDState {
	candidates := make([]string, 0, len(reachable.states))
	physicalCandidates := make([]string, 0, len(reachable.states))
	for _, state := range reachable.states {
		if state.known && isVerifiedAbsoluteTimeoutCWD(state.cwd) {
			candidates = append(candidates, state.cwd)
			physical, ok := verifiedPhysicalCWD(state.cwd)
			if ok {
				physicalCandidates = append(physicalCandidates, physical)
			}
		}
	}
	candidates, overflow := verifiedUniqueDirectoriesBounded(candidates)
	if len(physicalCandidates) > maxReachableStates {
		physicalCandidates = physicalCandidates[:maxReachableStates]
		overflow = true
	}
	state := compoundSegmentCWDState{
		candidates:         candidates,
		physicalCandidates: physicalCandidates,
	}
	state.unknown = reachable.unknown || overflow
	if !state.unknown && len(candidates) == 1 {
		state.cwd = candidates[0]
		state.physical, _ = verifiedPhysicalCWD(state.cwd)
		state.known = true
	}
	return state
}

// verifiedUniqueDirectories retains only bounded absolute searchable
// directories while preserving their first-seen order.
//
// Example: duplicate CWD candidates collapse before being forwarded to Bash.
func verifiedUniqueDirectories(candidates []string) []string {
	seen := make(map[string]struct{}, len(candidates))
	result := make([]string, 0, minInt(len(candidates), maxReachableStates))
	for _, candidate := range candidates {
		if !isVerifiedAbsoluteTimeoutCWD(candidate) {
			continue
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		if len(result) >= maxReachableStates {
			continue
		}
		result = append(result, candidate)
	}
	return result
}

// verifiedUniqueDirectoriesBounded retains verified directory candidates and
// reports whether the reachable-state cap discarded another distinct value.
//
// Example: 129 verified candidates return the first 128 plus overflow=true.
func verifiedUniqueDirectoriesBounded(candidates []string) ([]string, bool) {
	seen := make(map[string]struct{}, len(candidates))
	result := make([]string, 0, minInt(len(candidates), maxReachableStates))
	overflow := false
	for _, candidate := range candidates {
		if !isVerifiedAbsoluteTimeoutCWD(candidate) {
			continue
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		if len(result) >= maxReachableStates {
			overflow = true
			continue
		}
		result = append(result, candidate)
	}
	return result, overflow
}

// minInt keeps state-cap arithmetic local to the planner's bounded model.
//
// Example: minInt(200, maxReachableStates) limits a candidate buffer without
// changing the caller's reachability decision.
func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

// advanceCompoundReachableCWDState advances each reachable shell state and
// joins bounded pre/post states when a state-changing command has a redirect
// whose success cannot be established statically.
//
// Example: `cd /tmp > log; pwd` checks both the outer directory and /tmp.
func advanceCompoundReachableCWDState(
	reachable compoundReachableCWDState,
	current segment,
	operator string,
) compoundReachableCWDState {
	result := compoundReachableCWDState{
		unknown:           reachable.unknown,
		untrackedBranches: reachable.untrackedBranches || reachable.unknown && len(reachable.states) == 0,
	}
	for _, state := range reachable.states {
		if state.skipped {
			state.skipped = false
			result.states = append(result.states, state)
			continue
		}
		transitioned := current
		if len(current.redirects) > 0 || len(current.inputRedirects) > 0 {
			transitioned.redirects = nil
			transitioned.inputRedirects = nil
		}
		post, ok := advanceCompoundTransitionStateExact(state, transitioned, operator)
		if !ok {
			// An unresolved effect does not erase concrete pre-transition
			// candidates. It only records that another branch/state may exist.
			// Directory uncertainty does not erase a literal exit status.
			result.states = append(result.states, reachableOperatorExits(state, state, current, operator, false)...)
			result.unknown = true
			continue
		}
		preState := state
		if segmentHasRedirect(current) && stateTransitionChangesState(state, post) {
			preState = state
		}
		result.states = append(result.states, reachableOperatorExits(
			preState,
			post,
			current,
			operator,
			segmentHasRedirect(current) && stateTransitionChangesState(state, post),
		)...)
		if operator != ";" && operator != "\n" && operator != "&&" && operator != "||" && operator != "|" && operator != "&" {
			result.unknown = true
		}
		if operator == "|" || operator == "&" {
			result.unknown = true
		}
	}
	var overflow bool
	result.states, overflow = boundedTimeoutReplayStates(result.states)
	result.unknown = result.unknown || overflow
	result.untrackedBranches = result.untrackedBranches || overflow
	if len(result.states) == 0 && !result.unknown {
		result.unknown = true
	}
	return result
}

// segmentHasRedirect reports parser-owned input or output redirection without
// treating its operand as a command argv effect.
//
// Example: `cd /tmp > log` has an ambiguous transition boundary.
func segmentHasRedirect(current segment) bool {
	return len(current.redirects) > 0 || len(current.inputRedirects) > 0
}

// stateTransitionChangesState compares complete bounded shell state, not only
// CWD, so redirect ambiguity also covers PATH, CDPATH, exports, and stacks.
//
// Example: `export PATH=/tools > log` joins the pre/post environment states.
func stateTransitionChangesState(left, right timeoutReplayState) bool {
	return timeoutReplayStateKey(left) != timeoutReplayStateKey(right)
}

// reachableOperatorExits selects states that can reach the next segment for
// the finite shell operators. Unknown statuses retain both pre/post exits;
// only literal-known true/false statuses prune one side.
//
// Example: `false && cd /tmp` leaves the pre-state, while `true && cd /tmp`
// leaves the post-state.
func reachableOperatorExits(
	pre timeoutReplayState,
	post timeoutReplayState,
	current segment,
	operator string,
	redirectAmbiguous bool,
) []timeoutReplayState {
	markSkipped := func(state timeoutReplayState) timeoutReplayState {
		state.skipped = true
		return state
	}
	preAndPost := func() []timeoutReplayState {
		if redirectAmbiguous {
			return []timeoutReplayState{pre, post}
		}
		return []timeoutReplayState{post}
	}
	status, known := literalSegmentStatus(current)
	switch operator {
	case ";", "\n":
		return preAndPost()
	case "&&":
		if !known {
			return append([]timeoutReplayState{markSkipped(pre)}, preAndPost()...)
		}
		if status {
			return preAndPost()
		}
		return []timeoutReplayState{markSkipped(pre)}
	case "||":
		if !known {
			return append([]timeoutReplayState{markSkipped(pre)}, preAndPost()...)
		}
		if status {
			return []timeoutReplayState{markSkipped(pre)}
		}
		return preAndPost()
	case "|", "&":
		// Pipelines and background lists execute their children outside the
		// parent shell. Keep the parent state and mark the fact unknown at
		// the caller; a concrete pre-state remains useful for target checks.
		return []timeoutReplayState{pre}
	default:
		return []timeoutReplayState{pre}
	}
}

// literalSegmentStatus recognizes only shell literals whose exit status is
// fixed without running a command. All other commands retain both branches.
//
// Example: `:` is known true, while `printf ok` has an unknown status.
func literalSegmentStatus(current segment) (bool, bool) {
	_, argv := splitLeadingAssignments(current.argv)
	if len(argv) != 1 || argv[0].quoted {
		return false, false
	}
	switch argv[0].value {
	case "true", ":":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

// deduplicateTimeoutReplayStates removes duplicate reachable states without
// collapsing distinct directory, PATH, or stack facts.
//
// Example: a branch that returns to the same CWD contributes one state.
func deduplicateTimeoutReplayStates(states []timeoutReplayState) []timeoutReplayState {
	seen := make(map[string]struct{}, len(states))
	result := make([]timeoutReplayState, 0, minInt(len(states), maxReachableStates))
	for _, state := range states {
		key := timeoutReplayStateKey(state)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		if len(result) >= maxReachableStates {
			continue
		}
		result = append(result, state)
	}
	return result
}

// boundedTimeoutReplayStates deduplicates complete shell states and reports
// whether the finite reachable-state cap discarded another state.
//
// Example: an expanding branch fan-out keeps 128 states and marks overflow
// so downstream target checks remain conservative without borrowing a CWD.
func boundedTimeoutReplayStates(states []timeoutReplayState) ([]timeoutReplayState, bool) {
	seen := make(map[string]struct{}, len(states))
	result := make([]timeoutReplayState, 0, minInt(len(states), maxReachableStates))
	overflow := false
	for _, state := range states {
		key := timeoutReplayStateKey(state)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		if len(result) >= maxReachableStates {
			overflow = true
			continue
		}
		result = append(result, state)
	}
	return result, overflow
}

// timeoutReplayStateKey includes every bounded shell fact that can affect a
// later transition or replay lookup, including group snapshots.
//
// Example: two states with the same CWD but different CDPATH remain distinct.
func timeoutReplayStateKey(state timeoutReplayState) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%t\x00%t\x00%s\x00%t\x00%t\x00%t\x00%v\x00%v",
		state.cwd,
		state.cwdPhysical,
		state.commandPath,
		state.pathSet,
		state.pathExported,
		state.cdPath,
		state.cdPathSet,
		state.known,
		state.skipped,
		state.directoryStack,
		state.groupStack,
	)
}

// advanceCompoundTransitionState advances one unconditional shell segment
// while sharing directory, directory-stack, and PATH semantics with timeout
// replay. Unsupported or conditional transitions return an explicit unknown
// state instead of reusing a stale callback fact.
//
// Example: `pushd /tmp; printf ok; popd; pwd` records /tmp for the middle
// segments and the original directory for pwd.
func advanceCompoundTransitionState(
	state timeoutReplayState,
	current segment,
	operator string,
) timeoutReplayState {
	if (len(current.redirects) > 0 || len(current.inputRedirects) > 0) && segmentChangesDirectory(current) {
		return timeoutReplayState{}
	}
	next, ok := advanceCompoundTransitionStateExact(state, current, operator)
	if !ok {
		return timeoutReplayState{}
	}
	return next
}

// advanceCompoundTransitionStateExact applies one direct transition after
// redirect ambiguity has been removed by its caller.
//
// Example: `cd /tmp; printf ok` returns the verified /tmp state.
func advanceCompoundTransitionStateExact(
	state timeoutReplayState,
	current segment,
	operator string,
) (timeoutReplayState, bool) {
	if !state.known || (operator != ";" && operator != "\n" && operator != "&&" && operator != "||" && operator != "|" && operator != "&") || !current.groupSyntaxSafe ||
		!compoundTransitionSegmentIsLiteral(current) {
		return timeoutReplayState{}, false
	}
	next := cloneTimeoutReplayState(state)
	for _, kind := range current.groupOpens {
		next.groupStack = append(next.groupStack, compoundGroupFrame{
			kind:           kind,
			cwd:            next.cwd,
			cwdPhysical:    next.cwdPhysical,
			commandPath:    next.commandPath,
			pathSet:        next.pathSet,
			pathExported:   next.pathExported,
			known:          next.known,
			directoryStack: append([]string(nil), next.directoryStack...),
			cdPath:         next.cdPath,
			cdPathSet:      next.cdPathSet,
		})
	}

	var transitionOK bool
	next, transitionOK = applyStateTransition(next, current.argv)
	if !transitionOK {
		return timeoutReplayState{}, false
	}

	for _, kind := range current.groupCloses {
		if len(next.groupStack) == 0 || next.groupStack[len(next.groupStack)-1].kind != kind {
			return timeoutReplayState{}, false
		}
		frame := next.groupStack[len(next.groupStack)-1]
		next.groupStack = next.groupStack[:len(next.groupStack)-1]
		if kind == compoundGroupParenthesized {
			next.cwd = frame.cwd
			next.cwdPhysical = frame.cwdPhysical
			next.commandPath = frame.commandPath
			next.pathSet = frame.pathSet
			next.pathExported = frame.pathExported
			next.cdPath = frame.cdPath
			next.cdPathSet = frame.cdPathSet
			next.known = frame.known
			next.directoryStack = append([]string(nil), frame.directoryStack...)
		}
	}
	return next, true
}

// segmentChangesDirectory reports whether a direct segment has a supported
// directory builtin identity, without treating arbitrary arguments as effects.
//
// Example: `helper cd /tmp` is ordinary, while `cd /tmp` changes directory.
func segmentChangesDirectory(current segment) bool {
	_, argv := splitLeadingAssignments(current.argv)
	_, _, ok := directDirectoryBuiltin(argv)
	return ok
}

// directDirectoryBuiltin recognizes only the finite parent-shell identities
// whose argv is available to the planner. Quoting the command name does not
// change its shell identity; wrappers such as env remain child-only.
//
// Example: `command cd /tmp` returns cd, while `env cd /tmp` returns no
// parent-shell effect.
func directDirectoryBuiltin(argv []token) (string, []token, bool) {
	if len(argv) == 0 || strings.ContainsRune(argv[0].value, filepath.Separator) {
		return "", nil, false
	}
	name := argv[0].value
	if name == "cd" || name == "pushd" || name == "popd" {
		return name, argv, true
	}
	if name != "builtin" && name != "command" {
		return "", nil, false
	}
	index := 1
	if index < len(argv) && argv[index].value == "--" && !argv[index].quoted {
		index++
	}
	if index >= len(argv) || strings.ContainsRune(argv[index].value, filepath.Separator) {
		return "", nil, false
	}
	builtin := argv[index].value
	if builtin != "cd" && builtin != "pushd" && builtin != "popd" {
		return "", nil, false
	}
	return builtin, argv[index:], true
}

// compoundTransitionSegmentIsLiteral removes parser-owned group delimiters
// before checking the remaining state command for dynamic shell expansion.
//
// Example: `{ cd /tmp` is literal transition syntax, while `cd $TARGET` is
// dynamic even though both segments came from one parsed compound plan.
func compoundTransitionSegmentIsLiteral(current segment) bool {
	value := strings.TrimSpace(current.command)
	for _, kind := range current.groupOpens {
		value = strings.TrimSpace(value)
		if value == "" {
			return false
		}
		prefix := "("
		if kind == compoundGroupBrace {
			prefix = "{"
		}
		if !strings.HasPrefix(value, prefix) {
			return false
		}
		value = value[len(prefix):]
	}
	for range current.groupCloses {
		value = strings.TrimSpace(value)
		if value != "" && (strings.HasSuffix(value, ")") || strings.HasSuffix(value, "}")) {
			value = strings.TrimSpace(value[:len(value)-1])
		}
	}
	return timeoutSegmentIsLiteral(value)
}

// cloneTimeoutReplayState copies transition slices before a segment mutates
// them, keeping each planner fact independent of later group transitions.
//
// Example: a nested parenthesized group receives its own directory-stack copy.
func cloneTimeoutReplayState(state timeoutReplayState) timeoutReplayState {
	state.directoryStack = append([]string(nil), state.directoryStack...)
	state.groupStack = append([]compoundGroupFrame(nil), state.groupStack...)
	return state
}

// applyStateTransition applies one direct state-only command after leading
// shell assignments have been normalized. Ordinary commands preserve state;
// recognized directory commands update it, and unsupported directory-changing
// wrappers invalidate it.
//
// Example: `TRACE=1 cd /tmp` and `command cd /tmp` change directory, while
// `env cd /tmp` leaves the parent shell state unchanged.
func applyStateTransition(
	state timeoutReplayState,
	argv []token,
) (timeoutReplayState, bool) {
	assignments, commandArgv := splitLeadingAssignments(argv)
	if len(commandArgv) == 0 {
		for _, assignment := range assignments {
			switch assignmentName(assignment) {
			case "PATH":
				state.commandPath = strings.TrimPrefix(assignment.value, "PATH=")
				state.pathSet = true
			case "CDPATH":
				state.cdPath = strings.TrimPrefix(assignment.value, "CDPATH=")
				state.cdPathSet = true
			}
		}
		return state, true
	}
	if len(commandArgv) == 0 || len(commandArgv[0].value) == 0 {
		return timeoutReplayState{}, false
	}
	name := commandArgv[0].value
	if strings.ContainsRune(name, filepath.Separator) {
		return applyPathBuiltinTransition(state, commandArgv)
	}
	if builtin, builtinArgv, ok := directDirectoryBuiltin(commandArgv); ok {
		if len(assignments) > 0 && builtin != "cd" {
			return timeoutReplayState{}, false
		}
		before := state
		for _, assignment := range assignments {
			switch assignmentName(assignment) {
			case "PATH":
				state.commandPath = strings.TrimPrefix(assignment.value, "PATH=")
				state.pathSet = true
			case "CDPATH":
				state.cdPath = strings.TrimPrefix(assignment.value, "CDPATH=")
				state.cdPathSet = true
			}
		}
		next, ok := applyDirectoryTransition(state, builtin, builtinArgv)
		if !ok {
			return timeoutReplayState{}, false
		}
		// Prefix assignments to a builtin are temporary in the parent shell.
		next.commandPath = before.commandPath
		next.pathSet = before.pathSet
		next.pathExported = before.pathExported
		next.cdPath = before.cdPath
		next.cdPathSet = before.cdPathSet
		return next, true
	}
	if commandMayChangeDirectory(name, commandArgv[1:]) {
		return state, false
	}
	if len(assignments) == 0 {
		return applyPathBuiltinTransition(state, commandArgv)
	}
	return state, true
}

// splitLeadingAssignments separates literal shell assignments from the direct
// executable that follows them.
//
// Example: `PATH=/tools cd /tmp` returns PATH=/tools as an assignment and cd
// as the executable argv.
func splitLeadingAssignments(argv []token) ([]token, []token) {
	index := 0
	for index < len(argv) && assignmentName(argv[index]) != "" {
		index++
	}
	return argv[:index], argv[index:]
}

// commandMayChangeDirectory identifies wrappers or nested directory builtins
// whose CWD effect cannot be bounded from the direct argv.
//
// Example: `command cd /tmp` invalidates the transition rather than claiming
// that /tmp is the next directory.
func commandMayChangeDirectory(name string, _ []token) bool {
	switch name {
	case "exec", "source", ".", "eval":
		return true
	}
	return false
}

// applyDirectoryTransition models the supported literal cd, pushd, and popd
// builtins, including their verified filesystem effects and directory stack.
//
// Example: `cd -P link` records the physical target while `cd -L link` keeps
// the verified logical spelling.
func applyDirectoryTransition(
	state timeoutReplayState,
	name string,
	argv []token,
) (timeoutReplayState, bool) {
	switch name {
	case "cd":
		if len(argv) != 2 && len(argv) != 3 {
			return timeoutReplayState{}, false
		}
		mode := "-L"
		pathIndex := 1
		if len(argv) == 3 {
			if argv[1].quoted || (argv[1].value != "-L" && argv[1].value != "-P") {
				return timeoutReplayState{}, false
			}
			mode = argv[1].value
			pathIndex = 2
		}
		path, ok := resolveLiteralDirectory(state, argv[pathIndex])
		if !ok {
			return timeoutReplayState{}, false
		}
		if mode == "-P" {
			resolved, ok := resolvePhysicalDirectory(path)
			if !ok {
				return timeoutReplayState{}, false
			}
			path = resolved
		} else {
			path = filepath.Clean(path)
		}
		state.cwd = path
		state.known = true
		state.cwdPhysical, ok = verifiedPhysicalCWD(path)
		if !ok {
			return timeoutReplayState{}, false
		}
		return state, true
	case "pushd":
		if len(argv) != 2 {
			return timeoutReplayState{}, false
		}
		path, ok := resolveLiteralDirectory(state, argv[1])
		if !ok {
			return timeoutReplayState{}, false
		}
		path = filepath.Clean(path)
		state.directoryStack = append(state.directoryStack, state.cwd)
		state.cwd = path
		state.known = true
		state.cwdPhysical, ok = verifiedPhysicalCWD(path)
		if !ok {
			return timeoutReplayState{}, false
		}
		return state, true
	case "popd":
		if len(argv) != 1 || len(state.directoryStack) == 0 {
			return timeoutReplayState{}, false
		}
		last := len(state.directoryStack) - 1
		state.cwd = state.directoryStack[last]
		state.directoryStack = state.directoryStack[:last]
		state.known = state.cwd != "" && isVerifiedAbsoluteTimeoutCWD(state.cwd)
		if !state.known {
			return timeoutReplayState{}, false
		}
		physical, ok := verifiedPhysicalCWD(state.cwd)
		if !ok {
			return timeoutReplayState{}, false
		}
		state.cwdPhysical = physical
		return state, true
	default:
		return timeoutReplayState{}, false
	}
}

// resolveLiteralDirectory resolves one static directory operand against the
// planner's current verified CWD and rejects dynamic shell spelling.
//
// Example: `inner` resolves below the current CWD, while `$TARGET` remains
// unknown even when an environment variable happens to exist.
func resolveLiteralDirectory(
	state timeoutReplayState,
	argument token,
) (string, bool) {
	if argument.value == "" || strings.ContainsAny(argument.value, "$`\\*?[]{}~") || !state.known {
		return "", false
	}
	path := argument.value
	if !filepath.IsAbs(path) {
		if cdPath, ok := resolveCDPathDirectory(state, path); ok {
			return cdPath, true
		}
		path = joinLiteralPath(state.cwd, path)
	}
	if !isVerifiedAbsoluteTimeoutCWD(filepath.Clean(path)) {
		return "", false
	}
	return path, true
}

// resolveCDPathDirectory applies the bounded CDPATH search used by a literal
// relative cd. Entries with a slash remain ordinary relative roots, while an
// empty entry denotes the current directory.
//
// Example: CDPATH=/work; cd project resolves /work/project before ./project.
func resolveCDPathDirectory(state timeoutReplayState, value string) (string, bool) {
	if !state.cdPathSet || value == "" || strings.ContainsRune(value, filepath.Separator) ||
		value == "." || value == ".." {
		return "", false
	}
	for _, entry := range strings.Split(state.cdPath, string(filepath.ListSeparator)) {
		base := entry
		if base == "" {
			base = state.cwd
		} else if !filepath.IsAbs(base) {
			base = filepath.Join(state.cwd, base)
		}
		candidate := filepath.Clean(joinLiteralPath(base, value))
		if isVerifiedAbsoluteTimeoutCWD(candidate) {
			return candidate, true
		}
	}
	return "", false
}

// joinLiteralPath joins shell path words without normalizing away a later
// `..`; physical cd must process that component after resolving symlinks.
//
// Example: joining /work/link with .. preserves /work/link/.. for -P.
func joinLiteralPath(base, value string) string {
	if base == string(filepath.Separator) {
		return base + value
	}
	return strings.TrimRight(base, string(filepath.Separator)) + string(filepath.Separator) + value
}

// resolvePhysicalDirectory follows each path component before processing a
// later `..`, matching Bash's physical cd traversal order.
//
// Example: link/.. resolves to the target's parent, not the lexical parent of
// the symlink spelling.
func resolvePhysicalDirectory(path string) (string, bool) {
	if !filepath.IsAbs(path) {
		return "", false
	}
	volume := filepath.VolumeName(path)
	root := volume + string(filepath.Separator)
	remainder := strings.TrimPrefix(path, root)
	lexical := root
	physical := root
	for _, component := range strings.Split(remainder, string(filepath.Separator)) {
		switch component {
		case "", ".":
			continue
		case "..":
			lexical = strings.TrimRight(lexical, string(filepath.Separator)) + string(filepath.Separator) + component
			physical = filepath.Dir(physical)
		default:
			if lexical == root {
				lexical += component
			} else {
				lexical += string(filepath.Separator) + component
			}
			resolved, err := filepath.EvalSymlinks(lexical)
			if err != nil {
				return "", false
			}
			physical = filepath.Clean(resolved)
		}
	}
	if !isVerifiedAbsoluteTimeoutCWD(physical) {
		return "", false
	}
	return physical, true
}

// applyPathBuiltinTransition preserves the existing literal PATH replay rules
// while allowing the shared transition model to carry directory changes.
//
// Example: `export -n PATH; timeout 5 git status` retains lookup PATH but marks
// the probe child environment as unexported.
func applyPathBuiltinTransition(
	state timeoutReplayState,
	argv []token,
) (timeoutReplayState, bool) {
	if len(argv) == 2 && argv[0].value == "export" && !argv[0].quoted && !argv[1].quoted {
		switch {
		case argv[1].value == "PATH":
			state.pathExported = state.pathSet
			return state, true
		case assignmentName(argv[1]) == "PATH":
			state.commandPath = strings.TrimPrefix(argv[1].value, "PATH=")
			state.pathSet = true
			state.pathExported = true
			return state, true
		case assignmentName(argv[1]) == "CDPATH":
			state.cdPath = strings.TrimPrefix(argv[1].value, "CDPATH=")
			state.cdPathSet = true
			return state, true
		}
	}
	if len(argv) == 3 && argv[0].value == "export" && argv[1].value == "-n" &&
		argv[2].value == "PATH" && !argv[0].quoted && !argv[1].quoted && !argv[2].quoted {
		state.pathExported = false
		return state, true
	}
	if len(argv) == 2 && argv[0].value == "unset" && argv[1].value == "PATH" &&
		!argv[0].quoted && !argv[1].quoted {
		state.commandPath = ""
		state.pathSet = false
		state.pathExported = false
		return state, true
	}
	if len(argv) == 2 && argv[0].value == "unset" && argv[1].value == "CDPATH" &&
		!argv[0].quoted && !argv[1].quoted {
		state.cdPath = ""
		state.cdPathSet = false
		return state, true
	}
	return state, true
}

// timeoutReplayStateForRequest returns the callback's initial state when its
// CWD is verified. Older callers that do not send an export attribute retain
// the historical environment-derived behavior for a set PATH.
//
// Example: a callback with command_path_set=false starts with an unset PATH.
func timeoutReplayStateForRequest(request Request) timeoutReplayState {
	cwd, ok := timeoutProbeContext(request)
	if !ok {
		return timeoutReplayState{}
	}
	pathExported := request.CommandPathSet
	if request.CommandPathExported != nil {
		pathExported = request.CommandPathSet && *request.CommandPathExported
	}
	return timeoutReplayState{
		cwd:          cwd,
		cwdPhysical:  request.CWDPhysical,
		commandPath:  request.CommandPath,
		pathSet:      request.CommandPathSet,
		pathExported: pathExported,
		cdPath:       request.CDPath,
		cdPathSet:    request.CDPathSet,
		known:        true,
	}
}

// advanceTimeoutReplayState advances only a direct sequential state-only
// segment or a literal-known &&/|| branch. Every other prefix is intentionally
// opaque so an uncertain shell effect cannot manufacture a timeout observation.
//
// Example: cd /tmp; PATH=/tools advances state, while cd /tmp && timeout
// poisons the state because the conditional may not run.
func advanceTimeoutReplayState(
	state timeoutReplayState,
	current segment,
	operator string,
) timeoutReplayState {
	switch operator {
	case ";", "\n":
	case "&&", "||":
		status, known := literalSegmentStatus(current)
		if !known || (operator == "&&" && !status) || (operator == "||" && status) {
			return timeoutReplayState{}
		}
	default:
		return timeoutReplayState{}
	}
	return advanceCompoundTransitionState(state, current, operator)
}

// timeoutReplayForSegment records a direct literal timeout from the modeled
// shell state, or consumes an already-observed recursive replay without
// probing the outer callback context again.
//
// Example: a recursive segment with timeout_replay=true and an opaque record
// stays opaque even when the outer callback PATH names a launching timeout.
func timeoutReplayForSegment(
	request Request,
	current segment,
	segmentIndex int,
	state timeoutReplayState,
) (TimeoutReplay, bool) {
	if request.TimeoutReplay {
		return suppliedTimeoutReplayForSegment(request, current, segmentIndex)
	}
	prefix, ok := directLiteralTimeoutPrefix(current)
	if !ok || !state.known {
		return TimeoutReplay{}, false
	}
	replay := TimeoutReplay{
		Segment:             segmentIndex,
		ParentSegment:       segmentIndex,
		Prefix:              timeoutPrefixValues(prefix),
		CWD:                 state.cwd,
		CWDPhysical:         state.cwdPhysical,
		CommandPath:         state.commandPath,
		CommandPathSet:      state.pathSet,
		CommandPathExported: state.pathExported,
		Disposition:         TimeoutReplayOpaque,
	}
	executable, ok := resolveTimeoutExecutable(
		current.argv[0].value,
		state.cwd,
		state.commandPath,
		state.pathSet,
	)
	if !ok || !timeoutExecutableLaunchesProbeChild(
		executable,
		prefix,
		state.cwd,
		state.commandPath,
		state.pathSet,
		state.pathExported,
	) {
		return replay, true
	}
	replay.Disposition = TimeoutReplayObserved
	return replay, true
}

// suppliedTimeoutReplayForSegment validates one recursive record against the
// direct timeout spelling before it can expose the child argv. Invalid or
// absent data remains advisory and deliberately suppresses a stale reprobe.
//
// Example: a parent segment two record is valid only after Bash maps it to
// local child segment one with the identical literal prefix.
func suppliedTimeoutReplayForSegment(
	request Request,
	current segment,
	segmentIndex int,
) (TimeoutReplay, bool) {
	prefix, ok := directLiteralTimeoutPrefix(current)
	if !ok || segmentIndex != 1 || len(request.TimeoutReplays) != 1 {
		return TimeoutReplay{}, false
	}
	replay := request.TimeoutReplays[0]
	if !validTimeoutReplay(replay) || replay.Segment != segmentIndex ||
		!sameTimeoutPrefix(replay.Prefix, prefix) {
		return TimeoutReplay{}, false
	}
	if replay.CWDPhysical == "" {
		replay.CWDPhysical, _ = verifiedPhysicalCWD(replay.CWD)
	}
	return replay, true
}

// directLiteralTimeoutPrefix returns timeout's prefix only for a direct
// literal command segment without assignments, wrappers, or shell expansion
// syntax. Redirects are intentionally excluded from the probe argv.
//
// Example: timeout 5 git status is direct, while env timeout 5 git status is
// opaque because env controls the child launch.
func directLiteralTimeoutPrefix(current segment) ([]token, bool) {
	if len(current.argv) == 0 || assignmentName(current.argv[0]) != "" || current.argv[0].quoted ||
		filepath.Base(current.argv[0].value) != "timeout" ||
		!timeoutSegmentIsLiteral(current.command) {
		return nil, false
	}
	prefix, _, ok := timeoutPrefix(current.argv)
	if !ok {
		return nil, false
	}
	return prefix, true
}

// validTimeoutReplay reports whether replay carries one bounded, internally
// coherent execution context. It never turns malformed metadata into a
// planner diagnostic.
//
// Example: an unset PATH record must carry an empty value and no export bit.
func validTimeoutReplay(replay TimeoutReplay) bool {
	if replay.Segment < 1 || replay.Segment > maxSegments ||
		replay.ParentSegment < 1 || replay.ParentSegment > maxSegments ||
		!isVerifiedAbsoluteTimeoutCWD(replay.CWD) || len(replay.Prefix) < 2 ||
		len(replay.Prefix) > maxArguments {
		return false
	}
	physical, ok := verifiedPhysicalCWD(replay.CWD)
	if !ok || replay.CWDPhysical != "" && replay.CWDPhysical != physical {
		return false
	}
	for _, value := range replay.Prefix {
		if value == "" || len(value) > maxArgumentBytes {
			return false
		}
	}
	if !replay.CommandPathSet && (replay.CommandPath != "" || replay.CommandPathExported) {
		return false
	}
	switch replay.Disposition {
	case TimeoutReplayObserved, TimeoutReplayOpaque:
		return true
	default:
		return false
	}
}

// sameTimeoutPrefix reports whether replay's encoded prefix is byte-for-byte
// identical to the literal prefix parsed from the current segment.
//
// Example: timeout 5 and timeout 6 cannot share one observation.
func sameTimeoutPrefix(values []string, prefix []token) bool {
	if len(values) != len(prefix) {
		return false
	}
	for index, argument := range prefix {
		if values[index] != argument.value {
			return false
		}
	}
	return true
}

// timeoutPrefixValues copies prefix values into JSON-safe replay metadata.
//
// Example: timeout --signal TERM 5 yields ["timeout", "--signal", "TERM", "5"].
func timeoutPrefixValues(prefix []token) []string {
	values := make([]string, len(prefix))
	for index, argument := range prefix {
		values[index] = argument.value
	}
	return values
}

// timeoutSegmentIsLiteral conservatively rejects shell forms whose values can
// expand before timeout receives its argv. Quoted literal spellings containing
// these characters also stay opaque, which preserves ordinary behavior.
//
// Example: `timeout --signal '$SIGNAL' 5 git status` receives no launch fact.
func timeoutSegmentIsLiteral(command string) bool {
	return !strings.ContainsAny(command, "$`\\*?[]{}~")
}

// timeoutProbeContext returns the verified callback working directory used to
// run an observed timeout probe.
//
// Example: an absolute existing callback directory permits a direct timeout
// literal even when callback PATH is unset.
func timeoutProbeContext(request Request) (string, bool) {
	if !isVerifiedAbsoluteTimeoutCWD(request.CWD) {
		return "", false
	}
	return request.CWD, true
}

// isVerifiedAbsoluteTimeoutCWD reports whether path is an existing absolute
// directory suitable for direct timeout lookup and child execution.
//
// Example: /work/project is valid when it is a directory, while relative is
// not a replayable callback CWD.
func isVerifiedAbsoluteTimeoutCWD(path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir() && syscall.Access(path, executePermission) == nil
}

// verifiedPhysicalCWD returns the canonical searchable identity paired with a
// verified logical shell directory. Logical spelling remains the basis for
// shell-relative lookup; this identity is only for ownership and inode checks.
//
// Example: a logical symlink `/work/current` returns its real directory while
// preserving `/work/current` in the planner's CWD field.
func verifiedPhysicalCWD(path string) (string, bool) {
	if !isVerifiedAbsoluteTimeoutCWD(path) {
		return "", false
	}
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	physical = filepath.Clean(physical)
	if !isVerifiedAbsoluteTimeoutCWD(physical) {
		return "", false
	}
	return physical, true
}

// resolveTimeoutExecutable resolves a direct timeout literal from the original
// callback PATH or from the callback cwd without consulting the hook-mutated
// process PATH. Bare timeout retains shell PATH order: empty and relative
// components resolve from the verified callback cwd.
//
// Example: bare `timeout` uses Request.CommandPath, while `./timeout` resolves
// under Request.CWD.
func resolveTimeoutExecutable(
	literal string,
	cwd string,
	commandPath string,
	commandPathSet bool,
) (string, bool) {
	switch {
	case literal == "timeout":
		if !commandPathSet || commandPath == "" {
			return "", false
		}
		entries := strings.Split(commandPath, ":")
		for _, entry := range entries {
			if entry == "" || !filepath.IsAbs(entry) {
				entry = filepath.Join(cwd, entry)
			}
			if executable, ok := resolvedExecutable(filepath.Join(entry, literal)); ok {
				return executable, true
			}
		}
		return "", false
	case filepath.IsAbs(literal):
		return resolvedExecutable(literal)
	case strings.ContainsRune(literal, filepath.Separator):
		if !filepath.IsAbs(cwd) {
			return "", false
		}
		return resolvedExecutable(filepath.Join(cwd, literal))
	default:
		return "", false
	}
}

// resolvedExecutable returns an existing regular executable after filesystem
// resolution so a probe invokes the same target selected by a literal path.
//
// Example: a symlinked timeout path resolves to its executable target.
func resolvedExecutable(path string) (string, bool) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !filepath.IsAbs(resolved) {
		return "", false
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", false
	}
	return filepath.Clean(resolved), true
}

// timeoutExecutableLaunchesProbeChild runs a short bounded replacement-child
// probe with the verified callback PWD and captured PATH state, and reports
// whether the child, rather than the timeout executable's exit status,
// acknowledged its launch.
//
// Example: a timeout implementation that exits zero without invoking its child
// returns false.
func timeoutExecutableLaunchesProbeChild(
	executable string,
	prefix []token,
	callbackCWD string,
	callbackPath string,
	callbackPathSet bool,
	callbackPathExported bool,
) bool {
	probeChild, ok := resolvedExecutable(timeoutProbeChild)
	if !ok || len(prefix) < 2 {
		return false
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return false
	}
	defer reader.Close()
	defer writer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), timeoutProbeTimeout)
	defer cancel()
	arguments := make([]string, 0, len(prefix)+2)
	for _, argument := range prefix[1:] {
		arguments = append(arguments, argument.value)
	}
	arguments = append(arguments, probeChild, "%s", timeoutProbeAcknowledgement)
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Dir = callbackCWD
	environment := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "PATH=") || strings.HasPrefix(entry, "PWD=") {
			continue
		}
		environment = append(environment, entry)
	}
	environment = append(environment, "PWD="+callbackCWD)
	if callbackPathSet && callbackPathExported {
		environment = append(environment, "PATH="+callbackPath)
	}
	command.Env = environment
	command.Stdout = writer
	command.Stderr = io.Discard
	command.WaitDelay = timeoutProbeWaitDelay
	if err := command.Start(); err != nil {
		return false
	}
	if err := writer.Close(); err != nil {
		cancel()
		_ = command.Wait()
		return false
	}

	acknowledgement := make(chan bool, 1)
	go func() {
		buffer := make([]byte, len(timeoutProbeAcknowledgement))
		_, readErr := io.ReadFull(reader, buffer)
		acknowledgement <- readErr == nil && string(buffer) == timeoutProbeAcknowledgement
	}()
	completed := make(chan struct{})
	go func() {
		_ = command.Wait()
		close(completed)
	}()

	select {
	case observed := <-acknowledgement:
		cancel()
		<-completed
		return observed
	case <-ctx.Done():
		<-completed
		return false
	case <-completed:
		select {
		case observed := <-acknowledgement:
			return observed
		case <-ctx.Done():
			return false
		}
	}
}

func inspectSegment(
	request Request,
	current segment,
	segmentIndex int,
	wholeSingleSegmentPlan bool,
	timeoutLaunches []TimeoutLaunch,
	ledgerRedirectAppend *bool,
) (DecisionKind, *Diagnostic) {
	if len(current.argv) == 0 {
		return DecisionDeny, diagnosticForToken(
			CodePlanSyntaxDenied,
			"command segment is empty",
			segmentIndex,
			0,
			token{value: "<empty>", offset: current.offset},
			"supply one finite literal argv",
			"empty-segment",
		)
	}
	safeLedgerRedirects := make([]bool, len(current.redirects))
	var controlIndex activeControlIndex
	if request.Marker == MarkerActive {
		controlIndex = activeControlFileIndex(request.ActiveMarkers)
		deferToProvider := false
		for redirectIndex, redirect := range current.redirects {
			currentLedgerAppend := false
			decision, diagnostic := inspectProofPathOwnership(
				request,
				redirectWriterArgv(redirect),
				segmentIndex,
				&redirect,
				&currentLedgerAppend,
				controlIndex,
			)
			if diagnostic != nil {
				return decision, diagnostic
			}
			deferToProvider = deferToProvider || decision == DecisionDefer
			if currentLedgerAppend {
				safeLedgerRedirects[redirectIndex] = true
				if ledgerRedirectAppend != nil {
					*ledgerRedirectAppend = true
				}
			}
		}
		decision, diagnostic := inspectProofPathOwnership(request, current.argv, segmentIndex, nil, nil, controlIndex)
		if diagnostic != nil {
			return decision, diagnostic
		}
		if deferToProvider || decision == DecisionDefer {
			return DecisionDefer, nil
		}
	}
	if request.HookMode {
		for _, redirect := range current.redirects {
			if diagnostic := inspectHookSessionsWriter(request, redirectWriterArgv(redirect), segmentIndex); diagnostic != nil {
				return DecisionDeny, diagnostic
			}
		}
	}
	argvForUnwrap := current.argv
	for len(argvForUnwrap) > 0 && assignmentName(argvForUnwrap[0]) != "" {
		argvForUnwrap = argvForUnwrap[1:]
	}
	if len(argvForUnwrap) == 0 {
		// A shell environment assignment without an executable has no resolved
		// filesystem, control, or cross-session target for this planner to gate.
		return DecisionAllow, nil
	}

	unwrapped, diagnostic := unwrapWithMetadata(argvForUnwrap, segmentIndex, timeoutLaunches)
	if diagnostic != nil {
		// Wrapper option spelling and an incomplete wrapper do not identify a
		// concrete target. Leave those ordinary forms to the invoked shell.
		return DecisionAllow, nil
	}
	argv := unwrapped.argv
	name := filepath.Base(argv[0].value)
	if request.HookMode && name == "git" {
		// Native Git owns its option grammar. The callback checks named mutation
		// families after gathering every compound and nested command effect.
		return DecisionAllow, nil
	}
	gitCloneLaunch, gitCloneLaunchVisible := gitCloneSourceAcquisitionLaunch(
		current.argv,
		unwrapped,
		wholeSingleSegmentPlan,
	)
	if request.Marker == MarkerActive && request.Role == RoleWorker && name == "git" && !gitCloneLaunchVisible {
		// The provider owns native Git family routing; keep clone acquisition separate.
		return DecisionDefer, nil
	}
	if name == "git" {
		if optionIndex, ok := gitFsckLostFoundOption(argv, gitSubcommandIndex(argv)); ok {
			routeQualified := wholeSingleSegmentPlan && isWorkerEnvGitFsckLostFound(current.argv)
			if request.Provider == ProviderOmp && request.Marker == MarkerActive && request.Role == RoleWorker && !routeQualified {
				return DecisionDeny, diagnosticForToken(
					CodeWorkerGitOwnershipDenied,
					"worker argv selects Git fsck --lost-found writer",
					segmentIndex,
					originalTokenIndex(current.argv, argv[optionIndex]),
					argv[optionIndex],
					"remove --lost-found or route the repository-metadata write request to the ECI Supervisor",
					"worker-git-ownership",
				)
			}
			if hasAttachedGitContextOption(argv) {
				return inspectGit(request, argv, segmentIndex)
			}
			return DecisionDefer, nil
		}
	}
	if request.Marker == MarkerActive && name == "mktemp" {
		// The provider resolves direct worker directory templates against its
		// protected-control namespace and retains the coordinator template
		// route. Defer before generic admission so neither target check is lost.
		return DecisionDefer, nil
	}

	// Interpreter selectors, environment helpers, and utility options are
	// ordinary command forms. They do not identify a concrete destructive,
	// cross-session, or ECI-control target by themselves.
	if isGateModeReadOnlyGet(argv) {
		return DecisionAllow, nil
	}
	if isGateModeInvocation(argv) {
		if diagnostic := inspectGateMode(request, current.argv, argv, segmentIndex, wholeSingleSegmentPlan); diagnostic != nil {
			return DecisionDeny, diagnostic
		}
		return DecisionAllow, nil
	}
	if request.Marker == MarkerActive && request.Role == RoleWorker && isWorkerRepositoryAllowInvocation(argv) {
		// The provider-side lifecycle adapter still validates the canonical
		// executable, active marker, repository, and reason. Defer this exact
		// owner-scoped route before the generic worker lifecycle identity check.
		return DecisionDefer, nil
	}
	if request.Marker == MarkerActive && request.Role == RoleWorker && !isLifecycleReadOnlyInvocation(argv) {
		if diagnostic := inspectActiveWorkerLifecycleIdentity(request, current.argv, argv, segmentIndex); diagnostic != nil {
			return DecisionDeny, diagnostic
		}
		if !request.HookMode && argv[0].value == "eci-active" {
			return DecisionDeny, diagnosticForToken(
				CodeControlOwnerRequired,
				"worker argv selects coordinator-owned bare lifecycle command: executable=eci-active invocation=unwrapped",
				segmentIndex,
				originalTokenIndex(current.argv, argv[0]),
				argv[0],
				"route the exact lifecycle/control invocation through the coordinator",
				"worker-lifecycle-control",
			)
		}
	}
	effectiveCWD := ""
	if request.CWDKnown {
		effectiveCWD = request.CWD
	}
	if request.Marker == MarkerActive && isLifecycleScriptCapability(effectiveCWD, argv) {
		// The provider adapter owns canonical lifecycle-script identity,
		// arguments, and role authorization. Recognize only a visible script
		// position here so active fast admission cannot bypass that route.
		return DecisionDefer, nil
	}
	if request.Marker == MarkerActive && isCoordinatorHookRepair(argv) {
		// The legacy coordinator route performs the canonical script digest,
		// repository-root, and peer-identity checks. Defer this capability by
		// shape so those checks remain authoritative without embedding provider
		// paths or peer allowlists in the compiled planner.
		return DecisionDefer, nil
	}
	if request.Marker == MarkerActive && wholeSingleSegmentPlan && request.ShellDepth == 0 {
		if target, ok := isPreCommitHookModeRepair(current.argv); ok {
			// Hook-mode repair is an Emergency Unblock exception for one complete
			// direct plan. A compound plan retains its ordinary segment policy.
			switch request.Role {
			case RoleWorker:
				return DecisionDeny, diagnosticForToken(
					CodeControlOwnerRequired,
					"worker argv selects coordinator-owned pre-commit hook mode repair",
					segmentIndex,
					originalTokenIndex(current.argv, target),
					target,
					"route the exact pre-commit hook mode repair through the coordinator",
					"hook-mode-repair",
				)
			case RoleCoordinator:
				return DecisionDefer, nil
			}
		}
	}
	if request.Marker == MarkerActive {
		if target, resolved, ok := protectedHookModeMutation(effectiveCWD, argv); ok {
			switch request.Role {
			case RoleCoordinator:
				return DecisionDefer, nil
			case RoleWorker:
				diagnostic := diagnosticForToken(
					CodeControlOwnerRequired,
					"worker argv selects coordinator-owned protected hook mode mutation",
					segmentIndex,
					originalTokenIndex(current.argv, target),
					target,
					"route the protected hook-mode mutation through the coordinator",
					"worker-hook-mode-ownership",
				)
				diagnostic.Path = resolved
				return DecisionDeny, diagnostic
			}
		}
	}
	if name == "git" {
		decision, diagnostic := inspectGit(request, argv, segmentIndex)
		if diagnostic != nil {
			return DecisionDeny, diagnostic
		}
		if decision == DecisionDefer {
			if request.Marker == MarkerActive && gitCloneLaunchVisible {
				return DecisionDeny, gitCloneLaunchContextDiagnostic(current.argv, segmentIndex)
			}
			return DecisionDefer, nil
		}
		if request.Marker == MarkerActive {
			if gitCloneLaunch != nil {
				return DecisionAllow, nil
			}
			if gitCloneLaunchVisible {
				return DecisionDeny, gitCloneLaunchContextDiagnostic(current.argv, segmentIndex)
			}
			return DecisionDefer, nil
		}
	}
	if request.Marker == MarkerActive && gitCloneLaunchVisible {
		return DecisionDeny, gitCloneLaunchContextDiagnostic(current.argv, segmentIndex)
	}
	if isLifecycleScriptPath(effectiveCWD, argv[0].value) {
		return DecisionDefer, nil
	}
	if name == "eci-command-gate-mode" {
		return DecisionDefer, nil
	}
	if diagnostic := inspectBroadDestruction(argv, segmentIndex); diagnostic != nil {
		return DecisionDeny, diagnostic
	}
	if request.HookMode {
		if diagnostic := inspectHookSessionsWriter(request, argv, segmentIndex); diagnostic != nil {
			return DecisionDeny, diagnostic
		}
	}
	if !request.HookMode && request.Marker == MarkerActive && request.Role == RoleCoordinator && name == "find" {
		// Check each parsed segment before a compound plan can reach later
		// commands; every find action launches an unbounded per-match operation.
		if action, ok := findDynamicAction(argv); ok {
			return DecisionDeny, diagnosticForToken(
				CodePlanDynamicLaunchDenied,
				"find action executes a per-match operation outside a finite direct argv",
				segmentIndex,
				originalTokenIndex(current.argv, action),
				action,
				"replace the find action with a finite direct command, or use -print for inspection",
				"dynamic-find-action",
			)
		}
	}
	if request.Marker == MarkerActive && request.Role == RoleWorker {
		if isSourceWriter(name, argv) {
			if diagnostic := inspectLiveControl(request, argv, segmentIndex, controlIndex); diagnostic != nil {
				return DecisionDeny, diagnostic
			}
		}
		for redirectIndex, redirect := range current.redirects {
			if safeLedgerRedirects[redirectIndex] {
				continue
			}
			if diagnostic := inspectLiveControl(request, redirectWriterArgv(redirect), segmentIndex, controlIndex); diagnostic != nil {
				return DecisionDeny, diagnostic
			}
		}
	}
	return DecisionAllow, nil
}

// findDynamicAction returns the first find predicate that launches a child or
// mutates each match. Predicate operands are skipped so a literal pattern such
// as `-name -delete` remains an ordinary inspection command.
//
// Example: `find /tmp -type d -delete` returns `-delete`, while
// `find /tmp -name -delete -print` returns no dynamic action.
func findDynamicAction(argv []token) (token, bool) {
	valueOptions := map[string]struct{}{
		"-amin": {}, "-anewer": {}, "-atime": {}, "-cmin": {}, "-cnewer": {},
		"-ctime": {}, "-fstype": {}, "-gid": {}, "-group": {},
		"-iname": {}, "-inum": {}, "-ipath": {}, "-iregex": {}, "-iwholename": {},
		"-links": {}, "-lname": {}, "-mmin": {}, "-mtime": {},
		"-name": {}, "-newer": {}, "-newerXY": {}, "-path": {}, "-perm": {},
		"-size": {}, "-samefile": {}, "-type": {}, "-uid": {}, "-used": {},
		"-user": {}, "-wholename": {}, "-regex": {},
	}
	for index := 1; index < len(argv); index++ {
		value := argv[index].value
		if _, needsValue := valueOptions[value]; needsValue {
			if index+1 < len(argv) {
				index++
			}
			continue
		}
		switch value {
		case "-delete":
			return argv[index], true
		case "-exec", "-execdir", "-ok", "-okdir":
			return argv[index], true
		}
	}
	return token{}, false
}

// redirectWriterArgv gives one shell redirect destination the same concrete
// write-target treatment as a direct writer argument without discarding its
// independently retained effect.
//
// Example: `printf note >> log` produces the synthetic writer argv `tee log`.
func redirectWriterArgv(redirect outputRedirect) []token {
	return []token{{value: "tee"}, redirect.target}
}

// isGateModeInvocation reports whether the executable or a visible interpreter
// script operand names the command-gate mode control program.
//
// Example: command /home/user/.omp/agent/eci/bin/eci-command-gate-mode get is a
// gate-mode invocation even though command is a transparent wrapper.
func isGateModeInvocation(argv []token) bool {
	if len(argv) == 0 {
		return false
	}
	if filepath.Base(argv[0].value) == "eci-command-gate-mode" {
		return true
	}
	return len(argv) > 1 && isInterpreter(filepath.Base(argv[0].value)) &&
		filepath.Base(argv[1].value) == "eci-command-gate-mode"
}

// gateModeTarget returns the visible control-program token from a recognized
// direct or interpreter-shaped gate-mode invocation.
//
// Example: python3 /opt/eci-command-gate-mode get returns the script token.
func gateModeTarget(argv []token) (token, bool) {
	if len(argv) == 0 {
		return token{}, false
	}
	if filepath.Base(argv[0].value) == "eci-command-gate-mode" {
		return argv[0], true
	}
	if len(argv) > 1 && isInterpreter(filepath.Base(argv[0].value)) &&
		filepath.Base(argv[1].value) == "eci-command-gate-mode" {
		return argv[1], true
	}
	return token{}, false
}

// isGateModeReadOnlyGet reports whether a recognized gate-mode program has a
// visible get action, which only discovers its own state.
//
// Example: a copied eci-command-gate-mode get remains an ordinary command.
func isGateModeReadOnlyGet(argv []token) bool {
	target, ok := gateModeTarget(argv)
	if !ok {
		return false
	}
	for index, argument := range argv {
		if argument.offset == target.offset && argument.value == target.value {
			return index+1 < len(argv) && argv[index+1].value == "get"
		}
	}
	return false
}

// isDirectGateModeEnvelope reports whether original contains exactly the
// unwrapped direct control-program argv without quoting or an operator plan.
//
// Example: /home/user/.omp/agent/eci/bin/eci-command-gate-mode get is direct, while
// env FOO=bar /home/user/.omp/agent/eci/bin/eci-command-gate-mode get is not.
func isDirectGateModeEnvelope(original, argv []token, wholeSingleSegmentPlan bool) bool {
	if !wholeSingleSegmentPlan || len(original) != len(argv) || len(argv) < 2 ||
		filepath.Base(argv[0].value) != "eci-command-gate-mode" {
		return false
	}
	for index := range original {
		if original[index].quoted || original[index].offset != argv[index].offset ||
			original[index].value != argv[index].value {
			return false
		}
	}
	return true
}

// isExactGateModeArguments reports whether argv has the complete fixed action
// grammar accepted by the command-gate binary.
//
// Example: <gate-mode> set enforcing is exact, while <gate-mode> get extra is
// not because it has a trailing operand.
func isExactGateModeArguments(argv []token) bool {
	switch len(argv) {
	case 2:
		return argv[1].value == "get"
	case 3:
		return argv[1].value == "set" &&
			(argv[2].value == "permissive" || argv[2].value == "enforcing")
	default:
		return false
	}
}

// gateModeProviderPath returns the exact canonical command-gate path selected
// by the provider whose callback is being classified.
//
// Example: ProviderOmp selects the omp path and never its Kimi peer.
func gateModeProviderPath(provider Provider, identity gateModeIdentity) (string, bool) {
	switch provider {
	case ProviderOmp:
		// The omp path is independent of the Kimi peer root; it is present whenever the omp root resolves.
		if len(identity.canonicalPaths) < 1 {
			return "", false
		}
		return identity.canonicalPaths[0], true
	case ProviderKimi:
		if len(identity.canonicalPaths) != 2 {
			return "", false
		}
		return identity.canonicalPaths[1], true
	default:
		return "", false
	}
}

// gateModeEnvelopeDiagnostic rejects a control-program spelling that cannot
// prove the exact direct provider-owned control envelope.
//
// Example: a bare eci-command-gate-mode name is denied instead of resolving
// through a caller-controlled PATH.
func gateModeEnvelopeDiagnostic(
	original []token,
	target token,
	segmentIndex int,
	predicate string,
	reason string,
) *Diagnostic {
	return diagnosticForToken(
		CodeControlIdentityDenied,
		reason,
		segmentIndex,
		originalTokenIndex(original, target),
		target,
		"invoke the exact canonical provider command-gate binary with get or set <permissive|enforcing>",
		predicate,
	)
}

// inspectGateMode validates every execution-relevant component of a recognized
// command-gate control invocation before it can receive a generic fast path.
//
// Example: the exact current-provider binary with `set enforcing` is valid for
// a coordinator, while the same command remains worker-owned for a worker.
func inspectGateMode(
	request Request,
	original []token,
	argv []token,
	segmentIndex int,
	wholeSingleSegmentPlan bool,
) *Diagnostic {
	target, ok := gateModeTarget(argv)
	if !ok {
		return nil
	}
	if request.HookMode {
		return inspectHookGateModeTarget(request, argv, target, segmentIndex)
	}
	if !isDirectGateModeEnvelope(original, argv, wholeSingleSegmentPlan) {
		return gateModeEnvelopeDiagnostic(
			original,
			target,
			segmentIndex,
			"gate-mode-envelope",
			"command-gate control requires one unquoted direct canonical executable argv without wrappers or operators",
		)
	}

	identity := gateModeIdentityForEnvironment(request.Provider)
	expected, _ := gateModeProviderPath(request.Provider, identity)
	if target.value != expected {
		predicate := "gate-mode-envelope"
		reason := "command-gate control requires the exact provider-selected canonical executable path"
		if filepath.IsAbs(target.value) {
			predicate = "gate-mode-identity"
			reason = "command-gate control executable identity does not match the current provider's canonical path"
			for _, canonicalPath := range identity.canonicalPaths {
				if target.value == canonicalPath {
					predicate = "gate-mode-provider-path"
					reason = "command-gate control executable belongs to the peer provider rather than the current callback provider"
					break
				}
			}
		}
		return gateModeEnvelopeDiagnostic(original, target, segmentIndex, predicate, reason)
	}
	if !isExactGateModeArguments(argv) {
		return gateModeEnvelopeDiagnostic(
			original,
			target,
			segmentIndex,
			"gate-mode-envelope",
			"command-gate control requires exactly get or set <permissive|enforcing> without trailing argv",
		)
	}
	if identity.failure != "" {
		diagnostic := gateModeEnvelopeDiagnostic(
			original,
			target,
			segmentIndex,
			"gate-mode-identity",
			"command-gate control executable identity validation failed: failure="+identity.failure,
		)
		diagnostic.Path = expected
		return diagnostic
	}
	if request.Role != RoleWorker || argv[1].value != "set" {
		return nil
	}
	return diagnosticForToken(
		CodeControlOwnerRequired,
		"worker argv selects coordinator-owned command-gate mode mutation",
		segmentIndex,
		originalTokenIndex(original, argv[1]),
		argv[1],
		"route the exact command-gate mode change through the coordinator",
		"gate-mode-mutation",
	)
}

// inspectHookGateModeTarget checks the resolved mode mutation and its owner.
// Quoting, wrappers and unknown CLI arguments do not create a control effect.
// A statable non-canonical set target denies gate-mode-identity.
//
// Example: a quoted canonical set invocation belongs to the coordinator.
func inspectHookGateModeTarget(
	request Request,
	argv []token,
	target token,
	segmentIndex int,
) *Diagnostic {
	if len(argv) != 3 || argv[1].value != "set" || argv[2].value != "permissive" && argv[2].value != "enforcing" {
		return nil
	}
	resolved := resolveExecutable(target.value, request.CWD)
	actual, err := os.Stat(resolved)
	if resolved == "" || err != nil {
		return gateModeEnvelopeDiagnostic(argv, target, segmentIndex, "gate-mode-identity", "command-gate mode target has no statable executable identity")
	}
	for _, root := range protectedHookModeRoots() {
		canonical := filepath.Join(root, "bin", "eci-command-gate-mode")
		expected, err := os.Stat(canonical)
		if err != nil || !os.SameFile(actual, expected) {
			continue
		}
		own := resolvePathIdentity(root) == resolvePathIdentity(filepath.Join(os.Getenv("HOME"), ".omp", "agent", "eci"))
		if request.Role == RoleCoordinator && own {
			return nil
		}
		diagnostic := diagnosticForToken(CodeControlOwnerRequired, "resolved command-gate mode mutation selects its coordinator-owned control target", segmentIndex, originalTokenIndex(argv, target), target, "route this exact mode change through the owning provider coordinator in the current session", "gate-mode-owner")
		diagnostic.Path = canonical
		return diagnostic
	}
	return gateModeEnvelopeDiagnostic(argv, target, segmentIndex, "gate-mode-identity", "command-gate mode target is not a canonical provider command-gate binary")
}

// isExactGateModeEnvelope reports whether parsed is the direct current-provider
// command-gate capability after identity and argument validation.
//
// Example: the exact omp binary with `get` is an envelope; any other binary is
// not.
func isExactGateModeEnvelope(
	request Request,
	original []token,
	argv []token,
	wholeSingleSegmentPlan bool,
) bool {
	if !isDirectGateModeEnvelope(original, argv, wholeSingleSegmentPlan) ||
		!isExactGateModeArguments(argv) {
		return false
	}
	identity := gateModeIdentityForEnvironment(request.Provider)
	expected, providerKnown := gateModeProviderPath(request.Provider, identity)
	return providerKnown && identity.failure == "" && argv[0].value == expected
}

func originalTokenIndex(original []token, target token) int {
	for index, argument := range original {
		if argument.offset == target.offset && argument.value == target.value {
			return index
		}
	}
	return 0
}

// gateModeIdentityForEnvironment returns the canonical gate-mode identity for
// provider. ProviderOmp checks only the omp root and gate binary, and never
// consults the Kimi peer. ProviderKimi keeps the two-root checks: its gate must
// be the same file as the omp gate and have the same digest.
//
// Example: with no Kimi root, the omp identity still resolves and the Kimi
// identity reports canonical-path-invalid.
func gateModeIdentityForEnvironment(provider Provider) gateModeIdentity {
	home := os.Getenv("HOME")
	roots := []string{filepath.Join(home, ".omp", "agent", "eci")}
	if provider != ProviderOmp {
		roots = append(roots, firstNonEmpty(os.Getenv("KIMI_CODE_HOME"), filepath.Join(home, ".kimi-code")))
	}
	paths := make([]string, 0, len(roots))
	for _, root := range roots {
		if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
			return gateModeIdentity{canonicalPaths: paths, failure: "canonical-path-invalid"}
		}
		resolvedRoot, err := filepath.EvalSymlinks(root)
		if err != nil || filepath.Clean(resolvedRoot) != root {
			return gateModeIdentity{canonicalPaths: paths, failure: "canonical-path-invalid"}
		}
		paths = append(paths, filepath.Join(root, "bin", "eci-command-gate-mode"))
	}
	if len(paths) != len(roots) {
		return gateModeIdentity{canonicalPaths: paths, failure: "canonical-path-invalid"}
	}
	infos := make([]os.FileInfo, 0, len(paths))
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			return gateModeIdentity{canonicalPaths: paths, failure: "canonical-path-missing"}
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() ||
			!fileOwnedByCurrentUser(info) || info.Mode().Perm()&0111 == 0 {
			return gateModeIdentity{canonicalPaths: paths, failure: "canonical-metadata-invalid"}
		}
		infos = append(infos, info)
	}
	if provider == ProviderOmp {
		digest, err := digestRegularFile(paths[0], infos[0])
		if err != nil {
			return gateModeIdentity{canonicalPaths: paths, failure: "canonical-read-race"}
		}
		// The receipt is report-only: a missing or mismatched receipt is an
		// advisory on stderr and never denies omp gate-mode control.
		if receiptErr := gateModeReceiptError(paths[0], digest); receiptErr != nil {
			hookAdvisory("command-gate receipt", receiptErr)
		}
		return gateModeIdentity{canonicalPaths: paths}
	}
	if !os.SameFile(infos[0], infos[1]) {
		return gateModeIdentity{canonicalPaths: paths, failure: "canonical-hardlink-split"}
	}
	digest, err := digestRegularFile(paths[0], infos[0])
	if err != nil {
		return gateModeIdentity{canonicalPaths: paths, failure: "canonical-read-race"}
	}
	peerDigest, err := digestRegularFile(paths[1], infos[1])
	if err != nil || digest != peerDigest {
		return gateModeIdentity{canonicalPaths: paths, failure: "canonical-digest-mismatch"}
	}
	return gateModeIdentity{canonicalPaths: paths}
}

// gateModeReceiptError checks the "<hex>  <name>" receipt that
// build-native-binaries.sh writes beside the gate binary. The receipt must be
// a small regular file owned by the current user whose first line records the
// binary digest and name.
//
// Example: bin/eci-command-gate-mode.sha256 must read "<sha256>  eci-command-gate-mode".
func gateModeReceiptError(binaryPath string, digest [sha256.Size]byte) error {
	receiptPath := binaryPath + ".sha256"
	info, err := os.Lstat(receiptPath)
	if os.IsNotExist(err) {
		return errors.New("canonical-receipt-missing")
	}
	if err != nil || !info.Mode().IsRegular() || !fileOwnedByCurrentUser(info) || info.Size() > 1024 {
		return errors.New("canonical-receipt-invalid")
	}
	contents, err := os.ReadFile(receiptPath)
	if err != nil {
		return errors.New("canonical-receipt-invalid")
	}
	line, _, _ := strings.Cut(string(contents), "\n")
	if line != fmt.Sprintf("%x  %s", digest, filepath.Base(binaryPath)) {
		return errors.New("canonical-receipt-mismatch")
	}
	return nil
}

func firstNonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func fileOwnedByCurrentUser(info os.FileInfo) bool {
	state, ok := info.Sys().(*syscall.Stat_t)
	return ok && state.Uid == uint32(os.Getuid())
}

func digestRegularFile(path string, expected os.FileInfo) ([sha256.Size]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	defer file.Close()
	observed, err := file.Stat()
	if err != nil || !os.SameFile(observed, expected) || observed.Size() != expected.Size() {
		if err == nil {
			err = errors.New("file changed while reading")
		}
		return [sha256.Size]byte{}, err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return [sha256.Size]byte{}, err
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

// inspectActiveWorkerLifecycleIdentity identifies a non-read-only unwrapped
// lifecycle control executable that is canonical or byte-identical to either
// provider's eci-active executable.
//
// Example: stdbuf -oL /tmp/eci-active-copy nested-exit is lifecycle control,
// while status/help bypass this identity check as ordinary discovery.
func inspectActiveWorkerLifecycleIdentity(
	request Request,
	original []token,
	argv []token,
	segmentIndex int,
) *Diagnostic {
	if len(argv) == 0 {
		return nil
	}

	effectiveCWD := ""
	if request.CWDKnown {
		effectiveCWD = request.CWD
	}
	candidate := resolveExecutable(argv[0].value, effectiveCWD)
	if candidate == "" {
		return nil
	}
	resolvedCandidate, resolveErr := resolvePathWithMissingSuffix(candidate)
	if resolveErr != nil {
		// A transient filesystem resolution failure cannot prove lifecycle
		// identity. Keep the lexical candidate for exact canonical paths.
		resolvedCandidate = candidate
	}
	for _, root := range canonicalLifecycleRoots() {
		for _, alias := range [...]string{"eci-review-gate", "eci-stage"} {
			canonical := filepath.Join(root, "bin", alias)
			if candidate == canonical || resolvedCandidate == canonical {
				return workerLifecycleIdentityDiagnostic(original, argv[0], segmentIndex, canonical)
			}
		}
	}

	candidateInfo, err := os.Stat(candidate)
	if err != nil || !candidateInfo.Mode().IsRegular() || !fileOwnedByCurrentUser(candidateInfo) || candidateInfo.Mode().Perm()&0111 == 0 {
		return nil
	}

	var candidateDigest [sha256.Size]byte
	hasCandidateDigest := false
	for _, root := range canonicalLifecycleRoots() {
		canonical := filepath.Join(root, "bin", "eci-active")
		canonicalInfo, err := os.Lstat(canonical)
		if err != nil || canonicalInfo.Mode()&os.ModeSymlink != 0 || !canonicalInfo.Mode().IsRegular() ||
			!fileOwnedByCurrentUser(canonicalInfo) || canonicalInfo.Mode().Perm()&0111 == 0 {
			continue
		}
		if os.SameFile(candidateInfo, canonicalInfo) {
			return workerLifecycleIdentityDiagnostic(original, argv[0], segmentIndex, canonical)
		}
		if candidateInfo.Size() != canonicalInfo.Size() {
			continue
		}
		if !hasCandidateDigest {
			candidateDigest, err = digestRegularFile(candidate, candidateInfo)
			if err != nil {
				return nil
			}
			hasCandidateDigest = true
		}
		canonicalDigest, err := digestRegularFile(canonical, canonicalInfo)
		if err == nil && candidateDigest == canonicalDigest {
			return workerLifecycleIdentityDiagnostic(original, argv[0], segmentIndex, canonical)
		}
	}
	return nil
}

// canonicalLifecycleRoots returns existing, canonical provider homes that may
// own lifecycle executables.
//
// Example: the lexical $HOME/.omp/agent/eci root may contribute its resolved
// bin/eci-active identity.
func canonicalLifecycleRoots() []string {
	home := os.Getenv("HOME")
	values := []string{
		filepath.Join(home, ".omp", "agent", "eci"),
		firstNonEmpty(os.Getenv("KIMI_CODE_HOME"), filepath.Join(home, ".kimi-code")),
	}
	roots := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, root := range values {
		if root == "" || !filepath.IsAbs(root) {
			continue
		}
		resolved, err := filepath.EvalSymlinks(filepath.Clean(root))
		if err != nil || !filepath.IsAbs(resolved) {
			continue
		}
		resolved = filepath.Clean(resolved)
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			continue
		}
		if _, exists := seen[resolved]; exists {
			continue
		}
		seen[resolved] = struct{}{}
		roots = append(roots, resolved)
	}
	return roots
}

// workerLifecycleIdentityDiagnostic reports ownership of an unwrapped
// lifecycle executable while preserving the original command token position.
//
// Example: an env child is reported at its original child argv index.
func workerLifecycleIdentityDiagnostic(
	original []token,
	target token,
	segmentIndex int,
	canonical string,
) *Diagnostic {
	diagnostic := diagnosticForToken(
		CodeControlOwnerRequired,
		"worker argv selects coordinator-owned lifecycle executable: canonical_target="+canonical+" invocation=unwrapped",
		segmentIndex,
		originalTokenIndex(original, target),
		target,
		"route the exact lifecycle/control invocation through the coordinator",
		"worker-lifecycle-control",
	)
	diagnostic.Path = canonical
	return diagnostic
}

// resolveExecutable resolves a command without substituting the process cwd.
//
// Example: ./tool with an unknown cwd cannot identify an executable.
func resolveExecutable(value, cwd string) string {
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	if strings.ContainsRune(value, filepath.Separator) {
		if !filepath.IsAbs(cwd) {
			return ""
		}
		return filepath.Clean(filepath.Join(cwd, value))
	}
	resolved, err := exec.LookPath(value)
	if err != nil {
		return ""
	}
	return filepath.Clean(resolved)
}

// inspectProofPathOwnership classifies concrete writer and redirect targets
// before deferring ordinary proof artifacts to the provider.
//
// Example: a note-named marker alias retains the marker's control identity,
// while an ordinary coordination note retains its provider route.
func inspectProofPathOwnership(
	request Request,
	argv []token,
	segmentIndex int,
	redirect *outputRedirect,
	ledgerRedirectAppend *bool,
	controlIndex activeControlIndex,
) (DecisionKind, *Diagnostic) {
	if request.CWDKnown || len(request.CWDCandidates) == 0 {
		return inspectProofPathOwnershipExact(request, argv, segmentIndex, redirect, ledgerRedirectAppend, controlIndex)
	}
	deferToProvider := false
	for _, candidate := range verifiedUniqueDirectories(request.CWDCandidates) {
		candidateRequest := request
		candidateRequest.CWD = candidate
		candidateRequest.CWDKnown = true
		candidateRequest.CWDCandidates = []string{candidate}
		decision, diagnostic := inspectProofPathOwnershipExact(
			candidateRequest,
			argv,
			segmentIndex,
			redirect,
			ledgerRedirectAppend,
			controlIndex,
		)
		if diagnostic != nil {
			return decision, diagnostic
		}
		deferToProvider = deferToProvider || decision == DecisionDefer
	}
	if deferToProvider {
		return DecisionDefer, nil
	}
	return DecisionAllow, nil
}

// inspectProofPathOwnershipExact resolves one concrete CWD candidate. The
// caller joins multiple planner-reachable candidates before invoking it.
//
// Example: a redirected `cd` checks both its pre- and post-redirect paths.
func inspectProofPathOwnershipExact(
	request Request,
	argv []token,
	segmentIndex int,
	redirect *outputRedirect,
	ledgerRedirectAppend *bool,
	controlIndex activeControlIndex,
) (DecisionKind, *Diagnostic) {
	proofSessions := make([]proofSession, 0, len(request.ActiveMarkers))
	for _, marker := range request.ActiveMarkers {
		if !filepath.IsAbs(marker) {
			return DecisionDefer, nil
		}
		lexical := filepath.Clean(filepath.Dir(marker))
		resolved, err := resolvePathWithMissingSuffix(lexical)
		if err != nil {
			return DecisionDefer, nil
		}
		proofSessions = append(proofSessions, proofSession{lexical: lexical, resolved: resolved})
	}
	if len(proofSessions) == 0 {
		return DecisionAllow, nil
	}
	selectedProofSession := proofSessions[0]
	deferToProvider := false
	entryMutation := request.HookMode && hookUnlinksDirectoryEntry(argv)

	for sourceIndex, argument := range argv {
		pathArgument, argumentIndex, pathLike := outputDestinationOperand(argv, sourceIndex)
		outputWriter := pathLike
		switch {
		case filepath.Base(argv[0].value) == "cp":
			// Copy operands are authoritative: a generic --output spelling
			// cannot manufacture a destination for an unsupported cp form.
			argumentIndex = sourceIndex
			pathArgument, pathLike = argument, argumentIndex > 0
			outputWriter = false
		case !pathLike:
			argumentIndex = sourceIndex
			pathArgument, pathLike = commandPathOperand(argv[0].value, argument)
		}
		if !pathLike {
			continue
		}
		writer := outputWriter || isSourceWriterOperand(argv, argumentIndex)
		lexical := pathArgument.value
		if !filepath.IsAbs(lexical) {
			if !request.CWDKnown {
				// The segment CWD is unknown. Keep absolute target checks
				// available, but defer unresolved relative effects to the
				// provider's targeted ownership boundary.
				deferToProvider = true
				continue
			}
			lexical = filepath.Join(request.CWD, lexical)
		}
		lexical = filepath.Clean(lexical)

		resolved, resolveErr := resolvePathWithMissingSuffix(lexical)
		if entryMutation {
			resolved, resolveErr = resolvePathEntryWithMissingSuffix(lexical)
		}
		containingSession := ""
		contained := false
		anchorSearchComplete := true
		for _, proofSession := range proofSessions {
			if resolveErr == nil && pathWithin(resolved, proofSession.resolved) {
				contained = true
				break
			}
			if pathWithin(lexical, proofSession.lexical) {
				containingSession = proofSession.lexical
				continue
			}
			anchored, complete := pathHasResolvedProofAnchor(filepath.Dir(lexical), proofSession.resolved)
			if !complete {
				anchorSearchComplete = false
			}
			if anchored {
				containingSession = proofSession.lexical
			}
		}
		selectedRedirect := redirect
		if outputWriter {
			selectedRedirect = &outputRedirect{target: pathArgument, effect: outputRedirectOverwrite}
		}
		if selectedRedirect != nil {
			handled, diagnostic := inspectCurrentLedgerRedirect(
				*selectedRedirect,
				selectedProofSession,
				lexical,
				resolved,
				resolveErr,
				segmentIndex,
				argumentIndex,
			)
			if diagnostic != nil {
				return DecisionDeny, diagnostic
			}
			if handled {
				if ledgerRedirectAppend != nil && selectedRedirect.effect == outputRedirectAppend {
					*ledgerRedirectAppend = true
				}
				continue
			}
		}
		canonicalHandoff := isCanonicalWorkerHandoffPath(lexical, request.ActiveMarkers) ||
			(resolveErr == nil && isCanonicalWorkerHandoffPath(resolved, request.ActiveMarkers))
		if writer {
			controlPath := activeControlResolvedPath(lexical, controlIndex.files)
			if entryMutation && controlPath != "" {
				protectedEntry, err := resolvePathEntryWithMissingSuffix(controlPath)
				if err != nil || resolveErr != nil || protectedEntry != resolved {
					controlPath = ""
				}
			}
			if controlPath != "" {
				if isCanonicalWorkerHandoffPath(controlPath, request.ActiveMarkers) &&
					filepath.Base(controlPath) != "high_level_log.jsonl" &&
					(lexical == controlPath || (resolveErr == nil && resolved == controlPath)) {
					// The destination is the canonical handoff inode by its own
					// pathname; the provider owns that exact file.  An alias
					// with a different pathname that shares the inode falls
					// through to the concrete control-target denial below.
					deferToProvider = true
					continue
				}
				code := CodePlanLiveControlDenied
				reason := "writer destination resolves to an active-session ECI control artifact"
				predicate := string(request.Role) + "-proof-control"
				remediation := "route this exact control operation through the coordinator lifecycle route"
				// The inode index retains the marker's lexical directory spelling;
				// do not pass that spelling as an already-resolved path.
				if request.Role == RoleCoordinator && isAppendOnlyLedgerPath(controlPath, "", proofSessions) {
					code = CodeLedgerAppendOnly
					reason = "active-session high-level ledger files are append-only coordinator artifacts"
					predicate = "append-only-ledger"
					remediation = ledgerAppendRemediation(request.Provider)
				}
				diagnostic := diagnosticForToken(code, reason, segmentIndex, argumentIndex, pathArgument, remediation, predicate)
				diagnostic.Path = controlPath
				return DecisionDeny, diagnostic
			}
		}
		if writer && canonicalHandoff &&
			!isAppendOnlyLedgerPath(lexical, resolved, proofSessions) {
			deferToProvider = true
			continue
		}
		if contained {
			if request.Role == RoleCoordinator && writer &&
				isAppendOnlyLedgerPath(lexical, resolved, proofSessions) {
				diagnostic := diagnosticForToken(
					CodeLedgerAppendOnly,
					"active-session high-level ledger files are append-only coordinator artifacts",
					segmentIndex,
					argumentIndex,
					pathArgument,
					ledgerAppendRemediation(request.Provider),
					"append-only-ledger",
				)
				diagnostic.Path = lexical
				return DecisionDeny, diagnostic
			}
			if request.Role == RoleCoordinator && writer &&
				isReservedProofControlPath(lexical, resolved, proofSessions) {
				diagnostic := diagnosticForToken(
					CodePlanLiveControlDenied,
					"coordinator argv targets a reserved ECI control artifact inside the active proof session",
					segmentIndex,
					argumentIndex,
					pathArgument,
					"use the matching coordinator lifecycle route for the reserved ECI control operation",
					"coordinator-proof-control",
				)
				diagnostic.Path = lexical
				return DecisionDeny, diagnostic
			}
			if request.Role == RoleWorker && writer &&
				isReservedProofControlPath(lexical, resolved, proofSessions) {
				diagnostic := diagnosticForToken(
					CodePlanLiveControlDenied,
					"worker argv targets a reserved ECI control artifact inside the active proof session",
					segmentIndex,
					argumentIndex,
					pathArgument,
					"route the reserved ECI control operation through the coordinator",
					"worker-proof-control",
				)
				diagnostic.Path = lexical
				return DecisionDeny, diagnostic
			}
			if writer {
				deferToProvider = true
				continue
			}
			if request.Role == RoleWorker && pathHasMissingComponent(lexical) &&
				isReservedProofControlPath(lexical, resolved, proofSessions) {
				deferToProvider = true
			}
			continue
		}
		if request.Role == RoleWorker && pathHasMissingComponent(lexical) {
			for _, proofSession := range proofSessions {
				if pathWithin(lexical, proofSession.lexical) &&
					isReservedProofControlPath(lexical, resolved, proofSessions) {
					deferToProvider = true
				}
			}
		}
		if containingSession == "" {
			if !anchorSearchComplete {
				deferToProvider = true
			}
			continue
		}
		if resolveErr != nil {
			deferToProvider = true
		}
		// A lexical proof alias does not change an ordinary resolved effect.
	}

	if deferToProvider {
		return DecisionDefer, nil
	}
	return DecisionAllow, nil
}

// inspectCurrentLedgerRedirect classifies one resolved output redirect against
// the selected proof session's ledger artifacts and their concrete inode.
//
// Example: an EOF append to current/high_level_log.jsonl is handled and allowed,
// while a sibling session's high_level_log.jsonl receives a foreign-session denial.
func inspectCurrentLedgerRedirect(
	redirect outputRedirect,
	selectedSession proofSession,
	lexical string,
	resolved string,
	resolveErr error,
	segmentIndex int,
	argumentIndex int,
) (bool, *Diagnostic) {
	selectedLexicalName := filepath.Base(selectedSession.lexical)
	if session, artifact, ok := ledgerSessionArtifact(lexical, filepath.Dir(selectedSession.lexical)); ok {
		switch {
		case session == selectedLexicalName && artifact == "high_level_log.anchor":
			diagnostic := diagnosticForToken(
				CodeLedgerAnchorWriteDenied,
				"selected-session ledger anchor is control state: target=high_level_log.anchor path="+lexical,
				segmentIndex,
				argumentIndex,
				redirect.target,
				"leave high_level_log.anchor for normal ledger reconciliation",
				"current-ledger-anchor",
			)
			diagnostic.Path = lexical
			return true, diagnostic
		case session != selectedLexicalName:
			diagnostic := diagnosticForToken(
				CodeLedgerForeignSessionDenied,
				"ledger redirect targets a sibling proof session: target_session="+session+" path="+lexical,
				segmentIndex,
				argumentIndex,
				redirect.target,
				"write only the selected session's high_level_log.jsonl",
				"foreign-ledger-session",
			)
			diagnostic.Path = lexical
			return true, diagnostic
		}
	}
	if resolveErr != nil {
		return false, nil
	}

	selectedResolvedName := filepath.Base(selectedSession.resolved)
	if session, artifact, ok := ledgerSessionArtifact(resolved, filepath.Dir(selectedSession.resolved)); ok {
		switch {
		case session == selectedResolvedName && artifact == "high_level_log.anchor":
			diagnostic := diagnosticForToken(
				CodeLedgerAnchorWriteDenied,
				"selected-session ledger anchor is control state: target=high_level_log.anchor path="+resolved,
				segmentIndex,
				argumentIndex,
				redirect.target,
				"leave high_level_log.anchor for normal ledger reconciliation",
				"current-ledger-anchor",
			)
			diagnostic.Path = lexical
			return true, diagnostic
		case session != selectedResolvedName:
			diagnostic := diagnosticForToken(
				CodeLedgerForeignSessionDenied,
				"ledger redirect targets a sibling proof session: target_session="+session+" path="+resolved,
				segmentIndex,
				argumentIndex,
				redirect.target,
				"write only the selected session's high_level_log.jsonl",
				"foreign-ledger-session",
			)
			diagnostic.Path = lexical
			return true, diagnostic
		}
	}

	selectedLog, err := resolvePathWithMissingSuffix(filepath.Join(selectedSession.lexical, "high_level_log.jsonl"))
	if err != nil {
		return false, nil
	}
	artifactSession, artifact, info, sameArtifact := ledgerArtifactByInode(selectedSession, resolved)
	if !sameArtifact {
		if resolved != selectedLog {
			return false, nil
		}
		artifactSession = selectedLexicalName
		artifact = "high_level_log.jsonl"
	}
	if artifactSession != selectedLexicalName {
		diagnostic := diagnosticForToken(
			CodeLedgerForeignSessionDenied,
			"ledger redirect targets a sibling proof session: target_session="+artifactSession+" path="+resolved,
			segmentIndex,
			argumentIndex,
			redirect.target,
			"write only the selected session's high_level_log.jsonl",
			"foreign-ledger-session",
		)
		diagnostic.Path = lexical
		return true, diagnostic
	}
	if artifact == "high_level_log.anchor" {
		diagnostic := diagnosticForToken(
			CodeLedgerAnchorWriteDenied,
			"selected-session ledger anchor is control state: target=high_level_log.anchor path="+resolved,
			segmentIndex,
			argumentIndex,
			redirect.target,
			"leave high_level_log.anchor for normal ledger reconciliation",
			"current-ledger-anchor",
		)
		diagnostic.Path = lexical
		return true, diagnostic
	}
	if artifact != "high_level_log.jsonl" {
		return false, nil
	}
	if redirect.effect != outputRedirectAppend {
		diagnostic := diagnosticForToken(
			CodeLedgerRewriteDenied,
			"selected-session ledger redirect would replace its target: effect="+string(redirect.effect)+" target="+resolved,
			segmentIndex,
			argumentIndex,
			redirect.target,
			"append at EOF to the selected session's high_level_log.jsonl",
			"current-ledger-rewrite",
		)
		diagnostic.Path = lexical
		return true, diagnostic
	}

	if info == nil {
		info, err = os.Stat(resolved)
	}
	if err != nil || info == nil || !info.Mode().IsRegular() {
		diagnostic := diagnosticForToken(
			CodeLedgerAppendOnly,
			"selected-session ledger append target is not a regular file: target="+resolved,
			segmentIndex,
			argumentIndex,
			redirect.target,
			"append only to the existing regular selected-session high_level_log.jsonl",
			"current-ledger-target",
		)
		diagnostic.Path = lexical
		return true, diagnostic
	}
	state, ok := info.Sys().(*syscall.Stat_t)
	if !ok || state.Nlink != 1 {
		linkCount := "unavailable"
		if ok {
			linkCount = fmt.Sprintf("%d", state.Nlink)
		}
		diagnostic := diagnosticForToken(
			CodeLedgerSharedInodeDenied,
			"selected-session ledger append target must have one link: target="+resolved+" nlink="+linkCount,
			segmentIndex,
			argumentIndex,
			redirect.target,
			"restore a uniquely linked selected-session high_level_log.jsonl before appending",
			"ledger-shared-inode",
		)
		diagnostic.Path = lexical
		return true, diagnostic
	}
	return true, nil
}

// ledgerArtifactByInode identifies a selected or sibling ledger artifact by
// concrete filesystem identity rather than by its caller-provided pathname.
//
// Example: an external hardlink to current/high_level_log.jsonl returns the
// selected session and high_level_log.jsonl artifact.
func ledgerArtifactByInode(selectedSession proofSession, target string) (string, string, fs.FileInfo, bool) {
	targetInfo, err := os.Stat(target)
	if err != nil {
		return "", "", nil, false
	}
	selectedName := filepath.Base(selectedSession.lexical)
	for _, artifact := range []string{"high_level_log.jsonl", "high_level_log.anchor"} {
		candidateInfo, candidateErr := os.Stat(filepath.Join(selectedSession.lexical, artifact))
		if candidateErr == nil && os.SameFile(targetInfo, candidateInfo) {
			return selectedName, artifact, targetInfo, true
		}
	}

	proofRoot := filepath.Dir(selectedSession.lexical)
	entries, err := os.ReadDir(proofRoot)
	if err != nil {
		return "", "", nil, false
	}
	for _, entry := range entries {
		if entry.Name() == selectedName || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		for _, artifact := range []string{"high_level_log.jsonl", "high_level_log.anchor"} {
			candidateInfo, candidateErr := os.Stat(filepath.Join(proofRoot, entry.Name(), artifact))
			if candidateErr == nil && os.SameFile(targetInfo, candidateInfo) {
				return entry.Name(), artifact, targetInfo, true
			}
		}
	}
	return "", "", nil, false
}

// ledgerSessionArtifact identifies a direct session ledger artifact under one
// proof root without treating nested or similarly named paths as ledger state.
//
// Example: proof/session/high_level_log.jsonl returns session and high_level_log.jsonl.
func ledgerSessionArtifact(path string, proofRoot string) (string, string, bool) {
	relative, err := filepath.Rel(proofRoot, path)
	if err != nil || relative == "." || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", false
	}
	parts := strings.Split(relative, string(filepath.Separator))
	if len(parts) != 2 {
		return "", "", false
	}
	switch parts[1] {
	case "high_level_log.jsonl", "high_level_log.anchor":
		return parts[0], parts[1], true
	default:
		return "", "", false
	}
}

func pathHasMissingComponent(path string) bool {
	current := filepath.Clean(path)
	missing := false
	for {
		_, err := os.Lstat(current)
		switch {
		case err == nil:
			return missing
		case !errors.Is(err, fs.ErrNotExist):
			return true
		}
		missing = true
		parent := filepath.Dir(current)
		if parent == current {
			return true
		}
		current = parent
	}
}

func pathHasResolvedProofAnchor(path, resolvedProofRoot string) (bool, bool) {
	current := filepath.Clean(path)
	for range maxProofAnchors {
		resolved, err := resolvePathWithMissingSuffix(current)
		if err == nil && pathWithin(resolved, resolvedProofRoot) {
			return true, true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false, true
		}
		current = parent
	}
	return false, false
}

func pathOperand(argument token) (token, bool) {
	value := argument.value
	switch {
	case value == "":
		return token{}, false
	case value == "--":
		return token{}, false
	case strings.HasPrefix(value, "-"):
		separator := strings.IndexByte(value, '=')
		if separator < 0 || separator == len(value)-1 {
			return token{}, false
		}
		return token{
			value:  value[separator+1:],
			offset: argument.offset + separator + 1,
			quoted: argument.quoted,
		}, true
	default:
		return argument, true
	}
}

// commandPathOperand exposes command-specific key/value path operands to the
// shared ownership checks.  dd expresses its output destination as of=PATH,
// which is still a path operand even though it is not a positional argv path.
// Keep this normalization tied to the command shape rather than treating every
// arbitrary key/value argument as a filesystem path.
func commandPathOperand(command string, argument token) (token, bool) {
	if filepath.Base(command) == "dd" && strings.HasPrefix(argument.value, "of=") {
		value := strings.TrimPrefix(argument.value, "of=")
		if value == "" {
			return token{}, false
		}
		return token{
			value:  value,
			offset: argument.offset + len("of="),
			quoted: argument.quoted,
		}, true
	}
	return pathOperand(argument)
}

// outputDestinationOperand selects only command output options, retaining the
// original token coordinates while skipping scalar shell input operands.
//
// Example: sort -o < input output selects output's original argv index.
func outputDestinationOperand(
	argv []token,
	argumentIndex int,
) (token, int, bool) {
	if argumentIndex <= 0 || argumentIndex >= len(argv) ||
		argv[argumentIndex].inputRedirectTarget || isGitArchiveCommand(argv) {
		return token{}, argumentIndex, false
	}
	for _, previous := range argv[1:argumentIndex] {
		if !previous.inputRedirectTarget && previous.value == "--" {
			return token{}, argumentIndex, false
		}
	}
	argument := argv[argumentIndex]
	value := argument.value
	var options []string
	switch filepath.Base(argv[0].value) {
	case "sort":
		options = []string{"--output", "-o"}
	case "gitleaks":
		options = []string{"--report-path"}
	default:
		return token{}, argumentIndex, false
	}
	separateValue := false
	suffixOffset := 0
	for _, option := range options {
		if value == option {
			separateValue = true
			break
		}
		if strings.HasPrefix(value, option+"=") {
			suffixOffset = len(option) + 1
			break
		}
	}
	if separateValue {
		for index := argumentIndex + 1; index < len(argv); index++ {
			if argv[index].inputRedirectTarget {
				continue
			}
			path := argv[index]
			if path.value == "" || strings.HasPrefix(path.value, "-") {
				break
			}
			return path, index, true
		}
		return token{}, argumentIndex, false
	}
	if filepath.Base(argv[0].value) == "sort" && suffixOffset == 0 && strings.HasPrefix(value, "-o") &&
		len(value) > len("-o") && !strings.HasPrefix(value, "--") {
		suffixOffset = len("-o")
	}
	if suffixOffset == 0 {
		return token{}, argumentIndex, false
	}
	path := value[suffixOffset:]
	if path == "" || strings.HasPrefix(path, "-") {
		return token{}, argumentIndex, false
	}
	offset := argument.offset + suffixOffset
	if suffixOffset < len(argument.sourceOffsets) {
		offset = argument.sourceOffsets[suffixOffset]
	}
	return token{value: path, offset: offset, quoted: argument.quoted}, argumentIndex, true
}

func isGitArchiveCommand(argv []token) bool {
	if len(argv) == 0 || filepath.Base(argv[0].value) != "git" {
		return false
	}
	index := gitSubcommandIndex(argv)
	return index < len(argv) && argv[index].value == "archive"
}

// isReservedProofControlPath identifies a reserved basename within an active
// proof session, using the resolved destination when available.
//
// Example: an alias to an ordinary file does not inherit a control basename.
func isReservedProofControlPath(
	lexical string,
	resolved string,
	sessions []proofSession,
) bool {
	path := resolved
	if path == "" {
		path = lexical
	}
	base := filepath.Base(path)
	reserved := false
	for _, name := range eciControlBasenames {
		if base == name || strings.HasPrefix(base, name+".") {
			reserved = true
			break
		}
	}
	if !reserved {
		return false
	}
	for _, session := range sessions {
		if (resolved == "" && pathWithin(path, session.lexical)) || pathWithin(path, session.resolved) {
			return true
		}
	}
	return false
}

// isAppendOnlyLedgerPath identifies a ledger destination within an active
// proof session, using lexical membership only when no resolved path is given.
//
// Example: an indexed ledger path keeps its identity under a symlinked proof root.
func isAppendOnlyLedgerPath(
	lexical string,
	resolved string,
	sessions []proofSession,
) bool {
	path := resolved
	if path == "" {
		path = lexical
	}
	base := filepath.Base(path)
	if base != "high_level_log.jsonl" && base != "high_level_log.anchor" {
		return false
	}
	for _, session := range sessions {
		if (resolved == "" && pathWithin(path, session.lexical)) || pathWithin(path, session.resolved) {
			return true
		}
	}
	return false
}

// hookUnlinksDirectoryEntry reports operations whose targets are directory entries.
//
// Example: rm of a symlink removes the link; tee writes through its referent.
func hookUnlinksDirectoryEntry(argv []token) bool {
	if len(argv) == 0 {
		return false
	}
	switch filepath.Base(argv[0].value) {
	case "rm", "mv", "rmdir", "unlink":
		return true
	default:
		return false
	}
}

// resolvePathEntryWithMissingSuffix resolves parents while retaining the last entry.
//
// Example: removing /task/link resolves /task, leaving link as the unlink target.
func resolvePathEntryWithMissingSuffix(path string) (string, error) {
	parent, err := resolvePathWithMissingSuffix(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}

func resolvePathWithMissingSuffix(path string) (string, error) {
	current := filepath.Clean(path)
	missing := make([]string, 0)
	for {
		resolved, err := filepath.EvalSymlinks(current)
		switch {
		case err == nil:
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		case !errors.Is(err, fs.ErrNotExist):
			return "", err
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func pathWithin(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// unwrappedCommand records the literal child argv and every environment
// wrapper encountered while removing transparent launch wrappers.
//
// Example: command env NAME=value tool produces the tool argv with
// hasEnvironment set, so the caller cannot fast-admit the altered context.
type unwrappedCommand struct {
	argv                  []token
	hasEnvironment        bool
	hasTransparentWrapper bool
}

// unwrap returns the literal child argv selected by supported transparent
// wrappers while discarding context metadata for existing callers.
//
// Example: env NAME=value cat file returns the cat argv.
func unwrap(argv []token, segmentIndex int) ([]token, *Diagnostic) {
	result, diagnostic := unwrapWithMetadata(argv, segmentIndex, nil)
	if diagnostic != nil {
		return nil, diagnostic
	}
	return result.argv, nil
}

// unwrapWithMetadata returns the literal child argv and records whether any
// transparent wrapper changed the child environment before it starts.
//
// Example: exec env NAME=value tool returns tool with hasEnvironment set.
func unwrapWithMetadata(
	argv []token,
	segmentIndex int,
	timeoutLaunches []TimeoutLaunch,
) (unwrappedCommand, *Diagnostic) {
	current := argv
	hasEnvironment := false
	hasTransparentWrapper := false
	for depth := 0; depth < maxWrapperDepth; depth++ {
		name := filepath.Base(current[0].value)
		hasTransparentWrapper = hasTransparentWrapper || isTransparentWrapperName(name)
		switch name {
		case "env":
			hasEnvironment = true
			child, diagnostic := unwrapEnv(current, segmentIndex)
			if diagnostic != nil {
				return unwrappedCommand{}, diagnostic
			}
			if len(child) == 0 {
				// env can legally inspect or construct its child command. Its
				// option spelling alone does not identify a harmful target.
				return unwrappedCommand{
					argv:                  current,
					hasEnvironment:        hasEnvironment,
					hasTransparentWrapper: hasTransparentWrapper,
				}, nil
			}
			current = child
		case "command":
			child, diagnostic := unwrapSimpleOptions(current, segmentIndex, map[string]int{"-p": 0, "--": 0})
			if diagnostic != nil {
				return unwrappedCommand{}, diagnostic
			}
			current = child
		case "exec":
			child, diagnostic := unwrapSimpleOptions(current, segmentIndex, map[string]int{"-a": 1, "-c": 0, "-l": 0, "--": 0})
			if diagnostic != nil {
				return unwrappedCommand{}, diagnostic
			}
			current = child
		case "nice":
			child, diagnostic := unwrapNice(current, segmentIndex)
			if diagnostic != nil {
				return unwrappedCommand{}, diagnostic
			}
			current = child
		case "timeout":
			child, diagnostic := unwrapTimeout(current, segmentIndex, timeoutLaunches)
			if diagnostic != nil {
				return unwrappedCommand{}, diagnostic
			}
			if len(child) == 0 {
				return unwrappedCommand{
					argv:                  current,
					hasEnvironment:        hasEnvironment,
					hasTransparentWrapper: hasTransparentWrapper,
				}, nil
			}
			current = child
		case "time":
			child, diagnostic := unwrapTime(current, segmentIndex)
			if diagnostic != nil {
				return unwrappedCommand{}, diagnostic
			}
			current = child
		case "prlimit":
			child, diagnostic := unwrapPrlimit(current, segmentIndex)
			if diagnostic != nil {
				return unwrappedCommand{}, diagnostic
			}
			current = child
		case "chronic":
			child, diagnostic := unwrapSimpleOptions(current, segmentIndex, map[string]int{
				"-e": 0, "-f": 0, "-v": 0, "-d": 0, "-s": 0, "--": 0,
			})
			if diagnostic != nil {
				return unwrappedCommand{}, diagnostic
			}
			current = child
		case "nohup":
			child, diagnostic := unwrapSimpleOptions(current, segmentIndex, map[string]int{"--": 0})
			if diagnostic != nil {
				return unwrappedCommand{}, diagnostic
			}
			current = child
		case "setsid":
			child, diagnostic := unwrapSimpleOptions(current, segmentIndex, map[string]int{
				"-c": 0, "-f": 0, "-w": 0, "--wait": 0, "--fork": 0, "--ctty": 0, "--": 0,
			})
			if diagnostic != nil {
				return unwrappedCommand{}, diagnostic
			}
			current = child
		case "sudo":
			child, diagnostic := unwrapSudo(current, segmentIndex)
			if diagnostic != nil {
				return unwrappedCommand{}, diagnostic
			}
			current = child
		case "doas":
			child, diagnostic := unwrapDoas(current, segmentIndex)
			if diagnostic != nil {
				return unwrappedCommand{}, diagnostic
			}
			current = child
		case "systemd-run":
			child, diagnostic := unwrapSystemdRun(current, segmentIndex)
			if diagnostic != nil {
				return unwrappedCommand{}, diagnostic
			}
			current = child
		case "stdbuf":
			child, diagnostic := unwrapStdbuf(current, segmentIndex)
			if diagnostic != nil {
				return unwrappedCommand{}, diagnostic
			}
			current = child
		case "busybox":
			child, diagnostic := unwrapBusybox(current, segmentIndex)
			if diagnostic != nil {
				return unwrappedCommand{}, diagnostic
			}
			current = child
		default:
			return unwrappedCommand{
				argv:                  current,
				hasEnvironment:        hasEnvironment,
				hasTransparentWrapper: hasTransparentWrapper,
			}, nil
		}
	}

	return unwrappedCommand{}, diagnosticForToken(
		CodePlanWrapperDenied,
		fmt.Sprintf("transparent wrapper depth exceeds %d", maxWrapperDepth),
		segmentIndex,
		0,
		current[0],
		"remove redundant wrappers and invoke one finite child argv",
		"wrapper-depth-limit",
	)
}

// isTransparentWrapperName reports whether name is unwrapped before the
// planner examines the literal child argv.
//
// Example: stdbuf is transparent because its child executable remains visible.
func isTransparentWrapperName(name string) bool {
	switch name {
	case "busybox", "chronic", "command", "doas", "env", "exec", "nice", "nohup", "prlimit", "setsid", "stdbuf", "sudo", "systemd-run", "time", "timeout":
		return true
	default:
		return false
	}
}

func unwrapSudo(argv []token, segmentIndex int) ([]token, *Diagnostic) {
	return unwrapKnownOptions(argv, segmentIndex, map[string]int{
		"-u": 1, "--user": 1, "-g": 1, "--group": 1, "-h": 1, "--host": 1,
		"-p": 1, "--prompt": 1, "-r": 1, "--role": 1, "-t": 1, "--type": 1,
		"-T": 1, "--command-timeout": 1, "-C": 1, "--chdir": 1, "-D": 1,
		"--": 0,
	}, map[string]struct{}{
		"-A": {}, "--askpass": {}, "-b": {}, "--background": {}, "-E": {}, "--preserve-env": {},
		"-H": {}, "--set-home": {}, "-i": {}, "--login": {}, "-K": {}, "--remove-timestamp": {},
		"-k": {}, "--reset-timestamp": {}, "-n": {}, "--non-interactive": {}, "-P": {},
		"--preserve-groups": {}, "-S": {}, "--stdin": {}, "-V": {}, "--version": {},
	}, "sudo")
}

func unwrapDoas(argv []token, segmentIndex int) ([]token, *Diagnostic) {
	return unwrapKnownOptions(argv, segmentIndex, map[string]int{
		"-a": 1, "--auth-type": 1, "-C": 1, "--config": 1, "-u": 1, "--user": 1, "--": 0,
	}, map[string]struct{}{
		"-n": {}, "--non-interactive": {},
	}, "doas")
}

func unwrapSystemdRun(argv []token, segmentIndex int) ([]token, *Diagnostic) {
	return unwrapKnownOptions(argv, segmentIndex, map[string]int{
		"--unit": 1, "-p": 1, "--property": 1, "--working-directory": 1, "--service-type": 1,
		"--uid": 1, "--gid": 1, "--slice": 1, "--machine": 1, "--pipe": 0, "--pty": 0,
		"--quiet": 0, "--wait": 0, "--collect": 0, "--user": 0, "--system": 0, "--scope": 0,
		"--service": 0, "--remain-after-exit": 0, "--send-sighup": 0, "--same-dir": 0,
		"--working-directory=": 0, "--unit=": 0, "--property=": 0, "--": 0,
	}, nil, "systemd-run")
}

// unwrapStdbuf returns the finite child argv selected by the stdbuf launcher.
//
// Example: stdbuf -oL chmod 644 hooks/validate-bash.sh selects chmod as the child.
func unwrapStdbuf(argv []token, segmentIndex int) ([]token, *Diagnostic) {
	index := 1
	for index < len(argv) {
		value := argv[index].value
		switch {
		case value == "--":
			index++
			if index >= len(argv) {
				return nil, malformedWrapperDiagnostic(argv[index-1], segmentIndex, index-1)
			}
			return argv[index:], nil
		case value == "-i", value == "-o", value == "-e", value == "--input", value == "--output", value == "--error":
			if index+1 >= len(argv) {
				return nil, malformedWrapperDiagnostic(argv[index], segmentIndex, index)
			}
			index += 2
		case (strings.HasPrefix(value, "-i") || strings.HasPrefix(value, "-o") || strings.HasPrefix(value, "-e")) && len(value) > 2:
			index++
		case strings.HasPrefix(value, "--input="), strings.HasPrefix(value, "--output="), strings.HasPrefix(value, "--error="):
			index++
		case strings.HasPrefix(value, "-"):
			return nil, diagnosticForToken(
				CodePlanWrapperDenied,
				fmt.Sprintf("stdbuf has unsupported option %s before its child argv", value),
				segmentIndex,
				index,
				argv[index],
				"remove unsupported stdbuf option and provide one literal child argv",
				"unsupported-transparent-wrapper-option",
			)
		default:
			return argv[index:], nil
		}
	}
	return nil, malformedWrapperDiagnostic(argv[0], segmentIndex, 0)
}

// unwrapBusybox returns the BusyBox applet argv after its optional separator.
//
// Example: busybox -- chmod 644 hooks/validate-bash.sh selects chmod as the applet.
func unwrapBusybox(argv []token, segmentIndex int) ([]token, *Diagnostic) {
	index := 1
	if index < len(argv) && argv[index].value == "--" {
		index++
	}
	if index >= len(argv) {
		return nil, malformedWrapperDiagnostic(argv[0], segmentIndex, 0)
	}
	return argv[index:], nil
}

func unwrapKnownOptions(
	argv []token,
	segmentIndex int,
	arguments map[string]int,
	flags map[string]struct{},
	name string,
) ([]token, *Diagnostic) {
	index := 1
	for index < len(argv) {
		value := argv[index].value
		if value == "--" {
			index++
			break
		}
		if count, ok := arguments[value]; ok {
			if index+count >= len(argv) {
				return nil, malformedWrapperDiagnostic(argv[index], segmentIndex, index)
			}
			index += count + 1
			continue
		}
		if _, ok := flags[value]; ok {
			index++
			continue
		}
		matchedAttached := false
		for option := range arguments {
			if strings.HasSuffix(option, "=") && strings.HasPrefix(value, option) && len(value) > len(option) {
				matchedAttached = true
				break
			}
		}
		if matchedAttached {
			index++
			continue
		}
		if strings.HasPrefix(value, "-") {
			return nil, diagnosticForToken(
				CodePlanWrapperDenied,
				fmt.Sprintf("%s has unsupported option %s before its child argv", name, value),
				segmentIndex,
				index,
				argv[index],
				fmt.Sprintf("remove unsupported %s option and provide one literal child argv", name),
				"unsupported-transparent-wrapper-option",
			)
		}
		break
	}
	if index >= len(argv) {
		return nil, malformedWrapperDiagnostic(argv[0], segmentIndex, 0)
	}
	return argv[index:], nil
}

func unwrapEnv(argv []token, segmentIndex int) ([]token, *Diagnostic) {
	index := 1
	optionsEnded := false
	for index < len(argv) && !optionsEnded {
		value := argv[index].value
		switch {
		case value == "--":
			optionsEnded = true
			index++
		case value == "-i", value == "--ignore-environment":
			index++
		case value == "-S", value == "--split-string", strings.HasPrefix(value, "--split-string="):
			return nil, nil
		case value == "-u", value == "--unset", value == "-C", value == "--chdir":
			if index+1 >= len(argv) {
				return nil, nil
			}
			index += 2
		case strings.HasPrefix(value, "--unset="):
			index++
		case strings.HasPrefix(value, "--chdir="):
			index++
		case strings.HasPrefix(value, "-"):
			return nil, nil
		default:
			optionsEnded = true
		}
	}

	for index < len(argv) {
		name := environmentAssignmentName(argv[index])
		if name == "" {
			break
		}
		index++
	}
	if index >= len(argv) {
		return nil, nil
	}

	return argv[index:], nil
}

func unwrapSimpleOptions(
	argv []token,
	segmentIndex int,
	options map[string]int,
) ([]token, *Diagnostic) {
	index := 1
	for index < len(argv) {
		argumentCount, exists := options[argv[index].value]
		if !exists {
			break
		}
		if index+argumentCount >= len(argv) {
			return nil, malformedWrapperDiagnostic(argv[index], segmentIndex, index)
		}
		index += argumentCount + 1
	}
	if index >= len(argv) {
		return nil, malformedWrapperDiagnostic(argv[0], segmentIndex, 0)
	}
	return argv[index:], nil
}

func unwrapNice(argv []token, segmentIndex int) ([]token, *Diagnostic) {
	index := 1
	for index < len(argv) {
		switch {
		case argv[index].value == "--":
			index++
			if index >= len(argv) {
				return nil, malformedWrapperDiagnostic(argv[index-1], segmentIndex, index-1)
			}
			return argv[index:], nil
		case argv[index].value == "-n", argv[index].value == "--adjustment":
			if index+1 >= len(argv) {
				return nil, malformedWrapperDiagnostic(argv[index], segmentIndex, index)
			}
			index += 2
		case strings.HasPrefix(argv[index].value, "--adjustment="):
			index++
		default:
			return argv[index:], nil
		}
	}
	return nil, malformedWrapperDiagnostic(argv[0], segmentIndex, 0)
}

// unwrapTimeout returns timeout's child only when the exact prefix has a
// matching observed-launch fact; every other timeout form stays opaque.
//
// Example: a recorded `timeout --signal TERM 5` prefix exposes its child,
// while the same literal prefix without a child acknowledgement remains opaque.
func unwrapTimeout(
	argv []token,
	segmentIndex int,
	timeoutLaunches []TimeoutLaunch,
) ([]token, *Diagnostic) {
	prefix, child, ok := timeoutPrefix(argv)
	if !ok || !timeoutLaunchMatches(timeoutLaunches, segmentIndex, prefix) {
		return nil, nil
	}
	return child, nil
}

// timeoutLaunchMatches reports whether one planner fact has the same segment
// and literal prefix as the timeout argv currently being unwrapped.
//
// Example: a segment-one fact for ["timeout", "5"] does not match a
// segment-two timeout or a different duration.
func timeoutLaunchMatches(
	launches []TimeoutLaunch,
	segmentIndex int,
	prefix []token,
) bool {
	for _, launch := range launches {
		if launch.Segment != segmentIndex || len(launch.Prefix) != len(prefix) {
			continue
		}
		matched := true
		for index, argument := range prefix {
			if launch.Prefix[index] != argument.value {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// timeoutPrefix returns the literal timeout prefix through its duration and
// the original child argv only for a structurally complete direct timeout
// invocation. Signal validity remains runtime behavior for the probe.
//
// Example: `timeout --signal TERM 5 git status` returns the four-token prefix
// and `git status` child, while an unknown option returns no prefix.
func timeoutPrefix(argv []token) ([]token, []token, bool) {
	index := 1
	for index < len(argv) {
		value := argv[index].value
		if value == "--" {
			index++
			break
		}
		if !strings.HasPrefix(value, "-") || value == "-" {
			break
		}
		switch {
		case value == "--preserve-status", value == "--foreground", value == "--verbose":
			index++
			continue
		case value == "--kill-after", value == "--signal":
			if index+1 >= len(argv) {
				return nil, nil, false
			}
			if value == "--kill-after" && !isTimeoutDuration(argv[index+1].value) {
				return nil, nil, false
			}
			if value == "--signal" && argv[index+1].value == "" {
				return nil, nil, false
			}
			index += 2
			continue
		case strings.HasPrefix(value, "--kill-after="):
			duration := strings.TrimPrefix(value, "--kill-after=")
			if !isTimeoutDuration(duration) {
				return nil, nil, false
			}
			index++
			continue
		case strings.HasPrefix(value, "--signal="):
			signal := strings.TrimPrefix(value, "--signal=")
			if signal == "" {
				return nil, nil, false
			}
			index++
			continue
		case strings.HasPrefix(value, "--"):
			return nil, nil, false
		default:
			next, ok := timeoutPrefixShortOptions(argv, index)
			if !ok {
				return nil, nil, false
			}
			index = next
			continue
		}
	}

	if index >= len(argv) || !isTimeoutDuration(argv[index].value) {
		return nil, nil, false
	}
	index++
	if index >= len(argv) {
		return nil, nil, false
	}
	return argv[:index], argv[index:], true
}

// timeoutPrefixShortOptions identifies GNU-compatible short option boundaries
// without maintaining a platform signal vocabulary.
//
// Example: -pfk1s advances over preserve-status, foreground, and kill-after.
func timeoutPrefixShortOptions(argv []token, index int) (int, bool) {
	options := argv[index].value[1:]
	for len(options) > 0 {
		option := options[0]
		options = options[1:]
		switch option {
		case 'p', 'f', 'v':
			continue
		case 'k', 's':
			argument := options
			if strings.HasPrefix(argument, "=") {
				argument = argument[1:]
			}
			if argument == "" {
				if index+1 >= len(argv) {
					return 0, false
				}
				argument = argv[index+1].value
				index++
			}
			if option == 'k' && !isTimeoutDuration(argument) {
				return 0, false
			}
			if option == 's' && argument == "" {
				return 0, false
			}
			return index + 1, true
		default:
			return 0, false
		}
	}
	return index + 1, true
}

// isTimeoutDuration reports whether value is a nonnegative GNU-compatible
// numeric duration with an optional second, minute, hour, or day suffix.
//
// Example: .5s and 1e2m are valid, while not-a-duration and 5x are not.
func isTimeoutDuration(value string) bool {
	if value == "" {
		return false
	}
	if value[0] == '+' {
		value = value[1:]
		if value == "" {
			return false
		}
	}
	last := value[len(value)-1]
	switch last {
	case 's', 'm', 'h', 'd':
		value = value[:len(value)-1]
	}
	if value == "" {
		return false
	}

	index := 0
	digitCount := 0
	dotSeen := false
	for index < len(value) {
		character := value[index]
		if character >= '0' && character <= '9' {
			digitCount++
			index++
			continue
		}
		if character == '.' && !dotSeen {
			dotSeen = true
			index++
			continue
		}
		break
	}

	if digitCount == 0 {
		return false
	}
	if index == len(value) {
		return true
	}
	if value[index] != 'e' && value[index] != 'E' {
		return false
	}
	index++
	if index < len(value) && (value[index] == '+' || value[index] == '-') {
		index++
	}
	exponentDigits := 0
	for index < len(value) {
		character := value[index]
		if character < '0' || character > '9' {
			return false
		}
		exponentDigits++
		index++
	}
	return exponentDigits > 0
}

func unwrapTime(argv []token, segmentIndex int) ([]token, *Diagnostic) {
	return unwrapKnownOptions(argv, segmentIndex, map[string]int{
		"-f": 1, "--format": 1, "-o": 1, "--output": 1, "--": 0,
	}, map[string]struct{}{
		"--append": {}, "--verbose": {},
	}, "time")
}

func unwrapPrlimit(argv []token, segmentIndex int) ([]token, *Diagnostic) {
	return unwrapKnownOptions(argv, segmentIndex, map[string]int{
		"--pid": 1, "--output": 1, "--": 0,
		"--as=": 0, "--core=": 0, "--cpu=": 0, "--data=": 0,
		"--fsize=": 0, "--locks=": 0, "--memlock=": 0, "--msgqueue=": 0,
		"--nice=": 0, "--nofile=": 0, "--nproc=": 0, "--rss=": 0,
		"--rtprio=": 0, "--rttime=": 0, "--sigpending=": 0, "--stack=": 0,
	}, map[string]struct{}{
		"--verbose": {}, "--noheadings": {}, "--raw": {},
	}, "prlimit")
}

// inspectPrintenv leaves environment inspection to the invoked utility. Its
// option or query spelling does not resolve a cross-scope or destructive target.
//
// Example: both `printenv` and `printenv -- PATH` remain ordinary commands.
func inspectPrintenv(_ []token, _ int) *Diagnostic {
	return nil
}

// statFormatDirectives is the finite GNU stat metadata vocabulary accepted by
// the planner.  These directives describe file metadata; they do not select
// another command, read an input program, or change the inspected path.
var statFormatDirectives = map[byte]struct{}{
	'a': {}, 'A': {}, 'b': {}, 'B': {}, 'C': {}, 'd': {}, 'D': {},
	'f': {}, 'F': {}, 'g': {}, 'G': {}, 'h': {}, 'i': {}, 'm': {},
	'n': {}, 'N': {}, 'o': {}, 's': {}, 't': {}, 'T': {}, 'u': {},
	'U': {}, 'w': {}, 'W': {}, 'x': {}, 'X': {}, 'y': {}, 'Y': {},
	'z': {}, 'Z': {},
}

const maxStatFormatBytes = 256

// inspectStat keeps stat's read-only route finite and language-neutral.  The
// legacy provider adapters historically accepted a short list of exact
// formats, which meant the compiled planner's ordinary status-0 fast path
// could accidentally admit arbitrary format strings.  Accept a bounded
// metadata vocabulary instead: literal separators and known directives are
// fine, while shell markers, width/precision modifiers, and unknown escapes
// receive a coordinate-specific diagnostic.
func inspectStat(argv []token, segmentIndex int) *Diagnostic {
	if len(argv) < 2 {
		return statDiagnostic(
			"stat requires one -c/--format metadata expression and at least one literal path",
			segmentIndex,
			0,
			argv[0],
			"stat-format",
			"use -c or --format with a finite metadata expression and one to sixteen literal paths",
		)
	}

	formatSeen := false
	delimiterSeen := false
	paths := 0
	for index := 1; index < len(argv); {
		argument := argv[index]
		value := argument.value
		switch {
		case value == "--":
			if delimiterSeen {
				return statDiagnostic(
					"stat uses the path delimiter more than once",
					segmentIndex,
					index,
					argument,
					"stat-option",
					"use one -- delimiter before the finite literal path operands",
				)
			}
			delimiterSeen = true
			index++
		case value == "-L" || value == "--dereference":
			index++
		case value == "-c" || value == "--format" || value == "-Lc" || value == "-cL":
			if formatSeen {
				return statDiagnostic(
					"stat specifies more than one metadata format",
					segmentIndex,
					index,
					argument,
					"stat-option",
					"specify exactly one -c or --format option",
				)
			}
			if index+1 >= len(argv) {
				return statDiagnostic(
					"stat format option has no metadata expression",
					segmentIndex,
					index,
					argument,
					"stat-format",
					"supply one finite literal metadata expression after the format option",
				)
			}
			formatSeen = true
			if diagnostic := inspectStatFormat(argv[index+1], segmentIndex, index+1); diagnostic != nil {
				return diagnostic
			}
			index += 2
		case strings.HasPrefix(value, "--format="):
			if formatSeen {
				return statDiagnostic(
					"stat specifies more than one metadata format",
					segmentIndex,
					index,
					argument,
					"stat-option",
					"specify exactly one -c or --format option",
				)
			}
			formatSeen = true
			format := token{
				value:  strings.TrimPrefix(value, "--format="),
				offset: argument.offset + len("--format="),
				quoted: argument.quoted,
			}
			if diagnostic := inspectStatFormat(format, segmentIndex, index); diagnostic != nil {
				return diagnostic
			}
			index++
		case strings.HasPrefix(value, "-") && !delimiterSeen:
			return statDiagnostic(
				fmt.Sprintf("stat option %s is outside the bounded metadata inspection grammar", value),
				segmentIndex,
				index,
				argument,
				"stat-option",
				"use only -L/--dereference, one -c/--format option, and literal paths",
			)
		default:
			if !statPathLiteralSafe(value) {
				return statDiagnostic(
					fmt.Sprintf("stat path operand %s is not a safe literal path", value),
					segmentIndex,
					index,
					argument,
					"stat-path",
					"pass literal paths without shell expansion, globbing, or control syntax",
				)
			}
			paths++
			if paths > 16 {
				return statDiagnostic(
					"stat path operand count exceeds sixteen",
					segmentIndex,
					index,
					argument,
					"stat-path-limit",
					"inspect at most sixteen literal paths per command",
				)
			}
			index++
		}
	}
	if !formatSeen {
		return statDiagnostic(
			"stat requires an explicit metadata format",
			segmentIndex,
			0,
			argv[0],
			"stat-format",
			"use -c or --format with a finite metadata expression",
		)
	}
	if paths == 0 {
		return statDiagnostic(
			"stat format is present but no literal path operand was supplied",
			segmentIndex,
			len(argv)-1,
			argv[len(argv)-1],
			"stat-path",
			"supply one to sixteen literal paths after the metadata format",
		)
	}
	return nil
}

func inspectStatFormat(argument token, segmentIndex, argvIndex int) *Diagnostic {
	value := argument.value
	if value == "" || len(value) > maxStatFormatBytes {
		return statDiagnostic(
			"stat metadata format is empty or exceeds the bounded format length",
			segmentIndex,
			argvIndex,
			argument,
			"stat-format",
			"use a non-empty metadata format no longer than 256 bytes",
		)
	}

	directiveSeen := false
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character == '%' {
			if index+1 >= len(value) {
				return statDiagnostic(
					"stat metadata format ends with an incomplete percent directive",
					segmentIndex,
					argvIndex,
					argument,
					"stat-format-directive",
					"use %% or one documented single-character stat metadata directive",
				)
			}
			directive := value[index+1]
			if directive == '%' {
				index++
				continue
			}
			if _, ok := statFormatDirectives[directive]; !ok {
				return statDiagnostic(
					fmt.Sprintf("stat metadata directive %%%c is outside the bounded vocabulary", directive),
					segmentIndex,
					argvIndex,
					argument,
					"stat-format-directive",
					"use one finite documented stat metadata directive without width, precision, or nested expansion",
				)
			}
			directiveSeen = true
			index++
			continue
		}
		if character < 0x20 || strings.ContainsRune("$`\\;|&<>(){}[]*?", rune(character)) {
			predicate := "stat-format-syntax"
			reason := "stat metadata format contains shell syntax or a control byte"
			if character == '$' || character == '`' {
				predicate = "stat-format-dynamic"
				reason = "stat metadata format contains shell expansion syntax"
			}
			return statDiagnostic(
				reason,
				segmentIndex,
				argvIndex,
				argument,
				predicate,
				"pass one literal metadata format without shell expansion, control bytes, or nested syntax",
			)
		}
	}
	if !directiveSeen {
		return statDiagnostic(
			"stat metadata format contains no metadata directive",
			segmentIndex,
			argvIndex,
			argument,
			"stat-format",
			"include at least one documented stat metadata directive such as %s, %n, %y, or %x",
		)
	}
	return nil
}

func statPathLiteralSafe(value string) bool {
	if value == "" || len(value) > 4096 || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "~") {
		return false
	}
	if strings.ContainsAny(value, "$`\\\n\r*?[](){}<>|;&") {
		return false
	}
	return true
}

func statDiagnostic(reason string, segmentIndex, argvIndex int, argument token, predicate, remediation string) *Diagnostic {
	return diagnosticForToken(
		CodePlanStatFormatDenied,
		reason,
		segmentIndex,
		argvIndex,
		argument,
		remediation,
		predicate,
	)
}

// inspectFile permits only a finite inspection subset of file.  In
// particular, -C/--compile writes a compiled .mgc file next to the selected
// magic database and must never inherit generic read-only admission.
//
// Example: file -m magic source is an inspection, while file -C -m magic is
// denied before it can compile magic.mgc.
func inspectFile(argv []token, segmentIndex int) *Diagnostic {
	if len(argv) < 2 {
		return fileDiagnostic(
			"file requires at least one literal inspection path",
			segmentIndex,
			0,
			argv[0],
			"file-path",
			"pass one to sixteen literal paths to file",
		)
	}

	options := true
	paths := 0
	magicSeen := false
	for index := 1; index < len(argv); {
		argument := argv[index]
		value := argument.value
		switch {
		case options && value == "--":
			options = false
			index++
		case options && (value == "-C" || value == "--compile" || strings.HasPrefix(value, "--compile=") ||
			(strings.HasPrefix(value, "-") && !strings.HasPrefix(value, "--") && strings.Contains(value[1:], "C"))):
			return fileDiagnostic(
				fmt.Sprintf("file option %s compiles a magic database", value),
				segmentIndex,
				index,
				argument,
				"file-compile",
				"remove -C/--compile and inspect an existing literal file instead",
			)
		case options && (value == "-m" || value == "--magic-file"):
			if magicSeen || index+1 >= len(argv) || !statPathLiteralSafe(argv[index+1].value) {
				return fileDiagnostic(
					"file magic-file option requires one literal path",
					segmentIndex,
					index,
					argument,
					"file-magic-path",
					"use one -m/--magic-file argument with a finite literal magic path",
				)
			}
			magicSeen = true
			index += 2
		case options && strings.HasPrefix(value, "--magic-file="):
			magicPath := strings.TrimPrefix(value, "--magic-file=")
			if magicSeen || !statPathLiteralSafe(magicPath) {
				return fileDiagnostic(
					"file --magic-file requires one literal path",
					segmentIndex,
					index,
					argument,
					"file-magic-path",
					"use one --magic-file=PATH option with a finite literal magic path",
				)
			}
			magicSeen = true
			index++
		case options && isReadOnlyFileOption(value):
			index++
		case options && strings.HasPrefix(value, "-"):
			return fileDiagnostic(
				fmt.Sprintf("file option %s is outside the bounded inspection grammar", value),
				segmentIndex,
				index,
				argument,
				"file-option",
				"use only bounded file inspection options and literal paths; do not compile a magic database",
			)
		default:
			if !statPathLiteralSafe(value) {
				return fileDiagnostic(
					fmt.Sprintf("file path operand %s is not a safe literal path", value),
					segmentIndex,
					index,
					argument,
					"file-path",
					"pass literal paths without shell expansion, globbing, or control syntax",
				)
			}
			options = false
			paths++
			if paths > 16 {
				return fileDiagnostic(
					"file path operand count exceeds sixteen",
					segmentIndex,
					index,
					argument,
					"file-path-limit",
					"inspect at most sixteen literal paths per command",
				)
			}
			index++
		}
	}
	if paths == 0 {
		return fileDiagnostic(
			"file received options but no literal inspection path",
			segmentIndex,
			len(argv)-1,
			argv[len(argv)-1],
			"file-path",
			"pass one to sixteen literal paths after the bounded file options",
		)
	}
	return nil
}

func isReadOnlyFileOption(value string) bool {
	switch value {
	case "-b", "--brief", "-h", "--no-dereference", "-i", "--mime", "--mime-encoding", "--mime-type", "-L", "--dereference":
		return true
	default:
		return false
	}
}

func fileDiagnostic(reason string, segmentIndex, argvIndex int, argument token, predicate, remediation string) *Diagnostic {
	return diagnosticForToken(
		CodePlanFileOptionDenied,
		reason,
		segmentIndex,
		argvIndex,
		argument,
		remediation,
		predicate,
	)
}

// inspectUniq permits stdin filtering or one input file only.  GNU uniq's
// second positional operand is an output file and therefore a writer.
//
// Example: uniq input is read-only, while uniq input output is denied.
func inspectUniq(argv []token, segmentIndex int) *Diagnostic {
	if len(argv) == 1 {
		return nil
	}
	if len(argv) == 2 && statPathLiteralSafe(argv[1].value) {
		return nil
	}
	predicate := "uniq-option"
	reason := "uniq options are outside the bounded stdin/one-input inspection grammar"
	remediation := "use uniq with stdin or one literal input path; do not pass an OUTPUT operand"
	if len(argv) > 2 {
		predicate = "uniq-output"
		reason = "uniq accepts a second positional OUTPUT operand that writes a file"
	}
	argumentIndex := len(argv) - 1
	if argumentIndex > 2 {
		argumentIndex = 2
	}
	return diagnosticForToken(
		CodePlanUniqArgumentsDenied,
		reason,
		segmentIndex,
		argumentIndex,
		argv[argumentIndex],
		remediation,
		predicate,
	)
}

func inspectInterpreter(name string, argv []token, segmentIndex int) *Diagnostic {
	runtime := namedRuntimeFamily(name)
	if runtime != "" {
		return inspectNamedRuntime(runtime, argv, segmentIndex)
	}
	for index, argument := range argv[1:] {
		if interpreterInlineCodeArgument("", argument.value) {
			return diagnosticForToken(
				CodePlanDynamicLaunchDenied,
				"inline or stdin interpreter code hides the executed argv",
				segmentIndex,
				index+1,
				argument,
				"invoke a literal script path or direct executable argv",
				"dynamic-interpreter-launch",
			)
		}
	}
	return nil
}

// inspectUnknownInterpreter detects dynamic selectors on an executable whose
// name identifies it as an interpreter/runtime/script runner, without turning
// the command classifier into an executable allowlist. Named runtimes use
// their more precise grammar above; this fallback covers a new language tool
// while keeping ordinary tools such as rg -c and g++ -c literal.
//
// Example: interpreter-tool --module test-suite is finite, while
// interpreter-tool -c 'dynamic payload' is an inline-code launch.
func inspectUnknownInterpreter(name string, argv []token, segmentIndex int) *Diagnostic {
	// Git's -c/--exec options select repository configuration or a transport
	// helper, not interpreter source; its dedicated grammar below owns those
	// forms.
	if filepath.Base(name) == "git" {
		return nil
	}
	for index, argument := range argv[1:] {
		if (interpreterRoleName(name) || genericInterpreterLongCodeArgument(argument.value)) &&
			genericInterpreterInlineCodeArgument(argument.value) {
			return diagnosticForToken(
				CodePlanDynamicLaunchDenied,
				"inline or stdin interpreter code hides the executed argv",
				segmentIndex,
				index+1,
				argument,
				"invoke a literal script path, module, or direct executable argv",
				"dynamic-interpreter-launch",
			)
		}
	}
	return nil
}

// interpreterRoleName recognizes semantic role markers rather than executable
// names. This keeps the fallback ecosystem-neutral: a new tool named for its
// interpreter/runtime/script role receives the same dynamic-argv boundary,
// while unrelated tools with ambiguous short options remain ordinary argv.
func interpreterRoleName(name string) bool {
	name = strings.ToLower(filepath.Base(name))
	for _, marker := range [...]string{"interpreter", "runtime", "script", "repl"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

// genericInterpreterInlineCodeArgument reports selectors that conventionally
// make an interpreter consume code or stdin instead of a visible source.
// Exact and equals-attached long forms avoid treating unrelated short options
// such as grep's -e or a compiler's -c as interpreter selectors; short forms
// are applied only after interpreterRoleName has identified the executable.
func genericInterpreterInlineCodeArgument(value string) bool {
	switch value {
	case "-", "-c", "-e", "-s", "--stdin", "--command", "--eval", "--execute", "--exec", "--script", "--run":
		return true
	}
	for _, prefix := range [...]string{
		"-c", "-e", "-s",
		"--stdin=", "--command=", "--eval=", "--execute=", "--exec=", "--script=", "--run=",
	} {
		if strings.HasPrefix(value, prefix) && len(value) > len(prefix) {
			return true
		}
	}
	return false
}

// genericInterpreterLongCodeArgument identifies long selectors whose meaning
// is unambiguously dynamic even when the executable has an unknown basename.
// Short selectors remain gated by interpreterRoleName because tools such as
// rg, grep, and compilers legitimately use -c/-e for bounded literal options.
func genericInterpreterLongCodeArgument(value string) bool {
	switch value {
	case "--stdin", "--command", "--eval", "--execute", "--exec", "--script", "--run":
		return true
	}
	for _, prefix := range [...]string{
		"--stdin=", "--command=", "--eval=", "--execute=", "--exec=", "--script=", "--run=",
	} {
		if strings.HasPrefix(value, prefix) && len(value) > len(prefix) {
			return true
		}
	}
	return false
}

// inspectNamedRuntime accepts only a runtime selector followed by a visible
// literal target; every trailing argument belongs to that confirmed target.
//
// Example: python3 -m pytest -c is a module invocation with script argv.
func inspectNamedRuntime(
	runtime string,
	argv []token,
	segmentIndex int,
) *Diagnostic {
	arguments := argv[1:]
	if len(arguments) == 0 {
		return literalRuntimeTargetDiagnostic(runtime, segmentIndex, 0, argv[0])
	}

	selector := arguments[0]
	if interpreterInlineCodeArgument(runtime, selector.value) {
		return diagnosticForToken(
			CodePlanDynamicLaunchDenied,
			"inline or stdin interpreter code hides the executed argv",
			segmentIndex,
			1,
			selector,
			"invoke a literal script path or direct executable argv",
			"dynamic-interpreter-launch",
		)
	}

	if runtime == "python" && selector.value == "-m" {
		if len(arguments) > 1 && isLiteralRuntimeTarget(arguments[1]) {
			return nil
		}
		if len(arguments) > 1 {
			return literalRuntimeTargetDiagnostic(runtime, segmentIndex, 2, arguments[1])
		}
		return literalRuntimeTargetDiagnostic(runtime, segmentIndex, 1, selector)
	}
	if runtime == "php" && (selector.value == "-f" || selector.value == "-F") {
		if len(arguments) > 1 && isLiteralRuntimeTarget(arguments[1]) {
			return nil
		}
		if len(arguments) > 1 {
			return literalRuntimeTargetDiagnostic(runtime, segmentIndex, 2, arguments[1])
		}
		return literalRuntimeTargetDiagnostic(runtime, segmentIndex, 1, selector)
	}
	if isLiteralRuntimeTarget(selector) {
		return nil
	}
	return literalRuntimeTargetDiagnostic(runtime, segmentIndex, 1, selector)
}

// literalRuntimeTargetDiagnostic reports a named runtime invocation that has
// no supported literal selector and target.
//
// Example: python3 -W ignore is not a direct source or module invocation.
func literalRuntimeTargetDiagnostic(
	runtime string,
	segmentIndex int,
	argvIndex int,
	argument token,
) *Diagnostic {
	return diagnosticForToken(
		CodePlanDynamicLaunchDenied,
		fmt.Sprintf("%s runtime argv has no supported literal source, module, or test target", runtime),
		segmentIndex,
		argvIndex,
		argument,
		"invoke a literal script or the runtime's supported literal selector before trailing argv",
		"dynamic-interpreter-launch",
	)
}

// interpreterInlineCodeArgument reports whether value makes a recognized
// interpreter execute inline or stdin-provided code.
//
// Example: Python -cprint and PHP -recho both execute code from argv.
func interpreterInlineCodeArgument(
	runtime string,
	value string,
) bool {
	if value == "-c" || value == "-s" || value == "-" || value == "--stdin" {
		return true
	}

	switch runtime {
	case "python":
		return strings.HasPrefix(value, "-c")
	case "node", "nodejs":
		return value == "-e" || value == "--eval" || value == "-p" || value == "--print" ||
			strings.HasPrefix(value, "-e=") || strings.HasPrefix(value, "--eval=") ||
			strings.HasPrefix(value, "-p=") || strings.HasPrefix(value, "--print=")
	case "perl":
		return value == "-e" || value == "-E" || strings.HasPrefix(value, "-e") || strings.HasPrefix(value, "-E")
	case "ruby":
		return value == "-e" || strings.HasPrefix(value, "-e")
	case "php":
		return value == "-a" || phpInlineCodeArgument(value, "-r", "--run") ||
			phpInlineCodeArgument(value, "-B", "--process-begin") ||
			phpInlineCodeArgument(value, "-R", "--process-code") ||
			phpInlineCodeArgument(value, "-E", "--process-end")
	default:
		return false
	}
}

// phpInlineCodeArgument reports whether value is a PHP code option. PHP
// accepts attached short-option payloads, while long options attach only with
// an equals sign.
//
// Example: -recho and --run=echo are both inline code forms.
func phpInlineCodeArgument(
	value string,
	shortOption string,
	longOption string,
) bool {
	return strings.HasPrefix(value, shortOption) || value == longOption || strings.HasPrefix(value, longOption+"=")
}

// isLiteralRuntimeTarget reports whether argument is a nonempty visible
// source, module, or test target rather than a runtime option.
//
// Example: pytest is a target, while -W is a runtime option.
func isLiteralRuntimeTarget(argument token) bool {
	return argument.value != "" && !strings.HasPrefix(argument.value, "-")
}

func inspectGit(
	request Request,
	argv []token,
	segmentIndex int,
) (DecisionKind, *Diagnostic) {
	if request.ShellInputDynamic {
		for _, argument := range argv {
			if strings.Contains(argument.value, "$?") {
				// Generated input markers are not literal Git options or refs.
				// The provider retains concrete effects with typed uncertainty.
				return DecisionDefer, nil
			}
		}
	}
	repositoryContext := false
	approvedRepositoryContext := true
	repositoryContextCount := 0
	for index, argument := range argv[1:] {
		if argument.value == "-C" {
			repositoryContext = true
			repositoryContextCount++
			if index+2 >= len(argv) || !approvedGitRepositoryContext(request, argv[index+2].value) {
				approvedRepositoryContext = false
			}
		}
		if strings.HasPrefix(argument.value, "-C") && len(argument.value) > 2 {
			repositoryContext = true
			repositoryContextCount++
			if !approvedGitRepositoryContext(request, argument.value[2:]) {
				approvedRepositoryContext = false
			}
		}
		if isGitContextOption(argument.value) {
			// Configuration spelling does not resolve a filesystem or control
			// target. Let the provider's Git route inspect the actual operation.
			return DecisionDefer, nil
		}
	}
	if repositoryContextCount > 1 {
		approvedRepositoryContext = false
	}
	subcommandIndex := gitSubcommandIndex(argv)
	if subcommandIndex >= len(argv) {
		return DecisionAllow, nil
	}
	if argv[subcommandIndex].value == "branch" || argv[subcommandIndex].value == "apply" {
		// The provider resolves consumed values and final inspection/mutation
		// modes once for its ref, acceptance, and repository readers.
		return DecisionDefer, nil
	}
	mutationIndex, action := gitMutation(argv, subcommandIndex)
	if action == "add" || action == "reset" || action == "commit" ||
		action == "mv" || action == "rm" || action == "restore" || action == "checkout" {
		// Resolve the repository and actual index/history effect in the provider.
		// A producer checkpoint differs from amendment or whole-tree staging;
		// the Git verb alone cannot establish the intended ownership boundary.
		return DecisionDefer, nil
	}
	remediation := "route this protected Git mutation request to the ECI Supervisor; ordinary scoped Producer checkpoint commits use the separate resolved repository/effect checks"
	if action == "update-index" {
		remediation = "the responsible Producer stages exact owned paths or agreed hunks with git add; stages agreed removals with git add -u -- <paths> or git rm --cached -- <paths>; unstages with git restore --staged -- <paths>; preserve unrelated content; these routes do not replace arbitrary index metadata, cache, or mode-only operations"
	}
	if repositoryContext {
		// Repository selection is provider-owned capability: the adapter must
		// establish that the target is one approved canonical root before a
		// worker can use an otherwise read-only Git verb. Preserve the worker
		// ownership diagnostic for mutations even when a -C context is visible.
		if action != "" && request.Marker == MarkerActive && request.Role == RoleWorker {
			return DecisionDeny, diagnosticForToken(
				CodeWorkerGitOwnershipDenied,
				fmt.Sprintf("worker argv selects acceptance-sensitive Git operation %s", action),
				segmentIndex,
				mutationIndex,
				argv[mutationIndex],
				remediation,
				"worker-git-ownership",
			)
		}
		if request.Role == RoleCoordinator && approvedRepositoryContext && action == "" {
			return DecisionAllow, nil
		}
		// In particular this prevents an active worker fast path from
		// admitting git -C /tmp/foreign status without approved-root proof.
		return DecisionDefer, nil
	}
	if action == "" {
		return DecisionAllow, nil
	}
	if request.Marker == MarkerActive && request.Role == RoleWorker {
		return DecisionDeny, diagnosticForToken(
			CodeWorkerGitOwnershipDenied,
			fmt.Sprintf("worker argv selects acceptance-sensitive Git operation %s", action),
			segmentIndex,
			mutationIndex,
			argv[mutationIndex],
			remediation,
			"worker-git-ownership",
		)
	}
	return DecisionDefer, nil
}

// approvedGitRepositoryContext reports whether value exactly matches a canonical approved repository root.
//
// Example: /home/user/project matches the same entry in Request.ApprovedRoots.
func approvedGitRepositoryContext(request Request, value string) bool {
	if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return false
	}
	for _, root := range request.ApprovedRoots {
		if root != "" && filepath.IsAbs(root) && filepath.Clean(root) == root && root == value {
			return true
		}
	}
	return false
}

// inspectBroadDestruction distinguishes utility modes before reporting a
// concrete broad destructive command; redirects are classified independently.
//
// Example: wipefs --help is informational, while wipefs --all erases signatures.
func inspectBroadDestruction(
	argv []token,
	segmentIndex int,
) *Diagnostic {
	name := filepath.Base(argv[0].value)
	filesystemDestruction := strings.HasPrefix(name, "mkfs")
	if filesystemDestruction || name == "wipefs" {
		for _, argument := range argv[1:] {
			if argument.inputRedirectTarget {
				continue
			}
			if argument.value == "--" {
				break
			}
			switch argument.value {
			case "--help":
				return nil
			case "--version":
				if name == "mkfs" || name == "wipefs" {
					return nil
				}
			case "-h", "-V", "-n", "--no-act":
				if name == "wipefs" {
					return nil
				}
			case "-a", "--all", "-o", "--offset":
				filesystemDestruction = true
			default:
				if name == "wipefs" && (strings.HasPrefix(argument.value, "--offset=") ||
					strings.HasPrefix(argument.value, "-o") && len(argument.value) > 2) {
					filesystemDestruction = true
				}
			}
		}
	}
	if filesystemDestruction {
		return diagnosticForToken(
			CodeBroadDestructiveDenied,
			"filesystem formatting is a broad destructive operation",
			segmentIndex,
			0,
			argv[0],
			"remove the filesystem-format operation and use a scoped non-destructive command",
			"broad-destructive-filesystem",
		)
	}
	if name == "find" {
		deleteIndex := -1
		rootIndex := -1
		for index, argument := range argv[1:] {
			if argument.value == "-delete" {
				deleteIndex = index + 1
			}
			if argument.value == "/" || argument.value == "~" {
				rootIndex = index + 1
			}
		}
		if deleteIndex >= 0 && rootIndex >= 0 {
			diagnostic := diagnosticForToken(
				CodeBroadDestructiveDenied,
				"find -delete selects a broad root",
				segmentIndex,
				rootIndex,
				argv[rootIndex],
				"replace the broad root with one explicit narrow recoverable target",
				"broad-destructive-root",
			)
			diagnostic.Path = argv[rootIndex].value
			return diagnostic
		}
	}
	if name != "rm" {
		return nil
	}
	recursive := false
	force := false
	rootIndex := -1
	for index, argument := range argv[1:] {
		value := argument.value
		if value == "--recursive" || value == "-r" || value == "-R" ||
			strings.HasPrefix(value, "-") && (strings.Contains(value, "r") || strings.Contains(value, "R")) {
			recursive = true
		}
		if value == "--force" || value == "-f" || strings.HasPrefix(value, "-") && strings.Contains(value, "f") {
			force = true
		}
		if value == "/" || value == "~" {
			rootIndex = index + 1
		}
	}
	if recursive && force && rootIndex >= 0 {
		diagnostic := diagnosticForToken(
			CodeBroadDestructiveDenied,
			"recursive forced deletion selects a broad root",
			segmentIndex,
			rootIndex,
			argv[rootIndex],
			"replace the broad root with one explicit narrow recoverable target",
			"broad-destructive-root",
		)
		diagnostic.Path = argv[rootIndex].value
		return diagnostic
	}
	return nil
}

type activeControlFile struct {
	path string
	info os.FileInfo
}

// activeControlIndex retains concrete inode facts from immediate session controls.
//
// Example: an observed marker hardlink remains protected beside many controls.
type activeControlIndex struct {
	files []activeControlFile
}

// activeControlFileIndex observes immediate control files without discarding facts.
//
// Example: the 129th control leaves earlier marker and control aliases visible.
func activeControlFileIndex(markers []string) activeControlIndex {
	index := activeControlIndex{
		files: make([]activeControlFile, 0, len(markers)*len(eciControlBasenames)),
	}
	seenSessionDirs := make(map[string]struct{}, len(markers))
	for _, marker := range markers {
		sessionDir := filepath.Clean(filepath.Dir(marker))
		if _, seen := seenSessionDirs[sessionDir]; seen {
			continue
		}
		seenSessionDirs[sessionDir] = struct{}{}
		directory, err := os.Open(sessionDir)
		if err != nil {
			continue
		}
		entries, err := directory.ReadDir(-1)
		closeErr := directory.Close()
		if errors.Is(err, io.EOF) {
			err = nil
		}
		if observationErr := errors.Join(err, closeErr); observationErr != nil {
			hookAdvisory("observe active controls", observationErr)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.Type()&os.ModeSymlink != 0 || !isECIControlBasename(name) {
				continue
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			index.files = append(index.files, activeControlFile{
				path: filepath.Join(sessionDir, entry.Name()),
				info: info,
			})
		}
	}
	return index
}

func isECIControlBasename(value string) bool {
	for _, name := range eciControlBasenames {
		if value == name || strings.HasPrefix(value, name+".") {
			return true
		}
	}
	return false
}

func activeControlHardlinkPath(path string, index []activeControlFile) string {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	for _, control := range index {
		if os.SameFile(info, control.info) {
			return control.path
		}
	}
	return ""
}

// isCanonicalWorkerHandoffPath reports whether a path is a handoff document in an active session.
//
// Example: latest-status-report.yaml beside an active marker is a handoff document.
func isCanonicalWorkerHandoffPath(
	path string,
	markers []string,
) bool {
	base := filepath.Base(path)
	switch base {
	case "instructions.md", "project-understanding.yaml", "high_level_log.jsonl", "latest-status-report.md", "latest-status-report.yaml":
		for _, marker := range markers {
			if path == filepath.Join(filepath.Dir(marker), base) {
				return true
			}
		}
	}
	return false
}

// activeControlResolvedPath resolves one visible writer operand and compares
// its target inode with the bounded active-control index.
//
// Example: a hardlink used as a touch target resolves to the active marker,
// while read-only aliases never reach this writer-only check.
func activeControlResolvedPath(
	path string,
	index []activeControlFile,
) string {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	var handoffPath string
	for _, control := range index {
		if os.SameFile(info, control.info) {
			// A note-named hardlink cannot hide another control record sharing
			// its inode. Prefer the concrete control over the ordinary note.
			switch filepath.Base(control.path) {
			case "instructions.md", "project-understanding.yaml", "latest-status-report.md", "latest-status-report.yaml":
				handoffPath = control.path
				continue
			}
			return control.path
		}
	}
	return handoffPath
}

// inspectLiveControl rejects a worker's concrete active-control write target
// while leaving index completeness and read-only discovery advisory.
//
// Example: `printf value > eci_active` reaches this check through its redirect
// target, while `cat eci_active` does not.
func inspectLiveControl(
	request Request,
	argv []token,
	segmentIndex int,
	controlIndex activeControlIndex,
) *Diagnostic {
	if request.CWDKnown || len(request.CWDCandidates) == 0 {
		return inspectLiveControlExact(request, argv, segmentIndex, controlIndex)
	}
	for _, candidate := range verifiedUniqueDirectories(request.CWDCandidates) {
		candidateRequest := request
		candidateRequest.CWD = candidate
		candidateRequest.CWDKnown = true
		candidateRequest.CWDCandidates = []string{candidate}
		if diagnostic := inspectLiveControlExact(candidateRequest, argv, segmentIndex, controlIndex); diagnostic != nil {
			return diagnostic
		}
	}
	return nil
}

// inspectLiveControlExact checks one concrete CWD candidate for a worker
// writer. Reachable candidates are joined by inspectLiveControl.
//
// Example: a relative `rm eci_active` is denied if any reachable directory
// contains the active marker.
func inspectLiveControlExact(
	request Request,
	argv []token,
	segmentIndex int,
	controlIndex activeControlIndex,
) *Diagnostic {
	// A bounded alias index can be incomplete when a session has many control
	// records. Its completeness is diagnostic metadata, never a reason to
	// block a visible concrete write target. Exact marker paths below remain
	// independently checked.
	for argumentIndex, argument := range argv[1:] {
		if !isSourceWriterOperand(argv, argumentIndex+1) {
			continue
		}
		pathArgument, pathLike := commandPathOperand(argv[0].value, argument)
		if filepath.Base(argv[0].value) == "cp" {
			pathArgument, pathLike = argument, true
		}
		if !pathLike {
			continue
		}
		candidate := pathArgument.value
		if !filepath.IsAbs(candidate) {
			if !request.CWDKnown {
				continue
			}
			candidate = filepath.Join(request.CWD, candidate)
		}
		candidate = filepath.Clean(candidate)
		for _, markerPath := range request.ActiveMarkers {
			cleanMarker := filepath.Clean(markerPath)
			if candidate == cleanMarker {
				diagnostic := diagnosticForToken(
					CodePlanLiveControlDenied,
					"worker argv resolves to an exact active-session live-control artifact",
					segmentIndex,
					argumentIndex+1,
					pathArgument,
					"route this exact live-control operation through the coordinator",
					"worker-live-control",
				)
				diagnostic.Path = candidate
				return diagnostic
			}
		}
		controlPath := activeControlResolvedPath(candidate, controlIndex.files)
		if request.HookMode && hookUnlinksDirectoryEntry(argv) && controlPath != "" {
			entry, entryErr := resolvePathEntryWithMissingSuffix(candidate)
			protectedEntry, protectedErr := resolvePathEntryWithMissingSuffix(controlPath)
			if entryErr != nil || protectedErr != nil || entry != protectedEntry {
				controlPath = ""
			}
		}
		if isCanonicalWorkerHandoffPath(candidate, request.ActiveMarkers) &&
			(controlPath == "" || (isCanonicalWorkerHandoffPath(controlPath, request.ActiveMarkers) &&
				filepath.Base(controlPath) == filepath.Base(candidate))) {
			continue
		}
		if controlPath != "" {
			diagnostic := diagnosticForToken(
				CodePlanLiveControlDenied,
				"worker argv inode matches an active-session live-control artifact",
				segmentIndex,
				argumentIndex+1,
				pathArgument,
				"route this exact live-control operation through the coordinator",
				"worker-live-control",
			)
			diagnostic.Path = controlPath
			return diagnostic
		}
	}
	return nil
}

func deniedResult(request Request, diagnostic Diagnostic) Result {
	marker := string(request.Marker)
	reason := fmt.Sprintf(
		"[%s] ECI gate denied (phase=PreToolUse, operation=%s, provider=%s, role=%s, marker=%s, segment=%d, argv_index=%d, byte_offset=%d, token=%s, path=%s, predicate=%s); reason: %s; rejected segment=%s; remediation: %s.",
		diagnostic.Code,
		diagnostic.Operation,
		request.Provider,
		request.Role,
		marker,
		diagnostic.Segment,
		diagnostic.ArgvIndex,
		diagnostic.ByteOffset,
		shellEscape(diagnostic.Token),
		shellEscape(diagnostic.Path),
		diagnostic.Predicate,
		diagnostic.Reason,
		diagnostic.RejectedSegment,
		diagnostic.Remediation,
	)
	return Result{
		Decision:   DecisionDeny,
		Diagnostic: &diagnostic,
		HookSpecificOutput: &HookSpecificOutput{
			HookEventName:            "PreToolUse",
			PermissionDecision:       "deny",
			PermissionDecisionReason: reason,
		},
	}
}

// suppressInactiveDiagnostic keeps unsupported inactive-scope denials advisory.
//
// Example: concrete Worker session writes remain denied in hook mode.
func suppressInactiveDiagnostic(request Request, diagnostic *Diagnostic) bool {
	if request.Marker != MarkerInactive {
		return false
	}
	if diagnostic.Operation == "worker-control" || diagnostic.Predicate == "broad-destructive-root" ||
		(request.HookMode && diagnostic.Code == hookCodeWorkerSessionsRootDenied) {
		return false
	}
	code := string(diagnostic.Code)
	return !strings.HasPrefix(code, "ECI_ENVIRONMENT_") &&
		!strings.HasPrefix(code, "ECI_GIT_")
}

func newPlanError(
	code DiagnosticCode,
	reason string,
	offset int,
	segmentIndex int,
	argvIndex int,
	tokenValue string,
	remediation string,
	predicate string,
) *planError {
	return &planError{diagnostic: Diagnostic{
		Code:        code,
		Operation:   operationForCode(code),
		Segment:     segmentIndex,
		ArgvIndex:   argvIndex,
		ByteOffset:  offset,
		Token:       tokenValue,
		Path:        "n/a",
		Predicate:   predicate,
		Reason:      reason,
		Remediation: remediation,
	}}
}

func diagnosticForToken(
	code DiagnosticCode,
	reason string,
	segmentIndex int,
	argvIndex int,
	argument token,
	remediation string,
	predicate string,
) *Diagnostic {
	return &Diagnostic{
		Code:        code,
		Operation:   operationForCode(code),
		Segment:     segmentIndex,
		ArgvIndex:   argvIndex,
		ByteOffset:  argument.offset,
		Token:       argument.value,
		Path:        "n/a",
		Predicate:   predicate,
		Reason:      reason,
		Remediation: remediation,
	}
}

func operationForCode(code DiagnosticCode) string {
	value := string(code)
	switch {
	case code == CodeLifecycleCanonicalPathDenied:
		return "eci-lifecycle"
	case strings.HasPrefix(value, "ECI_PLAN_"):
		return "plan-segment"
	case strings.HasPrefix(value, "ECI_ENVIRONMENT_"):
		return "environment-boundary"
	case code == CodeWorkerGitOwnershipDenied:
		return "worker-git-ownership"
	case code == CodeGitExecutionContextDenied:
		return "git-execution-context"
	case code == CodeControlOwnerRequired, code == CodeControlIdentityDenied:
		return "worker-control"
	case code == CodeBroadDestructiveDenied:
		return "broad-destructive"
	case code == CodeLedgerAppendOnly:
		return "ledger-append-only"
	case code == CodeProofPathEscapeDenied:
		return "proof-path-ownership"
	default:
		return "plan-segment"
	}
}

func malformedWrapperDiagnostic(argument token, segmentIndex int, argvIndex int) *Diagnostic {
	return diagnosticForToken(
		CodePlanWrapperDenied,
		"transparent wrapper has no complete literal child argv",
		segmentIndex,
		argvIndex,
		argument,
		"supply the wrapper's required options and one literal child argv",
		"malformed-transparent-wrapper",
	)
}

func assignmentName(argument token) string {
	if argument.quoted {
		return ""
	}
	separator := strings.IndexByte(argument.value, '=')
	if separator <= 0 || !isIdentifier(argument.value[:separator]) {
		return ""
	}
	return argument.value[:separator]
}

// environmentAssignmentName recognizes env's assignment grammar after shell
// lexing. Quoting changes shell-leading assignment semantics, but it does not
// stop env from receiving an assignment token as its own argv element.
func environmentAssignmentName(argument token) string {
	argument.quoted = false
	return assignmentName(argument)
}

func isIdentifier(value string) bool {
	if value == "" || !isIdentifierFirst(value[0]) {
		return false
	}
	for index := 1; index < len(value); index++ {
		if !isIdentifierRest(value[index]) {
			return false
		}
	}
	return true
}

func isIdentifierFirst(character byte) bool {
	return character == '_' || character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z'
}

func isIdentifierRest(character byte) bool {
	return isIdentifierFirst(character) || character >= '0' && character <= '9'
}

func isReservedControl(argument token) bool {
	if argument.quoted || argument.value == "time" {
		return false
	}
	switch argument.value {
	case "!", "[[", "]]", "case", "coproc", "do", "done", "elif", "else", "esac", "fi", "for", "function", "if", "in", "select", "then", "until", "while":
		return true
	default:
		return false
	}
}

func isEnvironmentContextName(name string) bool {
	if strings.HasPrefix(name, "GIT_") {
		return true
	}
	switch name {
	case "BASH_ENV", "ENV", "LD_AUDIT", "LD_PRELOAD", "PERL5OPT", "PYTHONHOME", "PYTHONPATH", "PYTHONSTARTUP", "RUBYOPT":
		return true
	default:
		return false
	}
}

// inspectInheritedExecutionContext rejects an active command when the hook's
// own environment can preload code into the selected runtime or native child.
//
// Example: NODE_OPTIONS=--require=/probe prevents node script.js admission.
func inspectInheritedExecutionContext(
	target token,
	segmentIndex int,
) *Diagnostic {
	for _, name := range inheritedExecutionContextNames() {
		if os.Getenv(name) == "" {
			continue
		}
		return diagnosticForToken(
			CodeEnvironmentContextDenied,
			"inherited callback environment changes runtime or executable startup context",
			segmentIndex,
			0,
			token{value: name, offset: target.offset},
			"clear the reported inherited environment variable before invoking the active command",
			"inherited-environment-context",
		)
	}
	return nil
}

// inheritedExecutionContextNames returns every environment name that can alter
// native process loading or a runtime before a literal child argv runs.
//
// Example: NODE_OPTIONS is checked even for a renamed Node executable.
func inheritedExecutionContextNames() []string {
	return []string{
		"BASH_ENV", "ENV", "JAVA_TOOL_OPTIONS", "JDK_JAVA_OPTIONS", "LD_AUDIT", "LD_LIBRARY_PATH", "LD_PRELOAD",
		"LUA_CPATH", "LUA_INIT", "LUA_PATH", "NODE_OPTIONS", "NODE_PATH",
		"PERL5LIB", "PERL5OPT", "PHPRC", "PHP_INI_SCAN_DIR", "PYTHONHOME",
		"PYTHONPATH", "PYTHONSTARTUP", "RUBYLIB", "RUBYOPT", "ZDOTDIR",
	}
}

func isInterpreter(name string) bool {
	if namedRuntimeFamily(name) != "" {
		return true
	}
	switch name {
	case "bash", "dash", "sh", "zsh":
		return true
	default:
		return false
	}
}

// namedRuntimeFamily recognizes only runtime basenames and numeric version
// suffixes. Arbitrary similarly named helper executables remain ordinary argv.
//
// Example: python3.11 is Python, while python-tool is not a runtime alias.
func namedRuntimeFamily(name string) string {
	// TODO: return a named runtime-family type with typed constants instead of
	// bare strings that callers compare, such as "python" in
	// hookPythonTarget.matchesCommand and the inline-code checks.
	for _, runtime := range [...]string{"python", "nodejs", "node", "perl", "ruby", "php"} {
		if name == runtime {
			if runtime == "nodejs" {
				return "node"
			}
			return runtime
		}
		if strings.HasPrefix(name, runtime) && isNumericVersionSuffix(strings.TrimPrefix(name, runtime)) {
			if runtime == "nodejs" {
				return "node"
			}
			return runtime
		}
	}
	return ""
}

// isNumericVersionSuffix reports whether value is a dot-separated numeric
// runtime version suffix.
//
// Example: 3.11 is valid, while -tool and 3..11 are not.
func isNumericVersionSuffix(value string) bool {
	if value == "" {
		return false
	}
	previousDot := true
	for _, character := range value {
		switch {
		case character >= '0' && character <= '9':
			previousDot = false
		case character == '.' && !previousDot:
			previousDot = true
		default:
			return false
		}
	}
	return !previousDot
}

func isLifecycleScriptCapability(cwd string, argv []token) bool {
	if isLifecycleScriptPath(cwd, argv[0].value) {
		return true
	}
	if !isShellInterpreter(filepath.Base(argv[0].value)) {
		return false
	}

	index := 1
	for index < len(argv) {
		value := argv[index].value
		switch value {
		case "--":
			index++
			if index >= len(argv) {
				return false
			}
			return isLifecycleScriptPath(cwd, argv[index].value)
		case "-e", "-n", "--noexec", "-x", "--trace", "--noprofile", "--norc", "--posix", "--restricted", "--verbose":
			index++
		case "-O":
			if index+1 >= len(argv) {
				return false
			}
			index += 2
		default:
			if strings.HasPrefix(value, "-") {
				return false
			}
			return isLifecycleScriptPath(cwd, value)
		}
	}

	return false
}

// compoundPlanContainsFiniteShellScriptInvocation identifies a compound plan
// containing one direct finite shell-script argv. The provider adapter keeps
// ownership of its complete raw shell topology.
//
// Example: bash -x test.sh | tail -n 1 returns true, while bash -x test.sh
// remains a direct one-segment plan.
func compoundPlanContainsFiniteShellScriptInvocation(parsed plan) bool {
	if len(parsed.operators) == 0 {
		return false
	}
	for _, current := range parsed.segments {
		if isFiniteShellScriptInvocation(current.argv) {
			return true
		}
	}
	return false
}

// isFiniteShellScriptInvocation recognizes a direct shell interpreter with a
// visible finite script operand, rather than inline or stdin-provided code.
//
// Example: bash -x test.sh is finite; bash -c 'echo x' is not.
func isFiniteShellScriptInvocation(argv []token) bool {
	if len(argv) < 2 || !isShellInterpreter(filepath.Base(argv[0].value)) {
		return false
	}

	index := 1
	for index < len(argv) {
		value := argv[index].value
		switch value {
		case "--":
			index++
			return index < len(argv) && isLiteralRuntimeTarget(argv[index])
		case "-e", "-n", "--noexec", "-x", "--trace", "--noprofile", "--norc", "--posix", "--restricted", "--verbose":
			index++
		case "-O":
			if index+1 >= len(argv) {
				return false
			}
			index += 2
		default:
			if strings.HasPrefix(value, "-") {
				return false
			}
			return isLiteralRuntimeTarget(argv[index])
		}
	}

	return false
}

func isLifecycleScriptPath(cwd, value string) bool {
	if filepath.Base(value) == value {
		return isLifecycleName(value)
	}

	cleaned := filepath.Clean(value)
	if !filepath.IsAbs(cleaned) {
		cleaned = filepath.Join(cwd, cleaned)
	}
	resolved := resolvePathIdentity(cleaned)
	switch {
	case filepath.Base(filepath.Dir(resolved)) == "hooks":
		switch filepath.Base(resolved) {
		case "eci-review-gate.sh", "eci-active-gate.sh", "stop-gate.sh":
			return true
		default:
			return false
		}
	case filepath.Base(filepath.Dir(resolved)) == "bin":
		return filepath.Base(resolved) == "eci-active"
	default:
		return false
	}
}

func resolvePathIdentity(path string) string {
	resolved, err := resolvePathWithMissingSuffix(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return resolved
}

func isShellInterpreter(name string) bool {
	switch name {
	case "bash", "dash", "sh", "zsh":
		return true
	default:
		return false
	}
}

func isLifecycleName(name string) bool {
	switch name {
	case "eci-active", "eci-active-gate.sh", "eci-review-gate", "eci-review-gate.sh", "eci-stage", "stop-gate.sh":
		return true
	default:
		return false
	}
}

func isCoordinatorHookRepair(argv []token) bool {
	if len(argv) < 4 || !isInterpreter(filepath.Base(argv[0].value)) {
		return false
	}
	for index := 1; index+1 < len(argv); index++ {
		if filepath.Base(argv[index].value) == "install-pre-commit-go-mod.sh" &&
			argv[index+1].value == "--repair-hardlink" {
			return true
		}
	}
	return false
}

// isPreCommitHookModeRepair recognizes only the exact direct pre-commit hook mode repair argv.
//
// Example: chmod 755 hooks/pre-commit-go-mod.sh.
func isPreCommitHookModeRepair(argv []token) (token, bool) {
	if len(argv) != 3 || argv[0].quoted || argv[1].quoted || argv[2].quoted ||
		argv[0].value != "chmod" || argv[1].value != "755" || argv[2].value != "hooks/pre-commit-go-mod.sh" {
		return token{}, false
	}
	return argv[2], true
}

// protectedHookModeRelativePaths lists the hook files whose modes are owned by the active ECI coordinator.
//
// Example: hooks/validate-bash.sh cannot be mode-mutated by an active worker.
var protectedHookModeRelativePaths = [...]string{
	"hooks/validate-bash.sh",
	"hooks/pre-commit-go-mod.sh",
	"hooks/install-pre-commit-go-mod.sh",
	"hooks/tests/test-pre-commit-go-mod.sh",
}

// protectedHookModeMutation identifies a chmod target owned by the active ECI coordinator.
//
// Example: chmod 644 hooks/validate-bash.sh.
func protectedHookModeMutation(cwd string, argv []token) (token, string, bool) {
	if len(argv) < 3 || filepath.Base(argv[0].value) != "chmod" {
		return token{}, "", false
	}
	targets, recursive := chmodMutationTargets(argv)
	for _, target := range targets {
		if resolved, ok := protectedHookModeTargetPath(cwd, target.value); ok {
			return target, resolved, true
		}
		if recursive {
			if resolved, ok := protectedHookModeRecursiveTargetPath(cwd, target.value); ok {
				return target, resolved, true
			}
		}
	}
	return token{}, "", false
}

// chmodMutationTargets returns chmod mutation targets and whether parsed options enable recursion.
//
// Example: chmod hooks --ref=ordinary.txt returns hooks as a target with reference mode enabled.
func chmodMutationTargets(argv []token) ([]token, bool) {
	operands := make([]token, 0, len(argv)-2)
	index := 1
	recursive := false
	referenceMode := false
	options := true
	for index < len(argv) {
		value := argv[index].value
		switch {
		case options && value == "--":
			options = false
			index++
		case options && chmodOptionHasEmptyReferenceValue(value):
			return nil, recursive
		case options && chmodOptionIsReference(value):
			referenceMode = true
			if strings.Contains(value, "=") {
				index++
				continue
			}
			if index+1 >= len(argv) {
				return nil, recursive
			}
			index += 2
		case options && chmodOptionIsRecursive(value):
			recursive = true
			index++
		case options && strings.HasPrefix(value, "-"):
			index++
		default:
			operands = append(operands, argv[index])
			index++
		}
	}
	if referenceMode {
		return operands, recursive
	}
	if len(operands) < 2 {
		return nil, recursive
	}
	return operands[1:], recursive
}

// chmodOptionIsRecursive reports whether a chmod option enables recursive mutation.
//
// Example: -vR, --rec, and --recursive enable recursion.
func chmodOptionIsRecursive(value string) bool {
	if strings.HasPrefix(value, "--") {
		return len(value) >= len("--rec") && strings.HasPrefix("--recursive", value)
	}
	return strings.HasPrefix(value, "-") && !strings.HasPrefix(value, "--") && strings.Contains(value[1:], "R")
}

// chmodOptionIsReference reports whether a chmod option selects a nonempty reference source.
//
// Example: --ref=source and --reference source select a source operand.
func chmodOptionIsReference(value string) bool {
	option, reference, hasReference := strings.Cut(value, "=")
	return (!hasReference || reference != "") &&
		len(option) >= len("--ref") && strings.HasPrefix("--reference", option)
}

// chmodOptionHasEmptyReferenceValue reports an otherwise valid reference option with no source.
//
// Example: --ref= is invalid and cannot identify a mutation target.
func chmodOptionHasEmptyReferenceValue(value string) bool {
	option, reference, hasReference := strings.Cut(value, "=")
	return hasReference && reference == "" &&
		len(option) >= len("--ref") && strings.HasPrefix("--reference", option)
}

// protectedHookModeTargetPath resolves a protected tracked hook target below a trusted root.
//
// Example: ./hooks/validate-bash.sh resolves to the active provider's validate-bash.sh.
func protectedHookModeTargetPath(cwd, value string) (string, bool) {
	resolved, ok := resolveChmodTargetPath(cwd, value)
	if !ok {
		return "", false
	}
	for _, root := range protectedHookModeRoots() {
		relative, err := filepath.Rel(root, resolved)
		if err != nil {
			continue
		}
		for _, protectedPath := range protectedHookModeRelativePaths {
			if filepath.ToSlash(relative) != protectedPath {
				continue
			}
			return resolved, true
		}
	}
	return "", false
}

// protectedHookModeRecursiveTargetPath resolves a recursive chmod target that contains a protected hook.
//
// Example: chmod -R 644 hooks contains hooks/validate-bash.sh.
func protectedHookModeRecursiveTargetPath(cwd, value string) (string, bool) {
	resolved, ok := resolveChmodTargetPath(cwd, value)
	if !ok {
		return "", false
	}
	for _, root := range protectedHookModeRoots() {
		for _, protectedPath := range protectedHookModeRelativePaths {
			if pathWithin(filepath.Join(root, protectedPath), resolved) {
				return resolved, true
			}
		}
	}
	return "", false
}

// resolveChmodTargetPath canonicalizes a nonempty chmod target against the request working directory.
//
// Example: ./hooks resolves relative to the request working directory.
func resolveChmodTargetPath(cwd, value string) (string, bool) {
	if value == "" {
		return "", false
	}
	candidate := value
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(cwd, candidate)
	}
	return resolvePathIdentity(filepath.Clean(candidate)), true
}

// protectedHookModeRoots returns canonical provider roots whose tracked hook modes are protected.
//
// Example: an omp callback protects $HOME/.omp/agent/eci, and the planner root
// remains protected.
func protectedHookModeRoots() []string {
	home := os.Getenv("HOME")
	candidates := []string{
		filepath.Join(home, ".omp", "agent", "eci"),
		os.Getenv("KIMI_CODE_HOME"),
		filepath.Join(home, ".kimi-code"),
	}
	if executable, err := os.Executable(); err == nil {
		// Only the source-module layout identifies a provider root; managed artifacts do not.
		dir := filepath.Dir(executable)
		if filepath.Base(dir) == "eci-command-plan-go" &&
			filepath.Base(filepath.Dir(dir)) == "lib" &&
			filepath.Base(filepath.Dir(filepath.Dir(dir))) == "hooks" {
			candidates = append(candidates, filepath.Join(dir, "..", "..", ".."))
		}
	}
	roots := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" || !filepath.IsAbs(candidate) {
			continue
		}
		resolved := resolvePathIdentity(filepath.Clean(candidate))
		if _, ok := seen[resolved]; ok {
			continue
		}
		seen[resolved] = struct{}{}
		roots = append(roots, resolved)
	}
	return roots
}

func isGitContextOption(value string) bool {
	switch value {
	case "-c", "--config-env", "--git-dir", "--work-tree", "--exec-path", "--namespace", "--super-prefix", "--textconv", "--ext-diff":
		return true
	default:
		return strings.HasPrefix(value, "--config-env=") ||
			strings.HasPrefix(value, "--git-dir=") ||
			strings.HasPrefix(value, "--work-tree=") ||
			strings.HasPrefix(value, "--exec-path=") ||
			strings.HasPrefix(value, "--namespace=") ||
			strings.HasPrefix(value, "--super-prefix=")
	}
}

func hasAttachedGitContextOption(argv []token) bool {
	for _, argument := range argv[1:] {
		if strings.Contains(argument.value, "=") && isGitContextOption(argument.value) {
			return true
		}
	}
	return false
}

func gitSubcommandIndex(argv []token) int {
	index := 1
	for index < len(argv) {
		value := argv[index].value
		switch {
		case isGitSplitContextOption(value) && index+1 < len(argv):
			index += 2
		case value == "--literal-pathspecs", value == "--no-optional-locks", value == "--no-pager", strings.HasPrefix(value, "-"):
			index++
		default:
			return index
		}
	}
	return index
}

// isGitSplitContextOption reports Git global options whose value is supplied
// as the following argv token. Keeping these options in the subcommand scan
// prevents a worker fsck writer from being mistaken for a direct-env route
// merely because its context value uses split spelling.
func isGitSplitContextOption(value string) bool {
	switch value {
	// TODO: --exec-path takes its value only after =; bare --exec-path prints the path
	// and exits, so it must not consume the next word.
	case "-C", "-c", "--config-env", "--git-dir", "--work-tree", "--exec-path", "--namespace", "--super-prefix":
		return true
	default:
		return false
	}
}

// gitFsckLostFoundOption returns the exact --lost-found token after the fsck
// subcommand. The caller supplies the already-selected subcommand position so
// the direct worker route does not inherit generic Git option interpretation.
func gitFsckLostFoundOption(argv []token, subcommandIndex int) (int, bool) {
	if subcommandIndex >= len(argv) || argv[subcommandIndex].value != "fsck" {
		return 0, false
	}
	for index, argument := range argv[subcommandIndex+1:] {
		if argument.value == "--lost-found" {
			return subcommandIndex + 1 + index, true
		}
	}
	return 0, false
}

// isGitFsckLostFound reports whether argv selects Git fsck and passes the
// exact option that writes dangling objects under the repository metadata.
//
// Example: git --no-pager fsck --lost-found is true, while bare git fsck is false.
func isGitFsckLostFound(argv []token) bool {
	_, ok := gitFsckLostFoundOption(argv, gitSubcommandIndex(argv))
	return ok
}

// gitMutation identifies concrete effects handled directly by the Go gate.
// Branch and apply final modes are resolved by the shared provider reducer.
//
// Example: git rebase topic retains its direct history-mutation boundary.
func gitMutation(
	argv []token,
	subcommandIndex int,
) (int, string) {
	subcommand := argv[subcommandIndex].value
	switch subcommand {
	case "add", "am", "cherry-pick", "checkout", "commit", "config", "gc", "merge", "mv", "push", "rebase", "rename", "replace", "reset", "restore", "revert", "rm", "tag", "update-index", "worktree":
		return subcommandIndex, subcommand
	case "submodule":
		if subcommandIndex+1 >= len(argv) || argv[subcommandIndex+1].value != "status" {
			return subcommandIndex, subcommand
		}
	case "remote":
		if subcommandIndex+1 < len(argv) {
			action := argv[subcommandIndex+1].value
			switch action {
			case "add", "prune", "remove", "rename", "set-head", "set-url", "update":
				return subcommandIndex + 1, "remote " + action
			}
		}
	}
	return 0, ""
}

// isSourceWriterOperand identifies the mutated operand of a finite copy and
// preserves whole-command write semantics for the other existing writers.
// Unknown copy options or operand counts supply no inferred mutation.
//
// Example: cp -f -- SOURCE DEST writes only DEST; mv still mutates SOURCE.
func isSourceWriterOperand(
	argv []token,
	argumentIndex int,
) bool {
	if len(argv) == 0 || argumentIndex <= 0 || argumentIndex >= len(argv) {
		return false
	}
	if argv[argumentIndex].inputRedirectTarget {
		return false
	}
	name := filepath.Base(argv[0].value)
	if name != "cp" {
		if name == "sed" {
			return isSedWriterOperand(argv, argumentIndex)
		}
		if name == "truncate" {
			return isTruncateWriterOperand(argv, argumentIndex)
		}
		return isSourceWriter(name, argv)
	}

	optionsEnded := false
	operandCount := 0
	destinationIndex := -1
	for index := 1; index < len(argv); index++ {
		if argv[index].inputRedirectTarget {
			continue
		}
		value := argv[index].value
		if !optionsEnded {
			switch value {
			case "--":
				optionsEnded = true
				continue
			case "-f", "-r", "-R", "-v", "--force", "--recursive", "--verbose":
				continue
			}
			if strings.HasPrefix(value, "-") {
				if len(value) == 1 || strings.Trim(value[1:], "frR") != "" {
					return false
				}
				continue
			}
		}
		operandCount++
		destinationIndex = index
	}
	return operandCount == 2 && argumentIndex == destinationIndex
}

// isSedWriterOperand recognizes only in-place file operands, skipping script
// values supplied to -e/--expression and -f/--file.
//
// Example: `sed -f script -i output` marks output but not script.
func isSedWriterOperand(argv []token, argumentIndex int) bool {
	inPlace := false
	optionsEnded := false
	writerOperands := make(map[int]struct{})
	for index := 1; index < len(argv); index++ {
		value := argv[index].value
		if argv[index].inputRedirectTarget {
			continue
		}
		if !optionsEnded {
			switch {
			case value == "--":
				optionsEnded = true
			case value == "-i" || value == "--in-place" || strings.HasPrefix(value, "-i") || strings.HasPrefix(value, "--in-place="):
				inPlace = true
			case value == "-e" || value == "--expression" || value == "-f" || value == "--file":
				index++
			case strings.HasPrefix(value, "-e") || strings.HasPrefix(value, "-f") || strings.HasPrefix(value, "--expression=") || strings.HasPrefix(value, "--file="):
			case strings.HasPrefix(value, "-"):
				return false
			default:
				writerOperands[index] = struct{}{}
			}
			continue
		}
		writerOperands[index] = struct{}{}
	}
	if !inPlace {
		return false
	}
	_, ok := writerOperands[argumentIndex]
	return ok
}

// isTruncateWriterOperand excludes finite size-option values from target
// ownership checks and marks only concrete file operands.
//
// Example: `truncate -s SIZE output` writes output, not SIZE.
func isTruncateWriterOperand(argv []token, argumentIndex int) bool {
	optionsEnded := false
	writerOperands := make(map[int]struct{})
	for index := 1; index < len(argv); index++ {
		value := argv[index].value
		if argv[index].inputRedirectTarget {
			continue
		}
		if !optionsEnded {
			switch {
			case value == "--":
				optionsEnded = true
			case value == "-s" || value == "--size":
				index++
			case strings.HasPrefix(value, "-s") && len(value) > 2,
				strings.HasPrefix(value, "--size="):
			case strings.HasPrefix(value, "-"):
				return false
			default:
				writerOperands[index] = struct{}{}
			}
			continue
		}
		writerOperands[index] = struct{}{}
	}
	_, ok := writerOperands[argumentIndex]
	return ok
}

// isSourceWriter reports whether command name can modify a file named in argv:
// the listed file mutators always can, and sed can only with an in-place
// option.
//
// Example: `sed -i s/a/b/ file` is a writer; `sed s/a/b/ file` is not.
func isSourceWriter(name string, argv []token) bool {
	switch name {
	case "chmod", "chown", "cp", "dd", "install", "ln", "mv", "patch", "rm", "rsync", "rmdir", "shred", "srm", "tee", "touch", "truncate", "unlink":
		return true
	// TODO: treat yq/yq4 in-place writes (-i, yq4 --inplace, jq-based yq --in-place) as writers, selecting operands like isSedWriterOperand so the expression is not a target; context-ledger recommends "yq4 -i", which this check misses on another session's ledger.
	case "sed":
		for _, argument := range argv[1:] {
			if argument.value == "-i" || argument.value == "--in-place" || strings.HasPrefix(argument.value, "-i") || strings.HasPrefix(argument.value, "--in-place=") {
				return true
			}
		}
	}
	return false
}

func shellEscape(value string) string {
	if value == "" {
		return "''"
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if !(isIdentifierRest(character) || strings.ContainsRune("_./:=+,-", rune(character))) {
			return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
		}
	}
	return value
}
