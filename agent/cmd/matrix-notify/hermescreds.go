package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"maunium.net/go/mautrix/id"
)

// Keys read from Hermes's .env, and the fixed values used to interpret and bind them.
//
// Example: env[envPassword] is Hermes's Matrix password.
const (
	envHomeserver = "MATRIX_HOMESERVER"
	envUserID     = "MATRIX_USER_ID"
	envPassword   = "MATRIX_PASSWORD"
	envDeviceID   = "MATRIX_DEVICE_ID"
	// httpsScheme is the only homeserver URL scheme accepted.
	httpsScheme = "https"
	// homeserverTrailingSlash is trimmed so "https://hs/" and "https://hs" bind the same Account.
	homeserverTrailingSlash = "/"
	// dotenvValueCutset is what Python's str.rstrip() removes from an unquoted value, restricted to ASCII.
	dotenvValueCutset = " \t\n\r\v\f"
	// accountKeySeparator joins homeserver and user ID in Account.RoomsKey; changing it orphans saved rooms.
	accountKeySeparator = "|"
)

// Account identifies one Matrix account on one homeserver origin; stored sessions and rooms are bound to it.
//
// Example: Account{Homeserver: "https://matrix.example", UserID: "@bot:matrix.example"}.
type Account struct {
	Homeserver string    `json:"homeserver"`
	UserID     id.UserID `json:"user_id"`
}

// RoomsKey returns the account's persisted key in the rooms file; its format must never change.
//
// Example: "https://matrix.example|@bot:matrix.example".
func (a Account) RoomsKey() string { return a.Homeserver + accountKeySeparator + a.UserID.String() }

// HermesCredentials are Hermes's Matrix account settings, read in place from its .env on every run.
//
// Example: creds, err := loadHermesCredentials(filepath.Join(home, ".hermes/.env")).
type HermesCredentials struct {
	Account
	Password Secret
	// HermesDeviceID is Hermes's own E2EE device; it is read only to make sure this tool never uses it.
	HermesDeviceID id.DeviceID
}

// python-dotenv's parser grammar (dotenv/parser.py), which Hermes uses via DotEnv(interpolate=False).parse()
// in hermes_cli/env_loader.py. Go's leftmost-first regexp semantics match Python's for these patterns. It is ported
// rather than taken from a Go dotenv library because those expand ${VAR} and decode escapes differently.
//
// Example: r.read(dotenvUnquotedKey) consumes "KEY" from "KEY=v".
//
// TODO: Go's \s is [\t\n\f\r ], but Python's str-pattern \s and str.rstrip() also match \v, \x1c-\x1f, \x85 and
// Unicode spaces, so a .env that uses those characters as separators parses differently from Hermes; mirror Python's
// whitespace class in these patterns and in the value trim.
var (
	dotenvMultilineWhitespace = regexp.MustCompile(`^\s*`)
	dotenvExport              = regexp.MustCompile(`^(?:export[^\S\r\n]+)?`)
	dotenvSingleQuotedKey     = regexp.MustCompile(`^'([^']+)'`)
	dotenvUnquotedKey         = regexp.MustCompile(`^([^=#\s]+)`)
	dotenvWhitespace          = regexp.MustCompile(`^[^\S\r\n]*`)
	dotenvEqualSign           = regexp.MustCompile(`^(=[^\S\r\n]*)`)
	dotenvSingleQuotedValue   = regexp.MustCompile(`^'((?:\\'|[^'])*)'`)
	dotenvDoubleQuotedValue   = regexp.MustCompile(`^"((?:\\"|[^"])*)"`)
	dotenvUnquotedValue       = regexp.MustCompile(`^([^\r\n]*)`)
	dotenvComment             = regexp.MustCompile(`^(?:[^\S\r\n]*#[^\r\n]*)?`)
	dotenvEndOfLine           = regexp.MustCompile(`^[^\S\r\n]*(?:\r\n|\n|\r|$)`)
	dotenvRestOfLine          = regexp.MustCompile(`^[^\r\n]*(?:\r|\n|\r\n)?`)
	dotenvInlineComment       = regexp.MustCompile(`\s+#.*`)
	dotenvDoubleQuoteEscapes  = regexp.MustCompile(`\\[\\'"abfnrtv]`)
	dotenvSingleQuoteEscapes  = regexp.MustCompile(`\\[\\']`)
)

// dotenvEscapes decodes the escapes python-dotenv decodes with Python's unicode-escape codec.
//
// Example: dotenvEscapes[`\n`] is a newline.
var dotenvEscapes = map[string]string{
	`\\`: `\`, `\'`: `'`, `\"`: `"`, `\a`: "\a", `\b`: "\b", `\f`: "\f", `\n`: "\n", `\r`: "\r", `\t`: "\t", `\v`: "\v",
}

// errDotenvBinding marks a binding python-dotenv rejects; like python-dotenv, the parser skips its line.
//
// Example: `KEY "x` is skipped.
var errDotenvBinding = errors.New("invalid dotenv binding")

// dotenvReader is a cursor over .env text.
//
// Example: r := &dotenvReader{text: s}; r.read(dotenvExport).
type dotenvReader struct {
	text string
	pos  int
}

// read consumes re's anchored match at the cursor and returns its submatches, or fails without moving.
//
// Example: m, err := r.read(dotenvUnquotedKey); key := m[1].
func (r *dotenvReader) read(re *regexp.Regexp) ([]string, error) {
	m := re.FindStringSubmatch(r.text[r.pos:])
	if m == nil {
		return nil, errDotenvBinding
	}
	r.pos += len(m[0])
	return m, nil
}

// peek returns the next byte, or 0 at end of text.
//
// Example: r.peek() == '=' before a value.
func (r *dotenvReader) peek() byte {
	if r.pos >= len(r.text) {
		return 0
	}
	return r.text[r.pos]
}

// parseDotenv parses .env text exactly as python-dotenv's DotEnv(interpolate=False).parse() does: comments, quoting,
// escapes and multi-line quoted values; invalid bindings are skipped and later keys override earlier ones. Nothing is
// expanded or executed, and keys without "=" are absent.
//
// Example: parseDotenv("A=\"x\\\"y\" # c\n")["A"] == `x"y`.
func parseDotenv(text string) map[string]string {
	env := map[string]string{}
	r := &dotenvReader{text: text}
	for r.pos < len(text) {
		start := r.pos
		key, value, hasValue, err := parseDotenvBinding(r)
		if err != nil {
			r.pos = start
			_, _ = r.read(dotenvRestOfLine) // always matches
			continue
		}
		if key != "" && hasValue {
			env[key] = value
		}
	}
	return env
}

// parseDotenvBinding parses one binding at the cursor; an empty key means a blank or comment-only line.
//
// Example: key, value, hasValue, err := parseDotenvBinding(r).
func parseDotenvBinding(r *dotenvReader) (string, string, bool, error) {
	if _, err := r.read(dotenvMultilineWhitespace); err != nil {
		return "", "", false, err
	}
	if r.pos >= len(r.text) {
		return "", "", false, nil
	}
	if _, err := r.read(dotenvExport); err != nil {
		return "", "", false, err
	}
	var key string
	switch r.peek() {
	case '#':
	case '\'':
		m, err := r.read(dotenvSingleQuotedKey)
		if err != nil {
			return "", "", false, err
		}
		key = m[1]
	default:
		m, err := r.read(dotenvUnquotedKey)
		if err != nil {
			return "", "", false, err
		}
		key = m[1]
	}
	if _, err := r.read(dotenvWhitespace); err != nil {
		return "", "", false, err
	}
	var value string
	hasValue := r.peek() == '='
	if hasValue {
		if _, err := r.read(dotenvEqualSign); err != nil {
			return "", "", false, err
		}
		v, err := parseDotenvValue(r)
		if err != nil {
			return "", "", false, err
		}
		value = v
	}
	if _, err := r.read(dotenvComment); err != nil {
		return "", "", false, err
	}
	if _, err := r.read(dotenvEndOfLine); err != nil {
		return "", "", false, err
	}
	return key, value, hasValue, nil
}

// decodeDotenvEscape decodes one escape sequence matched by a python-dotenv escape pattern.
//
// Example: decodeDotenvEscape(`\"`) == `"`.
func decodeDotenvEscape(s string) string { return dotenvEscapes[s] }

// parseDotenvValue parses a quoted or unquoted value at the cursor.
//
// Example: `'a\'b'` yields `a'b`; `plain # c` yields `plain`.
func parseDotenvValue(r *dotenvReader) (string, error) {
	switch r.peek() {
	case '\'':
		m, err := r.read(dotenvSingleQuotedValue)
		if err != nil {
			return "", err
		}
		return dotenvSingleQuoteEscapes.ReplaceAllStringFunc(m[1], decodeDotenvEscape), nil
	case '"':
		m, err := r.read(dotenvDoubleQuotedValue)
		if err != nil {
			return "", err
		}
		return dotenvDoubleQuoteEscapes.ReplaceAllStringFunc(m[1], decodeDotenvEscape), nil
	case 0, '\n', '\r':
		return "", nil
	default:
		m, err := r.read(dotenvUnquotedValue)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(dotenvInlineComment.ReplaceAllString(m[1], ""), dotenvValueCutset), nil
	}
}

// loadHermesCredentials reads Hermes's Matrix settings from path and requires an https homeserver URL with a host.
//
// Example: creds, err := loadHermesCredentials(filepath.Join(home, ".hermes/.env")).
func loadHermesCredentials(path string) (HermesCredentials, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return HermesCredentials{}, categorize(CategoryCredentials, fmt.Errorf("read Hermes env: %w", err))
	}
	env := parseDotenv(string(b))
	creds := HermesCredentials{
		Account:        Account{Homeserver: strings.TrimRight(env[envHomeserver], homeserverTrailingSlash), UserID: id.UserID(env[envUserID])},
		Password:       Secret(env[envPassword]),
		HermesDeviceID: id.DeviceID(env[envDeviceID]),
	}
	var errs []error
	for _, kv := range [][2]string{{envHomeserver, creds.Homeserver}, {envUserID, creds.UserID.String()}, {envPassword, string(creds.Password)}} {
		if kv[1] == "" {
			errs = append(errs, fmt.Errorf("%s missing in %s", kv[0], path))
		}
	}
	if u, err := url.Parse(creds.Homeserver); creds.Homeserver != "" && (err != nil || u.Scheme != httpsScheme || u.Host == "") {
		errs = append(errs, errors.New(envHomeserver+" must be an https URL with a host"))
	}
	if err := errors.Join(errs...); err != nil {
		return HermesCredentials{}, categorize(CategoryCredentials, err)
	}
	return creds, nil
}
