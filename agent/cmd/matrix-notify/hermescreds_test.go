package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// quoteLikeHermes mirrors Hermes's _quote_env_value (hermes_cli/config.py), the writer of ~/.hermes/.env.
func quoteLikeHermes(v string) string {
	if v == "" || !strings.ContainsAny(v, "#\"' \t\n\r\v\f") {
		return v
	}
	return `"` + strings.ReplaceAll(strings.ReplaceAll(v, `\`, `\\`), `"`, `\"`) + `"`
}

func TestParseDotenvMatchesPythonDotenv(t *testing.T) {
	// Expected values are what python-dotenv DotEnv(interpolate=False).parse() yields for the same text.
	env := parseDotenv(`# comment
MATRIX_HOMESERVER='https://hs.example'
export MATRIX_USER_ID="@bot:hs.example"   # trailing comment
A="abc" # trailing comment
B="a\"b"
C=plain # inline
D=no#comment
E='it\'s \n raw'
F="tab\there\\back"
G=${HOME}/x
H="multi
line"
 I = spaced
J
K=
BROKEN "line
L=last
L=wins
`)
	want := map[string]string{
		"MATRIX_HOMESERVER": "https://hs.example", "MATRIX_USER_ID": "@bot:hs.example",
		"A": "abc", "B": `a"b`, "C": "plain", "D": "no#comment", "E": `it's \n raw`, "F": "tab\there\\back",
		"G": "${HOME}/x", "H": "multi\nline", "I": "spaced", "K": "", "L": "wins",
	}
	for k, v := range want {
		if got, ok := env[k]; !ok || got != v {
			t.Errorf("%s: got %q ok=%v, want %q", k, got, ok, v)
		}
	}
	for _, absent := range []string{"J", "BROKEN"} {
		if _, ok := env[absent]; ok {
			t.Errorf("%s present", absent)
		}
	}
}

func TestParseDotenvRoundTripsHermesQuoting(t *testing.T) {
	for _, pw := range []string{`p"w`, `back\slash`, `has # hash`, `it's`, `sp ace`, `plain\x`, `#lead`} {
		env := parseDotenv("MATRIX_PASSWORD=" + quoteLikeHermes(pw) + "\n")
		if env["MATRIX_PASSWORD"] != pw {
			t.Errorf("%q read back as %q", pw, env["MATRIX_PASSWORD"])
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
	if _, err := loadHermesCredentials(path); categoryOf(err) != CategoryCredentials || !strings.Contains(err.Error(), "MATRIX_PASSWORD") {
		t.Fatalf("missing password: %v", err)
	}
	write("MATRIX_HOMESERVER=http://hs\nMATRIX_USER_ID=@b:hs\nMATRIX_PASSWORD=s3cret\n")
	if _, err := loadHermesCredentials(path); categoryOf(err) != CategoryCredentials {
		t.Fatal("plain http accepted")
	}
	write("MATRIX_HOMESERVER=https://hs/\nMATRIX_USER_ID=@b:hs\nMATRIX_PASSWORD=\"s3\\\"cret\"\nMATRIX_DEVICE_ID=HD\n")
	creds, err := loadHermesCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	if creds.Account != (Account{Homeserver: "https://hs", UserID: "@b:hs"}) || creds.Password != `s3"cret` || creds.HermesDeviceID != "HD" {
		t.Fatal("credentials not loaded as written")
	}
	if _, err := loadHermesCredentials(filepath.Join(t.TempDir(), "none")); categoryOf(err) != CategoryCredentials {
		t.Fatalf("missing file: %v", err)
	}
}
