package base

import (
	"sync/atomic"
	"testing"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/internal/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStopBuildkitOnExit(t *testing.T) {
	t.Parallel()

	t.Run("nil engine does not register deferred stop", func(t *testing.T) {
		t.Parallel()

		cli := NewCLI(new(conslogging.ConsoleLogger))
		cli.Flags().Engine = nil

		cli.StopBuildkitOnExit(t.Context())
		assert.Empty(t, cli.deferredFuncs)
	})

	t.Run("non-apple engine does not register deferred stop", func(t *testing.T) {
		t.Parallel()

		cli := NewCLI(new(conslogging.ConsoleLogger))
		cli.Flags().Engine = engine.NewTestClient(engine.Metadata{
			Name:   "Docker",
			Scheme: engine.SchemeDocker,
		})

		cli.StopBuildkitOnExit(t.Context())
		assert.Empty(t, cli.deferredFuncs)
	})

	t.Run("apple engine registers deferred stop and is idempotent", func(t *testing.T) {
		t.Parallel()

		cli := NewCLI(new(conslogging.ConsoleLogger))
		cli.Flags().Engine = engine.NewTestClient(engine.Metadata{
			Name:   "Apple Container",
			Scheme: engine.SchemeApple,
		})
		cli.Flags().ContainerName = "test-buildkitd"

		cli.StopBuildkitOnExit(t.Context())
		require.Len(t, cli.deferredFuncs, 1)

		// Subsequent calls must be idempotent
		cli.StopBuildkitOnExit(t.Context())
		assert.Len(t, cli.deferredFuncs, 1)
	})

	t.Run("execute deferred funcs runs all registered hooks", func(t *testing.T) {
		t.Parallel()

		cli := NewCLI(new(conslogging.ConsoleLogger))

		var executed atomic.Int32

		cli.AddDeferredFunc(func() {
			executed.Add(1)
		})
		cli.AddDeferredFunc(func() {
			executed.Add(10)
		})

		cli.ExecuteDeferredFuncs()
		assert.Equal(t, int32(11), executed.Load())
	})
}
