package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLaneLifecycleMergeSequenceRespectsPrecedence checks YAML merge semantics.
//
// Example: A first null lifecycle remains invalid despite a later valid default.
func TestLaneLifecycleMergeSequenceRespectsPrecedence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, merged string
		valid        bool
	}{
		{"first null", "[{lifecycle: null}, {lifecycle: ''}]", false},
		{"first string", "[{lifecycle: ''}, {lifecycle: null}]", true},
		{"later null", "[{context: note}, {lifecycle: null}]", false},
		{"no lifecycle", "[{context: note}, {next_action: proceed}]", true},
	} {
		t.Run(tc.name,
			// Validate one merged lane's effective lifecycle.
			//
			// Example: The first mapping that supplies a value wins.
			func(t *testing.T) {
				input := strings.Replace(linkedReport, "    required_environment: implementation", "    <<: "+tc.merged+"\n    required_environment: implementation", 1)
				require.NotEqual(t, linkedReport, input)
				_, err := parse([]byte(input))
				if tc.valid {
					require.NoError(t, err)
				} else {
					require.ErrorContains(t, err, "lifecycle")
				}
			})
	}
}
