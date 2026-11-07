package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// plannerFixture is a canonical-root layout with three tiny planner modules and a bin/ for the pair.
type plannerFixture struct {
	root   string // stands in for $HOME/.omp/agent/eci
	binDir string // <root>/bin
	goTool string // go command from PATH
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

	planner, err := plannerApply(fx.root, fx.goTool)
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
		if !isELFFile(path) {
			t.Errorf("%s is not an ELF executable", name)
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

	_, err := plannerApply(fx.root, fx.goTool)
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

			_, err := plannerApply(fx.root, fx.goTool)
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

	if _, err := plannerApply(fx.root, fx.goTool); err == nil {
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

	if _, err := plannerApply(fx.root, fx.goTool); err == nil {
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

	_, err := plannerApply(fx.root, fx.goTool)
	if err == nil || !strings.Contains(err.Error(), "[ECI_PLANNER_PUBLISH_UNSAFE] pair target retained") {
		t.Fatalf("plannerApply error = %v, want the retained-target refusal", err)
	}
	fx.assertPair(t, before)
}
