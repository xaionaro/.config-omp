package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const (
	singletonManifestName = "eci-required-critics.json"
	aggregatePlanName     = "eci-aggregate-plan.json"
)

// manifestWrite publishes a required-critic manifest into the current session
// through the native importer, which owns descriptor-safe no-replace publish.
func manifestWrite(args []string) error {
	if len(args) != 2 {
		return usageError()
	}
	_, sid := sessionIdentity()
	if !validSessionID(sid) {
		return exitError{code: 1, msg: "manifest-write requires an explicit current ECI_SESSION_ID or ECI_THREAD_ID."}
	}
	sessionDir, err := resolveSessionDir(sid)
	if err != nil {
		return exitError{code: 1, msg: "eci-active: no current session directory found under the proof root."}
	}
	if filepath.Base(sessionDir) != sid {
		return exitError{code: 1, msg: "manifest-write resolved a different session than the current identity."}
	}
	lock, err := acquireProofSessionLock(sid)
	if err != nil {
		return err
	}
	defer lock.Close()
	reportAggregatePlanResidue(sessionDir, "manifest-write")

	importer := filepath.Join(eciBaseDir(), "bin", "eci-safe-import")
	if !isRegularNoFollow(importer) {
		return exitError{code: 1, msg: "manifest-write requires the native eci-safe-import binary; run hooks/build-native-binaries.sh."}
	}
	cmd := exec.Command(importer, "--proof-root", proofRoot(), "--session-dir", sessionDir,
		"--leaf", "singleton-manifest", "--source", args[1])
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		code := 1
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() > 0 {
			code = ee.ExitCode()
		}
		return exitError{code: code, msg: "manifest-write source or fixed destination is unsafe."}
	}
	fmt.Printf("ECI required-critic manifest published: %s\n", filepath.Join(sessionDir, singletonManifestName))
	return nil
}

// reportAggregatePlanResidue is the advisory branch of the pre-migration source
// reject_normal_lifecycle_for_aggregate_session. A live aggregate context is
// not reconstructed here because aggregate lifecycle mutators are deferred in
// omp; a plan file is reported as diagnostic residue only.
func reportAggregatePlanResidue(sessionDir, lifecycle string) {
	plan := filepath.Join(sessionDir, aggregatePlanName)
	if _, err := os.Lstat(plan); err == nil {
		fmt.Fprintf(os.Stderr, "ECI advisory: local aggregate-plan residue is not a live target for %s and remains diagnostic: %s\n", lifecycle, plan)
	}
}
