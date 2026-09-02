package base

import (
	"sync/atomic"
	"testing"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/stretchr/testify/assert"
)

func TestDeferredFuncs(t *testing.T) {
	t.Parallel()

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
