package regproxy

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	conslog "github.com/EarthBuild/earthbuild/conslogging"
	"github.com/stretchr/testify/require"
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
	r.Equal(defaultCloseGrace, c.closeGrace)
}

// On Docker Desktop the readiness probe reaches the registry through the
// support container and back into the proxy. A connection it left in the
// client's idle pool would be a proxied connection held open for the rest of
// the build, which stopping the proxy then waited on.
func TestWaitForRegistryDoesNotKeepItsConnection(t *testing.T) {
	t.Parallel()

	closed := make(chan struct{}, 1)

	reg := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}")
	}))
	reg.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateClosed {
			select {
			case closed <- struct{}{}:
			default:
			}
		}
	}
	reg.Start()
	t.Cleanup(reg.Close)

	err := waitForRegistry(t.Context(), reg.URL+"/v2/")
	if err != nil {
		t.Fatalf("wait for registry: %v", err)
	}

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the probe left its connection to the registry open")
	}
}
