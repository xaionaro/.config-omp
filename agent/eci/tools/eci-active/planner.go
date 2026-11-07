package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

const (
	plannerPublishLock = ".planner-importer.publish.lock"
	plannerTxnPrefix   = ".planner-importer.txn."
)

// plannerPairTools are the native gate binaries built and published as one pair. Each tool is published beside
// its <name>.sha256 receipt in the canonical bin/, the files hooks/lib/eci-native-binary.sh verifies before it
// runs a tool.
var plannerPairTools = []string{"eci-command-plan", "eci-safe-import", "eci-hook-helper"}

// plannerScrubbedEnv are variables that must not steer the planner build.
var plannerScrubbedEnv = map[string]bool{
	"GOCACHEPROG": true, "GOARM": true, "GOARM64": true, "GOAMD64": true,
	"GOOS": true, "GOARCH": true, "GOFLAGS": true, "GOWORK": true,
	"GOTOOLCHAIN": true, "GOROOT": true, "GOTMPDIR": true, "TMPDIR": true,
	"CGO_ENABLED": true, "GOPROXY": true, "GOMODCACHE": true,
}

// plannerEvent is a no-op rendezvous before and after each publication step. Tests replace it to inject a
// failure at an exact step, as planner_publication_event does in pre-migration source 575bb81
// bin/eci-runtime-sync.
var plannerEvent = func(step, path string) error { return nil }

// maintainPlanner rebuilds the planner pair from source with the go command on PATH and publishes it, with its
// receipts, into the canonical bin/ as one transaction. It then refreshes the module-directory planner that the
// eci-command-plan tests execute, and runs bin/eci-runtime-sync planner-apply for the optional Kimi peer. Peer
// failure is advisory.
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
	built, err := plannerApply(root, goTool)
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

// plannerGoTool returns the go command on PATH.
func plannerGoTool() (string, error) {
	path, err := exec.LookPath("go")
	if err != nil {
		return "", exitError{code: 1, msg: "[ECI_PLANNER_TOOLCHAIN_MISSING] go not found on PATH; install Go and retry; no planner state was changed."}
	}
	return path, nil
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

// plannerEnv is the caller's environment without plannerScrubbedEnv, plus the fixed build settings. GOROOT is not
// pinned: the builder is the go command found on PATH, and the toolchain it runs must supply its own GOROOT.
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
		"GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH,
		"GOMODCACHE="+plannerModCache(), "GOPROXY=off",
		"GOTMPDIR="+tmp, "TMPDIR="+tmp,
	)
}

// plannerBuildTool builds one planner module into the transaction and checks that the output is an ELF executable.
func plannerBuildTool(root, goTool, txn, name string) (string, error) {
	module := filepath.Join(root, "hooks", "lib", name+"-go")
	out := filepath.Join(txn, "built", name)
	cmd := exec.Command(goTool, "build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-pgo=off", "-ldflags=-buildid=", "-o", out, ".")
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
// receipts there. It returns the published planner. It holds the publication lock shared with bin/eci-runtime-sync.
func plannerApply(root, goTool string) (string, error) {
	binDir := filepath.Join(root, "bin")
	if info, err := os.Lstat(binDir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("[ECI_PLANNER_PUBLISH_UNSAFE] bin directory is missing or not a plain directory: %s; no planner state was changed", binDir)
	}
	lock, err := lockFileExclusive(filepath.Join(binDir, plannerPublishLock))
	if err != nil {
		return "", err
	}
	defer lock.Close()
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

	leaves := make([]*plannerLeaf, 0, 2*len(plannerPairTools))
	for _, name := range plannerPairTools {
		built, err := plannerBuildTool(root, goTool, txn, name)
		if err != nil {
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
	retained, err = publishPlannerLeaves(binDir, txn, leaves)
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
func publishPlannerLeaves(binDir, txn string, leaves []*plannerLeaf) (retained bool, err error) {
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

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	failure := renamePlannerLeaves(binDir, leaves)
	if failure == nil {
		select {
		case s := <-signals:
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
