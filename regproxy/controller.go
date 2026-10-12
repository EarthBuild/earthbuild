package regproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	conslog "github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/internal/engine"
	"github.com/EarthBuild/earthbuild/util/stringutil"
	registry "github.com/moby/buildkit/api/services/registry"
)

const (
	darwinContainerPrefix = "earthly-darwin-proxy"
	darwinContainerMaxAge = 5 * time.Hour

	// defaultCloseGrace is how long stopping the proxy waits for the
	// connections still open to end on their own before it closes them. It
	// only lets a connection that is already ending finish cleanly, so it is
	// short: the proxy is stopped once the build is over, when every pull
	// through it has returned, and a connection still open by then is a
	// client's idle keep-alive (docker keeps the one it pulled over) or one
	// that will never end. Waiting on those only delays exit.
	defaultCloseGrace = 250 * time.Millisecond
)

// Controller handles the management of the registry proxy. This may also
// include the Darwin proxy used to enable Docker Desktop setups.
type Controller struct {
	registryClient   registry.RegistryClient
	engine           *engine.Client
	log              *conslog.ConsoleLogger
	darwinProxyImage string
	darwinProxyWait  time.Duration
	closeGrace       time.Duration
	darwinProxy      bool
}

// NewController creates and returns a new registry proxy controller.
func NewController(
	registryClient registry.RegistryClient,
	eng *engine.Client,
	darwinProxy bool,
	darwinProxyImage string,
	darwinProxyWait time.Duration,
	log *conslog.ConsoleLogger,
) *Controller {
	return &Controller{
		registryClient:   registryClient,
		engine:           eng,
		darwinProxy:      darwinProxy,
		darwinProxyImage: darwinProxyImage,
		darwinProxyWait:  darwinProxyWait,
		closeGrace:       defaultCloseGrace,
		log:              log,
	}
}

// Start the proxy and create any support containers. It returns the address
// the proxy is reachable on and a function that stops it, which waits at most a
// short grace period on any connection a client still holds open.
func (c *Controller) Start(ctx context.Context) (string, func(), error) {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create proxy listener: %w", err)
	}

	// Find the assigned port.
	lnAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		ln.Close() // #nosec G104
		return "", nil, errors.New("failed to get proxy listener address")
	}

	addr := fmt.Sprintf("127.0.0.1:%d", lnAddr.Port)

	c.log.VerbosePrintf("Starting registry proxy on %s", addr)

	stopProxy := c.serve(ctx, ln)
	closers := []func(ctx context.Context){stopProxy}

	if c.darwinProxy {
		containerName := fmt.Sprintf("%s-%s", darwinContainerPrefix, stringutil.RandomAlphanumeric(6))
		stopFn := func(ctx context.Context) {
			err := c.stopDarwinProxy(ctx, containerName, true)
			if err != nil {
				c.log.VerbosePrintf("Failed to stop registry proxy support container: %v", err)
			}
		}

		port, err := c.startDarwinProxy(ctx, containerName, lnAddr.Port)
		if err != nil {
			stopFn(ctx)
			stopProxy(ctx)

			return "", nil, fmt.Errorf("failed to start Darwin support container: %w", err)
		}

		addr = fmt.Sprintf("127.0.0.1:%d", port)
		c.log.VerbosePrintf("Starting Darwin proxy on %s", addr)

		closers = append(closers, stopFn)
	}

	return addr, func() {
		for _, closer := range closers {
			closer(ctx)
		}
	}, nil
}

// serve proxies the connections accepted on ln until the returned function is
// called. That function stops accepting, gives the connections still open
// c.closeGrace to end on their own, and then closes them. A proxied connection
// lasts as long as its client keeps it, so without that bound a client holding
// an idle keep-alive, or one that never ends its connection, would keep the
// build from exiting.
func (c *Controller) serve(ctx context.Context, ln net.Listener) func(context.Context) {
	// The connections get their own context so they can be ended without
	// cancelling ctx.
	connCtx, closeConns := context.WithCancel(ctx)

	p := newRegistryProxy(ln, c.registryClient)
	go p.serve(connCtx)

	done := make(chan struct{})

	go func() {
		defer close(done)

		for err := range p.err() {
			// Once the connections have been closed on purpose, their errors
			// only say so.
			if err != nil && connCtx.Err() == nil && !errors.Is(err, context.Canceled) {
				c.log.VerbosePrintf("Failed to serve registry proxy: %v", err)
			}
		}
	}()

	return func(ctx context.Context) {
		defer closeConns()

		p.close()

		grace := time.NewTimer(c.closeGrace)
		defer grace.Stop()

		select {
		case <-done:
			return
		case <-grace.C:
			c.log.VerbosePrintf("Closing registry proxy connections still open after %s", c.closeGrace)
		case <-ctx.Done():
		}

		// Closing a connection ends its handler promptly whatever the client
		// does, so this wait is short.
		closeConns()
		<-done
	}
}

// startDarwinProxy: Since Docker Desktop (Mac) containers run in a VM, a
// special host name, host.docker.internal, is made available to access the host
// machine. Docker can only pull insecurely from localhost, so we use a socat
// container to proxy localhost:<port> request back out to the local registry
// proxy created above.
func (c *Controller) startDarwinProxy(ctx context.Context, containerName string, registryPort int) (int, error) {
	go func() {
		err := c.stopOldDarwinProxies(ctx)
		if err != nil {
			c.log.VerbosePrintf("Failed to stop old Darwin proxy support container: %v", err)
		}
	}()

	containerPort, err := acquireFreePort(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to acquire free port: %w", err)
	}

	spec := engine.ContainerSpec{
		NameOrID: containerName,
		ImageRef: c.darwinProxyImage,
		PortMappings: []engine.PortMapping{
			{
				HostIP:        "127.0.0.1",
				HostPort:      containerPort, // Bind to available port
				ContainerPort: 80,
			},
		},
		ContainerArgs: []string{
			"tcp-listen:80,fork,reuseaddr",
			fmt.Sprintf("tcp:host.docker.internal:%d", registryPort),
		},
	}

	err = c.engine.RunContainer(ctx, spec)
	if err != nil {
		return 0, fmt.Errorf("failed to start support container: %w", err)
	}

	childCtx, cancel := context.WithTimeout(ctx, c.darwinProxyWait)
	defer cancel()

	// Wait for the proxy chain to resolve to the BK registry.
	err = waitForRegistry(childCtx, fmt.Sprintf("http://127.0.0.1:%d/v2/", containerPort))
	if err != nil {
		return 0, err
	}

	return containerPort, nil
}

// waitForRegistry polls url, a registry's /v2/ path, until it answers 200 OK or
// ctx is done.
func waitForRegistry(ctx context.Context, url string) error {
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}

		// The connection runs through the support container and back into this
		// process's own registry proxy. Kept in the client's pool of idle
		// connections, it would hold a proxied connection open for the rest of
		// the build, and stopping the proxy would wait on it.
		req.Close = true

		res, err := http.DefaultClient.Do(req) // #nosec G704
		if res != nil && res.Body != nil {
			res.Body.Close() // #nosec G104
		}

		if err == nil && res != nil && res.StatusCode == http.StatusOK {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (c *Controller) stopOldDarwinProxies(ctx context.Context) error {
	containers, err := c.engine.ListContainers(ctx)
	if err != nil {
		return err
	}

	for _, container := range containers {
		if strings.HasPrefix(container.Name, darwinContainerPrefix) &&
			time.Since(container.Created) > darwinContainerMaxAge {
			err = c.stopDarwinProxy(ctx, container.Name, false)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *Controller) stopDarwinProxy(ctx context.Context, containerName string, checkExists bool) error {
	// Ignore parent context cancellations to prevent orphaned containers.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()

	if checkExists {
		info, err := c.engine.InspectContainer(ctx, containerName)
		if err != nil {
			return err
		}

		if info.Status == engine.StatusMissing {
			return nil
		}
	}

	err := c.engine.RemoveContainer(ctx, true, containerName)
	if err != nil {
		return fmt.Errorf("failed to stop support container: %w", err)
	}

	return nil
}

func acquireFreePort(ctx context.Context) (int, error) {
	addr := "127.0.0.1:0"

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return 0, fmt.Errorf("listen on open port: %w", err)
	}
	defer ln.Close() // Immediately close the listener

	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return 0, errors.New("get TCP address")
	}

	return tcpAddr.Port, nil
}
