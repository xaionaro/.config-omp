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
	sender id.UserID = "@hermes:hs"
	voidM  id.UserID = "@xaionaro:matrix.dx.center"
	vicM   id.UserID = "@danusya:matrix.dx.center"
)

// fakeAPI is a synthetic Matrix boundary that records every primitive called.
type fakeAPI struct {
	direct     map[id.UserID][]id.RoomID
	directErr  error
	rooms      map[id.RoomID]RoomState
	stateErr   map[id.RoomID]error
	keyed      bool
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

func (f *fakeAPI) CreateDM(_ context.Context, recipient id.UserID) (id.RoomID, error) {
	f.calls = append(f.calls, "create "+recipient.String())
	f.rooms[f.created] = dm(event.MembershipInvite, id.AlgorithmMegolmV1)
	return f.created, nil
}

func (f *fakeAPI) ShareRoomKey(_ context.Context, room id.RoomID, recipient id.UserID) (bool, error) {
	f.calls = append(f.calls, "share "+room.String()+" "+recipient.String())
	return f.keyed, f.shareErr
}

func (f *fakeAPI) SendEncrypted(_ context.Context, room id.RoomID, message Message) (id.EventID, error) {
	f.calls = append(f.calls, "send "+room.String()+" "+string(message))
	return f.sendResult, f.sendErr
}

func (f *fakeAPI) called(prefix string) bool {
	return slices.ContainsFunc(f.calls, func(c string) bool { return strings.HasPrefix(c, prefix) })
}

type memRooms map[id.UserID][]id.RoomID

func (m memRooms) Rooms(recipient id.UserID) ([]id.RoomID, error) { return m[recipient], nil }
func (m memRooms) Add(recipient id.UserID, room id.RoomID) error {
	m[recipient] = append(m[recipient], room)
	return nil
}

// dm builds a two-person room state for the sender and void in the given membership.
func dm(recipient event.Membership, alg id.Algorithm) RoomState {
	return RoomState{
		Members:           map[id.UserID]event.Membership{sender: event.MembershipJoin, voidM: recipient},
		Algorithm:         alg,
		HistoryVisibility: event.HistoryVisibilityShared,
	}
}

func newFake() *fakeAPI {
	return &fakeAPI{
		direct: map[id.UserID][]id.RoomID{}, rooms: map[id.RoomID]RoomState{}, stateErr: map[id.RoomID]error{},
		keyed: true, created: "!new:hs", sendResult: "$ev",
	}
}

// fakeWithDM is a fake whose m.direct points at one valid joined DM with void.
func fakeWithDM() *fakeAPI {
	f := newFake()
	f.rooms["!r:hs"] = dm(event.MembershipJoin, id.AlgorithmMegolmV1)
	f.direct[voidM] = []id.RoomID{"!r:hs"}
	return f
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
	for _, bad := range []Message{"", " ", "\n\t "} {
		if validMessage(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	if !validMessage("  hi $(rm -rf) ") {
		t.Error("message rejected")
	}
}

func TestDeliverSkipsInvalidCandidatesAndSendsLiterally(t *testing.T) {
	f := newFake()
	group := dm(event.MembershipJoin, id.AlgorithmMegolmV1)
	group.Members["@other:hs"] = event.MembershipJoin
	multiInvite := dm(event.MembershipJoin, id.AlgorithmMegolmV1)
	multiInvite.Members["@other:hs"] = event.MembershipInvite
	senderLeft := dm(event.MembershipJoin, id.AlgorithmMegolmV1)
	senderLeft.Members[sender] = event.MembershipLeave
	f.rooms = map[id.RoomID]RoomState{
		"!a-group:hs": group, "!b-multi:hs": multiInvite, "!c-stale:hs": dm(event.MembershipLeave, id.AlgorithmMegolmV1),
		"!d-plain:hs": dm(event.MembershipJoin, ""), "!e-olm:hs": dm(event.MembershipJoin, id.AlgorithmOlmV1),
		"!f-senderleft:hs": senderLeft, "!g-invited:hs": dm(event.MembershipInvite, id.AlgorithmMegolmV1),
		"!h-good:hs": dm(event.MembershipJoin, id.AlgorithmMegolmV1),
	}
	for r := range f.rooms {
		f.direct[voidM] = append(f.direct[voidM], r)
	}
	f.direct[voidM] = append(f.direct[voidM], "!z-gone:hs")
	out, err := deliver(context.Background(), f, memRooms{}, sender, voidM, "hi $(x)")
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != StatusSent || out.RoomID != "!h-good:hs" || out.EventID != "$ev" || out.RecipientMembership != event.MembershipJoin {
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
	out, err := resolve(context.Background(), f, memRooms{}, sender, voidM)
	if err != nil || out.RoomID != "!b:hs" || out.Status != StatusResolved {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestResolveNeverCreatesSharesOrSends(t *testing.T) {
	f := newFake()
	out, err := resolve(context.Background(), f, memRooms{}, sender, vicM)
	if err != nil || out.Status != StatusAbsent || out.RoomID != "" {
		t.Fatalf("%+v %v", out, err)
	}
	if f.called("create ") || f.called("send ") || f.called("share ") {
		t.Fatalf("side effects: %v", f.calls)
	}
}

func TestCreatesPrivateDMOncePersistsAndReusesPendingInvite(t *testing.T) {
	f := newFake()
	store := memRooms{}
	out, err := deliver(context.Background(), f, store, sender, voidM, "first")
	if err != nil || out.Status != StatusSent || out.RoomID != "!new:hs" || out.RecipientMembership != event.MembershipInvite {
		t.Fatalf("%+v %v", out, err)
	}
	if !slices.Equal(store[voidM], []id.RoomID{"!new:hs"}) {
		t.Fatalf("not persisted: %v", store)
	}
	f.calls = nil
	out, err = deliver(context.Background(), f, store, sender, voidM, "second")
	if err != nil || out.RoomID != "!new:hs" || f.called("create ") {
		t.Fatalf("second: %+v %v %v", out, err, f.calls)
	}
}

func TestInvitePendingWhenHistoryHiddenUntilJoin(t *testing.T) {
	f := newFake()
	st := dm(event.MembershipInvite, id.AlgorithmMegolmV1)
	st.HistoryVisibility = event.HistoryVisibilityJoined
	f.rooms["!r:hs"] = st
	f.direct[voidM] = []id.RoomID{"!r:hs"}
	out, err := deliver(context.Background(), f, memRooms{}, sender, voidM, "x")
	if err != nil || out.Status != StatusInvitePending || f.called("send ") {
		t.Fatalf("%+v %v %v", out, err, f.calls)
	}
}

func TestKeyPendingWhenNoRecipientDeviceReceivedTheKey(t *testing.T) {
	f := fakeWithDM()
	f.keyed = false
	out, err := deliver(context.Background(), f, memRooms{}, sender, voidM, "x")
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
		if _, err := deliver(context.Background(), f, memRooms{}, sender, voidM, "x"); categoryOf(err) != CategoryNetwork {
			t.Errorf("%s: err %v", name, err)
		}
		if f.called("create ") || f.called("send ") {
			t.Errorf("%s: side effects %v", name, f.calls)
		}
	}
}

// T2 (fake boundary): only an error the API marks as not stored is a failure; anything else may have been stored.
func TestSendOutcomeIsUnknownUnlessProvenNotStored(t *testing.T) {
	cases := map[string]struct {
		setup  func(*fakeAPI)
		status Status
		cat    Category
	}{
		"marked rejection": {func(f *fakeAPI) { f.sendErr = markNotStored(categorize(CategorySend, errors.New("rejected"))) }, StatusUnset, CategorySend},
		"unmarked error":   {func(f *fakeAPI) { f.sendErr = errors.New("connection reset") }, StatusUnknown, CategoryInternal},
		"no event id":      {func(f *fakeAPI) { f.sendResult = "" }, StatusUnknown, CategoryInternal},
	}
	for name, c := range cases {
		f := fakeWithDM()
		c.setup(f)
		out, err := deliver(context.Background(), f, memRooms{}, sender, voidM, "x")
		if out.Status != c.status || out.EventID != "" {
			t.Errorf("%s: outcome %+v", name, out)
		}
		if c.status == StatusUnknown && (err != nil || out.RoomID != "!r:hs") {
			t.Errorf("%s: unknown must carry the room and no error: %+v %v", name, out, err)
		}
		if c.status == StatusUnset && categoryOf(err) != c.cat {
			t.Errorf("%s: err %v", name, err)
		}
	}
}
