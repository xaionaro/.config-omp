package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type mautrixAPI struct {
	cli    *mautrix.Client
	crypto *cryptohelper.CryptoHelper
}

func (m *mautrixAPI) DirectRooms(ctx context.Context) (map[id.UserID][]id.RoomID, error) {
	direct := map[id.UserID][]id.RoomID{}
	err := m.cli.GetAccountData(ctx, event.AccountDataDirectChats.Type, &direct)
	if errors.Is(err, mautrix.MNotFound) {
		return map[id.UserID][]id.RoomID{}, nil
	}
	if err != nil {
		return nil, classifyHTTP(err, "read m.direct")
	}
	return direct, nil
}

func (m *mautrixAPI) RoomState(ctx context.Context, room id.RoomID) (roomState, error) {
	state, err := m.cli.State(ctx, room)
	if errors.Is(err, mautrix.MForbidden) || errors.Is(err, mautrix.MNotFound) {
		return roomState{}, errRoomInaccessible
	}
	if err != nil {
		return roomState{}, classifyHTTP(err, "read room state")
	}
	st := roomState{Members: map[id.UserID]event.Membership{}}
	for key, evt := range state[event.StateMember] {
		member := evt.Content.AsMember()
		if member.Membership == "" {
			return roomState{}, fmt.Errorf("%w: malformed member event", errRoom)
		}
		st.Members[id.UserID(key)] = member.Membership
	}
	if len(st.Members) == 0 {
		return roomState{}, fmt.Errorf("%w: room state has no members", errRoom)
	}
	if evt := state[event.StateEncryption][""]; evt != nil {
		st.Algorithm = evt.Content.AsEncryption().Algorithm
	}
	if evt := state[event.StateHistoryVisibility][""]; evt != nil {
		st.HistoryVisibility = evt.Content.AsHistoryVisibility().HistoryVisibility
	}
	return st, nil
}

func (m *mautrixAPI) CreateDM(ctx context.Context, target id.UserID) (id.RoomID, error) {
	empty := ""
	resp, err := m.cli.CreateRoom(ctx, &mautrix.ReqCreateRoom{
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
		return "", classifyHTTP(err, "create room")
	}
	return resp.RoomID, nil
}

func (m *mautrixAPI) TargetDevices(ctx context.Context, target id.UserID) (int, error) {
	if m.crypto == nil {
		return 0, fmt.Errorf("%w: crypto not initialized", errCrypto)
	}
	devices, err := m.crypto.Machine().FetchKeys(ctx, []id.UserID{target}, true)
	if err != nil {
		return 0, err
	}
	return len(devices[target]), nil
}

// SendEncrypted encrypts explicitly and sends only the m.room.encrypted event, so no code path emits plaintext.
func (m *mautrixAPI) SendEncrypted(ctx context.Context, room id.RoomID, text string) (id.EventID, error) {
	if m.crypto == nil {
		return "", fmt.Errorf("%w: crypto not initialized", errCrypto)
	}
	enc, err := m.crypto.Encrypt(ctx, room, event.EventMessage, &event.MessageEventContent{MsgType: event.MsgText, Body: text})
	if err != nil {
		return "", fmt.Errorf("%w: encrypt: %w", errCrypto, err)
	}
	resp, err := m.cli.SendMessageEvent(ctx, room, event.EventEncrypted, enc)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errSend, classifyHTTP(err, "send"))
	}
	return resp.EventID, nil
}

// classifyHTTP maps a homeserver failure onto auth or network categories.
func classifyHTTP(err error, what string) error {
	if errors.Is(err, mautrix.MUnknownToken) || errors.Is(err, mautrix.MForbidden) {
		return fmt.Errorf("%w: %s: %w", errAuth, what, err)
	}
	return fmt.Errorf("%w: %s: %w", errNetwork, what, err)
}

// connect reuses this tool's own saved device, or logs in with Hermes's password as a new server-assigned device.
func connect(ctx context.Context, stateDir string, creds HermesCredentials) (*mautrixAPI, error) {
	sess, err := loadSession(stateDir)
	if err != nil {
		return nil, err
	}
	if err := checkSession(sess, creds); err != nil {
		return nil, err
	}
	if err := checkOrphanStore(stateDir, sess); err != nil {
		return nil, err
	}
	cli, err := mautrix.NewClient(creds.Homeserver, "", "")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errCredentials, err)
	}
	needLogin := sess == nil
	if sess != nil {
		cli.UserID, cli.DeviceID, cli.AccessToken = id.UserID(sess.UserID), id.DeviceID(sess.DeviceID), sess.AccessToken
		who, err := cli.Whoami(ctx)
		switch {
		case err == nil:
			if who.UserID != cli.UserID || who.DeviceID != cli.DeviceID {
				return nil, fmt.Errorf("%w: token belongs to another account or device", errIdentity)
			}
		case errors.Is(err, mautrix.MUnknownToken):
			// Our token was revoked: renew it for our own device only.
			needLogin, cli.AccessToken = true, ""
		default:
			return nil, classifyHTTP(err, "whoami")
		}
	}
	key, err := pickleKey(stateDir)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errState, err)
	}
	ch, err := cryptohelper.NewCryptoHelper(cli, key, filepath.Join(stateDir, cryptoDBFile))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errState, err)
	}
	if needLogin {
		ch.LoginAs = &mautrix.ReqLogin{
			Type:                     mautrix.AuthTypePassword,
			Identifier:               mautrix.UserIdentifier{Type: mautrix.IdentifierTypeUser, User: creds.UserID},
			Password:                 creds.Password,
			InitialDeviceDisplayName: "OMP Matrix notify",
		}
		if sess != nil {
			ch.LoginAs.DeviceID = id.DeviceID(sess.DeviceID)
		}
	}
	if err := ch.Init(ctx); err != nil {
		_ = ch.Close()
		if errors.Is(err, mautrix.MForbidden) || errors.Is(err, mautrix.MUnknownToken) {
			return nil, fmt.Errorf("%w: login: %w", errAuth, err)
		}
		return nil, fmt.Errorf("%w: crypto init: %w", errCrypto, err)
	}
	cli.Crypto = ch
	got := Session{Homeserver: creds.Homeserver, UserID: cli.UserID.String(), DeviceID: cli.DeviceID.String(), AccessToken: cli.AccessToken}
	if err := checkSession(&got, creds); err != nil || (sess != nil && got.DeviceID != sess.DeviceID) {
		_ = ch.Close()
		return nil, fmt.Errorf("%w: login returned an unexpected account or device", errIdentity)
	}
	if needLogin {
		if err := saveSession(stateDir, got); err != nil {
			_ = ch.Close()
			return nil, fmt.Errorf("%w: %w", errState, err)
		}
	}
	return &mautrixAPI{cli: cli, crypto: ch}, nil
}
