package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// aggregate-review ports the pre-migration source's bin/eci-active aggregate-review at 575bb81. Plan validation and selection port
// pre-migration eci_aggregate_plan_is_valid, _plan_select, _live_repo_metadata, and _selected_child_is_genuinely_independent
// from the pre-migration proof-state shell library (575bb81 hooks/lib); the review gate ports the aggregate branch of hooks/eci-review-gate.sh.
// The command is read-only: it holds the ECI mutation lock while it validates and publishes no state.

const aggregateManifestFmt = "eci-aggregate.%s.required-critics.json"

var (
	aggregateRepoIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)
	// errGateDenied means the gate already printed its denial diagnostic.
	errGateDenied = errors.New("review gate denied")
)

// aggregateRepo is one plan member.
type aggregateRepo struct {
	id   string
	root string
}

// readJSONValue parses a regular file as one JSON document.
func readJSONValue(path string) (any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// jqTruthy matches jq's `-e` exit rule: null and false are not success.
func jqTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	}
	return true
}

// aggregateRepos checks the plan schema that eci_aggregate_plan_is_valid enforces with jq.
func aggregateRepos(doc map[string]any) ([]aggregateRepo, error) {
	list, ok := doc["repositories"].([]any)
	if !ok || len(list) == 0 {
		return nil, errors.New("plan repositories must be a non-empty array")
	}
	seen := make(map[string]bool, len(list))
	repos := make([]aggregateRepo, 0, len(list))
	for _, entry := range list {
		obj, ok := entry.(map[string]any)
		if !ok {
			return nil, errors.New("plan repository is not an object")
		}
		id, ok := obj["id"].(string)
		if !ok || !aggregateRepoIDRe.MatchString(id) {
			return nil, errors.New("plan repository id is invalid")
		}
		root, ok := obj["repo_root"].(string)
		if !ok || root == "" {
			return nil, errors.New("plan repository root is invalid")
		}
		if seen[id] {
			return nil, errors.New("plan repository ids are not unique")
		}
		seen[id] = true
		repos = append(repos, aggregateRepo{id: id, root: root})
	}
	return repos, nil
}

// aggregateStrictDescendant reports whether root is a strict child path below outer.
func aggregateStrictDescendant(outer, root string) bool {
	return outer != "" && root != "" && strings.HasPrefix(root, outer+"/")
}

// resolveUnder resolves a Git-reported path relative to base when it is not absolute.
func resolveUnder(base, raw string) string {
	if filepath.IsAbs(raw) {
		return raw
	}
	return filepath.Join(base, raw)
}

// aggregateLiveRepo re-derives the live Git topology of one selected repository and returns its common Git dir.
func aggregateLiveRepo(outer, root string) (string, error) {
	bad := errors.New("selected aggregate repository is not a canonical strict child")
	if !filepath.IsAbs(root) || strings.IndexFunc(root, func(r rune) bool { return r < 0x20 || r > 0x7e }) >= 0 ||
		strings.Contains(root, "//") || strings.Contains(root, "/./") || strings.Contains(root, "/../") ||
		strings.HasSuffix(root, "/..") || strings.HasSuffix(root, "/.") {
		return "", bad
	}
	if resolved, err := filepath.EvalSymlinks(root); err != nil || resolved != root {
		return "", bad
	}
	if fi, err := os.Lstat(root); err != nil || !fi.IsDir() {
		return "", bad
	}
	if !aggregateStrictDescendant(outer, root) {
		return "", bad
	}
	top, err := gitSafe("-C", root, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", bad
	}
	if resolved, err := filepath.EvalSymlinks(top); err != nil || resolved != root {
		return "", bad
	}
	gitDirRaw, _ := gitSafe("-C", root, "rev-parse", "--git-dir")
	gitCommonRaw, _ := gitSafe("-C", root, "rev-parse", "--git-common-dir")
	if _, err := resolvedDir(resolveUnder(root, gitDirRaw)); err != nil {
		return "", err
	}
	gitCommonDir, err := resolvedDir(resolveUnder(root, gitCommonRaw))
	if err != nil {
		return "", err
	}
	return gitCommonDir, nil
}

// resolvedDir returns the realpath of a directory, refusing a missing or non-directory target.
func resolvedDir(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !isDir(resolved) {
		return "", fmt.Errorf("git directory is unavailable: %s", path)
	}
	return resolved, nil
}

// aggregateSelectedIndependent ports eci_aggregate_selected_child_is_genuinely_independent. An independently
// initialized repository is ordinary work; a child that shares the parent's common Git dir is the same target.
func aggregateSelectedIndependent(outer, root string) error {
	childGitCommonDir, err := aggregateLiveRepo(outer, root)
	if err != nil {
		return err
	}
	inside, _ := gitSafe("-C", outer, "rev-parse", "--is-inside-work-tree")
	if inside != "true" {
		return nil
	}
	raw, _ := gitSafe("-C", outer, "rev-parse", "--git-common-dir")
	if raw == "" {
		return errors.New("outer common git dir is unavailable")
	}
	parentGitCommonDir, err := resolvedDir(resolveUnder(outer, raw))
	if err != nil {
		return err
	}
	if childGitCommonDir == parentGitCommonDir {
		return errors.New("selected aggregate repository shares the parent git common dir")
	}
	return nil
}

// aggregatePlanOuter ports eci_aggregate_plan_is_valid without a marker. It returns the validated outer cwd.
// When expectedOuter is empty the outer cwd is read from the plan.
func aggregatePlanOuter(plan, sid, expectedOuter string) (string, []aggregateRepo, error) {
	invalid := errors.New("aggregate plan is not valid")
	if !validSessionID(sid) {
		return "", nil, invalid
	}
	if err := proofRootSafe(); err != nil {
		return "", nil, err
	}
	root := proofRoot()
	sessionDir := filepath.Join(root, sid)
	if !sessionDirSafe(root, sid) || plan != filepath.Join(sessionDir, aggregatePlanName) {
		return "", nil, invalid
	}
	if !isRegularNoFollow(plan) {
		return "", nil, invalid
	}
	if resolved, err := filepath.EvalSymlinks(plan); err != nil || resolved != plan {
		return "", nil, invalid
	}
	doc, err := readJSONValue(plan)
	if err != nil {
		return "", nil, invalid
	}
	obj, ok := doc.(map[string]any)
	if !ok {
		return "", nil, invalid
	}
	repos, err := aggregateRepos(obj)
	if err != nil {
		return "", nil, err
	}

	outer := expectedOuter
	if outer != "" {
		if outer != canonicalCwd(outer) {
			return "", nil, invalid
		}
	} else {
		outer, _ = obj["outer_cwd"].(string)
		if !filepath.IsAbs(outer) || outer != canonicalCwd(outer) {
			return "", nil, invalid
		}
	}
	if !isDir(outer) || isSymlink(outer) {
		return "", nil, invalid
	}

	var prior []string
	for _, repo := range repos {
		if err := aggregateSelectedIndependent(outer, repo.root); err != nil {
			return "", nil, err
		}
		for _, p := range prior {
			if repo.root == p || strings.HasPrefix(repo.root, p+"/") || strings.HasPrefix(p, repo.root+"/") {
				return "", nil, errors.New("aggregate plan members are nested")
			}
		}
		prior = append(prior, repo.root)
	}
	return outer, repos, nil
}

// aggregateCurrentContext ports aggregate_current_context: the plan's own outer cwd must validate.
func aggregateCurrentContext(sid, sessionDir string) (string, string, error) {
	plan := filepath.Join(sessionDir, aggregatePlanName)
	doc, err := readJSONValue(plan)
	if err != nil {
		return "", "", err
	}
	obj, ok := doc.(map[string]any)
	if !ok {
		return "", "", errors.New("aggregate plan is not an object")
	}
	declared, _ := obj["outer_cwd"].(string)
	outer, _, err := aggregatePlanOuter(plan, sid, declared)
	if err != nil {
		return "", "", err
	}
	return plan, outer, nil
}

// aggregatePlanSelect ports the pre-migration eci_aggregate_plan_select for an explicit repository ID.
func aggregatePlanSelect(plan, sid, outer, repoID string) (string, error) {
	if !aggregateRepoIDRe.MatchString(repoID) {
		return "", errors.New("repository id is invalid")
	}
	validOuter, repos, err := aggregatePlanOuter(plan, sid, outer)
	if err != nil {
		return "", err
	}
	for _, repo := range repos {
		if repo.id != repoID {
			continue
		}
		if err := aggregateSelectedIndependent(validOuter, repo.root); err != nil {
			return "", err
		}
		return repo.root, nil
	}
	return "", errors.New("repository id is not in the plan")
}

// aggregateCallerSelected ports aggregate_caller_is_selected_target: the caller must sit at the parent cwd or
// inside the selected member.
func aggregateCallerSelected(plan, sid, outer, repoID string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	caller := canonicalCwd(cwd)
	if !isDir(caller) || isSymlink(caller) {
		return errors.New("caller cwd is unsafe")
	}
	if caller == outer {
		return nil
	}
	root, err := aggregatePlanSelect(plan, sid, outer, repoID)
	if err != nil {
		return err
	}
	if caller == root || strings.HasPrefix(caller, root+"/") {
		return nil
	}
	return errors.New("caller is outside the selected member")
}

// aggregateReview is the aggregate-review command: one phase check for one selected aggregate repository.
func aggregateReview(args []string) error {
	if len(args) != 3 {
		return usageError()
	}
	phase, repoID := args[1], args[2]
	if phase != "prewrite" && phase != "final" {
		return exitError{code: 1, msg: "aggregate-review supports only prewrite or final; commit and off remain aggregate-owned composite operations."}
	}
	if !aggregateRepoIDRe.MatchString(repoID) {
		return exitError{code: 1, msg: "aggregate-review requires a bounded aggregate repository ID."}
	}
	_, sid := sessionIdentity()
	if !validSessionID(sid) {
		return exitError{code: 1, msg: "aggregate-review requires an explicit current ECI_SESSION_ID or ECI_THREAD_ID."}
	}
	sessionDir, err := resolveSessionDir(sid)
	if err != nil {
		return exitError{code: 1, msg: "eci-active: no current session directory found under the proof root."}
	}
	if filepath.Base(sessionDir) != sid {
		return exitError{code: 1, msg: "aggregate-review resolved a different session than the current identity."}
	}
	// Read-only gate: it writes no state, so it takes no lock (as the shell gate).

	plan, outer, err := aggregateCurrentContext(sid, sessionDir)
	if err != nil {
		return exitError{code: 1, msg: "aggregate-review requires a validated active aggregate session and selected target."}
	}
	if err := aggregateCallerSelected(plan, sid, outer, repoID); err != nil {
		return exitError{code: 1, msg: "aggregate-review rejected a caller outside the selected aggregate target."}
	}
	if _, err := aggregatePlanSelect(plan, sid, outer, repoID); err != nil {
		return exitError{code: 1, msg: "aggregate-review rejected an unknown or invalid aggregate repository ID."}
	}
	if err := aggregateReviewGate(phase, sid, repoID, outer); err != nil {
		return exitError{code: 1, msg: fmt.Sprintf("ECI aggregate required-critic review gate rejected phase=%s repo=%s.", phase, repoID)}
	}
	return nil
}

// reviewGate carries the gate context used for refresh and denial output.
type reviewGate struct {
	phase string
	sid   string
	root  string
	cwd   string
}

// refresh prints the non-blocking refresh reminder that the pre-migration source's gate emits for missing or stale review metadata.
func (g reviewGate) refresh(reason, target string) {
	fmt.Fprintf(os.Stdout, "ECI review refresh needed: phase=%s target=%s reason=%s; ordinary work continues; workflow must route one fresh named least-restriction critic for the current diff.\n",
		g.phase, target, reason)
}

// deny prints the structured denial and returns the sentinel that stops the gate with exit 1.
func (g reviewGate) deny(detail string) error {
	root, sid := g.root, g.sid
	if root == "" {
		root = "<unresolved>"
	}
	if sid == "" {
		sid = "<missing>"
	}
	subject := fmt.Sprintf("session=%s,cwd=%s,manifest=%s", sid, diagValue(g.cwd),
		diagValue(filepath.Join(root, sid, "eci-required-critics.json")))
	remediation := "correct the reported gate input or evidence for the identified session/cwd/manifest, then retry the " +
		g.phase + " acceptance operation"
	fmt.Fprintln(os.Stderr, diagReason(diagCodeForReason(detail), g.phase, g.phase, subject, detail, remediation))
	return errGateDenied
}

// metadataOrRefresh ports review_metadata_file_or_refresh. It reports true only for a regular local file.
func (g reviewGate) metadataOrRefresh(path, missingReason, shapeReason, target string) bool {
	fi, err := os.Lstat(path)
	switch {
	case err == nil && fi.Mode()&os.ModeSymlink != 0:
		g.refresh(shapeReason, target)
		return false
	case err != nil:
		g.refresh(missingReason, target)
		return false
	case !fi.Mode().IsRegular():
		g.refresh(shapeReason, target)
		return false
	}
	return true
}

// gitPlainZero runs plain git (as the gate does) and reports whether it exited zero.
func gitPlainZero(args ...string) bool {
	return exec.Command("git", args...).Run() == nil
}

// gitPlainOutput runs plain git and returns its trimmed stdout, or "" on failure.
func gitPlainOutput(args ...string) string {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(string(out), "\n")
}

// aggregateReviewGate ports the aggregate branch of hooks/eci-review-gate.sh. It returns errGateDenied after
// printing a denial, or nil for pass and refresh outcomes (refresh is advisory, as in the pre-migration source).
func aggregateReviewGate(phase, sid, repoID, outer string) error {
	if outer != canonicalCwd(outer) {
		return errGateDenied
	}
	g := reviewGate{phase: phase, sid: sid, cwd: outer}
	if !validSessionID(sid) {
		return g.deny("ECI required-critic review gate needs a valid session id.")
	}
	g.root = proofRoot()
	if err := proofRootSafe(); err != nil {
		return g.deny(fmt.Sprintf("ECI required-critic review gate rejected an unsafe proof root: %s", g.root))
	}
	sessionDir := filepath.Join(g.root, sid)
	if !sessionDirSafe(g.root, sid) {
		return g.deny(fmt.Sprintf("ECI required-critic review gate rejected an unsafe session directory: %s", sessionDir))
	}
	if fi, err := os.Lstat(sessionDir); err != nil || !fi.IsDir() {
		return g.deny(fmt.Sprintf("ECI required-critic review gate needs the canonical session directory: %s", sessionDir))
	}
	if !aggregateRepoIDRe.MatchString(repoID) {
		return g.deny("ECI aggregate review gate requires a bounded repository ID.")
	}
	plan := filepath.Join(sessionDir, aggregatePlanName)
	manifest := filepath.Join(sessionDir, fmt.Sprintf(aggregateManifestFmt, repoID))
	return g.allowAggregate(outer, repoID, plan, manifest)
}

// allowAggregate ports allow_aggregate_current_target_or_refresh.
func (g reviewGate) allowAggregate(parentRaw, repoID, plan, manifest string) error {
	parent, ok := resolvedExistingDir(parentRaw)
	if !ok {
		return g.deny("ECI aggregate review gate rejected an unsafe current aggregate parent cwd.")
	}
	if !g.metadataOrRefresh(plan, "no current aggregate repository metadata exists",
		"historical aggregate repository metadata is not a regular local file", parent) {
		return nil
	}
	doc, err := readJSONValue(plan)
	if err != nil || !jqTruthy(doc) {
		g.refresh("historical aggregate repository metadata is not parseable", parent)
		return nil
	}
	repoRoot := ""
	for _, entry := range jqIterate(doc, "repositories") {
		obj, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := obj["id"].(string); !ok || id != repoID {
			continue
		}
		if root, ok := obj["repo_root"].(string); ok {
			repoRoot = root
			break
		}
	}
	if repoRoot == "" {
		g.refresh("aggregate metadata has no current selected repository", parent)
		return nil
	}
	repoRoot, ok = resolvedExistingDir(repoRoot)
	if !ok {
		g.refresh("the selected aggregate repository no longer exists", parent)
		return nil
	}
	top := gitPlainOutput("-C", repoRoot, "rev-parse", "--show-toplevel")
	if top == "" {
		g.refresh("the selected aggregate repository is no longer a current Git worktree", repoRoot)
		return nil
	}
	actual, ok := resolvedExistingDir(top)
	if !ok {
		return g.deny("ECI aggregate review gate rejected an unsafe current aggregate repository target.")
	}
	if !strings.HasPrefix(actual, parent+"/") {
		return g.deny("ECI aggregate review gate rejected a current repository outside the current aggregate parent cwd.")
	}
	return g.allowCurrent(parent, manifest, actual)
}

// allowCurrent ports allow_current_target_or_refresh for a repository hint, the only form the aggregate gate uses.
func (g reviewGate) allowCurrent(reviewCwdRaw, manifest, repoRootHint string) error {
	if _, ok := resolvedExistingDir(reviewCwdRaw); !ok {
		return g.deny("ECI required-critic review gate rejected an unsafe review cwd.")
	}
	repoRoot, ok := resolvedExistingDir(repoRootHint)
	if !ok {
		return g.deny("ECI required-critic review gate rejected an unsafe current repository target.")
	}
	top := gitPlainOutput("-C", repoRoot, "rev-parse", "--show-toplevel")
	if top == "" {
		g.refresh("no current repository is available for review", repoRoot)
		return nil
	}
	repoRoot, ok = resolvedExistingDir(top)
	if !ok {
		return g.deny("ECI required-critic review gate rejected an unsafe current repository target.")
	}
	if !g.metadataOrRefresh(manifest, "no current review metadata exists",
		"historical review metadata is not a regular local file", manifest) {
		return nil
	}
	doc, err := readJSONValue(manifest)
	if err != nil || !jqTruthy(doc) {
		g.refresh("historical review metadata is not parseable", "<unresolved>")
		return nil
	}
	target := ""
	if obj, ok := doc.(map[string]any); ok {
		target, _ = obj["current_target_path"].(string)
	}
	if target == "" {
		g.refresh("review metadata has no current target", "<unresolved>")
		return nil
	}
	candidate := target
	if !filepath.IsAbs(target) {
		candidate = filepath.Join(repoRoot, target)
	}
	targetPath, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		targetPath = filepath.Clean(candidate)
		g.refresh("the current target no longer exists", targetPath)
		return nil
	}
	if !strings.HasPrefix(targetPath, repoRoot+"/") {
		return g.deny(fmt.Sprintf("ECI required-critic review gate rejected a current target outside the current repository: %s", target))
	}
	if fi, err := os.Stat(targetPath); err != nil || !fi.Mode().IsRegular() {
		g.refresh("the current target is not a regular file", targetPath)
		return nil
	}
	relative := strings.TrimPrefix(targetPath, repoRoot+"/")
	if gitPlainZero("-C", repoRoot, "diff", "--quiet", "--", relative) &&
		gitPlainZero("-C", repoRoot, "diff", "--cached", "--quiet", "--", relative) {
		g.refresh("the target is no longer part of the current diff", targetPath)
		return nil
	}
	fmt.Fprintf(os.Stdout, "ECI review gate passed: phase=%s target=%s; ordinary work continues; workflow must route one fresh named least-restriction critic for the current diff.\n",
		g.phase, targetPath)
	return nil
}

// resolvedExistingDir returns the realpath of an existing directory.
func resolvedExistingDir(path string) (string, bool) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !isDir(resolved) {
		return "", false
	}
	return resolved, true
}

// jqIterate mirrors `.key?[]?`: array elements, or object values, or nothing.
func jqIterate(doc any, key string) []any {
	obj, ok := doc.(map[string]any)
	if !ok {
		return nil
	}
	switch v := obj[key].(type) {
	case []any:
		return v
	case map[string]any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, item)
		}
		return out
	}
	return nil
}
