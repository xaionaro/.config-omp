package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"maunium.net/go/mautrix/id"
)

// Session is this tool's own Matrix login, persisted so each run reuses one device instead of logging in again.
//
// Example: Session{Account: creds.Account, DeviceID: "ABCDEF", AccessToken: token}.
type Session struct {
	Account
	DeviceID    id.DeviceID `json:"device_id"`
	AccessToken string      `json:"access_token"`
}

// loadSession returns the saved session, or nil without error when none was saved.
//
// Example: sess, err := loadSession(dir).
func loadSession(dir string) (*Session, error) {
	b, err := os.ReadFile(filepath.Join(dir, sessionFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, categorize(CategoryState, err)
	}
	var s Session
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, categorize(CategoryState, fmt.Errorf("parse %s: %w", sessionFile, err))
	}
	return &s, nil
}

// saveSession persists s privately.
//
// Example: saveSession(dir, Session{Account: acct, DeviceID: dev, AccessToken: tok}).
func saveSession(
	dir string,
	s Session,
) error {
	b, err := json.Marshal(s)
	if err != nil {
		return categorize(CategoryState, err)
	}
	return writePrivate(filepath.Join(dir, sessionFile), b)
}

// checkSession fails closed unless s (if any) is this origin's and account's own, non-Hermes device.
//
// Example: checkSession(sess, creds) rejects a session whose device is Hermes's.
func checkSession(
	s *Session,
	creds HermesCredentials,
) error {
	if s == nil {
		return nil
	}
	if s.Account != creds.Account {
		return categorize(CategoryIdentity, errors.New("saved session is for another homeserver or account"))
	}
	if s.DeviceID == "" || s.DeviceID == creds.HermesDeviceID {
		return categorize(CategoryIdentity, errors.New("saved session has no own device"))
	}
	return nil
}

// checkOrphanStore refuses a crypto store whose device is not recorded in a session. connect saves the session
// before it creates the store, so an orphan means outside interference. To recover, delete session.json, pickle.key,
// crypto.db* and rooms.json from the state directory, keeping bin/, and remove the stale "OMP Matrix notify" device
// in a Matrix client.
//
// Example: checkOrphanStore(dir, nil) fails when crypto.db exists.
func checkOrphanStore(
	dir string,
	s *Session,
) error {
	if s != nil {
		return nil
	}
	_, err := os.Stat(filepath.Join(dir, cryptoDBFile))
	switch {
	case err == nil:
		return categorize(CategoryIdentity, errors.New("crypto store exists without a session"))
	case errors.Is(err, os.ErrNotExist):
		return nil
	default:
		return categorize(CategoryState, err)
	}
}
