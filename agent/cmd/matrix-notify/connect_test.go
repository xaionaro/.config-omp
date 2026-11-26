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
	targetAccts  map[id.DeviceID]*crypto.OlmAccount
	offerOTK     bool
	claims       int
	sendFault    sendFault
}

func newHomeserver(t *testing.T) *fakeHomeserver {
	hs := &fakeHomeserver{
		t: t, whoamiDevice: "OWNDEV",
		targetKeys: map[id.DeviceID]*mautrix.DeviceKeys{}, targetAccts: map[id.DeviceID]*crypto.OlmAccount{},
	}
	hs.srv = httptest.NewTLSServer(http.HandlerFunc(hs.serve))
	t.Cleanup(hs.srv.Close)
	return hs
}

func (hs *fakeHomeserver) creds() HermesCredentials {
	return HermesCredentials{Account: Account{Homeserver: hs.srv.URL, UserID: sender}, Password: "pw", HermesDeviceID: "HERMESDEV"}
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
		reply(map[string]any{"user_id": sender, "device_id": "OWNDEV", "access_token": "tok-" + string(rune('a'+len(hs.logins)))})
	case strings.HasSuffix(p, "/account/whoami"):
		if hs.unknownToken {
			matrixError(w, 401, "M_UNKNOWN_TOKEN")
			return
		}
		reply(map[string]any{"user_id": sender, "device_id": hs.whoamiDevice})
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
			if user == sender && hs.ownKeys != nil {
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
		hs.claims++
		reply(map[string]any{"one_time_keys": hs.claimableKeys()})
	case strings.Contains(p, "/sendToDevice/"):
		reply(map[string]any{})
	case strings.HasSuffix(p, "/account_data/m.direct"):
		reply(map[id.UserID][]id.RoomID{voidM: {testRoom}})
	case strings.HasSuffix(p, "/rooms/"+testRoom.String()+"/state"):
		_, _ = io.WriteString(w, dmStateJSON(voidM, "join"))
	case strings.Contains(p, "/rooms/"+testRoom.String()+"/send/"):
		switch hs.sendFault {
		case sendDropped:
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		case sendRejected:
			matrixError(w, 400, "M_UNKNOWN")
			return
		}
		parts := strings.Split(p, "/")
		hs.sentTypes = append(hs.sentTypes, parts[len(parts)-2])
		hs.sentBodies = append(hs.sentBodies, string(body))
		reply(map[string]any{"event_id": "$ev"})
	default:
		hs.t.Errorf("unexpected request %s %s", r.Method, p)
		matrixError(w, 404, "M_UNRECOGNIZED")
	}
}

// addRecipientDevice publishes validly signed device keys for void that have no claimable one-time key.
func (hs *fakeHomeserver) addRecipientDevice(t *testing.T, device id.DeviceID) {
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
	hs.targetAccts[device] = acct
	hs.mu.Unlock()
}

// sendFault selects how the fake homeserver answers /send.
//
// Example: hs.sendFault = sendDropped simulates a response lost after the request was sent.
type sendFault int

// The /send behaviors.
//
// Example: sendRejected answers with a Matrix error.
const (
	sendAccepted sendFault = iota
	sendDropped
	sendRejected
)

// claimableKeys returns one signed one-time key per target device when offerOTK is set, else none.
//
// Example: {"@xaionaro:…": {"TDEV": {"signed_curve25519:AAAA": {...}}}}.
func (hs *fakeHomeserver) claimableKeys() map[id.UserID]map[id.DeviceID]map[id.KeyID]mautrix.OneTimeKey {
	out := map[id.UserID]map[id.DeviceID]map[id.KeyID]mautrix.OneTimeKey{}
	if !hs.offerOTK {
		return out
	}
	out[voidM] = map[id.DeviceID]map[id.KeyID]mautrix.OneTimeKey{}
	for device, acct := range hs.targetAccts {
		acct.Internal.GenOneTimeKeys(1)
		keys, err := acct.Internal.OneTimeKeys()
		if err != nil {
			hs.t.Error(err)
			return out
		}
		for keyID, key := range keys {
			otk := mautrix.OneTimeKey{Key: key, IsSigned: true}
			sig, err := acct.SignJSON(&otk)
			if err != nil {
				hs.t.Error(err)
				return out
			}
			otk.Signatures = signatures.NewSingleSignature(voidM, id.KeyAlgorithmEd25519, device.String(), sig)
			out[voidM][device] = map[id.KeyID]mautrix.OneTimeKey{id.NewKeyID(id.KeyAlgorithmSignedCurve25519, keyID): otk}
			break
		}
		acct.Internal.MarkKeysAsPublished()
	}
	return out
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

func TestRealPathKeyPendingRecoversToCiphertextSend(t *testing.T) {
	hs := newHomeserver(t)
	hs.addRecipientDevice(t, "TDEV")
	api, err := hs.connect(t, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	out, err := deliver(ctx, api, memRooms{}, sender, voidM, "hello plaintext")
	if err != nil || out.Status != StatusKeyPending || len(hs.sentTypes) != 0 {
		t.Fatalf("device without Olm session: %+v %v %v", out, err, hs.sentTypes)
	}
	if devices, err := api.Crypto.Machine().CryptoStore.GetDevices(ctx, voidM); err != nil || devices["TDEV"] == nil {
		t.Fatalf("recipient device not known, so key_pending proves nothing: %v %v", devices, err)
	}
	if s, err := api.Crypto.Machine().CryptoStore.GetOutboundGroupSession(ctx, testRoom); err != nil || s != nil {
		t.Fatalf("unshared outbound session kept: %v %v", s, err)
	}
	claims := hs.claims
	hs.offerOTK = true
	out, err = deliver(ctx, api, memRooms{}, sender, voidM, "hello plaintext")
	if err != nil || out.Status != StatusSent || out.EventID != "$ev" {
		t.Fatalf("after the device became reachable: %+v %v", out, err)
	}
	if hs.claims == claims {
		t.Fatal("no new key claim after key_pending")
	}
	if len(hs.sentTypes) != 1 || hs.sentTypes[0] != "m.room.encrypted" {
		t.Fatalf("event types %v", hs.sentTypes)
	}
	if strings.Contains(hs.sentBodies[0], "hello plaintext") || !strings.Contains(hs.sentBodies[0], `"ciphertext"`) {
		t.Fatalf("body is not ciphertext: %s", hs.sentBodies[0])
	}
}

// T2 (real SDK): a dropped /send leaves the outcome unknown; a Matrix error answer proves the event was not stored.
func TestSendOutcomeUnknownOnlyWithoutMatrixAnswer(t *testing.T) {
	for fault, want := range map[sendFault]Status{sendDropped: StatusUnknown, sendRejected: StatusUnset} {
		hs := newHomeserver(t)
		hs.addRecipientDevice(t, "TDEV")
		hs.offerOTK = true
		api, err := hs.connect(t, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		hs.sendFault = fault
		out, err := deliver(context.Background(), api, memRooms{}, sender, voidM, "x")
		switch want {
		case StatusUnknown:
			if err != nil || out.Status != StatusUnknown || out.RoomID != testRoom || out.EventID != "" {
				t.Errorf("dropped: %+v %v", out, err)
			}
		default:
			if categoryOf(err) != CategorySend || out.Status != StatusUnset {
				t.Errorf("rejected: %+v %v", out, err)
			}
		}
	}
}
