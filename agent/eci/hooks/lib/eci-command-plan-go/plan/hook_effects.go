package plan

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// hookCodeWorkerSessionsRootDenied identifies Worker writes to omp session transcripts.
//
// Example: echo x > $HOME/.omp/agent/sessions/slug/new.jsonl selects this gate.
const hookCodeWorkerSessionsRootDenied DiagnosticCode = "ECI_WORKER_SESSIONS_ROOT_DENIED"

// inspectHookEffects completes callback-owned checks on every reachable command.
// Unknown shell values remain advisory unless a concrete effect is established.
//
// Example: a whole-index Git add returns a diagnostic.
func inspectHookEffects(
	request Request,
	result Result,
) *Diagnostic {
	if result.ShellAnalysis == nil {
		return nil
	}
	for _, record := range result.ShellAnalysis.Commands {
		if record.Reachability == segmentUnreachable || len(record.Argv) == 0 {
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

// hookCodeMakeMemoryCapRequired identifies make without a finite memory cap.
//
// Example: make test without systemd-run MemoryMax selects this gate.
const hookCodeMakeMemoryCapRequired DiagnosticCode = "ECI_MAKE_MEMORY_CAP_REQUIRED"

// inspectHookMakeMemoryCap denies make unless a launcher imposes a finite memory cap.
//
// Example: systemd-run --scope -p MemoryMax=1G make passes; env make is denied.
func inspectHookMakeMemoryCap(record ShellCommandRecord) *Diagnostic {
	if !hookMakeUncapped(record.Argv) {
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

// hookLauncherSpec describes option grammar for one transparent launcher.
//
// Example: timeout consumes value options, then one duration before its child.
type hookLauncherSpec struct {
	values     map[string]bool
	skipAssign bool
	positional int
	// capOption returns consumed argv words and whether this option imposes a cap.
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

// hookMakeUncapped reports whether argv can start make without a finite cap.
// Launcher grammar is consumed structurally; unknown grammar never proves a cap.
// Each recursion strictly shortens the scanned text, so the walk terminates.
//
// Example: bash -c 'env make' is uncapped, while systemd-run -p MemoryMax=1G make is not.
func hookMakeUncapped(argv []string) bool {
	for len(argv) > 0 && hookAssignmentWord(argv[0]) {
		argv = argv[1:]
	}
	if len(argv) == 0 {
		return false
	}
	name := filepath.Base(argv[0])
	switch name {
	case "make":
		return true
	case "systemd-run":
		child, capped := hookLauncherChild(argv[1:], hookLauncherSpec{
			values: map[string]bool{
				"-u": true, "--unit": true, "-M": true, "--machine": true, "-H": true, "--host": true,
				"-E": true, "--setenv": true, "--working-directory": true, "--service-type": true,
				"--uid": true, "--gid": true, "--slice": true, "--description": true, "--nice": true,
			},
			capOption: func(tok, next string, hasNext bool) (int, bool) {
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
		return !capped && hookMakeUncapped(child)
	case "prlimit":
		child, capped := hookLauncherChild(argv[1:], hookLauncherSpec{
			values: map[string]bool{"-p": true, "--pid": true, "-o": true, "--output": true},
			capOption: func(tok, next string, hasNext bool) (int, bool) {
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
		return !capped && hookMakeUncapped(child)
	case "find":
		for index := 1; index < len(argv); index++ {
			switch argv[index] {
			case "-exec", "-execdir", "-ok", "-okdir":
				end := index + 1
				for end < len(argv) && argv[end] != ";" && argv[end] != "+" {
					end++
				}
				if hookMakeUncapped(argv[index+1 : end]) {
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
		return hookMakeUncapped(child)
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
		return hookMakeUncapped(child)
	case "env":
		for index := 1; index < len(argv) && strings.HasPrefix(argv[index], "-"); index++ {
			if argv[index] == "-S" && index+1 < len(argv) {
				return hookMakeUncapped(strings.Fields(argv[index+1]))
			}
			if strings.HasPrefix(argv[index], "-S") && len(argv[index]) > 2 {
				return hookMakeUncapped(strings.Fields(argv[index][2:]))
			}
		}
		child, _ := hookLauncherChild(argv[1:], hookLauncherSpec{
			values:     map[string]bool{"-u": true, "--unset": true, "-C": true, "--chdir": true},
			skipAssign: true,
		})
		return hookMakeUncapped(child)
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
				return hookMakeUncapped(strings.Fields(argv[index+1]))
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
		return hookMakeUncapped(child)
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
	if verb != "add" && verb != "clean" {
		return nil
	}
	whole := false
	paths := 0
	options := true
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
			continue
		}
		if options && strings.HasPrefix(value, "-") {
			switch value {
			case "--dry-run", "-n", "--help", "-h":
				return nil
			case "-A", "--all", "-u", "--update":
				whole = verb == "add"
			case "-f", "-d", "-x", "-X", "-fd", "-fdx", "-df", "-ff", "-ffdx":
				whole = verb == "clean"
			case "-q", "--quiet", "-v", "--verbose":
			default:
				return nil
			}
			continue
		}
		if value != "." && value != ":/" && value != directory {
			paths++
		}
	}
	if !whole || paths > 0 {
		return nil
	}
	diagnostic := diagnosticForToken(CodeBroadDestructiveDenied, "Git "+verb+" selects the whole repository index or worktree: "+directory,
		record.Segment, argv[verbIndex].offset, argv[verbIndex], "use native Git with exact owned repository-relative paths; preserve unrelated work", "git-broad-effect")
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
