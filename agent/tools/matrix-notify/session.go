package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"maunium.net/go/mautrix/id"
)

const (
	sessionFile   = "session.json"
	roomsFile     = "rooms.json"
	pickleKeyFile = "pickle.key"
	cryptoDBFile  = "crypto.db"
	lockFile      = "lock"
)

// Session is this tool's own Matrix login, persisted so each run reuses one device instead of logging in again.
type Session struct {
	Homeserver  string `json:"homeserver"`
	UserID      string `json:"user_id"`
	DeviceID    string `json:"device_id"`
	AccessToken string `json:"access_token"`
}

func ensureStateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

// lockStateDir serializes runs sharing one device and crypto store; the lock is released when the process exits.
func lockStateDir(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, lockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil
}

// loadSession returns nil without error when no session was saved.
func loadSession(dir string) (*Session, error) {
	b, err := os.ReadFile(filepath.Join(dir, sessionFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errState, err)
	}
	var s Session
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%w: parse %s: %w", errState, sessionFile, err)
	}
	return &s, nil
}

func saveSession(dir string, s Session) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return writePrivate(filepath.Join(dir, sessionFile), b)
}

// checkSession fails closed unless s (if any) is this origin's and account's own, non-Hermes device.
func checkSession(s *Session, creds HermesCredentials) error {
	if s == nil {
		return nil
	}
	if s.Homeserver != creds.Homeserver || s.UserID != creds.UserID {
		return fmt.Errorf("%w: saved session is for another homeserver or account", errIdentity)
	}
	if s.DeviceID == "" || s.DeviceID == creds.HermesDeviceID {
		return fmt.Errorf("%w: saved session has no own device", errIdentity)
	}
	return nil
}

// checkOrphanStore refuses a crypto store whose device is not recorded, since a login would reuse an unknown device ID.
func checkOrphanStore(dir string, s *Session) error {
	if s != nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, cryptoDBFile)); err == nil {
		return fmt.Errorf("%w: crypto store exists without a session", errIdentity)
	}
	return nil
}

// pickleKey returns the key encrypting the local olm store, creating it on first use.
func pickleKey(dir string) ([]byte, error) {
	path := filepath.Join(dir, pickleKeyFile)
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		return b, nil
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	b = make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, writePrivate(path, b)
}

// roomFile stores DM rooms this tool created, keyed by "origin|account" then target.
type roomFile struct {
	dir     string
	account string
}

func (r roomFile) load() (map[string]map[id.UserID][]id.RoomID, error) {
	all := map[string]map[id.UserID][]id.RoomID{}
	b, err := os.ReadFile(filepath.Join(r.dir, roomsFile))
	if errors.Is(err, os.ErrNotExist) {
		return all, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errState, err)
	}
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, fmt.Errorf("%w: parse %s: %w", errState, roomsFile, err)
	}
	return all, nil
}

func (r roomFile) Rooms(target id.UserID) ([]id.RoomID, error) {
	all, err := r.load()
	if err != nil {
		return nil, err
	}
	return all[r.account][target], nil
}

func (r roomFile) Add(target id.UserID, room id.RoomID) error {
	all, err := r.load()
	if err != nil {
		return err
	}
	if all[r.account] == nil {
		all[r.account] = map[id.UserID][]id.RoomID{}
	}
	all[r.account][target] = append(all[r.account][target], room)
	b, err := json.Marshal(all)
	if err != nil {
		return err
	}
	return writePrivate(filepath.Join(r.dir, roomsFile), b)
}

func writePrivate(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
