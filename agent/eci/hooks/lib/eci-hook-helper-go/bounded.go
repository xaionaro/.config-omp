package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

// inputBudget is the largest record the helpers ever retain or emit.
const inputBudget = 65536

// readBounded reads r until EOF. It never retains more than inputBudget+1
// bytes; overflow reports that the input was larger than inputBudget.
func readBounded(r io.Reader) (data []byte, overflow bool, err error) {
	buf := make([]byte, 0, inputBudget+1)
	for len(buf) <= inputBudget {
		n, readErr := r.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if errors.Is(readErr, io.EOF) {
			return buf, false, nil
		}
		if readErr != nil {
			return nil, false, readErr
		}
	}
	return nil, true, nil
}

// readHookStdin returns the bounded stdin bytes or the failing status.
func readHookStdin(stdin io.Reader) ([]byte, int) {
	data, overflow, err := readBounded(stdin)
	if err != nil {
		return nil, statusFailed
	}
	if overflow {
		return nil, statusRejected
	}
	return data, statusOK
}

// validBounded reports whether data fits the budget, contains no NUL byte,
// and decodes as strict UTF-8.
func validBounded(data []byte) bool {
	return len(data) <= inputBudget && !bytes.Contains(data, []byte{0}) && utf8.Valid(data)
}

// emitBounded writes data to w only when validBounded accepts it.
func emitBounded(data []byte, w io.Writer) int {
	if !validBounded(data) {
		return statusRejected
	}
	if _, err := w.Write(data); err != nil {
		return statusFailed
	}
	return statusOK
}

// hookTranscriptPath parses the hook JSON object on stdin and returns its
// non-empty string transcript_path.
func hookTranscriptPath(stdin io.Reader) (string, int) {
	data, status := readHookStdin(stdin)
	if status != statusOK {
		return "", status
	}
	if !validBounded(data) {
		return "", statusRejected
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		return "", statusRejected
	}
	path, ok := object["transcript_path"].(string)
	if !ok || path == "" {
		return "", statusRejected
	}
	return path, statusOK
}
