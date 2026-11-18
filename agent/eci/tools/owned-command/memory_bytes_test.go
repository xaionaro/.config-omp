package main

import (
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMemoryBytesRejectsUnusableLimits checks explicit zero and the page boundary.
//
// Example: a one-page limit is accepted, but a smaller explicit limit is rejected.
func TestMemoryBytesRejectsUnusableLimits(t *testing.T) {
	t.Parallel()
	var limit MemoryBytes
	require.ErrorContains(t, limit.Set("0"), "memory limit must be positive")
	require.Error(t, limit.Set("1"))
	page := os.Getpagesize()
	require.NoError(t, limit.Set(strconv.Itoa(page)))
	require.Equal(t, MemoryBytes(page), limit)
}
