package plan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// maxHookTranscriptPrefixBytes bounds the transcript prefix read for role classification.
	//
	// Example: a session header inside the first 64 KiB and 16 lines classifies the transcript.
	maxHookTranscriptPrefixBytes = 64 << 10
)

// HookInput contains the callback fields needed to resolve command ownership.
//
// Example: Bash callbacks supply ToolInput.Command and the callback SessionID.
type HookInput struct {
	SessionID      string `json:"session_id"`
	CWD            string `json:"cwd"`
	CWDUnknown     bool   `json:"cwd_unknown"`
	TranscriptPath string `json:"transcript_path"`
	ToolName       string `json:"tool_name"`
	ToolInput      struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

// hookRequest resolves callback and transcript metadata into planner facts.
//
// Example: a child transcript selects the Worker role without activating legacy markers.
func hookRequest(input HookInput) (Request, error) {
	request := Request{Provider: ProviderOmp, Role: RoleCoordinator, CWD: input.CWD, Command: input.ToolInput.Command, Marker: MarkerInactive, ActiveSession: input.SessionID}
	request.HookSessionID = input.SessionID
	switch {
	case input.CWDUnknown:
		request.CWD = ""
	case request.CWD == "":
		cwd, err := os.Getwd()
		if err != nil {
			return request, err
		}
		request.CWD = cwd
	}
	request.CommandPath, request.CommandPathSet = os.LookupEnv("PATH")
	request.CDPath, request.CDPathSet = os.LookupEnv("CDPATH")
	role, transcriptErr := hookTranscriptRole(input.TranscriptPath)
	request.Role = role
	switch {
	case transcriptErr == nil:
	case errors.Is(transcriptErr, errHookTranscriptUnknown):
		hookAdvisory("classify callback transcript", transcriptErr)
	default:
		return request, transcriptErr
	}
	if os.Getenv("ECI_HOOK_IS_SUBAGENT") == "1" || os.Getenv("ECI_HOOK_IS_SUBAGENT") == "true" {
		request.Role = RoleWorker
	}
	return request, nil
}

// hookSessionID limits state paths to immediate session directory names.
//
// Example: child-123 is valid; ../child is not.
func hookSessionID(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range value {
		if c != '_' && c != '-' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// hookCanonicalPath resolves existing ancestors and tolerates absent targets.
//
// Example: a missing proof directory keeps its cleaned absolute path.
func hookCanonicalPath(path string) (string, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err == nil {
		return canonical, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return filepath.Clean(path), nil
	}
	return "", err
}

// hookProofRoot resolves the configured proof-root alias once per callback use.
//
// Example: an ancestor home symlink yields the physical proof directory.
func hookProofRoot() (string, error) {
	root := os.Getenv("ECI_PROOF_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, "tmp", "agent", "proof")
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("proof root must be absolute")
	}
	canonical, err := filepath.EvalSymlinks(root)
	if errors.Is(err, os.ErrNotExist) {
		return filepath.Clean(root), nil
	}
	return canonical, err
}

// errHookTranscriptUnknown marks a callback transcript that fails closed to the worker role.
var errHookTranscriptUnknown = errors.New("callback transcript is unknown")

// hookTranscriptStatus verifies a callback transcript and classifies its prefix.
//
// Example: a symlinked transcript is unknown rather than main.
func hookTranscriptStatus(path string) (string, error) {
	// fail marks an unverifiable transcript without inventing an owner.
	//
	// Example: a missing transcript is classified as unknown.
	fail := func(err error) (string, error) {
		return "", fmt.Errorf("%w: %w", errHookTranscriptUnknown, err)
	}
	if path == "" {
		return fail(errors.New("empty transcript path"))
	}
	if !filepath.IsAbs(path) {
		return fail(errors.New("transcript path is not absolute"))
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fail(err)
	}
	if !info.Mode().IsRegular() {
		return fail(errors.New("transcript is not a regular file"))
	}
	lexical, root, err := hookTranscriptRoots()
	if err != nil {
		return fail(err)
	}
	base, relative, err := hookTranscriptRelative(path, lexical, root)
	if err != nil {
		return fail(err)
	}
	if err := hookNoSymlinkBelow(base, relative); err != nil {
		return fail(err)
	}
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fail(err)
	}
	if physical != filepath.Join(root, relative) {
		return fail(errors.New("transcript does not resolve to its sessions location"))
	}
	info, err = os.Stat(physical)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return fail(errors.New("transcript is missing or empty"))
	}
	facts, err := hookTranscriptPrefix(physical)
	if err != nil {
		return fail(err)
	}
	depth := strings.Count(relative, string(filepath.Separator)) + 1
	switch {
	case depth == 2 && facts.parentPresent:
		return fail(errors.New("parentSession key at depth two"))
	case depth == 2 && facts.sessionInit:
		return fail(errors.New("session_init marker at depth two"))
	case depth == 2:
		return "main", nil
	case depth >= 3 && facts.parentString:
		return "subagent", nil
	case depth >= 3:
		return fail(errors.New("depth three or more without string parentSession"))
	default:
		return fail(fmt.Errorf("depth %d", depth))
	}
}

// maxHookTranscriptPrefixLines bounds the records classified from the transcript prefix.
const maxHookTranscriptPrefixLines = 16

// hookTitleSlotBytes is the size of omp's line-zero title slot, newline included.
// omp rewrites it in place, so a reader can observe it torn.
const hookTitleSlotBytes = 256

// hookTranscriptPrefix classifies the records in the first
// maxHookTranscriptPrefixBytes bytes and first maxHookTranscriptPrefixLines lines.
// A final segment without a newline (a torn append, or a line cut by the bound) is
// not a record. It fails when a record is not JSON or repeats a signal key, except a
// torn line-zero title slot, and when no session header is among the records. It
// returns the facts of the records.
//
// Example: a header on line seventeen lies past the prefix, so the transcript fails.
func hookTranscriptPrefix(path string) (hookTranscriptFacts, error) {
	file, err := os.Open(path)
	if err != nil {
		return hookTranscriptFacts{}, err
	}
	defer file.Close()
	buffer := make([]byte, maxHookTranscriptPrefixBytes)
	n, err := io.ReadFull(file, buffer)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return hookTranscriptFacts{}, err
	}
	prefix := buffer[:n]
	var facts hookTranscriptFacts
	for index := range maxHookTranscriptPrefixLines {
		end := bytes.IndexByte(prefix, '\n')
		if end < 0 {
			break
		}
		body := prefix[:end]
		prefix = prefix[end+1:]
		var value any
		if json.Unmarshal(body, &value) != nil {
			if index == 0 && end+1 == hookTitleSlotBytes {
				continue
			}
			return hookTranscriptFacts{}, errors.New("transcript line is not JSON")
		}
		if hookDuplicateSignalKey(body) {
			return hookTranscriptFacts{}, errors.New("transcript record repeats a signal key")
		}
		record := hookTranscriptSignal(value)
		facts.merge(record)
	}
	if !facts.header {
		return hookTranscriptFacts{}, errors.New("no session header in the bounded prefix")
	}
	return facts, nil
}

// hookDuplicateSignalKey reports whether a top-level JSON object repeats type,
// customType, or parentSession. encoding/json keeps the last duplicate, which
// would otherwise hide a signal from the classifier.
//
// Example: {"type":"session","parentSession":"p","type":"message"} repeats type.
func hookDuplicateSignalKey(body []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	seen := make(map[string]bool, 3)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		key, ok := token.(string)
		if !ok {
			return false
		}
		if key == "type" || key == "customType" || key == "parentSession" {
			if seen[key] {
				return true
			}
			seen[key] = true
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return false
		}
	}
	return false
}

// hookTranscriptFacts summarizes the classifier signals of one transcript.
type hookTranscriptFacts struct {
	header        bool // a record has type session
	parentString  bool // a session record declares a string parentSession
	parentPresent bool // a record carries a parentSession key of any JSON value
	sessionInit   bool // a record carries an omp session_init marker
}

// merge adds the signals of one record to the transcript facts.
func (facts *hookTranscriptFacts) merge(record hookTranscriptFacts) {
	facts.header = facts.header || record.header
	facts.parentString = facts.parentString || record.parentString
	facts.parentPresent = facts.parentPresent || record.parentPresent
	facts.sessionInit = facts.sessionInit || record.sessionInit
}

// hookTranscriptSignal reads the classifier signals of one record: a session
// header, a string parentSession on a session header, any parentSession key, and
// an omp session_init marker.
//
// Example: {"type":"session","parentSession":5} is a header whose parentSession is present but not a string.
func hookTranscriptSignal(value any) hookTranscriptFacts {
	object, ok := value.(map[string]any)
	if !ok {
		return hookTranscriptFacts{}
	}
	recordType, _ := object["type"].(string)
	customType, _ := object["customType"].(string)
	parent, hasParent := object["parentSession"]
	_, parentIsString := parent.(string)
	return hookTranscriptFacts{
		header:        recordType == "session",
		parentString:  recordType == "session" && parentIsString,
		parentPresent: hasParent,
		sessionInit:   recordType == "session_init" || customType == "session_init",
	}
}

// hookTranscriptRoots returns the omp sessions root under HOME as configured and as
// resolved through symlinks.
//
// Example: HOME=/home/u yields /home/u/.omp/agent/sessions and its resolved form.
func hookTranscriptRoots() (string, string, error) {
	home := os.Getenv("HOME")
	if home == "" {
		return "", "", errors.New("HOME is unset")
	}
	lexical := filepath.Join(home, ".omp", "agent", "sessions")
	resolved, err := filepath.EvalSymlinks(lexical)
	return lexical, resolved, err
}

// hookTranscriptRelative returns the base under which path lies and the path relative
// to that base. The base is the configured or the resolved sessions root. When path
// reaches the root through a symlink above it, such as a HOME alias, the base is the
// topmost ancestor of path that resolves to the resolved root. Every component of path
// must avoid symlinks into the root (hookNoSymlinkIntoRoot), except the base itself when
// it is the HOME-alias configured sessions link; symlinks below the base are rejected by
// hookNoSymlinkBelow.
//
// Example: <root>/project/a.jsonl and ALIAS/.omp/agent/sessions/project/a.jsonl, with
// ALIAS linked to HOME, both yield project/a.jsonl.
func hookTranscriptRelative(path, lexical, resolved string) (string, string, error) {
	for _, base := range []string{lexical, resolved} {
		relative, err := filepath.Rel(base, path)
		if err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return base, relative, nil
		}
	}
	var ancestors []string
	for dir := filepath.Dir(path); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		ancestors = append(ancestors, dir)
	}
	for i := len(ancestors) - 1; i >= 0; i-- {
		physical, err := filepath.EvalSymlinks(ancestors[i])
		if err != nil || physical != resolved {
			continue
		}
		relative, err := filepath.Rel(ancestors[i], path)
		if err != nil {
			continue
		}
		if err := hookNoSymlinkIntoRoot(path, ancestors[i], resolved); err != nil {
			return "", "", err
		}
		return ancestors[i], relative, nil
	}
	return "", "", errors.New("transcript is outside the sessions root")
}

// hookNoSymlinkBelow fails when any component of relative, below base, is a symlink
// or cannot be inspected.
//
// Example: sessions/S/P/C.jsonl fails when sessions/S/P is a symlink.
func hookNoSymlinkBelow(base, relative string) error {
	current := base
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("transcript path crosses a symlink")
		}
	}
	return nil
}

// hookNoSymlinkIntoRoot fails when any component of path, from the filesystem root
// down to path, is a symlink whose physical location is the resolved root or lies
// below it. Such a link reaches the sessions root from outside, so a match above it
// is not a plain alias. The one exception is the chosen base when it is the configured
// sessions link under a HOME alias: its lexical path ends in .omp/agent/sessions and the
// prefix before that suffix physically resolves to physical HOME. Its physical target is
// the resolved root by design, and hookNoSymlinkBelow still guards everything below it.
//
// Example: HOME/s -> root/project makes HOME/s/up/project/C.jsonl unknown, while
// ALIAS/.omp/agent/sessions -> root with base ALIAS/.omp/agent/sessions is accepted.
func hookNoSymlinkIntoRoot(path, base, resolved string) error {
	configured := string(filepath.Separator) + filepath.Join(".omp", "agent", "sessions")
	exempt := ""
	if alias := strings.TrimSuffix(base, configured); alias != base {
		physicalAlias, aliasErr := filepath.EvalSymlinks(alias)
		physicalHome, homeErr := filepath.EvalSymlinks(os.Getenv("HOME"))
		if aliasErr == nil && homeErr == nil && physicalAlias == physicalHome {
			exempt = base
		}
	}
	current := string(filepath.Separator)
	for _, part := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink == 0 || current == exempt {
			continue
		}
		physical, err := filepath.EvalSymlinks(current)
		if err != nil {
			return err
		}
		if physical == resolved || strings.HasPrefix(physical, resolved+string(filepath.Separator)) {
			return errors.New("transcript path crosses a symlink")
		}
	}
	return nil
}

// hookTranscriptRole maps a callback transcript to its planner role.
// Unknown transcripts remain Workers and carry the classification error.
//
// Example: an empty transcript path is unknown, so the callback runs as a worker.
func hookTranscriptRole(path string) (Role, error) {
	status, err := hookTranscriptStatus(path)
	if status == "main" {
		return RoleCoordinator, nil
	}
	return RoleWorker, err
}

// recordHookActivity preserves coordinator activity and callback-session writes.
//
// Example: a worker write records a touched repository under its child session.
func recordHookActivity(
	request Request,
	result Result,
) error {
	if request.HookSessionID != "" {
		request.ActiveSession = request.HookSessionID
	}
	if !hookSessionID(request.ActiveSession) {
		return nil
	}
	if result.ShellAnalysis == nil {
		return nil
	}
	mutable := result.ShellAnalysis.OutputWrites
	for _, command := range result.ShellAnalysis.Commands {
		if !hookReadonlyCommand(command) {
			mutable = true
		}
	}
	if !mutable {
		return nil
	}
	root, err := hookProofRoot()
	if err != nil {
		return err
	}
	if request.Role != RoleWorker {
		dir := filepath.Join(root, "activity", "sessions", request.ActiveSession)
		if err := hookStateDirectory(root, dir); err != nil {
			return err
		}
		marker := filepath.Join(dir, "shell")
		info, err := os.Lstat(marker)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && !info.Mode().IsRegular() {
			return fmt.Errorf("activity target is not regular")
		}
		if err := os.WriteFile(marker, []byte("kind: shell\ncwd: "+request.CWD+"\ncreated_utc: "+time.Now().UTC().Format(time.RFC3339)+"\n"), 0600); err != nil {
			return err
		}
	}
	if result.ShellAnalysis.OutputWrites {
		if err := hookTouchedRepository(request); err != nil {
			return err
		}
	}
	for _, command := range result.ShellAnalysis.Commands {
		argv := hookContextArgv(command)
		if len(argv) == 0 {
			continue
		}
		name := filepath.Base(argv[0])
		if hookReadonlyCommand(command) {
			continue
		}
		touched := request
		if command.CWDKnown {
			touched.CWD = command.CWD
		} else {
			touched.CWD = ""
		}
		if name == "git" {
			for index := 1; index+1 < len(argv); index++ {
				if argv[index] != "-C" {
					continue
				}
				directory := argv[index+1]
				if filepath.IsAbs(directory) {
					touched.CWD = directory
				} else {
					touched.CWD = filepath.Join(touched.CWD, directory)
				}
				index++
			}
		}
		if name == "eci-worker-git" {
			for index := 1; index+1 < len(argv); index++ {
				if argv[index] == "--repo" {
					directory := argv[index+1]
					switch {
					case filepath.IsAbs(directory):
						touched.CWD = directory
					case filepath.IsAbs(touched.CWD):
						touched.CWD = filepath.Join(touched.CWD, directory)
					default:
						touched.CWD = ""
					}
					break
				}
			}
		}
		if err := hookTouchedRepository(touched); err != nil {
			return err
		}
	}
	return nil
}

// hookReadonlyCommand identifies commands that need no mutation bookkeeping.
//
// Example: native git diff and cat do not trigger Git status scans.
func hookReadonlyCommand(command ShellCommandRecord) bool {
	command.Argv = hookContextArgv(command)
	if len(command.Argv) == 0 {
		return true
	}
	name := filepath.Base(command.Argv[0])
	switch name {
	case "cat", "rg", "grep", "ls", "pwd", "echo", "printf", "true", "false", "head", "tail", "wc", "stat", "readlink", "realpath":
		return true
	case "sed":
		for _, arg := range command.Argv[1:] {
			if arg == "--in-place" || strings.HasPrefix(arg, "--in-place=") || strings.HasPrefix(arg, "-i") {
				return false
			}
		}
		return true
	case "find":
		for _, arg := range command.Argv[1:] {
			if arg == "-delete" || arg == "-exec" || arg == "-execdir" {
				return false
			}
		}
		return true
	case "git":
		tokens := make([]token, 0, len(command.Argv))
		for _, argument := range command.Argv {
			tokens = append(tokens, token{value: argument})
		}
		index := gitSubcommandIndex(tokens)
		if index < 0 || index >= len(command.Argv) {
			return false
		}
		switch command.Argv[index] {
		case "diff", "show", "log", "status", "rev-parse", "ls-files", "ls-tree", "cat-file", "help":
			return true
		}
	case "eci-worker-git":
		for index := 1; index < len(command.Argv); index++ {
			if command.Argv[index] == "--repo" {
				index++
				continue
			}
			if strings.HasPrefix(command.Argv[index], "-") {
				continue
			}
			switch command.Argv[index] {
			case "inspect", "status", "show", "diff", "log":
				return true
			}
			return false
		}
	}
	return false
}

// hookContextArgv reuses effect analysis to select a command's literal child.
//
// Example: KEY=value cat file resolves to the cat argument vector.
func hookContextArgv(command ShellCommandRecord) []string {
	tokens := make([]token, 0, len(command.Argv))
	for index, argument := range command.Argv {
		tokens = append(tokens, token{value: argument, offset: index})
	}
	child := hookCommandChild(command, tokens)
	argv := make([]string, 0, len(child))
	for _, argument := range child {
		argv = append(argv, argument.value)
	}
	return argv
}

// hookStateDirectory creates session bookkeeping directories without traversing state symlinks.
//
// Example: activity/sessions/child is created beneath the canonical proof root.
func hookStateDirectory(
	root string,
	dir string,
) error {
	relative, err := filepath.Rel(root, dir)
	if err != nil {
		return err
	}
	current := root
	for _, component := range append([]string{""}, strings.Split(relative, string(filepath.Separator))...) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("state directory is not a regular directory")
		}
	}
	return nil
}

// hookTouchedRepository captures the first repository baseline for shell writes.
//
// Example: first mutation captures HEAD and status once; later writes retain that baseline.
func hookTouchedRepository(request Request) error {
	if !filepath.IsAbs(request.CWD) {
		return nil
	}
	output, err := hookGitOutput(request.CWD, "rev-parse", "--show-toplevel")
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return nil
	}
	if err != nil {
		return err
	}
	repository, err := hookCanonicalPath(strings.TrimSpace(string(output)))
	if err != nil {
		return err
	}
	if repository == "" {
		return nil
	}
	root, err := hookProofRoot()
	if err != nil {
		return err
	}
	dir := filepath.Join(root, "touched-repos", "sessions", request.ActiveSession)
	if err := hookStateDirectory(root, dir); err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(repository))
	path := filepath.Join(dir, hex.EncodeToString(sum[:]))
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("touched repository record is not regular")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	head, err := hookGitOutput(repository, "rev-parse", "--verify", "HEAD")
	if err != nil && !errors.As(err, &exitError) {
		return err
	}
	status, err := hookGitOutput(repository, "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return err
	}
	statusSum := sha256.Sum256([]byte(strings.TrimRight(string(status), "\n")))
	return os.WriteFile(path, []byte("repo: "+repository+"\nhead: "+strings.TrimSpace(string(head))+"\nstatus_sha: "+hex.EncodeToString(statusSum[:])+"\nrepo_wide: true\ncreated_utc: "+time.Now().UTC().Format(time.RFC3339)+"\n"), 0600)
}

// hookGitOutput queries the concrete repository without inherited Git selectors.
//
// Example: GIT_DIR from the callback cannot redirect its touched baseline.
func hookGitOutput(
	directory string,
	arguments ...string,
) ([]byte, error) {
	args := append([]string{"--no-optional-locks", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-C", directory}, arguments...)
	command := exec.Command("/usr/bin/git", args...)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GIT_") {
			continue
		}
		command.Env = append(command.Env, entry)
	}
	return command.Output()
}
