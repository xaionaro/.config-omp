package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNativeLogObservation distinguishes absent, empty, replaced and binary logs.
//
// Example: Identical bytes in a replacement file do not satisfy an observed inode.
func TestNativeLogObservation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	absent, err := captureReplayLog(dir)
	require.NoError(t, err)
	observed, err := observeNativeLog(absent)
	require.NoError(t, err)
	require.NoError(t, checkNativeLogObservation(absent, observed))
	path := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(path, nil, 0600))
	empty, err := captureReplayLog(dir)
	require.NoError(t, err)
	require.Error(t, checkNativeLogObservation(empty, observed))
	observed, err = observeNativeLog(empty)
	require.NoError(t, err)
	require.NoError(t, checkNativeLogObservation(empty, observed))
	require.NoError(t, os.Rename(path, path+".old"))
	require.NoError(t, os.WriteFile(path, nil, 0600))
	replaced, err := captureReplayLog(dir)
	require.NoError(t, err)
	require.Error(t, checkNativeLogObservation(replaced, observed))
	require.NoError(t, os.WriteFile(path, []byte{0xff, 0x00, '\n'}, 0600))
	binary, err := captureReplayLog(dir)
	require.NoError(t, err)
	observed, err = observeNativeLog(binary)
	require.NoError(t, err)
	require.NoError(t, checkNativeLogObservation(binary, observed))
	require.Error(t, checkNativeLogObservation(replaced, observed))
}

// TestNativeLogObservationRejectsMalformedInput requires a complete canonical observation.
//
// Example: An absent log cannot claim identity or bytes.
func TestNativeLogObservationRejectsMalformedInput(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`{}`,
		`{"present":false,"device":"1","inode":"","bytes_base64":""}`,
		`{"present":true,"device":"01","inode":"2","bytes_base64":""}`,
		`{"present":true,"device":"1","inode":"2","bytes_base64":"@@@"}`,
		`{"present":false,"device":"","inode":"","bytes_base64":"","extra":true}`,
		`{"Present":false,"device":"","inode":"","bytes_base64":""}`,
		`{"present":false,"present":false,"device":"","inode":"","bytes_base64":""}`,
		`{"present":true,"device":"18446744073709551616","inode":"1","bytes_base64":""}`,
	} {
		_, err := decodeNativeLogObservation([]byte(raw))
		require.Error(t, err, raw)
	}
	_, err := decodeNativeLogObservation([]byte(`{"present":false,"device":"","inode":"","bytes_base64":""}`))
	require.NoError(t, err)
	_, err = decodeNativeLogObservation([]byte(`{"present":true,"device":"18446744073709551615","inode":"0","bytes_base64":""}`))
	require.NoError(t, err)
}
