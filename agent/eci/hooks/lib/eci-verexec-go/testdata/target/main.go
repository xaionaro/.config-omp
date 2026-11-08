// Test target for eci-verexec: prints argv[0], the remaining arguments and every open descriptor
// above 2, then exits with the status named by ECI_VEREXEC_TARGET_EXIT (default 0).
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

func main() {
	var open []int
	for fd := 3; fd < 1024; fd++ {
		var st syscall.Stat_t
		if syscall.Fstat(fd, &st) == nil {
			open = append(open, fd)
		}
	}
	fmt.Printf("argv0=%s\nargs=%s\nfds=%v\n", os.Args[0], strings.Join(os.Args[1:], "|"), open)
	code, _ := strconv.Atoi(os.Getenv("ECI_VEREXEC_TARGET_EXIT"))
	os.Exit(code)
}
