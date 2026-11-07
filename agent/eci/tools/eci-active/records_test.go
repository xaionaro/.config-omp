package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repoAllowFixture isolates the proof root, session identity, and git repositories used by the repository-allow tests.
type repoAllowFixture struct {
	base    string // canonical temporary directory holding every fixture path
	sid     string // session identity exported through ECI_SESSION_ID
	session string // session state directory <base>/proof/<sid>
}

// newRepoAllowFixture points HOME, the proof root, and the session identity at a fresh temporary directory.
func newRepoAllowFixture(t *testing.T) repoAllowFixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temporary directory: %v", err)
	}
	fx := repoAllowFixture{base: base, sid: "repo-allow-test"}
	fx.session = filepath.Join(base, "proof", fx.sid)
	if err := os.MkdirAll(fx.session, 0o700); err != nil {
		t.Fatalf("create session directory: %v", err)
	}
	t.Setenv("HOME", base)
	t.Setenv(envProofRoot, filepath.Join(base, "proof"))
	t.Setenv(envSessionID, fx.sid)
	return fx
}

// makeRepo creates a git repository named name under the fixture and returns its canonical root.
func (fx repoAllowFixture) makeRepo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(fx.base, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create repository directory %s: %v", name, err)
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init %s: %v: %s", name, err, out)
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve repository %s: %v", name, err)
	}
	return root
}

// recordPath returns the additional-repository record path for the fixture session.
func (fx repoAllowFixture) recordPath() string {
	return filepath.Join(fx.session, repoRecordName)
}

// writeRecord writes an active additional-repository record for cwd and repo with the given file mode.
func (fx repoAllowFixture) writeRecord(t *testing.T, cwd, repo string, mode os.FileMode) {
	t.Helper()
	content := fmt.Sprintf("schema: eci-additional-repository/v1\nsession_id: %s\ncwd: %s\nrepository: %s\nreason: test reason\nstate: active\n",
		fx.sid, cwd, repo)
	if err := os.WriteFile(fx.recordPath(), []byte(content), 0o600); err != nil {
		t.Fatalf("write record: %v", err)
	}
	if err := os.Chmod(fx.recordPath(), mode); err != nil {
		t.Fatalf("set record mode %o: %v", mode, err)
	}
}

// assertRecordAbsent fails the test when the fixture session holds an additional-repository record.
func (fx repoAllowFixture) assertRecordAbsent(t *testing.T) {
	t.Helper()
	if _, err := os.Lstat(fx.recordPath()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("record %s: want absent, Lstat error = %v", fx.recordPath(), err)
	}
}

// TestRepositoryAllowStatusReportsActiveOrInactive prints the allowance of a valid record and inactive when no record exists.
func TestRepositoryAllowStatusReportsActiveOrInactive(t *testing.T) {
	fx := newRepoAllowFixture(t)
	repo := fx.makeRepo(t, "target")
	work := fx.makeRepo(t, "work")
	t.Chdir(work)

	out, err := captureStdout(t, func() error { return repositoryAllowStatus([]string{"repository-allow-status"}) })
	if err != nil {
		t.Fatalf("status without record: unexpected error: %#v", err)
	}
	if want := "ECI additional repository inactive\n"; out != want {
		t.Errorf("status without record: stdout = %q, want %q", out, want)
	}

	fx.writeRecord(t, work, repo, 0o600)
	out, err = captureStdout(t, func() error { return repositoryAllowStatus([]string{"repository-allow-status"}) })
	if err != nil {
		t.Fatalf("status with valid record: unexpected error: %#v", err)
	}
	if want := "ECI additional repository active: " + repo + "\nreason: test reason\n"; out != want {
		t.Errorf("status with valid record: stdout = %q, want %q", out, want)
	}
}

// TestRepositoryAllowStatusFailsClosedOnInvalidRecord exits 1 with the invalid-state diagnostic instead of reporting inactive.
func TestRepositoryAllowStatusFailsClosedOnInvalidRecord(t *testing.T) {
	tests := []struct {
		name   string
		mode   os.FileMode
		breaks func(t *testing.T, repo string) // nil when the recorded repository stays valid
	}{
		{name: "record mode is not 0600", mode: 0o644},
		{
			name: "recorded repository is no longer a git worktree",
			mode: 0o600,
			breaks: func(t *testing.T, repo string) {
				t.Helper()
				if err := os.RemoveAll(filepath.Join(repo, ".git")); err != nil {
					t.Fatalf("remove repository metadata: %v", err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newRepoAllowFixture(t)
			repo := fx.makeRepo(t, "target")
			work := fx.makeRepo(t, "work")
			t.Chdir(work)
			fx.writeRecord(t, work, repo, tt.mode)
			if tt.breaks != nil {
				tt.breaks(t, repo)
			}

			out, err := captureStdout(t, func() error { return repositoryAllowStatus([]string{"repository-allow-status"}) })
			var ee exitError
			if !errors.As(err, &ee) {
				t.Fatalf("status: want exitError, got %#v", err)
			}
			if want := (exitError{code: 1, msg: "ECI additional repository state is invalid: " + fx.recordPath()}); ee != want {
				t.Errorf("status: exitError = %#v, want %#v", ee, want)
			}
			if out != "" {
				t.Errorf("status: stdout = %q, want empty", out)
			}
		})
	}
}

// TestRepositoryAllowOnRefusesCwdOutsideWorktree refuses an allowance from a directory that no git worktree contains and writes no record.
func TestRepositoryAllowOnRefusesCwdOutsideWorktree(t *testing.T) {
	t.Setenv("LC_ALL", "C")
	fx := newRepoAllowFixture(t)
	repo := fx.makeRepo(t, "target")
	outside := t.TempDir()
	t.Chdir(outside)
	if out, err := exec.Command("git", "rev-parse", "--show-toplevel").CombinedOutput(); err == nil {
		t.Fatalf("fixture directory %s is inside a git work tree (%s); set TMPDIR outside any repository", outside, out)
	}

	out, err := captureStdout(t, func() error {
		return repositoryAllowOn([]string{"repository-allow-on", repo, "test reason"})
	})
	var ee exitError
	if !errors.As(err, &ee) {
		t.Fatalf("allow outside a worktree: want exitError, got %#v", err)
	}
	if ee.code != 1 || !strings.Contains(ee.msg, "not a git repository") {
		t.Errorf("allow outside a worktree: exitError = %#v, want code 1 with git's not-a-repository diagnostic", ee)
	}
	if out != "" {
		t.Errorf("allow outside a worktree: stdout = %q, want empty", out)
	}
	fx.assertRecordAbsent(t)
}

// TestRepositoryAllowOnRecordsOtherWorktree records the target when the working directory is a different git worktree.
func TestRepositoryAllowOnRecordsOtherWorktree(t *testing.T) {
	fx := newRepoAllowFixture(t)
	repo := fx.makeRepo(t, "target")
	work := fx.makeRepo(t, "work")
	t.Chdir(work)

	out, err := captureStdout(t, func() error {
		return repositoryAllowOn([]string{"repository-allow-on", repo, "test reason"})
	})
	if err != nil {
		t.Fatalf("allow from other worktree: unexpected error: %#v", err)
	}
	if want := "ECI additional repository active: " + repo + "\nreason: test reason\n"; out != want {
		t.Errorf("allow from other worktree: stdout = %q, want %q", out, want)
	}
	if _, err := os.Stat(fx.recordPath()); err != nil {
		t.Errorf("allow from other worktree: record not written: %v", err)
	}
}

// TestRepositoryAllowOnRefusesActiveRepository refuses to allow the repository that already is the working directory's worktree.
func TestRepositoryAllowOnRefusesActiveRepository(t *testing.T) {
	fx := newRepoAllowFixture(t)
	repo := fx.makeRepo(t, "target")
	t.Chdir(repo)

	out, err := captureStdout(t, func() error {
		return repositoryAllowOn([]string{"repository-allow-on", repo, "test reason"})
	})
	var ee exitError
	if !errors.As(err, &ee) {
		t.Fatalf("allow of active repository: want exitError, got %#v", err)
	}
	if want := (exitError{code: 1, msg: "Repository is already the active work repository."}); ee != want {
		t.Errorf("allow of active repository: exitError = %#v, want %#v", ee, want)
	}
	if out != "" {
		t.Errorf("allow of active repository: stdout = %q, want empty", out)
	}
	fx.assertRecordAbsent(t)
}
