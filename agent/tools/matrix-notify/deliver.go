package main

import (
	"context"
	"errors"
	"slices"
	"strings"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// errRoomInaccessible marks the documented "not in this room / no such room" answers; any other failure aborts.
//
// Example: RoomState returns it for M_FORBIDDEN, and resolution skips that candidate.
var errRoomInaccessible = errors.New("room inaccessible")

// RoomState is the subset of fresh room state that decides whether a room is a valid encrypted DM.
//
// Example: validDM(st, self, target) inspects a RoomState returned by MatrixAPI.RoomState.
type RoomState struct {
	Members           map[id.UserID]event.Membership
	Algorithm         id.Algorithm
	HistoryVisibility event.HistoryVisibility
}

// MatrixAPI is the Matrix boundary resolve and deliver need; MautrixAPI implements it against the homeserver.
//
// Example: deliver(ctx, api, store, self, target, text) with api := connect(...).
type MatrixAPI interface {
	// DirectRooms returns m.direct; an absent event is an empty map.
	//
	// Example: rooms := direct[target].
	DirectRooms(ctx context.Context) (map[id.UserID][]id.RoomID, error)
	// RoomState fetches fresh room state (refreshing the crypto state store); errRoomInaccessible if unreadable.
	//
	// Example: st, err := api.RoomState(ctx, room).
	RoomState(
		ctx context.Context,
		room id.RoomID,
	) (RoomState, error)
	// CreateDM creates a private encrypted direct room inviting only target.
	//
	// Example: room, err := api.CreateDM(ctx, target).
	CreateDM(
		ctx context.Context,
		target id.UserID,
	) (id.RoomID, error)
	// ShareRoomKey shares the room's Megolm key and returns how many of target's devices hold it.
	//
	// Example: n == 0 means no target device could decrypt a message sent now.
	ShareRoomKey(
		ctx context.Context,
		room id.RoomID,
		target id.UserID,
	) (int, error)
	// SendEncrypted sends text as a Megolm ciphertext event, never as plaintext.
	//
	// Example: ev, err := api.SendEncrypted(ctx, room, "hello").
	SendEncrypted(
		ctx context.Context,
		room id.RoomID,
		text string,
	) (id.EventID, error)
}

// RoomStore remembers DM rooms this tool created, per account and target; it is kept beside its consumer, deliver.
//
// Example: RoomFile implements it on disk.
type RoomStore interface {
	// Rooms lists the rooms saved for target.
	//
	// Example: saved, err := store.Rooms(target).
	Rooms(target id.UserID) ([]id.RoomID, error)
	// Add saves room for target.
	//
	// Example: store.Add(target, created).
	Add(
		target id.UserID,
		room id.RoomID,
	) error
}

// validMessage reports whether s has non-whitespace content.
//
// Example: validMessage(" \n") is false.
func validMessage(s string) bool { return strings.TrimSpace(s) != "" }

// validDM reports whether st is an encrypted room holding exactly self (joined) and target (joined or invited).
//
// Example: a three-member room is not a valid DM.
func validDM(
	st RoomState,
	self id.UserID,
	target id.UserID,
) bool {
	if st.Algorithm != id.AlgorithmMegolmV1 || st.Members[self] != event.MembershipJoin {
		return false
	}
	if m := st.Members[target]; m != event.MembershipJoin && m != event.MembershipInvite {
		return false
	}
	for user, m := range st.Members {
		if user != self && user != target && (m == event.MembershipJoin || m == event.MembershipInvite) {
			return false
		}
	}
	return true
}

// findDM validates every candidate (m.direct hint plus saved rooms) and returns the best, or "" when none is valid.
//
// Example: room, st, err := findDM(ctx, api, store, self, target).
func findDM(
	ctx context.Context,
	api MatrixAPI,
	store RoomStore,
	self id.UserID,
	target id.UserID,
) (id.RoomID, RoomState, error) {
	direct, err := api.DirectRooms(ctx)
	if err != nil {
		return "", RoomState{}, err
	}
	saved, err := store.Rooms(target)
	if err != nil {
		return "", RoomState{}, err
	}
	candidates := slices.Concat(direct[target], saved)
	slices.Sort(candidates)
	candidates = slices.Compact(candidates)
	var best id.RoomID
	var bestState RoomState
	for _, room := range candidates {
		st, err := api.RoomState(ctx, room)
		if errors.Is(err, errRoomInaccessible) {
			continue
		}
		if err != nil {
			return "", RoomState{}, err
		}
		if !validDM(st, self, target) {
			continue
		}
		if best == "" || (st.Members[target] == event.MembershipJoin && bestState.Members[target] != event.MembershipJoin) {
			best, bestState = room, st
		}
	}
	return best, bestState, nil
}

// resolve reports the DM a send would use, without creating, inviting, sharing keys or sending.
//
// Example: resolve(ctx, api, store, self, RecipientVickey.MXID()) proves the vickey path without a message.
func resolve(
	ctx context.Context,
	api MatrixAPI,
	store RoomStore,
	self id.UserID,
	target id.UserID,
) (Outcome, error) {
	room, st, err := findDM(ctx, api, store, self, target)
	if err != nil {
		return Outcome{}, err
	}
	if room == "" {
		return Outcome{Status: StatusAbsent}, nil
	}
	return Outcome{Status: StatusResolved, Room: room, TargetMembership: st.Members[target]}, nil
}

// deliver finds or creates the DM with target and sends message encrypted, or reports why it is pending.
//
// Example: out, err := deliver(ctx, api, store, self, target, "build finished").
func deliver(
	ctx context.Context,
	api MatrixAPI,
	store RoomStore,
	self id.UserID,
	target id.UserID,
	message string,
) (Outcome, error) {
	room, st, err := findDM(ctx, api, store, self, target)
	if err != nil {
		return Outcome{}, err
	}
	if room == "" {
		if room, err = api.CreateDM(ctx, target); err != nil {
			return Outcome{}, categorize(CategoryRoom, err)
		}
		// Persist before sending so the next run reuses this room even while the invite is pending.
		if err := store.Add(target, room); err != nil {
			return Outcome{}, categorize(CategoryState, err)
		}
		if st, err = api.RoomState(ctx, room); err != nil {
			return Outcome{}, categorize(CategoryRoom, err)
		}
		if !validDM(st, self, target) {
			return Outcome{}, categorize(CategoryRoom, errors.New("created room failed validation"))
		}
	}
	out := Outcome{Room: room, TargetMembership: st.Members[target]}
	if out.TargetMembership == event.MembershipInvite && st.HistoryVisibility == event.HistoryVisibilityJoined {
		out.Status = StatusInvitePending
		return out, nil
	}
	keyed, err := api.ShareRoomKey(ctx, room, target)
	if err != nil {
		return Outcome{}, categorize(CategoryCrypto, err)
	}
	if keyed == 0 {
		out.Status = StatusKeyPending
		return out, nil
	}
	ev, err := api.SendEncrypted(ctx, room, message)
	if err != nil {
		return Outcome{}, categorize(CategorySend, err)
	}
	if ev == "" {
		return Outcome{}, categorize(CategorySend, errors.New("server returned no event ID"))
	}
	out.Status, out.EventID = StatusSent, ev
	return out, nil
}
