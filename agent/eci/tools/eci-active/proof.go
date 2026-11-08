package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	envSessionID = "ECI_SESSION_ID"
	envThreadID  = "ECI_THREAD_ID"
	envProofRoot = "ECI_PROOF_ROOT"
)

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func homeDir() string { return os.Getenv("HOME") }

func configuredProofRoot() string {
	if v := os.Getenv(envProofRoot); v != "" {
		return v
	}
	return filepath.Join(homeDir(), "tmp", "agent", "proof")
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func isSymlink(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}

func isRegularNoFollow(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode().IsRegular()
}

// canonicalPath resolves a directory's ancestors (pwd -P semantics) and
// otherwise returns the cleaned path.
func canonicalPath(p string) string {
	if isDir(p) && !isSymlink(p) {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
	}
	return filepath.Clean(p)
}

// proofRoot is the canonical proof root.
func proofRoot() string { return canonicalPath(configuredProofRoot()) }

// proofRootSafe rejects a symlinked configured root, a non-directory root,
// and a non-directory parent.
func proofRootSafe() error {
	configured := filepath.Clean(configuredProofRoot())
	if !filepath.IsAbs(configured) {
		return fmt.Errorf("ECI proof root must be an absolute path: %s", configured)
	}
	root := proofRoot()
	if !filepath.IsAbs(root) {
		return fmt.Errorf("ECI proof root is unsafe: %s", root)
	}
	if isSymlink(configured) || isSymlink(root) {
		return fmt.Errorf("ECI proof root is unsafe: %s", root)
	}
	if exists(root) && !isDir(root) {
		return fmt.Errorf("ECI proof root is not a directory: %s", root)
	}
	if parent := filepath.Dir(root); exists(parent) && !isDir(parent) {
		return fmt.Errorf("ECI proof root or its parent is unsafe: %s", root)
	}
	return nil
}

func validSessionID(s string) bool { return sessionIDPattern.MatchString(s) }

// sessionIdentity returns the variable that names the session and its value: ECI_SESSION_ID when it is
// non-empty, otherwise ECI_THREAD_ID, the same selection hooks/eci-review-gate.sh:191 makes. A non-empty
// ECI_SESSION_ID is used even when it is invalid, so the fallback never masks an invalid explicit value.
func sessionIdentity() (string, string) {
	if sid := os.Getenv(envSessionID); sid != "" {
		return envSessionID, sid
	}
	return envThreadID, os.Getenv(envThreadID)
}

// currentSession reads the explicit session identity from the environment.
func currentSession() (string, error) {
	name, sid := sessionIdentity()
	if sid == "" {
		return "", exitError{code: 1, msg: "ECI coordination requires an explicit ECI_SESSION_ID or ECI_THREAD_ID."}
	}
	if !validSessionID(sid) {
		return "", exitError{code: 1, msg: "Invalid " + name + "; use only letters, digits, '_' or '-'."}
	}
	return sid, nil
}

const (
	envHarnessSessionID   = "PI_SESSION_ID"   // set by the omp bash tool on every call
	envHarnessSessionFile = "PI_SESSION_FILE" // the calling session's transcript
	// maxTranscriptHeaderBytes bounds the transcript prefix searched for the session header.
	maxTranscriptHeaderBytes = 64 << 10
	// maxSessionAncestors bounds the parentSession walk.
	maxSessionAncestors = 32
)

// harnessSessionsDir is the default omp sessions tree below the home that owns a transcript;
// harnessProfilesDir holds named profiles, each with its own agent/sessions tree.
var (
	harnessSessionsDir = sep + filepath.Join(".omp", "agent", "sessions") + sep
	harnessProfilesDir = sep + filepath.Join(".omp", "profiles") + sep
	profileSessionsDir = sep + filepath.Join("agent", "sessions") + sep
	sep                = string(filepath.Separator)
)

// accountHome is the home directory of the account running eci-active, independent of $HOME.
func accountHome() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.HomeDir
}

// harnessLayout returns the omp home that owns transcript file and the boundary of its
// parentSession walk: the default sessions tree or a profile's sessions tree under that home.
// Any other session directory (--session-dir, custom session files) belongs to the account home
// and gets an empty boundary, which limits parents to the caller's own top-level session directory.
func harnessLayout(file string) (home, sessions string) {
	if i := strings.Index(file, harnessSessionsDir); i > 0 {
		return file[:i], file[:i+len(harnessSessionsDir)]
	}
	if i := strings.Index(file, harnessProfilesDir); i > 0 {
		rest := file[i+len(harnessProfilesDir):]
		if j := strings.Index(rest, profileSessionsDir); j > 0 && !strings.Contains(rest[:j], sep) {
			return file[:i], file[:i+len(harnessProfilesDir)+j+len(profileSessionsDir)]
		}
	}
	return accountHome(), ""
}

// verifyCallerSession refuses a session-state mutation whose selected session is neither the
// calling omp session nor one of its parent sessions. The omp bash tool sets PI_SESSION_ID and
// PI_SESSION_FILE on every call, so a stale exported ECI_SESSION_ID cannot hide the caller.
//   - Only the default stores of the omp home that owns the caller transcript are bound (the
//     account home when the transcript is outside an omp home or absent); a fixture with a
//     private HOME or proof root is not.
//   - A call without either variable is outside an omp tool call and is not checked.
//   - The transcript header names the caller; a differing inherited PI_SESSION_ID is ignored.
//   - A bound call whose harness identity cannot be verified fails closed.
//
// Example: a Supervisor child exporting its root session id appends to the root ledger.
func verifyCallerSession(verb, sid, store string) error {
	callerID, file := os.Getenv(envHarnessSessionID), os.Getenv(envHarnessSessionFile)
	if callerID == "" && file == "" {
		return nil
	}
	unverified := func(reason string) error {
		return exitError{code: 1, msg: fmt.Sprintf("[ECI_SESSION_IDENTITY_UNVERIFIED] The omp caller identity (%s, %s) cannot be verified: %s; %s refused, no state was changed. Run the command from an omp bash tool call, which sets both, and retry.", envHarnessSessionID, envHarnessSessionFile, reason, verb)}
	}
	validFile := file == "" || (filepath.IsAbs(file) && strings.HasSuffix(file, ".jsonl"))
	var home, sessions string
	if file != "" && validFile {
		home, sessions = harnessLayout(file)
	} else {
		home = accountHome()
	}
	if !harnessStore(store, home) {
		return nil
	}
	if !validFile {
		return unverified(envHarnessSessionFile + " is not an absolute .jsonl transcript path: " + file)
	}
	lineage, err := sessionLineage(callerID, file, sessions)
	if err != nil {
		return unverified(err.Error())
	}
	if slices.Contains(lineage, sid) {
		return nil
	}
	name, _ := sessionIdentity()
	return exitError{code: 1, msg: fmt.Sprintf("[ECI_SESSION_IDENTITY_MISMATCH] %s=%s is neither the calling omp session %s nor one of its parent sessions; %s refused, no state was changed. A stale exported %s (copied environment or resumed shell) selects another session's state. Run `unset ECI_SESSION_ID ECI_THREAD_ID`, then set ECI_SESSION_ID=%s (the calling session) or the owning parent session id, and retry.", name, sid, lineage[0], verb, name, lineage[0])}
}

// harnessStore reports whether store is the context-session root or the proof root of home.
func harnessStore(store, home string) bool {
	if home == "" {
		return false
	}
	store = resolvedPath(store)
	return store == resolvedPath(filepath.Join(home, ".agent", "context", "sessions")) ||
		store == resolvedPath(filepath.Join(home, "tmp", "agent", "proof"))
}

// resolvedPath resolves the deepest existing ancestor of p and appends the remainder.
func resolvedPath(p string) string {
	p, rest := filepath.Clean(p), ""
	for {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Join(p, rest)
		}
		rest, p = filepath.Join(filepath.Base(p), rest), parent
	}
}

// sessionLineage returns the caller session id followed by its parent session ids. With a
// transcript, its header names the caller and a differing callerID (an inherited PI_SESSION_ID)
// is ignored. The walk follows parentSession transcripts inside sessions or, when sessions is
// empty, only the parent transcript of the caller's own top-level session directory
// (<dir>.jsonl for a child at <dir>/<child>.jsonl). A bare session id ends it (a recovery agent
// names the recovered session that way). An unreadable, foreign or repeated parent ends it.
func sessionLineage(callerID, file, sessions string) ([]string, error) {
	if file == "" {
		if !validSessionID(callerID) {
			return nil, fmt.Errorf("%s is invalid", envHarnessSessionID)
		}
		return []string{callerID}, nil
	}
	id, parent, err := transcriptHeader(file)
	if err != nil {
		return nil, err
	}
	lineage, seen, cur := []string{id}, map[string]bool{file: true}, file
	for len(lineage) <= maxSessionAncestors && parent != "" {
		if validSessionID(parent) {
			return append(lineage, parent), nil
		}
		inTree := strings.HasPrefix(parent, sessions)
		if sessions == "" {
			inTree = parent == filepath.Dir(cur)+".jsonl"
		}
		if !inTree || !strings.HasSuffix(parent, ".jsonl") || seen[parent] {
			break
		}
		seen[parent], cur = true, parent
		if id, parent, err = transcriptHeader(parent); err != nil {
			break
		}
		lineage = append(lineage, id)
	}
	return lineage, nil
}

// transcriptHeader reads the session record of an omp transcript from its first 64 KiB: the
// session id and the parentSession string, empty when absent or not a string.
func transcriptHeader(path string) (string, string, error) {
	if !isRegularNoFollow(path) {
		return "", "", fmt.Errorf("transcript is not a regular file: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxTranscriptHeaderBytes))
	if err != nil {
		return "", "", err
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		var record struct {
			Type   string `json:"type"`
			ID     string `json:"id"`
			Parent any    `json:"parentSession"`
		}
		if json.Unmarshal(line, &record) != nil || record.Type != "session" {
			continue
		}
		if !validSessionID(record.ID) {
			return "", "", fmt.Errorf("transcript session id is invalid: %s", path)
		}
		parent, _ := record.Parent.(string)
		return record.ID, parent, nil
	}
	return "", "", fmt.Errorf("transcript has no session header in its first 64 KiB: %s", path)
}

// resolveSessionDir returns <proof-root>/<session> after safety checks.
func resolveSessionDir(sid string) (string, error) {
	if err := proofRootSafe(); err != nil {
		return "", err
	}
	dir := filepath.Join(proofRoot(), sid)
	if isSymlink(dir) {
		return "", fmt.Errorf("ECI session directory is a symlink; refusing to use it: %s", dir)
	}
	if exists(dir) && !isDir(dir) {
		return "", fmt.Errorf("ECI session directory is not a directory: %s", dir)
	}
	return dir, nil
}

// sessionStatePath returns <proof-root>/<session>/<name> after validating the
// session directory and the leaf.
func sessionStatePath(sid, name string) (string, error) {
	if !validSessionID(sid) {
		return "", fmt.Errorf("invalid session id")
	}
	dir, err := resolveSessionDir(sid)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	if isSymlink(path) {
		return "", fmt.Errorf("state target is a symlink: %s", path)
	}
	return path, nil
}

// proofRootReady applies the proof-root checks every state mutation requires,
// including mutations whose state lives outside the proof root.
func proofRootReady() error {
	if err := proofRootSafe(); err != nil {
		return fmt.Errorf("ECI proof root is unsafe; refusing state mutation.")
	}
	if !isDir(proofRoot()) {
		return fmt.Errorf("ECI proof root is unavailable; refusing state mutation.")
	}
	return nil
}

// lockWait is the bounded wait for every ECI lock: ECI_LOCK_WAIT_SECONDS
// (0..60), default 5 s. 0 means one attempt and no wait.
func lockWait() time.Duration {
	if n, err := strconv.Atoi(os.Getenv("ECI_LOCK_WAIT_SECONDS")); err == nil && n >= 0 && n <= 60 {
		return time.Duration(n) * time.Second
	}
	return 5 * time.Second
}

// flockWait polls LOCK_EX|LOCK_NB until it succeeds or timeout passes. Polling
// keeps "closing the descriptor releases the lock" and retries EINTR.
func flockWait(f *os.File, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(25*time.Millisecond + rand.N(25*time.Millisecond))
	}
}

// lockBusy is the caller-neutral timeout message shared by every ECI lock.
// With ECI_LOCK_WAIT_SECONDS=0 it reads "after waiting 0s": one attempt failed.
func lockBusy(wait time.Duration, lockPath string) error {
	return fmt.Errorf("ECI mutation lock is busy after waiting %ds; refusing state mutation: %s", int(wait/time.Second), lockPath)
}

// acquireLock takes the per-user ECI mutation lock with the bounded wait. The
// lock is held until the returned file is closed.
func acquireLock() (*os.File, error) {
	if err := proofRootReady(); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(proofRoot(), ".eci-active.lock")
	if isSymlink(lockPath) || (exists(lockPath) && !isRegularNoFollow(lockPath)) {
		return nil, fmt.Errorf("ECI mutation lock is unsafe: %s", lockPath)
	}
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("ECI mutation lock is unavailable: %s", lockPath)
	}
	wait := lockWait()
	if err := flockWait(f, wait); err != nil {
		f.Close()
		return nil, lockBusy(wait, lockPath)
	}
	return f, nil
}

// acquireSessionLock takes <dir>/.eci-session.lock, the lock for state that
// lives in that session directory, with the bounded wait. The lock is held
// until the returned file is closed.
//   - Order: a caller that needs both takes the per-user lock first.
//   - Never call appendLedgerEntry while holding this lock: a second flock on
//     the same file in the same process waits against itself.
//   - Known limit: appendLedgerEntry and requirementAssignment hold it while
//     bin/status-report or bin/eci-requirement-assignment run. A cold build of
//     either tool takes about a minute, so a parallel same-session mutation
//     times out during it instead of serializing.
func acquireSessionLock(dir string) (*os.File, error) {
	lockPath := filepath.Join(dir, ".eci-session.lock")
	if isSymlink(lockPath) || (exists(lockPath) && !isRegularNoFollow(lockPath)) {
		return nil, fmt.Errorf("ECI session lock is unsafe: %s", lockPath)
	}
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("ECI session lock is unavailable: %s", lockPath)
	}
	wait := lockWait()
	if err := flockWait(f, wait); err != nil {
		f.Close()
		return nil, lockBusy(wait, lockPath)
	}
	// Old-session cleanup (session-snapshot.sh) may unlink the file while we
	// wait; a lock on an unlinked inode excludes nobody. Fail closed when the
	// path no longer names the locked inode. Residual risk: the cleanup can
	// still remove the directory after this check, while the lock is held.
	held, herr := f.Stat()
	now, nerr := os.Lstat(lockPath)
	if herr != nil || nerr != nil || !os.SameFile(held, now) {
		f.Close()
		return nil, fmt.Errorf("ECI session lock was replaced while waiting; refusing state mutation: %s", lockPath)
	}
	return f, nil
}

// acquireProofSessionLock takes the lock for session-scoped proof records in
// <proof>/<sid>. It keeps the proof-root checks of the per-user lock. When
// <proof>/<sid> is absent there is no session state to guard: it returns a nil
// file, takes no lock and creates no directory; the caller's own absence
// handling applies. Closing the nil file is a harmless no-op error.
func acquireProofSessionLock(sid string) (*os.File, error) {
	if err := proofRootReady(); err != nil {
		return nil, err
	}
	dir, err := resolveSessionDir(sid)
	if err != nil {
		return nil, err
	}
	if !isDir(dir) {
		return nil, nil
	}
	return acquireSessionLock(dir)
}

// contextSessionsRoot is <home>/.agent/context/sessions, the store of session ledgers and reports.
func contextSessionsRoot() string { return filepath.Join(homeDir(), ".agent", "context", "sessions") }

// contextSessionDir creates and returns <home>/.agent/context/sessions/<sid>.
func contextSessionDir(sid string) (string, error) {
	if !validSessionID(sid) {
		return "", fmt.Errorf("invalid session id")
	}
	dir := filepath.Join(contextSessionsRoot(), sid)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if !isDir(dir) || isSymlink(dir) {
		return "", fmt.Errorf("context session directory is unsafe: %s", dir)
	}
	return dir, nil
}

// canonicalCwd resolves the working directory like `pwd -P`.
func canonicalCwd(cwd string) string {
	if isDir(cwd) {
		if r, err := filepath.EvalSymlinks(cwd); err == nil {
			return r
		}
	}
	return cwd
}

func currentCwd() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return canonicalCwd(cwd), nil
}

// gitSafe runs git in a scrubbed environment with fixed system locations; a failed run returns git's own stderr as its error text.
func gitSafe(args ...string) (string, error) {
	var exe string
	for _, candidate := range []string{"/usr/bin/git", "/bin/git", "/usr/local/bin/git"} {
		if fi, err := os.Stat(candidate); err == nil && fi.Mode().Perm()&0o111 != 0 {
			exe = candidate
			break
		}
	}
	if exe == "" {
		return "", fmt.Errorf("git executable not found")
	}
	lc := os.Getenv("LC_ALL")
	if lc == "" {
		lc = "C"
	}
	lang := os.Getenv("LANG")
	if lang == "" {
		lang = "C"
	}
	cmd := exec.Command(exe, append([]string{"-c", "core.fsmonitor=false"}, args...)...)
	cmd.Env = []string{
		"HOME=" + homeDir(),
		"PATH=/usr/bin:/bin",
		"LANG=" + lang,
		"LC_ALL=" + lc,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_PAGER=cat",
		"GIT_EDITOR=:",
		"GIT_SEQUENCE_EDITOR=:",
		"GIT_ASKPASS=:",
		"GIT_TERMINAL_PROMPT=0",
	}
	out, err := cmd.Output()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return strings.TrimSuffix(string(out), "\n"), nil
	case errors.As(err, &exitErr) && len(exitErr.Stderr) > 0:
		// Callers show git's own diagnostic, so the captured stderr becomes the error text.
		return "", errors.New(strings.TrimSuffix(string(exitErr.Stderr), "\n"))
	default:
		return "", err
	}
}
