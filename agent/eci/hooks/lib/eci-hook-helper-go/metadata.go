package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// errMetadataPrefix reports any malformed or unexpected prefix. It is a
// rejection, not a "no metadata" answer.
var errMetadataPrefix = errors.New("session metadata prefix rejected")

// threadSpawnMetadata is the parsed thread_spawn source. A nil parentThreadID
// means the thread_spawn object had no parent_thread_id string.
type threadSpawnMetadata struct {
	parentThreadID *string
}

// parseThreadSpawnMetadata walks the session_meta prefix. It returns
// (nil, nil) when the prefix is well formed but carries no thread_spawn
// metadata, and an error when the prefix is malformed.
func parseThreadSpawnMetadata(text []byte) (*threadSpawnMetadata, error) {
	r := metaReader{text: text}
	return r.readThreadSpawnMetadata()
}

// metaReader mirrors SessionMetadataPrefixReader: a cursor over the decoded
// prefix that only parses the keys it needs and skips every other value.
type metaReader struct {
	text []byte
	pos  int
}

func (r *metaReader) readThreadSpawnMetadata() (*threadSpawnMetadata, error) {
	if err := r.expect("{"); err != nil {
		return nil, err
	}
	recordType := ""
	payloadPos := -1
	for {
		key, err := r.readString()
		if err != nil {
			return nil, err
		}
		if err := r.expect(":"); err != nil {
			return nil, err
		}
		switch {
		case key == "type":
			if recordType, err = r.readString(); err != nil {
				return nil, err
			}
		case key == "payload" && recordType == "session_meta":
			return r.readPayloadThreadSpawnMetadata()
		default:
			if key == "payload" {
				// Retained only as a fallback when session_meta arrives later.
				payloadPos = r.pos
			}
			if err := r.skipValue(); err != nil {
				return nil, err
			}
		}

		if r.consume("}") {
			if recordType == "session_meta" && payloadPos >= 0 {
				r.pos = payloadPos
				return r.readPayloadThreadSpawnMetadata()
			}
			return nil, nil
		}
		if err := r.expect(","); err != nil {
			return nil, err
		}
	}
}

func (r *metaReader) readPayloadThreadSpawnMetadata() (*threadSpawnMetadata, error) {
	if err := r.expect("{"); err != nil {
		return nil, err
	}
	for {
		key, err := r.readString()
		if err != nil {
			return nil, err
		}
		if err := r.expect(":"); err != nil {
			return nil, err
		}
		if key == "source" {
			return r.readSourceThreadSpawnMetadata()
		}
		if err := r.skipValue(); err != nil {
			return nil, err
		}
		if r.consume("}") {
			return nil, nil
		}
		if err := r.expect(","); err != nil {
			return nil, err
		}
	}
}

func (r *metaReader) readSourceThreadSpawnMetadata() (*threadSpawnMetadata, error) {
	if err := r.expect("{"); err != nil {
		return nil, err
	}
	for {
		key, err := r.readString()
		if err != nil {
			return nil, err
		}
		if err := r.expect(":"); err != nil {
			return nil, err
		}
		if key == "subagent" {
			return r.readSubagentThreadSpawnMetadata()
		}
		if err := r.skipValue(); err != nil {
			return nil, err
		}
		if r.consume("}") {
			return nil, nil
		}
		if err := r.expect(","); err != nil {
			return nil, err
		}
	}
}

func (r *metaReader) readSubagentThreadSpawnMetadata() (*threadSpawnMetadata, error) {
	if !r.consume("{") {
		return nil, r.skipValue()
	}
	for {
		key, err := r.readString()
		if err != nil {
			return nil, err
		}
		if err := r.expect(":"); err != nil {
			return nil, err
		}
		if key == "thread_spawn" {
			return r.readThreadSpawnObject()
		}
		if err := r.skipValue(); err != nil {
			return nil, err
		}
		if r.consume("}") {
			return nil, nil
		}
		if err := r.expect(","); err != nil {
			return nil, err
		}
	}
}

func (r *metaReader) readThreadSpawnObject() (*threadSpawnMetadata, error) {
	if !r.consume("{") {
		return nil, r.skipValue()
	}
	var parent *string
	for {
		key, err := r.readString()
		if err != nil {
			return nil, err
		}
		if err := r.expect(":"); err != nil {
			return nil, err
		}
		if key == "parent_thread_id" {
			value, err := r.readString()
			if err != nil {
				return nil, err
			}
			parent = &value
		} else if err := r.skipValue(); err != nil {
			return nil, err
		}
		if r.consume("}") {
			return &threadSpawnMetadata{parentThreadID: parent}, nil
		}
		if err := r.expect(","); err != nil {
			return nil, err
		}
	}
}

// readString decodes one JSON string token at the cursor.
func (r *metaReader) readString() (string, error) {
	r.skipWhitespace()
	if r.at(r.pos) != '"' {
		return "", errMetadataPrefix
	}
	end, err := scanString(r.text, r.pos)
	if err != nil {
		return "", err
	}
	var value string
	if err := json.Unmarshal(r.text[r.pos:end], &value); err != nil {
		return "", errMetadataPrefix
	}
	r.pos = end
	return value, nil
}

// skipValue advances past one JSON value of any type.
func (r *metaReader) skipValue() error {
	end, err := scanValue(r.text, r.pos)
	if err != nil {
		return err
	}
	r.pos = end
	return nil
}

func (r *metaReader) expect(token string) error {
	r.skipWhitespace()
	if !bytes.HasPrefix(r.text[r.pos:], []byte(token)) {
		return errMetadataPrefix
	}
	r.pos += len(token)
	return nil
}

func (r *metaReader) consume(token string) bool {
	r.skipWhitespace()
	if !bytes.HasPrefix(r.text[r.pos:], []byte(token)) {
		return false
	}
	r.pos += len(token)
	return true
}

func (r *metaReader) skipWhitespace() {
	r.pos = skipWhitespaceAt(r.text, r.pos)
}

func (r *metaReader) at(i int) byte {
	return byteAt(r.text, i)
}

// byteAt returns text[i], or 0 past the end. NUL cannot occur in validated
// input, so 0 never matches a structural token.
func byteAt(text []byte, i int) byte {
	if i < 0 || i >= len(text) {
		return 0
	}
	return text[i]
}

func skipWhitespaceAt(text []byte, i int) int {
	for i < len(text) {
		switch text[i] {
		case ' ', '\t', '\r', '\n':
			i++
		default:
			return i
		}
	}
	return i
}

// scanString returns the index just past the string token starting at start.
// Escapes are checked by json.Valid so that malformed tokens are rejected.
func scanString(text []byte, start int) (int, error) {
	if byteAt(text, start) != '"' {
		return 0, errMetadataPrefix
	}
	for i := start + 1; i < len(text); i++ {
		switch c := text[i]; {
		case c == '"':
			if !json.Valid(text[start : i+1]) {
				return 0, errMetadataPrefix
			}
			return i + 1, nil
		case c == '\\':
			i++
		case c < 0x20:
			return 0, errMetadataPrefix
		}
	}
	return 0, errMetadataPrefix
}

// scanValue returns the index just past one JSON value starting at the first
// non-whitespace byte at or after i.
func scanValue(text []byte, i int) (int, error) {
	i = skipWhitespaceAt(text, i)
	switch c := byteAt(text, i); {
	case c == '"':
		return scanString(text, i)
	case c == '{':
		return scanObject(text, i)
	case c == '[':
		return scanArray(text, i)
	case c == 't':
		return scanLiteral(text, i, "true")
	case c == 'f':
		return scanLiteral(text, i, "false")
	case c == 'n':
		return scanLiteral(text, i, "null")
	case c == '-' || (c >= '0' && c <= '9'):
		return scanNumber(text, i)
	}
	return 0, errMetadataPrefix
}

func scanObject(text []byte, i int) (int, error) {
	i = skipWhitespaceAt(text, i+1)
	if byteAt(text, i) == '}' {
		return i + 1, nil
	}
	for {
		i = skipWhitespaceAt(text, i)
		end, err := scanString(text, i)
		if err != nil {
			return 0, err
		}
		i = skipWhitespaceAt(text, end)
		if byteAt(text, i) != ':' {
			return 0, errMetadataPrefix
		}
		if i, err = scanValue(text, i+1); err != nil {
			return 0, err
		}
		i = skipWhitespaceAt(text, i)
		switch byteAt(text, i) {
		case '}':
			return i + 1, nil
		case ',':
			i++
		default:
			return 0, errMetadataPrefix
		}
	}
}

func scanArray(text []byte, i int) (int, error) {
	i = skipWhitespaceAt(text, i+1)
	if byteAt(text, i) == ']' {
		return i + 1, nil
	}
	for {
		var err error
		if i, err = scanValue(text, i); err != nil {
			return 0, err
		}
		i = skipWhitespaceAt(text, i)
		switch byteAt(text, i) {
		case ']':
			return i + 1, nil
		case ',':
			i++
		default:
			return 0, errMetadataPrefix
		}
	}
}

func scanLiteral(text []byte, i int, literal string) (int, error) {
	if !bytes.HasPrefix(text[i:], []byte(literal)) {
		return 0, errMetadataPrefix
	}
	return i + len(literal), nil
}

// scanNumber follows the JSON number grammar.
func scanNumber(text []byte, i int) (int, error) {
	if byteAt(text, i) == '-' {
		i++
	}
	switch c := byteAt(text, i); {
	case c == '0':
		i++
	case c >= '1' && c <= '9':
		i = skipDigits(text, i)
	default:
		return 0, errMetadataPrefix
	}
	if byteAt(text, i) == '.' {
		i++
		if byteAt(text, i) < '0' || byteAt(text, i) > '9' {
			return 0, errMetadataPrefix
		}
		i = skipDigits(text, i)
	}
	if c := byteAt(text, i); c == 'e' || c == 'E' {
		i++
		if c := byteAt(text, i); c == '+' || c == '-' {
			i++
		}
		if byteAt(text, i) < '0' || byteAt(text, i) > '9' {
			return 0, errMetadataPrefix
		}
		i = skipDigits(text, i)
	}
	return i, nil
}

func skipDigits(text []byte, i int) int {
	for c := byteAt(text, i); c >= '0' && c <= '9'; c = byteAt(text, i) {
		i++
	}
	return i
}

// readChunkBytes caps each read of a first-record prefix, as the pre-migration
// Python reader did.
const readChunkBytes = 4096

// readFirstRecordPrefix returns the bytes before the first newline. It reads
// in chunks of at most readChunkBytes and retains at most inputBudget+1 bytes:
// the extra byte lets a record of exactly inputBudget bytes be told apart from
// an oversize one. A record longer than inputBudget whose newline does not
// appear within the first inputBudget+1 bytes is rejected, as is a record that
// reaches the bound with no newline. A missing newline on a record that ends
// at EOF within the bound is not a failure.
func readFirstRecordPrefix(r io.Reader) ([]byte, bool) {
	buf := make([]byte, inputBudget+1)
	n := 0
	eof := false
	for {
		if i := bytes.IndexByte(buf[:n], '\n'); i >= 0 {
			return buf[:i], true
		}
		if n > inputBudget {
			return nil, false
		}
		if eof {
			return buf[:n], true
		}
		end := min(n+readChunkBytes, len(buf))
		m, err := r.Read(buf[n:end])
		n += m
		if errors.Is(err, io.EOF) {
			eof = true
		} else if err != nil {
			return nil, false
		}
	}
}

// transcriptThreadSpawnMetadata emits {"parent_thread_id":...} for a
// session_meta transcript whose subagent source is thread_spawn.
func transcriptThreadSpawnMetadata(root, raw string, stdout io.Writer) int {
	if !rawAbsolutePathAllowed(raw) {
		return statusRejected
	}
	file, ok := openContainedTranscript(root, raw)
	if !ok {
		return statusRejected
	}
	defer file.Close()
	prefix, ok := readFirstRecordPrefix(file)
	if !ok || !validBounded(prefix) {
		return statusRejected
	}
	metadata, err := parseThreadSpawnMetadata(prefix)
	if err != nil || metadata == nil {
		return statusRejected
	}
	payload, err := json.Marshal(struct {
		ParentThreadID *string `json:"parent_thread_id"`
	}{ParentThreadID: metadata.parentThreadID})
	if err != nil {
		return statusFailed
	}
	return emitBounded(payload, stdout)
}
