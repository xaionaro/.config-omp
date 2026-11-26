package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// recipients is the single closed table of people this tool may message.
var recipients = map[string]id.UserID{
	"void":   "@xaionaro:matrix.dx.center",
	"vickey": "@danusya:matrix.dx.center",
}

func recipientMXID(alias string) (id.UserID, error) {
	mxid, ok := recipients[alias]
	if !ok {
		return "", fmt.Errorf("%w: unknown recipient; allowed: void, vickey", errInput)
	}
	return mxid, nil
}

func validMessage(s string) bool { return strings.TrimSpace(s) != "" }

// errRoomInaccessible marks the documented "not in this room / no such room" answers; any other failure aborts.
var errRoomInaccessible = errors.New("room inaccessible")

type roomState struct {
	Members           map[id.UserID]event.Membership
	Algorithm         id.Algorithm
	HistoryVisibility event.HistoryVisibility
}

// matrixAPI is the Matrix boundary deliver needs; mautrixAPI implements it against the homeserver.
type matrixAPI interface {
	// DirectRooms returns m.direct; an absent event is an empty map.
	DirectRooms(ctx context.Context) (map[id.UserID][]id.RoomID, error)
	// RoomState fetches fresh room state (and refreshes the crypto state store); errRoomInaccessible if we cannot read it.
	RoomState(ctx context.Context, room id.RoomID) (roomState, error)
	CreateDM(ctx context.Context, target id.UserID) (id.RoomID, error)
	TargetDevices(ctx context.Context, target id.UserID) (int, error)
	// SendEncrypted sends text as a Megolm ciphertext event, never as plaintext.
	SendEncrypted(ctx context.Context, room id.RoomID, text string) (id.EventID, error)
}

// roomStore remembers DM rooms this tool created, per account and target.
type roomStore interface {
	Rooms(target id.UserID) ([]id.RoomID, error)
	Add(target id.UserID, room id.RoomID) error
}

type status string

const (
	statusSent          status = "sent"
	statusResolved      status = "resolved"
	statusAbsent        status = "absent" // resolve-only: no valid DM exists; a send would create one
	statusInvitePending status = "invite_pending"
	statusKeyPending    status = "key_pending"
	statusError         status = "error"
)

type outcome struct {
	Status           status
	Room             id.RoomID
	TargetMembership event.Membership
	EventID          id.EventID
}

// validDM reports whether st is an encrypted room holding exactly self (joined) and target (joined or invited).
func validDM(st roomState, self, target id.UserID) bool {
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

func resolveDM(ctx context.Context, api matrixAPI, store roomStore, self, target id.UserID) (id.RoomID, roomState, error) {
	direct, err := api.DirectRooms(ctx)
	if err != nil {
		return "", roomState{}, err
	}
	saved, err := store.Rooms(target)
	if err != nil {
		return "", roomState{}, err
	}
	candidates := slices.Concat(direct[target], saved)
	slices.Sort(candidates)
	candidates = slices.Compact(candidates)
	var best id.RoomID
	var bestState roomState
	for _, room := range candidates {
		st, err := api.RoomState(ctx, room)
		if errors.Is(err, errRoomInaccessible) {
			continue
		}
		if err != nil {
			return "", roomState{}, err
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

// deliver resolves (or, unless resolveOnly, creates) the DM with target and sends message encrypted.
func deliver(ctx context.Context, api matrixAPI, store roomStore, self, target id.UserID, message string, resolveOnly bool) (outcome, error) {
	room, st, err := resolveDM(ctx, api, store, self, target)
	if err != nil {
		return outcome{}, err
	}
	if room == "" {
		if resolveOnly {
			return outcome{Status: statusAbsent}, nil
		}
		if room, err = api.CreateDM(ctx, target); err != nil {
			return outcome{}, fmt.Errorf("%w: create DM: %w", errRoom, err)
		}
		// Persist before sending so the next run reuses this room even while the invite is pending.
		if err := store.Add(target, room); err != nil {
			return outcome{}, fmt.Errorf("%w: %w", errState, err)
		}
		if st, err = api.RoomState(ctx, room); err != nil {
			return outcome{}, err
		}
		if !validDM(st, self, target) {
			return outcome{}, fmt.Errorf("%w: created room failed validation", errRoom)
		}
	}
	out := outcome{Room: room, TargetMembership: st.Members[target]}
	if resolveOnly {
		out.Status = statusResolved
		return out, nil
	}
	if out.TargetMembership == event.MembershipInvite && st.HistoryVisibility == event.HistoryVisibilityJoined {
		out.Status = statusInvitePending
		return out, nil
	}
	n, err := api.TargetDevices(ctx, target)
	if err != nil {
		return outcome{}, fmt.Errorf("%w: query target devices: %w", errCrypto, err)
	}
	if n == 0 {
		out.Status = statusKeyPending
		return out, nil
	}
	ev, err := api.SendEncrypted(ctx, room, message)
	if err != nil {
		return outcome{}, err
	}
	if ev == "" {
		return outcome{}, fmt.Errorf("%w: server returned no event ID", errSend)
	}
	out.Status, out.EventID = statusSent, ev
	return out, nil
}
