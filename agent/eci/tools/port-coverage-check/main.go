// Command port-coverage-check verifies the port coverage manifest.
//
// The manifest maps every pinned unit to an omp path that must exist in the tree, to an excluded decision whose
// superseding omp path must exist, to a reason-coded decision, or to an open or unrecorded state that keeps the check
// red. A unit is a whole source instruction file, a whole bin wrapper, or one build, refresh, or install behavior of a
// bin wrapper. The check reads only the manifest, the embedded pinned units, and the omp tree.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
)

// main exits with the status that run reports for the process arguments.
//
// Example: port-coverage-check -manifest hooks/tests/port-coverage.tsv -root ~/.omp exits 0 only when no unit is open or
// unrecorded and every row's omp path exists.
func main() {
	os.Exit(run())
}

// run checks the manifest named by the flags, prints one line per violated rule, and then prints one summary line that
// counts the units in each state. It returns 1 when any rule is violated, and 0 otherwise.
//
// Example: run returns 1 and prints an "open until its owner lands" line for each open row.
func run() int {
	manifestPath := flag.String("manifest", "", "port coverage manifest to check")
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
	for _, failure := range report.Failures {
		fmt.Println(failure)
	}
	fmt.Printf("port coverage: %d units; %d mapped, %d reason-coded, %d excluded, %d open, %d unrecorded\n",
		len(units), report.Mapped, report.ReasonCoded, report.Excluded, report.Open, report.Unrecorded)
	if len(report.Failures) > 0 {
		return 1
	}
	return 0
}
