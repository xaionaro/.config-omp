//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const (
	// telemetryDirectoryMode is the exact owner-only mode for state descendants.
	telemetryDirectoryMode = 0o700
	// telemetryFileMode is the exact owner-only mode for telemetry files.
	telemetryFileMode = 0o600
	// telemetryDirectoryOpenFlags prevent descriptor traversal through aliases.
	telemetryDirectoryOpenFlags = syscall.O_RDONLY | syscall.O_DIRECTORY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | syscall.O_CLOEXEC
	// telemetryReadFlags open fixed generations without following aliases.
	telemetryReadFlags = syscall.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | syscall.O_CLOEXEC
	// telemetryAppendFlags append to the active generation without following aliases.
	telemetryAppendFlags = syscall.O_WRONLY | syscall.O_APPEND | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | syscall.O_CLOEXEC
	// telemetryLockFlags open the nonblocking rotation lock without following aliases.
	telemetryLockFlags = syscall.O_RDWR | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | syscall.O_CLOEXEC
)

var (
	// denialCodePattern recognizes the compiler-like bracketed denial identity.
	denialCodePattern = regexp.MustCompile(`^\[([A-Z0-9_]+)\]`)
	// denialOperationPattern recognizes the bounded operation field in a reason.
	denialOperationPattern = regexp.MustCompile(`(?:^|,|\()\s*operation=([^,)]+)`)
	// telemetryCodeRegistry is the closed set of codes accepted from denial text.
	telemetryCodeRegistry = map[TelemetryCode]struct{}{
		TelemetryCodeBroadDestructiveDenied:           {},
		TelemetryCodeCommandDynamicIndirectionDenied:  {},
		TelemetryCodeCommandNonliteralDenied:          {},
		TelemetryCodeCommandNotAllowlisted:            {},
		TelemetryCodeCommandSyntaxDenied:              {},
		TelemetryCodeCommandWrapperUnsupported:        {},
		TelemetryCodeCommitAdmissionRequired:          {},
		TelemetryCodeControlIdentityDenied:            {},
		TelemetryCodeControlOwnerRequired:             {},
		TelemetryCodeCoordinatorCleanupPipelineDenied: {},
		TelemetryCodeCoordinatorControlPipelineDenied: {},
		TelemetryCodeCoordinatorRouteArgumentsDenied:  {},
		TelemetryCodeCoordinatorSourceWriteDenied:     {},
		TelemetryCodeEnvironmentContextDenied:         {},
		TelemetryCodeEnvironmentEnumerationDenied:     {},
		TelemetryCodeEnvironmentNameDenied:            {},
		TelemetryCodeEnvironmentOptionDenied:          {},
		TelemetryCodeGitBranchRemoteDenied:            {},
		TelemetryCodeGitDynamicExecutionDenied:        {},
		TelemetryCodeGitExecutionContextDenied:        {},
		TelemetryCodeGitMutationDenied:                {},
		TelemetryCodeHookIdentityMalformed:            {},
		TelemetryCodeLifecycleArgumentsDenied:         {},
		TelemetryCodeLifecycleIdentityDenied:          {},
		TelemetryCodeLifecycleOwnerRequired:           {},
		TelemetryCodeMarkerMalformed:                  {},
		TelemetryCodeMarkerMissingCurrent:             {},
		TelemetryCodeMarkerOwnershipAmbiguous:         {},
		TelemetryCodeMarkerOwnershipInvalid:           {},
		TelemetryCodeMarkerScopeMismatch:              {},
		TelemetryCodeMarkerUnsafePath:                 {},
		TelemetryCodePlanDynamicLaunchDenied:          {},
		TelemetryCodePlanInternalDenied:               {},
		TelemetryCodePlanLifecycleIdentityDenied:      {},
		TelemetryCodePlanLimitDenied:                  {},
		TelemetryCodePlanLiveControlDenied:            {},
		TelemetryCodePlanSyntaxDenied:                 {},
		TelemetryCodePlanWrapperDenied:                {},
		TelemetryCodePlanWrapperDepthDenied:           {},
		TelemetryCodeProofPathEscapeDenied:            {},
		TelemetryCodeReviewGateArgumentsDenied:        {},
		TelemetryCodeWorkerAcceptanceDenied:           {},
		TelemetryCodeWorkerCommandNotAllowlisted:      {},
		TelemetryCodeWorkerControlReadDenied:          {},
		TelemetryCodeWorkerControlScriptDenied:        {},
		TelemetryCodeWorkerCoordinatorRouteDenied:     {},
		TelemetryCodeWorkerGitOwnershipDenied:         {},
		TelemetryCodeWorkerInstructionReadDenied:      {},
		TelemetryCodeWorkerLauncherDenied:             {},
		TelemetryCodeWorkerReviewGateDenied:           {},
		TelemetryCodeOther:                            {},
	}
	// telemetryOperationRegistry is the closed set of operations accepted from denial text.
	telemetryOperationRegistry = map[TelemetryOperation]struct{}{
		TelemetryOperationAcceptanceBoundary:        {},
		TelemetryOperationBroadDestructive:          {},
		TelemetryOperationCommitBoundary:            {},
		TelemetryOperationCoordinatorCleanupRoute:   {},
		TelemetryOperationCoordinatorMktemp:         {},
		TelemetryOperationCoordinatorRoute:          {},
		TelemetryOperationCoordinatorSourceWrite:    {},
		TelemetryOperationCoordinatorStaticPipeline: {},
		TelemetryOperationDirectArgv:                {},
		TelemetryOperationECIControl:                {},
		TelemetryOperationECILifecycle:              {},
		TelemetryOperationECIOff:                    {},
		TelemetryOperationEnvironmentBoundary:       {},
		TelemetryOperationGitBranchRemote:           {},
		TelemetryOperationGitExecutionContext:       {},
		TelemetryOperationHookIdentity:              {},
		TelemetryOperationPlanSegment:               {},
		TelemetryOperationProofPathOwnership:        {},
		TelemetryOperationReviewGate:                {},
		TelemetryOperationWorkerAcceptance:          {},
		TelemetryOperationWorkerCommand:             {},
		TelemetryOperationWorkerControl:             {},
		TelemetryOperationWorkerControlRead:         {},
		TelemetryOperationWorkerControlScript:       {},
		TelemetryOperationWorkerGitOwnership:        {},
		TelemetryOperationWorkerInstructionRead:     {},
		TelemetryOperationWorkerLauncher:            {},
		TelemetryOperationWorkerReviewGate:          {},
		TelemetryOperationOther:                     {},
	}
)

// TelemetryCode is the finite set of persisted denial-code identities.
//
// Example: known codes may be added without changing raw-denial storage semantics.
type telemetryCodeSet struct{}

// RotationOutcome records whether an append rotated, appended, or bypassed a contended lock.
//
// Example: RotationOutcomeLockContended proves the append did not wait for another writer.
type RotationOutcome string

const (
	// RotationOutcomeAppended identifies an append without rotation.
	RotationOutcomeAppended RotationOutcome = "appended"
	// RotationOutcomeRotated identifies an append preceded by fixed-family rotation.
	RotationOutcomeRotated RotationOutcome = "rotated"
	// RotationOutcomeLockContended identifies the nonblocking lock fallback.
	RotationOutcomeLockContended RotationOutcome = "lock-contended"
)

// TelemetryStore identifies the user-owned state root containing command-gate telemetry.
//
// Example: TelemetryStore{Root: "/home/user/.local/state"} writes under `eci/command-gate`.
type TelemetryStore struct {
	Root string

	logicalRoot       string
	canonicalRoot     string
	allowLogicalAlias bool
}

// ParseDenial reduces a hook denial to closed diagnostic identities.
//
// Example: unknown code and operation values become ECI_OTHER_DENIAL and other.
func ParseDenial(raw []byte) (Denial, error) {
	if len(raw) == 0 || len(raw) > MaxDenialBytes {
		return Denial{}, &TelemetryReductionError{Reason: "denial size rejected"}
	}
	if !utf8.Valid(raw) {
		return Denial{}, &TelemetryReductionError{Reason: "denial document rejected"}
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil || document == nil {
		return Denial{}, &TelemetryReductionError{Reason: "denial document rejected", Err: err}
	}
	outputBytes, ok := document["hookSpecificOutput"]
	if !ok {
		return Denial{}, &TelemetryReductionError{Reason: "denial document rejected"}
	}
	var output map[string]json.RawMessage
	if err := json.Unmarshal(outputBytes, &output); err != nil || output == nil {
		return Denial{}, &TelemetryReductionError{Reason: "denial document rejected", Err: err}
	}
	reasonBytes, ok := output["permissionDecisionReason"]
	if !ok {
		return Denial{}, &TelemetryReductionError{Reason: "denial document rejected"}
	}
	decisionBytes, ok := output["permissionDecision"]
	if !ok {
		return Denial{}, &TelemetryReductionError{Reason: "denial document rejected"}
	}
	var reason string
	reasonErr := json.Unmarshal(reasonBytes, &reason)
	var decision string
	decisionErr := json.Unmarshal(decisionBytes, &decision)
	if decisionErr != nil || decision != "deny" || reasonErr != nil || reason == "" {
		return Denial{}, &TelemetryReductionError{Reason: "denial shape rejected"}
	}
	codeMatches := denialCodePattern.FindStringSubmatch(reason)
	operationMatches := denialOperationPattern.FindStringSubmatch(reason)
	if len(codeMatches) != 2 || len(operationMatches) != 2 {
		return Denial{}, &TelemetryReductionError{Reason: "diagnostic identity rejected"}
	}
	code := TelemetryCode(codeMatches[1])
	if _, ok := telemetryCodeRegistry[code]; !ok {
		code = TelemetryCodeOther
	}
	operation := TelemetryOperation(operationMatches[1])
	if _, ok := telemetryOperationRegistry[operation]; !ok {
		operation = TelemetryOperationOther
	}
	return Denial{Raw: append([]byte(nil), raw...), Code: code, Operation: operation}, nil
}

// TelemetryReductionError identifies a denial that cannot be reduced safely.
//
// Example: permissive finalization converts this error into the fixed warning.
type TelemetryReductionError struct {
	Reason string
	Err    error
}

// Error returns the bounded reduction failure description.
//
// Example: callers can log the reason without retaining denial bytes.
func (err *TelemetryReductionError) Error() string {
	return err.Reason
}

// Unwrap exposes only the underlying parser error for typed inspection.
//
// Example: errors.Is can distinguish a JSON parser failure if a caller needs it.
func (err *TelemetryReductionError) Unwrap() error {
	return err.Err
}

// TelemetryUnavailable writes the fixed warning and suppresses broken stderr sinks.
//
// Example: permissive mode remains successful when telemetry storage is unavailable.
func TelemetryUnavailable(writer io.Writer) {
	if writer == nil {
		return
	}
	_, _ = writer.Write([]byte(TelemetryUnavailableMessage))
}

// TelemetryStoreFromEnvironment resolves the provider-neutral state root.
//
// Example: XDG_STATE_HOME is accepted only as an absolute lexical path; the HOME default may preserve a stable `.local` alias.
func TelemetryStoreFromEnvironment() (TelemetryStore, error) {
	if configured := os.Getenv("XDG_STATE_HOME"); configured != "" {
		if err := validateAbsoluteNormalizedPath(configured); err != nil {
			return TelemetryStore{}, err
		}
		return TelemetryStore{
			Root:              configured,
			logicalRoot:       configured,
			canonicalRoot:     configured,
			allowLogicalAlias: false,
		}, nil
	}
	home := os.Getenv("HOME")
	if home == "" {
		return TelemetryStore{}, &configPathError{state: ConfigStateInvalidPath, err: errors.New("HOME is missing")}
	}
	if err := validateAbsoluteNormalizedPath(home); err != nil {
		return TelemetryStore{}, err
	}
	logical := filepath.Join(home, ".local", "state")
	first, err := resolvePathWithMissing(logical)
	if err != nil {
		return TelemetryStore{}, &configPathError{state: ConfigStateInvalidPath, err: err}
	}
	second, err := resolvePathWithMissing(logical)
	if err != nil {
		return TelemetryStore{}, &configPathError{state: ConfigStateInvalidPath, err: err}
	}
	if first != second {
		return TelemetryStore{}, &configPathError{state: ConfigStateInvalidPath, err: errors.New("default state alias resolution changed")}
	}
	return TelemetryStore{
		Root:              first,
		logicalRoot:       logical,
		canonicalRoot:     first,
		allowLogicalAlias: true,
	}, nil
}

// resolvePathWithMissing resolves existing aliases while retaining a missing suffix.
//
// Example: `/home/user/.local/state` resolves `.local` but still admits a missing `state` child for creation.
func resolvePathWithMissing(path string) (string, error) {
	if err := validateAbsoluteNormalizedPath(path); err != nil {
		return "", err
	}
	current := string(filepath.Separator)
	components := strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator))
	for index, component := range components {
		candidate := filepath.Join(current, component)
		resolved, err := filepath.EvalSymlinks(candidate)
		switch {
		case err == nil:
			current = resolved
		case errors.Is(err, os.ErrNotExist):
			return filepath.Clean(filepath.Join(current, filepath.Join(components[index:]...))), nil
		default:
			return "", err
		}
	}
	return filepath.Clean(current), nil
}

// telemetryRootHandle keeps the state-root identity required after an append.
//
// Example: a replacement of the named state directory is detected before the finalizer returns.
type telemetryRootHandle struct {
	directory     *configDirectoryHandle
	logicalRoot   string
	canonicalRoot string
	allowAlias    bool
}

// telemetryLocation owns the deepest directory descriptor and therefore the entire descriptor chain.
//
// Example: closing the log directory releases the log, eci, and state-root descriptors together.
type telemetryLocation struct {
	root   telemetryRootHandle
	logDir *configDirectoryHandle
}

// openLogDirectory opens and validates the fixed telemetry directory hierarchy.
//
// Example: state root, `eci`, and `command-gate` are each opened without following aliases.
func (store TelemetryStore) openLogDirectory() (*telemetryLocation, error) {
	logicalRoot, canonicalRoot, allowAlias, err := store.roots()
	if err != nil {
		return nil, err
	}
	root, err := openDirectoryPath(canonicalRoot, true)
	if err != nil {
		return nil, err
	}
	rootHandle := telemetryRootHandle{
		directory:     root,
		logicalRoot:   logicalRoot,
		canonicalRoot: canonicalRoot,
		allowAlias:    allowAlias,
	}
	if err := rootHandle.validate(); err != nil {
		root.close()
		return nil, err
	}
	eciDirectory, err := openOwnedDirectory(root, TelemetryParentDirectoryName, true)
	if err != nil {
		root.close()
		return nil, err
	}
	logDirectory, err := openOwnedDirectory(eciDirectory, TelemetryDirectoryName, true)
	if err != nil {
		eciDirectory.close()
		return nil, err
	}
	location := &telemetryLocation{root: rootHandle, logDir: logDirectory}
	if err := location.root.validate(); err != nil {
		location.close()
		return nil, err
	}
	return location, nil
}

// roots returns normalized store paths, accepting direct test construction as an explicit root.
//
// Example: TelemetryStore{Root: root} remains a strict no-alias state store.
func (store TelemetryStore) roots() (string, string, bool, error) {
	if store.Root == "" {
		return "", "", false, &configPathError{state: ConfigStateInvalidPath, err: errors.New("telemetry root is missing")}
	}
	if store.canonicalRoot != "" {
		return store.logicalRoot, store.canonicalRoot, store.allowLogicalAlias, nil
	}
	if err := validateAbsoluteNormalizedPath(store.Root); err != nil {
		return "", "", false, err
	}
	return store.Root, store.Root, false, nil
}

// close releases the deepest telemetry descriptor chain.
//
// Example: a failed append still releases all opened state descriptors.
func (location *telemetryLocation) close() {
	if location == nil || location.logDir == nil {
		return
	}
	location.logDir.close()
	location.logDir = nil
}

// validate checks state-root ownership, identity, and stable default alias resolution.
//
// Example: replacing the canonical root after opening it is rejected before telemetry completes.
func (root telemetryRootHandle) validate() error {
	if root.directory == nil {
		return &configPathError{state: ConfigStateInvalidPath, err: errors.New("telemetry root descriptor is missing")}
	}
	observed, err := statDescriptor(root.directory.fd)
	if err != nil {
		return &configPathError{state: ConfigStateInvalidPath, err: err}
	}
	if observed.device != root.directory.identity.device || observed.inode != root.directory.identity.inode {
		return &configPathError{state: ConfigStateInvalidPath, err: errors.New("telemetry root descriptor identity changed")}
	}
	if err := validateTelemetryRootMetadata(observed); err != nil {
		return err
	}
	named, err := statPathNoFollow(root.canonicalRoot)
	if err != nil || named.device != observed.device || named.inode != observed.inode {
		if err == nil {
			err = errors.New("telemetry canonical root identity changed")
		}
		return &configPathError{state: ConfigStateInvalidPath, err: err}
	}
	if root.allowAlias {
		first, firstErr := resolvePathWithMissing(root.logicalRoot)
		second, secondErr := resolvePathWithMissing(root.logicalRoot)
		if firstErr != nil || secondErr != nil || first != second || first != root.canonicalRoot {
			if firstErr != nil {
				return &configPathError{state: ConfigStateInvalidPath, err: firstErr}
			}
			if secondErr != nil {
				return &configPathError{state: ConfigStateInvalidPath, err: secondErr}
			}
			return &configPathError{state: ConfigStateInvalidPath, err: errors.New("telemetry logical state root changed target")}
		}
		logical, err := statFollow(root.logicalRoot)
		if err != nil || logical.device != observed.device || logical.inode != observed.inode {
			if err == nil {
				err = errors.New("telemetry logical root identity changed")
			}
			return &configPathError{state: ConfigStateInvalidPath, err: err}
		}
		return nil
	}
	if filepath.Clean(root.logicalRoot) != root.logicalRoot {
		return &configPathError{state: ConfigStateInvalidPath, err: errors.New("telemetry explicit state root is not normalized")}
	}
	return nil
}

// validateTelemetryRootMetadata enforces owner-readable state roots without requiring exact group bits.
//
// Example: a group-readable but not group-writable root remains usable; world-writable roots do not.
func validateTelemetryRootMetadata(identity fileIdentity) error {
	if identity.mode&syscall.S_IFMT != syscall.S_IFDIR || identity.uid != uint32(os.Getuid()) || identity.mode&0o700 != 0o700 || identity.mode&0o022 != 0 || identity.mode&0o7000 != 0 {
		return &configPathError{state: ConfigStateInvalidMetadata, err: errors.New("telemetry state root metadata rejected")}
	}
	return nil
}

// statFollow reads a path identity while following a stable logical alias.
//
// Example: the HOME-derived `.local` symlink is compared to the opened canonical descriptor.
func statFollow(path string) (fileIdentity, error) {
	var metadata syscall.Stat_t
	if err := syscall.Stat(path, &metadata); err != nil {
		return fileIdentity{}, err
	}
	return fileIdentityFromStat(metadata), nil
}

// telemetryPurpose identifies the validation policy for one telemetry file.
//
// Example: telemetryPurposeRotationLock additionally requires a zero-byte lock.
type telemetryPurpose string

const (
	// telemetryPurposeRotationLock identifies the nonblocking lock file.
	telemetryPurposeRotationLock telemetryPurpose = "rotation-lock"
	// telemetryPurposeAppend identifies the active append generation.
	telemetryPurposeAppend telemetryPurpose = "log-append"
	// telemetryPurposeGeneration identifies a retained rotated generation.
	telemetryPurposeGeneration telemetryPurpose = "log-generation"
)

// telemetryFileHandle ties one regular telemetry descriptor to its named directory entry.
//
// Example: a replacement between open and write is rejected by identity validation.
type telemetryFileHandle struct {
	fd       int
	identity fileIdentity
	parentFD int
	name     string
	purpose  telemetryPurpose
}

// close releases one telemetry file descriptor.
//
// Example: closing a generation handle releases it after rotation validation completes.
func (file *telemetryFileHandle) close() {
	if file == nil || file.fd < 0 {
		return
	}
	_ = syscall.Close(file.fd)
	file.fd = -1
}

// openTelemetryFile opens one regular telemetry file with bounded nonblocking flags.
//
// Example: FIFO, socket, symlink, and hard-link entries are rejected before use.
func openTelemetryFile(parent *configDirectoryHandle, name string, purpose telemetryPurpose, create bool) (*telemetryFileHandle, error) {
	if err := validateDirectoryIdentity(parent); err != nil {
		return nil, err
	}
	flags := telemetryReadFlags
	switch purpose {
	case telemetryPurposeRotationLock:
		flags = telemetryLockFlags
	case telemetryPurposeAppend:
		flags = telemetryAppendFlags
	case telemetryPurposeGeneration:
		flags = telemetryReadFlags
	default:
		return nil, errors.New("unsupported telemetry file purpose")
	}
	fd, err := syscall.Openat(parent.fd, name, flags, 0)
	created := false
	if errors.Is(err, syscall.ENOENT) && create {
		fd, err = syscall.Openat(parent.fd, name, flags|syscall.O_CREAT|syscall.O_EXCL, telemetryFileMode)
		created = err == nil
		if errors.Is(err, syscall.EEXIST) {
			fd, err = syscall.Openat(parent.fd, name, flags, 0)
		}
	}
	if err != nil {
		if errors.Is(err, syscall.ENOENT) && !create {
			return nil, err
		}
		return nil, &configPathError{state: ConfigStateInvalidPath, err: err}
	}
	if created {
		if err := syscall.Fchmod(fd, telemetryFileMode); err != nil {
			_ = syscall.Close(fd)
			return nil, &configPathError{state: ConfigStateInvalidMetadata, err: err}
		}
	}
	identity, err := statDescriptor(fd)
	if err != nil {
		_ = syscall.Close(fd)
		return nil, &configPathError{state: ConfigStateInvalidMetadata, err: err}
	}
	file := &telemetryFileHandle{fd: fd, identity: identity, parentFD: parent.fd, name: name, purpose: purpose}
	if err := validateTelemetryIdentity(file, name); err != nil {
		file.close()
		return nil, err
	}
	return file, nil
}

// openOptionalTelemetryFile returns nil only for a missing fixed-generation name.
//
// Example: rotation probes exactly the active name and four numbered generations.
func openOptionalTelemetryFile(parent *configDirectoryHandle, name string, purpose telemetryPurpose) (*telemetryFileHandle, error) {
	file, err := openTelemetryFile(parent, name, purpose, false)
	if errors.Is(err, syscall.ENOENT) {
		return nil, nil
	}
	return file, err
}

// validateTelemetryIdentity validates descriptor metadata and the current named entry.
//
// Example: replacing `would-deny.jsonl` after opening it is detected before append completion.
func validateTelemetryIdentity(file *telemetryFileHandle, name string) error {
	observed, err := statDescriptor(file.fd)
	if err != nil {
		return &configPathError{state: ConfigStateInvalidMetadata, err: err}
	}
	if err := validateTelemetryFileMetadata(observed, file.purpose); err != nil {
		return err
	}
	if observed.device != file.identity.device || observed.inode != file.identity.inode {
		return &configPathError{state: ConfigStateInvalidPath, err: errors.New("telemetry descriptor identity changed")}
	}
	named, err := statNamed(file.parentFD, name)
	if err != nil {
		return &configPathError{state: ConfigStateInvalidPath, err: err}
	}
	if err := validateTelemetryFileMetadata(named, file.purpose); err != nil {
		return err
	}
	if named.device != observed.device || named.inode != observed.inode {
		return &configPathError{state: ConfigStateInvalidPath, err: errors.New("telemetry named identity changed")}
	}
	return nil
}

// validateTelemetryFileMetadata enforces regular, owner-only, single-link telemetry files.
//
// Example: a nonempty rotation lock is rejected before flock to avoid stale state ambiguity.
func validateTelemetryFileMetadata(identity fileIdentity, purpose telemetryPurpose) error {
	if identity.mode&syscall.S_IFMT != syscall.S_IFREG || identity.uid != uint32(os.Getuid()) || identity.mode&0o7777 != telemetryFileMode || identity.nlink != 1 {
		return &configPathError{state: ConfigStateInvalidMetadata, err: errors.New("telemetry file metadata rejected")}
	}
	if purpose == telemetryPurposeRotationLock && identity.size != 0 {
		return &configPathError{state: ConfigStateInvalidMetadata, err: errors.New("rotation lock must be empty")}
	}
	return nil
}

// validateFixedFamily rejects any unsafe fixed-generation entry without enumerating the directory.
//
// Example: only `.rotation.lock`, the active file, and `.1` through `.4` are inspected.
func validateFixedFamily(parent *configDirectoryHandle) error {
	lock, err := openOptionalTelemetryFile(parent, RotationLockName, telemetryPurposeRotationLock)
	if err != nil {
		return err
	}
	if lock != nil {
		lock.close()
	}
	current, err := openOptionalTelemetryFile(parent, TelemetryFileName, telemetryPurposeGeneration)
	if err != nil {
		return err
	}
	if current != nil {
		current.close()
	}
	for index := 1; index <= MaxLogFiles; index++ {
		name := fmt.Sprintf("%s.%d", TelemetryFileName, index)
		generation, err := openOptionalTelemetryFile(parent, name, telemetryPurposeGeneration)
		if err != nil {
			return err
		}
		if generation != nil {
			generation.close()
		}
	}
	return nil
}

// telemetryNameAbsent checks one fixed entry without following aliases.
//
// Example: rotation never overwrites an occupied destination generation.
func telemetryNameAbsent(parent *configDirectoryHandle, name string) (bool, error) {
	_, err := statNamed(parent.fd, name)
	if errors.Is(err, syscall.ENOENT) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

// unlinkTelemetryGeneration removes one validated retained generation.
//
// Example: the oldest `.4` entry is removed only when its descriptor and name still agree.
func unlinkTelemetryGeneration(parent *configDirectoryHandle, file *telemetryFileHandle) error {
	if err := validateTelemetryIdentity(file, file.name); err != nil {
		return err
	}
	if err := syscall.Unlinkat(parent.fd, file.name); err != nil {
		return err
	}
	absent, err := telemetryNameAbsent(parent, file.name)
	if err != nil {
		return err
	}
	if !absent {
		return errors.New("telemetry generation unlink retained its name")
	}
	identity, err := statDescriptor(file.fd)
	if err != nil {
		return err
	}
	if identity.device != file.identity.device || identity.inode != file.identity.inode || identity.nlink != 0 {
		return errors.New("telemetry generation unlink identity changed")
	}
	return nil
}

// renameTelemetryGeneration moves one validated generation to a free fixed destination.
//
// Example: `.3` becomes `.4` only after `.4` has been proven absent.
func renameTelemetryGeneration(parent *configDirectoryHandle, file *telemetryFileHandle, destination string) error {
	if err := validateTelemetryIdentity(file, file.name); err != nil {
		return err
	}
	absent, err := telemetryNameAbsent(parent, destination)
	if err != nil {
		return err
	}
	if !absent {
		return errors.New("telemetry rotation destination is occupied")
	}
	if err := syscall.Renameat(parent.fd, file.name, parent.fd, destination); err != nil {
		return err
	}
	absent, err = telemetryNameAbsent(parent, file.name)
	if err != nil {
		return err
	}
	if !absent {
		return errors.New("telemetry rotation source name persisted")
	}
	return validateTelemetryIdentity(file, destination)
}

// rotateFixedFamily rotates a full active generation through the fixed retention family.
//
// Example: `.4` is discarded, `.3` becomes `.4`, and the active file becomes `.1`.
func rotateFixedFamily(parent *configDirectoryHandle) (bool, error) {
	current, err := openOptionalTelemetryFile(parent, TelemetryFileName, telemetryPurposeGeneration)
	if err != nil {
		return false, err
	}
	if current == nil {
		return false, nil
	}
	defer current.close()
	if current.identity.size < MaxLogBytes {
		return false, nil
	}
	generations := make(map[int]*telemetryFileHandle, MaxLogFiles)
	for index := 1; index <= MaxLogFiles; index++ {
		name := fmt.Sprintf("%s.%d", TelemetryFileName, index)
		generation, err := openOptionalTelemetryFile(parent, name, telemetryPurposeGeneration)
		if err != nil {
			return false, err
		}
		generations[index] = generation
		if generation != nil {
			defer generation.close()
		}
	}
	if oldest := generations[MaxLogFiles]; oldest != nil {
		if err := unlinkTelemetryGeneration(parent, oldest); err != nil {
			return false, err
		}
	}
	for index := MaxLogFiles - 1; index >= 1; index-- {
		if generation := generations[index]; generation != nil {
			if err := renameTelemetryGeneration(parent, generation, fmt.Sprintf("%s.%d", TelemetryFileName, index+1)); err != nil {
				return false, err
			}
		}
	}
	if err := renameTelemetryGeneration(parent, current, TelemetryFileName+".1"); err != nil {
		return false, err
	}
	if err := validateFixedFamily(parent); err != nil {
		return false, err
	}
	return true, nil
}

// validateTelemetryHandleFamily proves an appended descriptor has exactly one retained name.
//
// Example: an active descriptor renamed concurrently remains acceptable only if it is in `.1` through `.4` exactly once.
func validateTelemetryHandleFamily(parent *configDirectoryHandle, file *telemetryFileHandle) error {
	descriptor, err := statDescriptor(file.fd)
	if err != nil {
		return err
	}
	if err := validateTelemetryFileMetadata(descriptor, telemetryPurposeGeneration); err != nil {
		return err
	}
	matches := 0
	for index := 0; index <= MaxLogFiles; index++ {
		name := TelemetryFileName
		if index > 0 {
			name = fmt.Sprintf("%s.%d", TelemetryFileName, index)
		}
		named, err := statNamed(parent.fd, name)
		if errors.Is(err, syscall.ENOENT) {
			continue
		}
		if err != nil {
			return err
		}
		if err := validateTelemetryFileMetadata(named, telemetryPurposeGeneration); err != nil {
			return err
		}
		if named.device == descriptor.device && named.inode == descriptor.inode {
			matches++
		}
	}
	if matches != 1 {
		return errors.New("appended telemetry file has an invalid retained-name count")
	}
	return nil
}

// writeTelemetryRecord writes one complete bounded record to a validated append descriptor.
//
// Example: a short write is retried only for the same descriptor and remains identity-checked afterward.
func writeTelemetryRecord(file *telemetryFileHandle, record []byte) error {
	for written := 0; written < len(record); {
		n, err := syscall.Write(file.fd, record[written:])
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		written += n
	}
	return nil
}

// appendTelemetryRecord appends one record and handles a concurrent rotation race safely.
//
// Example: if the active name moves after the write, the descriptor must be found in exactly one retained generation.
func appendTelemetryRecord(parent *configDirectoryHandle, record []byte) error {
	file, err := openTelemetryFile(parent, TelemetryFileName, telemetryPurposeAppend, true)
	if err != nil {
		return err
	}
	defer file.close()
	if err := validateTelemetryIdentity(file, TelemetryFileName); err != nil {
		return err
	}
	if err := writeTelemetryRecord(file, record); err != nil {
		return err
	}
	if err := validateTelemetryIdentity(file, TelemetryFileName); err != nil {
		if familyErr := validateTelemetryHandleFamily(parent, file); familyErr != nil {
			return err
		}
	}
	return validateDirectoryIdentity(parent)
}

// tryRotateTelemetry obtains the rotation lock without waiting and appends the record.
//
// Example: lock contention falls back to a bounded append and returns RotationOutcomeLockContended.
func tryRotateTelemetry(parent *configDirectoryHandle, record []byte) (RotationOutcome, error) {
	lock, err := openTelemetryFile(parent, RotationLockName, telemetryPurposeRotationLock, true)
	if err != nil {
		return "", err
	}
	defer lock.close()
	if err := validateTelemetryIdentity(lock, RotationLockName); err != nil {
		return "", err
	}
	if err := syscall.Flock(lock.fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			if appendErr := appendTelemetryRecord(parent, record); appendErr != nil {
				return "", appendErr
			}
			return RotationOutcomeLockContended, nil
		}
		return "", err
	}
	if err := validateFixedFamily(parent); err != nil {
		return "", err
	}
	rotated, err := rotateFixedFamily(parent)
	if err != nil {
		return "", err
	}
	if err := appendTelemetryRecord(parent, record); err != nil {
		return "", err
	}
	if err := validateFixedFamily(parent); err != nil {
		return "", err
	}
	if err := validateTelemetryIdentity(lock, RotationLockName); err != nil {
		return "", err
	}
	if rotated {
		return RotationOutcomeRotated, nil
	}
	return RotationOutcomeAppended, nil
}

// AppendEvent reduces and stores one denial event under descriptor-relative state.
//
// Example: AppendEvent never serializes the original command or denial reason.
func (store TelemetryStore) AppendEvent(denial Denial, context EventContext) error {
	location, err := store.openLogDirectory()
	if err != nil {
		return err
	}
	defer location.close()
	event := struct {
		Schema      string `json:"schema"`
		Event       string `json:"event"`
		AtUTC       string `json:"at_utc"`
		Provider    string `json:"provider"`
		Role        string `json:"role"`
		Marker      string `json:"marker"`
		Source      string `json:"source"`
		Code        string `json:"code"`
		Operation   string `json:"operation"`
		ConfigState string `json:"config_state"`
	}{
		Schema:      EventSchema,
		Event:       "would-deny",
		AtUTC:       time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		Provider:    string(context.Provider),
		Role:        string(context.Role),
		Marker:      string(context.Marker),
		Source:      string(context.Source),
		Code:        string(denial.Code),
		Operation:   string(denial.Operation),
		ConfigState: string(context.ConfigState),
	}
	record, err := json.Marshal(event)
	if err != nil {
		return &TelemetryReductionError{Reason: "telemetry event encoding rejected", Err: err}
	}
	record = append(record, '\n')
	if len(record) > MaxEventBytes {
		return &TelemetryReductionError{Reason: "telemetry event exceeds the bounded record size"}
	}
	if _, err := tryRotateTelemetry(location.logDir, record); err != nil {
		return err
	}
	return location.root.validate()
}

// readBoundedDenial reads at most MaxDenialBytes plus one byte and drains the input.
//
// Example: an oversized denial is rejected without leaving unread bytes on the hook pipe.
func readBoundedDenial(reader io.Reader) ([]byte, error) {
	if reader == nil {
		return nil, &TelemetryReductionError{Reason: "denial input is missing"}
	}
	limited := io.LimitReader(reader, MaxDenialBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxDenialBytes {
		_, _ = io.Copy(io.Discard, reader)
		return nil, &TelemetryReductionError{Reason: "denial size rejected"}
	}
	return raw, nil
}

// copyEnforcingInput preserves every denial byte for the enforcing path.
//
// Example: a malformed denial is still forwarded byte-for-byte when enforcement is active.
func copyEnforcingInput(reader io.Reader, writer io.Writer) error {
	if reader == nil || writer == nil {
		return errors.New("enforcing stream is missing")
	}
	_, err := io.Copy(writer, reader)
	return err
}

// finalizeDenial applies the mode contract to one provider-neutral finalizer request.
//
// Example: permissive failures warn and return success; enforcing mode forwards input unchanged.
func finalizeDenial(
	config ConfigStore,
	state TelemetryStore,
	command Command,
	reader io.Reader,
	writer io.Writer,
	warning io.Writer,
) int {
	modeState := config.ReadMode()
	if modeState.Mode == ModeEnforcing {
		_ = copyEnforcingInput(reader, writer)
		return 0
	}
	raw, err := readBoundedDenial(reader)
	if err == nil {
		var denial Denial
		denial, err = ParseDenial(raw)
		if err == nil {
			err = state.AppendEvent(denial, EventContext{
				Provider:    command.Provider,
				Role:        command.Role,
				Marker:      command.Marker,
				Source:      command.Source,
				ConfigState: modeState.ConfigState,
			})
		}
	}
	if err != nil {
		TelemetryUnavailable(warning)
	}
	return 0
}
