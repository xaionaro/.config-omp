package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"maunium.net/go/mautrix/id"
)

// State directory layout and modes. The directory holds secrets (token, pickle key, Olm store).
//
// Example: filepath.Join(stateDir, sessionFile).
const (
	sessionFile     = "session.json"
	roomsFile       = "rooms.json"
	pickleKeyFile   = "pickle.key"
	cryptoDBFile    = "crypto.db"
	lockFile        = "lock"
	dirMode         = 0o700
	fileMode        = 0o600
	pickleKeySize   = 32
	lockRetryPeriod = 100 * time.Millisecond
)

// ensureStateDir creates dir private to the user.
//
// Example: ensureStateDir("~/.omp/agent/matrix-notify").
func ensureStateDir(dir string) error {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return categorize(CategoryState, err)
	}
	return categorizeIfErr(CategoryState, os.Chmod(dir, dirMode))
}

// lockStateDir takes the exclusive lock serializing runs that share one device and crypto store, waiting no longer
// than ctx allows; closing the returned file releases it.
//
// Example: lock, err := lockStateDir(ctx, dir); defer lock.Close().
func lockStateDir(
	ctx context.Context,
	dir string,
) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, lockFile), os.O_CREATE|os.O_RDWR, fileMode)
	if err != nil {
		return nil, categorize(CategoryState, err)
	}
	ticker := time.NewTicker(lockRetryPeriod)
	defer ticker.Stop()
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, categorize(CategoryState, errors.Join(err, f.Close()))
		}
		select {
		case <-ctx.Done():
			return nil, categorize(CategoryState, errors.Join(fmt.Errorf("wait for state lock: %w", ctx.Err()), f.Close()))
		case <-ticker.C:
		}
	}
}

// loadOrCreatePickleKey returns the key encrypting the local Olm store, creating it on first use.
//
// Example: key, err := loadOrCreatePickleKey(dir).
func loadOrCreatePickleKey(dir string) ([]byte, error) {
	path := filepath.Join(dir, pickleKeyFile)
	b, err := os.ReadFile(path)
	switch {
	case err == nil && len(b) == pickleKeySize:
		return b, nil
	case err == nil:
		return nil, categorize(CategoryState, fmt.Errorf("%s has %d bytes, want %d", pickleKeyFile, len(b), pickleKeySize))
	case !errors.Is(err, os.ErrNotExist):
		return nil, categorize(CategoryState, err)
	}
	b = make([]byte, pickleKeySize)
	if _, err := rand.Read(b); err != nil {
		return nil, categorize(CategoryState, err)
	}
	return b, writePrivate(path, b)
}

// writePrivate atomically replaces path with b, readable only by the user.
//
// Example: writePrivate(filepath.Join(dir, sessionFile), data).
func writePrivate(
	path string,
	b []byte,
) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, fileMode); err != nil {
		return categorize(CategoryState, err)
	}
	return categorizeIfErr(CategoryState, os.Rename(tmp, path))
}

// RoomFile stores DM rooms this tool created, per account and target, in the state directory.
//
// Example: RoomFile{Dir: dir, Account: creds.Account}.Rooms(target).
type RoomFile struct {
	Dir     string
	Account Account
}

// load reads every account's saved rooms; a missing file is empty.
//
// Example: all, err := f.load(); all[f.Account.String()][target].
func (f RoomFile) load() (map[string]map[id.UserID][]id.RoomID, error) {
	all := map[string]map[id.UserID][]id.RoomID{}
	b, err := os.ReadFile(filepath.Join(f.Dir, roomsFile))
	if errors.Is(err, os.ErrNotExist) {
		return all, nil
	}
	if err != nil {
		return nil, categorize(CategoryState, err)
	}
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, categorize(CategoryState, fmt.Errorf("parse %s: %w", roomsFile, err))
	}
	return all, nil
}

// Rooms lists the rooms saved for target under this account.
//
// Example: saved, err := f.Rooms(target).
func (f RoomFile) Rooms(target id.UserID) ([]id.RoomID, error) {
	all, err := f.load()
	if err != nil {
		return nil, err
	}
	return all[f.Account.String()][target], nil
}

// Add saves room for target under this account.
//
// Example: f.Add(target, created).
func (f RoomFile) Add(
	target id.UserID,
	room id.RoomID,
) error {
	all, err := f.load()
	if err != nil {
		return err
	}
	key := f.Account.String()
	if all[key] == nil {
		all[key] = map[id.UserID][]id.RoomID{}
	}
	all[key][target] = append(all[key][target], room)
	b, err := json.Marshal(all)
	if err != nil {
		return categorize(CategoryState, err)
	}
	return writePrivate(filepath.Join(f.Dir, roomsFile), b)
}
