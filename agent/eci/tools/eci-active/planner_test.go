package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
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

func TestPlannerApplyPublishesToolsWithLoaderReceipts(t *testing.T) {
	fx := newPlannerFixture(t)
	fx.seedPreviousPair(t)

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
		{"publish fails after two tools are renamed", "before-rename:eci-hook-helper"},
		{"receipt fails after its tool is renamed", "before-rename:eci-safe-import.sha256"},
		{"last receipt fails after its rename", "after-rename:eci-hook-helper.sha256"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fx := newPlannerFixture(t)
			before := fx.seedPreviousPair(t)
			failAt(t, tt.step)

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

func TestPlannerApplyRetainsForeignLeafAndTransaction(t *testing.T) {
	fx := newPlannerFixture(t)
	fx.seedPreviousPair(t)
	foreign := filepath.Join(fx.binDir, "eci-command-plan")
	onPlannerEvent(t, func(step, _ string) error {
		if step == "before-rename:eci-hook-helper" {
			if err := os.WriteFile(foreign, []byte("foreign\n"), 0o755); err != nil {
				return err
			}
			return errors.New("injected failure after a foreign write")
		}
		return nil
	})

	if _, err := plannerApply(fx.root, fx.goTool, runtime.GOOS, runtime.GOARCH); err == nil {
		t.Fatal("plannerApply succeeded despite the injected failure")
	}
	if data, err := os.ReadFile(foreign); err != nil || string(data) != "foreign\n" {
		t.Errorf("foreign leaf = %q, %v; want it left in place", data, err)
	}
	if left := fx.transactions(t); len(left) != 1 {
		t.Errorf("transactions left: %v, want the retained transaction", left)
	}
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
