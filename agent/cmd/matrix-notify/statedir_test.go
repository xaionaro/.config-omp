package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// T4: the lock waits on the kernel's release event, is bounded by ctx, and an abandoned wait never keeps the lock.
func TestLockWaitsForReleaseWithinDeadline(t *testing.T) {
	dir := t.TempDir()
	held, err := lockStateDir(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := lockStateDir(ctx, dir); !errors.Is(err, context.DeadlineExceeded) || categoryOf(err) != CategoryState {
		t.Fatalf("(a) second lock: %v", err)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	// (b) The abandoned waiter may take the lock late; it must release it at once, so this bounded acquire succeeds.
	bounded, cancelB := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelB()
	again, err := lockStateDir(bounded, dir)
	if err != nil {
		t.Fatalf("(b) after abandoned wait: %v", err)
	}
	// (c) A blocked waiter acquires once the holder releases. The release is scheduled on a real timer because the
	// waiter's blocking flock is exactly what is under test; the 5 s bound turns a regression into a failure.
	time.AfterFunc(50*time.Millisecond, func() { _ = again.Close() })
	waiter, cancelC := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelC()
	last, err := lockStateDir(waiter, dir)
	if err != nil {
		t.Fatalf("(c) waiter: %v", err)
	}
	_ = last.Close()
}

// T8: rooms saved under the 603921c key format ("homeserver|user") still load.
func TestRoomsFromPreviousFormatStillLoad(t *testing.T) {
	dir := t.TempDir()
	old := `{"https://hs|@b:hs":{"@xaionaro:matrix.dx.center":["!old:hs"]}}`
	if err := os.WriteFile(filepath.Join(dir, roomsFile), []byte(old), fileMode); err != nil {
		t.Fatal(err)
	}
	got, err := (RoomFile{Dir: dir, Account: testAccount}).Rooms(voidM)
	if err != nil || !slices.Equal(got, []id.RoomID{"!old:hs"}) {
		t.Fatalf("%v %v", got, err)
	}
	if got, _ := (RoomFile{Dir: dir, Account: testAccount}).Rooms(vicM); len(got) != 0 {
		t.Fatalf("other recipient sees %v", got)
	}
}

// T5: secrets never appear in fmt output, while session.json keeps the raw token in the 603921c format.
func TestSecretsRedactInFormattingButPersistRaw(t *testing.T) {
	creds := HermesCredentials{Account: testAccount, Password: "hunter2-pw", HermesDeviceID: "HD"}
	sess := Session{Account: testAccount, DeviceID: "OWN", AccessToken: "syt_token_value"}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d"} {
		out := fmt.Sprintf(verb+" "+verb, creds, sess)
		if strings.Contains(out, "hunter2") || strings.Contains(out, "syt_token") ||
			strings.Contains(out, fmt.Sprintf("%x", "hunter2-pw")) || strings.Contains(out, fmt.Sprintf("%x", "syt_token_value")) {
			t.Errorf("%s leaked: %s", verb, out)
		}
	}
	b, err := json.Marshal(sess)
	if err != nil || !strings.Contains(string(b), `"access_token":"syt_token_value"`) {
		t.Fatalf("json %s %v", b, err)
	}
	dir := t.TempDir()
	old := `{"homeserver":"https://hs","user_id":"@b:hs","device_id":"OWN","access_token":"syt_token_value"}`
	if err := os.WriteFile(filepath.Join(dir, sessionFile), []byte(old), fileMode); err != nil {
		t.Fatal(err)
	}
	got, err := loadSession(dir)
	if err != nil || got == nil || got.AccessToken != "syt_token_value" || got.Account != testAccount {
		t.Fatalf("old session: %v", err)
	}
}
