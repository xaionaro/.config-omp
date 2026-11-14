package plan

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
)

// hookCodeWorkerSessionsRootDenied identifies Worker writes to omp session transcripts.
//
// Example: echo x > $HOME/.omp/agent/sessions/slug/new.jsonl selects this gate.
const hookCodeWorkerSessionsRootDenied DiagnosticCode = "ECI_WORKER_SESSIONS_ROOT_DENIED"

// hookAcknowledgment is a prefix assignment that admits one intended effect a gate would otherwise deny.
//
// Example: ECI_WHOLE_TREE_RESET=1 before git reset --hard admits one whole-tree reset.
type hookAcknowledgment string

const (
	// hookWholeResetAcknowledgment admits one intended whole-tree reset.
	//
	// Example: ECI_WHOLE_TREE_RESET=1 git reset --hard HEAD~1.
	hookWholeResetAcknowledgment hookAcknowledgment = "ECI_WHOLE_TREE_RESET=1"

	// hookLiveRemovalAcknowledgment admits one intended removal or relocation of a live Bash-gate file.
	//
	// Example: ECI_LIVE_HOOK_REMOVAL=1 git rm hooks/validate-bash.sh.
	hookLiveRemovalAcknowledgment hookAcknowledgment = "ECI_LIVE_HOOK_REMOVAL=1"
)

// hookGitResetMode is the effect class of a Git reset mode: what the reset rewrites
// besides HEAD.
//
// Example: git reset --hard selects hookGitResetWorktree.
type hookGitResetMode int

const (
	// hookGitResetMixed resets the index and no worktree file; it is Git's default mode.
	//
	// Example: git reset HEAD~1.
	hookGitResetMixed hookGitResetMode = iota

	// hookGitResetSoft moves HEAD only.
	//
	// Example: git reset --soft HEAD~1.
	hookGitResetSoft

	// hookGitResetWorktree resets the index and rewrites worktree files: --hard, --merge or --keep.
	//
	// Example: git reset --keep HEAD~1.
	hookGitResetWorktree
)

// inspectHookEffects completes callback-owned checks on every reachable command.
// Unknown shell values remain advisory unless a concrete effect is established.
//
// Example: a whole-index Git add or removal of the live validate-bash.sh returns a diagnostic.
func inspectHookEffects(
	request Request,
	result Result,
) *Diagnostic {
	if result.ShellAnalysis == nil {
		return nil
	}
	// TODO: diagnostics returned here leave RejectedSegment empty, so every
	// hook-effects denial prints "rejected segment=;". Fill it from the denied
	// record's argv, shell-escaped as classifier.go rejectedSegment does.
	for _, record := range result.ShellAnalysis.Commands {
		if !hookRecordRuns(record) {
			continue
		}
		// Every gate mode enforces the Python route, so check all records before
		// a mode-dependent denial such as the make cap can end the scan.
		if diagnostic := inspectHookPythonDirect(record); diagnostic != nil {
			return diagnostic
		}
	}
	for _, record := range result.ShellAnalysis.Commands {
		if !hookRecordRuns(record) {
			continue
		}
		// The memory cap applies to every callback, not only active ECI scopes.
		if diagnostic := inspectHookMakeMemoryCap(record); diagnostic != nil {
			return diagnostic
		}
		argv := make([]token, len(record.Argv))
		for index, value := range record.Argv {
			argv[index] = token{value: value, offset: index}
		}
		child := hookCommandChild(record, argv)
		if len(child) == 0 {
			continue
		}
		if !hookArgumentKnown(record, child[0].offset) {
			continue
		}
		if diagnostic := inspectHookGit(request, record, child); diagnostic != nil {
			return diagnostic
		}
		if diagnostic := inspectHookLiveGateRemoval(record, child); diagnostic != nil {
			return diagnostic
		}
		childRequest := request
		childRequest.CWD = record.CWD
		childRequest.CWDKnown = record.CWDKnown
		for index := 1; index < len(child); index++ {
			if !hookArgumentKnown(record, child[index].offset) {
				child[index].value = ""
			}
		}
		if diagnostic := inspectHookSessionsWriter(childRequest, child, record.Segment); diagnostic != nil {
			return diagnostic
		}
	}
	return nil
}

// hookRecordRuns reports whether a shell record can start a command, so
// callback checks inspect it.
//
// Example: the record after "false &&" is unreachable and returns false.
func hookRecordRuns(record ShellCommandRecord) bool {
	return record.Reachability != segmentUnreachable && len(record.Argv) > 0
}

// hookCodeMakeMemoryCapRequired identifies make without a finite memory cap.
//
// Example: make test without systemd-run MemoryMax selects this gate.
const hookCodeMakeMemoryCapRequired DiagnosticCode = "ECI_MAKE_MEMORY_CAP_REQUIRED"

// inspectHookMakeMemoryCap applies the cap gate for the binary's target OS.
//
// Example: an uncapped make is denied on Linux, but not on Darwin.
func inspectHookMakeMemoryCap(record ShellCommandRecord) *Diagnostic {
	return inspectHookMakeMemoryCapForOS(record, runtime.GOOS)
}

// inspectHookMakeMemoryCapForOS denies uncapped make except on Darwin, where
// the recognized launcher routes have no verified macOS equivalent.
//
// Example: systemd-run --scope -p MemoryMax=1G make passes on Linux.
func inspectHookMakeMemoryCapForOS(
	record ShellCommandRecord,
	goos string,
) *Diagnostic {
	if goos == "darwin" || !hookMakeUncapped(record.Argv) {
		return nil
	}
	return &Diagnostic{
		Code:        hookCodeMakeMemoryCapRequired,
		Operation:   "make-memory-cap",
		Segment:     record.Segment,
		Token:       "make",
		Reason:      "make must be run with a finite memory cap",
		Remediation: "for example: systemd-run --scope -p MemoryMax=1G make ...",
	}
}

// hookCodePythonScriptRequired identifies a direct Python interpreter call.
//
// Example: python3 -c 'print(1)' or /usr/bin/python3 tool.py selects this gate.
const hookCodePythonScriptRequired DiagnosticCode = "ECI_PYTHON_SCRIPT_REQUIRED"

// hookPythonTarget is a direct Python interpreter launch. A memory cap does not clear it.
//
// Example: hookLaunchReaches([]string{"env", "python3"}, hookPythonTarget{}) is true.
type hookPythonTarget struct{}

// matchesCommand reports whether word's basename names the Python runtime
// family. Aliases under other names are not resolved: the gate targets likely
// direct calls, not deliberate renaming.
//
// Example: python3, /usr/bin/python3.12, and .venv/bin/python match; ./py does not.
func (hookPythonTarget) matchesCommand(word string) bool {
	return namedRuntimeFamily(filepath.Base(word)) == "python"
}

// honorsMemoryCap reports that no launcher option permits a direct interpreter call.
//
// Example: systemd-run -p MemoryMax=1G python3 stays denied.
func (hookPythonTarget) honorsMemoryCap() bool { return false }

// inspectHookPythonDirect denies any direct Python interpreter call; the
// allowed route runs a script file whose shebang selects the interpreter.
//
// Example: python3 tool.py is denied, while ./tool.py is not.
func inspectHookPythonDirect(record ShellCommandRecord) *Diagnostic {
	if !hookLaunchReaches(record.Argv, hookPythonTarget{}) {
		return nil
	}
	return &Diagnostic{
		Code:        hookCodePythonScriptRequired,
		Operation:   "python-direct",
		Segment:     record.Segment,
		Token:       "python",
		Reason:      "the command calls a Python interpreter directly; Python runs only as an executable script file",
		Remediation: "write the code to a script file whose first line is #!/usr/bin/python3 (not #!/usr/bin/env python3, which resolves python3 through PATH and can reach a refusing wrapper), chmod +x it, and run it by path, for example ./script.py",
	}
}

// hookLauncherSpec describes option grammar for one transparent launcher.
//
// Example: timeout consumes value options, then one duration before its child.
type hookLauncherSpec struct {
	values     map[string]bool
	skipAssign bool
	positional int
	// capOption returns consumed argv words and whether this option imposes a cap.
	// TODO: coding-go forbids stored function values; replace capOption with a named per-launcher cap-option parser type.
	capOption func(tok string, next string, hasNext bool) (int, bool)
}

// hookLauncherChild returns the child argv selected by a launcher and whether an option capped it.
//
// Example: prlimit --as=1073741824 make returns [make] with capped true.
func hookLauncherChild(argv []string, spec hookLauncherSpec) ([]string, bool) {
	capped := false
	positional := spec.positional
	for index := 0; index < len(argv); {
		tok := argv[index]
		if tok == "--" {
			return argv[index+1:], capped
		}
		if spec.capOption != nil {
			next := ""
			hasNext := index+1 < len(argv)
			if hasNext {
				next = argv[index+1]
			}
			if consumed, limited := spec.capOption(tok, next, hasNext); consumed > 0 {
				capped = capped || limited
				index += consumed
				continue
			}
		}
		if strings.HasPrefix(tok, "-") && tok != "-" {
			if spec.values[tok] {
				index += 2
			} else {
				index++
			}
			continue
		}
		if spec.skipAssign && hookAssignmentWord(tok) {
			index++
			continue
		}
		if positional > 0 {
			positional--
			index++
			continue
		}
		return argv[index:], capped
	}
	return nil, capped
}

// hookAssignmentWord reports a leading NAME=value shell assignment word.
//
// Example: FOO=bar make has assignment FOO=bar before the make argv.
func hookAssignmentWord(word string) bool {
	name, _, found := strings.Cut(word, "=")
	return found && isIdentifier(name)
}

// hookFiniteMemoryLimit reports a numeric limit rather than an unbounded sentinel.
//
// Example: 1G and 1073741824 are finite; infinity and unlimited are not.
func hookFiniteMemoryLimit(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value != "" && value != "infinity" && value != "unlimited"
}

// hookLaunchTarget identifies a command that a launcher walk looks for.
//
// Example: hookMakeTarget matches make and lets a finite memory cap stop the walk.
type hookLaunchTarget interface {
	// matchesCommand reports whether one argv[0] word starts the target.
	//
	// Example: hookPythonTarget matches /usr/bin/python3.
	matchesCommand(word string) bool
	// honorsMemoryCap reports whether a finite launcher memory cap clears the target.
	//
	// Example: true for make, false for Python.
	honorsMemoryCap() bool
}

// hookMakeTarget is the make launch that the memory-cap gate requires capped.
//
// Example: hookLaunchReaches(argv, hookMakeTarget{}) finds env make.
type hookMakeTarget struct{}

// matchesCommand reports whether word names make.
//
// Example: /usr/bin/make matches.
func (hookMakeTarget) matchesCommand(word string) bool { return filepath.Base(word) == "make" }

// honorsMemoryCap reports that a finite cap satisfies the make gate.
//
// Example: prlimit --as=1G make passes.
func (hookMakeTarget) honorsMemoryCap() bool { return true }

// hookMakeUncapped reports whether argv can start make without a finite cap.
//
// Example: bash -c 'env make' is uncapped, while systemd-run -p MemoryMax=1G make is not.
func hookMakeUncapped(argv []string) bool {
	return hookLaunchReaches(argv, hookMakeTarget{})
}

// hookLaunchReaches reports whether argv can start target through recognized launchers.
// Launcher grammar is consumed structurally; unknown grammar never proves a cap.
// Each recursion strictly shortens the scanned text, so the walk terminates.
//
// Example: env python3 x.py reaches hookPythonTarget; printf python3 does not.
func hookLaunchReaches(argv []string, target hookLaunchTarget) bool {
	for len(argv) > 0 && hookAssignmentWord(argv[0]) {
		argv = argv[1:]
	}
	if len(argv) == 0 {
		return false
	}
	if target.matchesCommand(argv[0]) {
		return true
	}
	// TODO: launcher options are parsed here and again by the classifier's
	// unwrap* functions, and the option tables drift: stdbuf --output L python3 x
	// passes while stdbuf -o L python3 x is denied. Walk the classifier's
	// unwrapped argv instead of keeping these tables.
	name := filepath.Base(argv[0])
	switch name {
	case "systemd-run":
		child, capped := hookLauncherChild(argv[1:], hookLauncherSpec{
			values: map[string]bool{
				"-u": true, "--unit": true, "-M": true, "--machine": true, "-H": true, "--host": true,
				"-E": true, "--setenv": true, "--working-directory": true, "--service-type": true,
				"--uid": true, "--gid": true, "--slice": true, "--description": true, "--nice": true,
			},
			capOption:
			// consumes one systemd-run -p/--property option and reports whether it sets a finite MemoryMax.
			//
			// Example: -p MemoryMax=1G consumes two words and reports a cap.
			func(tok, next string, hasNext bool) (int, bool) {
				var value string
				var consumed int
				switch {
				case (tok == "-p" || tok == "--property") && hasNext:
					value, consumed = next, 2
				case strings.HasPrefix(tok, "--property="):
					value, consumed = strings.TrimPrefix(tok, "--property="), 1
				case strings.HasPrefix(tok, "-p") && len(tok) > 2:
					value, consumed = tok[2:], 1
				default:
					return 0, false
				}
				key, limit, _ := strings.Cut(value, "=")
				return consumed, strings.EqualFold(key, "MemoryMax") && hookFiniteMemoryLimit(limit)
			},
		})
		return !(capped && target.honorsMemoryCap()) && hookLaunchReaches(child, target)
	case "prlimit":
		child, capped := hookLauncherChild(argv[1:], hookLauncherSpec{
			values: map[string]bool{"-p": true, "--pid": true, "-o": true, "--output": true},
			capOption:
			// consumes one prlimit -v/--as option and reports whether its limit is finite.
			//
			// Example: --as=1073741824 consumes one word and reports a cap.
			func(tok, next string, hasNext bool) (int, bool) {
				switch {
				case (tok == "-v" || tok == "--as") && hasNext:
					return 2, hookFiniteMemoryLimit(next)
				case strings.HasPrefix(tok, "--as="):
					return 1, hookFiniteMemoryLimit(strings.TrimPrefix(tok, "--as="))
				case strings.HasPrefix(tok, "-v"):
					return 1, hookFiniteMemoryLimit(strings.TrimPrefix(strings.TrimPrefix(tok, "-v"), "="))
				}
				return 0, false
			},
		})
		return !(capped && target.honorsMemoryCap()) && hookLaunchReaches(child, target)
	case "find":
		for index := 1; index < len(argv); index++ {
			switch argv[index] {
			case "-exec", "-execdir", "-ok", "-okdir":
				end := index + 1
				for end < len(argv) && argv[end] != ";" && argv[end] != "+" {
					end++
				}
				if hookLaunchReaches(argv[index+1:end], target) {
					return true
				}
				index = end
			}
		}
		return false
	case "xargs":
		child, _ := hookLauncherChild(argv[1:], hookLauncherSpec{
			values: map[string]bool{
				"-I": true, "-n": true, "-P": true, "-a": true, "-d": true, "-s": true, "-L": true, "-E": true,
				"--arg-file": true, "--delimiter": true, "--max-args": true, "--max-procs": true,
				"--max-lines": true, "--max-chars": true, "--process-slot-var": true,
			},
		})
		return hookLaunchReaches(child, target)
	case "command":
		for _, tok := range argv[1:] {
			if !strings.HasPrefix(tok, "-") {
				break
			}
			if strings.ContainsAny(tok, "vV") {
				// command -v and -V inspect a name without executing a child.
				return false
			}
		}
		child, _ := hookLauncherChild(argv[1:], hookLauncherSpec{})
		return hookLaunchReaches(child, target)
	case "env":
		for index := 1; index < len(argv) && strings.HasPrefix(argv[index], "-"); index++ {
			if argv[index] == "-S" && index+1 < len(argv) {
				return hookLaunchReaches(strings.Fields(argv[index+1]), target)
			}
			if strings.HasPrefix(argv[index], "-S") && len(argv[index]) > 2 {
				return hookLaunchReaches(strings.Fields(argv[index][2:]), target)
			}
		}
		child, _ := hookLauncherChild(argv[1:], hookLauncherSpec{
			values:     map[string]bool{"-u": true, "--unset": true, "-C": true, "--chdir": true},
			skipAssign: true,
		})
		return hookLaunchReaches(child, target)
	case "bash", "sh", "dash", "zsh", "ksh":
		for index := 1; index < len(argv); index++ {
			tok := argv[index]
			if tok == "--" || !strings.HasPrefix(tok, "-") && !strings.HasPrefix(tok, "+") {
				// A script path or stdin payload is not a literal -c string.
				return false
			}
			if tok == "-o" || tok == "+o" {
				index++
				continue
			}
			if strings.HasPrefix(tok, "--") {
				continue
			}
			if strings.ContainsRune(tok, 'c') {
				if index+1 >= len(argv) {
					return false
				}
				// TODO: only the payload's first word is checked, so a launcher-wrapped
				// compound payload such as timeout 5 bash -c 'cd /tmp && python3 x' is
				// missed. Run the planner's shell analysis on the payload, as a direct
				// bash -c already gets, and walk each resulting record.
				return hookLaunchReaches(strings.Fields(argv[index+1]), target)
			}
		}
		return false
	case "sudo", "doas", "exec", "time", "nice", "timeout", "nohup", "setsid", "stdbuf", "chronic", "busybox":
		positional := 0
		if name == "timeout" {
			positional = 1
		}
		child, _ := hookLauncherChild(argv[1:], hookLauncherSpec{
			values:     hookWrapperValues(name),
			positional: positional,
		})
		return hookLaunchReaches(child, target)
	}
	return false
}

// hookWrapperValues lists options whose separate value word belongs to a launcher.
//
// Example: sudo -u root make consumes root as the sudo user argument.
func hookWrapperValues(name string) map[string]bool {
	switch name {
	case "sudo":
		return map[string]bool{
			"-u": true, "--user": true, "-g": true, "--group": true, "-D": true, "--chdir": true,
			"-h": true, "--host": true, "-p": true, "--prompt": true, "-r": true, "--role": true,
			"-t": true, "--type": true, "-T": true, "--command-timeout": true, "-C": true, "--close-from": true,
		}
	case "doas":
		return map[string]bool{"-a": true, "-C": true, "-u": true}
	case "exec":
		return map[string]bool{"-a": true}
	case "time":
		return map[string]bool{"-f": true, "--format": true, "-o": true, "--output": true}
	case "nice":
		return map[string]bool{"-n": true, "--adjustment": true}
	case "timeout":
		return map[string]bool{"-k": true, "--kill-after": true, "-s": true, "--signal": true}
	case "stdbuf":
		return map[string]bool{"-i": true, "-o": true, "-e": true}
	}
	return nil
}

// hookCommandChild retains the concrete child of transparent shell launches.
// Prefix environment records are context data rather than executable argv.
//
// Example: ECI_SESSION_ID=owner timeout 5 git add file selects git.
func hookCommandChild(
	record ShellCommandRecord,
	argv []token,
) []token {
	for len(argv) > 0 {
		name, _, assignment := strings.Cut(argv[0].value, "=")
		if !assignment || !isIdentifier(name) {
			break
		}
		argv = argv[1:]
	}
	if len(argv) == 0 {
		return nil
	}
	child, uncertainty := unwrap(argv, record.Segment)
	if uncertainty != nil || len(child) == 0 {
		return nil
	}
	if filepath.Base(child[0].value) != "timeout" {
		return child
	}
	index := 1
	for index < len(child) && strings.HasPrefix(child[index].value, "-") {
		if !hookArgumentKnown(record, child[index].offset) {
			return nil
		}
		switch child[index].value {
		case "--":
			index++
			goto duration
		case "-k", "--kill-after", "-s", "--signal":
			index++
			if index >= len(child) || !hookArgumentKnown(record, child[index].offset) {
				return nil
			}
		default:
			if !strings.HasPrefix(child[index].value, "--kill-after=") && !strings.HasPrefix(child[index].value, "--signal=") && child[index].value != "--preserve-status" && child[index].value != "--foreground" && child[index].value != "--verbose" {
				return nil
			}
		}
		index++
	}
duration:
	if index+1 >= len(child) || !hookArgumentKnown(record, child[index].offset) {
		return nil
	}
	child, uncertainty = unwrap(child[index+1:], record.Segment)
	if uncertainty != nil {
		return nil
	}
	return child
}

// hookArgumentKnown reports one concrete word with stable argv cardinality.
//
// Example: an unknown quoted commit message does not obscure the Git verb.
func hookArgumentKnown(
	record ShellCommandRecord,
	index int,
) bool {
	return !slices.Contains(record.UnknownArguments, index) &&
		!slices.Contains(record.MayDisappearArguments, index) &&
		!slices.Contains(record.MayMultiplyArguments, index) &&
		!slices.Contains(record.UnknownCardinalityArguments, index)
}

// hookPrefixAcknowledged reports a known word equal to acknowledgment anywhere before
// the command word: a prefix assignment, or an operand of a wrapper such as env.
//
// Example: ECI_WHOLE_TREE_RESET=1 git reset --hard acknowledges one whole-tree reset.
func hookPrefixAcknowledged(
	record ShellCommandRecord,
	child []token,
	acknowledgment hookAcknowledgment,
) bool {
	for index, value := range record.Argv[:child[0].offset] {
		if value == string(acknowledgment) && hookArgumentKnown(record, index) {
			return true
		}
	}
	return false
}

// hookGitDirectory resolves the directory the planner treats as the start of Git's
// relative paths: the callback directory, changed by -C in original order. Git resolves
// the last --work-tree after every -C, then starts relative paths at the current
// directory when it lies inside that tree, otherwise at the tree's top. In an unknown
// directory, an absolute --work-tree gives its top. --git-dir selects only the
// repository; without --work-tree, the current directory is the worktree top. GIT_DIR,
// GIT_WORK_TREE and core.worktree are not modeled. It never substitutes the callback
// directory for an unknown child directory.
//
// Example: git -C /project/sub --work-tree=.. add file selects /project/sub.
func hookGitDirectory(
	record ShellCommandRecord,
	argv []token,
	verbIndex int,
) string {
	directory := ""
	if record.CWDKnown {
		directory = record.CWD
	}
	workTree := ""
	for index := 1; index < verbIndex; index++ {
		value := argv[index].value
		target := ""
		switch {
		case value == "-C":
			index++
			if index >= verbIndex || !hookArgumentKnown(record, argv[index].offset) {
				return ""
			}
			target = argv[index].value
		case strings.HasPrefix(value, "-C"):
			if !hookArgumentKnown(record, argv[index].offset) {
				return ""
			}
			target = strings.TrimPrefix(value, "-C")
		case value == "--work-tree":
			index++
			if index >= verbIndex || !hookArgumentKnown(record, argv[index].offset) {
				return ""
			}
			workTree = argv[index].value
		case strings.HasPrefix(value, "--work-tree="):
			if !hookArgumentKnown(record, argv[index].offset) {
				return ""
			}
			_, workTree, _ = strings.Cut(value, "=")
		case isGitSplitContextOption(value):
			// Another option's separate value, such as --git-dir's, selects no directory.
			index++
			continue
		}
		if target == "" {
			continue
		}
		if filepath.IsAbs(target) {
			directory = resolvePathIdentity(target)
			continue
		}
		if directory == "" {
			return ""
		}
		// TODO: filepath.Join cancels .. textually, while Git resolves it from the physical
		// directory, so from a CWD reached through a symlink, git -C .. rm
		// hooks/validate-bash.sh is unchecked; resolve this join and the relative
		// --work-tree join below with resolvePhysicalDirectory, as physical cd does.
		directory = resolvePathIdentity(filepath.Join(directory, target))
	}
	if workTree == "" {
		return directory
	}
	if !filepath.IsAbs(workTree) {
		if directory == "" {
			return ""
		}
		workTree = filepath.Join(directory, workTree)
	}
	top := resolvePathIdentity(workTree)
	// TODO: after cd <dir> &&, Git starts at <dir> when it lies inside the tree, so
	// cd hooks && git --work-tree=<top> rm validate-bash.sh is unchecked, as is
	// cd sub && git --work-tree=<top> reset side when side is a branch and a path only at
	// the top; check every CWD candidate, as hookGitResetLoneRevision needs.
	if directory != "" && pathWithin(resolvePathIdentity(directory), top) {
		return directory
	}
	return top
}

// inspectHookGit checks concrete broad Git effects.
//
// Example: git add -A selects the whole-index gate.
func inspectHookGit(
	request Request,
	record ShellCommandRecord,
	argv []token,
) *Diagnostic {
	if filepath.Base(argv[0].value) != "git" {
		return nil
	}
	verbIndex := gitSubcommandIndex(argv)
	if verbIndex >= len(argv) || !hookArgumentKnown(record, argv[verbIndex].offset) {
		return nil
	}
	for _, index := range record.MayDisappearArguments {
		if index < argv[verbIndex].offset {
			return nil
		}
	}
	for _, index := range record.MayMultiplyArguments {
		if index < argv[verbIndex].offset {
			return nil
		}
	}
	for _, index := range record.UnknownCardinalityArguments {
		if index < argv[verbIndex].offset {
			return nil
		}
	}
	if argv[verbIndex].value == "worktree" && verbIndex+1 < len(argv) && !hookArgumentKnown(record, argv[verbIndex+1].offset) {
		return nil
	}
	directory := hookGitDirectory(record, argv, verbIndex)
	if diagnostic := inspectHookGitBroad(record, argv, verbIndex, directory); diagnostic != nil {
		return diagnostic
	}
	return nil
}

// inspectHookGitBroad identifies explicit whole-index or whole-worktree add, clean and
// reset effects. Unknown option and scalar identity remains advisory; previews stay
// native. A whole-tree reset prefixed with hookWholeResetAcknowledgment stays native.
//
// Example: git add -A and git reset --hard are whole; git add -A -- file is narrow.
func inspectHookGitBroad(
	record ShellCommandRecord,
	argv []token,
	verbIndex int,
	directory string,
) *Diagnostic {
	verb := argv[verbIndex].value
	arguments := argv[verbIndex+1:]
	route := "use native Git with exact owned repository-relative paths; preserve unrelated work"
	switch verb {
	case "add", "clean":
		if !hookGitAddCleanWhole(record, verb, arguments, directory) {
			return nil
		}
	case "reset":
		if !hookGitResetWhole(record, arguments, directory) || hookPrefixAcknowledged(record, argv, hookWholeResetAcknowledgment) {
			return nil
		}
		// A whole-index reset clears other Producers' staging, and --hard overwrites live files.
		// Reset alone offers an acknowledgment: an intended whole-tree reset has no path-scoped
		// equivalent, and gate-escape-hatches.md (Git destructive checks row) records that route.
		route = "unstage with git reset -- <owned paths>; undo your own edits with Edit or Write, or git restore --staged --worktree -- <owned paths>; move HEAD with git reset --soft <rev>; for an intended whole-tree reset after git status shows only your own staged and uncommitted work, prefix this command with " + string(hookWholeResetAcknowledgment) + "; a workspace Git guard may still refuse worktree or HEAD changes in a shared checkout, so follow its route there"
	default:
		return nil
	}
	// TODO: in an unknown working directory, directory is empty, so the reason ends in
	// ": " and Path is empty; name the unknown directory instead.
	diagnostic := diagnosticForToken(CodeBroadDestructiveDenied, "Git "+verb+" selects the whole repository index or worktree: "+directory,
		record.Segment, argv[verbIndex].offset, argv[verbIndex], route, "git-broad-effect")
	diagnostic.Path = directory
	return diagnostic
}

// hookGitAddCleanWhole reports a git add or clean that selects the whole worktree: add
// with -A or -u, or clean with a force, directory or ignored-file option, and no path
// other than a whole pathspec. Previews, help, unknown options and unknown operands
// stay advisory, except that an operand after -- that may expand to nothing narrows
// nothing.
//
// Example: git add -A and git clean -fdx are whole; git add -A -- owned.txt is not.
func hookGitAddCleanWhole(
	record ShellCommandRecord,
	verb string,
	arguments []token,
	directory string,
) bool {
	whole := false
	paths := 0
	options := true
	for _, argument := range arguments {
		known := hookArgumentKnown(record, argument.offset)
		value := argument.value
		operand := !options || !strings.HasPrefix(value, "-")
		switch {
		case !known && !options && slices.Contains(record.MayDisappearArguments, argument.offset):
			// An operand that may expand to nothing narrows nothing.
			continue
		case !known:
			return false
		case options && value == "--":
			options = false
			continue
		case operand && !hookGitWholePathspec(value, directory):
			// TODO: a whole pathspec among other paths still selects the whole worktree, so
			// git add -A owned.txt . is unchecked; count it as reset does.
			paths++
			continue
		case operand:
			continue
		}
		switch value {
		case "--dry-run", "-n", "--help", "-h":
			return false
		case "-A", "--all", "-u", "--update":
			whole = verb == "add"
		// TODO: another verb's option clears whole, so git add -A -f is unchecked; let
		// only the verb's own options change whole.
		case "-f", "-d", "-x", "-X", "-fd", "-fdx", "-df", "-ff", "-ffdx":
			whole = verb == "clean"
		case "-q", "--quiet", "-v", "--verbose":
		default:
			return false
		}
	}
	return whole && paths == 0
}

// hookGitWholePathspec reports a pathspec that selects the whole tree from the Git
// working directory: ., :/ or that directory, in any spelling that cleans to . or the
// directory, such as ./ or a trailing slash.
//
// Example: the . in git reset -- . and the ./ in git add -A ./ select every entry.
func hookGitWholePathspec(
	value string,
	directory string,
) bool {
	// TODO: from a subdirectory, . and the directory select only that subdirectory, while ..
	// or the top's own path can select the whole tree. Resolve the repository root to
	// classify both, for add and reset alike; until then cd sub && git reset -- . is
	// falsely denied and git -C sub --work-tree=<top> reset <top> is unchecked.
	cleaned := filepath.Clean(value)
	// TODO: this comparison is textual, so a symlinked spelling of directory, such as
	// git -C . reset <top> from a symlinked path, is unchecked; compare resolved path
	// identities.
	return cleaned == "." || value == ":/" || cleaned == directory
}

// hookGitResetWhole reports a git reset that resets the whole index, and with --hard,
// --merge or --keep the whole worktree. It follows Git's argument rules: options may
// follow operands, the last mode option wins, and -- ends options. --soft resets
// neither. A mixed reset is whole when no path remains or a whole pathspec is among
// its paths. Unknown options and unknown operands stay advisory, except that an operand
// after -- that may expand to nothing narrows nothing.
//
// Example: git reset main --hard, git reset origin/main and git reset -- owned.txt . are whole.
func hookGitResetWhole(
	record ShellCommandRecord,
	arguments []token,
	directory string,
) bool {
	mode := hookGitResetMixed
	// operandsBeforeSeparator counts the operands before --; -1 means no -- yet.
	operandsBeforeSeparator := -1
	operands := 0
	last := ""
	wholePath := false
	for _, argument := range arguments {
		known := hookArgumentKnown(record, argument.offset)
		value := argument.value
		switch {
		case !known && operandsBeforeSeparator >= 0 && slices.Contains(record.MayDisappearArguments, argument.offset):
			// An operand after -- that may expand to nothing narrows nothing.
		case !known:
			return false
		case operandsBeforeSeparator < 0 && value == "--":
			operandsBeforeSeparator = operands
		case operandsBeforeSeparator >= 0 || !strings.HasPrefix(value, "-"):
			wholePath = wholePath || hookGitWholePathspec(value, directory)
			last = value
			operands++
		case value == "--soft":
			mode = hookGitResetSoft
		case value == "--mixed":
			mode = hookGitResetMixed
		case value == "--hard" || value == "--merge" || value == "--keep":
			mode = hookGitResetWorktree
		case value != "-q" && value != "--quiet":
			// Other options, such as --patch or --pathspec-from-file, change what resets.
			// TODO: -N, --[no-]refresh and --[no-]recurse-submodules narrow nothing, yet they
			// leave the reset unchecked; treat them as -q is treated.
			return false
		}
	}
	switch {
	case mode == hookGitResetSoft:
		return false
	case mode == hookGitResetWorktree || operands == 0 || wholePath:
		return true
	case operandsBeforeSeparator == 1:
		// In git reset <rev> -- <paths>, no path leaves the whole index.
		return operands == 1
	case operandsBeforeSeparator == 0 || operands > 1:
		// Git reads every operand after a leading -- or after its first operand as a path.
		return false
	}
	return hookGitResetLoneRevision(last, directory)
}

// hookGitResetLoneRevision reports that Git may read the lone operand of a mixed
// reset without -- as a revision. Git reads an operand that exists in the working
// directory as a path, or refuses it when it also names a revision. A missing operand
// is a revision, a wildcard pathspec, or an error; the planner resolves no revision,
// such as HEAD^{/fix*}, so all three count as a revision. In an unknown directory only
// a hookHeadRevision operand counts. A lookup failure other than a missing path stays
// advisory.
//
// Example: git reset origin/main names a revision unless a path origin/main exists.
func hookGitResetLoneRevision(
	operand string,
	directory string,
) bool {
	if directory == "" {
		// TODO: after cd <dir> &&, the directory is unknown, so git reset origin/main is
		// unchecked there; count the operand as a revision when it is missing from every
		// CWD candidate.
		return hookHeadRevision(operand)
	}
	// TODO: Git reads a lone :/<path> as a path from the repository root when that path
	// exists, so git reset :/owned.txt is falsely denied; look it up there once the root
	// is resolved. A lone :!<path> or :^<path> resets every other path and stays checked.
	path := operand
	if !filepath.IsAbs(path) {
		path = joinLiteralPath(directory, path)
	}
	_, err := os.Lstat(path)
	// Git's is_missing_file_error also counts ENOTDIR, a file in place of a directory.
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// hookHeadRevision reports HEAD or @ with optional ~ and ^ steps. Git reads it as a
// revision whenever it resolves and refuses it when a same-named path also exists, so
// it needs no directory lookup.
//
// Example: HEAD~1 and @^ qualify; HEADER.md does not.
func hookHeadRevision(value string) bool {
	name, steps := value, ""
	if index := strings.IndexAny(value, "~^"); index >= 0 {
		name, steps = value[:index], value[index:]
	}
	return (name == "HEAD" || name == "@") && strings.Trim(steps, "~^0123456789") == ""
}

// inspectHookSessionsWriter protects the runtime's session transcript directory.
//
// Example: a Worker redirect to a resolved session transcript is denied.
func inspectHookSessionsWriter(
	request Request,
	argv []token,
	segmentIndex int,
) *Diagnostic {
	if request.Role != RoleWorker || len(argv) == 0 {
		return nil
	}
	name := filepath.Base(argv[0].value)
	if !isSourceWriter(name, argv) && name != "find" {
		return nil
	}
	for index := 1; index < len(argv); index++ {
		argument := argv[index]
		if argument.value == "" || strings.HasPrefix(argument.value, "-") || !isSourceWriterOperand(argv, index) && name != "find" {
			continue
		}
		if name == "find" {
			action, found := findDynamicAction(argv)
			if !found || action.value != "-delete" || index != 1 {
				continue
			}
		}
		pathArgument, pathLike := commandPathOperand(argv[0].value, argument)
		if name == "cp" || name == "find" {
			pathArgument, pathLike = argument, true
		}
		if !pathLike {
			continue
		}
		if diagnostic := hookSessionsOperandDiagnostic(request, name, argv, segmentIndex, index, pathArgument); diagnostic != nil {
			return diagnostic
		}
	}
	return nil
}

// hookSessionsRoots returns the lexical and symlink-resolved omp sessions roots under HOME.
// It returns nil when HOME is not absolute, so no Worker deny is inferred.
//
// Example: $HOME/.omp/agent/sessions and its resolved target both name transcript storage.
func hookSessionsRoots() []string {
	home := os.Getenv("HOME")
	if !filepath.IsAbs(home) {
		return nil
	}
	lexical := filepath.Clean(filepath.Join(home, ".omp", "agent", "sessions"))
	roots := []string{lexical}
	if resolved := resolvePathIdentity(lexical); resolved != lexical {
		roots = append(roots, resolved)
	}
	return roots
}

// hookSessionsRootTarget reports whether a Worker writer or removal reaches the sessions root.
// Removals and move sources also deny an operand that is an ancestor of the root.
//
// Example: rm of $HOME/.omp/agent removes the transcripts beneath the root.
func hookSessionsRootTarget(name string, final bool, lexical, resolved string) bool {
	ancestor := name == "rm" || name == "rmdir" || name == "unlink" || name == "shred" || name == "srm" || name == "find" || name == "mv" && !final
	for _, root := range hookSessionsRoots() {
		if pathWithin(lexical, root) || pathWithin(resolved, root) {
			return true
		}
		if ancestor && pathWithin(root, resolved) {
			return true
		}
	}
	return false
}

// hookTildePrefix returns the text between a leading "~" and the first "/", and whether
// bash expands that tilde-prefix. Only an unquoted tilde-prefix expands; any quoting inside
// it makes the word literal. Quoting after the first unquoted "/" does not stop expansion.
// A word without raw offsets is treated as unquoted for expansion analysis.
//
// Example: ~/".omp/x" expands with an empty prefix, while ~"/.omp/x" stays literal.
func hookTildePrefix(argument token) (string, bool) {
	if !strings.HasPrefix(argument.value, "~") {
		return "", false
	}
	slash := strings.IndexByte(argument.value, '/')
	if slash < 0 {
		slash = len(argument.value)
	}
	prefix := argument.value[1:slash]
	if len(argument.sourceOffsets) != len(argument.value) {
		return prefix, true
	}
	leadingUnquoted := argument.sourceOffsets[0] == argument.offset
	if slash == len(argument.value) {
		return prefix, leadingUnquoted && !argument.quoted
	}
	return prefix, leadingUnquoted && argument.sourceOffsets[slash] == argument.offset+slash
}

// hookTildeHome returns HOME for an unquoted leading "~" or "~/...".
// Other tilde prefixes and non-absolute HOME do not yield an expansion.
//
// Example: unquoted ~/.omp/agent/sessions/x.jsonl expands under HOME, while "~/x" and ~"/x" do not.
func hookTildeHome(argument token) (string, bool) {
	prefix, expands := hookTildePrefix(argument)
	if !expands || prefix != "" {
		return "", false
	}
	home := os.Getenv("HOME")
	if !filepath.IsAbs(home) {
		return "", false
	}
	return filepath.Clean(home), true
}

// hookSessionsMention reports whether operand text contains the literal ".omp" spelling.
// It helps identify ambiguous session-root spellings that remain advisory.
//
// Example: ~/.omp/agent/sess[s]ions/x mentions it; ~/tmp/session-notes.txt does not.
func hookSessionsMention(text string) bool {
	return strings.Contains(text, ".omp")
}

// hookSessionsAmbiguous reports whether an operand that mentions the sessions location has a
// spelling the lexical check cannot establish: a glob or brace, a "~user", "~+", or "~-" prefix,
// a mixed quote form after an unquoted "~", a ".." after a component, or an unknown CWD for a
// relative operand (an absolute operand does not depend on the CWD).
//
// Example: ~/.omp/agent/sess*/x and ~/.omp/agent/x/../sessions/y are ambiguous.
func hookSessionsAmbiguous(argument token, cwdKnown bool) bool {
	if !cwdKnown || strings.ContainsAny(argument.value, "*?[{}") {
		return true
	}
	if prefix, expands := hookTildePrefix(argument); expands && (prefix != "" || argument.quoted) {
		return true
	}
	for index, component := range strings.Split(argument.value, "/") {
		if index > 0 && component == ".." {
			return true
		}
	}
	return false
}

// hookSessionsOperandDiagnostic denies a resolved Worker write to the sessions root.
//
// Example: tee to a session transcript is denied; an ambiguous glob is advisory.
func hookSessionsOperandDiagnostic(
	request Request,
	name string,
	argv []token,
	segmentIndex int,
	argvIndex int,
	argument token,
) *Diagnostic {
	home, expands := hookTildeHome(argument)
	text := argument.value
	if expands {
		text = home + argument.value[1:]
	}
	if (hookSessionsMention(argument.value) || hookSessionsMention(text)) && hookSessionsAmbiguous(argument, request.CWDKnown || filepath.IsAbs(text)) {
		return nil
	}
	lexical := argument.value
	switch {
	case expands:
		lexical = text
	case !filepath.IsAbs(lexical):
		if !request.CWDKnown {
			return nil
		}
		lexical = filepath.Join(request.CWD, lexical)
	}
	lexical = filepath.Clean(lexical)
	var resolved string
	var err error
	if hookUnlinksDirectoryEntry(argv) {
		resolved, err = resolvePathEntryWithMissingSuffix(lexical)
	} else {
		resolved, err = resolvePathWithMissingSuffix(lexical)
	}
	if err != nil {
		return nil
	}
	if !hookSessionsRootTarget(name, argvIndex == len(argv)-1, lexical, resolved) {
		return nil
	}
	diagnostic := diagnosticForToken(hookCodeWorkerSessionsRootDenied, "Worker write or removal selects omp session transcript root "+resolved,
		segmentIndex, argvIndex, argument, "use the owned task path outside the omp sessions root; session transcripts belong to the omp runtime", "worker-sessions-root")
	diagnostic.Path = resolved
	return diagnostic
}

// hookCodeLiveBashGateRemoval identifies removal or relocation of a file the Bash gate runs.
//
// Example: rm hooks/validate-bash.sh in the live provider root selects this gate.
const hookCodeLiveBashGateRemoval DiagnosticCode = "ECI_LIVE_BASH_GATE_REMOVAL_DENIED"

// liveBashGateRelativePaths lists provider files every Bash callback executes.
// Removing one blocks every later Bash call, including the shell repair route,
// so the same actor could not restore it with Bash. Other hook files stay ordinary targets.
//
// Example: hooks/lib/eci-bash-guard.sh is sourced by hooks/validate-bash.sh.
var liveBashGateRelativePaths = [...]string{
	"hooks/validate-bash.sh",
	"hooks/lib/eci-bash-guard.sh",
	"hooks/lib/eci-native-binary.sh",
}

// inspectHookLiveGateRemoval protects existing live Bash-gate files from removal or relocation.
// Only rm, rmdir, unlink, shred, srm, mv sources and physical git rm/mv sources are checked.
// This gate does not check Git checkout, restore or reset; the Live Bash-gate removal
// row of gate-escape-hatches.md records why.
// Paths outside the live provider roots, including clones and other repositories, are unaffected.
//
// Example: rm hooks/validate-bash.sh is denied; ECI_LIVE_HOOK_REMOVAL=1 rm hooks/validate-bash.sh is not.
func inspectHookLiveGateRemoval(
	record ShellCommandRecord,
	child []token,
) *Diagnostic {
	name, base, targets := hookRemovalTargets(record, child)
	if len(targets) == 0 {
		return nil
	}
	if hookPrefixAcknowledged(record, child, hookLiveRemovalAcknowledgment) {
		return nil
	}
	for _, argument := range targets {
		lexical := argument.value
		if !filepath.IsAbs(lexical) {
			if base == "" {
				continue
			}
			lexical = filepath.Join(base, lexical)
		}
		target := resolvePathIdentity(filepath.Clean(lexical))
		if name != "shred" && name != "srm" {
			// Entry operations remove a symlink itself, not its referent.
			entry, err := resolvePathEntryWithMissingSuffix(filepath.Clean(lexical))
			if err != nil {
				continue
			}
			target = entry
		}
		for _, root := range protectedHookModeRoots() {
			for _, relative := range liveBashGateRelativePaths {
				hook := filepath.Join(root, relative)
				if !pathWithin(hook, target) {
					continue
				}
				if info, err := os.Stat(hook); err != nil || !info.Mode().IsRegular() {
					// An absent hook cannot be locked out further.
					continue
				}
				diagnostic := diagnosticForToken(hookCodeLiveBashGateRemoval,
					name+" removes or relocates live Bash-gate file "+hook+" through "+target+"; afterwards every Bash call is blocked, including the shell repair route",
					record.Segment, argument.offset, argument,
					"edit the file in place with Edit or Write; for index-only removal use git rm --cached; to remove or relocate it here intentionally, prefix this command with "+string(hookLiveRemovalAcknowledgment)+" and restore the file with Write before the next Bash call",
					"live-bash-gate-removal")
				diagnostic.Path = target
				return diagnostic
			}
		}
	}
	return nil
}

// hookRemovalTargets returns the command name, relative-path base and removed or moved-away operands.
// Destinations are excluded because replacing a hook keeps the gate present.
// Index-only and preview Git forms return no targets.
//
// Example: git -C repo mv a b returns git, repo and [a].
func hookRemovalTargets(
	record ShellCommandRecord,
	child []token,
) (string, string, []token) {
	name := filepath.Base(child[0].value)
	base := ""
	if record.CWDKnown {
		base = record.CWD
	}
	arguments := child[1:]
	moves := name == "mv"
	switch name {
	case "rm", "rmdir", "unlink", "shred", "srm", "mv":
	case "git":
		verbIndex := gitSubcommandIndex(child)
		if verbIndex >= len(child) || !hookArgumentKnown(record, child[verbIndex].offset) {
			return name, "", nil
		}
		if verb := child[verbIndex].value; verb != "rm" && verb != "mv" {
			return name, "", nil
		}
		base = hookGitDirectory(record, child, verbIndex)
		arguments = child[verbIndex+1:]
		moves = child[verbIndex].value == "mv"
	default:
		return name, "", nil
	}
	operands := make([]token, 0, len(arguments))
	targetDirectory := false
	options := true
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if !hookArgumentKnown(record, argument.offset) {
			continue
		}
		value := argument.value
		if options && value == "--" {
			options = false
			continue
		}
		if options && strings.HasPrefix(value, "-") && value != "-" {
			switch {
			// git rm and git mv short options take no values, so -rn is a preview like -n.
			case name == "git" && (value == "--cached" || value == "--dry-run" || !strings.HasPrefix(value, "--") && strings.ContainsRune(value, 'n')):
				return name, "", nil
			case name == "mv" && (value == "-t" || value == "--target-directory"):
				targetDirectory = true
				index++
			case name == "mv" && (strings.HasPrefix(value, "--target-directory=") || strings.HasPrefix(value, "-t") && !strings.HasPrefix(value, "--")):
				targetDirectory = true
			}
			continue
		}
		operands = append(operands, argument)
	}
	if moves && !targetDirectory && len(operands) > 0 {
		operands = operands[:len(operands)-1]
	}
	return name, base, operands
}
