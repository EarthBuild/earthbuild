package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsEarthlyAmazing(t *testing.T) {
	t.Parallel()

	require.Equal(t, "IceCool", howCoolIsEarthly)
}
