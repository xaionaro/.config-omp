package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/id"
)

func fakeServer(t *testing.T, h http.HandlerFunc) (*MautrixAPI, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	cli, err := mautrix.NewClient(srv.URL, sender, "tok")
	if err != nil {
		t.Fatal(err)
	}
	return &MautrixAPI{Client: cli}, &n
}

func matrixError(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `{"errcode":"`+code+`","error":"x"}`)
}

func TestSendWithoutCryptoSendsNothing(t *testing.T) {
	api, n := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) { t.Error("request made") })
	_, err := api.SendEncrypted(context.Background(), "!r:hs", "secret text")
	var notStored *NotStoredError
	if categoryOf(err) != CategoryCrypto || !errors.As(err, &notStored) {
		t.Fatalf("plaintext path allowed, or failure not marked as not stored: %v", err)
	}
	if _, err := api.ShareRoomKey(context.Background(), "!r:hs", voidM); categoryOf(err) != CategoryCrypto {
		t.Fatalf("share without crypto: %v", err)
	}
	if n.Load() != 0 {
		t.Fatalf("%d requests", n.Load())
	}
}

func TestDirectRoomsAbsenceVersusFailure(t *testing.T) {
	api, _ := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) { matrixError(w, 404, "M_NOT_FOUND") })
	if d, err := api.DirectRooms(context.Background()); err != nil || len(d) != 0 {
		t.Fatalf("%v %v", d, err)
	}
	// RT2: a 403 on a room call is a room failure, neither absence nor a credential rejection.
	api, _ = fakeServer(t, func(w http.ResponseWriter, _ *http.Request) { matrixError(w, 403, "M_FORBIDDEN") })
	if _, err := api.DirectRooms(context.Background()); categoryOf(err) != CategoryRoom {
		t.Fatalf("m.direct 403: %v", err)
	}
	if _, err := api.CreateDM(context.Background(), vicM); categoryOf(err) != CategoryRoom {
		t.Fatalf("createRoom 403: %v", err)
	}
}

func TestRoomStateClassification(t *testing.T) {
	api, _ := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) { matrixError(w, 403, "M_FORBIDDEN") })
	if _, err := api.RoomState(context.Background(), "!r:hs"); !errors.Is(err, errRoomInaccessible) {
		t.Fatalf("403: %v", err)
	}
	api, _ = fakeServer(t, func(w http.ResponseWriter, _ *http.Request) { matrixError(w, 500, "M_UNKNOWN") })
	if _, err := api.RoomState(context.Background(), "!r:hs"); categoryOf(err) != CategoryRoom || errors.Is(err, errRoomInaccessible) {
		t.Fatalf("500: %v", err)
	}
	api, _ = fakeServer(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, dmStateJSON(voidM, "invite")) })
	st, err := api.RoomState(context.Background(), "!r:hs")
	if err != nil || st.Algorithm != id.AlgorithmMegolmV1 || st.Members[voidM] != "invite" || st.Members[sender] != "join" || st.HistoryVisibility != "shared" {
		t.Fatalf("%+v %v", st, err)
	}
	api, _ = fakeServer(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `[]`) })
	if _, err := api.RoomState(context.Background(), "!r:hs"); categoryOf(err) != CategoryRoom {
		t.Fatalf("memberless state accepted: %v", err)
	}
}

func TestTransportFailureIsNetwork(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	cli, err := mautrix.NewClient(srv.URL, sender, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&MautrixAPI{Client: cli}).DirectRooms(context.Background()); categoryOf(err) != CategoryNetwork {
		t.Fatalf("%v", err)
	}
}

func TestCreateDMRequestIsPrivateEncryptedSingleInvite(t *testing.T) {
	var body map[string]any
	api, _ := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/createRoom") {
			t.Errorf("path %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, `{"room_id":"!new:hs"}`)
	})
	room, err := api.CreateDM(context.Background(), vicM)
	if err != nil || room != "!new:hs" {
		t.Fatalf("%v %v", room, err)
	}
	b, _ := json.Marshal(body)
	for _, want := range []string{`"visibility":"private"`, `"preset":"private_chat"`, `"is_direct":true`, `"invite":["@danusya:matrix.dx.center"]`, `"algorithm":"m.megolm.v1.aes-sha2"`, `"type":"m.room.encryption"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
	if strings.Contains(string(b), "trusted_private_chat") {
		t.Error("trusted preset used")
	}
}

// dmStateJSON is the /state response of an encrypted DM between the sender and recipient.
func dmStateJSON(recipient id.UserID, membership string) string {
	return `[
{"type":"m.room.member","state_key":"` + sender.String() + `","sender":"` + sender.String() + `","content":{"membership":"join"}},
{"type":"m.room.member","state_key":"` + recipient.String() + `","sender":"` + sender.String() + `","content":{"membership":"` + membership + `"}},
{"type":"m.room.encryption","state_key":"","sender":"` + sender.String() + `","content":{"algorithm":"m.megolm.v1.aes-sha2"}},
{"type":"m.room.history_visibility","state_key":"","sender":"` + sender.String() + `","content":{"history_visibility":"shared"}}]`
}
