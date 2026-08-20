package regproxy

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	conslog "github.com/EarthBuild/earthbuild/conslogging"
)

func TestNewController(t *testing.T) {
	t.Parallel()

	// A simple regression test that ensures the values are passed correctly.
	cons := conslog.Current(0, conslog.Info, false)
	c := NewController(nil, nil, true, "proxy-image", time.Second, cons)
	r := require.New(t)
	r.Equal("proxy-image", c.darwinProxyImage)
	r.Equal(time.Second, c.darwinProxyWait)
	r.True(c.darwinProxy)
}
