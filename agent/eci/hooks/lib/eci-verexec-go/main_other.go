//go:build !linux

package main

import (
	"os"
	"runtime"
	"syscall"
)

// main denies every call: only Linux runs the native verifier; other hosts keep the bash loader, and
// build-native-binaries.sh never builds this binary for them.
//
// Example: on darwin, `eci-verexec --deny-fd 3 eci-hook-helper` writes "... (host OS darwin is not supported) ..." and exits 125.
func main() {
	inv, err := parseArgs(os.Args[1:])
	if err != nil {
		os.Stderr.WriteString(usageText)
		os.Exit(exitUsage)
	}
	syscall.Write(inv.denyFD, []byte(denialText(inv.name, "host OS "+runtime.GOOS+" is not supported")))
	os.Exit(exitDenied)
}
