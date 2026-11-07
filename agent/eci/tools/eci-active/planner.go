package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const (
	plannerPublishLock = ".planner-importer.publish.lock"
	plannerTxnPrefix   = ".planner-importer.txn."
)

// plannerPairTools are the native gate binaries built and published as one pair. Each tool is published
// beside its <name>.sha256 receipt in the canonical bin/, the files hooks/lib/eci-native-binary.sh verifies
// before it runs a tool.
var plannerPairTools = []string{"eci-command-plan", "eci-safe-import", "eci-hook-helper"}

// plannerScrubbedEnv are inherited variables that must not steer the planner build. GOPROXY, GOSUMDB,
// GONOSUMDB, GOMODCACHE and GOCACHE stay inherited: modules download through the caller's proxy and go.sum
// verifies them under -mod=readonly.
var plannerScrubbedEnv = map[string]bool{
	"GOCACHEPROG": true, "GO386": true, "GOAMD64": true, "GOARM": true, "GOARM64": true,
	"GOMIPS": true, "GOMIPS64": true, "GOPPC64": true, "GORISCV64": true, "GOWASM": true,
	"GOOS": true, "GOARCH": true, "GOFLAGS": true, "GOWORK": true, "GOTOOLCHAIN": true,
	"GOROOT": true, "GOTMPDIR": true, "TMPDIR": true, "CGO_ENABLED": true,
}

// plannerEvent is a no-op rendezvous before and after each publication step. Tests replace it to inject a
// failure at an exact step, as planner_publication_event does in pre-migration source 575bb81
// bin/eci-runtime-sync.
var plannerEvent = func(step, path string) error { return nil }

// maintainPlanner rebuilds the planner pair from source with the go command on PATH and publishes it, with its
// receipts, into the canonical bin/ as one transaction. It then refreshes the module-directory planner that
// the eci-command-plan tests execute, and runs bin/eci-runtime-sync planner-apply for the optional Kimi peer.
// Peer failure is advisory.
func maintainPlanner(args []string) error {
	if len(args) != 1 {
		return usageError()
	}
	root, err := canonicalEciRoot()
	if err != nil {
		return err
	}
	goTool, err := plannerGoTool()
	if err != nil {
		return err
	}
	built, err := plannerApply(root, goTool, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "PLANNER_LOCAL_SYNCED %s\n", filepath.Join(root, "bin"))
	srcPlanner := filepath.Join(root, "hooks", "lib", "eci-command-plan-go", "eci-command-plan")
	if err := refuseUnsafeLeaf(srcPlanner); err != nil {
		fmt.Fprintf(os.Stderr, "ECI planner advisory: module-directory planner not refreshed: %v\n", err)
	} else if err := copyFileAtomic(built, srcPlanner, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "ECI planner advisory: module-directory planner not refreshed: %v\n", err)
	}
	peer := exec.Command(filepath.Join(root, "bin", "eci-runtime-sync"), "planner-apply", "--target", kimiHome())
	peer.Stdout = os.Stderr
	peer.Stderr = os.Stderr
	if err := peer.Run(); err != nil {
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

// plannerGoTool returns the go command on PATH, which must resolve to an absolute path, as
// hooks/build-native-binaries.sh selects it.
func plannerGoTool() (string, error) {
	path, err := exec.LookPath("go")
	switch {
	case errors.Is(err, exec.ErrDot) || (err == nil && !filepath.IsAbs(path)):
		return "", exitError{code: 1, msg: fmt.Sprintf("[ECI_PLANNER_TOOLCHAIN_MISSING] go resolves to %q, not an absolute path; no planner state was changed.", path)}
	case err != nil:
		return "", exitError{code: 1, msg: "[ECI_PLANNER_TOOLCHAIN_MISSING] go not found on PATH; install Go and retry; no planner state was changed."}
	}
	return path, nil
}

// plannerEnv is the caller's environment without plannerScrubbedEnv, plus the fixed build settings. GOFLAGS is
// one space: an empty GOFLAGS lets a GOFLAGS entry in the user's go env file apply.
func plannerEnv(tmp, goos, goarch string) []string {
	env := make([]string, 0, len(os.Environ())+8)
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if !plannerScrubbedEnv[key] {
			env = append(env, kv)
		}
	}
	return append(env,
		"CGO_ENABLED=0", "GOFLAGS= ", "GOWORK=off", "GOTOOLCHAIN=local",
		"GOOS="+goos, "GOARCH="+goarch, "GOTMPDIR="+tmp, "TMPDIR="+tmp,
	)
}

// plannerBuildTool builds one planner module into the transaction and checks the output's host format. The go
// command runs in its own process group, which cancelling ctx kills, so no compiler or linker outlives the build
// and writes into the transaction after it is removed.
func plannerBuildTool(ctx context.Context, root, goTool, txn, name, goos, goarch string) (string, error) {
	module := filepath.Join(root, "hooks", "lib", name+"-go")
	out := filepath.Join(txn, "built", name)
	cmd := exec.CommandContext(ctx, goTool, "build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-pgo=off", "-ldflags=-buildid=", "-o", out, ".")
	cmd.Dir = module
	cmd.Env = plannerEnv(filepath.Join(txn, "tmp"), goos, goarch)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	err := cmd.Run()
	if cmd.Process != nil {
		plannerWaitForGroup(cmd.Process.Pid)
	}
	if err != nil {
		return "", fmt.Errorf("[ECI_PLANNER_BUILD_FAILED] build of %s failed: %v; no planner state was changed", name, err)
	}
	if err := os.Chmod(out, 0o755); err != nil {
		return "", err
	}
	if err := checkHostExecutable(out, goos, goarch); err != nil {
		return "", fmt.Errorf("[ECI_PLANNER_BUILD_FAILED] incompatible host executable %s: %v; no planner state was changed", name, err)
	}
	return out, nil
}

// plannerWaitForGroup returns once no process is left in process group pgid, or after a bounded wait. A cancelled
// build's compiler or linker can still run after the go command has exited.
func plannerWaitForGroup(pgid int) {
	for range 500 {
		if syscall.Kill(-pgid, 0) != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// plannerInterrupted returns the publication exit for a termination signal that arrived during stage, or nil.
func plannerInterrupted(interrupt <-chan os.Signal, stage string) error {
	select {
	case s := <-interrupt:
		return exitError{code: 128 + int(s.(syscall.Signal)), msg: fmt.Sprintf("[ECI_PLANNER_INTERRUPTED] %v during %s; no planner state was changed", s, stage)}
	default:
		return nil
	}
}

// Header bytes the native-binary loader (hooks/lib/eci-native-binary.sh) accepts.
var (
	elfMagic     = []byte{0x7f, 'E', 'L', 'F'}
	machoMagic64 = []byte{0xcf, 0xfa, 0xed, 0xfe}
	machoExecute = []byte{0x02, 0x00, 0x00, 0x00}
	machoCPU     = map[string][]byte{"amd64": {0x07, 0x00, 0x00, 0x01}, "arm64": {0x0c, 0x00, 0x00, 0x01}}
)

// linuxHostHeader returns the first 20 bytes of the running executable, the reference the loader takes from
// /proc/$$/exe.
var linuxHostHeader = func() ([]byte, error) { return readHeader("/proc/self/exe", 20) }

// readHeader returns exactly n leading bytes of path.
func readHeader(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := make([]byte, n)
	if _, err := io.ReadFull(f, h); err != nil {
		return nil, fmt.Errorf("header of %s is shorter than %d bytes", path, n)
	}
	return h, nil
}

// checkHostExecutable reports whether path has the executable format the loader accepts for goos/goarch.
// linux: ELF magic; class (byte 4), byte order (byte 5) and machine (bytes 18-19) equal to the running
// executable's; OS ABI (byte 7) none or Linux. darwin: thin 64-bit Mach-O (bytes 0-3), the CPU type of goarch
// (bytes 4-7) and MH_EXECUTE (bytes 12-15); a universal binary fails the magic check. Other hosts are refused.
func checkHostExecutable(path, goos, goarch string) error {
	switch goos {
	case "linux":
		h, err := readHeader(path, 20)
		if err != nil {
			return err
		}
		host, err := linuxHostHeader()
		if err != nil {
			return err
		}
		if !bytes.Equal(h[0:4], elfMagic) {
			return errors.New("not an ELF executable")
		}
		if h[4] != host[4] || h[5] != host[5] || h[18] != host[18] || h[19] != host[19] || (h[7] != 0x00 && h[7] != 0x03) {
			return errors.New("ELF class, byte order, OS ABI or machine does not match this host")
		}
	case "darwin":
		cpu, ok := machoCPU[goarch]
		if !ok {
			return fmt.Errorf("unsupported host %s/%s", goos, goarch)
		}
		h, err := readHeader(path, 16)
		if err != nil {
			return err
		}
		if !bytes.Equal(h[0:4], machoMagic64) {
			return errors.New("not a thin 64-bit Mach-O executable")
		}
		if !bytes.Equal(h[4:8], cpu) || !bytes.Equal(h[12:16], machoExecute) {
			return fmt.Errorf("Mach-O CPU type or file type is not an %s executable", goarch)
		}
	default:
		return fmt.Errorf("unsupported host %s/%s", goos, goarch)
	}
	return nil
}

// plannerLeaf is one published file under bin/: a tool or its receipt.
type plannerLeaf struct {
	name    string // leaf name under bin/
	install string // staged file in the transaction
	backup  string // copy of the previous regular leaf; empty when the leaf was absent
	old     string // leaf state before publication
	new     string // state of the staged file
}

// plannerLeafState describes path as absent, symlink, other, or regular:<mode>:<sha256>.
func plannerLeafState(path string) (string, error) {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "absent", nil
	case err != nil:
		return "", err
	case info.Mode()&os.ModeSymlink != 0:
		return "symlink", nil
	case !info.Mode().IsRegular():
		return "other", nil
	}
	digest, err := digestFile(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("regular:%o:%s", info.Mode().Perm(), digest), nil
}

// copyLeaf copies a regular leaf to dst with the same permission bits.
func copyLeaf(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	return copyFileAtomic(src, dst, info.Mode().Perm())
}

// plannerApply builds the pair into a transaction under <root>/bin and publishes the three tools and their
// receipts there. It returns the built planner. Under the publication lock shared with bin/eci-runtime-sync.
func plannerApply(root, goTool, goos, goarch string) (string, error) {
	binDir := filepath.Join(root, "bin")
	if info, err := os.Lstat(binDir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("[ECI_PLANNER_PUBLISH_UNSAFE] bin directory is missing or not a plain directory: %s; no planner state was changed", binDir)
	}
	lock, err := lockFileExclusive(filepath.Join(binDir, plannerPublishLock))
	if err != nil {
		return "", err
	}
	defer lock.Close()
	// The first termination signal cancels the builds and is handed to publication. The handler exists before the
	// transaction does, so an interrupt at any later point reaches the cleanup below.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	interrupt := make(chan os.Signal, 1)
	watched := make(chan struct{})
	defer close(watched)
	go func() {
		select {
		case s := <-signals:
			interrupt <- s
			cancel()
		case <-watched:
		}
	}()
	txn, err := os.MkdirTemp(binDir, plannerTxnPrefix)
	if err != nil {
		return "", err
	}
	retained := false
	defer func() {
		if !retained {
			os.RemoveAll(txn)
		}
	}()
	for _, dir := range []string{"built", "tmp"} {
		if err := os.Mkdir(filepath.Join(txn, dir), 0o700); err != nil {
			return "", err
		}
	}
	fmt.Fprintf(os.Stderr, "ECI build: original modules; GOCACHEPROG cleared; scratch=%s\n", txn)

	leaves := make([]*plannerLeaf, 0, 2*len(plannerPairTools))
	for _, name := range plannerPairTools {
		built, err := plannerBuildTool(ctx, root, goTool, txn, name, goos, goarch)
		if err != nil {
			if interruption := plannerInterrupted(interrupt, "the build of "+name); interruption != nil {
				return "", interruption
			}
			return "", err
		}
		digest, err := digestFile(built)
		if err != nil {
			return "", fmt.Errorf("[ECI_PLANNER_RECEIPT_FAILED] digest of %s unavailable: %v; no planner state was changed", name, err)
		}
		receipt := filepath.Join(txn, "built", name+".sha256")
		if err := writeFileAtomic(receipt, strings.NewReader(digest+"  "+name+"\n"), 0o644); err != nil {
			return "", fmt.Errorf("[ECI_PLANNER_RECEIPT_FAILED] receipt of %s not staged: %v; no planner state was changed", name, err)
		}
		leaves = append(leaves, &plannerLeaf{name: name, install: built}, &plannerLeaf{name: name + ".sha256", install: receipt})
	}
	if interruption := plannerInterrupted(interrupt, "the builds"); interruption != nil {
		return "", interruption
	}
	retained, err = publishPlannerLeaves(binDir, txn, leaves, interrupt)
	if err != nil {
		return "", err
	}
	return filepath.Join(binDir, "eci-command-plan"), nil
}

// publishPlannerLeaves installs the staged leaves under binDir as one unit, following planner_prepare_pair,
// planner_publish_pair, planner_restore_pair and planner_finish of pre-migration source 575bb81
// bin/eci-runtime-sync. Every previous regular leaf is backed up first. Each rename first confirms that its
// leaf still has the state observed before publication. A failure, or a termination signal, before every
// leaf has its staged state restores the previous leaves. A leaf that changed under the transaction is left
// in place and reported; the transaction directory is then retained for recovery and retained is true.
func publishPlannerLeaves(binDir, txn string, leaves []*plannerLeaf, interrupt <-chan os.Signal) (retained bool, err error) {
	for _, l := range leaves {
		path := filepath.Join(binDir, l.name)
		if l.old, err = plannerLeafState(path); err != nil {
			return false, err
		}
		switch {
		case l.old == "absent":
		case strings.HasPrefix(l.old, "regular:"):
			l.backup = filepath.Join(txn, l.name+".backup")
			if err := copyLeaf(path, l.backup); err != nil {
				return false, err
			}
		default:
			return false, fmt.Errorf("[ECI_PLANNER_PUBLISH_UNSAFE] pair target retained: %s (%s); no planner state was changed", path, l.old)
		}
		if l.new, err = plannerLeafState(l.install); err != nil {
			return false, err
		}
	}

	failure := renamePlannerLeaves(binDir, leaves)
	if failure == nil {
		select {
		case s := <-interrupt:
			failure = exitError{code: 128 + int(s.(syscall.Signal)), msg: fmt.Sprintf("[ECI_PLANNER_PUBLISH_FAILED] %v during publication; the previous planner pair is restored", s)}
		default:
			return false, nil
		}
	}
	if !restorePlannerLeaves(binDir, txn, leaves) {
		fmt.Fprintf(os.Stderr, "ECI recovery transaction retained: %s\n", txn)
		return true, failure
	}
	return false, failure
}

// renamePlannerLeaves renames every staged leaf into place and confirms that each then has its staged state.
func renamePlannerLeaves(binDir string, leaves []*plannerLeaf) error {
	for _, l := range leaves {
		path := filepath.Join(binDir, l.name)
		if err := plannerEvent("before-rename:"+l.name, path); err != nil {
			return err
		}
		if state, err := plannerLeafState(path); err != nil || state != l.old {
			return fmt.Errorf("[ECI_PLANNER_PUBLISH_FAILED] %s changed during publication", path)
		}
		if err := os.Rename(l.install, path); err != nil {
			return fmt.Errorf("[ECI_PLANNER_PUBLISH_FAILED] %v", err)
		}
		if err := plannerEvent("after-rename:"+l.name, path); err != nil {
			return err
		}
	}
	for _, l := range leaves {
		path := filepath.Join(binDir, l.name)
		if state, err := plannerLeafState(path); err != nil || state != l.new {
			return fmt.Errorf("[ECI_PLANNER_PUBLISH_FAILED] %s does not match its staged state", path)
		}
	}
	return nil
}

// restorePlannerLeaves returns every leaf that has its staged state to its previous state and reports the
// final state of each leaf. It reports false when any leaf could not be restored or held a foreign state.
func restorePlannerLeaves(binDir, txn string, leaves []*plannerLeaf) bool {
	ok := true
	for _, l := range leaves {
		path := filepath.Join(binDir, l.name)
		state, err := plannerLeafState(path)
		if err != nil {
			ok = false
			continue
		}
		if state == l.old {
			continue
		}
		if state != l.new {
			fmt.Fprintf(os.Stderr, "ECI recovery retained foreign state: %s=%s\n", path, state)
			ok = false
			continue
		}
		if err := plannerEvent("before-restore:"+l.name, path); err != nil {
			ok = false
			continue
		}
		if l.old == "absent" {
			err = os.Remove(path)
		} else {
			restore := filepath.Join(txn, l.name+".restore")
			if err = copyLeaf(l.backup, restore); err == nil {
				err = os.Rename(restore, path)
			}
		}
		if err != nil {
			ok = false
			continue
		}
		if state, err := plannerLeafState(path); err != nil || state != l.old {
			ok = false
		}
	}
	for _, l := range leaves {
		path := filepath.Join(binDir, l.name)
		state, err := plannerLeafState(path)
		if err != nil {
			state = "unknown"
		}
		fmt.Fprintf(os.Stderr, "ECI recovery state: %s=%s\n", path, state)
	}
	return ok
}

// canonicalEciRoot requires the running binary to be the canonical ECI home under HOME. The planner sources
// are read from this root, so a binary run from elsewhere refuses rather than building a foreign tree.
func canonicalEciRoot() (string, error) {
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
