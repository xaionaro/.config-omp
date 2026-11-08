//go:build !linux

package main

import (
	"os"
	"runtime"
	"syscall"
)

// Only Linux runs the native verifier; other hosts keep the bash loader and never build this binary.
func main() {
	inv, err := parseArgs(os.Args[1:])
	if err != nil {
		os.Stderr.WriteString(usageText)
		os.Exit(exitUsage)
	}
	syscall.Write(inv.denyFD, []byte(denialText(inv.name, "host OS "+runtime.GOOS+" is not supported")))
	os.Exit(exitDenied)
}
