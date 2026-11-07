package main

import (
	"os"
	"testing"
)

// TestExternalCancellationUsesOneSnapshot rejects repeated global discovery after preparation.
//
// Example: known family settlement repolls bound handles without enumerating unrelated PIDs again.
func TestExternalCancellationUsesOneSnapshot(t *testing.T) {
	g, kernel := newDriverControl()
	configureDriverExternalFamily(g, kernel, 2, 300)
	if !g.initialCancel() {
		t.Fatal("valid external root rejected")
	}
	g.drive()
	receipt, code := g.finalize()
	if code != 0 || !receipt.HostChildrenSettled || len(kernel.signals) != 3 {
		t.Fatalf("static family did not settle through three proved handles: status=%d receipt=%+v signals=%+v", code, receipt, kernel.signals)
	}
	if kernel.entryCalls != 1 {
		t.Fatalf("external cancellation enumerated global proc %d times; want one initial snapshot", kernel.entryCalls)
	}
}

// TestExternalCustodyRejectsSupervisorParent prevents launch adoption from entering foreign custody.
//
// Example: even a launch-mode guard cannot admit a numeric child through the foreign ancestry driver.
func TestExternalCustodyRejectsSupervisorParent(t *testing.T) {
	kernel := &syntheticKernel{StatReads: []metadataRead{{Stat: custodyStat(20, 2, os.Getpid())}, {Stat: custodyStat(20, 2, os.Getpid())}}, PIDFDResults: []syntheticFDResult{{FD: 40}}, StrictPoll: true}
	g := custodyGuard(kernel)
	g.launchMode = true
	value, absent, rejected := g.acquire(&pendingAcquisition{PID: 20, DirectoryFD: 50}, g.parents())
	if value != nil || absent || rejected || len(g.current) != 0 || len(kernel.Signals) != 0 {
		t.Fatalf("foreign custody admitted supervisor child: value=%+v absent=%t rejected=%t current=%v signals=%v", value, absent, rejected, g.current, kernel.Signals)
	}
}
