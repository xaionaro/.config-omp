package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

// statusCommitRejected is the exit status for a command the grammar refuses.
const statusCommitRejected = 1

// runApprovalCommitCommand validates one candidate `git commit` command line
// (args[0]) against the bounded approval grammar. It prints "ok" and returns
// statusOK when the command is accepted; otherwise it prints the rejection
// reason to stdout and returns statusCommitRejected. Any other argv shape is a
// usage error (statusRejected) written to stderr.
func runApprovalCommitCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	_ = stdin
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: approval-commit-command <command>")
		return statusRejected
	}
	return validateApprovalCommitCommand(args[0], stdout)
}

var approvalValueOptions = map[string]bool{
	"-m": true, "--message": true, "-F": true, "--file": true, "--author": true,
	"--date": true, "--cleanup": true, "--trailer": true, "--fixup": true,
	"--squash": true, "--reuse-message": true, "-C": true,
	"--reedit-message": true, "--template": true, "-S": true, "--gpg-sign": true,
}

var approvalFlagOptions = map[string]bool{
	"-a": true, "--all": true, "-p": true, "--patch": true, "--no-verify": true,
	"--verify": true, "--signoff": true, "--amend": true, "--allow-empty": true,
	"--allow-empty-message": true, "--edit": true, "--no-edit": true,
	"--reset-author": true, "--only": true, "--include": true,
	"--no-post-rewrite": true, "--quiet": true, "-q": true, "--verbose": true,
	"-v": true, "--status": true, "--no-status": true,
}

// hasControlCharacter reports whether s contains a code point below 32 or 127.
func hasControlCharacter(s string) bool {
	for _, r := range s {
		if r < 32 || r == 127 {
			return true
		}
	}
	return false
}

// validateApprovalCommitCommand applies the grammar in the original order and
// prints the first rejection reason, message for message.
func validateApprovalCommitCommand(command string, stdout io.Writer) int {
	reject := func(message string) int {
		fmt.Fprintln(stdout, message)
		return statusCommitRejected
	}
	if command == "" {
		return reject("command is empty")
	}
	if hasControlCharacter(command) {
		return reject("command contains control characters; tabs/newlines are not allowed")
	}
	if message := scanShellQuoting(command); message != "" {
		return reject(message)
	}
	if !matchesGitCommitPrefix(command) {
		return reject("expected a direct literal 'git commit' invocation; wrappers, assignments, and alternate Git paths are not allowed")
	}
	tokens, err := shlexSplitPosix(command)
	if err != nil {
		return reject("shell quoting is invalid: " + err.Error())
	}
	if len(tokens) < 2 || tokens[0] != "git" || tokens[1] != "commit" {
		return reject("expected command tokens ['git', 'commit']")
	}
	return validateCommitOptions(tokens, stdout)
}

// scanShellQuoting enforces the quoting rules: operators and substitution
// markers are rejected outside single quotes, substitution markers are rejected
// inside double quotes, and quoting must be balanced. It returns the rejection
// message, or "" when the scan passes.
func scanShellQuoting(command string) string {
	var quote byte
	escaped := false
	for i := range len(command) {
		c := command[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			}
			continue
		}
		if quote == '"' {
			if c == '"' {
				quote = 0
			} else if c == '$' || c == '`' {
				return "shell substitution marker is not allowed inside double quotes"
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if strings.IndexByte(";&|()<>`$", c) >= 0 {
			return "shell operator or substitution marker is not allowed"
		}
	}
	if quote != 0 || escaped {
		return "shell quoting is unbalanced"
	}
	return ""
}

// matchesGitCommitPrefix implements ^git\s+commit(?:\s|$). Python's \s agrees
// with unicode.IsSpace once control characters have been rejected.
func matchesGitCommitPrefix(command string) bool {
	rest, ok := strings.CutPrefix(command, "git")
	if !ok {
		return false
	}
	trimmed := strings.TrimLeftFunc(rest, unicode.IsSpace)
	if len(trimmed) == len(rest) {
		return false
	}
	rest, ok = strings.CutPrefix(trimmed, "commit")
	if !ok {
		return false
	}
	if rest == "" {
		return true
	}
	first, _ := utf8.DecodeRuneInString(rest)
	return unicode.IsSpace(first)
}

// shlexSplitPosix reproduces Python's shlex.split(s, posix=True) for input
// that has already passed scanShellQuoting and the control-character check.
// Only ' ' separates words; a backslash escapes the next byte outside quotes,
// and inside double quotes only \" and \\ are unescaped.
func shlexSplitPosix(s string) ([]string, error) {
	var tokens []string
	n := len(s)
	i := 0
	for i < n {
		if s[i] == ' ' {
			i++
			continue
		}
		var tok []byte
		quoted := false
		for i < n && s[i] != ' ' {
			switch c := s[i]; c {
			case '\\':
				if i+1 >= n {
					return nil, errors.New("No escaped character")
				}
				tok = append(tok, s[i+1])
				i += 2
			case '\'':
				quoted = true
				end := strings.IndexByte(s[i+1:], '\'')
				if end < 0 {
					return nil, errors.New("No closing quotation")
				}
				tok = append(tok, s[i+1:i+1+end]...)
				i += end + 2
			case '"':
				quoted = true
				i++
				for {
					if i >= n {
						return nil, errors.New("No closing quotation")
					}
					if s[i] == '"' {
						i++
						break
					}
					if s[i] == '\\' {
						if i+1 >= n {
							return nil, errors.New("No escaped character")
						}
						next := s[i+1]
						if next != '"' && next != '\\' {
							tok = append(tok, '\\')
						}
						tok = append(tok, next)
						i += 2
						continue
					}
					tok = append(tok, s[i])
					i++
				}
			default:
				tok = append(tok, c)
				i++
			}
		}
		// Python's shlex yields an empty word only when it was quoted.
		if len(tok) > 0 || quoted {
			tokens = append(tokens, string(tok))
		}
	}
	return tokens, nil
}

// validateCommitOptions walks the tokens after "git commit" and applies the
// bounded option grammar.
func validateCommitOptions(tokens []string, stdout io.Writer) int {
	reject := func(message string) int {
		fmt.Fprintln(stdout, message)
		return statusCommitRejected
	}
	index := 2
	for index < len(tokens) {
		token := tokens[index]
		if token == "--" {
			return reject("pathspec arguments are outside the bounded approval grammar; action: stage intended paths explicitly with git add -- <paths>, then rerun approval with a plain git commit -m ... and no -a/-am/pathspec")
		}
		if approvalValueOptions[token] {
			if index+1 >= len(tokens) || tokens[index+1] == "" || hasControlCharacter(tokens[index+1]) {
				return reject(fmt.Sprintf("option %s requires one bounded printable value", token))
			}
			index += 2
			continue
		}
		if name, value, hasEq := strings.Cut(token, "="); hasEq && strings.HasPrefix(name, "--") && approvalValueOptions[name] {
			if value == "" {
				return reject(fmt.Sprintf("option %s requires a non-empty value", name))
			}
			index++
			continue
		}
		if approvalFlagOptions[token] {
			index++
			continue
		}
		if token == "-am" {
			return reject("unsupported or unbounded commit option: -am; action: stage intended paths explicitly with git add -- <paths>, then rerun approval with a plain git commit -m ... and no -a/-am/pathspec")
		}
		return reject(fmt.Sprintf("unsupported or unbounded commit option: %s", token))
	}
	fmt.Fprintln(stdout, "ok")
	return statusOK
}
