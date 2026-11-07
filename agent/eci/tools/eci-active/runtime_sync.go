package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Runtime synchronization ports the local publication of codex's eci_runtime_sync_run
// (hooks/lib/eci-runtime-sync.sh at 575bb81). The canonical source is the running omp ECI home. Its managed
// surface is the hooks/ tree plus the bin/ entries codex publishes. Codex's peer-apply step accepts only the
// canonical Codex home as its source, so it is reported as an advisory rather than copied.
const (
	runtimeSyncProvider    = "eci"
	runtimeSyncHomeSuffix  = "/.omp/agent/eci"
	runtimeSyncReceiptName = ".eci-runtime-sync-manifest"
	runtimeSyncRootsEnv    = "ECI_RUNTIME_ROOTS"
)

// runtimeSyncRequired must exist in the canonical source before anything is published.
var runtimeSyncRequired = []string{"bin/eci-active", "hooks/validate-bash.sh"}

// runtimeSyncBins are the bin/ entries published when present (codex's list).
var runtimeSyncBins = []string{
	"bin/eci-active", "bin/eci-active-dispatch", "bin/eci-runtime-sync",
	"bin/eci-command-gate-mode", "bin/eci-worker-git",
}

// runtimeRow is one staged managed file.
type runtimeRow struct {
	rel  string
	sha  string
	mode os.FileMode
}

// runtimeSyncFail formats a refusal in codex's shape.
func runtimeSyncFail(code, format string, args ...any) error {
	return exitError{code: 1, msg: fmt.Sprintf("[%s] ECI runtime synchronization denied: %s", code, fmt.Sprintf(format, args...))}
}

// syncRuntime publishes the canonical ECI runtime to every resolved target home and refreshes the source receipt.
func syncRuntime(args []string) error {
	if len(args) != 1 {
		return usageError()
	}
	source, err := canonicalEciRoot()
	if err != nil {
		return err
	}
	for _, rel := range runtimeSyncRequired {
		if !isRegularNoFollow(filepath.Join(source, rel)) {
			return runtimeSyncFail("ECI_RUNTIME_SYNC_SOURCE_IDENTITY", "provider=%s source=%s lacks managed runtime file %s", runtimeSyncProvider, source, rel)
		}
	}
	tmpRoot, err := runtimeSyncTmpRoot()
	if err != nil {
		return err
	}
	stage, err := os.MkdirTemp(tmpRoot, "eci-runtime-sync.")
	if err != nil {
		return runtimeSyncFail("ECI_RUNTIME_SYNC_STAGE_CREATE", "provider=%s could not create staging directory under %s", runtimeSyncProvider, tmpRoot)
	}
	defer os.RemoveAll(stage)

	rows, manifest, err := stageRuntime(source, stage)
	if err != nil {
		return err
	}
	targets, err := discoverRuntimeTargets(source)
	if err != nil {
		return err
	}
	for _, target := range targets {
		if err := publishRuntimeTarget(source, target, stage, rows, manifest); err != nil {
			return err
		}
	}
	publishRuntimeSourceReceipt(source, manifest)
	fmt.Fprintf(os.Stdout, "ECI runtime synchronization complete: provider=%s source=%s targets=%d staging=cleaned\n",
		runtimeSyncProvider, source, len(targets))
	fmt.Fprintf(os.Stderr, "ECI runtime peer synchronization advisory: local runtime synchronization succeeded; the Kimi peer runtime was not synchronized.\n")
	fmt.Fprintf(os.Stderr, "[ECI_RUNTIME_SYNC_PROVIDER] peer synchronization skipped: no peer is configured; peer apply runs only from the canonical Codex home: %s\n", source)
	return nil
}

// runtimeSyncTmpRoot is the usable HOME/tmp directory that holds the staging directory.
func runtimeSyncTmpRoot() (string, error) {
	home := homeDir()
	if home == "" {
		return "", runtimeSyncFail("ECI_RUNTIME_SYNC_TMP_INVALID", "provider=%s requires HOME to locate a temporary staging directory", runtimeSyncProvider)
	}
	root := filepath.Join(home, "tmp")
	canon, err := filepath.EvalSymlinks(root)
	if err != nil || !isDir(canon) || canon == string(filepath.Separator) {
		return "", runtimeSyncFail("ECI_RUNTIME_SYNC_TMP_INVALID", "provider=%s requires a usable temporary staging directory at %s", runtimeSyncProvider, root)
	}
	return canon, nil
}

// runtimeSafeRel accepts only plain printable relative paths, as codex's manifest validation does.
func runtimeSafeRel(rel string) bool {
	return !strings.HasPrefix(rel, "/") && !strings.Contains(rel, "..") &&
		strings.IndexFunc(rel, func(r rune) bool { return r < 0x20 || r > 0x7e }) < 0
}

// runtimeManifestPaths returns the sorted managed relative paths under the source root.
func runtimeManifestPaths(source string) ([]string, error) {
	set := map[string]bool{}
	for _, rel := range runtimeSyncBins {
		if info, err := os.Stat(filepath.Join(source, rel)); err == nil && info.Mode().IsRegular() {
			set[rel] = true
		}
	}
	hooks := filepath.Join(source, "hooks")
	tests := filepath.Join(hooks, "tests")
	err := filepath.WalkDir(hooks, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path == tests || name == "__pycache__" || strings.HasPrefix(name, ".eci-go-build.") ||
				strings.HasPrefix(name, ".eci-command-plan.txn.") {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || strings.HasPrefix(name, ".eci-go-build.") ||
			name == ".eci-command-plan.lock" || name == ".eci-command-plan.publish.lock" ||
			strings.HasSuffix(name, ".pyc") || strings.HasSuffix(name, ".pyo") {
			return nil
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		set[filepath.ToSlash(rel)] = true
		return nil
	})
	if err != nil {
		return nil, runtimeSyncFail("ECI_RUNTIME_SYNC_SOURCE_PATH", "source=%s hooks tree could not be enumerated: %v", source, err)
	}
	out := make([]string, 0, len(set))
	for rel := range set {
		out = append(out, rel)
	}
	sort.Strings(out)
	return out, nil
}

// stageRuntime snapshots every managed file into stage and returns the rows plus the manifest bytes.
func stageRuntime(source, stage string) ([]runtimeRow, []byte, error) {
	rels, err := runtimeManifestPaths(source)
	if err != nil {
		return nil, nil, err
	}
	var rows []runtimeRow
	var manifest bytes.Buffer
	for _, rel := range rels {
		if !runtimeSafeRel(rel) {
			return nil, nil, runtimeSyncFail("ECI_RUNTIME_SYNC_SOURCE_PATH", "source=%s relative=%s is not a safe manifest path", source, rel)
		}
		src := filepath.Join(source, filepath.FromSlash(rel))
		info, err := os.Stat(src)
		if err != nil || !info.Mode().IsRegular() {
			fmt.Fprintf(os.Stderr, "ECI runtime maintenance advisory: source disappeared during collection: %s\n", src)
			continue
		}
		mode := info.Mode().Perm()
		dst := filepath.Join(stage, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, nil, runtimeSyncFail("ECI_RUNTIME_SYNC_STAGE_CREATE", "provider=%s could not stage %s", runtimeSyncProvider, rel)
		}
		in, err := os.Open(src)
		if err != nil {
			return nil, nil, runtimeSyncFail("ECI_RUNTIME_SYNC_STAGE_CREATE", "provider=%s could not read %s", runtimeSyncProvider, src)
		}
		err = writeFileAtomic(dst, in, mode)
		_ = in.Close()
		if err != nil {
			return nil, nil, runtimeSyncFail("ECI_RUNTIME_SYNC_STAGE_CREATE", "provider=%s could not stage %s", runtimeSyncProvider, rel)
		}
		sha, err := digestFile(dst)
		if err != nil {
			return nil, nil, runtimeSyncFail("ECI_RUNTIME_SYNC_HASH", "cannot hash staged file: %s", dst)
		}
		rows = append(rows, runtimeRow{rel: rel, sha: sha, mode: mode})
		fmt.Fprintf(&manifest, "%s\t%s\t%o\n", rel, sha, uint32(mode))
	}
	return rows, manifest.Bytes(), nil
}

// runtimeMountPoints lists mount points from mountinfo, skipping entries codex also skips.
func runtimeMountPoints() []string {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		mount := fields[4]
		if strings.ContainsAny(mount, `\ `) || strings.IndexFunc(mount, func(r rune) bool { return r < 0x20 || r > 0x7e }) >= 0 {
			continue
		}
		out = append(out, mount)
	}
	return out
}

// resolveRuntimeTarget canonicalizes a target home and requires the omp ECI home name.
func resolveRuntimeTarget(path string) (string, error) {
	canon, err := filepath.EvalSymlinks(path)
	if err != nil || !isDir(canon) {
		return "", runtimeSyncFail("ECI_RUNTIME_SYNC_TARGET_MISSING", "provider=%s target=%s is unavailable", runtimeSyncProvider, path)
	}
	if !strings.HasSuffix(canon, runtimeSyncHomeSuffix) {
		return "", runtimeSyncFail("ECI_RUNTIME_SYNC_TARGET_PROVIDER_MISMATCH", "provider=%s target=%s has the wrong provider directory name", runtimeSyncProvider, canon)
	}
	return canon, nil
}

// discoverRuntimeTargets returns the distinct target homes other than source. Explicit ECI_RUNTIME_ROOTS entries
// must validate; mountinfo aliases of the source are used only when they validate.
func discoverRuntimeTargets(source string) ([]string, error) {
	seen := map[string]bool{source: true}
	var out []string
	add := func(canon string) {
		if !seen[canon] {
			seen[canon] = true
			out = append(out, canon)
		}
	}
	for _, entry := range strings.Split(os.Getenv(runtimeSyncRootsEnv), ":") {
		if entry == "" {
			continue
		}
		canon, err := resolveRuntimeTarget(entry)
		if err != nil {
			return nil, err
		}
		add(canon)
	}
	rel := strings.TrimPrefix(source, string(filepath.Separator))
	for _, mount := range runtimeMountPoints() {
		if canon, err := resolveRuntimeTarget(filepath.Join("/", mount, rel)); err == nil {
			add(canon)
		}
	}
	return out, nil
}

// publishRuntimeTarget installs every staged file into target, verifies digest and mode, and writes the receipt.
func publishRuntimeTarget(source, target, stage string, rows []runtimeRow, manifest []byte) error {
	for _, r := range rows {
		dst := filepath.Join(target, filepath.FromSlash(r.rel))
		if err := ensureSafeDir(target, r.rel); err != nil {
			return err
		}
		if err := refuseUnsafeLeaf(dst); err != nil {
			return err
		}
		in, err := os.Open(filepath.Join(stage, filepath.FromSlash(r.rel)))
		if err != nil {
			return runtimeSyncFail("ECI_RUNTIME_SYNC_STAGE_TARGET", "provider=%s target=%s could not be staged", runtimeSyncProvider, dst)
		}
		err = writeFileAtomic(dst, in, r.mode)
		_ = in.Close()
		if err != nil {
			return runtimeSyncFail("ECI_RUNTIME_SYNC_PUBLISH", "provider=%s source=%s target=%s atomic publish failed", runtimeSyncProvider, source, dst)
		}
		sha, err := digestFile(dst)
		info, statErr := os.Stat(dst)
		if err != nil || statErr != nil || sha != r.sha || info.Mode().Perm() != r.mode {
			return runtimeSyncFail("ECI_RUNTIME_SYNC_VERIFY", "provider=%s source=%s target=%s digest_or_mode_mismatch", runtimeSyncProvider, source, dst)
		}
	}
	// The receipt is descriptive; a failure to write it is advisory, as in codex.
	receipt := filepath.Join(target, runtimeSyncReceiptName)
	if info, err := os.Lstat(receipt); err == nil && info.IsDir() {
		fmt.Fprintf(os.Stderr, "ECI runtime maintenance advisory: target receipt is not a replaceable file: %s\n", receipt)
	} else if err := writeFileAtomic(receipt, bytes.NewReader(manifest), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "ECI runtime maintenance advisory: could not publish target receipt: %s\n", receipt)
	}
	relation := "separate-root"
	if si, err := os.Stat(source); err == nil {
		if ti, err := os.Stat(target); err == nil && os.SameFile(si, ti) {
			relation = "same-root"
		}
	}
	fmt.Fprintf(os.Stdout, "ECI runtime synchronized: provider=%s source=%s target=%s files=%d receipt=%s inode_relation=%s\n",
		runtimeSyncProvider, source, target, len(rows), receipt, relation)
	return nil
}

// publishRuntimeSourceReceipt refreshes the canonical source receipt. Failures are advisory, as in codex.
func publishRuntimeSourceReceipt(source string, manifest []byte) {
	receipt := filepath.Join(source, runtimeSyncReceiptName)
	if info, err := os.Lstat(receipt); err == nil && info.IsDir() {
		fmt.Fprintf(os.Stderr, "ECI runtime maintenance advisory: source receipt is not a replaceable file: %s\n", receipt)
		return
	}
	if err := writeFileAtomic(receipt, bytes.NewReader(manifest), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "ECI runtime maintenance advisory: could not publish source receipt: %s\n", receipt)
		return
	}
	fmt.Fprintf(os.Stdout, "ECI runtime source receipt refreshed: provider=%s source=%s receipt=%s\n", runtimeSyncProvider, source, receipt)
}
