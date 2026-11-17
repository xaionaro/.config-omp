package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDecodeNativeBatchRequiresExactObservation checks the public CAS envelope.
//
// Example: a numeric generation is not an exact decimal string token.
func TestDecodeNativeBatchRequiresExactObservation(t *testing.T) {
	t.Parallel()
	valid := `{"base":{"database_id":"0123456789abcdef0123456789abcdef","generation":"42"},"ops":[]}`
	base, ops, err := decodeNativeBatch([]byte(valid))
	require.NoError(t, err)
	require.Equal(t, int64(42), base.Generation)
	require.Empty(t, ops)
	for _, value := range []string{
		`{"base":{"database_id":"0123456789abcdef0123456789abcdef","generation":42},"ops":[]}`,
		`{"base":{"database_id":"0123456789abcdef0123456789abcdef","generation":"01"},"ops":[]}`,
		`{"base":{"database_id":"x","generation":"0"},"ops":[]}`,
		`{"base":{"database_id":"0123456789abcdef0123456789abcdef","generation":"0","generation":"1"},"ops":[]}`,
	} {
		_, _, err := decodeNativeBatch([]byte(value))
		require.Error(t, err)
	}
}

// TestDecodeNativeObservationRequiresCanonicalResponse validates child tokens.
//
// Example: malformed child output must not be forwarded as a usable token.
func TestDecodeNativeObservationRequiresCanonicalResponse(t *testing.T) {
	t.Parallel()
	base, err := decodeNativeObservation([]byte(`{"database_id":"0123456789abcdef0123456789abcdef","generation":"42"}`))
	require.NoError(t, err)
	require.Equal(t, int64(42), base.Generation)
	for _, value := range []string{
		`{"database_id":"0123456789abcdef0123456789abcdef","generation":42}`,
		`{"database_id":"0123456789abcdef0123456789abcdef","generation":"01"}`,
		`{"database_id":"x","generation":"0"}`,
		`{"database_id":"0123456789abcdef0123456789abcdef","generation":"0","generation":"1"}`,
		`{"database_id":"0123456789abcdef0123456789abcdef","generation":"0","extra":true}`,
	} {
		_, err := decodeNativeObservation([]byte(value))
		require.Error(t, err)
	}
}
