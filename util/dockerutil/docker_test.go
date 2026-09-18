package dockerutil

import (
	"context"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/internal/engine"
	"github.com/stretchr/testify/require"
)

func TestWaitForImageTimeout(t *testing.T) {
	t.Parallel()

	eng, err := engine.NewStub(&engine.Config{})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
	defer cancel()

	start := time.Now()
	err = waitForImage(ctx, eng, "nonexistent-image:latest")
	elapsed := time.Since(start)

	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.GreaterOrEqual(t, elapsed, 200*time.Millisecond)
}
