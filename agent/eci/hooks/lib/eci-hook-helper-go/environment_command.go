package main

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// environmentUnrecognized is the status for a command that cannot be
// tokenized or contains no environment command. The caller treats the empty
// classification as ordinary work.
const environmentUnrecognized = 1

const (
	environmentMaxNames = 16
	environmentMaxDepth = 8
)

// environmentInterpreterNames are variables whose assignment before an env
// child would change how an interpreter or shell starts.
var environmentInterpreterNames = map[string]bool{
	"BASH_ENV": true, "BASHOPTS": true, "CDPATH": true, "ENV": true,
	"GEM_HOME": true, "GEM_PATH": true, "IFS": true, "LD_LIBRARY_PATH": true,
	"LD_PRELOAD": true, "NODE_OPTIONS": true, "NODE_PATH": true, "PATH": true,
	"PERL5LIB": true, "PERL5OPT": true, "PYTHONHOME": true, "PYTHONPATH": true,
	"PYTHONSTARTUP": true, "RUBYLIB": true, "RUBYOPT": true, "SHELLOPTS": true,
	"ZDOTDIR": true,
}

// environmentOperators are the token values that end a command segment.
var environmentOperators = map[string]bool{
	";": true, "&": true, "&&": true, "|": true, "||": true, "(": true, ")": true,
	">": true, ">>": true, ">|": true, ">&": true,
	"<": true, "<<": true, "<<<": true, "<&": true,
}

// environmentOutcome is one output line: DENY/ALLOW/WRAPPER, then four
// fields that are already in their printed form.
type environmentOutcome [6]string

var (
	errEnvironmentQuote  = errors.New("no closing quotation")
	errEnvironmentEscape = errors.New("no escaped character")
)

// runEnvironmentCommand classifies one command string (args[0]) the way the
// former Python recognizer did. It prints one tab-separated line and returns 0
// for ALLOW, WRAPPER, and DENY. It returns environmentUnrecognized (1) when
// the command cannot be tokenized or has no environment command, and 2 for
// any argument shape other than exactly one command.
func runEnvironmentCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return statusRejected
	}
	segments, ok := environmentSegments(args[0])
	if !ok {
		return environmentUnrecognized
	}
	var result environmentOutcome
	found := false
	for i, values := range segments {
		outcome, matched := inspectEnvironmentWords(values, i+1, 0, 0)
		if !matched {
			continue
		}
		if outcome[0] == "DENY" {
			return writeEnvironmentLine(stdout, outcome)
		}
		result, found = outcome, true
	}
	if !found {
		return environmentUnrecognized
	}
	if result[0] == "ALLOW" && result[1] == "printenv" && result[2] == "1" &&
		result[3] == "0" && len(segments) == 1 {
		result[5] = "direct-registered-query"
	}
	return writeEnvironmentLine(stdout, result)
}

func writeEnvironmentLine(w io.Writer, fields environmentOutcome) int {
	if _, err := io.WriteString(w, strings.Join(fields[:], "\t")+"\n"); err != nil {
		return statusFailed
	}
	return statusOK
}

// environmentSegments tokenizes command and splits the tokens at operators.
// It reports false where the former shlex call raised ValueError.
func environmentSegments(command string) ([][]string, bool) {
	lexer := &environmentLexer{src: command, pending: -1, state: ' '}
	var tokens []string
	for {
		token, ok, err := lexer.readToken()
		if err != nil {
			return nil, false
		}
		if !ok {
			break
		}
		tokens = append(tokens, token)
	}
	tokens = append(tokens, ";")
	var segments [][]string
	var current []string
	for _, token := range tokens {
		if environmentOperators[token] {
			if len(current) > 0 {
				segments = append(segments, current)
			}
			current = nil
			continue
		}
		current = append(current, token)
	}
	return segments, true
}

// environmentLexer is a byte-for-byte translation of Python's shlex in POSIX
// mode with punctuation_chars "();<>|&" and whitespace_split enabled. Every
// byte that changes the token stream is ASCII, so bytes at or above 0x80 are
// ordinary word bytes, as code points were in the Python recognizer.
type environmentLexer struct {
	src     string
	pos     int
	pending int  // one pushed-back byte, or -1
	state   byte // ' ', 'a', 'c', a quote, '\\', or envStateEnd
	token   []byte
	quoted  bool
	escaped byte
}

// envStateEnd is the state shlex reaches at end of input.
const envStateEnd byte = 0

func (l *environmentLexer) read() int {
	if l.pending >= 0 {
		c := l.pending
		l.pending = -1
		return c
	}
	if l.pos >= len(l.src) {
		return -1
	}
	c := l.src[l.pos]
	l.pos++
	return int(c)
}

func (l *environmentLexer) pushBack(b byte) {
	l.pending = int(b)
}

// skipLine consumes input through the next newline, as shlex's readline does.
func (l *environmentLexer) skipLine() {
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		l.pos++
		if c == '\n' {
			return
		}
	}
}

// readToken returns the next token. ok is false at end of input.
func (l *environmentLexer) readToken() (string, bool, error) {
	l.quoted = false
	l.escaped = ' '
	for {
		ch := l.read()
		if l.state == envStateEnd {
			l.token = nil
			break
		}
		stop, err := l.step(ch)
		if err != nil {
			return "", false, err
		}
		if stop {
			break
		}
	}
	result := string(l.token)
	l.token = nil
	if !l.quoted && result == "" {
		return "", false, nil
	}
	return result, true, nil
}

// step handles one input byte (-1 at end of input) and reports whether
// read_token should stop, mirroring the branches of shlex.read_token.
func (l *environmentLexer) step(ch int) (bool, error) {
	switch {
	case l.state == ' ':
		if ch < 0 {
			l.state = envStateEnd
			return true, nil
		}
		b := byte(ch)
		switch {
		case isEnvironmentWhitespace(b):
			return len(l.token) > 0 || l.quoted, nil
		case b == '#':
			l.skipLine()
			return false, nil
		case b == '\\':
			l.escaped = 'a'
			l.state = '\\'
			return false, nil
		case isEnvironmentPunct(b):
			l.token = append(l.token[:0], b)
			l.state = 'c'
			return false, nil
		case b == '\'' || b == '"':
			l.state = b
			return false, nil
		default:
			l.token = append(l.token[:0], b)
			l.state = 'a'
			return false, nil
		}

	case l.state == '\'' || l.state == '"':
		l.quoted = true
		if ch < 0 {
			return false, errEnvironmentQuote
		}
		b := byte(ch)
		if b == l.state {
			l.state = 'a'
			return false, nil
		}
		if l.state == '"' && b == '\\' {
			l.escaped = '"'
			l.state = '\\'
			return false, nil
		}
		l.token = append(l.token, b)
		return false, nil

	case l.state == '\\':
		if ch < 0 {
			return false, errEnvironmentEscape
		}
		b := byte(ch)
		if (l.escaped == '\'' || l.escaped == '"') && b != '\\' && b != l.escaped {
			l.token = append(l.token, '\\')
		}
		l.token = append(l.token, b)
		l.state = l.escaped
		return false, nil

	default: // 'a' and 'c'
		if ch < 0 {
			l.state = envStateEnd
			return true, nil
		}
		b := byte(ch)
		if isEnvironmentWhitespace(b) {
			l.state = ' '
			return len(l.token) > 0 || l.quoted, nil
		}
		if b == '#' {
			l.skipLine()
			l.state = ' '
			return len(l.token) > 0 || l.quoted, nil
		}
		if l.state == 'c' {
			if isEnvironmentPunct(b) {
				l.token = append(l.token, b)
				return false, nil
			}
			l.pushBack(b)
			l.state = ' '
			return true, nil
		}
		if b == '\'' || b == '"' {
			l.state = b
			return false, nil
		}
		if b == '\\' {
			l.escaped = 'a'
			l.state = '\\'
			return false, nil
		}
		if !isEnvironmentPunct(b) {
			l.token = append(l.token, b)
			return false, nil
		}
		l.pushBack(b)
		l.state = ' '
		return len(l.token) > 0 || l.quoted, nil
	}
}

func isEnvironmentWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

func isEnvironmentPunct(b byte) bool {
	return strings.IndexByte("();<>|&", b) >= 0
}

func isEnvironmentIdentByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// isEnvironmentIdentifier matches [A-Za-z_][A-Za-z0-9_]* over the whole string.
func isEnvironmentIdentifier(s string) bool {
	if s == "" || (s[0] >= '0' && s[0] <= '9') {
		return false
	}
	for i := range len(s) {
		if !isEnvironmentIdentByte(s[i]) {
			return false
		}
	}
	return true
}

// environmentAssignmentName returns the name of a leading NAME=... word.
func environmentAssignmentName(s string) (string, bool) {
	k := 0
	for k < len(s) && isEnvironmentIdentByte(s[k]) {
		k++
	}
	if k == 0 || k == len(s) || s[k] != '=' || (s[0] >= '0' && s[0] <= '9') {
		return "", false
	}
	return s[:k], true
}

// environmentShown renders a token the way Python's unicode_escape did. Bytes
// that are not valid UTF-8 are read as the surrogate-escaped code points that
// Python's argv decoding produces.
func environmentShown(value string) string {
	var out strings.Builder
	for i := 0; i < len(value); {
		r, size := utf8.DecodeRuneInString(value[i:])
		if r == utf8.RuneError && size == 1 {
			r = 0xDC00 + rune(value[i])
		}
		i += size
		switch {
		case r == '\\':
			out.WriteString(`\\`)
		case r == '\t':
			out.WriteString(`\t`)
		case r == '\n':
			out.WriteString(`\n`)
		case r == '\r':
			out.WriteString(`\r`)
		case r >= 0x20 && r < 0x7f:
			out.WriteRune(r)
		case r < 0x20 || (r >= 0x7f && r <= 0xff):
			fmt.Fprintf(&out, `\x%02x`, r)
		case r < 0x10000:
			fmt.Fprintf(&out, `\u%04x`, r)
		default:
			fmt.Fprintf(&out, `\U%08x`, r)
		}
	}
	return out.String()
}

func environmentDenied(code string, segment, index int, token, reason string) (environmentOutcome, bool) {
	return environmentOutcome{
		"DENY", code, strconv.Itoa(segment), strconv.Itoa(index),
		environmentShown(token), reason,
	}, true
}

// inspectEnvironmentWords classifies one segment. base is the absolute index
// of values[0] within the segment; ok is false when the segment is not an
// environment command.
func inspectEnvironmentWords(values []string, segment, base, depth int) (environmentOutcome, bool) {
	if len(values) == 0 || depth > environmentMaxDepth {
		return environmentOutcome{}, false
	}
	name := values[0][strings.LastIndexByte(values[0], '/')+1:]
	switch name {
	case "printenv":
		return inspectPrintenv(values[1:], segment, base)
	case "env":
		return inspectEnv(values, segment, base, depth)
	}
	return environmentOutcome{}, false
}

func inspectPrintenv(names []string, segment, base int) (environmentOutcome, bool) {
	if len(names) == 0 {
		return environmentDenied("ECI_ENVIRONMENT_ENUMERATION_DENIED", segment, base, "printenv", "no-names")
	}
	if len(names) > environmentMaxNames {
		return environmentDenied("ECI_ENVIRONMENT_ENUMERATION_DENIED", segment, base, "printenv", "too-many-names")
	}
	seen := make(map[string]bool, len(names))
	for offset, candidate := range names {
		index := base + offset + 1
		if strings.HasPrefix(candidate, "-") {
			return environmentDenied("ECI_ENVIRONMENT_OPTION_DENIED", segment, index, candidate, "unsupported-option")
		}
		if !isEnvironmentIdentifier(candidate) {
			return environmentDenied("ECI_ENVIRONMENT_ENUMERATION_DENIED", segment, index, candidate, "malformed-name")
		}
		if seen[candidate] {
			return environmentDenied("ECI_ENVIRONMENT_ENUMERATION_DENIED", segment, index, candidate, "duplicate-name")
		}
		seen[candidate] = true
	}
	return environmentOutcome{
		"ALLOW", "printenv", strconv.Itoa(segment), strconv.Itoa(base),
		"printenv", "registered-query",
	}, true
}

func inspectEnv(values []string, segment, base, depth int) (environmentOutcome, bool) {
	index := 1
	optionsDone := false
	for index < len(values) && !optionsDone {
		token := values[index]
		absolute := base + index
		switch {
		case token == "--":
			index++
			optionsDone = true
		case token == "-i" || token == "--ignore-environment":
			index++
		case token == "-S" || token == "--split-string" || strings.HasPrefix(token, "--split-string="):
			return environmentDenied("ECI_ENVIRONMENT_OPTION_DENIED", segment, absolute, token, "split-string")
		case token == "-u" || token == "--unset" || token == "-C" || token == "--chdir":
			if index+1 >= len(values) {
				return environmentDenied("ECI_ENVIRONMENT_OPTION_DENIED", segment, absolute, token, "missing-option-argument")
			}
			argument := values[index+1]
			if (token == "-u" || token == "--unset") && !isEnvironmentIdentifier(argument) {
				return environmentDenied("ECI_ENVIRONMENT_OPTION_DENIED", segment, absolute, token, "malformed-option-argument")
			}
			index += 2
		case strings.HasPrefix(token, "--unset="):
			if !isEnvironmentIdentifier(token[len("--unset="):]) {
				return environmentDenied("ECI_ENVIRONMENT_OPTION_DENIED", segment, absolute, token, "malformed-option-argument")
			}
			index++
		case strings.HasPrefix(token, "--chdir="):
			if token == "--chdir=" {
				return environmentDenied("ECI_ENVIRONMENT_OPTION_DENIED", segment, absolute, token, "missing-option-argument")
			}
			index++
		case strings.HasPrefix(token, "-"):
			return environmentDenied("ECI_ENVIRONMENT_OPTION_DENIED", segment, absolute, token, "unsupported-option")
		default:
			optionsDone = true
		}
	}

	for index < len(values) {
		assignment, ok := environmentAssignmentName(values[index])
		if !ok {
			break
		}
		absolute := base + index
		if strings.HasPrefix(assignment, "GIT_") {
			return environmentDenied("ECI_ENVIRONMENT_CONTEXT_DENIED", segment, absolute, assignment, "repository-context")
		}
		if environmentInterpreterNames[assignment] {
			return environmentDenied("ECI_ENVIRONMENT_CONTEXT_DENIED", segment, absolute, assignment, "interpreter-context")
		}
		index++
	}
	if index >= len(values) {
		return environmentDenied("ECI_ENVIRONMENT_ENUMERATION_DENIED", segment, base, "env", "no-child")
	}
	if nested, ok := inspectEnvironmentWords(values[index:], segment, base+index, depth+1); ok {
		return nested, true
	}
	return environmentOutcome{
		"WRAPPER", "env", strconv.Itoa(segment), strconv.Itoa(base),
		"env", "finite-child",
	}, true
}
