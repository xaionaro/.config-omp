package main

import (
	"errors"
	"fmt"
	"testing"
)

func TestCategorizeKeepsTheInnermostCategory(t *testing.T) {
	inner := categorize(CategoryNetwork, errors.New("dial"))
	if got := categoryOf(categorize(CategorySend, fmt.Errorf("send: %w", inner))); got != CategoryNetwork {
		t.Fatalf("got %v", got)
	}
	if got := categoryOf(categorize(CategorySend, errors.New("rejected"))); got != CategorySend {
		t.Fatalf("got %v", got)
	}
	if got := categoryOf(errors.New("plain")); got != CategoryInternal {
		t.Fatalf("got %v", got)
	}
}

func TestEnumsMarshalToFixedWireNames(t *testing.T) {
	for v, want := range map[fmt.Stringer]string{
		CategoryAuth: "auth", CategoryNetwork: "network", CategoryInternal: "internal",
		StatusSent: "sent", StatusKeyPending: "key_pending", StatusInvitePending: "invite_pending", StatusAbsent: "absent",
		RecipientVoid: "void", RecipientVickey: "vickey",
	} {
		if v.String() != want {
			t.Errorf("%v: %q", want, v.String())
		}
	}
}
