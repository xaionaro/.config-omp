package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	plannerGo            = "/usr/lib/go-1.26/bin/go"
	plannerGoRoot        = "/usr/lib/go-1.26"
	plannerContract      = "original-go-build/v1"
	plannerReceiptName   = ".eci-command-plan.provenance"
	plannerPublishLock   = ".planner-importer.publish.lock"
	plannerLocalProvider = "eci"
	plannerPeerProvider  = "kimi"
)

// plannerPairTools are the native binaries built as one pair.
var plannerPairTools = []string{"eci-command-plan", "eci-safe-import", "eci-hook-helper"}

// plannerScrubbedEnv are variables that must not steer the planner build.
var plannerScrubbedEnv = map[string]bool{
	"GOCACHEPROG": true, "GOARM": true, "GOARM64": true, "GOAMD64": true,
	"GOOS": true, "GOARCH": true, "GOFLAGS": true, "GOWORK": true,
	"GOTOOLCHAIN": true, "GOROOT": true, "GOTMPDIR": true, "TMPDIR": true,
	"CGO_ENABLED": true, "GOPROXY": true, "GOMODCACHE": true,
}

// maintainPlanner rebuilds the ECI planner pair from source, publishes it to the local runtime root, installs
// the local planner into the source tree, and then refreshes the optional Kimi peer. Peer failure is advisory.
func maintainPlanner(args []string) error {
	if len(args) != 1 {
		return usageError()
	}
	root, err := canonicalOmpEciRoot()
	if err != nil {
		return err
	}
	if !isRegularNoFollow(plannerGo) {
		return exitError{code: 1, msg: fmt.Sprintf("[ECI_PLANNER_TOOLCHAIN_MISSING] pinned Go toolchain is not a regular file: %s; no planner state was changed.", plannerGo)}
	}
	runtimeRoot := filepath.Join(homeDir(), "tmp", "eci-runtime")
	if err := os.MkdirAll(runtimeRoot, 0o755); err != nil {
		return err
	}
	lock, err := lockFileExclusive(filepath.Join(runtimeRoot, plannerPublishLock))
	if err != nil {
		return err
	}
	defer lock.Close()

	txn, err := os.MkdirTemp(runtimeRoot, ".planner-importer.txn.")
	if err != nil {
		return err
	}
	defer os.RemoveAll(txn)
	for _, dir := range []string{"built", "tmp"} {
		if err := os.Mkdir(filepath.Join(txn, dir), 0o755); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "ECI build: original modules; GOCACHEPROG cleared; scratch=%s\n", txn)

	rows, err := plannerRecordInputs(root)
	if err != nil {
		return fmt.Errorf("[ECI_PLANNER_INPUTS_INCOMPLETE] planner source observation failed: %v; no planner state was changed", err)
	}
	built := make(map[string]string, len(plannerPairTools))
	for _, name := range plannerPairTools {
		out, err := plannerBuildTool(root, txn, name)
		if err != nil {
			return err
		}
		built[name] = out
	}
	receipt, err := plannerReceipt(built, rows)
	if err != nil {
		return err
	}
	if err := publishPlannerPair(filepath.Join(runtimeRoot, plannerLocalProvider), built, receipt); err != nil {
		return err
	}
	srcDir := filepath.Join(root, "hooks", "lib", "eci-command-plan-go")
	if err := installSourcePlanner(srcDir, built["eci-command-plan"], receipt); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "PLANNER_LOCAL_SYNCED %s\n", filepath.Join(srcDir, "eci-command-plan"))

	if err := syncPlannerPeer(runtimeRoot, kimiHome(), built, receipt, filepath.Join(srcDir, "eci-command-plan")); err != nil {
		fmt.Fprintf(os.Stderr, "ECI planner peer synchronization advisory: %v; the local planner pair is synchronized.\n", err)
	}
	return nil
}

// kimiHome is the optional Kimi peer home.
func kimiHome() string {
	if v := os.Getenv("KIMI_CODE_HOME"); v != "" {
		return v
	}
	return filepath.Join(homeDir(), ".kimi-code")
}

// plannerModCache is the module cache holding the planner's pinned dependencies. The account home is used
// rather than $HOME so an isolated HOME still reads the operator's cache. GOPROXY=off prevents downloads.
func plannerModCache() string {
	if v := os.Getenv("GOMODCACHE"); v != "" {
		return v
	}
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		return filepath.Join(u.HomeDir, "go", "pkg", "mod")
	}
	return filepath.Join(homeDir(), "go", "pkg", "mod")
}

func plannerEnv(tmp string) []string {
	env := make([]string, 0, len(os.Environ())+12)
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if !plannerScrubbedEnv[key] {
			env = append(env, kv)
		}
	}
	return append(env,
		"CGO_ENABLED=0", "GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local",
		"GOROOT="+plannerGoRoot, "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH,
		"GOMODCACHE="+plannerModCache(), "GOPROXY=off",
		"GOTMPDIR="+tmp, "TMPDIR="+tmp,
	)
}

// plannerRecordInputs returns the module and source-input provenance rows for the planner pair.
func plannerRecordInputs(root string) ([]string, error) {
	var rows []string
	for _, name := range plannerPairTools {
		module := filepath.Join(root, "hooks", "lib", name+"-go")
		rows = append(rows, "module\t"+module)
		entries, err := os.ReadDir(module)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			n := entry.Name()
			isInput := (strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go")) || n == "go.mod" || n == "go.sum"
			if !entry.Type().IsRegular() || !isInput {
				continue
			}
			p := filepath.Join(module, n)
			digest, err := digestFile(p)
			if err != nil {
				return nil, err
			}
			rows = append(rows, "input\t"+p+"\t"+digest)
		}
	}
	return rows, nil
}

// plannerBuildTool builds one planner module into the transaction scratch directory.
func plannerBuildTool(root, txn, name string) (string, error) {
	module := filepath.Join(root, "hooks", "lib", name+"-go")
	out := filepath.Join(txn, "built", name)
	cmd := exec.Command(plannerGo, "build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-pgo=off", "-ldflags=-buildid=", "-o", out, ".")
	cmd.Dir = module
	cmd.Env = plannerEnv(filepath.Join(txn, "tmp"))
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("[ECI_PLANNER_BUILD_FAILED] build of %s failed: %v; no planner state was changed", name, err)
	}
	if err := os.Chmod(out, 0o755); err != nil {
		return "", err
	}
	if !isELFFile(out) {
		return "", fmt.Errorf("[ECI_PLANNER_BUILD_FAILED] incompatible host executable: %s", name)
	}
	return out, nil
}

// isELFFile reports whether path starts with the ELF magic.
func isELFFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return false
	}
	return magic == [4]byte{0x7f, 'E', 'L', 'F'}
}

// plannerReceipt renders the original-go-build/v1 provenance receipt for the built pair.
func plannerReceipt(built map[string]string, rows []string) ([]byte, error) {
	tools := filepath.Join(plannerGoRoot, "pkg", "tool", runtime.GOOS+"_"+runtime.GOARCH)
	sources := []struct{ key, path string }{
		{"go_binary_sha256", plannerGo},
		{"compiler_sha256", filepath.Join(tools, "compile")},
		{"linker_sha256", filepath.Join(tools, "link")},
		{"binary_sha256", built["eci-command-plan"]},
		{"importer_sha256", built["eci-safe-import"]},
		{"helper_sha256", built["eci-hook-helper"]},
	}
	digests := make(map[string]string, len(sources))
	for _, s := range sources {
		d, err := digestFile(s.path)
		if err != nil {
			return nil, fmt.Errorf("[ECI_PLANNER_PROVENANCE_INCOMPLETE] digest of %s unavailable: %v", s.key, err)
		}
		digests[s.key] = d
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "contract\t%s\n", plannerContract)
	fmt.Fprintf(&b, "go_binary_path\t%s\n", plannerGo)
	fmt.Fprintf(&b, "go_binary_sha256\t%s\n", digests["go_binary_sha256"])
	fmt.Fprintf(&b, "compiler_sha256\t%s\n", digests["compiler_sha256"])
	fmt.Fprintf(&b, "linker_sha256\t%s\n", digests["linker_sha256"])
	fmt.Fprintf(&b, "target\t%s/%s\n", runtime.GOOS, runtime.GOARCH)
	for _, row := range rows {
		b.WriteString(row)
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "binary_sha256\t%s\n", digests["binary_sha256"])
	fmt.Fprintf(&b, "importer_sha256\t%s\n", digests["importer_sha256"])
	fmt.Fprintf(&b, "helper_sha256\t%s\n", digests["helper_sha256"])
	return b.Bytes(), nil
}

// publishPlannerPair installs the three built tools and the receipt under dir.
func publishPlannerPair(dir string, built map[string]string, receipt []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if isSymlink(dir) {
		return fmt.Errorf("[ECI_PLANNER_PUBLISH_UNSAFE] publish root is a symlink: %s", dir)
	}
	for _, name := range plannerPairTools {
		if err := refuseUnsafeLeaf(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	for _, name := range plannerPairTools {
		if err := copyFileAtomic(built[name], filepath.Join(dir, name), 0o755); err != nil {
			return err
		}
	}
	receiptPath := filepath.Join(dir, plannerReceiptName)
	if err := refuseUnsafeLeaf(receiptPath); err != nil {
		return err
	}
	return writeFileAtomic(receiptPath, bytes.NewReader(receipt), 0o600)
}

// installSourcePlanner places the local planner and its receipt into the source tree.
func installSourcePlanner(srcDir, planner string, receipt []byte) error {
	bin := filepath.Join(srcDir, "eci-command-plan")
	if err := refuseUnsafeLeaf(bin); err != nil {
		return err
	}
	if err := copyFileAtomic(planner, bin, 0o755); err != nil {
		return err
	}
	receiptPath := filepath.Join(srcDir, plannerReceiptName)
	if err := refuseUnsafeLeaf(receiptPath); err != nil {
		return err
	}
	return writeFileAtomic(receiptPath, bytes.NewReader(receipt), 0o600)
}

// syncPlannerPeer publishes the pair for Kimi and hard-links the local planner into the Kimi tree.
func syncPlannerPeer(runtimeRoot, kimi string, built map[string]string, receipt []byte, localPlanner string) error {
	info, err := os.Lstat(kimi)
	if os.IsNotExist(err) {
		return fmt.Errorf("Kimi home is not present at %s", kimi)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("Kimi home is not a plain directory: %s", kimi)
	}
	if err := publishPlannerPair(filepath.Join(runtimeRoot, plannerPeerProvider), built, receipt); err != nil {
		return err
	}
	rel := "hooks/lib/eci-command-plan-go/eci-command-plan"
	if err := ensureSafeDir(kimi, rel); err != nil {
		return err
	}
	dst := filepath.Join(kimi, rel)
	if err := refuseUnsafeLeaf(dst); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(dst), fmt.Sprintf(".eci-command-plan.link.%d", os.Getpid()))
	_ = os.Remove(tmp)
	if err := os.Link(localPlanner, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// canonicalOmpEciRoot requires the running binary to be the canonical ECI home under HOME. The planner sources
// are read from this root, so a binary run from elsewhere refuses rather than building a foreign tree.
func canonicalOmpEciRoot() (string, error) {
	want, err := filepath.EvalSymlinks(filepath.Join(homeDir(), ".omp", "agent", "eci"))
	if err != nil {
		return "", exitError{code: 1, msg: "[ECI_RUNTIME_SOURCE_MISMATCH] canonical ECI home is not available under HOME; no state was changed."}
	}
	got, err := filepath.EvalSymlinks(eciBaseDir())
	if err != nil || got != want {
		return "", exitError{code: 1, msg: fmt.Sprintf("[ECI_RUNTIME_SOURCE_MISMATCH] the running ECI binary is not under %s; no state was changed.", want)}
	}
	return want, nil
}
