package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// plannerFixture is a canonical-root layout with three tiny planner modules and a bin/ for the pair.
type plannerFixture struct {
	root   string // stands in for $HOME/.omp/agent/eci
	binDir string // <root>/bin
	goTool string // absolute go command from PATH
}

// newPlannerFixture writes one main package per planner tool and an empty bin/.
func newPlannerFixture(t *testing.T) plannerFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	goTool, err := plannerGoTool()
	if err != nil {
		t.Fatalf("plannerGoTool: %v", err)
	}
	fx := plannerFixture{root: root, binDir: filepath.Join(root, "bin"), goTool: goTool}
	if err := os.Mkdir(fx.binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range plannerPairTools {
		fx.writeModule(t, name, "package main\n\nfunc main() {}\n")
	}
	return fx
}

// writeModule replaces the module source of one planner tool.
func (fx plannerFixture) writeModule(t *testing.T, name, mainGo string) {
	t.Helper()
	dir := filepath.Join(fx.root, "hooks", "lib", name+"-go")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/"+name+"\n\ngo 1.24\n", 0o644)
	writeTestFile(t, filepath.Join(dir, "main.go"), mainGo, 0o644)
}

// writeTestFile writes content to path with mode, whatever the umask.
func writeTestFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// seedPreviousPair installs a previous pair with matching receipts and returns the state of every leaf.
func (fx plannerFixture) seedPreviousPair(t *testing.T) map[string]string {
	t.Helper()
	for _, name := range plannerPairTools {
		path := filepath.Join(fx.binDir, name)
		writeTestFile(t, path, "previous "+name+"\n", 0o755)
		digest, err := digestFile(path)
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, path+".sha256", digest+"  "+name+"\n", 0o644)
	}
	return fx.snapshot(t)
}

// snapshot returns the state of every pair leaf under bin/.
func (fx plannerFixture) snapshot(t *testing.T) map[string]string {
	t.Helper()
	states := map[string]string{}
	for _, name := range plannerPairTools {
		for _, leaf := range []string{name, name + ".sha256"} {
			state, err := plannerLeafState(filepath.Join(fx.binDir, leaf))
			if err != nil {
				t.Fatal(err)
			}
			states[leaf] = state
		}
	}
	return states
}

// assertPair fails the test unless every leaf under bin/ has the wanted state.
func (fx plannerFixture) assertPair(t *testing.T, want map[string]string) {
	t.Helper()
	if got := fx.snapshot(t); !reflect.DeepEqual(got, want) {
		t.Errorf("bin/ pair changed:\n got %v\nwant %v", got, want)
	}
}

// transactions lists the transaction directories left under bin/.
func (fx plannerFixture) transactions(t *testing.T) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(fx.binDir, plannerTxnPrefix+"*"))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

// onPlannerEvent replaces plannerEvent for the rest of the test.
func onPlannerEvent(t *testing.T, fn func(step, path string) error) {
	t.Helper()
	saved := plannerEvent
	t.Cleanup(func() { plannerEvent = saved })
	plannerEvent = fn
}

// failAt makes plannerEvent fail at one step for the rest of the test.
func failAt(t *testing.T, step string) {
	t.Helper()
	onPlannerEvent(t, func(got, _ string) error {
		if got == step {
			return errors.New("injected failure at " + step)
		}
		return nil
	})
}

// checkPlannerPairReceipts checks that each present receipt matches its binary.
//
// Example: an absent receipt is recoverable, while a stale receipt is rejected.
func (fx plannerFixture) checkPlannerPairReceipts() error {
	for _, name := range plannerPairTools {
		path := filepath.Join(fx.binDir, name)
		receipt, err := os.ReadFile(path + ".sha256")
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		digest, err := digestFile(path)
		if err != nil {
			return err
		}
		if string(receipt) != digest+"  "+name+"\n" {
			return fmt.Errorf("torn planner pair %s", name)
		}
	}
	return nil
}

func TestPlannerApplyPublishesToolsWithLoaderReceipts(t *testing.T) {
	fx := newPlannerFixture(t)
	fx.seedPreviousPair(t)
	onPlannerEvent(t,
		// Check every publication step for a stale receipt.
		//
		// Example: a binary rename before receipt removal fails the invariant.
		func(
			_ string,
			_ string,
		) error {
			return fx.checkPlannerPairReceipts()
		})

	planner, err := plannerApply(fx.root, fx.goTool, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatalf("plannerApply: %v", err)
	}
	if want := filepath.Join(fx.binDir, "eci-command-plan"); planner != want {
		t.Errorf("planner = %q, want %q", planner, want)
	}
	for _, name := range plannerPairTools {
		path := filepath.Join(fx.binDir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o755 {
			t.Errorf("%s mode = %o, want 755", name, info.Mode().Perm())
		}
		if err := checkHostExecutable(path, runtime.GOOS, runtime.GOARCH); err != nil {
			t.Errorf("%s host format: %v", name, err)
		}
		receipt, err := os.ReadFile(path + ".sha256")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := digestFile(path)
		if err != nil {
			t.Fatal(err)
		}
		// hooks/lib/eci-native-binary.sh reads one line matching ^([0-9a-f]{64})"  <name>"$.
		if !regexp.MustCompile(`^[0-9a-f]{64}  `+regexp.QuoteMeta(name)+`\n$`).Match(receipt) || string(receipt) != digest+"  "+name+"\n" {
			t.Errorf("%s receipt = %q, want %q", name, receipt, digest+"  "+name+"\n")
		}
	}
	if left := fx.transactions(t); len(left) != 0 {
		t.Errorf("transactions left: %v", left)
	}
}

func TestPlannerApplyBuildFailureKeepsPreviousPair(t *testing.T) {
	fx := newPlannerFixture(t)
	before := fx.seedPreviousPair(t)
	fx.writeModule(t, "eci-safe-import", "package main\n\nfunc main() { undefined() }\n")

	_, err := plannerApply(fx.root, fx.goTool, runtime.GOOS, runtime.GOARCH)
	if err == nil || !strings.Contains(err.Error(), "[ECI_PLANNER_BUILD_FAILED] build of eci-safe-import failed") {
		t.Fatalf("plannerApply error = %v, want the eci-safe-import build failure", err)
	}
	fx.assertPair(t, before)
	if left := fx.transactions(t); len(left) != 0 {
		t.Errorf("transactions left: %v", left)
	}
}

func TestPlannerApplyFailureRestoresPreviousPair(t *testing.T) {
	for _, tt := range []struct{ name, step string }{
		{"receipt removal fails before binary rename", "before-remove-receipt:eci-safe-import.sha256"},
		{"publish fails after two tools are renamed", "before-rename:eci-hook-helper"},
		{"receipt fails after its tool is renamed", "before-rename:eci-safe-import.sha256"},
		{"last receipt fails after its rename", "after-rename:eci-hook-helper.sha256"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fx := newPlannerFixture(t)
			before := fx.seedPreviousPair(t)
			onPlannerEvent(t,
				// Check the invariant before injecting the selected failure.
				//
				// Example: a receipt remains absent while its binary is replaced.
				func(
					got string,
					_ string,
				) error {
					if err := fx.checkPlannerPairReceipts(); err != nil {
						return err
					}
					if got == tt.step {
						return errors.New("injected failure at " + tt.step)
					}
					return nil
				})

			_, err := plannerApply(fx.root, fx.goTool, runtime.GOOS, runtime.GOARCH)
			if err == nil || !strings.Contains(err.Error(), "injected failure at "+tt.step) {
				t.Fatalf("plannerApply error = %v, want the injected failure", err)
			}
			fx.assertPair(t, before)
			if left := fx.transactions(t); len(left) != 0 {
				t.Errorf("transactions left: %v", left)
			}
		})
	}
}

// TestPlannerApplyFailedBinaryRestoreLeavesReceiptAbsent preserves reprovisioning.
//
// Example: a failed rollback of the importer does not restore its old receipt.
func TestPlannerApplyFailedBinaryRestoreLeavesReceiptAbsent(t *testing.T) {
	fx := newPlannerFixture(t)
	fx.seedPreviousPair(t)
	onPlannerEvent(t,
		// Check each state and fail once on publication and binary restore.
		//
		// Example: a failed binary restore keeps its receipt absent.
		func(
			step string,
			_ string,
		) error {
			if err := fx.checkPlannerPairReceipts(); err != nil {
				return err
			}
			switch step {
			case "after-rename:eci-safe-import.sha256":
				return errors.New("injected publication failure")
			case "before-restore:eci-safe-import":
				return errors.New("injected binary restore failure")
			default:
				return nil
			}
		})
	_, err := plannerApply(fx.root, fx.goTool, runtime.GOOS, runtime.GOARCH)
	require.ErrorContains(t, err, "injected binary restore failure")
	_, err = os.Lstat(filepath.Join(fx.binDir, "eci-safe-import.sha256"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.Len(t, fx.transactions(t), 1)
}

// TestPlannerPublicationSignalReportsRollbackFailure checks the message main prints.
//
// Example: SIGTERM with a failed restore reports both the signal and rollback error.
func TestPlannerPublicationSignalReportsRollbackFailure(t *testing.T) {
	fx := newPlannerFixture(t)
	fx.seedPreviousPair(t)
	txn := t.TempDir()
	binary := filepath.Join(txn, "eci-command-plan")
	receipt := binary + ".sha256"
	writeTestFile(t, binary, "staged binary\n", 0o755)
	writeTestFile(t, receipt, "staged receipt\n", 0o644)
	tools := []plannerTool{{
		Binary:  plannerLeaf{name: "eci-command-plan", install: binary},
		Receipt: plannerLeaf{name: "eci-command-plan.sha256", install: receipt},
	}}
	failAt(t, "before-restore:eci-command-plan")
	interrupt := make(chan os.Signal, 1)
	interrupt <- syscall.SIGTERM
	retained, err := publishPlannerLeaves(fx.binDir, txn, tools, interrupt)
	var exitErr exitError
	require.True(t, retained)
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 128+int(syscall.SIGTERM), exitErr.code)
	assert.Contains(t, exitErr.msg, "injected failure at before-restore:eci-command-plan")
	assert.NotContains(t, exitErr.msg, "previous planner pair is restored")
}

func TestPlannerApplyFailureRemovesLeavesThatWereAbsent(t *testing.T) {
	fx := newPlannerFixture(t)
	failAt(t, "before-rename:eci-hook-helper.sha256")

	if _, err := plannerApply(fx.root, fx.goTool, runtime.GOOS, runtime.GOARCH); err == nil {
		t.Fatal("plannerApply succeeded despite the injected failure")
	}
	for leaf, state := range fx.snapshot(t) {
		if state != "absent" {
			t.Errorf("%s = %s, want absent", leaf, state)
		}
	}
	if left := fx.transactions(t); len(left) != 0 {
		t.Errorf("transactions left: %v", left)
	}
}

// provisionClassification observes the shell provisioner's decision in a private home.
//
// Example: an absent receipt normally selects a build.
func (fx plannerFixture) provisionClassification(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	agentDir := filepath.Join(home, ".omp", "agent")
	require.NoError(t, os.MkdirAll(agentDir, 0o700))
	require.NoError(t, os.Symlink(fx.root, filepath.Join(agentDir, "eci")))
	provision, err := filepath.Abs(filepath.Join("..", "..", "hooks", "lib", "eci-native-provision.sh"))
	require.NoError(t, err)
	cmd := exec.Command("bash", "-c", `. "$1"; eci_native_classify eci-command-plan; printf '%s' "$eci_native_class"`, "bash", provision)
	cmd.Env = append(os.Environ(), "HOME="+home)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "classify provisioner: %s", output)
	return string(output)
}

func TestPlannerApplyRetainsForeignLeafAndTransaction(t *testing.T) {
	fx := newPlannerFixture(t)
	fx.seedPreviousPair(t)
	foreign := filepath.Join(fx.binDir, "eci-command-plan")
	var foreignReceipt, foreignData []byte
	onPlannerEvent(t,
		// Change a published binary while retaining its staged receipt.
		//
		// Example: an external write arrives before the final tool is published.
		func(
			step string,
			_ string,
		) error {
			if step == "before-rename:eci-hook-helper" {
				binary, err := os.ReadFile(foreign)
				if err != nil {
					return err
				}
				foreignReceipt, err = os.ReadFile(foreign + ".sha256")
				if err != nil {
					return err
				}
				foreignData = append(binary, []byte("\nforeign\n")...)
				if err := os.WriteFile(foreign, foreignData, 0o755); err != nil {
					return err
				}
				return errors.New("injected failure after a foreign write")
			}
			return nil
		})

	_, err := plannerApply(fx.root, fx.goTool, runtime.GOOS, runtime.GOARCH)
	require.Error(t, err)
	data, err := os.ReadFile(foreign)
	require.NoError(t, err)
	assert.Equal(t, foreignData, data, "foreign binary changed during rollback")
	assert.NoError(t, checkHostExecutable(foreign, runtime.GOOS, runtime.GOARCH))
	assert.Equal(t, "stuck", fx.provisionClassification(t))
	data, err = os.ReadFile(foreign + ".sha256")
	require.NoError(t, err)
	assert.Equal(t, foreignReceipt, data, "foreign binary receipt changed during rollback")
	assert.Len(t, fx.transactions(t), 1)
}

// TestPlannerApplyRetainsForeignBinaryWithoutReceipt covers the publication gap.
//
// Example: a foreign binary installed after receipt removal must not be rebuilt.
func TestPlannerApplyRetainsForeignBinaryWithoutReceipt(t *testing.T) {
	fx := newPlannerFixture(t)
	fx.seedPreviousPair(t)
	foreign := filepath.Join(fx.binDir, "eci-command-plan")
	executable, err := os.Executable()
	require.NoError(t, err)
	foreignData, err := os.ReadFile(executable)
	require.NoError(t, err)
	onPlannerEvent(t,
		// Install a foreign binary during the receipt-absent publication gap.
		//
		// Example: an external writer replaces the binary after receipt removal.
		func(
			step string,
			_ string,
		) error {
			if step == "before-rename:eci-command-plan" {
				if err := os.WriteFile(foreign, foreignData, 0o755); err != nil {
					return err
				}
				return errors.New("injected foreign write after receipt removal")
			}
			return nil
		})
	_, err = plannerApply(fx.root, fx.goTool, runtime.GOOS, runtime.GOARCH)
	require.Error(t, err)
	data, err := os.ReadFile(foreign)
	require.NoError(t, err)
	assert.Equal(t, foreignData, data, "foreign binary changed during rollback")
	_, err = os.Lstat(foreign + ".sha256")
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.Len(t, fx.transactions(t), 1)
	assert.Equal(t, "stuck", fx.provisionClassification(t))
	_, err = plannerApply(fx.root, fx.goTool, runtime.GOOS, runtime.GOARCH)
	assert.ErrorContains(t, err, "retained conflict")
	data, err = os.ReadFile(foreign)
	require.NoError(t, err)
	assert.Equal(t, foreignData, data, "second plannerApply changed foreign binary")
}

// TestPlannerApplyRetainsForeignReceipt preserves a concurrently changed receipt.
//
// Example: rollback leaves a foreign receipt untouched and retains the transaction.
func TestPlannerApplyRetainsForeignReceipt(t *testing.T) {
	fx := newPlannerFixture(t)
	fx.seedPreviousPair(t)
	receipt := filepath.Join(fx.binDir, "eci-command-plan.sha256")
	onPlannerEvent(t,
		// Inject a foreign receipt before publication of the final tool.
		//
		// Example: a concurrent writer replaces the planner receipt.
		func(
			step string,
			_ string,
		) error {
			if step == "before-rename:eci-hook-helper" {
				if err := os.WriteFile(receipt, []byte("foreign receipt\n"), 0o644); err != nil {
					return err
				}
				return errors.New("injected failure after a foreign receipt")
			}
			return nil
		})
	_, err := plannerApply(fx.root, fx.goTool, runtime.GOOS, runtime.GOARCH)
	require.Error(t, err)
	data, err := os.ReadFile(receipt)
	require.NoError(t, err)
	assert.Equal(t, "foreign receipt\n", string(data))
	assert.Len(t, fx.transactions(t), 1)
}

// TestPlannerApplyRetainsPairWithForeignReceiptMode preserves a matching pair.
//
// Example: an independently changed receipt mode must not trigger binary rollback.
func TestPlannerApplyRetainsPairWithForeignReceiptMode(t *testing.T) {
	fx := newPlannerFixture(t)
	fx.seedPreviousPair(t)
	binary := filepath.Join(fx.binDir, "eci-command-plan")
	receipt := binary + ".sha256"
	var stagedBinary []byte
	onPlannerEvent(t,
		// Change only receipt metadata after the matching pair is published.
		//
		// Example: a concurrent chmod changes the receipt mode to 0600.
		func(
			step string,
			_ string,
		) error {
			if step != "before-rename:eci-hook-helper" {
				return nil
			}
			var err error
			stagedBinary, err = os.ReadFile(binary)
			if err != nil {
				return err
			}
			if err := os.Chmod(receipt, 0o600); err != nil {
				return err
			}
			return errors.New("injected failure after receipt chmod")
		})
	_, err := plannerApply(fx.root, fx.goTool, runtime.GOOS, runtime.GOARCH)
	require.Error(t, err)
	data, err := os.ReadFile(binary)
	require.NoError(t, err)
	assert.Equal(t, stagedBinary, data, "binary changed after foreign receipt mode")
	info, err := os.Stat(receipt)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	assert.NoError(t, fx.checkPlannerPairReceipts(), "matching pair was torn during rollback")
	assert.Len(t, fx.transactions(t), 1)
}

func TestPlannerApplyRefusesNonRegularTarget(t *testing.T) {
	fx := newPlannerFixture(t)
	before := fx.seedPreviousPair(t)
	link := filepath.Join(fx.binDir, "eci-safe-import.sha256")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/dev/null", link); err != nil {
		t.Fatal(err)
	}
	before["eci-safe-import.sha256"] = "symlink"

	_, err := plannerApply(fx.root, fx.goTool, runtime.GOOS, runtime.GOARCH)
	if err == nil || !strings.Contains(err.Error(), "[ECI_PLANNER_PUBLISH_UNSAFE] pair target retained") {
		t.Fatalf("plannerApply error = %v, want the retained-target refusal", err)
	}
	fx.assertPair(t, before)
}

// crossBuild builds a tiny main package for goos/goarch and returns the binary path.
func crossBuild(t *testing.T, goos, goarch string) string {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/probe\n\ngo 1.24\n", 0o644)
	writeTestFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {}\n", 0o644)
	goTool, err := plannerGoTool()
	if err != nil {
		t.Fatalf("plannerGoTool: %v", err)
	}
	out := filepath.Join(dir, "probe")
	cmd := exec.Command(goTool, "build", "-o", out, ".")
	cmd.Dir = dir
	cmd.Env = plannerEnv(t.TempDir(), goos, goarch)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build for %s/%s: %v\n%s", goos, goarch, err, combined)
	}
	return out
}

// patched writes a copy of src with data written at offset.
func patched(t *testing.T, src string, offset int, data []byte) string {
	t.Helper()
	content, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	copy(content[offset:], data)
	out := filepath.Join(t.TempDir(), "patched")
	writeTestFile(t, out, string(content), 0o755)
	return out
}

// checkFormat fails the test unless checkHostExecutable accepts (want == "") or refuses with an error containing want.
func checkFormat(t *testing.T, label, path, goos, goarch, want string) {
	t.Helper()
	err := checkHostExecutable(path, goos, goarch)
	switch {
	case want == "" && err != nil:
		t.Errorf("%s: refused: %v", label, err)
	case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
		t.Errorf("%s: error = %v, want one containing %q", label, err, want)
	}
}

func TestCheckHostExecutableDarwin(t *testing.T) {
	arm64 := crossBuild(t, "darwin", "arm64")
	amd64 := crossBuild(t, "darwin", "amd64")
	fat := filepath.Join(t.TempDir(), "fat")
	writeTestFile(t, fat, string(append([]byte{0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 2}, make([]byte, 24)...)), 0o755)

	checkFormat(t, "darwin/arm64 on a forced darwin/arm64 host", arm64, "darwin", "arm64", "")
	checkFormat(t, "darwin/amd64 on a forced darwin/amd64 host", amd64, "darwin", "amd64", "")
	checkFormat(t, "arm64 CPU type on an amd64 host", arm64, "darwin", "amd64", "CPU type or file type")
	checkFormat(t, "amd64 CPU type on an arm64 host", amd64, "darwin", "arm64", "CPU type or file type")
	checkFormat(t, "universal binary", fat, "darwin", "arm64", "not a thin 64-bit Mach-O executable")
	checkFormat(t, "MH_DYLIB", patched(t, arm64, 12, []byte{0x06, 0, 0, 0}), "darwin", "arm64", "CPU type or file type")
	checkFormat(t, "darwin/386", arm64, "darwin", "386", "unsupported host")
	checkFormat(t, "windows", arm64, "windows", "arm64", "unsupported host")
	if runtime.GOOS == "linux" {
		checkFormat(t, "darwin Mach-O on this Linux host", arm64, "linux", runtime.GOARCH, "not an ELF executable")
	}
}

func TestCheckHostExecutableLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the linux branch compares against /proc/self/exe; this host is " + runtime.GOOS)
	}
	native := crossBuild(t, "linux", runtime.GOARCH)
	script := filepath.Join(t.TempDir(), "script")
	writeTestFile(t, script, "#!/bin/sh\nexit 0\n# padding padding\n", 0o755)
	otherMachine := []byte{0x3e, 0x00} // EM_X86_64
	if runtime.GOARCH == "amd64" {
		otherMachine = []byte{0xb7, 0x00} // EM_AARCH64
	}

	checkFormat(t, "native ELF", native, "linux", runtime.GOARCH, "")
	checkFormat(t, "shell script", script, "linux", runtime.GOARCH, "not an ELF executable")
	checkFormat(t, "foreign e_machine", patched(t, native, 18, otherMachine), "linux", runtime.GOARCH, "does not match this host")
	checkFormat(t, "ELFCLASS32", patched(t, native, 4, []byte{0x01}), "linux", runtime.GOARCH, "does not match this host")
	checkFormat(t, "big-endian", patched(t, native, 5, []byte{0x02}), "linux", runtime.GOARCH, "does not match this host")
	checkFormat(t, "FreeBSD OS ABI", patched(t, native, 7, []byte{0x09}), "linux", runtime.GOARCH, "does not match this host")
}

func TestPlannerGoToolRequiresAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "rel"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, "rel", "go"), "#!/bin/sh\n", 0o755)
	t.Chdir(dir)

	t.Setenv("PATH", "rel")
	if _, err := plannerGoTool(); err == nil || !strings.Contains(err.Error(), "not an absolute path") {
		t.Errorf("relative PATH entry: error = %v, want the absolute-path refusal", err)
	}
	t.Setenv("PATH", filepath.Join(dir, "empty"))
	if _, err := plannerGoTool(); err == nil || !strings.Contains(err.Error(), "go not found on PATH") {
		t.Errorf("no go on PATH: error = %v, want the not-found refusal", err)
	}
	t.Setenv("PATH", filepath.Join(dir, "rel"))
	if got, err := plannerGoTool(); err != nil || got != filepath.Join(dir, "rel", "go") {
		t.Errorf("absolute PATH entry: got %q, %v", got, err)
	}
}

func TestPlannerEnvPinsGOFLAGSAndInheritsModulePolicy(t *testing.T) {
	t.Setenv("GOFLAGS", "-mod=mod")
	t.Setenv("GOPROXY", "https://proxy.example")
	t.Setenv("GOSUMDB", "sum.golang.org")
	t.Setenv("GOROOT", "/stale/root")
	t.Setenv("GOAMD64", "v3")
	t.Setenv("GONOSUMDB", "unset below")
	if err := os.Unsetenv("GONOSUMDB"); err != nil {
		t.Fatal(err)
	}

	env := map[string][]string{}
	for _, kv := range plannerEnv("/txn/tmp", "darwin", "arm64") {
		key, value, _ := strings.Cut(kv, "=")
		env[key] = append(env[key], value)
	}
	for key, want := range map[string]string{
		"GOFLAGS": " ", "GOPROXY": "https://proxy.example", "GOSUMDB": "sum.golang.org",
		"GOOS": "darwin", "GOARCH": "arm64", "GOTOOLCHAIN": "local", "GOWORK": "off", "CGO_ENABLED": "0",
		"GOTMPDIR": "/txn/tmp", "TMPDIR": "/txn/tmp",
	} {
		if got := env[key]; len(got) != 1 || got[0] != want {
			t.Errorf("%s = %q, want exactly [%q]", key, got, want)
		}
	}
	for _, key := range []string{"GOROOT", "GOAMD64", "GONOSUMDB"} {
		if got, ok := env[key]; ok {
			t.Errorf("%s = %q, want unset", key, got)
		}
	}
}

// TestPlannerApplyBuildHelper is the child process of TestPlannerApplyInterruptedDuringBuildLeavesNoTransaction.
// It runs plannerApply on the fixture named by the environment and records the outcome in the result file.
func TestPlannerApplyBuildHelper(t *testing.T) {
	root := os.Getenv("ECI_PLANNER_BUILD_HELPER_ROOT")
	if root == "" {
		t.Skip("runs only as the child of TestPlannerApplyInterruptedDuringBuildLeavesNoTransaction")
	}
	_, err := plannerApply(root, os.Getenv("ECI_PLANNER_BUILD_HELPER_GO"), runtime.GOOS, runtime.GOARCH)
	outcome := "ok"
	if err != nil {
		outcome = err.Error()
	}
	if err := os.WriteFile(os.Getenv("ECI_PLANNER_BUILD_HELPER_RESULT"), []byte(outcome+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestPlannerApplyInterruptedDuringBuildLeavesNoTransaction sends SIGTERM to a child process while it builds the
// pair. The child must return from plannerApply, so its transaction is removed and the previous pair is kept.
func TestPlannerApplyInterruptedDuringBuildLeavesNoTransaction(t *testing.T) {
	fx := newPlannerFixture(t)
	before := fx.seedPreviousPair(t)
	result := filepath.Join(t.TempDir(), "outcome")

	child := exec.Command(os.Args[0], "-test.run=^TestPlannerApplyBuildHelper$")
	child.Env = append(os.Environ(),
		"ECI_PLANNER_BUILD_HELPER_ROOT="+fx.root,
		"ECI_PLANNER_BUILD_HELPER_GO="+fx.goTool,
		"ECI_PLANNER_BUILD_HELPER_RESULT="+result,
	)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	// plannerApply creates its transaction before the first build, so the transaction marks the build phase.
	deadline := time.Now().Add(time.Minute)
	for len(fx.transactions(t)) == 0 {
		if time.Now().After(deadline) {
			child.Process.Kill()
			child.Wait()
			t.Fatal("the child never created its transaction")
		}
		time.Sleep(time.Millisecond)
	}
	if err := child.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Errorf("child did not return from plannerApply after SIGTERM: %v", err)
	}
	outcome, err := os.ReadFile(result)
	if err != nil || !strings.Contains(string(outcome), "[ECI_PLANNER_INTERRUPTED]") {
		t.Errorf("child outcome = %q, %v; want the interruption", outcome, err)
	}
	if left := fx.transactions(t); len(left) != 0 {
		t.Errorf("transactions left: %v", left)
	}
	fx.assertPair(t, before)
}

// plannerStartChild starts the child that runs plannerApply on fx, using the named helper test and the extra
// environment, and waits until the child's transaction exists, which means its build phase has begun.
func plannerStartChild(t *testing.T, fx plannerFixture, helper, result string, env ...string) *exec.Cmd {
	t.Helper()
	child := exec.Command(os.Args[0], "-test.run=^"+helper+"$")
	child.Env = append(append(os.Environ(),
		"ECI_PLANNER_BUILD_HELPER_ROOT="+fx.root,
		"ECI_PLANNER_BUILD_HELPER_GO="+fx.goTool,
		"ECI_PLANNER_BUILD_HELPER_RESULT="+result,
	), env...)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Minute)
	for len(fx.transactions(t)) == 0 {
		if time.Now().After(deadline) {
			child.Process.Kill()
			child.Wait()
			t.Fatal("the child never created its transaction")
		}
		time.Sleep(time.Millisecond)
	}
	// go creates its work directory under the transaction's tmp before it starts compiling. Once that directory
	// exists, a signal reaches a compiler that is still running rather than go's startup.
	txn := fx.transactions(t)[0]
	for {
		if work, _ := filepath.Glob(filepath.Join(txn, "tmp", "go-build*", "b*")); len(work) > 0 {
			break
		}
		if time.Now().After(deadline) {
			child.Process.Kill()
			child.Wait()
			t.Fatal("the child never started compiling")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	return child
}

// plannerWaitChild waits for the child to exit and fails the test if it takes longer than limit.
func plannerWaitChild(t *testing.T, child *exec.Cmd, limit time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		child.Process.Kill()
		<-done
		t.Fatalf("the child did not return within %v after its signal", limit)
		return nil
	}
}

// TestPlannerApplyIgnoredHangupHelper is the child of TestPlannerApplyIgnoredHangupDoesNotAbort. It starts with
// SIGHUP ignored, as under nohup, and runs plannerApply.
func TestPlannerApplyIgnoredHangupHelper(t *testing.T) {
	root := os.Getenv("ECI_PLANNER_BUILD_HELPER_ROOT")
	if root == "" {
		t.Skip("runs only as the child of TestPlannerApplyIgnoredHangupDoesNotAbort")
	}
	signal.Ignore(syscall.SIGHUP)
	_, err := plannerApply(root, os.Getenv("ECI_PLANNER_BUILD_HELPER_GO"), runtime.GOOS, runtime.GOARCH)
	outcome := "ok"
	if err != nil {
		outcome = err.Error()
	}
	if err := os.WriteFile(os.Getenv("ECI_PLANNER_BUILD_HELPER_RESULT"), []byte(outcome+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// plannerSlowSource returns a main package large enough that compiling it takes seconds, so a signal sent when
// the build starts reaches a compiler that is still running.
func plannerSlowSource(functions int) string {
	var b strings.Builder
	b.WriteString("package main\n\n")
	for i := range functions {
		fmt.Fprintf(&b, "func f%d(x int) int { return x*%d + %d }\n", i, i+1, i)
	}
	b.WriteString("\nfunc main() {\n\tx := 0\n")
	for i := range functions {
		fmt.Fprintf(&b, "\tx = f%d(x)\n", i)
	}
	b.WriteString("\t_ = x\n}\n")
	return b.String()
}

// TestPlannerApplyInterruptCancelsSlowBuild interrupts a build that is still compiling. The build must be cancelled
// and its process group killed, so the child returns within the deadline with the build-phase message.
func TestPlannerApplyInterruptCancelsSlowBuild(t *testing.T) {
	fx := newPlannerFixture(t)
	before := fx.seedPreviousPair(t)
	fx.writeModule(t, "eci-command-plan", plannerSlowSource(40000))
	result := filepath.Join(t.TempDir(), "outcome")
	child := plannerStartChild(t, fx, "TestPlannerApplyBuildHelper", result)
	if err := child.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := plannerWaitChild(t, child, 2*time.Second); err != nil {
		t.Errorf("child exit after SIGTERM: %v", err)
	}
	outcome, err := os.ReadFile(result)
	if err != nil || !strings.Contains(string(outcome), "during the build of ") {
		t.Errorf("child outcome = %q, %v; want an interruption during the build of a module", outcome, err)
	}
	if left := fx.transactions(t); len(left) != 0 {
		t.Errorf("transactions left: %v", left)
	}
	fx.assertPair(t, before)
}

// TestPlannerApplyIgnoredHangupDoesNotAbort runs the build with SIGHUP ignored, as under nohup. A SIGHUP during the
// build must not abort it: the run completes and publishes the pair.
func TestPlannerApplyIgnoredHangupDoesNotAbort(t *testing.T) {
	fx := newPlannerFixture(t)
	before := fx.seedPreviousPair(t)
	result := filepath.Join(t.TempDir(), "outcome")
	child := plannerStartChild(t, fx, "TestPlannerApplyIgnoredHangupHelper", result)
	if err := child.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	if err := plannerWaitChild(t, child, time.Minute); err != nil {
		t.Errorf("child exit after SIGHUP: %v", err)
	}
	outcome, err := os.ReadFile(result)
	if err != nil || strings.TrimSpace(string(outcome)) != "ok" {
		t.Errorf("child outcome = %q, %v; want the build to complete", outcome, err)
	}
	if fx.snapshot(t)["eci-command-plan"] == before["eci-command-plan"] {
		t.Errorf("the pair was not published after an ignored SIGHUP")
	}
	if left := fx.transactions(t); len(left) != 0 {
		t.Errorf("transactions left: %v", left)
	}
}

// TestPlannerApplyInterruptKillsBuildGroup interrupts a build whose go command leaves a child in the build's process
// group. Cancelling the build must kill the whole group, so the child returns promptly with the build-phase message.
// If only the go command were killed, the child would keep the group alive, and the child would return only after the
// bounded group wait, past the deadline.
func TestPlannerApplyInterruptKillsBuildGroup(t *testing.T) {
	fx := newPlannerFixture(t)
	before := fx.seedPreviousPair(t)
	dir := t.TempDir()
	pgidFile := filepath.Join(dir, "pgid")
	t.Cleanup(func() {
		// Kill the group the wrapper recorded, so no sleeper outlives the test.
		if b, err := os.ReadFile(pgidFile); err == nil {
			var pgid int
			if _, err := fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &pgid); err == nil && pgid > 0 {
				syscall.Kill(-pgid, syscall.SIGKILL)
			}
		}
	})
	// The wrapper stands in for the go command. It starts a sleeper in the build's process group, records the group
	// id, and execs the real go command, so the group outlives the go process unless it is killed.
	wrapper := filepath.Join(dir, "go")
	writeTestFile(t, wrapper, "#!/bin/sh\nsleep 30 &\necho $$ > \"$ECI_TEST_PGID_FILE\"\nexec \"$ECI_TEST_REAL_GO\" \"$@\"\n", 0o755)
	wrapped := fx
	wrapped.goTool = wrapper
	result := filepath.Join(dir, "outcome")
	child := plannerStartChild(t, wrapped, "TestPlannerApplyBuildHelper", result,
		"ECI_TEST_REAL_GO="+fx.goTool, "ECI_TEST_PGID_FILE="+pgidFile)
	if err := child.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := plannerWaitChild(t, child, 3*time.Second); err != nil {
		t.Errorf("child exit after SIGTERM: %v", err)
	}
	outcome, err := os.ReadFile(result)
	if err != nil || !strings.Contains(string(outcome), "during the build of ") {
		t.Errorf("child outcome = %q, %v; want an interruption during the build of a module", outcome, err)
	}
	if left := fx.transactions(t); len(left) != 0 {
		t.Errorf("transactions left: %v", left)
	}
	fx.assertPair(t, before)
}
