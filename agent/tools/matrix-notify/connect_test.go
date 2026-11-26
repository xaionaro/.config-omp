package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto"
	"maunium.net/go/mautrix/crypto/signatures"
	"maunium.net/go/mautrix/id"
)

const testRoom id.RoomID = "!r:hs"

// fakeHomeserver implements the Matrix endpoints connect, deliver and the crypto machine use, and records requests.
type fakeHomeserver struct {
	t            *testing.T
	srv          *httptest.Server
	mu           sync.Mutex
	loginDown    bool
	unknownToken bool
	whoamiDevice id.DeviceID
	logins       []map[string]any
	ownKeys      json.RawMessage
	targetKeys   map[id.DeviceID]*mautrix.DeviceKeys
	sentTypes    []string
	sentBodies   []string
	paths        []string
}

func newHomeserver(t *testing.T) *fakeHomeserver {
	hs := &fakeHomeserver{t: t, whoamiDevice: "OWNDEV", targetKeys: map[id.DeviceID]*mautrix.DeviceKeys{}}
	hs.srv = httptest.NewTLSServer(http.HandlerFunc(hs.serve))
	t.Cleanup(hs.srv.Close)
	return hs
}

func (hs *fakeHomeserver) creds() HermesCredentials {
	return HermesCredentials{Account: Account{Homeserver: hs.srv.URL, UserID: self}, Password: "pw", HermesDeviceID: "HERMESDEV"}
}

func (hs *fakeHomeserver) serve(w http.ResponseWriter, r *http.Request) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	p := r.URL.Path
	hs.paths = append(hs.paths, r.Method+" "+p)
	body, _ := io.ReadAll(r.Body)
	reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	switch {
	case strings.HasSuffix(p, "/v3/login"):
		if hs.loginDown {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		hs.logins = append(hs.logins, req)
		hs.unknownToken = false
		reply(map[string]any{"user_id": self, "device_id": "OWNDEV", "access_token": "tok-" + string(rune('a'+len(hs.logins)))})
	case strings.HasSuffix(p, "/account/whoami"):
		if hs.unknownToken {
			matrixError(w, 401, "M_UNKNOWN_TOKEN")
			return
		}
		reply(map[string]any{"user_id": self, "device_id": hs.whoamiDevice})
	case strings.HasSuffix(p, "/keys/upload"):
		var req struct {
			DeviceKeys json.RawMessage `json:"device_keys"`
		}
		_ = json.Unmarshal(body, &req)
		if len(req.DeviceKeys) > 0 && string(req.DeviceKeys) != "null" {
			hs.ownKeys = req.DeviceKeys
		}
		reply(map[string]any{"one_time_key_counts": map[string]int{"signed_curve25519": 50}})
	case strings.HasSuffix(p, "/keys/query"):
		var req struct {
			DeviceKeys map[id.UserID]any `json:"device_keys"`
		}
		_ = json.Unmarshal(body, &req)
		out := map[id.UserID]map[id.DeviceID]any{}
		for user := range req.DeviceKeys {
			out[user] = map[id.DeviceID]any{}
			if user == self && hs.ownKeys != nil {
				out[user]["OWNDEV"] = hs.ownKeys
			}
			if user == voidM {
				for dev, k := range hs.targetKeys {
					out[user][dev] = k
				}
			}
		}
		reply(map[string]any{"device_keys": out})
	case strings.HasSuffix(p, "/keys/claim"):
		reply(map[string]any{"one_time_keys": map[string]any{}})
	case strings.Contains(p, "/sendToDevice/"):
		reply(map[string]any{})
	case strings.HasSuffix(p, "/account_data/m.direct"):
		reply(map[id.UserID][]id.RoomID{voidM: {testRoom}})
	case strings.HasSuffix(p, "/rooms/"+testRoom.String()+"/state"):
		_, _ = io.WriteString(w, dmStateJSON(voidM, "join"))
	case strings.Contains(p, "/rooms/"+testRoom.String()+"/send/"):
		parts := strings.Split(p, "/")
		hs.sentTypes = append(hs.sentTypes, parts[len(parts)-2])
		hs.sentBodies = append(hs.sentBodies, string(body))
		reply(map[string]any{"event_id": "$ev"})
	default:
		hs.t.Errorf("unexpected request %s %s", r.Method, p)
		matrixError(w, 404, "M_UNRECOGNIZED")
	}
}

// addTargetDevice publishes validly signed device keys for void that have no claimable one-time key.
func (hs *fakeHomeserver) addTargetDevice(t *testing.T, device id.DeviceID) {
	acct := crypto.NewOlmAccount()
	signing, identity := acct.Keys()
	keys := &mautrix.DeviceKeys{
		UserID: voidM, DeviceID: device, Algorithms: []id.Algorithm{id.AlgorithmOlmV1, id.AlgorithmMegolmV1},
		Keys: mautrix.KeyMap{
			id.NewDeviceKeyID(id.KeyAlgorithmEd25519, device):    signing.String(),
			id.NewDeviceKeyID(id.KeyAlgorithmCurve25519, device): identity.String(),
		},
	}
	sig, err := acct.SignJSON(keys)
	if err != nil {
		t.Fatal(err)
	}
	keys.Signatures = signatures.NewSingleSignature(voidM, id.KeyAlgorithmEd25519, device.String(), sig)
	hs.mu.Lock()
	hs.targetKeys[device] = keys
	hs.mu.Unlock()
}

func (hs *fakeHomeserver) connect(t *testing.T, dir string) (*MautrixAPI, error) {
	t.Helper()
	api, err := connect(context.Background(), dir, hs.creds(), hs.srv.Client())
	if err == nil {
		t.Cleanup(func() { _ = api.Crypto.Close() })
	}
	return api, err
}

func TestConnectLogsInAsServerAssignedDeviceThenReuses(t *testing.T) {
	hs := newHomeserver(t)
	dir := t.TempDir()
	api, err := hs.connect(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if api.Client.DeviceID != "OWNDEV" || len(hs.logins) != 1 {
		t.Fatalf("device %s logins %d", api.Client.DeviceID, len(hs.logins))
	}
	if _, has := hs.logins[0]["device_id"]; has || hs.logins[0]["initial_device_display_name"] != deviceDisplayName {
		t.Fatalf("first login must let the server assign the device: %v", hs.logins[0])
	}
	if s, err := loadSession(dir); err != nil || s == nil || s.DeviceID != "OWNDEV" {
		t.Fatalf("session %v %v", s, err)
	}
	_ = api.Crypto.Close()
	api, err = hs.connect(t, dir)
	if err != nil || api.Client.DeviceID != "OWNDEV" || len(hs.logins) != 1 {
		t.Fatalf("reuse: %v logins %d", err, len(hs.logins))
	}
}

func TestConnectRenewsRejectedTokenForOwnDeviceOnly(t *testing.T) {
	hs := newHomeserver(t)
	dir := t.TempDir()
	api, err := hs.connect(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = api.Crypto.Close()
	hs.unknownToken = true
	if _, err := hs.connect(t, dir); err != nil {
		t.Fatal(err)
	}
	if len(hs.logins) != 2 || hs.logins[1]["device_id"] != "OWNDEV" {
		t.Fatalf("renewal logins %v", hs.logins)
	}
	if s, _ := loadSession(dir); s.AccessToken != "tok-c" {
		t.Fatal("renewed token not saved")
	}
}

func TestConnectFailsClosedOnIdentityMismatch(t *testing.T) {
	hs := newHomeserver(t)
	dir := t.TempDir()
	api, err := hs.connect(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = api.Crypto.Close()
	hs.whoamiDevice = "OTHERDEV"
	if _, err := hs.connect(t, dir); categoryOf(err) != CategoryIdentity {
		t.Fatalf("mismatch: %v", err)
	}
	if len(hs.logins) != 1 {
		t.Fatal("mismatch triggered a login")
	}
}

func TestFailedFirstLoginLeavesNoOrphanStore(t *testing.T) {
	hs := newHomeserver(t)
	dir := t.TempDir()
	hs.loginDown = true
	if _, err := hs.connect(t, dir); categoryOf(err) != CategoryNetwork {
		t.Fatalf("first: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, cryptoDBFile)); !os.IsNotExist(err) {
		t.Fatal("crypto store created before a session existed")
	}
	hs.loginDown = false
	if _, err := hs.connect(t, dir); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestRealPathKeyPendingThenCiphertextOnly(t *testing.T) {
	hs := newHomeserver(t)
	hs.addTargetDevice(t, "TDEV")
	api, err := hs.connect(t, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	out, err := deliver(context.Background(), api, memRooms{}, self, voidM, "hello plaintext")
	if err != nil || out.Status != StatusKeyPending {
		t.Fatalf("device without Olm session: %+v %v", out, err)
	}
	if devices, err := api.Crypto.Machine().CryptoStore.GetDevices(context.Background(), voidM); err != nil || devices["TDEV"] == nil {
		t.Fatalf("target device not known, so key_pending proves nothing: %v %v", devices, err)
	}
	if len(hs.sentTypes) != 0 {
		t.Fatalf("sent %v", hs.sentTypes)
	}
	ev, err := api.SendEncrypted(context.Background(), testRoom, "hello plaintext")
	if err != nil || ev != "$ev" {
		t.Fatalf("send: %v %v", ev, err)
	}
	if len(hs.sentTypes) != 1 || hs.sentTypes[0] != "m.room.encrypted" {
		t.Fatalf("event types %v", hs.sentTypes)
	}
	if strings.Contains(hs.sentBodies[0], "hello plaintext") || !strings.Contains(hs.sentBodies[0], `"ciphertext"`) {
		t.Fatalf("body is not ciphertext: %s", hs.sentBodies[0])
	}
}
