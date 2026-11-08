package main

import (
	"errors"
	"strconv"
)

// Exit statuses. A denial is deny text on the deny descriptor AND exitDenied; a target that exits 125
// writes no deny text. The exec statuses keep the shell's meaning: 126 cannot execute, 127 not found.
const (
	exitDenied     = 125
	exitCannotExec = 126
	exitNotFound   = 127
	exitUsage      = 2
)

type invocation struct {
	check  bool // verify only, exit 0 without exec
	denyFD int
	name   string
	args   []string // arguments after "--", passed to the target after argv[0]
}

// parseArgs reads: [--check] --deny-fd N NAME [-- ARGS...]
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

const usageText = "usage: eci-verexec [--check] --deny-fd N NAME [-- ARGS...]\n"
