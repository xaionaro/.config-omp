package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	overrideBaseName    = ".eci-accidental-mistake-override"
	overrideClaimSuffix = ".claim"
	overrideRecordName  = "record"
)

// requireMainCoordinatorOwner mirrors codex require_main_coordinator_owner.
// omp reads ECI_ROLE and ECI_HOOK_IS_SUBAGENT (1 or true, as in hook_context.go).
func requireMainCoordinatorOwner() error {
	switch os.Getenv("ECI_ROLE") {
	case "", "main", "orchestrator", "lead", "coordinator":
	default:
		return exitError{code: 1, msg: "Only the main/orchestrator may publish or clear ECI lifecycle state."}
	}
	switch os.Getenv("ECI_HOOK_IS_SUBAGENT") {
	case "1", "true":
		return exitError{code: 1, msg: "Only the main/coordinator may clean an accidental-mistake override claim."}
	}
	return nil
}

// accidentalOverrideCleanup removes an orphaned tombstone claim once the
// original override record is confirmed absent.
func accidentalOverrideCleanup(args []string) error {
	if len(args) != 3 || args[1] != "--authorized-by-user" {
		return usageError()
	}
	if err := requireMainCoordinatorOwner(); err != nil {
		return err
	}
	return cleanupAccidentalOverrideClaim(args[2])
}

// claimPathCanonical accepts only <proof-root>/<sid>/.eci-accidental-mistake-override.claim
// as a real directory with no symlinked components.
func claimPathCanonical(claim string) bool {
	if proofRootSafe() != nil {
		return false
	}
	root := proofRoot()
	if !filepath.IsAbs(claim) {
		return false
	}
	for _, r := range claim {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	prefix := root + string(filepath.Separator)
	if !strings.HasPrefix(claim, prefix) {
		return false
	}
	sid, tail, _ := strings.Cut(strings.TrimPrefix(claim, prefix), "/")
	if tail != overrideBaseName+overrideClaimSuffix || !validSessionID(sid) {
		return false
	}
	if filepath.Clean(claim) != claim {
		return false
	}
	if !sessionDirSafe(root, sid) {
		return false
	}
	if claim != filepath.Join(root, sid, overrideBaseName+overrideClaimSuffix) {
		return false
	}
	fi, err := os.Lstat(claim)
	if err != nil || !fi.IsDir() {
		return false
	}
	resolved, err := filepath.EvalSymlinks(claim)
	return err == nil && resolved == claim
}

// sessionDirSafe reports true when the session path is absent or a real directory.
func sessionDirSafe(root, sid string) bool {
	fi, err := os.Lstat(filepath.Join(root, sid))
	return err != nil || fi.IsDir()
}

// ownedByEUID reports whether fi is owned by the effective user.
func ownedByEUID(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Geteuid()
}

// stateFileOwnerValid mirrors codex codex_state_file_owner_is_valid with unique links.
func stateFileOwnerValid(path string) bool {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || !ownedByEUID(fi) {
		return false
	}
	st := fi.Sys().(*syscall.Stat_t)
	return uint64(st.Nlink) == 1
}

// validateClaimTombstone checks that claim is an orphaned, owner-valid tombstone.
func validateClaimTombstone(claim string) error {
	if !claimPathCanonical(claim) {
		return exitError{code: 1, msg: "accidental-override-cleanup requires an existing canonical proof-root claim directory: " + claim}
	}
	original := strings.TrimSuffix(claim, overrideClaimSuffix)
	if _, err := os.Lstat(original); err == nil {
		return exitError{code: 1, msg: "accidental-override-cleanup refused an active or ambiguous claim; the original override record still exists: " + original}
	}
	record := filepath.Join(claim, overrideRecordName)
	ri, err := os.Lstat(record)
	if err != nil || !ri.Mode().IsRegular() || ri.Size() == 0 {
		return exitError{code: 1, msg: "accidental-override-cleanup requires one nonempty regular tombstone record: " + record}
	}
	if !stateFileOwnerValid(record) {
		return exitError{code: 1, msg: "accidental-override-cleanup refused a tombstone with an invalid owner or link count: " + record}
	}
	ci, err := os.Lstat(claim)
	if err != nil || !ownedByEUID(ci) {
		return exitError{code: 1, msg: "accidental-override-cleanup refused a claim directory with an invalid owner: " + claim}
	}
	entries, err := os.ReadDir(claim)
	if err != nil {
		return exitError{code: 1, msg: "accidental-override-cleanup requires exactly one nonempty tombstone record: " + claim}
	}
	for _, e := range entries {
		if e.Name() != overrideRecordName {
			return exitError{code: 1, msg: "accidental-override-cleanup refused an ambiguous claim with extra entries: " + claim}
		}
	}
	if len(entries) != 1 {
		return exitError{code: 1, msg: "accidental-override-cleanup requires exactly one nonempty tombstone record: " + claim}
	}
	return nil
}

// cleanupAccidentalOverrideClaim revalidates under the mutation lock, then
// removes the tombstone and its claim directory.
func cleanupAccidentalOverrideClaim(claim string) error {
	if err := validateClaimTombstone(claim); err != nil {
		return err
	}
	lock, err := acquireLock()
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := validateClaimTombstone(claim); err != nil {
		return err
	}
	record := filepath.Join(claim, overrideRecordName)
	if err := os.Remove(record); err != nil {
		return exitError{code: 1, msg: "accidental-override-cleanup could not remove the tombstone: " + record}
	}
	if err := os.Remove(claim); err != nil {
		return exitError{code: 1, msg: "accidental-override-cleanup refused to remove a changed claim directory: " + claim}
	}
	_, errRecord := os.Lstat(record)
	_, errClaim := os.Lstat(claim)
	if errRecord == nil || errClaim == nil {
		return exitError{code: 1, msg: "accidental-override-cleanup could not verify claim removal: " + claim}
	}
	fmt.Printf("Removed orphaned accidental-mistake override claim: %s\n", claim)
	return nil
}
