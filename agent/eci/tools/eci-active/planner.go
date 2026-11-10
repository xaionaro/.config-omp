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

// plannerNotifySignals registers the termination signals that the process does not ignore when it starts. Calling
// signal.Notify for an ignored signal would un-ignore it, so a process started under nohup would be aborted by SIGHUP.
func plannerNotifySignals(c chan<- os.Signal) {
	var watched []os.Signal
	for _, s := range []os.Signal{syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM} {
		if !signal.Ignored(s) {
			watched = append(watched, s)
		}
	}
	if len(watched) == 0 {
		return
	}
	signal.Notify(c, watched...)
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

// plannerTool groups a staged executable with its matching receipt.
//
// Example: a receipt is removed before replacing its executable.
type plannerTool struct {
	Binary  plannerLeaf
	Receipt plannerLeaf
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

// checkPlannerConflictRecords refuses retained conflicts in one transaction.
//
// Example: a recorded conflict names the bounded retention route and preserves the transaction.
func checkPlannerConflictRecords(
	binDir string,
	txnName string,
) error {
	for _, name := range plannerPairTools {
		record := filepath.Join(binDir, txnName, name+".conflict")
		_, err := os.Lstat(record)
		if err == nil {
			return fmt.Errorf("[ECI_PLANNER_PUBLISH_UNSAFE] retained conflict for %s (%s); if Bash works, run bash \"$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\" --preserve-conflict %s; otherwise see the retained planner conflict gate catalog entry. Do not remove the record or overwrite unresolved leaves", name, record, name)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect retained conflict %s: %w", record, err)
		}
	}
	return nil
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
	entries, err := os.ReadDir(binDir)
	if err != nil {
		return "", fmt.Errorf("inspect retained planner transactions: %w", err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), plannerTxnPrefix) {
			continue
		}
		if err := checkPlannerConflictRecords(binDir, entry.Name()); err != nil {
			return "", err
		}
	}
	// The first termination signal cancels the builds and is handed to publication. The handler exists before the
	// transaction does, so an interrupt at any later point reaches the cleanup below.
	signals := make(chan os.Signal, 1)
	plannerNotifySignals(signals)
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

	tools := make([]plannerTool, 0, len(plannerPairTools))
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
		tools = append(tools, plannerTool{
			Binary:  plannerLeaf{name: name, install: built},
			Receipt: plannerLeaf{name: name + ".sha256", install: receipt},
		})
	}
	if interruption := plannerInterrupted(interrupt, "the builds"); interruption != nil {
		return "", interruption
	}
	retained, err = publishPlannerLeaves(binDir, txn, tools, interrupt)
	if err != nil {
		return "", err
	}
	return filepath.Join(binDir, "eci-command-plan"), nil
}

// preparePlannerLeaf records and backs up a safe publication target.
//
// Example: a regular existing receipt is copied into the transaction.
func preparePlannerLeaf(
	binDir string,
	txn string,
	leaf *plannerLeaf,
) error {
	path := filepath.Join(binDir, leaf.name)
	old, err := plannerLeafState(path)
	if err != nil {
		return fmt.Errorf("inspect planner target %s: %w", path, err)
	}
	leaf.old = old
	switch {
	case old == "absent":
	case strings.HasPrefix(old, "regular:"):
		leaf.backup = filepath.Join(txn, leaf.name+".backup")
		if err := copyLeaf(path, leaf.backup); err != nil {
			return fmt.Errorf("back up planner target %s: %w", path, err)
		}
	default:
		return fmt.Errorf("[ECI_PLANNER_PUBLISH_UNSAFE] pair target retained: %s (%s); no planner state was changed", path, old)
	}
	leaf.new, err = plannerLeafState(leaf.install)
	if err != nil {
		return fmt.Errorf("inspect staged planner target %s: %w", leaf.install, err)
	}
	return nil
}

// publishPlannerLeaves backs up every leaf and publishes each tool with its
// receipt absent during binary replacement. Catchable failures restore the old
// leaves; foreign changes retain the transaction for recovery.
//
// Example: a failure after replacing a binary restores its previous pair.
func publishPlannerLeaves(
	binDir string,
	txn string,
	tools []plannerTool,
	interrupt <-chan os.Signal,
) (retained bool, err error) {
	for i := range tools {
		if err := preparePlannerLeaf(binDir, txn, &tools[i].Binary); err != nil {
			return false, err
		}
		if err := preparePlannerLeaf(binDir, txn, &tools[i].Receipt); err != nil {
			return false, err
		}
	}

	failure := renamePlannerLeaves(binDir, tools)
	if failure == nil {
		select {
		case s := <-interrupt:
			failure = exitError{code: 128 + int(s.(syscall.Signal)), msg: fmt.Sprintf("[ECI_PLANNER_PUBLISH_FAILED] %v during publication", s)}
		default:
			return false, nil
		}
	}
	if rollbackErr := restorePlannerLeaves(binDir, txn, tools); rollbackErr != nil {
		fmt.Fprintf(os.Stderr, "ECI recovery transaction retained: %s\n", txn)
		var interrupted exitError
		if errors.As(failure, &interrupted) {
			return true, exitError{code: interrupted.code, msg: fmt.Sprintf("%s; rollback failed: %v", interrupted.msg, rollbackErr)}
		}
		return true, errors.Join(failure, fmt.Errorf("planner rollback: %w", rollbackErr))
	}
	var interrupted exitError
	if errors.As(failure, &interrupted) {
		return false, exitError{code: interrupted.code, msg: interrupted.msg + "; the previous planner pair is restored"}
	}
	return false, failure
}

// renamePlannerLeaf installs one staged leaf after checking its expected state.
//
// Example: a staged receipt replaces the absent receipt after its binary.
func renamePlannerLeaf(
	binDir string,
	leaf *plannerLeaf,
	expected string,
) error {
	path := filepath.Join(binDir, leaf.name)
	if err := plannerEvent("before-rename:"+leaf.name, path); err != nil {
		return err
	}
	state, err := plannerLeafState(path)
	if err != nil {
		return fmt.Errorf("[ECI_PLANNER_PUBLISH_FAILED] inspect %s: %w", path, err)
	}
	if state != expected {
		return fmt.Errorf("[ECI_PLANNER_PUBLISH_FAILED] %s changed during publication", path)
	}
	if err := os.Rename(leaf.install, path); err != nil {
		return fmt.Errorf("[ECI_PLANNER_PUBLISH_FAILED] %w", err)
	}
	return plannerEvent("after-rename:"+leaf.name, path)
}

// renamePlannerLeaves removes each old receipt before replacing its binary.
// An interruption leaves a matching pair or an absent receipt for reprovision.
//
// Example: a binary rename happens between receipt removal and receipt install.
func renamePlannerLeaves(
	binDir string,
	tools []plannerTool,
) error {
	for i := range tools {
		tool := &tools[i]
		receipt := &tool.Receipt
		path := filepath.Join(binDir, receipt.name)
		if receipt.old != "absent" {
			if err := plannerEvent("before-remove-receipt:"+receipt.name, path); err != nil {
				return err
			}
			state, err := plannerLeafState(path)
			if err != nil {
				return fmt.Errorf("[ECI_PLANNER_PUBLISH_FAILED] inspect %s: %w", path, err)
			}
			if state != receipt.old {
				return fmt.Errorf("[ECI_PLANNER_PUBLISH_FAILED] %s changed during publication", path)
			}
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("[ECI_PLANNER_PUBLISH_FAILED] %w", err)
			}
			if err := plannerEvent("after-remove-receipt:"+receipt.name, path); err != nil {
				return err
			}
		}
		if err := renamePlannerLeaf(binDir, &tool.Binary, tool.Binary.old); err != nil {
			return err
		}
		if err := renamePlannerLeaf(binDir, receipt, "absent"); err != nil {
			return err
		}
	}
	for i := range tools {
		for _, l := range [2]*plannerLeaf{&tools[i].Binary, &tools[i].Receipt} {
			path := filepath.Join(binDir, l.name)
			state, err := plannerLeafState(path)
			if err != nil {
				return fmt.Errorf("[ECI_PLANNER_PUBLISH_FAILED] inspect %s: %w", path, err)
			}
			if state != l.new {
				return fmt.Errorf("[ECI_PLANNER_PUBLISH_FAILED] %s does not match its staged state", path)
			}
		}
	}
	return nil
}

// restorePlannerLeaf restores an absent or staged leaf to its previous state.
//
// Example: an absent old receipt is restored by removing the staged receipt.
func restorePlannerLeaf(
	binDir string,
	txn string,
	leaf *plannerLeaf,
	state string,
) error {
	path := filepath.Join(binDir, leaf.name)
	if err := plannerEvent("before-restore:"+leaf.name, path); err != nil {
		return err
	}
	current, err := plannerLeafState(path)
	if err != nil {
		return fmt.Errorf("inspect recovery target %s: %w", path, err)
	}
	if current != state {
		return fmt.Errorf("%s changed during recovery", path)
	}
	if leaf.old == "absent" {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove recovery target %s: %w", path, err)
		}
	} else {
		restore := filepath.Join(txn, leaf.name+".restore")
		if err := copyLeaf(leaf.backup, restore); err != nil {
			return fmt.Errorf("copy recovery backup for %s: %w", path, err)
		}
		if err := os.Rename(restore, path); err != nil {
			return fmt.Errorf("restore recovery target %s: %w", path, err)
		}
	}
	current, err = plannerLeafState(path)
	if err != nil {
		return fmt.Errorf("inspect restored target %s: %w", path, err)
	}
	if current != leaf.old {
		return fmt.Errorf("%s did not restore its previous state", path)
	}
	return plannerEvent("after-restore:"+leaf.name, path)
}

// restorePlannerPair leaves both leaves alone if either is foreign.
//
// Example: an externally changed receipt prevents restoration of its binary.
func restorePlannerPair(
	binDir string,
	txn string,
	tool *plannerTool,
) error {
	receipt := &tool.Receipt
	binary := &tool.Binary
	receiptPath := filepath.Join(binDir, receipt.name)
	binaryPath := filepath.Join(binDir, binary.name)
	receiptState, err := plannerLeafState(receiptPath)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", receiptPath, err)
	}
	binaryState, err := plannerLeafState(binaryPath)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", binaryPath, err)
	}
	if (receiptState != receipt.old && receiptState != receipt.new && receiptState != "absent") ||
		(binaryState != binary.old && binaryState != binary.new) {
		return fmt.Errorf("foreign planner pair %s=%s %s=%s", binaryPath, binaryState, receiptPath, receiptState)
	}
	if receiptState == receipt.new && receiptState != receipt.old {
		if err := plannerEvent("before-remove-staged-receipt:"+receipt.name, receiptPath); err != nil {
			return err
		}
		state, err := plannerLeafState(receiptPath)
		if err != nil {
			return fmt.Errorf("inspect %s: %w", receiptPath, err)
		}
		if state != receiptState {
			return fmt.Errorf("%s changed during recovery", receiptPath)
		}
		if err := os.Remove(receiptPath); err != nil {
			return fmt.Errorf("remove staged receipt %s: %w", receiptPath, err)
		}
		receiptState = "absent"
		if err := plannerEvent("after-remove-staged-receipt:"+receipt.name, receiptPath); err != nil {
			return err
		}
	}
	if binaryState != binary.old {
		if err := restorePlannerLeaf(binDir, txn, binary, binaryState); err != nil {
			return err
		}
	}
	if receiptState == "absent" && receipt.old != "absent" {
		if err := restorePlannerLeaf(binDir, txn, receipt, receiptState); err != nil {
			return err
		}
	}
	return nil
}

// recordPlannerConflict records only a pair observed outside the transaction's states.
//
// Example: a changed binary and absent receipt stop automatic reprovisioning.
func recordPlannerConflict(
	binDir string,
	txn string,
	tool *plannerTool,
) error {
	binary := &tool.Binary
	receipt := &tool.Receipt
	binaryPath := filepath.Join(binDir, binary.name)
	receiptPath := filepath.Join(binDir, receipt.name)
	binaryState, err := plannerLeafState(binaryPath)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", binaryPath, err)
	}
	receiptState, err := plannerLeafState(receiptPath)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", receiptPath, err)
	}
	if (binaryState == binary.old || binaryState == binary.new) &&
		(receiptState == receipt.old || receiptState == receipt.new || receiptState == "absent") {
		return nil
	}
	record := filepath.Join(txn, binary.name+".conflict")
	if err := os.WriteFile(record, nil, 0o600); err != nil {
		return fmt.Errorf("record foreign planner pair %s (automatic provision may overwrite it): %w", binaryPath, err)
	}
	return fmt.Errorf("[ECI_PLANNER_PUBLISH_UNSAFE] foreign planner pair retained at %s and %s; if Bash works, run bash \"$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\" --preserve-conflict %s; otherwise see the retained planner conflict gate catalog entry. Do not remove %s or overwrite unresolved leaves", binaryPath, receiptPath, binary.name, txn)
}

// restorePlannerLeaves attempts rollback and records observed foreign pairs.
//
// Example: a failed binary restore leaves its receipt absent for reprovisioning.
func restorePlannerLeaves(
	binDir string,
	txn string,
	tools []plannerTool,
) error {
	var failures []error
	for i := range tools {
		if err := restorePlannerPair(binDir, txn, &tools[i]); err != nil {
			failures = append(failures, err)
		}
		if err := recordPlannerConflict(binDir, txn, &tools[i]); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
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
