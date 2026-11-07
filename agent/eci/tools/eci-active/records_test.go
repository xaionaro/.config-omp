package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, err)
	fx := repoAllowFixture{base: base, sid: "repo-allow-test"}
	fx.session = filepath.Join(base, "proof", fx.sid)
	require.NoError(t, os.MkdirAll(fx.session, 0o700))
	t.Setenv("HOME", base)
	t.Setenv(envProofRoot, filepath.Join(base, "proof"))
	t.Setenv(envSessionID, fx.sid)
	return fx
}

// makeRepo creates a git repository named name under the fixture and returns its canonical root.
func (fx repoAllowFixture) makeRepo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(fx.base, name)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput()
	require.NoError(t, err, string(out))
	root, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
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
	require.NoError(t, os.WriteFile(fx.recordPath(), []byte(content), 0o600))
	require.NoError(t, os.Chmod(fx.recordPath(), mode))
}

// TestRepositoryAllowStatusReportsActiveOrInactive prints the allowance of a valid record and inactive when no record exists.
func TestRepositoryAllowStatusReportsActiveOrInactive(t *testing.T) {
	fx := newRepoAllowFixture(t)
	repo := fx.makeRepo(t, "target")
	work := fx.makeRepo(t, "work")
	t.Chdir(work)

	out, err := captureStdout(t, func() error { return repositoryAllowStatus([]string{"repository-allow-status"}) })
	require.NoError(t, err)
	assert.Equal(t, "ECI additional repository inactive\n", out)

	fx.writeRecord(t, work, repo, 0o600)
	out, err = captureStdout(t, func() error { return repositoryAllowStatus([]string{"repository-allow-status"}) })
	require.NoError(t, err)
	assert.Equal(t, "ECI additional repository active: "+repo+"\nreason: test reason\n", out)
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
				require.NoError(t, os.RemoveAll(filepath.Join(repo, ".git")))
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
			require.ErrorAs(t, err, &ee)
			assert.Equal(t, exitError{code: 1, msg: "ECI additional repository state is invalid: " + fx.recordPath()}, ee)
			assert.Empty(t, out)
		})
	}
}
