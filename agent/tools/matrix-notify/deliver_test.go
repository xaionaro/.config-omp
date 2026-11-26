package main

import (
	"context"
	"errors"
	"slices"
	"strings"
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
	direct     map[id.UserID][]id.RoomID
	directErr  error
	rooms      map[id.RoomID]RoomState
	stateErr   map[id.RoomID]error
	keyed      int
	shareErr   error
	created    id.RoomID
	sendResult id.EventID
	sendErr    error
	calls      []string
}

func (f *fakeAPI) DirectRooms(context.Context) (map[id.UserID][]id.RoomID, error) {
	f.calls = append(f.calls, "direct")
	return f.direct, f.directErr
}

func (f *fakeAPI) RoomState(_ context.Context, room id.RoomID) (RoomState, error) {
	f.calls = append(f.calls, "state "+room.String())
	if err := f.stateErr[room]; err != nil {
		return RoomState{}, err
	}
	st, ok := f.rooms[room]
	if !ok {
		return RoomState{}, errRoomInaccessible
	}
	return st, nil
}

func (f *fakeAPI) CreateDM(_ context.Context, target id.UserID) (id.RoomID, error) {
	f.calls = append(f.calls, "create "+target.String())
	f.rooms[f.created] = dm(event.MembershipInvite, id.AlgorithmMegolmV1)
	return f.created, nil
}

func (f *fakeAPI) ShareRoomKey(_ context.Context, room id.RoomID, target id.UserID) (int, error) {
	f.calls = append(f.calls, "share "+room.String()+" "+target.String())
	return f.keyed, f.shareErr
}

func (f *fakeAPI) SendEncrypted(_ context.Context, room id.RoomID, text string) (id.EventID, error) {
	f.calls = append(f.calls, "send "+room.String()+" "+text)
	return f.sendResult, f.sendErr
}

func (f *fakeAPI) called(prefix string) bool {
	return slices.ContainsFunc(f.calls, func(c string) bool { return strings.HasPrefix(c, prefix) })
}

type memRooms map[id.UserID][]id.RoomID

func (m memRooms) Rooms(target id.UserID) ([]id.RoomID, error) { return m[target], nil }
func (m memRooms) Add(target id.UserID, room id.RoomID) error {
	m[target] = append(m[target], room)
	return nil
}

// dm builds a two-person room state for self and void in the given membership.
func dm(target event.Membership, alg id.Algorithm) RoomState {
	return RoomState{
		Members:           map[id.UserID]event.Membership{self: event.MembershipJoin, voidM: target},
		Algorithm:         alg,
		HistoryVisibility: event.HistoryVisibilityShared,
	}
}

func newFake() *fakeAPI {
	return &fakeAPI{
		direct: map[id.UserID][]id.RoomID{}, rooms: map[id.RoomID]RoomState{}, stateErr: map[id.RoomID]error{},
		keyed: 1, created: "!new:hs", sendResult: "$ev",
	}
}

func TestRecipientAliases(t *testing.T) {
	for alias, want := range map[string]id.UserID{"void": voidM, "vickey": vicM} {
		r, err := parseRecipient(alias)
		if err != nil || r.MXID() != want || r.String() != alias {
			t.Errorf("%s: %v %q %v", alias, r, r.MXID(), err)
		}
	}
	for _, bad := range []string{"", "VOID", "bob", voidM.String()} {
		if _, err := parseRecipient(bad); categoryOf(err) != CategoryInput {
			t.Errorf("%q accepted: %v", bad, err)
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
	selfLeft := dm(event.MembershipJoin, id.AlgorithmMegolmV1)
	selfLeft.Members[self] = event.MembershipLeave
	f.rooms = map[id.RoomID]RoomState{
		"!a-group:hs": group, "!b-multi:hs": multiInvite, "!c-stale:hs": dm(event.MembershipLeave, id.AlgorithmMegolmV1),
		"!d-plain:hs": dm(event.MembershipJoin, ""), "!e-olm:hs": dm(event.MembershipJoin, id.AlgorithmOlmV1),
		"!f-selfleft:hs": selfLeft, "!g-invited:hs": dm(event.MembershipInvite, id.AlgorithmMegolmV1),
		"!h-good:hs": dm(event.MembershipJoin, id.AlgorithmMegolmV1),
	}
	for r := range f.rooms {
		f.direct[voidM] = append(f.direct[voidM], r)
	}
	f.direct[voidM] = append(f.direct[voidM], "!z-gone:hs")
	out, err := deliver(context.Background(), f, memRooms{}, self, voidM, "hi $(x)")
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != StatusSent || out.Room != "!h-good:hs" || out.EventID != "$ev" || out.TargetMembership != event.MembershipJoin {
		t.Fatalf("outcome %+v", out)
	}
	if !slices.Contains(f.calls, "send !h-good:hs hi $(x)") || f.called("create ") {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestResolvePrefersJoinedOverInvitedDeterministically(t *testing.T) {
	f := newFake()
	f.rooms["!a:hs"] = dm(event.MembershipInvite, id.AlgorithmMegolmV1)
	f.rooms["!c:hs"] = dm(event.MembershipJoin, id.AlgorithmMegolmV1)
	f.rooms["!b:hs"] = dm(event.MembershipJoin, id.AlgorithmMegolmV1)
	f.direct[voidM] = []id.RoomID{"!c:hs", "!a:hs", "!b:hs"}
	out, err := resolve(context.Background(), f, memRooms{}, self, voidM)
	if err != nil || out.Room != "!b:hs" || out.Status != StatusResolved {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestResolveNeverCreatesSharesOrSends(t *testing.T) {
	f := newFake()
	out, err := resolve(context.Background(), f, memRooms{}, self, vicM)
	if err != nil || out.Status != StatusAbsent || out.Room != "" {
		t.Fatalf("%+v %v", out, err)
	}
	if f.called("create ") || f.called("send ") || f.called("share ") {
		t.Fatalf("side effects: %v", f.calls)
	}
}

func TestCreatesPrivateDMOncePersistsAndReusesPendingInvite(t *testing.T) {
	f := newFake()
	store := memRooms{}
	out, err := deliver(context.Background(), f, store, self, voidM, "first")
	if err != nil || out.Status != StatusSent || out.Room != "!new:hs" || out.TargetMembership != event.MembershipInvite {
		t.Fatalf("%+v %v", out, err)
	}
	if !slices.Equal(store[voidM], []id.RoomID{"!new:hs"}) {
		t.Fatalf("not persisted: %v", store)
	}
	f.calls = nil
	out, err = deliver(context.Background(), f, store, self, voidM, "second")
	if err != nil || out.Room != "!new:hs" || f.called("create ") {
		t.Fatalf("second: %+v %v %v", out, err, f.calls)
	}
}

func TestInvitePendingWhenHistoryHiddenUntilJoin(t *testing.T) {
	f := newFake()
	st := dm(event.MembershipInvite, id.AlgorithmMegolmV1)
	st.HistoryVisibility = event.HistoryVisibilityJoined
	f.rooms["!r:hs"] = st
	f.direct[voidM] = []id.RoomID{"!r:hs"}
	out, err := deliver(context.Background(), f, memRooms{}, self, voidM, "x")
	if err != nil || out.Status != StatusInvitePending || f.called("send ") {
		t.Fatalf("%+v %v %v", out, err, f.calls)
	}
}

func TestKeyPendingWhenNoTargetDeviceReceivedTheKey(t *testing.T) {
	f := newFake()
	f.keyed = 0
	f.rooms["!r:hs"] = dm(event.MembershipJoin, id.AlgorithmMegolmV1)
	f.direct[voidM] = []id.RoomID{"!r:hs"}
	out, err := deliver(context.Background(), f, memRooms{}, self, voidM, "x")
	if err != nil || out.Status != StatusKeyPending || out.EventID != "" || f.called("send ") {
		t.Fatalf("%+v %v %v", out, err, f.calls)
	}
	if !slices.Contains(f.calls, "share !r:hs "+voidM.String()) {
		t.Fatalf("key not shared before deciding: %v", f.calls)
	}
}

func TestFailuresNeverCreateRooms(t *testing.T) {
	boom := categorize(CategoryNetwork, errors.New("network down"))
	cases := map[string]func(*fakeAPI){
		"m.direct":   func(f *fakeAPI) { f.directErr = boom },
		"room state": func(f *fakeAPI) { f.direct[voidM] = []id.RoomID{"!r:hs"}; f.stateErr["!r:hs"] = boom },
	}
	for name, setup := range cases {
		f := newFake()
		setup(f)
		if _, err := deliver(context.Background(), f, memRooms{}, self, voidM, "x"); categoryOf(err) != CategoryNetwork {
			t.Errorf("%s: err %v", name, err)
		}
		if f.called("create ") || f.called("send ") {
			t.Errorf("%s: side effects %v", name, f.calls)
		}
	}
}

func TestSendFailureOrMissingEventIsNotSuccess(t *testing.T) {
	for name, setup := range map[string]func(*fakeAPI){
		"rejected": func(f *fakeAPI) { f.sendErr = categorize(CategorySend, errors.New("rejected")) },
		"no event": func(f *fakeAPI) { f.sendResult = "" },
	} {
		f := newFake()
		setup(f)
		f.rooms["!r:hs"] = dm(event.MembershipJoin, id.AlgorithmMegolmV1)
		f.direct[voidM] = []id.RoomID{"!r:hs"}
		out, err := deliver(context.Background(), f, memRooms{}, self, voidM, "x")
		if categoryOf(err) != CategorySend || out.Status == StatusSent || out.EventID != "" {
			t.Errorf("%s: %+v %v", name, out, err)
		}
	}
}
