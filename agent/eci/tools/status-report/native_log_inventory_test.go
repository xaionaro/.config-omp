package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNativeLogInventoryRetainsCompleteOccurrenceBoundaries checks the DB-independent catalog.
//
// Example: A partial final line remains in the observation but is not inventoried.
func TestNativeLogInventoryRetainsCompleteOccurrenceBoundaries(t *testing.T) {
	t.Parallel()
	log := []byte(`{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"A"}}]}}}` + "\n" + `{"partial":`)
	request := fmt.Sprintf(`{"present":true,"device":"1","inode":"2","bytes_base64":%q}`, base64.StdEncoding.EncodeToString(log))
	var out, diagnostics bytes.Buffer
	code := run([]string{"log-inventory", "--stdin"}, bytes.NewBufferString(request), &out, &diagnostics)
	require.Equal(t, 0, code, diagnostics.String())
	var result struct {
		CompleteLines int `json:"complete_lines"`
		Occurrences []struct { Ref string `json:"ref"`; Update json.RawMessage `json:"update"` } `json:"occurrences"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &result))
	require.Equal(t, 1, result.CompleteLines)
	require.Len(t, result.Occurrences, 1)
	require.Equal(t, "1/u/0", result.Occurrences[0].Ref)
	require.NotEmpty(t, result.Occurrences[0].Update)
}

// TestNativeLogInventoryRejectsMalformedCompleteLine distinguishes a terminated bad line from a tail.
func TestNativeLogInventoryRejectsMalformedCompleteLine(t *testing.T) {
	t.Parallel()
	request := fmt.Sprintf(`{"present":true,"device":"1","inode":"2","bytes_base64":%q}`, base64.StdEncoding.EncodeToString([]byte("{bad\n")))
	var out, diagnostics bytes.Buffer
	code := run([]string{"log-inventory", "--stdin"}, bytes.NewBufferString(request), &out, &diagnostics)
	require.Equal(t, 1, code)
	require.Empty(t, out.String())
	require.NotEmpty(t, diagnostics.String())
}
