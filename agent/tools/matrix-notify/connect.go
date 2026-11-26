package main

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
)

// deviceDisplayName names this tool's own device in the account's device list.
//
// Example: the login request sets initial_device_display_name to deviceDisplayName.
const deviceDisplayName = "OMP Matrix notify"

// connect reuses this tool's saved device, or logs in with Hermes's password as a new server-assigned device. The
// session is saved before the crypto store is created, so a failed first login leaves nothing behind; a rejected
// token is renewed only for the saved device; any account or device mismatch fails closed without reset.
// httpClient may be nil for the default client.
//
// Example: api, err := connect(ctx, stateDir, creds, nil).
func connect(
	ctx context.Context,
	stateDir string,
	creds HermesCredentials,
	httpClient *http.Client,
) (*MautrixAPI, error) {
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
		return nil, categorize(CategoryCredentials, err)
	}
	if httpClient != nil {
		cli.Client = httpClient
	}
	needLogin := sess == nil
	if sess != nil {
		cli.UserID, cli.DeviceID, cli.AccessToken = sess.UserID, sess.DeviceID, sess.AccessToken
		who, err := cli.Whoami(ctx)
		switch {
		case err == nil && (who.UserID != sess.UserID || who.DeviceID != sess.DeviceID):
			return nil, categorize(CategoryIdentity, errors.New("token belongs to another account or device"))
		case err == nil:
		case errors.Is(err, mautrix.MUnknownToken):
			needLogin, cli.AccessToken = true, ""
		default:
			return nil, classifyHTTP(err, CategoryAuth)
		}
	}
	if needLogin {
		if err := login(ctx, cli, stateDir, creds, sess); err != nil {
			return nil, err
		}
	}
	key, err := loadOrCreatePickleKey(stateDir)
	if err != nil {
		return nil, err
	}
	ch, err := cryptohelper.NewCryptoHelper(cli, key, filepath.Join(stateDir, cryptoDBFile))
	if err != nil {
		return nil, categorize(CategoryState, err)
	}
	if err := ch.Init(ctx); err != nil {
		return nil, errors.Join(classifyHTTP(err, CategoryCrypto), ch.Close())
	}
	cli.Crypto = ch
	return &MautrixAPI{Client: cli, Crypto: ch}, nil
}

// login authenticates with Hermes's password — as a server-assigned device when prev is nil, else renewing prev's
// device — verifies the result and saves the session.
//
// Example: login(ctx, cli, dir, creds, nil) on first use.
func login(
	ctx context.Context,
	cli *mautrix.Client,
	stateDir string,
	creds HermesCredentials,
	prev *Session,
) error {
	req := &mautrix.ReqLogin{
		Type:                     mautrix.AuthTypePassword,
		Identifier:               mautrix.UserIdentifier{Type: mautrix.IdentifierTypeUser, User: creds.UserID.String()},
		Password:                 creds.Password,
		InitialDeviceDisplayName: deviceDisplayName,
		StoreCredentials:         true,
	}
	if prev != nil {
		req.DeviceID = prev.DeviceID
	}
	if _, err := cli.Login(ctx, req); err != nil {
		return classifyHTTP(err, CategoryAuth)
	}
	got := Session{Account: Account{Homeserver: creds.Homeserver, UserID: cli.UserID}, DeviceID: cli.DeviceID, AccessToken: cli.AccessToken}
	if err := checkSession(&got, creds); err != nil {
		return err
	}
	if prev != nil && got.DeviceID != prev.DeviceID {
		return categorize(CategoryIdentity, errors.New("renewal returned another device"))
	}
	return saveSession(stateDir, got)
}
