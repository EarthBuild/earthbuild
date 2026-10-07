package dockerutil

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/internal/engine"
	"github.com/EarthBuild/earthbuild/util/hint"
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

// A client with no container frontend cannot load an exported image; the error
// must say how to build without local image output rather than fail cryptically.
func TestLoadDockerTarWithoutContainerFrontend(t *testing.T) {
	t.Parallel()

	eng, err := engine.NewStub(&engine.Config{})
	require.NoError(t, err)

	err = LoadDockerTar(t.Context(), eng, io.NopCloser(strings.NewReader("")))
	require.ErrorIs(t, err, engine.ErrNotInitialized)

	hintErr, ok := errors.AsType[*hint.Error](err)
	require.True(t, ok, "want a hint error, got %T", err)
	require.Contains(t, hintErr.Hint(), "--no-image-output")
}

// Pulling an image from the local registry ends in the container frontend too,
// so it must carry the same hint as loading a tar.
func TestDockerPullLocalImagesWithoutContainerFrontend(t *testing.T) {
	t.Parallel()

	eng, err := engine.NewStub(&engine.Config{})
	require.NoError(t, err)

	err = DockerPullLocalImages(t.Context(), eng, "127.0.0.1:8371", map[string]string{"sess-abc/img:latest": "img:latest"})
	require.ErrorIs(t, err, engine.ErrNotInitialized)

	hintErr, ok := errors.AsType[*hint.Error](err)
	require.True(t, ok, "want a hint error, got %T", err)
	require.Contains(t, hintErr.Hint(), "--no-image-output")
}
