package regproxy

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"

	registry "github.com/moby/buildkit/api/services/registry"
)

// newRegistryProxy creates and returns a new registry proxy that streams Docker
// container image data from the BK embedded Docker registry.
func newRegistryProxy(ln net.Listener, cl registry.RegistryClient) *registryProxy {
	return &registryProxy{ln: ln, cl: cl, errCh: make(chan error)}
}

// registryProxy uses a gRPC stream to translate incoming Docker image requests
// into a gRPC byte stream and back out into a valid HTTP response. The data is
// streamed over the gRPC connection rather than buffered as the images can be
// rather large.
type registryProxy struct {
	ln    net.Listener
	cl    registry.RegistryClient
	errCh chan error
	done  atomic.Bool
}

// Serve waits for TCP connections and pipes data received from the connection
// to BK via the gRPC server.
func (r *registryProxy) serve(ctx context.Context) {
	var wg sync.WaitGroup

	defer func() {
		wg.Wait()
		close(r.errCh)
	}()

	for {
		select {
		case <-ctx.Done():
			r.errCh <- ctx.Err()
			return
		default:
			conn, err := r.ln.Accept()
			if err != nil {
				if !r.done.Load() {
					r.errCh <- fmt.Errorf("failed to accept: %w", err)
				}

				return
			}

			wg.Go(func() {
				r.errCh <- r.handle(ctx, conn)
			})
		}
	}
}

func (r *registryProxy) close() {
	r.done.Store(true)
	r.ln.Close() // #nosec G104
}

func (r *registryProxy) err() <-chan error {
	return r.errCh
}

func (r *registryProxy) handle(ctx context.Context, conn net.Conn) error {
	stream, err := r.cl.Proxy(ctx)
	if err != nil {
		conn.Close() // #nosec G104
		return fmt.Errorf("failed to create proxy stream: %w", err)
	}

	// The bytes are opaque in both directions: each ends when its source ends
	// it, and that end is passed on as a half-close, so a request whose
	// response is still arriving is never cut short. Copy closes conn.
	err = registry.Copy(ctx, conn, stream, stream.CloseSend)
	if err != nil {
		return fmt.Errorf("failed to proxy the connection: %w", err)
	}

	return nil
}
