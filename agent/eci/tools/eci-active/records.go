package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	repoRecordName       = "eci-additional-repository"
	permissiveRecordName = "eci-permissive-mode"
	globalPermissiveName = ".eci-permissive-mode"
	coordinatorRecord    = "eci-coordinator-edit"
	maxRecordBytes       = 4096
	maxRequestBytes      = 1048576
	maxReasonBytes       = 2048
	permissiveTTLMax     = 3600
)

var (
	selectorPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	epochPattern    = regexp.MustCompile(`^[0-9]{1,12}$`)
	ttlPattern      = regexp.MustCompile(`^[0-9]{1,4}$`)
)

// readSmallRecord validates a 0600 owner-only regular record and returns its
// lines. Records must end with LF and contain no CR.
func readSmallRecord(path string) ([]string, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxRecordBytes || fi.Size() == 0 {
		return nil, fmt.Errorf("record is not a bounded regular file: %s", path)
	}
	if fi.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("record mode is not 0600: %s", path)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Geteuid() {
		return nil, fmt.Errorf("record owner is not the current user: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if data[len(data)-1] != '\n' || strings.ContainsRune(string(data), '\r') {
		return nil, fmt.Errorf("record framing is invalid: %s", path)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"), nil
}

// writeRecord publishes content atomically with mode 0600.
func writeRecord(path, content string) error {
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func reasonValid(r string) bool {
	if r == "" || len(r) > maxReasonBytes {
		return false
	}
	for i := 0; i < len(r); i++ {
		if r[i] < 0x20 || r[i] == 0x7f {
			return false
		}
	}
	return strings.TrimSpace(r) != ""
}

// canonicalRepoRoot requires an absolute path to an existing git worktree root.
func canonicalRepoRoot(requested string) (string, error) {
	if !filepath.IsAbs(requested) || !isDir(requested) {
		return "", fmt.Errorf("repository must be an absolute existing directory")
	}
	resolved, err := filepath.EvalSymlinks(requested)
	if err != nil || !isDir(resolved) {
		return "", fmt.Errorf("repository is not resolvable")
	}
	top, err := gitSafe("-C", resolved, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("repository is not a git work tree")
	}
	actual, err := filepath.EvalSymlinks(top)
	if err != nil || actual != resolved {
		return "", fmt.Errorf("repository must be a git root")
	}
	return actual, nil
}

// absentTarget accepts an absolute path that does not yet exist.
func absentTarget(requested string) (string, error) {
	if !filepath.IsAbs(requested) {
		return "", fmt.Errorf("repository must be absolute")
	}
	clean := filepath.Clean(requested)
	if exists(clean) {
		return "", fmt.Errorf("repository path exists")
	}
	return clean, nil
}

// repoRecordPath returns the additional-repository record path for a session.
func repoRecordPath(sid string) (string, error) {
	return sessionStatePath(sid, repoRecordName)
}

// repoRecordValid checks a repository record for the given session and cwd.
func repoRecordValid(sid, expectedCwd, expectedRepo string, allowAbsent bool) error {
	if !isDir(expectedCwd) || isSymlink(expectedCwd) {
		return fmt.Errorf("cwd is unsafe")
	}
	expectedCwd = canonicalCwd(expectedCwd)
	path, err := repoRecordPath(sid)
	if err != nil {
		return err
	}
	lines, err := readSmallRecord(path)
	if err != nil {
		return err
	}
	if len(lines) != 6 || lines[0] != "schema: eci-additional-repository/v1" ||
		lines[1] != "session_id: "+sid || !strings.HasPrefix(lines[2], "cwd: ") ||
		canonicalCwd(strings.TrimPrefix(lines[2], "cwd: ")) != expectedCwd {
		return fmt.Errorf("repository record is invalid")
	}
	if !strings.HasPrefix(lines[3], "repository: ") {
		return fmt.Errorf("repository record is invalid")
	}
	repo := strings.TrimPrefix(lines[3], "repository: ")
	if allowAbsent && !exists(repo) {
		norm, err := absentTarget(repo)
		if err != nil || norm != repo {
			return fmt.Errorf("repository record target is invalid")
		}
	} else if _, err := canonicalRepoRoot(repo); err != nil {
		return err
	}
	if expectedRepo != "" && repo != expectedRepo {
		return fmt.Errorf("repository mismatch")
	}
	if !strings.HasPrefix(lines[4], "reason: ") || !reasonValid(strings.TrimPrefix(lines[4], "reason: ")) {
		return fmt.Errorf("repository reason is invalid")
	}
	if lines[5] != "state: active" {
		return fmt.Errorf("repository state is not active")
	}
	return nil
}

// repoContext returns the current session id, session dir, and canonical cwd.
func repoContext() (string, string, error) {
	sid, err := currentSession()
	if err != nil {
		return "", "", err
	}
	dir, err := resolveSessionDir(sid)
	if err != nil {
		return "", "", err
	}
	if filepath.Base(dir) != sid {
		return "", "", fmt.Errorf("session directory does not match PI_SESSION_ID")
	}
	cwd, err := currentCwd()
	if err != nil {
		return "", "", err
	}
	return sid, cwd, nil
}

// activeRepoRoot is the work repository of the current cwd.
func activeRepoRoot(cwd string) (string, error) {
	top, err := gitSafe("-C", cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(top)
}

func repositoryAllowStatus(args []string) error {
	if len(args) != 1 {
		return usageError()
	}
	sid, cwd, err := repoContext()
	if err != nil {
		return err
	}
	path, err := repoRecordPath(sid)
	if err != nil {
		return err
	}
	if !exists(path) {
		fmt.Println("ECI additional repository inactive")
		return nil
	}
	if err := repoRecordValid(sid, cwd, "", false); err != nil {
		fmt.Println("ECI additional repository inactive")
		return nil
	}
	lines, err := readSmallRecord(path)
	if err != nil {
		return err
	}
	repo := strings.TrimPrefix(lines[3], "repository: ")
	reason := strings.TrimPrefix(lines[4], "reason: ")
	fmt.Printf("ECI additional repository active: %s\nreason: %s\n", repo, reason)
	return nil
}

func repositoryAllowOn(args []string) error {
	if len(args) != 3 {
		return usageError()
	}
	sid, cwd, err := repoContext()
	if err != nil {
		return exitError{code: 1, msg: err.Error()}
	}
	repo, err := canonicalRepoRoot(args[1])
	if err != nil {
		return exitError{code: 1, msg: err.Error()}
	}
	reason := args[2]
	if !reasonValid(reason) {
		return exitError{code: 1, msg: "repository-allow-on requires a bounded non-blank reason without control characters."}
	}
	active, err := activeRepoRoot(cwd)
	if err == nil && active == repo {
		return exitError{code: 1, msg: "Repository is already the active work repository."}
	}
	lock, err := acquireLock()
	if err != nil {
		return err
	}
	defer lock.Close()
	path, err := repoRecordPath(sid)
	if err != nil {
		return err
	}
	if exists(path) {
		return exitError{code: 1, msg: "An additional repository is already recorded; remove it first."}
	}
	content := fmt.Sprintf("schema: eci-additional-repository/v1\nsession_id: %s\ncwd: %s\nrepository: %s\nreason: %s\nstate: active\n",
		sid, cwd, repo, reason)
	if err := writeRecord(path, content); err != nil {
		return err
	}
	if err := repoRecordValid(sid, cwd, repo, false); err != nil {
		os.Remove(path)
		return exitError{code: 1, msg: "repository record failed validation after write."}
	}
	fmt.Printf("ECI additional repository active: %s\nreason: %s\n", repo, reason)
	return nil
}

func repositoryAllowOff(args []string) error {
	if len(args) != 2 {
		return usageError()
	}
	sid, cwd, err := repoContext()
	if err != nil {
		return exitError{code: 1, msg: err.Error()}
	}
	repo, err := canonicalRepoRoot(args[1])
	if err != nil {
		repo, err = absentTarget(args[1])
		if err != nil {
			return exitError{code: 1, msg: err.Error()}
		}
	}
	lock, err := acquireLock()
	if err != nil {
		return err
	}
	defer lock.Close()
	path, err := repoRecordPath(sid)
	if err != nil {
		return err
	}
	if !exists(path) {
		return exitError{code: 1, msg: "No matching repository allowance."}
	}
	if err := repoRecordValid(sid, cwd, repo, true); err != nil {
		return exitError{code: 1, msg: "No matching repository allowance."}
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	fmt.Printf("ECI additional repository inactive: %s\n", repo)
	return nil
}

// permissiveRequest validates the gate/operation/request selectors.
func permissiveRequest(scope, gate, op, req string) error {
	if scope != "session" && scope != "global" {
		return exitError{code: 1, msg: "permissive scope must be session or global."}
	}
	if !selectorPattern.MatchString(gate) || !selectorPattern.MatchString(op) {
		return exitError{code: 1, msg: "permissive gate and operation must be bounded selectors."}
	}
	if req == "" || len(req) > maxRequestBytes {
		return exitError{code: 1, msg: "permissive request must be non-empty and bounded."}
	}
	return nil
}

// permissivePath returns the record path. Global records live under $HOME/.omp.
func permissivePath(scope string) (string, error) {
	if scope == "session" {
		sid, err := currentSession()
		if err != nil {
			return "", err
		}
		return sessionStatePath(sid, permissiveRecordName)
	}
	h := homeDir()
	if !filepath.IsAbs(h) || filepath.Clean(h) != h {
		return "", fmt.Errorf("HOME is not a canonical absolute path")
	}
	root := filepath.Join(h, ".omp")
	if !isDir(root) || isSymlink(root) {
		return "", fmt.Errorf("global ECI root is unavailable")
	}
	path := filepath.Join(root, globalPermissiveName)
	if isSymlink(path) {
		return "", fmt.Errorf("permissive record is a symlink")
	}
	return path, nil
}

// permissiveValid returns nil when the record is an active, unexpired global
// permissive record. Session records depend on the retired hook marker and
// are never reported active.
func permissiveValid(scope, gate, op string, now int64) (expired bool, err error) {
	if scope != "global" {
		return false, fmt.Errorf("session permissive validation is retired")
	}
	path, err := permissivePath(scope)
	if err != nil {
		return false, err
	}
	lines, err := readSmallRecord(path)
	if err != nil {
		return false, err
	}
	if len(lines) != 9 || lines[0] != "schema: eci-permissive-mode/v2" ||
		lines[1] != "scope: global" || lines[2] != "session_id: *" || lines[3] != "cwd: *" ||
		lines[4] != "gate: "+gate || lines[5] != "operation: "+op || lines[8] != "state: active" {
		return false, fmt.Errorf("permissive record is invalid")
	}
	issued := strings.TrimPrefix(lines[6], "issued_at_epoch: ")
	expires := strings.TrimPrefix(lines[7], "expires_at_epoch: ")
	if !strings.HasPrefix(lines[6], "issued_at_epoch: ") || !strings.HasPrefix(lines[7], "expires_at_epoch: ") ||
		!epochPattern.MatchString(issued) || !epochPattern.MatchString(expires) {
		return false, fmt.Errorf("permissive epochs are invalid")
	}
	iss, _ := strconv.ParseInt(issued, 10, 64)
	exp, _ := strconv.ParseInt(expires, 10, 64)
	if exp <= iss {
		return false, fmt.Errorf("permissive expiry is not after issue")
	}
	if now >= exp {
		return true, nil
	}
	return false, nil
}

func permissiveStatus(args []string) error {
	if len(args) != 5 {
		return usageError()
	}
	scope, gate, op, req := args[1], args[2], args[3], args[4]
	if err := permissiveRequest(scope, gate, op, req); err != nil {
		return err
	}
	path, err := permissivePath(scope)
	if err != nil || !exists(path) {
		fmt.Println("ECI permissive mode inactive")
		return nil
	}
	expired, err := permissiveValid(scope, gate, op, time.Now().Unix())
	if err != nil {
		fmt.Printf("ECI permissive mode inactive: %s\n", path)
		return nil
	}
	if expired {
		os.Remove(path)
		fmt.Printf("ECI permissive mode inactive: %s\n", path)
		return nil
	}
	fmt.Printf("ECI permissive mode active: %s\n", path)
	return nil
}

func permissiveOn(args []string) error {
	if len(args) != 6 {
		return usageError()
	}
	scope, gate, op, req, ttlText := args[1], args[2], args[3], args[4], args[5]
	if scope != "global" {
		return exitError{code: 2, msg: "[ECI_COORDINATION_DEFERRED] Session permissive validation depends on the legacy hook marker; no state was changed."}
	}
	if err := permissiveRequest(scope, gate, op, req); err != nil {
		return err
	}
	if !ttlPattern.MatchString(ttlText) {
		return exitError{code: 1, msg: "permissive ttl must be 1..3600 seconds."}
	}
	ttl, _ := strconv.Atoi(ttlText)
	if ttl < 1 || ttl > permissiveTTLMax {
		return exitError{code: 1, msg: "permissive ttl must be 1..3600 seconds."}
	}
	lock, err := acquireLock()
	if err != nil {
		return err
	}
	defer lock.Close()
	path, err := permissivePath(scope)
	if err != nil {
		return err
	}
	if exists(path) && !isRegularNoFollow(path) {
		return exitError{code: 1, msg: "permissive record target is unsafe."}
	}
	now := time.Now().Unix()
	content := fmt.Sprintf("schema: eci-permissive-mode/v2\nscope: global\nsession_id: *\ncwd: *\ngate: %s\noperation: %s\nissued_at_epoch: %d\nexpires_at_epoch: %d\nstate: active\n",
		gate, op, now, now+int64(ttl))
	if err := writeRecord(path, content); err != nil {
		return err
	}
	if _, err := permissiveValid(scope, gate, op, now); err != nil {
		os.Remove(path)
		return exitError{code: 1, msg: "permissive record failed validation after write."}
	}
	fmt.Printf("ECI permissive mode active: %s\n", path)
	return nil
}

func permissiveOff(args []string) error {
	if len(args) != 5 {
		return usageError()
	}
	scope, gate, op, req := args[1], args[2], args[3], args[4]
	if err := permissiveRequest(scope, gate, op, req); err != nil {
		return err
	}
	lock, err := acquireLock()
	if err != nil {
		return err
	}
	defer lock.Close()
	path, err := permissivePath(scope)
	if err != nil {
		return err
	}
	if exists(path) {
		if !isRegularNoFollow(path) {
			return exitError{code: 1, msg: fmt.Sprintf("ECI permissive mode target is unsafe: %s", path)}
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	fmt.Printf("ECI permissive mode inactive: %s\n", path)
	return nil
}

// coordinatorEditStatus always reports inactive: the legacy hook marker it
// depends on is retired. Unsafe state targets are still reported as errors.
func coordinatorEditStatus(args []string) error {
	if len(args) != 1 {
		return usageError()
	}
	sid, _, err := repoContext()
	if err != nil {
		return exitError{code: 1, msg: err.Error()}
	}
	path, err := sessionStatePath(sid, coordinatorRecord)
	if err != nil {
		return exitError{code: 1, msg: "Coordinator edit state target is unsafe."}
	}
	if exists(path) && !isRegularNoFollow(path) {
		return exitError{code: 1, msg: "Coordinator edit state target is unsafe."}
	}
	fmt.Println("ECI coordinator edit state inactive")
	return nil
}
