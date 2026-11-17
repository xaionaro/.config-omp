//go:build !linux

package main

import "fmt"

// renameNativeNoReplace refuses installation when no-replace is unavailable.
//
// Example: unsupported platforms preserve the staged database.
func renameNativeNoReplace(
	source string,
	target string,
) error {
	return fmt.Errorf("native no-replace install unavailable; staging preserved")
}
