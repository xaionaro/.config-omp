package main

import (
	"bytes"
	"strings"
	"testing"
)

func runApprovalCase(t *testing.T, command string) (int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	status := runApprovalCommitCommand([]string{command}, strings.NewReader(""), &stdout, &stderr)
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr for %q: %q", command, stderr.String())
	}
	return status, stdout.String()
}

func TestApprovalCommitCommandAccepts(t *testing.T) {
	for _, command := range []string{
		"git commit -m 'msg'",
		`git commit -m "fix: add thing"`,
		"git commit -m msg",
		"git commit --message=hello",
		"git commit",
		"git commit --amend --no-edit",
		"git commit -a -m x",
		"git commit -m 'a;b'",
		`git commit -m "a\"b"`,
		`git commit -m "a\\b"`,
		"git  commit   -m   x",
		"git commit -m x -m y",
	} {
		status, out := runApprovalCase(t, command)
		if status != 0 || out != "ok\n" {
			t.Errorf("%q: status=%d out=%q, want 0 and ok", command, status, out)
		}
	}
}

func TestApprovalCommitCommandRejectsWithReason(t *testing.T) {
	cases := map[string]string{
		"":                          "command is empty",
		"git\tcommit -m x":          "command contains control characters; tabs/newlines are not allowed",
		"git commit -m x\x7f":       "command contains control characters; tabs/newlines are not allowed",
		"ls -la":                    "expected a direct literal 'git commit' invocation; wrappers, assignments, and alternate Git paths are not allowed",
		"git commit -m a;b":         "shell operator or substitution marker is not allowed",
		"git commit -m $(id)":       "shell operator or substitution marker is not allowed",
		`git commit -m "$HOME"`:     "shell substitution marker is not allowed inside double quotes",
		"git commit -m 'unbalanced": "shell quoting is unbalanced",
		"git commit -m a\\":         "shell quoting is unbalanced",
		"git\u00a0commit -m x":      "expected command tokens ['git', 'commit']",
		"git commit --message=":     "option --message requires a non-empty value",
		"git commit -m ''":          "option -m requires one bounded printable value",
		"git commit -m":             "option -m requires one bounded printable value",
		"git commit -- file.txt":    "pathspec arguments are outside the bounded approval grammar; action: stage intended paths explicitly with git add -- <paths>, then rerun approval with a plain git commit -m ... and no -a/-am/pathspec",
		"git commit -am 'x'":        "unsupported or unbounded commit option: -am; action: stage intended paths explicitly with git add -- <paths>, then rerun approval with a plain git commit -m ... and no -a/-am/pathspec",
		"git commit --unknown":      "unsupported or unbounded commit option: --unknown",
	}
	for command, want := range cases {
		status, out := runApprovalCase(t, command)
		if status != 1 {
			t.Errorf("%q: status=%d, want 1", command, status)
		}
		if out != want+"\n" {
			t.Errorf("%q: out=%q, want %q", command, out, want+"\n")
		}
	}
}

func TestApprovalCommitCommandUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	status := runApprovalCommitCommand(nil, strings.NewReader(""), &stdout, &stderr)
	if status != statusRejected {
		t.Fatalf("status=%d, want %d", status, statusRejected)
	}
	if stdout.Len() != 0 || stderr.Len() == 0 {
		t.Fatalf("stdout=%q stderr=%q, want usage on stderr only", stdout.String(), stderr.String())
	}
}
