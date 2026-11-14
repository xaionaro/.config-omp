package plan

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
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
		if diagnostic := inspectHookLiveGateRemoval(record, argv, child); diagnostic != nil {
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

// hookPythonInterpreter reports whether path's basename names the Python runtime family.
//
// Example: /usr/bin/python3.12 is Python, while python-tool is not.
func hookPythonInterpreter(path string) bool {
	return namedRuntimeFamily(filepath.Base(path)) == "python"
}

// hookPythonTarget is a direct Python interpreter launch. A memory cap does not clear it.
// CWD resolves relative command paths and is empty when the command's directory is unknown.
//
// Example: hookLaunchReaches([]string{"env", "python3"}, hookPythonTarget{}) is true.
type hookPythonTarget struct {
	CWD string
}

// matchesCommand reports whether word names a Python interpreter, either by
// basename or by resolving it through PATH (bare names), CWD (relative paths),
// and symlinks. A relative path with unknown CWD, or a word that resolves to no
// runnable file, falls back to the basename alone.
//
// Example: python3, /usr/bin/python3, and ./py -> /usr/bin/python3 match.
func (t hookPythonTarget) matchesCommand(word string) bool {
	if hookPythonInterpreter(word) {
		return true
	}
	if strings.Contains(word, "/") && !filepath.IsAbs(word) {
		if t.CWD == "" {
			return false
		}
		word = filepath.Join(t.CWD, word)
	}
	resolved, ok := hookResolveCommand(word)
	return ok && hookPythonInterpreter(resolved)
}

// hookResolveCommand resolves a command word through PATH and symlinks to its
// physical file. A word that names no runnable file returns false; any other
// lookup failure is also reported as a callback advisory, because an
// unresolved word establishes no launch.
//
// Example: python3 returns /usr/bin/python3.12 when PATH reaches that symlink chain.
func hookResolveCommand(word string) (string, bool) {
	path, err := exec.LookPath(word)
	if err == nil {
		path, err = filepath.EvalSymlinks(path)
	}
	switch {
	case err == nil:
		return path, true
	case hookCommandAbsent(err):
		return "", false
	default:
		hookAdvisory("resolve command word", err)
		return "", false
	}
}

// hookCommandAbsent reports a lookup failure meaning the word names no runnable
// file, so the shell could not start it either.
//
// Example: exec.ErrNotFound for an unknown bare name, or ENOTDIR for ./file/x.
func hookCommandAbsent(err error) bool {
	for _, absent := range []error{
		exec.ErrNotFound, fs.ErrNotExist, fs.ErrPermission,
		syscall.ENOTDIR, syscall.EISDIR, syscall.ELOOP, syscall.ENAMETOOLONG,
	} {
		if errors.Is(err, absent) {
			return true
		}
	}
	return false
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
	target := hookPythonTarget{}
	if record.CWDKnown {
		target.CWD = record.CWD
	}
	if !hookLaunchReaches(record.Argv, target) {
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

// hookGitDirectory resolves explicit Git directory selection in original order.
// It never substitutes the callback directory for an unknown child directory.
//
// Example: git -C /project -C subdir add file selects /project/subdir.
func hookGitDirectory(
	record ShellCommandRecord,
	argv []token,
	verbIndex int,
) string {
	directory := ""
	if record.CWDKnown {
		directory = record.CWD
	}
	for index := 1; index < verbIndex; index++ {
		value := argv[index].value
		target := ""
		switch {
		case value == "-c" || value == "--config-env" || value == "--exec-path" || value == "--namespace" || value == "--super-prefix":
			index++
			continue
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
		case value == "--work-tree" || value == "--git-dir":
			index++
			if index >= verbIndex || !hookArgumentKnown(record, argv[index].offset) {
				return ""
			}
			target = argv[index].value
		case strings.HasPrefix(value, "--work-tree=") || strings.HasPrefix(value, "--git-dir="):
			if !hookArgumentKnown(record, argv[index].offset) {
				return ""
			}
			_, target, _ = strings.Cut(value, "=")
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
		directory = resolvePathIdentity(filepath.Join(directory, target))
	}
	return directory
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

// inspectHookGitBroad identifies explicit whole-index or whole-worktree effects.
// Unknown option and scalar identity remains advisory; previews stay native.
//
// Example: git add -A -- file is narrow, whereas git add -A is whole-worktree.
func inspectHookGitBroad(
	record ShellCommandRecord,
	argv []token,
	verbIndex int,
	directory string,
) *Diagnostic {
	verb := argv[verbIndex].value
	if verb != "add" && verb != "reset" && verb != "clean" {
		return nil
	}
	whole := verb == "reset" && verbIndex+1 == len(argv)
	paths := 0
	options := true
	revision := false
	for _, argument := range argv[verbIndex+1:] {
		if !hookArgumentKnown(record, argument.offset) {
			if !options && whole && slices.Contains(record.MayDisappearArguments, argument.offset) {
				continue
			}
			return nil
		}
		value := argument.value
		if options && value == "--" {
			options = false
			if verb == "reset" {
				whole = true
			}
			continue
		}
		if options && strings.HasPrefix(value, "-") {
			switch value {
			case "--dry-run", "-n", "--help", "-h":
				return nil
			case "-A", "--all", "-u", "--update":
				whole = verb == "add"
			case "--hard", "--mixed", "--merge", "--keep":
				whole = verb == "reset"
			case "--soft":
				return nil
			case "-f", "-d", "-x", "-X", "-fd", "-fdx", "-df", "-ff", "-ffdx":
				whole = verb == "clean"
			case "-q", "--quiet", "-v", "--verbose":
			default:
				return nil
			}
			continue
		}
		if verb == "reset" && options && !revision {
			if !whole {
				// Without an explicit reset mode or --, this literal may be
				// a path rather than a revision. That identity stays advisory.
				return nil
			}
			revision = true
			continue
		}
		if value != "." && value != ":/" && value != directory {
			paths++
		}
	}
	if !whole || paths > 0 {
		return nil
	}
	route := "use native Git with exact owned repository-relative paths; preserve unrelated work"
	if verb == "reset" {
		// A whole-index reset clears other Producers' staging and --hard
		// overwrites live files; this route keeps every effect path-scoped.
		route = "unstage with git reset -- <owned paths>; discard with git restore [--staged] -- <owned paths>; move HEAD with git reset --soft <rev>, then restore owned paths"
	}
	diagnostic := diagnosticForToken(CodeBroadDestructiveDenied, "Git "+verb+" selects the whole repository index or worktree: "+directory,
		record.Segment, argv[verbIndex].offset, argv[verbIndex], route, "git-broad-effect")
	diagnostic.Path = directory
	return diagnostic
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

// hookLiveRemovalAcknowledgment is the prefix assignment that admits one intended removal.
//
// Example: ECI_LIVE_HOOK_REMOVAL=1 git rm hooks/validate-bash.sh.
const hookLiveRemovalAcknowledgment = "ECI_LIVE_HOOK_REMOVAL=1"

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
// Git checkout and restore stay native: they rewrite a tracked hook with tracked content, so
// the gate keeps running; discarding uncommitted edits follows ordinary preservation practice.
// Paths outside the live provider roots, including clones and other repositories, are unaffected.
//
// Example: rm hooks/validate-bash.sh is denied; ECI_LIVE_HOOK_REMOVAL=1 rm hooks/validate-bash.sh is not.
func inspectHookLiveGateRemoval(
	record ShellCommandRecord,
	argv []token,
	child []token,
) *Diagnostic {
	name, base, targets := hookRemovalTargets(record, child)
	if len(targets) == 0 {
		return nil
	}
	for _, prefix := range argv[:child[0].offset] {
		if prefix.value == hookLiveRemovalAcknowledgment && hookArgumentKnown(record, prefix.offset) {
			return nil
		}
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
					"edit the file in place with Edit or Write; for index-only removal use git rm --cached; to remove or relocate it here intentionally, prefix this command with "+hookLiveRemovalAcknowledgment+" and restore the file with Write before the next Bash call",
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
