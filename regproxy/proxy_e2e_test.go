package regproxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	conslog "github.com/EarthBuild/earthbuild/conslogging"
	registry "github.com/moby/buildkit/api/services/registry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// These tests put the whole proxy chain between a real net/http client and a
// real HTTP server standing in for buildkitd's embedded registry:
//
//	http.Client -> Controller.Start's listener -> handle -> gRPC over loopback
//	  -> buildkit's registry.Server.Proxy -> registry
//
// Nothing in the chain is faked. buildkit's own tests cover Server.Proxy
// against a hand-written client; these cover it against this repo's client,
// which is the pairing that ships.

// chain starts a registry serving h and the proxy chain in front of it. It
// returns an HTTP client and the base URL to reach the registry through the
// proxy, and a count of the connections the registry accepted.
func chain(t *testing.T, h http.HandlerFunc) (*http.Client, string, func() int64) {
	t.Helper()

	c := newChain(t, h, defaultCloseGrace)

	return c.client, c.base, c.conns
}

// proxyChain is a registry and the proxy chain in front of it.
type proxyChain struct {
	// client reaches the registry through the proxy at base.
	client *http.Client
	// conns counts the connections the registry accepted.
	conns func() int64
	// stop stops the proxy as a build does once it is over. It is also called
	// when the test ends, after the client's idle connections are closed.
	stop func()
	base string
}

// newChain is chain, with the proxy waiting closeGrace on open connections
// when it is stopped.
func newChain(t *testing.T, h http.HandlerFunc, closeGrace time.Duration) *proxyChain {
	t.Helper()

	var conns atomic.Int64

	reg := httptest.NewUnstartedServer(h)
	reg.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	reg.Start()
	t.Cleanup(reg.Close)

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for gRPC: %v", err)
	}

	gs := grpc.NewServer()
	registry.RegisterRegistryServer(gs, registry.NewServer(reg.Listener.Addr().String()))

	go func() { _ = gs.Serve(ln) }()

	t.Cleanup(gs.Stop)

	cc, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial gRPC: %v", err)
	}

	t.Cleanup(func() { _ = cc.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	ctrl := NewController(registry.NewRegistryClient(cc), nil, false, "", 0, conslog.Current(0, conslog.Info, false))
	ctrl.closeGrace = closeGrace

	addr, stop, err := ctrl.Start(ctx)
	if err != nil {
		t.Fatalf("start proxy: %v", err)
	}

	stop = sync.OnceFunc(stop)
	tr := &http.Transport{}

	// Idle connections are closed before the proxy, so stopping it does not
	// spend its grace period on a kept-alive connection the client would
	// otherwise hold.
	t.Cleanup(stop)
	t.Cleanup(tr.CloseIdleConnections)

	return &proxyChain{
		// A proxy that mishandles termination strands a request rather than
		// failing it, so bound every request: a hung pull is a failure too.
		client: &http.Client{Transport: tr, Timeout: 20 * time.Second},
		base:   "http://" + addr,
		conns:  conns.Load,
		stop:   stop,
	}
}

// blob is a stand-in for a layer: larger than the 32KiB copy buffers, so it
// crosses the stream as many messages.
func blob(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 251)
	}

	return b
}

func get(t *testing.T, client *http.Client, url string) []byte {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET through the proxy: %v", err)
	}
	defer resp.Body.Close()

	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read proxied body: %v (got %d bytes)", err, len(got))
	}

	return got
}

func TestProxyChainServesAResponse(t *testing.T) {
	t.Parallel()

	want := blob(128 * 1024)

	client, base, _ := chain(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(want)))
		_, _ = w.Write(want)
	})

	got := get(t, client, base+"/v2/img/blobs/sha256:0")
	if !bytes.Equal(got, want) {
		t.Errorf("body: got %d bytes, want the %d the registry served", len(got), len(want))
	}
}

// The registry pausing mid-body is not the end of the response. The old
// client called 50ms of silence the end and truncated.
func TestProxyChainDoesNotTruncateAResponseThatStalls(t *testing.T) {
	t.Parallel()

	want := blob(128 * 1024)

	client, base, _ := chain(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(want)))
		half := len(want) / 2

		_, _ = w.Write(want[:half])
		_ = http.NewResponseController(w).Flush()

		time.Sleep(300 * time.Millisecond)

		_, _ = w.Write(want[half:])
	})

	got := get(t, client, base+"/v2/img/blobs/sha256:0")
	if !bytes.Equal(got, want) {
		t.Errorf("body: got %d bytes, want the %d the registry served", len(got), len(want))
	}
}

// The request direction is this repo's half of the fix: handle used to close
// its send side after 50ms of quiet from docker, cutting off a request body
// that arrived in two parts.
func TestProxyChainDoesNotTruncateARequestThatStalls(t *testing.T) {
	t.Parallel()

	body := blob(128 * 1024)

	client, base, _ := chain(t, func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		_, _ = fmt.Fprint(w, n)
	})

	pr, pw := io.Pipe()

	go func() {
		half := len(body) / 2

		_, _ = pw.Write(body[:half])

		time.Sleep(300 * time.Millisecond)

		_, _ = pw.Write(body[half:])
		_ = pw.Close()
	}()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, base+"/v2/img/blobs/uploads/0", pr)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	req.ContentLength = int64(len(body))

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("PUT through the proxy: %v", err)
	}
	defer resp.Body.Close()

	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	if want := strconv.Itoa(len(body)); string(got) != want {
		t.Errorf("registry received %s bytes, want %s", got, want)
	}
}

// docker keeps one connection across the manifest and blob requests of a
// pull, with its own work in between. The old client ended the connection
// after the first response, so the second request was never forwarded.
func TestProxyChainKeepsAConnectionAliveAcrossRequests(t *testing.T) {
	t.Parallel()

	want := blob(4096)

	client, base, conns := chain(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(want)))
		_, _ = w.Write(want)
	})

	for i := range 2 {
		if i > 0 {
			time.Sleep(200 * time.Millisecond)
		}

		got := get(t, client, base+"/v2/img/manifests/latest")
		if !bytes.Equal(got, want) {
			t.Fatalf("response %d: got %d bytes, want %d", i+1, len(got), len(want))
		}
	}

	if n := conns(); n != 1 {
		t.Errorf("registry connections: got %d, want 1 -- the proxy did not keep the connection alive", n)
	}
}

// The proxy is stopped once the build is over, and a proxied connection lasts
// as long as its client keeps it. Stopping used to wait for every client to let
// go, so one holding its connection open kept the build from exiting: on macOS
// the Docker Desktop readiness probe's own idle keep-alive did so after every
// build, for net/http's 90s idle timeout.
func TestProxyStopDoesNotWaitOnAConnectionTheClientKeepsOpen(t *testing.T) {
	t.Parallel()

	want := blob(4096)
	h := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(want)))
		_, _ = w.Write(want)
	}

	tests := []struct {
		// hold leaves a connection to the proxy open, with the proxy serving it.
		hold func(t *testing.T, c *proxyChain)
		name string
	}{
		{
			// An HTTP client keeps the connection of a finished request to reuse.
			name: "idle keep-alive",
			hold: func(t *testing.T, c *proxyChain) {
				t.Helper()

				if got := get(t, c.client, c.base+"/v2/"); !bytes.Equal(got, want) {
					t.Fatalf("body: got %d bytes, want %d", len(got), len(want))
				}
			},
		},
		{
			// A client stops part way through a request and never finishes it.
			name: "unfinished request",
			hold: func(t *testing.T, c *proxyChain) {
				t.Helper()

				conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", strings.TrimPrefix(c.base, "http://"))
				if err != nil {
					t.Fatalf("dial the proxy: %v", err)
				}

				t.Cleanup(func() { _ = conn.Close() })

				_, err = io.WriteString(conn, "GET /v2/ HTTP/1.1\r\nHost: registry\r\n")
				if err != nil {
					t.Fatalf("write a partial request: %v", err)
				}

				// The registry accepting a connection means the proxy took this
				// one and opened its stream.
				deadline := time.Now().Add(10 * time.Second)

				for c.conns() == 0 {
					if time.Now().After(deadline) {
						t.Fatal("the proxy never forwarded the connection")
					}

					time.Sleep(10 * time.Millisecond)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := newChain(t, h, 100*time.Millisecond)
			tt.hold(t, c)

			stopped := make(chan struct{})

			go func() {
				c.stop()
				close(stopped)
			}()

			select {
			case <-stopped:
			case <-time.After(10 * time.Second):
				t.Fatal("stopping the proxy is still waiting on the connection the client holds open")
			}
		})
	}
}
