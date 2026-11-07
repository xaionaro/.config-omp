package main

import (
	"bytes"
	"strings"
	"testing"
)

func runEnvironmentForTest(t *testing.T, command string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	status := runEnvironmentCommand([]string{command}, strings.NewReader(""), &out, &out)
	return status, out.String()
}

func TestRunEnvironmentCommandClassifications(t *testing.T) {
	tests := []struct {
		name    string
		command string
		status  int
		stdout  string
	}{
		{
			name:    "direct printenv query is allowed",
			command: "printenv PATH",
			status:  statusOK,
			stdout:  "ALLOW\tprintenv\t1\t0\tprintenv\tdirect-registered-query\n",
		},
		{
			name:    "quoted printenv name is allowed",
			command: `printenv "HOME"`,
			status:  statusOK,
			stdout:  "ALLOW\tprintenv\t1\t0\tprintenv\tdirect-registered-query\n",
		},
		{
			name:    "printenv without names is denied",
			command: "printenv",
			status:  statusOK,
			stdout:  "DENY\tECI_ENVIRONMENT_ENUMERATION_DENIED\t1\t0\tprintenv\tno-names\n",
		},
		{
			name:    "printenv with too many names is denied",
			command: "printenv " + strings.TrimSpace(strings.Repeat("A ", 17)),
			status:  statusOK,
			stdout:  "DENY\tECI_ENVIRONMENT_ENUMERATION_DENIED\t1\t0\tprintenv\ttoo-many-names\n",
		},
		{
			name:    "printenv option is denied",
			command: "printenv -0 HOME",
			status:  statusOK,
			stdout:  "DENY\tECI_ENVIRONMENT_OPTION_DENIED\t1\t1\t-0\tunsupported-option\n",
		},
		{
			name:    "malformed printenv name is denied",
			command: "printenv 1X",
			status:  statusOK,
			stdout:  "DENY\tECI_ENVIRONMENT_ENUMERATION_DENIED\t1\t1\t1X\tmalformed-name\n",
		},
		{
			name:    "duplicate printenv name is denied",
			command: "printenv HOME HOME",
			status:  statusOK,
			stdout:  "DENY\tECI_ENVIRONMENT_ENUMERATION_DENIED\t1\t2\tHOME\tduplicate-name\n",
		},
		{
			name:    "non-ASCII name is shown with unicode escapes",
			command: "printenv é",
			status:  statusOK,
			stdout:  "DENY\tECI_ENVIRONMENT_ENUMERATION_DENIED\t1\t1\t\\xe9\tmalformed-name\n",
		},
		{
			name:    "control character in name is shown escaped",
			command: "printenv 'A\tB'",
			status:  statusOK,
			stdout:  "DENY\tECI_ENVIRONMENT_ENUMERATION_DENIED\t1\t1\tA\\tB\tmalformed-name\n",
		},
		{
			name:    "invalid UTF-8 name is shown surrogate-escaped",
			command: "printenv \xff",
			status:  statusOK,
			stdout:  "DENY\tECI_ENVIRONMENT_ENUMERATION_DENIED\t1\t1\t\\udcff\tmalformed-name\n",
		},
		{
			name:    "comment after query is ignored",
			command: "printenv HOME # env PATH=x ls",
			status:  statusOK,
			stdout:  "ALLOW\tprintenv\t1\t0\tprintenv\tdirect-registered-query\n",
		},
		{
			name:    "pipeline query is allowed without the direct reason",
			command: "echo a | printenv HOME",
			status:  statusOK,
			stdout:  "ALLOW\tprintenv\t2\t0\tprintenv\tregistered-query\n",
		},
		{
			name:    "later allowed segment keeps its base index",
			command: "ls && printenv A",
			status:  statusOK,
			stdout:  "ALLOW\tprintenv\t2\t0\tprintenv\tregistered-query\n",
		},
		{
			name:    "deny in a later segment wins over an earlier allow",
			command: "printenv HOME; env PATH=x ls",
			status:  statusOK,
			stdout:  "DENY\tECI_ENVIRONMENT_CONTEXT_DENIED\t2\t1\tPATH\tinterpreter-context\n",
		},
		{
			name:    "env option split-string is denied",
			command: "env -S 'printenv HOME'",
			status:  statusOK,
			stdout:  "DENY\tECI_ENVIRONMENT_OPTION_DENIED\t1\t1\t-S\tsplit-string\n",
		},
		{
			name:    "env unset option without argument is denied",
			command: "env -u",
			status:  statusOK,
			stdout:  "DENY\tECI_ENVIRONMENT_OPTION_DENIED\t1\t1\t-u\tmissing-option-argument\n",
		},
		{
			name:    "env unset option with malformed name is denied",
			command: "env -u 1A printenv",
			status:  statusOK,
			stdout:  "DENY\tECI_ENVIRONMENT_OPTION_DENIED\t1\t1\t-u\tmalformed-option-argument\n",
		},
		{
			name:    "env unset= with malformed name is denied",
			command: "env --unset=1A ls",
			status:  statusOK,
			stdout:  "DENY\tECI_ENVIRONMENT_OPTION_DENIED\t1\t1\t--unset=1A\tmalformed-option-argument\n",
		},
		{
			name:    "env chdir= with empty value is denied",
			command: "env --chdir= ls",
			status:  statusOK,
			stdout:  "DENY\tECI_ENVIRONMENT_OPTION_DENIED\t1\t1\t--chdir=\tmissing-option-argument\n",
		},
		{
			name:    "unknown env option is denied",
			command: "env -x ls",
			status:  statusOK,
			stdout:  "DENY\tECI_ENVIRONMENT_OPTION_DENIED\t1\t1\t-x\tunsupported-option\n",
		},
		{
			name:    "env without a child is denied",
			command: "env",
			status:  statusOK,
			stdout:  "DENY\tECI_ENVIRONMENT_ENUMERATION_DENIED\t1\t0\tenv\tno-child\n",
		},
		{
			name:    "env with a finite child is a wrapper",
			command: "env ls",
			status:  statusOK,
			stdout:  "WRAPPER\tenv\t1\t0\tenv\tfinite-child\n",
		},
		{
			name:    "path-qualified env is recognized by basename",
			command: "/usr/bin/env FOO=1 printenv A",
			status:  statusOK,
			stdout:  "ALLOW\tprintenv\t1\t2\tprintenv\tregistered-query\n",
		},
		{
			name:    "nested env reaches the printenv query",
			command: "env env printenv A",
			status:  statusOK,
			stdout:  "ALLOW\tprintenv\t1\t2\tprintenv\tregistered-query\n",
		},
		{
			name:    "env with ignore-environment reaches the query",
			command: "env -i FOO=1 printenv HOME",
			status:  statusOK,
			stdout:  "ALLOW\tprintenv\t1\t3\tprintenv\tregistered-query\n",
		},
		{
			name:    "interpreter variable assignment is denied",
			command: "env PATH=/x printenv HOME",
			status:  statusOK,
			stdout:  "DENY\tECI_ENVIRONMENT_CONTEXT_DENIED\t1\t1\tPATH\tinterpreter-context\n",
		},
		{
			name:    "repository variable assignment is denied",
			command: "env GIT_DIR=x ls",
			status:  statusOK,
			stdout:  "DENY\tECI_ENVIRONMENT_CONTEXT_DENIED\t1\t1\tGIT_DIR\trepository-context\n",
		},
		{
			name:    "unterminated quote is unrecognized",
			command: "echo 'unterminated",
			status:  environmentUnrecognized,
			stdout:  "",
		},
		{
			name:    "trailing backslash is unrecognized",
			command: "printenv HOME\\",
			status:  environmentUnrecognized,
			stdout:  "",
		},
		{
			name:    "ordinary command is unrecognized",
			command: "ls -la",
			status:  environmentUnrecognized,
			stdout:  "",
		},
		{
			name:    "empty command is unrecognized",
			command: "",
			status:  environmentUnrecognized,
			stdout:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, stdout := runEnvironmentForTest(t, tt.command)
			if status != tt.status {
				t.Fatalf("status = %d, want %d (stdout %q)", status, tt.status, stdout)
			}
			if stdout != tt.stdout {
				t.Fatalf("stdout = %q, want %q", stdout, tt.stdout)
			}
		})
	}
}

func TestRunEnvironmentCommandRejectsArgumentShape(t *testing.T) {
	for _, args := range [][]string{nil, {"printenv", "HOME"}} {
		var out bytes.Buffer
		status := runEnvironmentCommand(args, strings.NewReader(""), &out, &out)
		if status != statusRejected {
			t.Fatalf("args %q: status = %d, want %d", args, status, statusRejected)
		}
		if out.Len() != 0 {
			t.Fatalf("args %q: stdout = %q, want empty", args, out.String())
		}
	}
}
