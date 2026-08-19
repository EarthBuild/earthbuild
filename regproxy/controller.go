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
)

// Controller handles the management of the registry proxy. This may also
// include the Darwin proxy used to enable Docker Desktop setups.
type Controller struct {
	registryClient   registry.RegistryClient
	engine           *engine.Client
	darwinProxyImage string
	cons             conslog.ConsoleLogger
	darwinProxyWait  time.Duration
	darwinProxy      bool
}

// NewController creates and returns a new registry proxy controller.
func NewController(
	registryClient registry.RegistryClient,
	eng *engine.Client,
	darwinProxy bool,
	darwinProxyImage string,
	darwinProxyWait time.Duration,
	cons conslog.ConsoleLogger,
) *Controller {
	return &Controller{
		registryClient:   registryClient,
		engine:           eng,
		darwinProxy:      darwinProxy,
		darwinProxyImage: darwinProxyImage,
		darwinProxyWait:  darwinProxyWait,
		cons:             cons,
	}
}

// Start the proxy and create any support containers.
func (c *Controller) Start(ctx context.Context) (string, func(), error) {
	addr := "127.0.0.1:0"

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return "", nil, fmt.Errorf("failed to create proxy listener: %w", err)
	}

	p := newRegistryProxy(ln, c.registryClient)
	go p.serve(ctx)

	// Find the assigned port.
	reg, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return "", nil, errors.New("failed to get proxy listener address")
	}

	addr = fmt.Sprintf("127.0.0.1:%d", reg.Port)

	c.cons.VerbosePrintf("Starting registry proxy on %s", addr)

	doneCh := make(chan struct{})

	go func() {
		for err := range p.err() {
			if err != nil && !errors.Is(err, context.Canceled) {
				c.cons.VerbosePrintf("Failed to serve registry proxy: %v", err)
			}
		}

		doneCh <- struct{}{}
	}()

	closers := []func(ctx context.Context){
		func(ctx context.Context) {
			p.close()

			select {
			case <-ctx.Done():
			case <-doneCh:
			}
		},
	}

	if c.darwinProxy {
		containerName := fmt.Sprintf("%s-%s", darwinContainerPrefix, stringutil.RandomAlphanumeric(6))
		stopFn := func(_ context.Context) {
			err := c.stopDarwinProxy(containerName, true) //nolint:contextcheck
			if err != nil {
				c.cons.VerbosePrintf("Failed to stop registry proxy support container: %v", err)
			}
		}

		port, err := c.startDarwinProxy(ctx, containerName, reg.Port)
		if err != nil {
			stopFn(ctx)
			return "", nil, fmt.Errorf("failed to start Darwin support container: %w", err)
		}

		addr = fmt.Sprintf("127.0.0.1:%d", port)
		c.cons.VerbosePrintf("Starting Darwin proxy on %s", addr)

		closers = append(closers, stopFn)
	}

	return addr, func() {
		for _, closer := range closers {
			closer(ctx)
		}
	}, nil
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
			c.cons.VerbosePrintf("Failed to stop old Darwin proxy support container: %s", err)
		}
	}()

	containerPort, err := acquireFreePort(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to acquire free port: %w", err)
	}

	spec := engine.ContainerSpec{
		NameOrID: containerName,
		ImageRef: c.darwinProxyImage,
		Ports: []engine.Port{
			{
				IP:            "127.0.0.1",
				HostPort:      containerPort, // Bind to available port
				ContainerPort: 80,
				Protocol:      engine.ProtocolTCP,
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

	// Wait for the proxy chain to resolve to the BK registry. The /v2/ path
	// will return a 200 when ready.
	for {
		url := fmt.Sprintf("http://127.0.0.1:%d/v2/", containerPort)

		req, err := http.NewRequestWithContext(childCtx, http.MethodGet, url, nil)
		if err != nil {
			return 0, err
		}

		res, err := http.DefaultClient.Do(req) // #nosec G704
		if res != nil && res.Body != nil {
			res.Body.Close() // #nosec G104
		}

		if err == nil && res != nil && res.StatusCode == http.StatusOK {
			break
		}

		select {
		case <-childCtx.Done():
			return 0, childCtx.Err()
		case <-time.After(time.Second):
			continue
		}
	}

	return containerPort, nil
}

func (c *Controller) stopOldDarwinProxies(ctx context.Context) error {
	containers, err := c.engine.ListContainers(ctx)
	if err != nil {
		return err
	}

	for _, cntr := range containers {
		if strings.HasPrefix(cntr.Name, darwinContainerPrefix) &&
			time.Since(cntr.Created) > darwinContainerMaxAge {
			err = c.stopDarwinProxy(cntr.Name, false) //nolint:contextcheck
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *Controller) stopDarwinProxy(containerName string, checkExists bool) error {
	// Ignore parent context cancellations as to prevent orphaned containers.
	detachedCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if checkExists {
		info, err := c.engine.InspectContainer(detachedCtx, containerName)
		if err != nil {
			return err
		}

		if info.Status == engine.StatusMissing {
			return nil
		}
	}

	err := c.engine.RemoveContainer(detachedCtx, true, containerName)
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
