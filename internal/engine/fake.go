package engine

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

	e.Endpoints, err = e.ResolveEndpoints(Stub, cfg)
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

// ListContainers returns ErrNotInitialized.
func (*stubEngine) ListContainers(context.Context) ([]Container, error) {
	return nil, ErrNotInitialized
}

// InspectContainer returns ErrNotInitialized.
func (*stubEngine) InspectContainer(context.Context, ...string) (map[string]Container, error) {
	return nil, ErrNotInitialized
}

// RemoveContainer returns ErrNotInitialized.
func (*stubEngine) RemoveContainer(context.Context, bool, ...string) error {
	return ErrNotInitialized
}

// StopContainer returns ErrNotInitialized.
func (*stubEngine) StopContainer(context.Context, time.Duration, ...string) error {
	return ErrNotInitialized
}

// Logs returns ErrNotInitialized.
func (*stubEngine) Logs(context.Context, ...string) (map[string]Logs, error) {
	return nil, ErrNotInitialized
}

// RunContainer returns ErrNotInitialized.
func (*stubEngine) RunContainer(context.Context, ...ContainerSpec) error {
	return ErrNotInitialized
}

// InspectImage returns ErrNotInitialized.
func (*stubEngine) InspectImage(context.Context, ...string) (map[string]Image, error) {
	return nil, ErrNotInitialized
}

// PullImage returns ErrNotInitialized.
func (*stubEngine) PullImage(context.Context, ...string) error {
	return ErrNotInitialized
}

// RemoveImage returns ErrNotInitialized.
func (*stubEngine) RemoveImage(context.Context, bool, ...string) error {
	return ErrNotInitialized
}

// TagImage returns ErrNotInitialized.
func (*stubEngine) TagImage(context.Context, ...Tag) error {
	return ErrNotInitialized
}

// ImageLoadCommand returns an empty command.
func (*stubEngine) ImageLoadCommand(string) string {
	return ""
}

// LoadImage returns ErrNotInitialized.
func (*stubEngine) LoadImage(context.Context, ...io.Reader) error {
	return ErrNotInitialized
}

// InspectVolume returns ErrNotInitialized.
func (*stubEngine) InspectVolume(context.Context, ...string) (map[string]Volume, error) {
	return nil, ErrNotInitialized
}
