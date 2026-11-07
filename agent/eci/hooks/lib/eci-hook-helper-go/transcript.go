package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

const (
	dirOpenFlags  = syscall.O_RDONLY | syscall.O_CLOEXEC | syscall.O_NOFOLLOW | syscall.O_DIRECTORY
	fileOpenFlags = syscall.O_RDONLY | syscall.O_CLOEXEC | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
)

// relativePartsAllowed reports whether parts is non-empty and contains no
// empty, "." or ".." component.
func relativePartsAllowed(parts []string) bool {
	if len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// rawCharAllowed reports whether r is a printable character: codepoint >= 32
// and outside the C1 control range 127..159.
func rawCharAllowed(r rune) bool {
	return r >= 32 && (r < 127 || r > 159)
}

// rawAbsolutePathAllowed applies the raw transcript_path rules: printable
// characters only, absolute, and no empty, "." or ".." component.
func rawAbsolutePathAllowed(raw string) bool {
	for _, r := range raw {
		if !rawCharAllowed(r) {
			return false
		}
	}
	if !strings.HasPrefix(raw, "/") {
		return false
	}
	parts := strings.Split(raw, "/")
	return relativePartsAllowed(parts[1:])
}

// pathParts mirrors pathlib's parts for an absolute path: empty and "."
// components are dropped; ".." is kept so that it never matches.
func pathParts(path string) []string {
	var parts []string
	for _, part := range strings.Split(path, "/") {
		if part != "" && part != "." {
			parts = append(parts, part)
		}
	}
	return parts
}

// openAbsoluteDirectory walks an absolute, already-resolved directory path
// from "/" with O_NOFOLLOW|O_DIRECTORY at every component.
func openAbsoluteDirectory(path string) (int, bool) {
	fd, err := syscall.Open("/", dirOpenFlags, 0)
	if err != nil {
		return -1, false
	}
	for _, part := range pathParts(path) {
		if !relativePartsAllowed([]string{part}) {
			syscall.Close(fd)
			return -1, false
		}
		next, err := syscall.Openat(fd, part, dirOpenFlags, 0)
		syscall.Close(fd)
		if err != nil {
			return -1, false
		}
		fd = next
	}
	return fd, true
}

// openContainedTranscript opens raw as a regular, non-symlink *.jsonl file
// that lies under the sessions root.
func openContainedTranscript(root, raw string) (*os.File, bool) {
	if !strings.HasPrefix(root, "/") || !strings.HasPrefix(raw, "/") {
		return nil, false
	}
	rootParts := pathParts(root)
	pathPs := pathParts(raw)
	if len(pathPs) < len(rootParts) || !slices.Equal(pathPs[:len(rootParts)], rootParts) {
		return nil, false
	}
	rel := pathPs[len(rootParts):]
	if !relativePartsAllowed(rel) || !strings.HasSuffix(rel[len(rel)-1], ".jsonl") {
		return nil, false
	}

	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, false
	}
	dirFD, ok := openAbsoluteDirectory(resolved)
	if !ok {
		return nil, false
	}
	for _, part := range rel[:len(rel)-1] {
		next, err := syscall.Openat(dirFD, part, dirOpenFlags, 0)
		syscall.Close(dirFD)
		if err != nil {
			return nil, false
		}
		dirFD = next
	}
	fd, err := syscall.Openat(dirFD, rel[len(rel)-1], fileOpenFlags, 0)
	syscall.Close(dirFD)
	if err != nil {
		return nil, false
	}
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil || st.Mode&syscall.S_IFMT != syscall.S_IFREG {
		syscall.Close(fd)
		return nil, false
	}
	return os.NewFile(uintptr(fd), raw), true
}

// readFirstRecord returns the bytes before the first newline, reading at most
// inputBudget+1 bytes. It fails when no newline appears within the budget.
func readFirstRecord(r io.Reader) ([]byte, bool) {
	buf := make([]byte, 0, inputBudget+1)
	for len(buf) <= inputBudget {
		start := len(buf)
		n, err := r.Read(buf[start:cap(buf)])
		buf = buf[:start+n]
		if i := bytes.IndexByte(buf[start:], '\n'); i >= 0 {
			return buf[:start+i], true
		}
		if errors.Is(err, io.EOF) {
			return buf, true
		}
		if err != nil {
			return nil, false
		}
	}
	return nil, false
}

// transcriptFirstRecord emits the first record of the transcript at raw when
// it is a JSON object.
func transcriptFirstRecord(root, raw string, stdout io.Writer) int {
	if !rawAbsolutePathAllowed(raw) {
		return statusRejected
	}
	file, ok := openContainedTranscript(root, raw)
	if !ok {
		return statusRejected
	}
	defer file.Close()
	record, ok := readFirstRecord(file)
	if !ok || !validBounded(record) {
		return statusRejected
	}
	var object map[string]any
	if err := json.Unmarshal(record, &object); err != nil || object == nil {
		return statusRejected
	}
	return emitBounded(record, stdout)
}

// transcriptStatus prints main, subagent, or unknown for the omp transcript
// named by stdin. Every classification failure prints unknown.
func transcriptStatus(root, raw string, stdout io.Writer) int {
	if _, err := io.WriteString(stdout, classifyTranscript(root, raw)+"\n"); err != nil {
		return statusFailed
	}
	return statusOK
}

// classifyTranscript mirrors the command-plan transcript rule: a regular,
// non-empty transcript under the sessions root is a subagent when it lies three or
// more levels below the root and its header declares a string parentSession; it is
// main when it lies two levels below, with no parentSession and no session_init
// marker. Every other outcome is unknown, which fails closed to the worker role.
//
// Example: sessions/<a>/<b>/<c>.jsonl with a parentSession header is a subagent.
func classifyTranscript(root, raw string) string {
	if !rawAbsolutePathAllowed(raw) {
		return "unknown"
	}
	file, ok := openContainedTranscript(root, raw)
	if !ok {
		return "unknown"
	}
	defer file.Close()
	parentSession, sessionInit, size, valid, err := scanTranscriptLines(file)
	if err != nil || !valid || size == 0 {
		return "unknown"
	}
	depth := len(pathParts(raw)) - len(pathParts(root))
	switch {
	case depth == 2 && !parentSession && !sessionInit:
		return "main"
	case depth >= 3 && parentSession:
		return "subagent"
	default:
		return "unknown"
	}
}

// errTranscriptLineTooLong reports a line that exceeds the bounded header prefix.
var errTranscriptLineTooLong = errors.New("transcript line exceeds the bounded header prefix")

// scanTranscriptLines reads every line once. valid is false when any line,
// including an empty one, is not JSON or repeats a signal key. Before the session
// header is seen, each line must fit in the remaining inputBudget, so an oversize
// header is never classified. parentSession reports a session header with a string
// parentSession; sessionInit reports an omp session_init marker.
func scanTranscriptLines(r io.Reader) (parentSession, sessionInit bool, size int, valid bool, err error) {
	reader := bufio.NewReader(r)
	header := false
	for {
		limit := -1
		if !header {
			limit = inputBudget - size
		}
		line, readErr := readTranscriptLine(reader, limit)
		if errors.Is(readErr, errTranscriptLineTooLong) {
			return false, false, size, false, nil
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return false, false, size, false, readErr
		}
		size += len(line)
		if len(line) > 0 {
			body := bytes.TrimSuffix(line, []byte("\n"))
			var value any
			if json.Unmarshal(body, &value) != nil || transcriptDuplicateSignalKey(body) {
				return false, false, size, false, nil
			}
			recordParent, recordInit := transcriptSignal(value)
			parentSession = parentSession || recordParent
			sessionInit = sessionInit || recordInit
			if object, ok := value.(map[string]any); ok && object["type"] == "session" {
				header = true
			}
		}
		if errors.Is(readErr, io.EOF) {
			return parentSession, sessionInit, size, true, nil
		}
	}
}

// readTranscriptLine returns one newline-terminated line. A non-negative limit
// bounds the bytes read, so an oversize line fails without being buffered whole.
//
// Example: a limit of 10 rejects a 20-byte line after one buffer read.
func readTranscriptLine(reader *bufio.Reader, limit int) ([]byte, error) {
	var line []byte
	for {
		chunk, err := reader.ReadSlice('\n')
		line = append(line, chunk...)
		if limit >= 0 && len(line) > limit {
			return nil, errTranscriptLineTooLong
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return line, err
		}
	}
}

// transcriptDuplicateSignalKey reports whether a top-level JSON object repeats
// type, customType, or parentSession. encoding/json keeps the last duplicate,
// which would otherwise hide a signal from the classifier.
//
// Example: {"type":"session","parentSession":"p","type":"message"} repeats type.
func transcriptDuplicateSignalKey(body []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	seen := make(map[string]bool, 3)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		key, ok := token.(string)
		if !ok {
			return false
		}
		if key == "type" || key == "customType" || key == "parentSession" {
			if seen[key] {
				return true
			}
			seen[key] = true
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return false
		}
	}
	return false
}

// transcriptSignal reports whether a record is a session header with a string
// parentSession, and whether it carries an omp session_init marker.
//
// Example: {"type":"session","parentSession":"x"} sets the first result.
func transcriptSignal(value any) (bool, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return false, false
	}
	recordType, _ := object["type"].(string)
	customType, _ := object["customType"].(string)
	sessionInit := recordType == "session_init" || customType == "session_init"
	_, hasParent := object["parentSession"].(string)
	return recordType == "session" && hasParent, sessionInit
}
