package container

import (
	"context"
	"fmt"
	"io"
	"time"
)

// stubEngine is a null/stub engine for use when a container engine is not available or needed
// (e.g. remote satellite builds).
type stubEngine struct {
	*shellEngine
}

// newStubEngine creates a stub engine instance.
func newStubEngine(cfg *Config) (engineDriver, error) {
	e := &stubEngine{
		shellEngine: &shellEngine{Console: cfg.Console},
	}

	var err error

	e.Endpoints, err = e.ResolveEndpoints(DriverStub, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate buildkit URLs: %w", err)
	}

	return e, nil
}

// NewStub creates a stub engine instance for fallback/tests.
func NewStub(cfg *Config) (*Client, error) {
	drv, err := newStubEngine(cfg)
	if err != nil {
		return nil, err
	}

	return &Client{driver: drv}, nil
}

// NewFake is an alias for NewStub for backwards compatibility.
func NewFake(cfg *Config) (*Client, error) {
	return NewStub(cfg)
}

type mockDriver struct {
	stubEngine

	meta Metadata
}

func (m *mockDriver) Metadata() Metadata {
	return m.meta
}

// NewTestClient creates a *Client for testing with custom metadata.
func NewTestClient(meta Metadata) *Client {
	return &Client{
		driver: &mockDriver{
			stubEngine: stubEngine{
				shellEngine: &shellEngine{},
			},
			meta: meta,
		},
	}
}

// IsAvailable always returns false for the stub engine.
func (*stubEngine) IsAvailable(context.Context) bool {
	return false
}

// Metadata returns engine configuration for the stub engine.
func (e *stubEngine) Metadata() Metadata {
	return Metadata{
		Name:      "Stub",
		Scheme:    SchemeInvalid,
		Endpoints: e.Endpoints,
	}
}

// Version returns an empty Version.
func (*stubEngine) Version(context.Context) (Version, error) {
	return Version{}, nil
}

// ContainerList returns ErrNotInitialized.
func (*stubEngine) ContainerList(context.Context) ([]Container, error) {
	return nil, ErrNotInitialized
}

// ContainerInfo returns ErrNotInitialized.
func (*stubEngine) ContainerInfo(context.Context, ...string) (map[string]Container, error) {
	return nil, ErrNotInitialized
}

// ContainerRemove returns ErrNotInitialized.
func (*stubEngine) ContainerRemove(context.Context, bool, ...string) error {
	return ErrNotInitialized
}

// ContainerStop returns ErrNotInitialized.
func (*stubEngine) ContainerStop(context.Context, time.Duration, ...string) error {
	return ErrNotInitialized
}

// ContainerLogs returns ErrNotInitialized.
func (*stubEngine) ContainerLogs(context.Context, ...string) (map[string]Logs, error) {
	return nil, ErrNotInitialized
}

// ContainerRun returns ErrNotInitialized.
func (*stubEngine) ContainerRun(context.Context, ...RunConfig) error {
	return ErrNotInitialized
}

// ImageInfo returns ErrNotInitialized.
func (*stubEngine) ImageInfo(context.Context, ...string) (map[string]Image, error) {
	return nil, ErrNotInitialized
}

// ImagePull returns ErrNotInitialized.
func (*stubEngine) ImagePull(context.Context, ...string) error {
	return ErrNotInitialized
}

// ImageRemove returns ErrNotInitialized.
func (*stubEngine) ImageRemove(context.Context, bool, ...string) error {
	return ErrNotInitialized
}

// ImageTag returns ErrNotInitialized.
func (*stubEngine) ImageTag(context.Context, ...Tag) error {
	return ErrNotInitialized
}

// ImageLoadCommand returns an empty command.
func (*stubEngine) ImageLoadCommand(string) string {
	return ""
}

// ImageLoad returns ErrNotInitialized.
func (*stubEngine) ImageLoad(context.Context, ...io.Reader) error {
	return ErrNotInitialized
}

// VolumeInfo returns ErrNotInitialized.
func (*stubEngine) VolumeInfo(context.Context, ...string) (map[string]Volume, error) {
	return nil, ErrNotInitialized
}
