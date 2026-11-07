package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/require"
)

// TestTaggedFlowScannerPreservesSourcePositions checks the scanner-only mask.
//
// Example: Multiple explicit tags do not shift delimiters or hide owner comments.
func TestTaggedFlowScannerPreservesSourcePositions(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n"} {
		t.Run(strings.ReplaceAll(ending, "\r", "CR"),
			// Compare real byte positions and comment detection under one line ending.
			//
			// Example: A quoted hash is data while the following hash is a comment.
			func(t *testing.T) {
				input := []byte("record: {label: \"!!str # data\", first: &owner !!str 'λ#1', second: !!str '2', custom: !<tag:example.org,2024:thing> keep} # owner note" + ending)
				before := append([]byte(nil), input...)
				scan, err := flowScanBytes(input)
				require.NoError(t, err)
				require.Equal(t, before, input)
				require.Contains(t, string(scan), "\"!!str # data\"")
				require.Contains(t, string(scan), "# owner note")
				start := bytes.IndexByte(input, '{')
				end, err := flowEnd(input, start, token.MappingStartType)
				require.NoError(t, err)
				require.Equal(t, bytes.LastIndexByte(input, '}'), end)
				require.False(t, yamlRangeHasComment(input, start, end+1))
				require.True(t, yamlRangeHasComment(input, end+1, len(input)))
			})
	}
}
