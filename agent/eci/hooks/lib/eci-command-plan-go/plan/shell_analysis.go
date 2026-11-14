package plan

import (
	"path/filepath"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/pattern"
	"mvdan.cc/sh/v3/syntax"
)

// ShellAnalysis carries executable argv independently from lossless raw topology.
// Incomplete parsing is advisory; each retained record still names a command.
//
// Example: a quoted heredoc contributes its cat header and the following git.
type ShellAnalysis struct {
	Commands   []ShellCommandRecord `json:"commands"`
	Incomplete bool                 `json:"incomplete"`
	// OutputWrites records a filesystem output effect for callback bookkeeping.
	// Example: >> log is a write; 2>&1 only duplicates a descriptor.
	OutputWrites bool `json:"-"`
}

// ShellCommandRecord binds arguments and per-word uncertainty to incoming
// directory/reachability facts. It is analysis data, never executable input.
//
// Example: a substitution's cd changes its own later records, not its parent.
type ShellCommandRecord struct {
	Argv                        []string            `json:"argv"`
	UnknownArguments            []int               `json:"unknown_arguments,omitempty"`
	MayDisappearArguments       []int               `json:"may_disappear_arguments"`
	MayMultiplyArguments        []int               `json:"may_multiply_arguments"`
	UnknownCardinalityArguments []int               `json:"unknown_cardinality_arguments,omitempty"`
	CWD                         string              `json:"cwd"`
	CWDKnown                    bool                `json:"cwd_known"`
	CWDCandidates               []string            `json:"cwd_candidates"`
	Reachability                SegmentReachability `json:"reachability"`
	Segment                     int                 `json:"segment"`
}

// shellPayload describes a statically selected child program or argv/context.
//
// Example: env -S exposes its split argv without executing generated shell text.
type shellPayload struct {
	command        string
	commandDynamic bool
	launchState    *shellExpansionState
	argv           []shellArgument
	arguments      []shellArgument
	stdin          bool
	handled        bool
	incomplete     bool
	noExecution    bool
	substitutions  []shellSubstitution
	cwd            string
	cwdKnown       bool
	cwdSet         bool
	environment    []shellArgument
	contextUnknown bool
}

// shellSourceCall separates executable arguments from its prefix environment.
// Dynamic marks unevaluated values without discarding the concrete argv around them.
//
// Example: X=$RANDOM bash -s retains bash while the assignment stays unresolved.
type shellSourceCall struct {
	Argv          []shellArgument
	Environment   []shellArgument
	Dynamic       bool
	Substitutions []shellSubstitution
}

// shellArgument retains one word's value separately from expansion uncertainty.
//
// Example: a dynamic commit message leaves the adjacent literal -C path known.
type shellArgument struct {
	Value              string
	Literal            bool
	MayDisappear       bool
	MayMultiply        bool
	CardinalityUnknown bool
}

// singleField reports whether source syntax guarantees exactly one argv field.
//
// Example: a quoted scalar stays one field even when its value is unavailable.
func (a shellArgument) singleField() bool {
	return !a.MayDisappear && !a.MayMultiply && !a.CardinalityUnknown
}

// inspectShellAnalysis reuses the ordinary segment classifier for executable
// heredoc children and publishes their argv for the provider's target checks.
//
// Example: an inert apostrophe is masked while $(git rebase topic) is inspected.
func inspectShellAnalysis(
	request Request,
	parsed plan,
	states []compoundSegmentState,
) (*ShellAnalysis, *Result) {
	if request.ShellDepth >= maxWrapperDepth {
		return nil, nil
	}
	analysis := &ShellAnalysis{Commands: []ShellCommandRecord{}, Incomplete: parsed.projectionIncomplete}
	needed := request.CollectShellCommands || parsed.projection != nil
	expansionFacts := shellExpansionSegments(request.Command, parsed.segments, request.ShellExpansion)
	for index, current := range parsed.segments {
		state := states[index]
		if state.reachability == segmentUnreachable {
			continue
		}
		for _, redirect := range current.redirects {
			analysis.OutputWrites = analysis.OutputWrites || !redirect.descriptorDuplication
		}
		childRequest := shellChildRequest(request, state)
		childRequest.ShellExpansion = expansionFacts[index].state
		if parsed.projection != nil {
			for _, child := range parsed.projection.Substitutions {
				if child.OwnerOffset < current.sourceStart || child.OwnerOffset >= current.sourceStart+len(current.command) {
					continue
				}
				childRequest.Command = child.Command
				result := Classify(childRequest)
				if result.Decision == DecisionDeny {
					return nil, &result
				}
				appendShellAnalysis(analysis, result.ShellAnalysis)
			}
		}
		effect, inspect := effectSegmentAfterKeywords(current)
		if !inspect {
			continue
		}
		payload := literalShellPayload(effect.argv, effect.command, request.ShellEnvironment, state.cwd.cwd, state.cwd.known, expansionFacts[index])
		analysis.Incomplete = analysis.Incomplete || payload.contextUnknown
		for _, substitution := range payload.substitutions {
			childRequest.Command = substitution.Command
			result := Classify(childRequest)
			if result.Decision == DecisionDeny {
				return nil, &result
			}
			// Process substitutions stay visible to the provider, as at HEAD.
			needed = needed || substitution.Process || result.Decision != DecisionAllow
			appendShellAnalysis(analysis, result.ShellAnalysis)
		}
		if payload.handled {
			if payload.noExecution {
				needed = true
				continue
			}
			if payload.incomplete {
				needed = true
				analysis.Incomplete = true
				continue
			}
			if payload.contextUnknown {
				// Environment uncertainty does not change the process CWD.
				// Retain inherited or explicitly resolved directory evidence.
				analysis.Incomplete = true
			}
			if payload.cwdSet {
				childRequest.SegmentCWD = payload.cwd
				known := payload.cwdKnown
				childRequest.SegmentCWDKnown = &known
				childRequest.SegmentCWDUnknown = nil
				childRequest.SegmentCWDCandidates = nil
				childRequest.SegmentCWDPhysical = ""
				childRequest.SegmentCWDPhysicalCandidates = nil
			}
			if len(payload.argv) > 0 {
				record := ShellCommandRecord{
					CWD: childRequest.SegmentCWD, CWDKnown: *childRequest.SegmentCWDKnown,
					CWDCandidates: append([]string{}, childRequest.SegmentCWDCandidates...),
					Reachability:  state.reachability, Segment: index + 1,
				}
				appendShellArguments(&record, payload.environment)
				appendShellArguments(&record, payload.argv)
				analysis.Commands = append(analysis.Commands, record)
				continue
			}
			if payload.stdin {
				input, found := matchingShellInput(parsed.projection, current)
				if !found {
					needed = true
					analysis.Incomplete = true
					continue
				}
				analysis.Incomplete = analysis.Incomplete || !input.Literal
				payload.command = input.Command
				payload.commandDynamic = !input.Literal
			}
			childRequest.Command = payload.command
			if payload.launchState != nil {
				childRequest.ShellExpansion = *payload.launchState
			}
			childRequest.ShellEnvironment = payload.environment
			childRequest.ShellInputDynamic = request.ShellInputDynamic || payload.commandDynamic
			result := Classify(childRequest)
			if result.Decision == DecisionDeny {
				return nil, &result
			}
			needed = needed || payload.commandDynamic || result.Decision != DecisionAllow
			appendShellAnalysis(analysis, result.ShellAnalysis)
			continue
		}
		if len(payload.arguments) == 0 {
			for _, argument := range effect.argv {
				// A failed AST decode supplies no proof that expansion-shaped
				// values are literal filesystem targets.
				literal := !strings.ContainsAny(argument.value, "$`*?[~")
				payload.arguments = append(payload.arguments, shellArgument{Value: argument.value, Literal: literal, CardinalityUnknown: !literal})
			}
			payload.environment = request.ShellEnvironment
		}
		record := ShellCommandRecord{
			CWD: state.cwd.cwd, CWDKnown: state.cwd.known,
			CWDCandidates: append([]string{}, state.cwd.candidates...),
			Reachability:  state.reachability, Segment: index + 1,
		}
		appendShellArguments(&record, payload.environment)
		appendShellArguments(&record, payload.arguments)
		analysis.Commands = append(analysis.Commands, record)
	}
	if !needed {
		return nil, nil
	}
	if request.ShellInputDynamic {
		markShellInputUnknown(analysis)
	}
	return analysis, nil
}

// markShellInputUnknown keeps markers from expanded script text out of literal
// target resolution. Generated quoting does not establish original word bounds.
//
// Example: a parent expansion inserted between child single quotes stays unknown.
func markShellInputUnknown(analysis *ShellAnalysis) {
	if analysis == nil {
		return
	}
	analysis.Incomplete = true
	for recordIndex := range analysis.Commands {
		record := &analysis.Commands[recordIndex]
		for index, value := range record.Argv {
			if !strings.Contains(value, "$?") {
				continue
			}
			if !slices.Contains(record.UnknownArguments, index) {
				record.UnknownArguments = append(record.UnknownArguments, index)
			}
			if !slices.Contains(record.UnknownCardinalityArguments, index) {
				record.UnknownCardinalityArguments = append(record.UnknownCardinalityArguments, index)
			}
		}
	}
}

// appendShellArguments publishes words and the indices whose values are unknown.
//
// Example: a dynamic -C operand remains distinguishable from a literal directory.
func appendShellArguments(
	record *ShellCommandRecord,
	arguments []shellArgument,
) {
	for _, argument := range arguments {
		if !argument.Literal {
			record.UnknownArguments = append(record.UnknownArguments, len(record.Argv))
		}
		if argument.MayDisappear {
			record.MayDisappearArguments = append(record.MayDisappearArguments, len(record.Argv))
		}
		if argument.MayMultiply {
			record.MayMultiplyArguments = append(record.MayMultiplyArguments, len(record.Argv))
		}
		if argument.CardinalityUnknown {
			record.UnknownCardinalityArguments = append(record.UnknownCardinalityArguments, len(record.Argv))
		}
		record.Argv = append(record.Argv, argument.Value)
	}
}

// matchingShellInput finds the inline stdin belonging to this source segment.
//
// Example: a shell header receives its own heredoc, not a later command's input.
func matchingShellInput(
	projection *shellProjection,
	current segment,
) (shellInput, bool) {
	if projection == nil {
		return shellInput{}, false
	}
	for _, input := range projection.Inputs {
		if input.OwnerOffset >= current.sourceStart && input.OwnerOffset < current.sourceStart+len(current.command) {
			return input, true
		}
	}
	return shellInput{}, false
}

// shellChildRequest isolates child directory transitions while inheriting the
// verified incoming context of the statement that expands the redirection.
//
// Example: cd other <<EOF expands its body before cd can change directories.
func shellChildRequest(
	request Request,
	state compoundSegmentState,
) Request {
	child := request
	child.ShellDepth++
	child.SegmentCWD = state.cwd.cwd
	child.SegmentCWDKnown = &state.cwd.known
	child.SegmentCWDUnknown = &state.cwd.unknown
	child.SegmentCWDCandidates = append([]string{}, state.cwd.candidates...)
	child.SegmentCWDPhysical = state.cwd.physical
	child.SegmentCWDPhysicalCandidates = append([]string{}, state.cwd.physicalCandidates...)
	child.TimeoutReplay = false
	child.TimeoutReplays = nil
	return child
}

// appendShellAnalysis combines independent child records without sharing their
// directory transitions with the parent or another substitution.
//
// Example: two substitutions retain two separate initial directory states.
func appendShellAnalysis(
	analysis *ShellAnalysis,
	child *ShellAnalysis,
) {
	if child == nil {
		analysis.Incomplete = true
		return
	}
	analysis.Commands = append(analysis.Commands, child.Commands...)
	analysis.Incomplete = analysis.Incomplete || child.Incomplete
	analysis.OutputWrites = analysis.OutputWrites || child.OutputWrites
}

// literalShellPayload finds source-backed command text that a known wrapper
// passes to a nested shell. Shell words are decoded from mvdan's syntax AST;
// dynamic values retain uncertainty beside the visible command arguments.
//
// Example: bash -c 'git status' selects the quoted command for recursion.
func literalShellPayload(
	argv []token,
	source string,
	environment []shellArgument,
	cwd string,
	cwdKnown bool,
	facts shellExpansionFacts,
) shellPayload {
	if len(argv) == 0 {
		return shellPayload{}
	}
	_, commandArgv := splitLeadingAssignments(argv)
	if len(commandArgv) == 0 {
		return shellPayload{}
	}
	base := filepath.Base(commandArgv[0].value)
	possible := base == "eval" || base == "env" || base == "command" || base == "builtin" || base == "exec" || isShellName(base)
	call, ok := sourceBackedShellArgv(source, argv, facts)
	if !ok {
		if possible {
			return shellPayload{handled: true, incomplete: true}
		}
		return shellPayload{}
	}
	inherited := append(append([]shellArgument{}, environment...), call.Environment...)
	payload := literalShellPayloadWords(call.Argv, inherited, cwd, cwdKnown, 0)
	payload.arguments = call.Argv
	payload.contextUnknown = payload.contextUnknown || call.Dynamic
	payload.substitutions = call.Substitutions
	return payload
}

// sourceBackedShellArgv decodes executable arguments and prefix assignments
// separately, so only the actual command selects launcher semantics.
//
// Example: LC_ALL=C bash -s keeps LC_ALL in the environment and selects bash.
func sourceBackedShellArgv(
	source string,
	argv []token,
	facts shellExpansionFacts,
) (shellSourceCall, bool) {
	parser := syntax.NewParser()
	file, err := parser.Parse(strings.NewReader(source), "")
	if err != nil || file == nil || len(file.Stmts) != 1 {
		return shellSourceCall{}, false
	}
	call, ok := file.Stmts[0].Cmd.(*syntax.CallExpr)
	if !ok || call == nil {
		return shellSourceCall{}, false
	}
	decoded := shellSourceCall{}
	extractor := shellSubstitutionCollector{Source: source}
	syntax.Walk(call, extractor.Collect)
	decoded.Substitutions = extractor.Substitutions
	decoded.Dynamic = len(extractor.Errors) > 0
	for _, assignment := range call.Assigns {
		if assignment == nil || assignment.Name == nil {
			return shellSourceCall{}, false
		}
		value := ""
		literal := true
		if assignment.Value != nil {
			value, literal = literalSyntaxWord(source, assignment.Value)
			if !literal {
				var err error
				value, err = shellArgumentValue(source, assignment.Value.Parts, false)
				if err != nil {
					return shellSourceCall{}, false
				}
				decoded.Dynamic = true
			}
		}
		decoded.Environment = append(decoded.Environment, shellArgument{Value: assignment.Name.Value + "=" + value, Literal: literal})
	}
	for _, word := range call.Args {
		value, literal := literalSyntaxWord(source, word)
		if !literal {
			var err error
			value, err = shellArgumentValue(source, word.Parts, false)
			if err != nil {
				return shellSourceCall{}, false
			}
			decoded.Dynamic = true
		}
		argument := shellArgument{Value: value, Literal: literal}
		argument.MayDisappear, argument.MayMultiply, argument.CardinalityUnknown = shellWordCardinality(word.Parts, false)
		if shellExactScalar(word, facts.exactScalars) {
			argument.MayDisappear = false
			argument.MayMultiply = false
			argument.CardinalityUnknown = false
		}
		if facts.nullglob && shellLiteralGlob(word) {
			argument.Literal = false
			argument.MayDisappear = true
			argument.MayMultiply = true
			argument.CardinalityUnknown = false
			decoded.Dynamic = true
		}
		decoded.Argv = append(decoded.Argv, argument)
	}
	if len(decoded.Argv)+len(decoded.Environment) != len(argv) {
		return shellSourceCall{}, false
	}
	return decoded, true
}

// shellOptionState distinguishes explicit option evidence from missing state.
//
// Example: an omitted -f is unknown, while +f establishes pathname expansion.
type shellOptionState uint8

const (
	// shellOptionUnknown carries no supported proof of the effective setting.
	shellOptionUnknown shellOptionState = iota
	// shellOptionOff records an explicitly disabled effective option.
	shellOptionOff
	// shellOptionOn records an explicitly enabled effective option.
	shellOptionOn
)

// shellExpansionState carries supported shell options and persistent attribute
// uncertainty into child scopes without inferring defaults from missing metadata.
//
// Example: a command substitution inherits source-established noglob state.
type shellExpansionState struct {
	nullglob          shellOptionState
	failglob          shellOptionState
	noglob            shellOptionState
	attributesUnknown bool
	shadowedCommands  []string
}

// invalidateOptionsAndAttributes drops expansion proofs while retaining known
// function shadows. Later option assignments cannot restore builtin resolution.
//
// Example: shopt can establish nullglob after a function declaration, but a
// later call to that function still cannot borrow builtin option preservation.
func (state *shellExpansionState) invalidateOptionsAndAttributes() {
	state.nullglob = shellOptionUnknown
	state.failglob = shellOptionUnknown
	state.noglob = shellOptionUnknown
	state.attributesUnknown = true
}

// shellFunctionDeclarations collects names that may shadow ordinary commands
// in the current scope, without executing or interpreting function bodies.
//
// Example: a conditional definition of true invalidates later builtin proofs
// for true, while a definition inside a substitution stays in that child scope.
type shellFunctionDeclarations struct {
	Names []string
}

// Visit retains source-visible function names without changing earlier state
// snapshots or carrying child-process definitions into their parent scope.
//
// Example: true() { shopt -s failglob; } records true without reading its body.
func (declarations *shellFunctionDeclarations) Visit(node syntax.Node) bool {
	switch node := node.(type) {
	case nil, *syntax.CmdSubst, *syntax.ProcSubst, *syntax.Subshell:
		return false
	case *syntax.Stmt:
		return !node.Background && !node.Coprocess
	case *syntax.FuncDecl:
		if node.Name != nil && !slices.Contains(declarations.Names, node.Name.Value) {
			declarations.Names = append(slices.Clone(declarations.Names), node.Name.Value)
		}
		return false
	default:
		return true
	}
}

// shellExpansionFacts carries only source-established expansion bounds.
// Scalar values are not substituted into commands or treated as resolved paths.
//
// Example: explicit IFS plus msg=foo proves one field for a later plain $msg.
type shellExpansionFacts struct {
	state        shellExpansionState
	nullglob     bool
	exactScalars map[string]bool
}

// shellExpansionSegments tracks direct shell options and assignment-only scalar
// bindings within a simple sequential scope. Compound scopes retain incoming
// options only when every command preserves them; no inherited IFS, variable
// values, or conditional effects are assumed.
//
// Example: unset IFS; msg=foo; git commit -m $msg -a retains the -a option role.
func shellExpansionSegments(
	source string,
	segments []segment,
	incoming shellExpansionState,
) []shellExpansionFacts {
	result := make([]shellExpansionFacts, len(segments))
	file, err := syntax.NewParser().Parse(strings.NewReader(source), "")
	if err != nil || file == nil {
		return result
	}
	state := incoming
	bindings := map[string]string{}
	ifs, ifsKnown := "", false
	for _, statement := range file.Stmts {
		declarations := shellFunctionDeclarations{Names: state.shadowedCommands}
		syntax.Walk(statement, declarations.Visit)
		state.shadowedCommands = declarations.Names
		// A rejected whole-scope proof still carries command-resolution
		// uncertainty into separately inspected executable child payloads.
		for index, current := range segments {
			if current.sourceStart < int(statement.End().Offset()) && current.sourceStart+len(current.command) > int(statement.Pos().Offset()) {
				result[index].state.shadowedCommands = state.shadowedCommands
			}
		}
		call, simple := statement.Cmd.(*syntax.CallExpr)
		if !simple || len(call.Args) == 0 && len(call.Assigns) == 0 {
			proof := shellOptionPreservation{
				Source: source, Preserved: true, ShadowedCommands: state.shadowedCommands,
			}
			syntax.Walk(statement, proof.Visit)
			state.attributesUnknown = true
			if !proof.Preserved {
				state.invalidateOptionsAndAttributes()
			}
			// No branch effects are inferred. Only a whole-scope invariant can
			// carry the same incoming option facts to its executable descendants.
			for _, offset := range proof.Offsets {
				for index, current := range segments {
					if offset >= current.sourceStart && offset < current.sourceStart+len(current.command) {
						result[index] = shellExpansionFacts{
							state:    state,
							nullglob: state.nullglob == shellOptionOn && state.failglob == shellOptionOff && state.noglob == shellOptionOff,
						}
					}
				}
			}
			clear(bindings)
			ifsKnown = false
			continue
		}
		exactScalars := map[string]bool{}
		if ifsKnown && !state.attributesUnknown {
			for name, value := range bindings {
				if value != "" && !strings.ContainsAny(value, ifs) && !strings.ContainsAny(value, "*?[") {
					exactScalars[name] = true
				}
			}
		}
		offset := int(call.Pos().Offset())
		for index, current := range segments {
			if offset >= current.sourceStart && offset < current.sourceStart+len(current.command) {
				result[index] = shellExpansionFacts{
					state:        state,
					nullglob:     state.nullglob == shellOptionOn && state.failglob == shellOptionOff && state.noglob == shellOptionOff,
					exactScalars: exactScalars,
				}
			}
		}
		if statement.Background || statement.Coprocess {
			continue
		}
		if len(call.Args) == 0 && len(statement.Redirs) == 0 && !statement.Negated {
			for _, assignment := range call.Assigns {
				if assignment.Name == nil || assignment.Append || assignment.Naked || assignment.Index != nil || assignment.Array != nil {
					clear(bindings)
					ifsKnown = false
					break
				}
				value, literal := "", true
				if assignment.Value != nil {
					value, literal = literalSyntaxWord(source, assignment.Value)
				}
				if !literal {
					clear(bindings)
					ifsKnown = false
					break
				}
				bindings[assignment.Name.Value] = value
				if assignment.Name.Value == "IFS" {
					ifs, ifsKnown = value, true
				}
			}
			continue
		}
		// Facts are used by the current invocation only. An unmodelled command
		// may modify shell variables, so it cannot carry scalar proof onward.
		clear(bindings)
		ifsKnown = false
		if len(call.Args) == 0 {
			state.invalidateOptionsAndAttributes()
			continue
		}
		name, literal := literalSyntaxWord(source, call.Args[0])
		if !literal || slices.Contains(state.shadowedCommands, name) {
			state.invalidateOptionsAndAttributes()
			continue
		}
		values := make([]string, len(call.Args))
		for index, word := range call.Args {
			value, known := literalSyntaxWord(source, word)
			values[index] = value
			literal = literal && known
		}
		if literal && name == "unset" && len(values) == 2 && values[1] == "IFS" && len(statement.Redirs) == 0 && len(call.Assigns) == 0 && !statement.Negated {
			ifs, ifsKnown = " \t\n", true
			continue
		}
		switch name {
		case "shopt", "set":
			if len(statement.Redirs) != 0 || len(call.Assigns) != 0 {
				state.invalidateOptionsAndAttributes()
				continue
			}
			switch {
			case literal && name == "shopt" && len(values) == 3 && (values[1] == "-s" || values[1] == "-u"):
				switch values[2] {
				case "nullglob":
					state.nullglob = shellOptionOff
					if values[1] == "-s" {
						state.nullglob = shellOptionOn
					}
				case "failglob":
					state.failglob = shellOptionOff
					if values[1] == "-s" {
						state.failglob = shellOptionOn
					}
				default:
					state.invalidateOptionsAndAttributes()
				}
			case literal && name == "set" && len(values) == 2 && (values[1] == "-f" || values[1] == "+f"):
				state.noglob = shellOptionOff
				if values[1] == "-f" {
					state.noglob = shellOptionOn
				}
			case literal && name == "set" && len(values) == 3 && (values[1] == "-o" || values[1] == "+o") && values[2] == "noglob":
				state.noglob = shellOptionOff
				if values[1] == "-o" {
					state.noglob = shellOptionOn
				}
			default:
				state.invalidateOptionsAndAttributes()
			}
		case "declare", "typeset", "local", "readonly", "export", "nameref", "let", "read", "mapfile", "readarray":
			state.attributesUnknown = true
			if !literal || !shellVariableBuiltinPreservesOptions(values) {
				state.invalidateOptionsAndAttributes()
			}
		case "eval", ".", "source", "builtin", "command", "exec":
			state.invalidateOptionsAndAttributes()
		}
	}
	return result
}

// shellOptionPreservation proves a whole supported statement leaves shell
// options unchanged. It records executable offsets only for transporting the
// same incoming facts, without deriving branch, loop, or assignment outcomes.
//
// Example: true && git add -A -- file* preserves incoming glob facts; a set
// command anywhere in that statement prevents this invariant from being used.
type shellOptionPreservation struct {
	Source           string
	Preserved        bool
	Offsets          []int
	ShadowedCommands []string
}

// Visit admits only supported commands whose parent-shell option effects are
// known. Substitutions execute in child scopes and receive separate analysis.
//
// Example: a literal declaration taints variables but cannot turn on failglob.
func (proof *shellOptionPreservation) Visit(node syntax.Node) bool {
	if !proof.Preserved || node == nil {
		return false
	}
	switch node := node.(type) {
	case *syntax.CmdSubst, *syntax.ProcSubst:
		return false
	case *syntax.CallExpr:
		proof.Offsets = append(proof.Offsets, int(node.Pos().Offset()))
		if len(node.Args) == 0 {
			return true
		}
		name, literal := literalSyntaxWord(proof.Source, node.Args[0])
		if !literal || slices.Contains(proof.ShadowedCommands, name) {
			proof.Preserved = false
			return false
		}
		switch name {
		case "git", "true", "false", ":", "printf", "echo", "test", "[", "env":
			return true
		case "read", "mapfile", "readarray", "let":
			values := make([]string, len(node.Args))
			for index, word := range node.Args {
				var known bool
				values[index], known = literalSyntaxWord(proof.Source, word)
				if !known {
					proof.Preserved = false
					return false
				}
			}
			proof.Preserved = shellVariableBuiltinPreservesOptions(values)
		default:
			proof.Preserved = false
		}
	case *syntax.DeclClause:
		proof.Offsets = append(proof.Offsets, int(node.Pos().Offset()))
		// Unshadowed declaration values and attributes do not change shell
		// options. Scalar uncertainty is retained by the scope tracker.
		proof.Preserved = node.Variant != nil && !slices.Contains(proof.ShadowedCommands, node.Variant.Value)
	case *syntax.LetClause:
		proof.Offsets = append(proof.Offsets, int(node.Pos().Offset()))
		proof.Preserved = !slices.Contains(proof.ShadowedCommands, "let")
	case *syntax.BinaryCmd, *syntax.IfClause, *syntax.Block, *syntax.Subshell:
		// Inspect every arm before retaining any incoming option fact.
	case syntax.Command:
		proof.Preserved = false
	}
	return proof.Preserved
}

// shellVariableBuiltinPreservesOptions recognizes bounded variable-only
// builtin forms. Callback-bearing or unsupported forms cannot borrow this
// proof even when their ordinary purpose is reading or assigning variables.
//
// Example: read -r answer preserves options; mapfile -C callback does not.
func shellVariableBuiltinPreservesOptions(values []string) bool {
	if len(values) == 0 {
		return false
	}
	switch values[0] {
	case "declare", "typeset", "local", "readonly", "export", "nameref", "let":
		return true
	case "read":
		for _, value := range values[1:] {
			if value != "-r" && value != "--" && !isIdentifier(value) {
				return false
			}
		}
		return true
	case "mapfile", "readarray":
		for index := 1; index < len(values); index++ {
			value := values[index]
			if value == "--" {
				return index+1 == len(values) || index+2 == len(values) && isIdentifier(values[index+1])
			}
			if !strings.HasPrefix(value, "-") {
				return index+1 == len(values) && isIdentifier(value)
			}
			if len(value) < 2 {
				return false
			}
			for offset := 1; offset < len(value); offset++ {
				switch value[offset] {
				case 't':
				case 'd', 'n', 'O', 's', 'u', 'c':
					if offset+1 == len(value) {
						index++
						if index >= len(values) {
							return false
						}
					}
					offset = len(value)
				default:
					return false
				}
			}
		}
		return true
	default:
		return false
	}
}

// shellExactScalar recognizes a plain unquoted scalar with proven one-field
// cardinality. Parameter modifiers, arrays, concatenation, and quoting retain
// their existing independent analysis rather than borrowing this scalar fact.
//
// Example: $msg and ${msg} can use a preceding msg=foo binding plus explicit IFS.
func shellExactScalar(
	word *syntax.Word,
	exactScalars map[string]bool,
) bool {
	if len(word.Parts) != 1 {
		return false
	}
	parameter, ok := word.Parts[0].(*syntax.ParamExp)
	if !ok || parameter.Param == nil || parameter.Excl || parameter.Length || parameter.Width || parameter.Index != nil || parameter.Slice != nil || parameter.Repl != nil || parameter.Names != 0 || parameter.Exp != nil {
		return false
	}
	return exactScalars[parameter.Param.Value]
}

// shellLiteralGlob recognizes valid unquoted literal pathname patterns without
// looking at the filesystem. Quoted, dynamic, or invalid patterns stay advisory.
//
// Example: file* is eligible, while a quoted star or an unmatched bracket is not.
func shellLiteralGlob(word *syntax.Word) bool {
	var value strings.Builder
	for _, part := range word.Parts {
		literal, ok := part.(*syntax.Lit)
		if !ok {
			return false
		}
		value.WriteString(literal.Value)
	}
	text := value.String()
	if !pattern.HasMeta(text, 0) {
		return false
	}
	_, err := pattern.Regexp(text, pattern.Filenames)
	return err == nil
}

// shellWordCardinality retains source-established field-count bounds without
// evaluating parameters or substitutions. Quoting preserves empty scalar fields;
// literal text anchors a field even when an adjacent unquoted expansion vanishes.
//
// Example: $path may vanish or split, pre$path cannot vanish, and "$path" stays one.
func shellWordCardinality(
	parts []syntax.WordPart,
	quoted bool,
) (mayDisappear bool, mayMultiply bool, unknown bool) {
	guaranteed := false
	for _, part := range parts {
		switch part := part.(type) {
		case *syntax.Lit:
			guaranteed = guaranteed || part.Value != ""
			unknown = unknown || !quoted && strings.ContainsAny(part.Value, "*?[")
		case *syntax.SglQuoted:
			guaranteed = true
		case *syntax.DblQuoted:
			disappears, multiplies, unavailable := shellWordCardinality(part.Parts, true)
			guaranteed = guaranteed || len(part.Parts) == 0 || !disappears
			mayMultiply = mayMultiply || multiplies
			unknown = unknown || unavailable
		case *syntax.ParamExp:
			if !part.Excl && !part.Width && part.Slice == nil && part.Repl == nil && part.Exp == nil && part.Names == 0 &&
				(part.Length || part.Index == nil && part.Param != nil && strings.Contains("?#$", part.Param.Value) && len(part.Param.Value) == 1) {
				// Numeric output cannot vanish from an empty scalar value.
				// Without IFS evidence, unquoted field splitting is unavailable.
				guaranteed = true
				unknown = unknown || !quoted
				continue
			}
			list := part.Param != nil && part.Param.Value == "@" && !part.Length
			list = list || part.Excl && part.Names == syntax.NamesPrefixWords
			if index, ok := part.Index.(*syntax.Word); ok && !part.Length {
				list = list || index.Lit() == "@"
			}
			if quoted && !list {
				guaranteed = true
			} else {
				mayMultiply = true
			}
		case *syntax.CmdSubst:
			guaranteed = guaranteed || quoted
			mayMultiply = mayMultiply || !quoted
		case *syntax.ArithmExp:
			guaranteed = true
			unknown = unknown || !quoted
		case *syntax.ProcSubst:
			guaranteed = true
		default:
			unknown = true
		}
	}
	return !guaranteed, mayMultiply, unknown
}

// shellArgumentValue removes quoting and replaces each expansion with one
// unknown marker. Offset-preserving masks belong to source analysis; padding
// them into decoded values would invent separators during env split parsing.
//
// Example: --${OPTION}=value remains one unknown option word, --$?=value.
func shellArgumentValue(
	source string,
	parts []syntax.WordPart,
	insideQuotes bool,
) (string, error) {
	var value strings.Builder
	for _, part := range parts {
		switch part := part.(type) {
		case *syntax.ParamExp, *syntax.CmdSubst, *syntax.ArithmExp, *syntax.ProcSubst:
			value.WriteString("$?")
		case *syntax.DblQuoted:
			decoded, err := shellArgumentValue(source, part.Parts, true)
			if err != nil {
				return "", err
			}
			value.WriteString(decoded)
		default:
			decoded, _, err := shellHeredocDelimiter(source, []syntax.WordPart{part}, insideQuotes)
			if err != nil {
				return "", err
			}
			value.WriteString(decoded)
		}
	}
	return value.String(), nil
}

// literalSyntaxWord removes quotes only after every word part is proven static.
//
// Example: a quoted command string is decoded without evaluating a variable.
func literalSyntaxWord(
	source string,
	word *syntax.Word,
) (string, bool) {
	if word == nil {
		return "", false
	}
	for _, part := range word.Parts {
		_, ok := literalSyntaxPart(part, false)
		if !ok {
			return "", false
		}
	}
	value, _, err := shellHeredocDelimiter(source, word.Parts, false)
	return value, err == nil
}

// literalSyntaxPart recognizes literals whose value requires no shell expansion.
//
// Example: quoted text is static, while parameter expansion remains unresolved.
func literalSyntaxPart(
	part syntax.WordPart,
	quoted bool,
) (string, bool) {
	switch part := part.(type) {
	case *syntax.Lit:
		if !quoted && strings.ContainsAny(part.Value, "*?[") {
			return "", false
		}
		if !quoted && strings.HasPrefix(part.Value, "~") {
			return "", false
		}
		return part.Value, true
	case *syntax.SglQuoted:
		// ANSI-C quoting is static; shellHeredocDelimiter decodes its escapes.
		return part.Value, true
	case *syntax.DblQuoted:
		if part.Dollar {
			return "", false
		}
		var value strings.Builder
		for _, nested := range part.Parts {
			text, ok := literalSyntaxPart(nested, true)
			if !ok {
				return "", false
			}
			value.WriteString(text)
		}
		return value.String(), true
	default:
		return "", false
	}
}

// literalShellPayloadWords follows supported literal launchers to a shell child.
//
// Example: command env bash -c selects the same payload with inherited context.
func literalShellPayloadWords(
	argv []shellArgument,
	environment []shellArgument,
	cwd string,
	cwdKnown bool,
	depth int,
) shellPayload {
	if len(argv) == 0 || depth >= maxWrapperDepth {
		return shellPayload{}
	}
	if !argv[0].Literal {
		return shellPayload{environment: environment, contextUnknown: true}
	}
	name := filepath.Base(argv[0].Value)
	switch name {
	case "eval":
		if len(argv) < 2 {
			return shellPayload{handled: true, incomplete: true}
		}
		var parts []string
		for _, argument := range argv[1:] {
			parts = append(parts, argument.Value)
		}
		dynamic := slices.ContainsFunc(argv[1:],
			// A nonliteral eval operand inserts unknown bytes into child syntax.
			//
			// Example: eval "$code" cannot prove quoting in the expanded program.
			func(argument shellArgument) bool { return !argument.Literal })
		return shellPayload{command: strings.Join(parts, " "), commandDynamic: dynamic, environment: environment, handled: true}
	case "command", "builtin", "exec":
		index := 1
		for index < len(argv) && strings.HasPrefix(argv[index].Value, "-") {
			if !argv[index].singleField() {
				return shellPayload{handled: true, incomplete: true}
			}
			if argv[index].Value == "--" {
				index++
				break
			}
			if name == "exec" && argv[index].Value == "-a" {
				if index+1 >= len(argv) {
					return shellPayload{handled: true, incomplete: true}
				}
				if !argv[index+1].singleField() {
					return shellPayload{handled: true, incomplete: true}
				}
				index += 2
				continue
			}
			index++
		}
		return literalShellPayloadWords(argv[index:], environment, cwd, cwdKnown, depth+1)
	case "env":
		return literalEnvShellPayload(argv, environment, cwd, cwdKnown, depth+1)
	case "bash", "sh", "dash", "zsh":
		payload := literalShellInvocation(argv)
		payload.environment = environment
		return payload
	default:
		return shellPayload{environment: environment}
	}
}

// literalEnvShellPayload resolves env options before selecting its child argv.
//
// Example: env -C repo -S 'sh -s' changes the directory before shell startup.
func literalEnvShellPayload(
	argv []shellArgument,
	environment []shellArgument,
	cwd string,
	cwdKnown bool,
	depth int,
) shellPayload {
	if depth >= maxWrapperDepth {
		return shellPayload{handled: true, incomplete: true}
	}
	values := append([]shellArgument(nil), argv...)
	hasSplit := false
	if len(values) < 2 {
		return shellPayload{}
	}
	index := 1
	splits := 0
	assignments := append([]shellArgument{}, environment...)
	contextUnknown := false
	directory, directorySet := shellArgument{}, false
	nullOutput := false
	for index < len(values) {
		value := values[index].Value
		if !values[index].singleField() {
			return shellPayload{handled: true, incomplete: true}
		}
		if len(value) > 2 && value[0] == '-' && value[1] != '-' {
			// Consume short options at their actual cursor: no-operand
			// flags leave a group remainder; value options own that remainder.
			var remainder string
			switch value[1] {
			case 'i', 'v', '0':
				remainder = "-" + value[2:]
			case 'C', 'S', 'u', 'a':
				remainder = value[2:]
			}
			if remainder != "" {
				suffix := values[index]
				suffix.Value = remainder
				parts := []shellArgument{{Value: value[:2], Literal: true}, suffix}
				values = append(append(append([]shellArgument{}, values[:index]...), parts...), values[index+1:]...)
				value = values[index].Value
			}
		}
		switch {
		case value == "--":
			index++
			goto optionsDone
		case value == "-i" || value == "--ignore-environment" || value == "-":
			assignments = nil
			contextUnknown = true
			index++
		case value == "--help" || value == "--version":
			return shellPayload{handled: true, noExecution: true}
		case value == "-0" || value == "--null":
			nullOutput = true
			index++
		case value == "-v" || value == "--debug":
			index++
		case value == "-a" || value == "--argv0":
			if index+1 >= len(values) || !values[index+1].singleField() {
				return shellPayload{handled: true, incomplete: true}
			}
			index += 2
		case strings.HasPrefix(value, "--argv0="):
			index++
		case value == "-S" || value == "--split-string" ||
			strings.HasPrefix(value, "--split-string=") || strings.HasPrefix(value, "-S"):
			// Split only the current option. A preceding option may consume
			// a spelling such as -S as its operand, and -- ends option parsing.
			if splits >= maxWrapperDepth {
				return shellPayload{handled: true, incomplete: true}
			}
			split, consumed := "", 1
			splitLiteral := values[index].Literal
			switch {
			case value == "-S" || value == "--split-string":
				if index+1 >= len(values) || !values[index+1].singleField() {
					return shellPayload{handled: true, incomplete: true}
				}
				split, consumed = values[index+1].Value, 2
				splitLiteral = values[index+1].Literal
			case strings.HasPrefix(value, "--split-string="):
				split = strings.TrimPrefix(value, "--split-string=")
			default:
				split = value[2:]
			}
			parts, ok := splitEnvString(split, splitLiteral)
			if !ok {
				return shellPayload{handled: true, incomplete: true}
			}
			values = append(append(append([]shellArgument{}, values[:index]...), parts...), values[index+consumed:]...)
			hasSplit = true
			splits++
		case value == "-C" || value == "--chdir":
			if index+1 >= len(values) || !values[index+1].singleField() {
				return shellPayload{handled: true, incomplete: true}
			}
			directory, directorySet = values[index+1], true
			index += 2
		case strings.HasPrefix(value, "--chdir="):
			directory = values[index]
			directory.Value = strings.TrimPrefix(value, "--chdir=")
			directorySet = true
			index++
		case value == "-u" || value == "--unset":
			if index+1 >= len(values) || !values[index+1].singleField() {
				return shellPayload{handled: true, incomplete: true}
			}
			if isEnvironmentContextName(values[index+1].Value) {
				contextUnknown = true
			}
			assignments = removeShellAssignment(assignments, values[index+1])
			index += 2
		case strings.HasPrefix(value, "--unset="):
			if isEnvironmentContextName(strings.TrimPrefix(value, "--unset=")) {
				contextUnknown = true
			}
			name := values[index]
			name.Value = strings.TrimPrefix(value, "--unset=")
			assignments = removeShellAssignment(assignments, name)
			index++
		case strings.HasPrefix(value, "-"):
			if hasSplit {
				return shellPayload{handled: true, incomplete: true}
			}
			return shellPayload{}
		default:
			goto optionsDone
		}
	}

optionsDone:
	if nullOutput {
		// GNU env rejects --null when a command operand is supplied.
		return shellPayload{handled: true, noExecution: true}
	}
	if directorySet {
		// env stores the final -C operand, then changes directory once.
		// Relative operands therefore resolve from the incoming directory.
		if directory.Literal {
			cwd, cwdKnown = resolveShellCWD(cwd, cwdKnown, directory.Value)
		} else {
			cwd, cwdKnown = "", false
		}
	}
	for index < len(values) {
		name, _, ok := strings.Cut(values[index].Value, "=")
		if !ok {
			break
		}
		if !values[index].singleField() {
			return shellPayload{handled: true, incomplete: true}
		}
		if isEnvironmentContextName(name) && !strings.HasPrefix(name, "GIT_") {
			contextUnknown = true
		}
		assignments = append(assignments, values[index])
		index++
	}
	if index >= len(values) {
		return shellPayload{handled: hasSplit, incomplete: hasSplit}
	}
	child := literalShellPayloadWords(values[index:], assignments, cwd, cwdKnown, depth+1)
	if !child.handled {
		child.argv = append([]shellArgument{}, values[index:]...)
	}
	child.handled = true
	child.contextUnknown = child.contextUnknown || contextUnknown
	if directorySet && !child.cwdSet {
		child.cwd = cwd
		child.cwdKnown = cwdKnown
		child.cwdSet = true
	}
	return child
}

// removeShellAssignment applies an env unset to the explicitly inherited values.
//
// Example: env -u GIT_DIR removes a prefix GIT_DIR before resolving its child.
func removeShellAssignment(
	assignments []shellArgument,
	name shellArgument,
) []shellArgument {
	var retained []shellArgument
	for _, assignment := range assignments {
		if !name.Literal {
			// An unknown unset may remove any binding. Retain its identity,
			// but do not claim the earlier value is still the effective target.
			assignment.Literal = false
			retained = append(retained, assignment)
		} else if !strings.HasPrefix(assignment.Value, name.Value+"=") {
			retained = append(retained, assignment)
		}
	}
	return retained
}

// resolveShellCWD resolves a literal chdir only from a known base or absolute path.
//
// Example: env -C subdir inherits a verified parent directory for resolution.
func resolveShellCWD(
	base string,
	baseKnown bool,
	path string,
) (string, bool) {
	if path == "" {
		return "", false
	}
	if !filepath.IsAbs(path) {
		if !baseKnown {
			return "", false
		}
		path = filepath.Join(base, path)
	}
	path = filepath.Clean(path)
	if !isVerifiedAbsoluteTimeoutCWD(path) {
		return "", false
	}
	return path, true
}

// splitEnvString decodes bounded env split-string syntax without expanding values.
// Masked dynamic spans taint only their resulting argument, not adjacent words.
//
// Example: git -C $? keeps the command literal and the directory unknown.
func splitEnvString(
	value string,
	literal bool,
) ([]shellArgument, bool) {
	var words []shellArgument
	var word strings.Builder
	quote := byte(0)
	started := false
	wordLiteral := true
	guaranteed := false
	cardinalityUnknown := false
	flush :=
		// flush appends a completed word, including a deliberately empty quoted word.
		//
		// Example: two separators after one word append that word only once.
		func() {
			if started {
				words = append(words, shellArgument{Value: word.String(), Literal: wordLiteral,
					MayDisappear: !wordLiteral && !guaranteed, MayMultiply: !wordLiteral,
					CardinalityUnknown: cardinalityUnknown})
				word.Reset()
				started = false
				wordLiteral = true
				guaranteed = false
				cardinalityUnknown = false
			}
		}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if !literal && strings.HasPrefix(value[index:], "$?") {
			word.WriteString("$?")
			wordLiteral = false
			// env reparses quote bytes supplied by the expanded string.
			// Unknown data inside its quotes cannot prove a scalar bound.
			cardinalityUnknown = cardinalityUnknown || quote != 0
			started = true
			index++
			continue
		}
		if quote == '\'' {
			guaranteed = true
			if character == '\'' {
				quote = 0
			} else if character == '\\' && index+1 < len(value) && (value[index+1] == '\\' || value[index+1] == '\'') {
				index++
				word.WriteByte(value[index])
			} else {
				word.WriteByte(character)
			}
			started = true
			continue
		}
		if character == '\\' {
			guaranteed = true
			if index+1 >= len(value) {
				return nil, false
			}
			index++
			next := value[index]
			switch next {
			case 'c':
				if quote == '"' {
					return nil, false
				}
				flush()
				return words, true
			case 'f':
				word.WriteByte('\f')
			case 'n':
				word.WriteByte('\n')
			case 'r':
				word.WriteByte('\r')
			case 't':
				word.WriteByte('\t')
			case 'v':
				word.WriteByte('\v')
			case '_':
				if quote == '"' {
					word.WriteByte(' ')
				} else {
					flush()
				}
			case '#', '$', '\\', '"', '\'':
				word.WriteByte(next)
			default:
				return nil, false
			}
			started = true
			continue
		}
		if quote == '"' {
			guaranteed = true
			if character == '"' {
				quote = 0
			} else if character == '$' {
				return nil, false
			} else {
				word.WriteByte(character)
			}
			started = true
			continue
		}
		if character == '\'' || character == '"' {
			quote = character
			started = true
			guaranteed = true
			continue
		}
		if character == '$' {
			return nil, false
		}
		if isEnvSplitWhitespace(character) {
			flush()
			continue
		}
		if character == '#' && !started {
			break
		}
		word.WriteByte(character)
		started = true
		guaranteed = true
	}
	if quote != 0 {
		return nil, false
	}
	flush()
	return words, true
}

// isEnvSplitWhitespace recognizes env split-string argument separators.
//
// Example: a tab separates unquoted arguments just as a space does.
func isEnvSplitWhitespace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\n' || value == '\r' || value == '\v' || value == '\f'
}

// isShellName identifies the shell launchers supported by child analysis.
//
// Example: bash selects shell payload semantics, while cat retains ordinary data.
func isShellName(value string) bool {
	switch value {
	case "bash", "sh", "dash", "zsh":
		return true
	default:
		return false
	}
}

// literalShellInvocation selects inline command text or stdin from shell options.
//
// Example: bash -s reads stdin, while bash script.sh leaves script-file analysis separate.
func literalShellInvocation(argv []shellArgument) shellPayload {
	commandMode, stdinMode := false, false
	noExecution, interactive := false, false
	index := 1
	for index < len(argv) {
		option := argv[index].Value
		if !argv[index].singleField() {
			return shellPayload{handled: true, incomplete: true}
		}
		if option == "--" || option == "-" {
			index++
			break
		}
		if len(option) < 2 || (option[0] != '-' && option[0] != '+') {
			break
		}
		if option == "--rcfile" || option == "--init-file" {
			if index+1 >= len(argv) || !argv[index+1].singleField() {
				return shellPayload{handled: true, incomplete: true}
			}
			index += 2
			continue
		}
		if option == "--help" || option == "--version" {
			return shellPayload{handled: true, noExecution: true}
		}
		if strings.HasPrefix(option, "--") {
			index++
			continue
		}
		for _, flag := range option[1:] {
			switch flag {
			case 'c':
				commandMode = true
			case 's':
				stdinMode = true
			case 'n':
				noExecution = option[0] == '-'
			case 'i':
				interactive = option[0] == '-'
			case 'o', 'O':
				if index+1 < len(argv) {
					index++
					if !argv[index].singleField() {
						return shellPayload{handled: true, incomplete: true}
					}
					if !argv[index].Literal {
						continue
					}
					if flag == 'o' && argv[index].Value == "noexec" {
						noExecution = option[0] == '-'
					}
				}
			}
		}
		index++
	}
	// Startup code runs after invocation flags. This handoff has no complete
	// startup environment, so rc/profile, BASH_ENV, ENV, and remote-startup
	// execution cannot be excluded by --norc or --noprofile alone. Do not carry
	// launcher option or attribute proof across that boundary. Direct option
	// commands in the child source may establish fresh facts afterward.
	expansion := shellExpansionState{attributesUnknown: true}
	if noExecution && !interactive {
		return shellPayload{handled: true, noExecution: true}
	}
	// -c selects the first operand after all invocation options. An earlier
	// -s requests stdin only when no command string is selected.
	if commandMode {
		if index >= len(argv) || !argv[index].singleField() {
			return shellPayload{handled: true, incomplete: true}
		}
		return shellPayload{command: argv[index].Value, commandDynamic: !argv[index].Literal, launchState: &expansion, handled: true}
	}
	if !stdinMode && index < len(argv) && argv[index].Value != "-" {
		return shellPayload{}
	}
	return shellPayload{stdin: true, launchState: &expansion, handled: true}
}
