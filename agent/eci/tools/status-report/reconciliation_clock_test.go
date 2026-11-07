package main

import "io"

// testRun executes the CLI at the representative report time.
//
// Example: Validate a fixture without depending on the machine clock.
func testRun(
	args []string,
	in io.Reader,
	out io.Writer,
	errOut io.Writer,
) int {
	at, err := parseUTC("2026-11-02T15:24:00Z")
	if err != nil {
		panic(err)
	}
	return runAt(args, in, out, errOut, at)
}

// testReconcile evaluates each source fixture at its report timestamp.
//
// Example: Check an estimate against a fixed report time.
func testReconcile(
	r report,
	dir string,
) []string {
	at, err := parseUTC(r.Timestamp)
	if err != nil {
		panic(err)
	}
	return reconcileAt(r, dir, at)
}
