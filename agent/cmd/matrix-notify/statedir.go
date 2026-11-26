package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/xaionaro-go/observability"
	"golang.org/x/sys/unix"
	"maunium.net/go/mautrix/id"
)

// State directory layout and modes. The directory holds secrets (token, pickle key, Olm store, saved rooms).
//
// Example: filepath.Join(stateDir, sessionFile).
const (
	// sessionFile holds this tool's own device ID and access token.
	//
	// Example: filepath.Join(stateDir, sessionFile).
	sessionFile = "session.json"
	// roomsFile lists the DM rooms this tool created, per account and recipient.
	//
	// Example: RoomFile reads filepath.Join(dir, roomsFile).
	roomsFile = "rooms.json"
	// pickleKeyFile holds the key encrypting the Olm store.
	//
	// Example: loadOrCreatePickleKey(dir) reads it.
	pickleKeyFile = "pickle.key"
	// cryptoDBFile is the SQLite Olm and state store.
	//
	// Example: cryptohelper.NewCryptoHelper(cli, key, filepath.Join(dir, cryptoDBFile)).
	cryptoDBFile = "crypto.db"
	// lockFile is flocked to serialize runs sharing the device and store.
	//
	// Example: lockStateDir(ctx, dir).
	lockFile = "lock"
	// tempSuffix names the file writePrivate writes before renaming it into place.
	//
	// Example: session.json.tmp.
	tempSuffix = ".tmp"
	// dirMode makes the state directory private to the user.
	//
	// Example: os.MkdirAll(dir, dirMode).
	dirMode = 0o700
	// fileMode makes state files private to the user.
	//
	// Example: os.OpenFile(path, flags, fileMode).
	fileMode = 0o600
	// pickleKeySize is the pickle key's length in bytes.
	//
	// Example: a pickle.key of another length fails with CategoryState.
	pickleKeySize = 32
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

// flockExclusive blocks until the kernel grants an exclusive flock on fd, retrying when a signal interrupts the wait.
//
// Example: err := flockExclusive(fd) inside SyscallConn().Control.
func flockExclusive(fd uintptr) error {
	for {
		err := unix.Flock(int(fd), unix.LOCK_EX)
		if !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}

// lockStateDir takes the exclusive lock serializing runs that share one device and crypto store; closing the returned
// file releases it. It waits on the kernel's release event (a blocking flock on its own goroutine), bounded by ctx.
// The flock runs inside the file's SyscallConn().Control, which keeps the descriptor open until flock returns, so
// closing the file when ctx ends defers the real close until then: a lock the abandoned wait takes late is released
// at once and never outlives this call.
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
	raw, err := f.SyscallConn()
	if err != nil {
		return nil, categorize(CategoryState, errors.Join(err, f.Close()))
	}
	acquired := make(chan error, 1)
	waitForLock :=
		// waitForLock blocks until the lock is granted and reports flock's result on acquired.
		//
		// Example: observability.Go(ctx, waitForLock).
		func(context.Context) {
			var flockErr error
			controlErr := raw.Control(
				// The Control callback runs flock on the live descriptor.
				//
				// Example: raw.Control(func(fd uintptr) { flockErr = flockExclusive(fd) }).
				func(fd uintptr) { flockErr = flockExclusive(fd) },
			)
			acquired <- errors.Join(controlErr, flockErr)
		}
	observability.Go(ctx, waitForLock)
	select {
	case err := <-acquired:
		if err != nil {
			return nil, categorize(CategoryState, errors.Join(err, f.Close()))
		}
		return f, nil
	case <-ctx.Done():
		return nil, categorize(CategoryState, errors.Join(fmt.Errorf("wait for state lock: %w", ctx.Err()), f.Close()))
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

// writePrivate durably and atomically replaces path with b, readable only by the user: it fsyncs the temp file
// before the rename and the directory after it. Callers hold the state lock, which makes the fixed temp name safe.
//
// Example: writePrivate(filepath.Join(dir, sessionFile), data).
func writePrivate(
	path string,
	b []byte,
) error {
	tmp := path + tempSuffix
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fileMode)
	if err != nil {
		return categorize(CategoryState, err)
	}
	_, err = f.Write(b)
	if err := errors.Join(err, f.Sync(), f.Close()); err != nil {
		return categorize(CategoryState, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return categorize(CategoryState, err)
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return categorize(CategoryState, err)
	}
	return categorizeIfErr(CategoryState, errors.Join(dir.Sync(), dir.Close()))
}

// RoomFile stores DM rooms this tool created, per account and recipient, in the state directory.
//
// Example: RoomFile{Dir: dir, Account: creds.Account}.Rooms(recipient).
type RoomFile struct {
	Dir     string
	Account Account
}

// load reads every account's saved rooms, keyed by Account.RoomsKey; a missing file is empty.
//
// Example: all, err := f.load(); all[f.Account.RoomsKey()][recipient].
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

// Rooms lists the rooms saved for recipient under this account.
//
// Example: saved, err := f.Rooms(recipient).
func (f RoomFile) Rooms(recipient id.UserID) ([]id.RoomID, error) {
	all, err := f.load()
	if err != nil {
		return nil, err
	}
	return all[f.Account.RoomsKey()][recipient], nil
}

// Add saves room for recipient under this account; callers hold the state lock.
//
// Example: f.Add(recipient, created).
func (f RoomFile) Add(
	recipient id.UserID,
	room id.RoomID,
) error {
	all, err := f.load()
	if err != nil {
		return err
	}
	key := f.Account.RoomsKey()
	if all[key] == nil {
		all[key] = map[id.UserID][]id.RoomID{}
	}
	all[key][recipient] = append(all[key][recipient], room)
	b, err := json.Marshal(all)
	if err != nil {
		return categorize(CategoryState, err)
	}
	return writePrivate(filepath.Join(f.Dir, roomsFile), b)
}
