package main

import (
	"errors"
	"strconv"
)

// Exit statuses. A denial is deny text on the deny descriptor AND exitDenied; a target that exits 125
// writes no deny text. The exec statuses keep the shell's meaning: 126 cannot execute, 127 not found.
const (
	// exitDenied is a verification denial, always with one deny line on the deny descriptor.
	//
	// Example: a group-writable target exits 125 with "... (is group- or other-writable) ...".
	exitDenied = 125
	// exitCannotExec is an exec failure after verification other than ENOENT.
	//
	// Example: a verified ELF the kernel rejects (ENOEXEC) exits 126 with no deny text.
	exitCannotExec = 126
	// exitNotFound is an ENOENT exec failure after verification, such as a missing ELF interpreter.
	//
	// Example: a verified dynamic ELF whose PT_INTERP does not exist exits 127.
	exitNotFound = 127
	// exitUsage is a malformed command line or a deny descriptor that is not open.
	//
	// Example: eci-verexec with no --deny-fd exits 2.
	exitUsage = 2
)

// invocation is the parsed command line.
//
// Example: "--check --deny-fd 3 eci-hook-helper" parses to {check: true, denyFD: 3, name: "eci-hook-helper"}.
type invocation struct {
	check  bool // verify only, exit 0 without exec
	denyFD int
	name   string
	args   []string // arguments after "--", passed to the target after argv[0]
}

// parseArgs reads: [--check] --deny-fd N NAME [-- ARGS...]
//
// Example: parseArgs([]string{"--deny-fd", "3", "eci-command-plan", "--", "--hook"}) sets args to ["--hook"].
func parseArgs(argv []string) (invocation, error) {
	inv := invocation{denyFD: -1}
	i := 0
	for ; i < len(argv); i++ {
		switch argv[i] {
		case "--check":
			inv.check = true
			continue
		case "--deny-fd":
			if i+1 >= len(argv) {
				return inv, errors.New("--deny-fd needs a value")
			}
			n, err := strconv.Atoi(argv[i+1])
			if err != nil || n < 0 {
				return inv, errors.New("--deny-fd needs a descriptor number")
			}
			inv.denyFD = n
			i++
			continue
		}
		break
	}
	if inv.denyFD < 0 {
		return inv, errors.New("--deny-fd is required")
	}
	if i >= len(argv) {
		return inv, errors.New("an artifact name is required")
	}
	inv.name = argv[i]
	i++
	if i < len(argv) {
		if argv[i] != "--" {
			return inv, errors.New("expected -- after the artifact name")
		}
		inv.args = argv[i+1:]
	}
	return inv, nil
}

// usageText is printed to stderr with exitUsage.
//
// Example: eci-verexec --bogus prints this line after the parse error.
const usageText = "usage: eci-verexec [--check] --deny-fd N NAME [-- ARGS...]\n"
