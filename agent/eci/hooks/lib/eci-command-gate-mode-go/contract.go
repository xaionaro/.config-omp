package main

import "fmt"

const (
	// MaxConfigBytes bounds the command-gate configuration record.
	MaxConfigBytes = 32
	// MaxDenialBytes bounds one denial document before reduction.
	MaxDenialBytes = 65_536
	// MaxEventBytes bounds one persisted telemetry record.
	MaxEventBytes = 4_096
	// MaxLogBytes bounds the active telemetry generation.
	MaxLogBytes = 1_048_576
	// MaxLogFiles is the number of rotated telemetry generations retained.
	MaxLogFiles = 4

	// EventSchema identifies the persisted command-gate telemetry schema.
	// v2 adds bounded command, cwd, session_id, and reason diagnostics.
	EventSchema = "eci-command-gate-event/v2"
	// MaxEventCommandBytes bounds the JSON-encoded command field of one record.
	MaxEventCommandBytes = 1_024
	// MaxEventCWDBytes bounds the JSON-encoded cwd field of one record.
	MaxEventCWDBytes = 512
	// MaxEventSessionIDBytes bounds the JSON-encoded session_id field of one record.
	MaxEventSessionIDBytes = 128
	// MaxEventReasonBytes bounds the JSON-encoded reason field of one record.
	MaxEventReasonBytes = 1_536
	// TruncationMarker ends every diagnostic field cut to its byte bound.
	TruncationMarker = "…[truncated]"
	// EnvDenialCommand carries the denied command from the hook to finalize.
	EnvDenialCommand = "ECI_GATE_DENIAL_COMMAND"
	// EnvDenialCWD carries the denied command's working directory to finalize.
	EnvDenialCWD = "ECI_GATE_DENIAL_CWD"
	// EnvDenialSessionID carries the callback session id to finalize.
	EnvDenialSessionID = "ECI_GATE_DENIAL_SESSION_ID"
	// ConfigDirectoryName is the command-gate configuration directory name.
	ConfigDirectoryName = "eci"
	// ConfigFileName is the command-gate configuration file name.
	ConfigFileName = "command-gate-mode"
	// TelemetryDirectoryName is the command-gate telemetry directory name.
	TelemetryDirectoryName = "command-gate"
	// TelemetryParentDirectoryName is the state subtree containing telemetry directories.
	TelemetryParentDirectoryName = "eci"
	// TelemetryFileName is the active command-gate telemetry file name.
	TelemetryFileName = "would-deny.jsonl"
	// RotationLockName serializes nonblocking telemetry rotation attempts.
	RotationLockName = ".rotation.lock"
	// TelemetryUnavailableMessage is the bounded permissive-mode warning.
	TelemetryUnavailableMessage = "eci-command-gate-mode: telemetry unavailable\n"
)

// TelemetryCode identifies the closed diagnostic class persisted by permissive mode.
//
// Example: TelemetryCodeOther represents a code not registered in the current schema.
type TelemetryCode string

const (
	// TelemetryCodeBroadDestructiveDenied identifies broad destructive command denial.
	TelemetryCodeBroadDestructiveDenied TelemetryCode = "ECI_BROAD_DESTRUCTIVE_DENIED"
	// TelemetryCodeCommandDynamicIndirectionDenied identifies dynamic command indirection denial.
	TelemetryCodeCommandDynamicIndirectionDenied TelemetryCode = "ECI_COMMAND_DYNAMIC_INDIRECTION_DENIED"
	// TelemetryCodeCommandNonliteralDenied identifies nonliteral command denial.
	TelemetryCodeCommandNonliteralDenied TelemetryCode = "ECI_COMMAND_NONLITERAL_DENIED"
	// TelemetryCodeCommandNotAllowlisted identifies a command outside the accepted grammar.
	TelemetryCodeCommandNotAllowlisted TelemetryCode = "ECI_COMMAND_NOT_ALLOWLISTED"
	// TelemetryCodeCommandSyntaxDenied identifies shell syntax denial.
	TelemetryCodeCommandSyntaxDenied TelemetryCode = "ECI_COMMAND_SYNTAX_DENIED"
	// TelemetryCodeCommandWrapperUnsupported identifies an unsupported command wrapper.
	TelemetryCodeCommandWrapperUnsupported TelemetryCode = "ECI_COMMAND_WRAPPER_UNSUPPORTED"
	// TelemetryCodeCommitAdmissionRequired identifies a missing commit admission.
	TelemetryCodeCommitAdmissionRequired TelemetryCode = "ECI_COMMIT_ADMISSION_REQUIRED"
	// TelemetryCodeControlIdentityDenied identifies control identity denial.
	TelemetryCodeControlIdentityDenied TelemetryCode = "ECI_CONTROL_IDENTITY_DENIED"
	// TelemetryCodeControlOwnerRequired identifies coordinator-only control ownership.
	TelemetryCodeControlOwnerRequired TelemetryCode = "ECI_CONTROL_OWNER_REQUIRED"
	// TelemetryCodeCoordinatorCleanupPipelineDenied identifies cleanup pipeline denial.
	TelemetryCodeCoordinatorCleanupPipelineDenied TelemetryCode = "ECI_COORDINATOR_CLEANUP_PIPELINE_DENIED"
	// TelemetryCodeCoordinatorControlPipelineDenied identifies control pipeline denial.
	TelemetryCodeCoordinatorControlPipelineDenied TelemetryCode = "ECI_COORDINATOR_CONTROL_PIPELINE_DENIED"
	// TelemetryCodeCoordinatorRouteArgumentsDenied identifies coordinator route argument denial.
	TelemetryCodeCoordinatorRouteArgumentsDenied TelemetryCode = "ECI_COORDINATOR_ROUTE_ARGUMENTS_DENIED"
	// TelemetryCodeCoordinatorSourceWriteDenied identifies coordinator source-write denial.
	TelemetryCodeCoordinatorSourceWriteDenied TelemetryCode = "ECI_COORDINATOR_SOURCE_WRITE_DENIED"
	// TelemetryCodeEnvironmentContextDenied identifies environment context denial.
	TelemetryCodeEnvironmentContextDenied TelemetryCode = "ECI_ENVIRONMENT_CONTEXT_DENIED"
	// TelemetryCodeEnvironmentEnumerationDenied identifies environment enumeration denial.
	TelemetryCodeEnvironmentEnumerationDenied TelemetryCode = "ECI_ENVIRONMENT_ENUMERATION_DENIED"
	// TelemetryCodeEnvironmentNameDenied identifies environment-name denial.
	TelemetryCodeEnvironmentNameDenied TelemetryCode = "ECI_ENVIRONMENT_NAME_DENIED"
	// TelemetryCodeEnvironmentOptionDenied identifies environment-option denial.
	TelemetryCodeEnvironmentOptionDenied TelemetryCode = "ECI_ENVIRONMENT_OPTION_DENIED"
	// TelemetryCodeGitBranchRemoteDenied identifies remote branch inspection denial.
	TelemetryCodeGitBranchRemoteDenied TelemetryCode = "ECI_GIT_BRANCH_REMOTE_DENIED"
	// TelemetryCodeGitDynamicExecutionDenied identifies dynamic git execution denial.
	TelemetryCodeGitDynamicExecutionDenied TelemetryCode = "ECI_GIT_DYNAMIC_EXECUTION_DENIED"
	// TelemetryCodeGitExecutionContextDenied identifies git execution-context denial.
	TelemetryCodeGitExecutionContextDenied TelemetryCode = "ECI_GIT_EXECUTION_CONTEXT_DENIED"
	// TelemetryCodeGitMutationDenied identifies git mutation denial.
	TelemetryCodeGitMutationDenied TelemetryCode = "ECI_GIT_MUTATION_DENIED"
	// TelemetryCodeHookIdentityMalformed identifies malformed hook identity.
	TelemetryCodeHookIdentityMalformed TelemetryCode = "ECI_HOOK_IDENTITY_MALFORMED"
	// TelemetryCodeLifecycleArgumentsDenied identifies lifecycle argument denial.
	TelemetryCodeLifecycleArgumentsDenied TelemetryCode = "ECI_LIFECYCLE_ARGUMENTS_DENIED"
	// TelemetryCodeLifecycleIdentityDenied identifies lifecycle identity denial.
	TelemetryCodeLifecycleIdentityDenied TelemetryCode = "ECI_LIFECYCLE_IDENTITY_DENIED"
	// TelemetryCodeLifecycleOwnerRequired identifies lifecycle ownership denial.
	TelemetryCodeLifecycleOwnerRequired TelemetryCode = "ECI_LIFECYCLE_OWNER_REQUIRED"
	// TelemetryCodeMarkerMalformed identifies malformed marker state.
	TelemetryCodeMarkerMalformed TelemetryCode = "ECI_MARKER_MALFORMED"
	// TelemetryCodeMarkerMissingCurrent identifies a missing current marker.
	TelemetryCodeMarkerMissingCurrent TelemetryCode = "ECI_MARKER_MISSING_CURRENT"
	// TelemetryCodeMarkerOwnershipAmbiguous identifies ambiguous marker ownership.
	TelemetryCodeMarkerOwnershipAmbiguous TelemetryCode = "ECI_MARKER_OWNERSHIP_AMBIGUOUS"
	// TelemetryCodeMarkerOwnershipInvalid identifies invalid marker ownership.
	TelemetryCodeMarkerOwnershipInvalid TelemetryCode = "ECI_MARKER_OWNERSHIP_INVALID"
	// TelemetryCodeMarkerScopeMismatch identifies marker scope mismatch.
	TelemetryCodeMarkerScopeMismatch TelemetryCode = "ECI_MARKER_SCOPE_MISMATCH"
	// TelemetryCodeMarkerUnsafePath identifies an unsafe marker path.
	TelemetryCodeMarkerUnsafePath TelemetryCode = "ECI_MARKER_UNSAFE_PATH"
	// TelemetryCodePlanDynamicLaunchDenied identifies dynamic plan launch denial.
	TelemetryCodePlanDynamicLaunchDenied TelemetryCode = "ECI_PLAN_DYNAMIC_LAUNCH_DENIED"
	// TelemetryCodePlanInternalDenied identifies internal plan denial.
	TelemetryCodePlanInternalDenied TelemetryCode = "ECI_PLAN_INTERNAL_DENIED"
	// TelemetryCodePlanLifecycleIdentityDenied identifies plan lifecycle identity denial.
	TelemetryCodePlanLifecycleIdentityDenied TelemetryCode = "ECI_PLAN_LIFECYCLE_IDENTITY_DENIED"
	// TelemetryCodePlanLimitDenied identifies plan limit denial.
	TelemetryCodePlanLimitDenied TelemetryCode = "ECI_PLAN_LIMIT_DENIED"
	// TelemetryCodePlanLiveControlDenied identifies live plan control denial.
	TelemetryCodePlanLiveControlDenied TelemetryCode = "ECI_PLAN_LIVE_CONTROL_DENIED"
	// TelemetryCodePlanSyntaxDenied identifies plan syntax denial.
	TelemetryCodePlanSyntaxDenied TelemetryCode = "ECI_PLAN_SYNTAX_DENIED"
	// TelemetryCodePlanWrapperDenied identifies denied plan wrapper use.
	TelemetryCodePlanWrapperDenied TelemetryCode = "ECI_PLAN_WRAPPER_DENIED"
	// TelemetryCodePlanWrapperDepthDenied identifies excessive plan wrapper depth.
	TelemetryCodePlanWrapperDepthDenied TelemetryCode = "ECI_PLAN_WRAPPER_DEPTH_DENIED"
	// TelemetryCodeProofPathEscapeDenied identifies proof path escape denial.
	TelemetryCodeProofPathEscapeDenied TelemetryCode = "ECI_PROOF_PATH_ESCAPE_DENIED"
	// TelemetryCodeReviewGateArgumentsDenied identifies review-gate argument denial.
	TelemetryCodeReviewGateArgumentsDenied TelemetryCode = "ECI_REVIEW_GATE_ARGUMENTS_DENIED"
	// TelemetryCodeWorkerAcceptanceDenied identifies worker acceptance denial.
	TelemetryCodeWorkerAcceptanceDenied TelemetryCode = "ECI_WORKER_ACCEPTANCE_DENIED"
	// TelemetryCodeWorkerCommandNotAllowlisted identifies worker command grammar denial.
	TelemetryCodeWorkerCommandNotAllowlisted TelemetryCode = "ECI_WORKER_COMMAND_NOT_ALLOWLISTED"
	// TelemetryCodeWorkerControlReadDenied identifies worker control-read denial.
	TelemetryCodeWorkerControlReadDenied TelemetryCode = "ECI_WORKER_CONTROL_READ_DENIED"
	// TelemetryCodeWorkerControlScriptDenied identifies worker control-script denial.
	TelemetryCodeWorkerControlScriptDenied TelemetryCode = "ECI_WORKER_CONTROL_SCRIPT_DENIED"
	// TelemetryCodeWorkerCoordinatorRouteDenied identifies worker coordinator-route denial.
	TelemetryCodeWorkerCoordinatorRouteDenied TelemetryCode = "ECI_WORKER_COORDINATOR_ROUTE_DENIED"
	// TelemetryCodeWorkerGitOwnershipDenied identifies worker git-ownership denial.
	TelemetryCodeWorkerGitOwnershipDenied TelemetryCode = "ECI_WORKER_GIT_OWNERSHIP_DENIED"
	// TelemetryCodeWorkerInstructionReadDenied identifies worker instruction-read denial.
	TelemetryCodeWorkerInstructionReadDenied TelemetryCode = "ECI_WORKER_INSTRUCTION_READ_DENIED"
	// TelemetryCodeWorkerLauncherDenied identifies worker launcher denial.
	TelemetryCodeWorkerLauncherDenied TelemetryCode = "ECI_WORKER_LAUNCHER_DENIED"
	// TelemetryCodeWorkerReviewGateDenied identifies worker review-gate denial.
	TelemetryCodeWorkerReviewGateDenied TelemetryCode = "ECI_WORKER_REVIEW_GATE_DENIED"
	// TelemetryCodeOther is the closed fallback for an unknown denial code.
	TelemetryCodeOther TelemetryCode = "ECI_OTHER_DENIAL"
)

// TelemetryOperation identifies the closed operation class persisted by permissive mode.
//
// Example: TelemetryOperationOther represents an operation not registered in the schema.
type TelemetryOperation string

const (
	// TelemetryOperationAcceptanceBoundary identifies command acceptance checks.
	TelemetryOperationAcceptanceBoundary TelemetryOperation = "acceptance-boundary"
	// TelemetryOperationBroadDestructive identifies broad-destructive checks.
	TelemetryOperationBroadDestructive TelemetryOperation = "broad-destructive"
	// TelemetryOperationCommitBoundary identifies commit admission checks.
	TelemetryOperationCommitBoundary TelemetryOperation = "commit-boundary"
	// TelemetryOperationCoordinatorCleanupRoute identifies coordinator cleanup routing.
	TelemetryOperationCoordinatorCleanupRoute TelemetryOperation = "coordinator-cleanup-route"
	// TelemetryOperationCoordinatorMktemp identifies coordinator temporary-directory routing.
	TelemetryOperationCoordinatorMktemp TelemetryOperation = "coordinator-mktemp"
	// TelemetryOperationCoordinatorRoute identifies coordinator routing.
	TelemetryOperationCoordinatorRoute TelemetryOperation = "coordinator-route"
	// TelemetryOperationCoordinatorSourceWrite identifies coordinator source writes.
	TelemetryOperationCoordinatorSourceWrite TelemetryOperation = "coordinator-source-write"
	// TelemetryOperationCoordinatorStaticPipeline identifies static coordinator pipeline checks.
	TelemetryOperationCoordinatorStaticPipeline TelemetryOperation = "coordinator-static-pipeline"
	// TelemetryOperationDirectArgv identifies direct argv checks.
	TelemetryOperationDirectArgv TelemetryOperation = "direct-argv"
	// TelemetryOperationECIControl identifies ECI control operations.
	TelemetryOperationECIControl TelemetryOperation = "eci-control"
	// TelemetryOperationECILifecycle identifies ECI lifecycle operations.
	TelemetryOperationECILifecycle TelemetryOperation = "eci-lifecycle"
	// TelemetryOperationECIOff identifies the inactive ECI route.
	TelemetryOperationECIOff TelemetryOperation = "eci-off"
	// TelemetryOperationEnvironmentBoundary identifies environment boundary checks.
	TelemetryOperationEnvironmentBoundary TelemetryOperation = "environment-boundary"
	// TelemetryOperationGitBranchRemote identifies remote branch checks.
	TelemetryOperationGitBranchRemote TelemetryOperation = "git-branch-remote"
	// TelemetryOperationGitExecutionContext identifies git execution context checks.
	TelemetryOperationGitExecutionContext TelemetryOperation = "git-execution-context"
	// TelemetryOperationHookIdentity identifies hook identity checks.
	TelemetryOperationHookIdentity TelemetryOperation = "hook-identity"
	// TelemetryOperationPlanSegment identifies plan segment checks.
	TelemetryOperationPlanSegment TelemetryOperation = "plan-segment"
	// TelemetryOperationProofPathOwnership identifies proof path ownership checks.
	TelemetryOperationProofPathOwnership TelemetryOperation = "proof-path-ownership"
	// TelemetryOperationReviewGate identifies review-gate checks.
	TelemetryOperationReviewGate TelemetryOperation = "review-gate"
	// TelemetryOperationWorkerAcceptance identifies worker acceptance checks.
	TelemetryOperationWorkerAcceptance TelemetryOperation = "worker-acceptance"
	// TelemetryOperationWorkerCommand identifies worker command checks.
	TelemetryOperationWorkerCommand TelemetryOperation = "worker-command"
	// TelemetryOperationWorkerControl identifies worker control checks.
	TelemetryOperationWorkerControl TelemetryOperation = "worker-control"
	// TelemetryOperationWorkerControlRead identifies worker control-read checks.
	TelemetryOperationWorkerControlRead TelemetryOperation = "worker-control-read"
	// TelemetryOperationWorkerControlScript identifies worker control-script checks.
	TelemetryOperationWorkerControlScript TelemetryOperation = "worker-control-script"
	// TelemetryOperationWorkerGitOwnership identifies worker git-ownership checks.
	TelemetryOperationWorkerGitOwnership TelemetryOperation = "worker-git-ownership"
	// TelemetryOperationWorkerInstructionRead identifies worker instruction-read checks.
	TelemetryOperationWorkerInstructionRead TelemetryOperation = "worker-instruction-read"
	// TelemetryOperationWorkerLauncher identifies worker launcher checks.
	TelemetryOperationWorkerLauncher TelemetryOperation = "worker-launcher"
	// TelemetryOperationWorkerReviewGate identifies worker review-gate checks.
	TelemetryOperationWorkerReviewGate TelemetryOperation = "worker-review-gate"
	// TelemetryOperationOther is the closed fallback for an unknown operation.
	TelemetryOperationOther TelemetryOperation = "other"
)

// Denial is the identity and diagnostic reason extracted from one hook denial document.
//
// Example: ParseDenial returns the closed code/operation plus the unbounded reason text.
type Denial struct {
	Raw       []byte
	Code      TelemetryCode
	Operation TelemetryOperation
	Reason    string
}

// TelemetryBytes returns the stable reduced representation used by redaction tests.
//
// Example: the returned bytes contain only closed diagnostic fields.
func (denial Denial) TelemetryBytes() []byte {
	return []byte(string(denial.Code) + "\n" + string(denial.Operation) + "\n")
}

// EventContext binds a denial to its provider, ECI ownership state, and command context.
//
// Example: EventContext{Provider: ProviderOmp, Command: "git add -A"} records worker provenance.
type EventContext struct {
	Provider    Provider
	Role        Role
	Marker      Marker
	Source      Source
	ConfigState ConfigState
	Command     string
	CWD         string
	SessionID   string
}

// Mode identifies the command-gate enforcement mode.
//
// Example: ModePermissive records would-deny events while allowing callbacks.
type Mode string

const (
	// ModePermissive records would-deny events without blocking callbacks.
	ModePermissive Mode = "permissive"
	// ModeEnforcing blocks denied callbacks while preserving their denial bytes.
	ModeEnforcing Mode = "enforcing"
)

// ConfigState identifies why a mode value was or was not accepted.
//
// Example: ConfigStateMissing records the intentionally permissive absent-file default.
type ConfigState string

const (
	// ConfigStateMissing identifies an absent configuration path.
	ConfigStateMissing ConfigState = "missing"
	// ConfigStateConfiguredPermissive identifies a valid permissive record.
	ConfigStateConfiguredPermissive ConfigState = "configured-permissive"
	// ConfigStateConfiguredEnforcing identifies a valid enforcing record.
	ConfigStateConfiguredEnforcing ConfigState = "configured-enforcing"
	// ConfigStateInvalidPath identifies a symlink or traversal path failure.
	ConfigStateInvalidPath ConfigState = "invalid-path"
	// ConfigStateInvalidMetadata identifies unsafe ownership, type, mode, or link count.
	ConfigStateInvalidMetadata ConfigState = "invalid-metadata"
	// ConfigStateReadError identifies a bounded configuration read failure.
	ConfigStateReadError ConfigState = "read-error"
	// ConfigStateOversize identifies a record larger than MaxConfigBytes.
	ConfigStateOversize ConfigState = "oversize"
	// ConfigStateInvalidBytes identifies a record with an unsupported exact byte value.
	ConfigStateInvalidBytes ConfigState = "invalid-bytes"
)

// ModeState is the effective mode and the state explaining its source.
//
// Example: ModeState{Mode: ModePermissive, ConfigState: ConfigStateInvalidBytes} retains the diagnostic without blocking ordinary work.
type ModeState struct {
	Mode        Mode
	ConfigState ConfigState
}

// Provider identifies the provider adapter supplying a finalizer request.
//
// Example: ProviderOmp identifies the omp hook adapter.
type Provider string

const (
	// ProviderOmp identifies the omp adapter.
	ProviderOmp Provider = "omp"
	// ProviderKimi identifies the Kimi adapter.
	ProviderKimi Provider = "kimi"
)

// Role identifies the owner of a finalizer request.
//
// Example: RoleWorker identifies a delegated worker callback.
type Role string

const (
	// RoleCoordinator identifies the main coordinator callback.
	RoleCoordinator Role = "coordinator"
	// RoleWorker identifies a delegated worker callback.
	RoleWorker Role = "worker"
)

// Marker identifies whether the callback has an active ECI marker.
//
// Example: MarkerInactive identifies a callback outside active ECI ownership.
type Marker string

const (
	// MarkerActive identifies an active ECI callback.
	MarkerActive Marker = "active"
	// MarkerInactive identifies an inactive ECI callback.
	MarkerInactive Marker = "inactive"
)

// Source identifies the validator path that produced a denial.
//
// Example: SourceParser identifies a compiled command-plan denial.
type Source string

const (
	// SourceParser identifies the command-plan parser route.
	SourceParser Source = "parser"
	// SourceLegacy identifies the legacy validator route.
	SourceLegacy Source = "legacy"
)

// CommandKind identifies one supported command-line operation.
//
// Example: CommandFinalize identifies a denial-finalization operation.
type CommandKind string

const (
	// CommandGet reads the configured enforcement mode.
	CommandGet CommandKind = "get"
	// CommandSet changes the configured enforcement mode.
	CommandSet CommandKind = "set"
	// CommandFinalize reduces or forwards one denial document.
	CommandFinalize CommandKind = "finalize"
)

// Command is the validated, provider-neutral command-line request.
//
// Example: Command{Kind: CommandSet, Mode: ModeEnforcing} represents `set enforcing`.
type Command struct {
	Kind     CommandKind
	Mode     Mode
	Provider Provider
	Role     Role
	Marker   Marker
	Source   Source
}

// UsageError identifies a command-line shape or enum value outside the contract.
//
// Example: callers map UsageError to the compatibility exit status 2.
type UsageError struct {
	Reason string
}

// Error returns the stable human-readable usage failure.
//
// Example: the CLI can print this reason beside its usage text.
func (err *UsageError) Error() string {
	return err.Reason
}

// ParseArguments validates the exact command-gate mode CLI grammar.
//
// Example: ParseArguments([]string{"finalize", "omp", "worker", "active", "parser"}).
func ParseArguments(args []string) (Command, error) {
	switch {
	case len(args) == 1 && args[0] == string(CommandGet):
		return Command{Kind: CommandGet}, nil
	case len(args) == 2 && args[0] == string(CommandSet):
		mode, ok := parseMode(args[1])
		if !ok {
			return Command{}, &UsageError{Reason: fmt.Sprintf("unsupported mode %q", args[1])}
		}
		return Command{Kind: CommandSet, Mode: mode}, nil
	case len(args) == 5 && args[0] == string(CommandFinalize):
		provider, providerOK := parseProvider(args[1])
		role, roleOK := parseRole(args[2])
		marker, markerOK := parseMarker(args[3])
		source, sourceOK := parseSource(args[4])
		if !providerOK || !roleOK || !markerOK || !sourceOK {
			return Command{}, &UsageError{Reason: "unsupported finalize argument"}
		}
		return Command{
			Kind:     CommandFinalize,
			Provider: provider,
			Role:     role,
			Marker:   marker,
			Source:   source,
		}, nil
	default:
		return Command{}, &UsageError{Reason: "unsupported command shape"}
	}
}

// parseMode converts one published mode spelling into its closed type.
//
// Example: parseMode("permissive") returns ModePermissive and true.
func parseMode(value string) (Mode, bool) {
	switch Mode(value) {
	case ModePermissive, ModeEnforcing:
		return Mode(value), true
	default:
		return "", false
	}
}

// parseProvider converts one published provider spelling into its closed type.
//
// Example: parseProvider("omp") returns ProviderOmp and true.
func parseProvider(value string) (Provider, bool) {
	switch Provider(value) {
	case ProviderOmp, ProviderKimi:
		return Provider(value), true
	default:
		return "", false
	}
}

// parseRole converts one published role spelling into its closed type.
//
// Example: parseRole("worker") returns RoleWorker and true.
func parseRole(value string) (Role, bool) {
	switch Role(value) {
	case RoleCoordinator, RoleWorker:
		return Role(value), true
	default:
		return "", false
	}
}

// parseMarker converts one published marker spelling into its closed type.
//
// Example: parseMarker("active") returns MarkerActive and true.
func parseMarker(value string) (Marker, bool) {
	switch Marker(value) {
	case MarkerActive, MarkerInactive:
		return Marker(value), true
	default:
		return "", false
	}
}

// parseSource converts one published denial-source spelling into its closed type.
//
// Example: parseSource("legacy") returns SourceLegacy and true.
func parseSource(value string) (Source, bool) {
	switch Source(value) {
	case SourceParser, SourceLegacy:
		return Source(value), true
	default:
		return "", false
	}
}
