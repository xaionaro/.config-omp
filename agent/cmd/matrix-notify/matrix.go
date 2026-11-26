package main

import (
	"context"
	"errors"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// privateChatPreset is the createRoom preset for a DM: invitees do not get the creator's power, unlike
// trusted_private_chat. mautrix v0.33 defines no constant for it.
//
// Example: ReqCreateRoom{Preset: privateChatPreset}.
const privateChatPreset = "private_chat"

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

// CreateDM creates a private, private_chat encrypted direct room inviting only recipient.
//
// Example: room, err := api.CreateDM(ctx, recipient).
func (m *MautrixAPI) CreateDM(
	ctx context.Context,
	recipient id.UserID,
) (id.RoomID, error) {
	empty := ""
	resp, err := m.Client.CreateRoom(ctx, &mautrix.ReqCreateRoom{
		Visibility: string(mautrix.RoomDirectoryVisibilityPrivate),
		Preset:     privateChatPreset,
		IsDirect:   true,
		Invite:     []id.UserID{recipient},
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

// ShareRoomKey refreshes recipient's devices, shares the room's outbound Megolm session with the room's joined and
// invited members, and reports whether the key was encrypted to at least one of recipient's devices: under the
// default SendKeysMinTrust those are exactly the devices with an Olm session. The SDK never re-shares a session it
// marked shared, even to a device that later becomes reachable, so when no recipient device got the key the session
// is removed and the next run claims keys and shares afresh.
//
// Example: keyed, err := api.ShareRoomKey(ctx, room, recipient); !keyed means key_pending.
func (m *MautrixAPI) ShareRoomKey(
	ctx context.Context,
	room id.RoomID,
	recipient id.UserID,
) (bool, error) {
	if m.Crypto == nil {
		return false, categorize(CategoryCrypto, errors.New("crypto not initialized"))
	}
	mach := m.Crypto.Machine()
	if _, err := mach.FetchKeys(ctx, []id.UserID{recipient}, true); err != nil {
		return false, classifyHTTP(err, CategoryCrypto)
	}
	members, err := m.Client.StateStore.GetRoomJoinedOrInvitedMembers(ctx, room)
	if err != nil {
		return false, categorize(CategoryCrypto, err)
	}
	if err := mach.ShareGroupSession(ctx, room, members); err != nil {
		return false, classifyHTTP(err, CategoryCrypto)
	}
	devices, err := mach.CryptoStore.GetDevices(ctx, recipient)
	if err != nil {
		return false, categorize(CategoryCrypto, err)
	}
	for _, device := range devices {
		session, err := mach.CryptoStore.GetLatestSession(ctx, device.IdentityKey)
		if err != nil {
			return false, categorize(CategoryCrypto, err)
		}
		if session != nil {
			return true, nil
		}
	}
	if err := mach.CryptoStore.RemoveOutboundGroupSession(ctx, room); err != nil {
		return false, categorize(CategoryCrypto, err)
	}
	return false, nil
}

// SendEncrypted encrypts explicitly and sends only the m.room.encrypted event, so no code path emits plaintext.
// Failures before the request and Matrix error answers are marked not stored; a transport failure or deadline
// during the request is left unmarked, because the homeserver may have stored the event.
//
// Example: ev, err := api.SendEncrypted(ctx, room, "hello").
func (m *MautrixAPI) SendEncrypted(
	ctx context.Context,
	room id.RoomID,
	text string,
) (id.EventID, error) {
	if m.Crypto == nil {
		return "", markNotStored(categorize(CategoryCrypto, errors.New("crypto not initialized")))
	}
	enc, err := m.Crypto.Encrypt(ctx, room, event.EventMessage, &event.MessageEventContent{MsgType: event.MsgText, Body: text})
	if err != nil {
		return "", markNotStored(classifyHTTP(err, CategoryCrypto))
	}
	resp, err := m.Client.SendMessageEvent(ctx, room, event.EventEncrypted, enc)
	var httpErr mautrix.HTTPError
	switch {
	case err == nil:
		return resp.EventID, nil
	case errors.As(err, &httpErr) && httpErr.RespError != nil:
		return "", markNotStored(classifyHTTP(err, CategorySend))
	default:
		return "", categorize(CategoryNetwork, err)
	}
}

// classifyHTTP categorizes a homeserver call failure: rejected credentials are auth, failures without a Matrix
// response are network, and anything else is the caller's fallback.
//
// Example: classifyHTTP(err, CategoryRoom) for a failed /state.
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
