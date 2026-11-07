package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
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

// acquireLock takes the non-blocking ECI mutation lock. The lock is held until
// the returned file is closed.
func acquireLock() (*os.File, error) {
	if err := proofRootSafe(); err != nil {
		return nil, fmt.Errorf("ECI proof root is unsafe; refusing state mutation.")
	}
	root := proofRoot()
	if !isDir(root) {
		return nil, fmt.Errorf("ECI proof root is unavailable; refusing state mutation.")
	}
	lockPath := filepath.Join(root, ".eci-active.lock")
	if isSymlink(lockPath) || (exists(lockPath) && !isRegularNoFollow(lockPath)) {
		return nil, fmt.Errorf("ECI mutation lock is unsafe: %s", lockPath)
	}
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("ECI mutation lock is unavailable: %s", lockPath)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("ECI mutation lock is busy; refusing state mutation: %s", lockPath)
	}
	return f, nil
}

// contextSessionDir creates and returns <home>/.agent/context/sessions/<sid>.
func contextSessionDir(sid string) (string, error) {
	if !validSessionID(sid) {
		return "", fmt.Errorf("invalid session id")
	}
	dir := filepath.Join(homeDir(), ".agent", "context", "sessions", sid)
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
