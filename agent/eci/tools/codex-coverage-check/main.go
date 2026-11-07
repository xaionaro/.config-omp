// Command codex-coverage-check verifies the codex port coverage manifest.
//
// The manifest maps every pinned codex unit to an omp path that must exist in the tree, or to a recorded reason
// code. A unit is either a whole codex instruction file or one build, refresh, or install behavior of a codex bin
// wrapper. The check reads only the manifest, the embedded pinned units, and the omp tree, so it runs without
// ~/.codex.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
)

// main exits with the status that run reports for the process arguments.
//
// Example: codex-coverage-check -manifest hooks/tests/codex-port-coverage.tsv -root ~/.omp exits 0 for full coverage.
func main() {
	os.Exit(run())
}

// run checks the manifest named by the flags, prints one line per violated rule, and returns the exit status. On
// success it prints one summary line instead.
//
// Example: run returns 1 and prints a "mapped path ... is not in the tree" line when an omp file is missing.
func run() int {
	manifestPath := flag.String("manifest", "", "codex port coverage manifest to check")
	treeRoot := flag.String("root", "", "omp tree root that mapped paths resolve against")
	flag.Parse()
	if *manifestPath == "" || *treeRoot == "" {
		flag.Usage()
		return 2
	}
	manifest, err := os.ReadFile(*manifestPath)
	if err != nil {
		fmt.Println(err)
		return 1
	}
	units, err := pinnedUnits()
	if err != nil {
		fmt.Println(err)
		return 1
	}
	report, err := Check(bytes.NewReader(manifest), units, os.DirFS(*treeRoot))
	if err != nil {
		fmt.Println(err)
		return 1
	}
	if len(report.Failures) > 0 {
		for _, failure := range report.Failures {
			fmt.Println(failure)
		}
		return 1
	}
	fmt.Printf("codex port coverage ok: %d units; %d mapped to omp paths, %d with recorded reasons\n",
		len(units), report.Mapped, report.Recorded)
	return 0
}
