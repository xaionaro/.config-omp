package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
)

// TestB3RunHookEvidence drives the real hook entry (RunHook, which sets
// HookMode and CollectShellCommands) for an omp gate-mode set and get with an
// empty transcript path and the receipt removed. The gate is a real copy of the
// built command-gate binary, so the finalize step does not fail on a text
// placeholder. Evidence only; not part of the patch.
func TestB3RunHookEvidence(t *testing.T) {
	realGate, err := os.ReadFile(os.Getenv("B3_REAL_GATE"))
	if err != nil || len(realGate) == 0 {
		t.Skip("set B3_REAL_GATE to a built eci-command-gate-mode binary")
	}
	for _, command := range []string{"set enforcing", "get"} {
		t.Run(command, func(t *testing.T) {
			fixture := newB3Fixture(t)
			if err := os.WriteFile(fixture.eciGate, realGate, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(fixture.eciReceipt); err != nil {
				t.Fatal(err)
			}
			input, err := json.Marshal(map[string]any{
				"session_id": "test-session",
				"tool_name":  "Bash",
				"cwd":        fixture.home,
				"tool_input": map[string]string{"command": fixture.eciGate + " " + command},
			})
			if err != nil {
				t.Fatal(err)
			}
			var stdout strings.Builder
			var status int
			stderr := captureStderr(t, func() {
				status = RunHook(strings.NewReader(string(input)), &stdout, io.Discard)
			})
			sum := sha256.Sum256(realGate)
			homeInfo, _ := os.Stat(fixture.home)
			inode := uint64(0)
			if st, ok := homeInfo.Sys().(*syscall.Stat_t); ok {
				inode = st.Ino
			}
			t.Logf("runhook %s: rc=%d gate_sha256=%s home=%s home_inode=%d stdout=%q stderr=%q", command, status, hex.EncodeToString(sum[:]), fixture.home, inode, stdout.String(), stderr)
		})
	}
}
