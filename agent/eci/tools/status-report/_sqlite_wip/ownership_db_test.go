package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestOwnershipLegacyMigration preserves old markers and escaped literals.
//
// Example: Old escaped NUL values migrate to tagged values.
func TestOwnershipLegacyMigration(t *testing.T) {
	t.Parallel()
	var values map[string]syncOwnership
	err := json.Unmarshal([]byte(`{"a":"\u0000absent","n":"\u0000null","l":"\u0000literal-null","e":"\u0000value:\u0000absent","v":"ordinary"}`), &values)
	require.NoError(t, err)
	require.Equal(t, syncAbsentValue, values["a"])
	require.Equal(t, syncNullValue, values["n"])
	require.Equal(t, scalarSyncStoredValue("null"), values["l"])
	require.Equal(t, scalarSyncStoredValue("\x00absent"), values["e"])
	require.NotEqual(t, values["a"], values["e"])
	encoded, err := yaml.Marshal(values)
	require.NoError(t, err)
	var restored map[string]syncOwnership
	require.NoError(t, yaml.Unmarshal(encoded, &restored))
	require.Equal(t, values, restored)
}

// TestOwnershipLegacyYAMLImport preserves legacy and tagged field identities.
//
// Example: A tagged sentinel value remains distinct from legacy absence.
func TestOwnershipLegacyYAMLImport(t *testing.T) {
	t.Parallel()
	var values map[string]syncOwnership
	err := yaml.Unmarshal([]byte("absent: \"\\0absent\"\nnull_field: \"\\0null\"\nliteral: {kind: value, value: \"\\0absent\"}\n"), &values)
	require.NoError(t, err)
	require.Equal(t, syncAbsentValue, values["absent"])
	require.Equal(t, syncNullValue, values["null_field"])
	require.Equal(t, scalarSyncStoredValue("\x00absent"), values["literal"])
	require.NotEqual(t, values["absent"], values["literal"])
}

// TestOwnershipLiteralSentinelsRoundTrip preserves every historical marker as data.
//
// Example: An escaped-prefix string is not decoded a second time.
func TestOwnershipLiteralSentinelsRoundTrip(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"", "null", "\x00absent", "\x00null", "\x00literal-null", "\x00value:", "\x00value:\x00absent"} {
		value := scalarSyncStoredValue(text)
		raw, err := json.Marshal(value)
		require.NoError(t, err)
		var restored syncOwnership
		require.NoError(t, json.Unmarshal(raw, &restored))
		require.Equal(t, value, restored)
		require.NotEqual(t, syncAbsentValue, restored)
		require.NotEqual(t, syncNullValue, restored)
	}
}

// TestOwnershipLegacyNullRemainsAmbiguous preserves historical conflict behavior.
//
// Example: An untagged null string is neither a literal string nor explicit null.
func TestOwnershipLegacyNullRemainsAmbiguous(t *testing.T) {
	t.Parallel()
	var prior syncOwnership
	require.NoError(t, json.Unmarshal([]byte(`"null"`), &prior))
	require.NotEqual(t, syncNullValue, prior)
	require.NotEqual(t, scalarSyncStoredValue("null"), prior)
	raw, err := json.Marshal(prior)
	require.NoError(t, err)
	var restored syncOwnership
	require.NoError(t, json.Unmarshal(raw, &restored))
	require.Equal(t, prior, restored)
}

// TestOwnershipTypedValuesRemainDistinct verifies imported and literal ownership.
//
// Example: A literal sentinel string does not become an absent field.
func TestOwnershipTypedValuesRemainDistinct(t *testing.T) {
	t.Parallel()
	var state syncState
	err := json.Unmarshal([]byte(`{"applied":{"absent":{"kind":"absent"},"null":{"kind":"null"},"literal":{"kind":"value","value":"\u0000absent"},"old":"\u0000absent"}}`), &state)
	require.NoError(t, err)
	require.Equal(t, syncAbsentValue, state.Applied["absent"])
	require.Equal(t, syncNullValue, state.Applied["null"])
	require.Equal(t, syncAbsentValue, state.Applied["old"])
	require.NotEqual(t, state.Applied["absent"], state.Applied["literal"])
	require.NotEqual(t, state.Applied["null"], state.Applied["literal"])
	encoded, err := json.Marshal(state)
	require.NoError(t, err)
	var restored syncState
	require.NoError(t, json.Unmarshal(encoded, &restored))
	require.Equal(t, state.Applied, restored.Applied)
}

// TestOwnershipRejectsMalformedTags protects replay from ambiguous ownership.
//
// Example: An absent tag cannot carry a string value.
func TestOwnershipRejectsMalformedTags(t *testing.T) {
	t.Parallel()
	for _, input := range []string{`null`, `{"kind":"absent","value":"x"}`, `{"kind":"null","value":"x"}`, `{"kind":"value"}`, `{"kind":"other"}`} {
		var value syncOwnership
		require.Error(t, json.Unmarshal([]byte(input), &value), input)
	}
}
