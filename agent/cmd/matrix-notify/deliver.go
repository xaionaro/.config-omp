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

// NotStoredError marks a SendEncrypted failure that proves the homeserver did not store the event.
//
// Example: errors.As(err, new(*NotStoredError)) distinguishes "rejected" from "may have been stored".
type NotStoredError struct {
	Err error
}

// Error returns the underlying error text.
//
// Example: markNotStored(errors.New("encrypt")).Error() == "encrypt".
func (e *NotStoredError) Error() string { return e.Err.Error() }

// Unwrap exposes the underlying error, including its Category.
//
// Example: categoryOf(markNotStored(categorize(CategoryCrypto, err))) == CategoryCrypto.
func (e *NotStoredError) Unwrap() error { return e.Err }

// markNotStored marks err as a send failure that proves the event was not stored.
//
// Example: return "", markNotStored(classifyHTTP(err, CategorySend)) for a Matrix error answer.
func markNotStored(err error) error { return &NotStoredError{Err: err} }

// RoomState is the subset of fresh room state that decides whether a room is a valid encrypted DM.
//
// Example: validDM(st, sender, recipient) inspects a RoomState returned by MatrixAPI.RoomState.
type RoomState struct {
	Members           map[id.UserID]event.Membership
	Algorithm         id.Algorithm
	HistoryVisibility event.HistoryVisibility
}

// MatrixAPI is the Matrix boundary resolve and deliver need; MautrixAPI implements it against the homeserver.
//
// Example: deliver(ctx, api, store, sender, recipient, message) with api := connect(...).
type MatrixAPI interface {
	// DirectRooms returns m.direct; an absent event is an empty map.
	//
	// Example: rooms := direct[recipient].
	DirectRooms(ctx context.Context) (map[id.UserID][]id.RoomID, error)
	// RoomState fetches fresh room state (refreshing the crypto state store); errRoomInaccessible if unreadable.
	//
	// Example: st, err := api.RoomState(ctx, room).
	RoomState(
		ctx context.Context,
		room id.RoomID,
	) (RoomState, error)
	// CreateDM creates a private encrypted direct room inviting only recipient.
	//
	// Example: room, err := api.CreateDM(ctx, recipient).
	CreateDM(
		ctx context.Context,
		recipient id.UserID,
	) (id.RoomID, error)
	// ShareRoomKey shares the room's Megolm key and reports whether it was encrypted to at least one of
	// recipient's devices.
	//
	// Example: false means no recipient device could decrypt a message sent now.
	ShareRoomKey(
		ctx context.Context,
		room id.RoomID,
		recipient id.UserID,
	) (bool, error)
	// SendEncrypted sends message as a Megolm ciphertext event, never as plaintext. An error means "not stored" only
	// when wrapped by markNotStored; any other error leaves it unknown whether the homeserver stored the event.
	//
	// Example: ev, err := api.SendEncrypted(ctx, room, "hello").
	SendEncrypted(
		ctx context.Context,
		room id.RoomID,
		message Message,
	) (id.EventID, error)
}

// RoomStore remembers DM rooms this tool created, per account and recipient; it is kept beside its consumer,
// deliver.
//
// Example: RoomFile implements it on disk.
type RoomStore interface {
	// Rooms lists the rooms saved for recipient.
	//
	// Example: saved, err := store.Rooms(recipient).
	Rooms(recipient id.UserID) ([]id.RoomID, error)
	// Add saves room for recipient.
	//
	// Example: store.Add(recipient, created).
	Add(
		recipient id.UserID,
		room id.RoomID,
	) error
}

// Message is the literal text body of one DM, sent as-is.
//
// Example: Message("build finished").
type Message string

// validMessage reports whether message has non-whitespace content.
//
// Example: validMessage(" \n") is false.
func validMessage(message Message) bool { return strings.TrimSpace(string(message)) != "" }

// validDM reports whether st is an encrypted room holding exactly sender (joined) and recipient (joined or invited).
//
// Example: a three-member room is not a valid DM.
func validDM(
	st RoomState,
	sender id.UserID,
	recipient id.UserID,
) bool {
	if st.Algorithm != id.AlgorithmMegolmV1 || st.Members[sender] != event.MembershipJoin {
		return false
	}
	if m := st.Members[recipient]; m != event.MembershipJoin && m != event.MembershipInvite {
		return false
	}
	for user, m := range st.Members {
		if user != sender && user != recipient && (m == event.MembershipJoin || m == event.MembershipInvite) {
			return false
		}
	}
	return true
}

// findDM validates every candidate (m.direct hint plus saved rooms) and returns the best, or "" when none is valid.
//
// Example: room, st, err := findDM(ctx, api, store, sender, recipient).
func findDM(
	ctx context.Context,
	api MatrixAPI,
	store RoomStore,
	sender id.UserID,
	recipient id.UserID,
) (id.RoomID, RoomState, error) {
	direct, err := api.DirectRooms(ctx)
	if err != nil {
		return "", RoomState{}, err
	}
	saved, err := store.Rooms(recipient)
	if err != nil {
		return "", RoomState{}, err
	}
	candidates := slices.Concat(direct[recipient], saved)
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
		if !validDM(st, sender, recipient) {
			continue
		}
		if best == "" || (st.Members[recipient] == event.MembershipJoin && bestState.Members[recipient] != event.MembershipJoin) {
			best, bestState = room, st
		}
	}
	return best, bestState, nil
}

// resolve reports the DM a send would use, without creating, inviting, sharing keys or sending.
//
// Example: resolve(ctx, api, store, sender, RecipientVickey.MXID()) proves the vickey path without a message.
func resolve(
	ctx context.Context,
	api MatrixAPI,
	store RoomStore,
	sender id.UserID,
	recipient id.UserID,
) (Outcome, error) {
	room, st, err := findDM(ctx, api, store, sender, recipient)
	if err != nil {
		return Outcome{}, err
	}
	if room == "" {
		return Outcome{Status: StatusAbsent}, nil
	}
	return Outcome{Status: StatusResolved, RoomID: room, RecipientMembership: st.Members[recipient]}, nil
}

// deliver finds or creates the DM with recipient and sends message encrypted, or reports why it is pending. Once
// the send request may have left, it never returns an error: the event either has an ID (sent) or may exist
// (unknown), unless SendEncrypted proves it was not stored.
//
// Example: out, err := deliver(ctx, api, store, sender, recipient, "build finished").
func deliver(
	ctx context.Context,
	api MatrixAPI,
	store RoomStore,
	sender id.UserID,
	recipient id.UserID,
	message Message,
) (Outcome, error) {
	room, st, err := findDM(ctx, api, store, sender, recipient)
	if err != nil {
		return Outcome{}, err
	}
	if room == "" {
		// TODO: a saved room that the recipient left, or whose invite they rejected, fails validDM, so the next send
		// creates and invites a new room, once per leave or decline (as does a createRoom whose response was lost, or a
		// failed store.Add), and rooms.json only grows; decide whether a left or declined notifier room ends in a
		// terminal "declined" result or a fresh invite, and prune saved rooms that no longer validate.
		if room, err = api.CreateDM(ctx, recipient); err != nil {
			return Outcome{}, categorize(CategoryRoom, err)
		}
		// Persist before sending so the next run reuses this room even while the invite is pending.
		if err := store.Add(recipient, room); err != nil {
			return Outcome{}, categorize(CategoryState, err)
		}
		if st, err = api.RoomState(ctx, room); err != nil {
			return Outcome{}, categorize(CategoryRoom, err)
		}
		if !validDM(st, sender, recipient) {
			return Outcome{}, categorize(CategoryRoom, errors.New("created room failed validation"))
		}
	}
	out := Outcome{RoomID: room, RecipientMembership: st.Members[recipient]}
	if out.RecipientMembership == event.MembershipInvite && st.HistoryVisibility == event.HistoryVisibilityJoined {
		out.Status = StatusInvitePending
		return out, nil
	}
	keyed, err := api.ShareRoomKey(ctx, room, recipient)
	if err != nil {
		return Outcome{}, categorize(CategoryCrypto, err)
	}
	if !keyed {
		out.Status = StatusKeyPending
		return out, nil
	}
	ev, err := api.SendEncrypted(ctx, room, message)
	var notStored *NotStoredError
	switch {
	case errors.As(err, &notStored):
		return Outcome{}, categorize(CategorySend, err)
	case err != nil, ev == "":
		out.Status = StatusUnknown
		return out, nil
	}
	out.Status, out.EventID = StatusSent, ev
	return out, nil
}
