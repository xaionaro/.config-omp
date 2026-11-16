//go:build !linux

package main

import "fmt"

// renameRecoveryNoReplace refuses quarantine where atomic no-replace rename is unavailable.
//
// Example: unsupported platforms preserve all interrupted evidence unchanged.
func renameRecoveryNoReplace(
	source string,
	target string,
) error {
	return fmt.Errorf("no-replace recovery quarantine unavailable; evidence preserved")
}
