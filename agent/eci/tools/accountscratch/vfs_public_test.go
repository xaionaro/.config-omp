package accountscratch

import (
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	// publicScratchChildEnv distinguishes the isolated account-home subprocess.
	//
	// Example: the subprocess checks Open without recursively unsharing.
	publicScratchChildEnv = "ECI_TEST_PUBLIC_SCRATCH_CHILD"
)

// TestOpenRejectsUnavailableAccountScratch exercises the public account lookup.
//
// Example: a usable misleading HOME and TMPDIR never replace missing account tmp.
func TestOpenRejectsUnavailableAccountScratch(t *testing.T) {
	t.Parallel()

	if os.Getenv(publicScratchChildEnv) == "1" {
		account, err := user.Current()
		require.NoError(t, err)
		_, err = Open("file:" + filepath.Join(account.HomeDir, "database.db"))
		switch os.Getenv("ECI_TEST_PUBLIC_SCRATCH_SCENARIO") {
		case "missing":
			require.ErrorIs(t, err, os.ErrNotExist)
		case "regular-file":
			require.ErrorIs(t, err, syscall.ENOTDIR)
		default:
			require.FailNow(t, "unknown scratch scenario")
		}
		_, err = os.Lstat(filepath.Join(account.HomeDir, "database.db"))
		require.True(t, errors.Is(err, os.ErrNotExist))
		return
	}

	account, err := user.Current()
	require.NoError(t, err)
	require.NotEmpty(t, account.HomeDir)
	executable, err := os.Executable()
	require.NoError(t, err)
	binary, err := os.ReadFile(executable)
	require.NoError(t, err)
	for _, scenario := range []string{"missing", "regular-file"} {
		t.Run(scenario,
			// checkUnavailableRoot runs with a private overlay of the account home.
			//
			// Example: an ordinary file named tmp is unusable even as root.
			func(t *testing.T) {
				home := t.TempDir()
				require.NoError(t, os.Mkdir(filepath.Join(home, "bin"), 0700))
				require.NoError(t, os.WriteFile(filepath.Join(home, "bin", "test"), binary, 0700))
				fallback := filepath.Join(home, "fallback")
				require.NoError(t, os.Mkdir(fallback, 0700))
				require.NoError(t, os.Mkdir(filepath.Join(fallback, "tmp"), 0700))
				for _, path := range []string{filepath.Join(home, "unrelated"), filepath.Join(fallback, "sentinel")} {
					require.NoError(t, os.WriteFile(path, []byte("preserve"), 0600))
				}
				if scenario == "regular-file" {
					require.NoError(t, os.WriteFile(filepath.Join(home, "tmp"), []byte("not a directory"), 0600))
				}
				script := `set -eu; mount --make-rprivate /; mount --bind "$1" "$2"; export HOME="$2/fallback" TMPDIR="$2/fallback" ECI_TEST_PUBLIC_SCRATCH_CHILD=1 ECI_TEST_PUBLIC_SCRATCH_SCENARIO="$3"; exec "$2/bin/test" -test.run '^TestOpenRejectsUnavailableAccountScratch$' -test.count=1`
				cmd := exec.Command("unshare", "-Ur", "-m", "/bin/sh", "-c", script, "scratch", home, account.HomeDir, scenario)
				output, err := cmd.CombinedOutput()
				require.NoError(t, err, "%s", output)
				for _, path := range []string{filepath.Join(home, "unrelated"), filepath.Join(fallback, "sentinel")} {
					data, err := os.ReadFile(path)
					require.NoError(t, err)
					require.Equal(t, "preserve", string(data))
				}
				entries, err := os.ReadDir(fallback)
				require.NoError(t, err)
				require.Len(t, entries, 2)
				fallbackEntries, err := os.ReadDir(filepath.Join(fallback, "tmp"))
				require.NoError(t, err)
				require.Empty(t, fallbackEntries)
				if scenario == "regular-file" {
					data, err := os.ReadFile(filepath.Join(home, "tmp"))
					require.NoError(t, err)
					require.Equal(t, "not a directory", string(data))
				}
			},
		)
	}
}
