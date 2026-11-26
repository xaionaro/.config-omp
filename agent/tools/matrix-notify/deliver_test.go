package main

import (
	"context"
	"errors"
	"slices"
	"testing"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

const (
	self  id.UserID = "@hermes:hs"
	voidM id.UserID = "@xaionaro:matrix.dx.center"
	vicM  id.UserID = "@danusya:matrix.dx.center"
)

// fakeAPI is a synthetic Matrix boundary that records every primitive called.
type fakeAPI struct {
	direct    map[id.UserID][]id.RoomID
	directErr error
	rooms     map[id.RoomID]roomState
	stateErr  map[id.RoomID]error
	devices   int
	devErr    error
	created   id.RoomID
	sendErr   error
	calls     []string
}

func (f *fakeAPI) DirectRooms(context.Context) (map[id.UserID][]id.RoomID, error) {
	f.calls = append(f.calls, "direct")
	return f.direct, f.directErr
}

func (f *fakeAPI) RoomState(_ context.Context, room id.RoomID) (roomState, error) {
	f.calls = append(f.calls, "state "+room.String())
	if err := f.stateErr[room]; err != nil {
		return roomState{}, err
	}
	st, ok := f.rooms[room]
	if !ok {
		return roomState{}, errRoomInaccessible
	}
	return st, nil
}

func (f *fakeAPI) CreateDM(_ context.Context, target id.UserID) (id.RoomID, error) {
	f.calls = append(f.calls, "create "+target.String())
	f.rooms[f.created] = dm(event.MembershipInvite, id.AlgorithmMegolmV1)
	return f.created, nil
}

func (f *fakeAPI) TargetDevices(_ context.Context, target id.UserID) (int, error) {
	f.calls = append(f.calls, "devices "+target.String())
	return f.devices, f.devErr
}

func (f *fakeAPI) SendEncrypted(_ context.Context, room id.RoomID, text string) (id.EventID, error) {
	f.calls = append(f.calls, "send "+room.String()+" "+text)
	if f.sendErr != nil {
		return "", f.sendErr
	}
	return "$ev", nil
}

func (f *fakeAPI) sent() bool {
	return slices.ContainsFunc(f.calls, func(c string) bool { return len(c) > 4 && c[:5] == "send " })
}

func (f *fakeAPI) createdRoom() bool {
	return slices.ContainsFunc(f.calls, func(c string) bool { return len(c) > 6 && c[:7] == "create " })
}

type memRooms map[id.UserID][]id.RoomID

func (m memRooms) Rooms(target id.UserID) ([]id.RoomID, error) { return m[target], nil }
func (m memRooms) Add(target id.UserID, room id.RoomID) error {
	m[target] = append(m[target], room)
	return nil
}

// dm builds a two-person room state for self and the target in the given membership.
func dm(target event.Membership, alg id.Algorithm) roomState {
	return roomState{
		Members:           map[id.UserID]event.Membership{self: event.MembershipJoin, voidM: target},
		Algorithm:         alg,
		HistoryVisibility: event.HistoryVisibilityShared,
	}
}

func newFake() *fakeAPI {
	return &fakeAPI{direct: map[id.UserID][]id.RoomID{}, rooms: map[id.RoomID]roomState{}, stateErr: map[id.RoomID]error{}, devices: 1, created: "!new:hs"}
}

func TestRecipientAliases(t *testing.T) {
	for alias, want := range map[string]id.UserID{"void": voidM, "vickey": vicM} {
		if got, err := recipientMXID(alias); err != nil || got != want {
			t.Errorf("%s: %q %v", alias, got, err)
		}
	}
	for _, bad := range []string{"", "VOID", "bob", voidM.String()} {
		if _, err := recipientMXID(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestValidMessage(t *testing.T) {
	for _, bad := range []string{"", " ", "\n\t "} {
		if validMessage(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	if !validMessage("  hi $(rm -rf) ") {
		t.Error("text rejected")
	}
}

func TestDeliverSkipsInvalidCandidatesAndSendsLiterally(t *testing.T) {
	f := newFake()
	group := dm(event.MembershipJoin, id.AlgorithmMegolmV1)
	group.Members["@other:hs"] = event.MembershipJoin
	multiInvite := dm(event.MembershipJoin, id.AlgorithmMegolmV1)
	multiInvite.Members["@other:hs"] = event.MembershipInvite
	stale := dm(event.MembershipLeave, id.AlgorithmMegolmV1)
	selfLeft := dm(event.MembershipJoin, id.AlgorithmMegolmV1)
	selfLeft.Members[self] = event.MembershipLeave
	f.rooms = map[id.RoomID]roomState{
		"!a-group:hs": group, "!b-multi:hs": multiInvite, "!c-stale:hs": stale, "!d-plain:hs": dm(event.MembershipJoin, ""),
		"!e-olm:hs": dm(event.MembershipJoin, "m.olm.v1.curve25519-aes-sha2"), "!f-selfleft:hs": selfLeft,
		"!g-invited:hs": dm(event.MembershipInvite, id.AlgorithmMegolmV1), "!h-good:hs": dm(event.MembershipJoin, id.AlgorithmMegolmV1),
	}
	for r := range f.rooms {
		f.direct[voidM] = append(f.direct[voidM], r)
	}
	f.direct[voidM] = append(f.direct[voidM], "!z-gone:hs")
	out, err := deliver(context.Background(), f, memRooms{}, self, voidM, "hi $(x)", false)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != statusSent || out.Room != "!h-good:hs" || out.EventID != "$ev" || out.TargetMembership != event.MembershipJoin {
		t.Fatalf("outcome %+v", out)
	}
	if !slices.Contains(f.calls, "send !h-good:hs hi $(x)") || f.createdRoom() {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestDeliverPrefersJoinedOverInvitedDeterministically(t *testing.T) {
	f := newFake()
	f.rooms["!a:hs"] = dm(event.MembershipInvite, id.AlgorithmMegolmV1)
	f.rooms["!c:hs"] = dm(event.MembershipJoin, id.AlgorithmMegolmV1)
	f.rooms["!b:hs"] = dm(event.MembershipJoin, id.AlgorithmMegolmV1)
	f.direct[voidM] = []id.RoomID{"!c:hs", "!a:hs", "!b:hs"}
	out, err := deliver(context.Background(), f, memRooms{}, self, voidM, "x", true)
	if err != nil || out.Room != "!b:hs" || out.Status != statusResolved {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestResolveOnlyNeverCreatesInvitesOrSends(t *testing.T) {
	f := newFake()
	out, err := deliver(context.Background(), f, memRooms{}, self, vicM, "", true)
	if err != nil || out.Status != statusAbsent || out.Room != "" {
		t.Fatalf("%+v %v", out, err)
	}
	if f.createdRoom() || f.sent() || slices.Contains(f.calls, "devices "+vicM.String()) {
		t.Fatalf("side effects: %v", f.calls)
	}
}

func TestCreatesPrivateDMOncePersistsAndReusesPendingInvite(t *testing.T) {
	f := newFake()
	store := memRooms{}
	out, err := deliver(context.Background(), f, store, self, voidM, "first", false)
	if err != nil || out.Status != statusSent || out.Room != "!new:hs" || out.TargetMembership != event.MembershipInvite {
		t.Fatalf("%+v %v", out, err)
	}
	if !slices.Equal(store[voidM], []id.RoomID{"!new:hs"}) {
		t.Fatalf("not persisted: %v", store)
	}
	f.calls = nil
	out, err = deliver(context.Background(), f, store, self, voidM, "second", false)
	if err != nil || out.Room != "!new:hs" || f.createdRoom() {
		t.Fatalf("second: %+v %v %v", out, err, f.calls)
	}
}

func TestInvitePendingWhenHistoryHiddenUntilJoin(t *testing.T) {
	f := newFake()
	st := dm(event.MembershipInvite, id.AlgorithmMegolmV1)
	st.HistoryVisibility = event.HistoryVisibilityJoined
	f.rooms["!r:hs"] = st
	f.direct[voidM] = []id.RoomID{"!r:hs"}
	out, err := deliver(context.Background(), f, memRooms{}, self, voidM, "x", false)
	if err != nil || out.Status != statusInvitePending || f.sent() {
		t.Fatalf("%+v %v %v", out, err, f.calls)
	}
}

func TestKeyPendingWithoutTargetDevices(t *testing.T) {
	f := newFake()
	f.devices = 0
	f.rooms["!r:hs"] = dm(event.MembershipJoin, id.AlgorithmMegolmV1)
	f.direct[voidM] = []id.RoomID{"!r:hs"}
	out, err := deliver(context.Background(), f, memRooms{}, self, voidM, "x", false)
	if err != nil || out.Status != statusKeyPending || out.EventID != "" || f.sent() {
		t.Fatalf("%+v %v %v", out, err, f.calls)
	}
}

func TestFailuresNeverCreateRooms(t *testing.T) {
	boom := errors.New("network down")
	cases := map[string]func(*fakeAPI){
		"m.direct":   func(f *fakeAPI) { f.directErr = boom },
		"room state": func(f *fakeAPI) { f.direct[voidM] = []id.RoomID{"!r:hs"}; f.stateErr["!r:hs"] = boom },
	}
	for name, setup := range cases {
		f := newFake()
		setup(f)
		if _, err := deliver(context.Background(), f, memRooms{}, self, voidM, "x", false); !errors.Is(err, boom) {
			t.Errorf("%s: err %v", name, err)
		}
		if f.createdRoom() || f.sent() {
			t.Errorf("%s: side effects %v", name, f.calls)
		}
	}
}

func TestSendFailureIsNotSuccess(t *testing.T) {
	f := newFake()
	f.sendErr = errors.New("rejected")
	f.rooms["!r:hs"] = dm(event.MembershipJoin, id.AlgorithmMegolmV1)
	f.direct[voidM] = []id.RoomID{"!r:hs"}
	out, err := deliver(context.Background(), f, memRooms{}, self, voidM, "x", false)
	if err == nil || out.Status == statusSent || out.EventID != "" {
		t.Fatalf("%+v %v", out, err)
	}
}
