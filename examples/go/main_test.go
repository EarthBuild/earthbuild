package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHowCoolIsEarth(t *testing.T) {
	t.Parallel()

	require.Equal(t, "IceCool", howCoolIsEarth)
}
