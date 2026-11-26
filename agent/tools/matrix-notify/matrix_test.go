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

func fakeServer(t *testing.T, h http.HandlerFunc) (*mautrixAPI, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	cli, err := mautrix.NewClient(srv.URL, self, "tok")
	if err != nil {
		t.Fatal(err)
	}
	return &mautrixAPI{cli: cli}, &n
}

func matrixError(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `{"errcode":"`+code+`","error":"x"}`)
}

func TestSendEncryptedWithoutCryptoSendsNothing(t *testing.T) {
	api, n := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) { t.Error("request made") })
	if _, err := api.SendEncrypted(context.Background(), "!r:hs", "secret text"); err == nil {
		t.Fatal("plaintext path allowed")
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
	api, _ = fakeServer(t, func(w http.ResponseWriter, _ *http.Request) { matrixError(w, 403, "M_FORBIDDEN") })
	if _, err := api.DirectRooms(context.Background()); err == nil {
		t.Fatal("forbidden treated as absence")
	}
}

func TestRoomStateClassification(t *testing.T) {
	api, _ := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) { matrixError(w, 403, "M_FORBIDDEN") })
	if _, err := api.RoomState(context.Background(), "!r:hs"); !errors.Is(err, errRoomInaccessible) {
		t.Fatalf("403: %v", err)
	}
	api, _ = fakeServer(t, func(w http.ResponseWriter, _ *http.Request) { matrixError(w, 500, "M_UNKNOWN") })
	if _, err := api.RoomState(context.Background(), "!r:hs"); err == nil || errors.Is(err, errRoomInaccessible) {
		t.Fatalf("500: %v", err)
	}
	api, _ = fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[
{"type":"m.room.member","state_key":"@hermes:hs","sender":"@hermes:hs","content":{"membership":"join"}},
{"type":"m.room.member","state_key":"@xaionaro:matrix.dx.center","sender":"@hermes:hs","content":{"membership":"invite"}},
{"type":"m.room.encryption","state_key":"","sender":"@hermes:hs","content":{"algorithm":"m.megolm.v1.aes-sha2"}},
{"type":"m.room.history_visibility","state_key":"","sender":"@hermes:hs","content":{"history_visibility":"shared"}}]`)
	})
	st, err := api.RoomState(context.Background(), "!r:hs")
	if err != nil || st.Algorithm != id.AlgorithmMegolmV1 || st.Members[voidM] != "invite" || st.Members[self] != "join" || st.HistoryVisibility != "shared" {
		t.Fatalf("%+v %v", st, err)
	}
	api, _ = fakeServer(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `[]`) })
	if _, err := api.RoomState(context.Background(), "!r:hs"); err == nil {
		t.Fatal("memberless state accepted")
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
