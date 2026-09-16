package base

import (
	"sync/atomic"
	"testing"

	"github.com/EarthBuild/earthbuild/config"
	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
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

func TestInitBuildkit(t *testing.T) {
	t.Parallel()

	t.Run("remote TCP host with missing CA certificate returns error", func(t *testing.T) {
		t.Parallel()

		c := NewCLI(new(conslogging.ConsoleLogger))
		c.SetCfg(&config.Config{
			Global: config.GlobalConfig{
				TLSEnabled: true,
				TLSCACert:  "/nonexistent/ca.pem",
			},
		})
		c.Flags().BuildkitHost = "tcp://remote.buildkit.example.com:8372"

		cmd := new(cli.Command)
		err := c.InitBuildkit(cmd)
		require.ErrorContains(t, err, "requires existing CA certificate")
	})
}
