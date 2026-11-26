package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"maunium.net/go/mautrix/id"
)

var testAccount = Account{Homeserver: "https://hs", UserID: "@b:hs"}

func TestSessionRoundTripIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := ensureStateDir(dir); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != dirMode {
		t.Fatalf("state dir mode: %v", err)
	}
	if s, err := loadSession(dir); err != nil || s != nil {
		t.Fatalf("empty dir: %v %v", s, err)
	}
	want := Session{Account: testAccount, DeviceID: "OWNDEV", AccessToken: "tok"}
	if err := saveSession(dir, want); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(dir, sessionFile)); err != nil || fi.Mode().Perm() != fileMode {
		t.Fatalf("session file mode: %v", err)
	}
	if got, err := loadSession(dir); err != nil || got == nil || *got != want {
		t.Fatalf("round trip: %v", err)
	}
}

func TestPickleKeyIsStableAndValidated(t *testing.T) {
	dir := t.TempDir()
	a, err := loadOrCreatePickleKey(dir)
	if err != nil || len(a) != pickleKeySize {
		t.Fatalf("key: %d %v", len(a), err)
	}
	if b, err := loadOrCreatePickleKey(dir); err != nil || string(a) != string(b) {
		t.Fatal("pickle key changed between calls")
	}
	if err := os.WriteFile(filepath.Join(dir, pickleKeyFile), []byte("short"), fileMode); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreatePickleKey(dir); categoryOf(err) != CategoryState {
		t.Fatalf("short key accepted: %v", err)
	}
}

func TestCheckSessionIsolatesAccountAndDevice(t *testing.T) {
	creds := HermesCredentials{Account: testAccount, HermesDeviceID: "HD"}
	if err := checkSession(&Session{Account: testAccount, DeviceID: "OWN"}, creds); err != nil {
		t.Fatal(err)
	}
	if err := checkSession(nil, creds); err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]*Session{
		"origin":        {Account: Account{Homeserver: "https://other", UserID: "@b:hs"}, DeviceID: "OWN"},
		"account":       {Account: Account{Homeserver: "https://hs", UserID: "@c:hs"}, DeviceID: "OWN"},
		"hermes device": {Account: testAccount, DeviceID: "HD"},
		"no device":     {Account: testAccount},
	} {
		if err := checkSession(s, creds); categoryOf(err) != CategoryIdentity {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
}

func TestOrphanCryptoStoreFailsClosed(t *testing.T) {
	dir := t.TempDir()
	if err := checkOrphanStore(dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, cryptoDBFile), nil, fileMode); err != nil {
		t.Fatal(err)
	}
	if err := checkOrphanStore(dir, nil); categoryOf(err) != CategoryIdentity {
		t.Fatalf("orphan store accepted: %v", err)
	}
	if err := checkOrphanStore(dir, &Session{DeviceID: "OWN"}); err != nil {
		t.Fatal(err)
	}
}

func TestRoomFileIsBoundToAccountAndPersists(t *testing.T) {
	dir := t.TempDir()
	a := RoomFile{Dir: dir, Account: testAccount}
	if err := a.Add(voidM, "!r:hs"); err != nil {
		t.Fatal(err)
	}
	if got, err := (RoomFile{Dir: dir, Account: testAccount}).Rooms(voidM); err != nil || !slices.Equal(got, []id.RoomID{"!r:hs"}) {
		t.Fatalf("%v %v", got, err)
	}
	if got, _ := (RoomFile{Dir: dir, Account: Account{Homeserver: "https://hs", UserID: "@c:hs"}}).Rooms(voidM); len(got) != 0 {
		t.Fatalf("other account sees %v", got)
	}
	if got, _ := a.Rooms(vicM); len(got) != 0 {
		t.Fatalf("other target sees %v", got)
	}
	if fi, err := os.Stat(filepath.Join(dir, roomsFile)); err != nil || fi.Mode().Perm() != fileMode {
		t.Fatalf("rooms file mode: %v", err)
	}
}

func TestLockHonorsDeadlineAndReleases(t *testing.T) {
	dir := t.TempDir()
	held, err := lockStateDir(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := lockStateDir(ctx, dir); !errors.Is(err, context.DeadlineExceeded) || categoryOf(err) != CategoryState {
		t.Fatalf("second lock: %v", err)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := lockStateDir(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = again.Close()
}
