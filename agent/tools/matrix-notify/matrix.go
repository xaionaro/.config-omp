package main

import (
	"context"
	"errors"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// MautrixAPI implements MatrixAPI with a logged-in mautrix client and its crypto helper.
//
// Example: api, err := connect(ctx, dir, creds, nil); deliver(ctx, api, ...).
type MautrixAPI struct {
	Client *mautrix.Client
	Crypto *cryptohelper.CryptoHelper
}

// DirectRooms returns m.direct; M_NOT_FOUND is the documented "never set" answer and yields an empty map.
//
// Example: direct, err := api.DirectRooms(ctx).
func (m *MautrixAPI) DirectRooms(ctx context.Context) (map[id.UserID][]id.RoomID, error) {
	direct := map[id.UserID][]id.RoomID{}
	err := m.Client.GetAccountData(ctx, event.AccountDataDirectChats.Type, &direct)
	if errors.Is(err, mautrix.MNotFound) {
		return map[id.UserID][]id.RoomID{}, nil
	}
	if err != nil {
		return nil, classifyHTTP(err, CategoryRoom)
	}
	return direct, nil
}

// RoomState fetches /state, which also refreshes the client state store the crypto machine reads members from.
//
// Example: st, err := api.RoomState(ctx, room).
func (m *MautrixAPI) RoomState(
	ctx context.Context,
	room id.RoomID,
) (RoomState, error) {
	state, err := m.Client.State(ctx, room)
	if errors.Is(err, mautrix.MForbidden) || errors.Is(err, mautrix.MNotFound) {
		return RoomState{}, errRoomInaccessible
	}
	if err != nil {
		return RoomState{}, classifyHTTP(err, CategoryRoom)
	}
	st := RoomState{Members: map[id.UserID]event.Membership{}}
	for key, evt := range state[event.StateMember] {
		member := evt.Content.AsMember()
		if member.Membership == "" {
			return RoomState{}, categorize(CategoryRoom, errors.New("malformed member event"))
		}
		st.Members[id.UserID(key)] = member.Membership
	}
	if len(st.Members) == 0 {
		return RoomState{}, categorize(CategoryRoom, errors.New("room state has no members"))
	}
	if evt := state[event.StateEncryption][""]; evt != nil {
		st.Algorithm = evt.Content.AsEncryption().Algorithm
	}
	if evt := state[event.StateHistoryVisibility][""]; evt != nil {
		st.HistoryVisibility = evt.Content.AsHistoryVisibility().HistoryVisibility
	}
	return st, nil
}

// CreateDM creates a private_chat (not trusted_private_chat) encrypted direct room inviting only target.
//
// Example: room, err := api.CreateDM(ctx, target).
func (m *MautrixAPI) CreateDM(
	ctx context.Context,
	target id.UserID,
) (id.RoomID, error) {
	empty := ""
	resp, err := m.Client.CreateRoom(ctx, &mautrix.ReqCreateRoom{
		Visibility: "private",
		Preset:     "private_chat",
		IsDirect:   true,
		Invite:     []id.UserID{target},
		InitialState: []*event.Event{{
			Type:     event.StateEncryption,
			StateKey: &empty,
			Content:  event.Content{Parsed: &event.EncryptionEventContent{Algorithm: id.AlgorithmMegolmV1}},
		}},
	})
	if err != nil {
		return "", classifyHTTP(err, CategoryRoom)
	}
	return resp.RoomID, nil
}

// ShareRoomKey refreshes target's devices, shares the room's outbound Megolm session with the room's joined and
// invited members, and counts target devices that hold an Olm session: the SDK encrypts the room key to exactly
// those and withholds it from devices it could not open one with. A session already shared in an earlier run is not
// re-shared to a device whose Olm session appeared later; the SDK rotates the session when target's device list
// changes.
//
// Example: n, err := api.ShareRoomKey(ctx, room, target); n == 0 means key_pending.
func (m *MautrixAPI) ShareRoomKey(
	ctx context.Context,
	room id.RoomID,
	target id.UserID,
) (int, error) {
	if m.Crypto == nil {
		return 0, categorize(CategoryCrypto, errors.New("crypto not initialized"))
	}
	mach := m.Crypto.Machine()
	if _, err := mach.FetchKeys(ctx, []id.UserID{target}, true); err != nil {
		return 0, classifyHTTP(err, CategoryCrypto)
	}
	members, err := m.Client.StateStore.GetRoomJoinedOrInvitedMembers(ctx, room)
	if err != nil {
		return 0, categorize(CategoryCrypto, err)
	}
	if err := mach.ShareGroupSession(ctx, room, members); err != nil {
		return 0, classifyHTTP(err, CategoryCrypto)
	}
	devices, err := mach.CryptoStore.GetDevices(ctx, target)
	if err != nil {
		return 0, categorize(CategoryCrypto, err)
	}
	keyed := 0
	for _, device := range devices {
		session, err := mach.CryptoStore.GetLatestSession(ctx, device.IdentityKey)
		if err != nil {
			return 0, categorize(CategoryCrypto, err)
		}
		if session != nil {
			keyed++
		}
	}
	return keyed, nil
}

// SendEncrypted encrypts explicitly and sends only the m.room.encrypted event, so no code path emits plaintext.
//
// Example: ev, err := api.SendEncrypted(ctx, room, "hello").
func (m *MautrixAPI) SendEncrypted(
	ctx context.Context,
	room id.RoomID,
	text string,
) (id.EventID, error) {
	if m.Crypto == nil {
		return "", categorize(CategoryCrypto, errors.New("crypto not initialized"))
	}
	enc, err := m.Crypto.Encrypt(ctx, room, event.EventMessage, &event.MessageEventContent{MsgType: event.MsgText, Body: text})
	if err != nil {
		return "", classifyHTTP(err, CategoryCrypto)
	}
	resp, err := m.Client.SendMessageEvent(ctx, room, event.EventEncrypted, enc)
	if err != nil {
		return "", classifyHTTP(err, CategorySend)
	}
	return resp.EventID, nil
}

// classifyHTTP categorizes a homeserver call failure: rejected credentials are auth, failures without a Matrix
// response are network, and anything else is the caller's fallback.
//
// Example: classifyHTTP(err, CategorySend) for a failed /send.
func classifyHTTP(
	err error,
	fallback Category,
) error {
	var httpErr mautrix.HTTPError
	switch {
	case errors.Is(err, mautrix.MUnknownToken), errors.Is(err, mautrix.MForbidden):
		return categorize(CategoryAuth, err)
	case errors.As(err, &httpErr) && httpErr.RespError != nil:
		return categorize(fallback, err)
	case errors.As(err, &httpErr):
		return categorize(CategoryNetwork, err)
	default:
		return categorize(fallback, err)
	}
}
