package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"maunium.net/go/mautrix/id"
)

func TestSessionRoundTripIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := ensureStateDir(dir); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("state dir mode: %v", err)
	}
	if s, err := loadSession(dir); err != nil || s != nil {
		t.Fatalf("empty dir: %v %v", s, err)
	}
	want := Session{Homeserver: "https://hs", UserID: "@b:hs", DeviceID: "OWNDEV", AccessToken: "tok"}
	if err := saveSession(dir, want); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(dir, sessionFile)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("session file mode: %v", err)
	}
	if got, err := loadSession(dir); err != nil || got == nil || *got != want {
		t.Fatalf("round trip: %v", err)
	}
}

func TestPickleKeyIsStable(t *testing.T) {
	dir := t.TempDir()
	a, err := pickleKey(dir)
	if err != nil || len(a) < 32 {
		t.Fatalf("key: %d %v", len(a), err)
	}
	if b, err := pickleKey(dir); err != nil || string(a) != string(b) {
		t.Fatal("pickle key changed between calls")
	}
}

func TestCheckSessionIsolatesAccountAndDevice(t *testing.T) {
	creds := HermesCredentials{Homeserver: "https://hs", UserID: "@b:hs", HermesDeviceID: "HD"}
	ok := &Session{Homeserver: "https://hs", UserID: "@b:hs", DeviceID: "OWN"}
	if err := checkSession(ok, creds); err != nil {
		t.Fatal(err)
	}
	if err := checkSession(nil, creds); err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]*Session{
		"origin":        {Homeserver: "https://other", UserID: "@b:hs", DeviceID: "OWN"},
		"account":       {Homeserver: "https://hs", UserID: "@c:hs", DeviceID: "OWN"},
		"hermes device": {Homeserver: "https://hs", UserID: "@b:hs", DeviceID: "HD"},
		"no device":     {Homeserver: "https://hs", UserID: "@b:hs"},
	} {
		if err := checkSession(s, creds); !errors.Is(err, errIdentity) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
}

func TestOrphanCryptoStoreFailsClosed(t *testing.T) {
	dir := t.TempDir()
	if err := checkOrphanStore(dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, cryptoDBFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkOrphanStore(dir, nil); !errors.Is(err, errIdentity) {
		t.Fatalf("orphan store accepted: %v", err)
	}
	if err := checkOrphanStore(dir, &Session{DeviceID: "OWN"}); err != nil {
		t.Fatal(err)
	}
}

func TestRoomFileIsBoundToAccountAndPersists(t *testing.T) {
	dir := t.TempDir()
	a := roomFile{dir: dir, account: "https://hs|@b:hs"}
	if err := a.Add(voidM, "!r:hs"); err != nil {
		t.Fatal(err)
	}
	if got, err := (roomFile{dir: dir, account: "https://hs|@b:hs"}).Rooms(voidM); err != nil || !slices.Equal(got, []id.RoomID{"!r:hs"}) {
		t.Fatalf("%v %v", got, err)
	}
	if got, _ := (roomFile{dir: dir, account: "https://hs|@c:hs"}).Rooms(voidM); len(got) != 0 {
		t.Fatalf("other account sees %v", got)
	}
	if got, _ := a.Rooms(vicM); len(got) != 0 {
		t.Fatalf("other target sees %v", got)
	}
	if fi, err := os.Stat(filepath.Join(dir, roomsFile)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("rooms file mode: %v", err)
	}
}
