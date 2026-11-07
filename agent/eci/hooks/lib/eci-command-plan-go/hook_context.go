package main

// TODO: Derive callback scope and parent ownership without reading the retired ECI marker.

import (
	"bufio"
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
	// maxHookRecordBytes bounds one marker or dependency allowance record.
	//
	// Example: records over 4 KiB are advisory metadata rather than authority.
	maxHookRecordBytes = 4096
	// maxHookObservedMarkers bounds advisory peer marker discovery.
	//
	// Example: the direct session is checked before observing up to 64 markers.
	maxHookObservedMarkers = 64
)

// HookInput contains the callback fields needed to resolve command ownership.
//
// Example: Bash callbacks supply ToolInput.Command and the callback SessionID.
type HookInput struct {
	SessionID      string `json:"session_id"`
	CWD            string `json:"cwd"`
	TranscriptPath string `json:"transcript_path"`
	ToolName       string `json:"tool_name"`
	ToolInput      struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

// hookRequest resolves bounded marker and transcript metadata into planner facts.
//
// Example: a spawned child uses its parent's active marker and approved roots.
func hookRequest(input HookInput) (Request, error) {
	request := Request{Provider: ProviderOmp, Role: RoleCoordinator, CWD: input.CWD, Command: input.ToolInput.Command, Marker: MarkerInactive, ActiveSession: input.SessionID}
	request.HookSessionID = input.SessionID
	if request.CWD == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return request, err
		}
		request.CWD = cwd
	}
	request.CommandPath, request.CommandPathSet = os.LookupEnv("PATH")
	request.CDPath, request.CDPathSet = os.LookupEnv("CDPATH")
	worker := hookTranscriptStatus(input.TranscriptPath) == "subagent"
	var advisoryErrors []error
	if worker || os.Getenv("ECI_HOOK_IS_SUBAGENT") == "1" || os.Getenv("ECI_HOOK_IS_SUBAGENT") == "true" {
		request.Role = RoleWorker
	}
	root, err := hookProofRoot()
	if err != nil {
		return request, err
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return request, errors.Join(advisoryErrors...)
	}
	if err != nil {
		return request, err
	}
	// Direct callback identity must survive the advisory peer cap.
	direct := make([]os.DirEntry, 0, 4)
	peers := make([]os.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if hookSessionAlias(entry.Name()) == hookSessionAlias(input.SessionID) {
			direct = append(direct, entry)
		} else {
			peers = append(peers, entry)
		}
	}
	entries = append(direct, peers...)
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() || !hookSessionID(entry.Name()) {
			continue
		}
		marker := filepath.Join(root, entry.Name(), "eci_active")
		data, err := hookSmallRecord(marker)
		if err != nil {
			advisoryErrors = append(advisoryErrors, fmt.Errorf("read active marker: %w", err))
			continue
		}
		if data == nil {
			continue
		}
		count++
		if count > maxHookObservedMarkers {
			break
		}
		lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		if len(lines) < 3 || len(lines) > 4 || !strings.HasPrefix(lines[0], "scope: ") || strings.TrimPrefix(lines[0], "scope: ") == "" || !strings.HasPrefix(lines[1], "cwd: ") || lines[2] != "session_id: "+entry.Name() {
			continue
		}
		if len(lines) == 4 && !strings.HasPrefix(lines[3], "created_utc: ") {
			continue
		}
		markerCWD := strings.TrimPrefix(lines[1], "cwd: ")
		if !filepath.IsAbs(markerCWD) {
			continue
		}
		request.ActiveMarkers = append(request.ActiveMarkers, marker)
		matches := hookSessionAlias(entry.Name()) == hookSessionAlias(input.SessionID)
		if !matches || request.Marker == MarkerActive {
			continue
		}
		request.Marker = MarkerActive
		request.ActiveSession = entry.Name()
		canonicalCWD, err := hookCanonicalPath(markerCWD)
		if err != nil {
			return request, err
		}
		request.ApprovedRoots = []string{canonicalCWD}
		repository, err := hookAdditionalRepository(root, entry.Name(), markerCWD)
		if err != nil {
			advisoryErrors = append(advisoryErrors, fmt.Errorf("read declared repository: %w", err))
			continue
		}
		if repository != "" {
			request.ApprovedRoots = append(request.ApprovedRoots, repository)
		}
	}
	return request, errors.Join(advisoryErrors...)
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

// hookSessionAlias preserves the historical bare and session_ discovery forms.
//
// Example: session_owner and owner select the same marker identity.
func hookSessionAlias(value string) string { return strings.TrimPrefix(value, "session_") }

// hookCanonicalPath resolves existing ancestors and tolerates absent targets.
//
// Example: an undeployed declared dependency keeps its absolute cleaned path.
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

// hookSmallRecord reads a bounded regular control record without following its final symlink.
//
// Example: an oversized marker returns no metadata instead of reading its tail.
func hookSmallRecord(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxHookRecordBytes {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxHookRecordBytes+1))
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	if len(data) > maxHookRecordBytes || len(data) == 0 || data[len(data)-1] != '\n' {
		return nil, nil
	}
	for _, c := range data {
		if c < 32 && c != '\n' || c == 127 {
			return nil, nil
		}
	}
	return data, nil
}

// hookAdditionalRepository decodes one owning session's declared dependency.
//
// Example: an active six-line record adds its canonical repository root.
func hookAdditionalRepository(
	root string,
	session string,
	cwd string,
) (string, error) {
	data, err := hookSmallRecord(filepath.Join(root, session, "eci-additional-repository"))
	if err != nil || data == nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 6 || lines[0] != "schema: eci-additional-repository/v1" || lines[1] != "session_id: "+session || lines[2] != "cwd: "+cwd || !strings.HasPrefix(lines[3], "repository: /") || !strings.HasPrefix(lines[4], "reason: ") || strings.TrimSpace(strings.TrimPrefix(lines[4], "reason: ")) == "" || lines[5] != "state: active" {
		return "", nil
	}
	return hookCanonicalPath(strings.TrimPrefix(lines[3], "repository: "))
}

// hookTranscriptStatus classifies the callback transcript as main, subagent,
// or unknown. Every failure is unknown; only subagent earns the worker role.
//
// Example: a second-line session header with a string parentSession is a subagent.
func hookTranscriptStatus(path string) string {
	home := os.Getenv("HOME")
	if path == "" || home == "" {
		return "unknown"
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "unknown"
	}
	root, err := filepath.EvalSymlinks(filepath.Join(home, ".omp", "agent", "sessions"))
	if err != nil {
		return "unknown"
	}
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "unknown"
	}
	relative, err := filepath.Rel(root, physical)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "unknown"
	}
	info, err = os.Stat(physical)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return "unknown"
	}
	signal, valid, err := hookTranscriptScan(physical)
	if err != nil || !valid {
		return "unknown"
	}
	if strings.Count(relative, string(filepath.Separator))+1 >= 3 {
		return "subagent"
	}
	if signal {
		return "subagent"
	}
	return "main"
}

// hookTranscriptScan reads every transcript line once. valid is false when any
// line is not JSON, which makes the whole transcript unknown.
//
// Example: an empty line is invalid, matching the shell fromjson check.
func hookTranscriptScan(path string) (bool, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, false, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	signal := false
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			var value any
			if json.Unmarshal(bytes.TrimSuffix(line, []byte("\n")), &value) != nil {
				return false, false, nil
			}
			if hookTranscriptSignal(value) {
				signal = true
			}
		}
		if errors.Is(readErr, io.EOF) {
			return signal, true, nil
		}
		if readErr != nil {
			return false, false, readErr
		}
	}
}

// hookTranscriptSignal reports an omp session_init marker or a session header
// whose parentSession is a string.
//
// Example: {"type":"session","parentSession":"x"} is a signal.
func hookTranscriptSignal(value any) bool {
	object, ok := value.(map[string]any)
	if !ok {
		return false
	}
	recordType, _ := object["type"].(string)
	customType, _ := object["customType"].(string)
	if recordType == "session_init" || customType == "session_init" {
		return true
	}
	_, hasParent := object["parentSession"].(string)
	return recordType == "session" && hasParent
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
					touched.CWD = argv[index+1]
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
	args := append([]string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-C", directory}, arguments...)
	command := exec.Command("/usr/bin/git", args...)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GIT_") {
			continue
		}
		command.Env = append(command.Env, entry)
	}
	return command.Output()
}
