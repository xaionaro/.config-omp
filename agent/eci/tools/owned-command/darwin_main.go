//go:build darwin

// The darwin entry point hands the command line to the darwin-pgroup backend. The Linux entry point is in main.go.
package main

import "os"

// main runs owned-command with the darwin-pgroup backend and exits with its status.
//
// Example: owned-command -receipt f -- true exits 0 and writes the receipt.
func main() {
	os.Exit(pgroupMain(os.Args[1:]))
}
