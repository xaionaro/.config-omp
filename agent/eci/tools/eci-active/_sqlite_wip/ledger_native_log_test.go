package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNativeLogWireObservation preserves presence, identity and every byte.
//
// Example: an absent log differs from an existing empty log.
func TestNativeLogWireObservation(t *testing.T) {
	t.Parallel()
	absent, err := makeNativeLogObservation(nil, nil)
	require.NoError(t, err)
	require.False(t, absent.Present)
	require.Empty(t, absent.Device)
	require.Empty(t, absent.Inode)
	require.Empty(t, absent.BytesBase64)

	path := filepath.Join(t.TempDir(), "log")
	data := []byte{0xff, 0, '\n'}
	require.NoError(t, os.WriteFile(path, data, 0600))
	info, err := os.Stat(path)
	require.NoError(t, err)
	present, err := makeNativeLogObservation(info, data)
	require.NoError(t, err)
	require.True(t, present.Present)
	require.NotEmpty(t, present.Device)
	require.NotEmpty(t, present.Inode)
	require.Equal(t, base64.StdEncoding.EncodeToString(data), present.BytesBase64)
	empty, err := makeNativeLogObservation(info, nil)
	require.NoError(t, err)
	require.True(t, empty.Present)
	require.Empty(t, empty.BytesBase64)
}
