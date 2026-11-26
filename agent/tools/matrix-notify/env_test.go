package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseEnvReadsQuotedAndBareValues(t *testing.T) {
	env := parseEnv(strings.NewReader(`# comment
MATRIX_HOMESERVER='https://hs.example'
MATRIX_USER_ID="@bot:hs.example"
export MATRIX_PASSWORD=p=w#d
 MATRIX_DEVICE_ID = HERMESDEV
EVIL=$(touch /tmp/pwned)

BROKEN_LINE
`))
	want := map[string]string{
		"MATRIX_HOMESERVER": "https://hs.example",
		"MATRIX_USER_ID":    "@bot:hs.example",
		"MATRIX_PASSWORD":   "p=w#d",
		"MATRIX_DEVICE_ID":  "HERMESDEV",
		"EVIL":              "$(touch /tmp/pwned)",
	}
	if len(env) != len(want) {
		t.Fatalf("got %d keys, want %d", len(env), len(want))
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("%s mismatch", k)
		}
	}
}

func TestLoadHermesCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	write := func(s string) {
		if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("MATRIX_HOMESERVER=https://hs\nMATRIX_USER_ID=@b:hs\n")
	if _, err := loadHermesCredentials(path); err == nil || !strings.Contains(err.Error(), "MATRIX_PASSWORD") {
		t.Fatalf("missing password: %v", err)
	}
	write("MATRIX_HOMESERVER=http://hs\nMATRIX_USER_ID=@b:hs\nMATRIX_PASSWORD=s3cret\n")
	if _, err := loadHermesCredentials(path); err == nil {
		t.Fatal("plain http accepted")
	}
	write("MATRIX_HOMESERVER=https://hs/\nMATRIX_USER_ID=@b:hs\nMATRIX_PASSWORD=s3cret\nMATRIX_DEVICE_ID=HD\n")
	creds, err := loadHermesCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	if creds.Homeserver != "https://hs" || creds.UserID != "@b:hs" || creds.Password != "s3cret" || creds.HermesDeviceID != "HD" {
		t.Fatal("credentials not loaded as written")
	}
}
