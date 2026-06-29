package containerutil

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// This is a stub for use when a proper frontend is not available.
type stubFrontend struct {
	*shellFrontend
}

// ErrFrontendNotInitialized is returned when the frontend is not initialized.
var ErrFrontendNotInitialized = errors.New("frontend (e.g. docker/podman) not initialized")

// NewStubFrontend creates a stubbed frontend. Useful in cases where a frontend could not be detected,
// but we still need a frontend. Examples include earthbuild/earthbuild, or integration tests. It is
// currently only used as a fallback when docker or other frontends are missing.
func NewStubFrontend(cfg *FrontendConfig) (ContainerFrontend, error) {
	fe := &stubFrontend{
		shellFrontend: &shellFrontend{Console: cfg.Console},
	}

	var err error

	fe.urls, err = fe.setupAndValidateAddresses(FrontendStub, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate buildkit URLs: %w", err)
	}

	return fe, nil
}

func (*stubFrontend) Scheme() string {
	return ""
}

func (*stubFrontend) IsAvailable(context.Context) bool {
	return false
}

func (sf *stubFrontend) Config() *CurrentFrontend {
	return &CurrentFrontend{
		Setting:      FrontendStub,
		FrontendURLs: sf.urls,
	}
}

func (*stubFrontend) Information(context.Context) (*FrontendInfo, error) {
	return &FrontendInfo{}, nil
}

func (*stubFrontend) ContainerList(context.Context) ([]*ContainerInfo, error) {
	return nil, ErrFrontendNotInitialized
}

func (*stubFrontend) ContainerInfo(context.Context, ...string) (map[string]*ContainerInfo, error) {
	return nil, ErrFrontendNotInitialized
}

func (*stubFrontend) ContainerRemove(context.Context, bool, ...string) error {
	return ErrFrontendNotInitialized
}

func (*stubFrontend) ContainerStop(context.Context, uint, ...string) error {
	return ErrFrontendNotInitialized
}

func (*stubFrontend) ContainerLogs(context.Context, ...string) (map[string]*ContainerLogs, error) {
	return nil, ErrFrontendNotInitialized
}

func (*stubFrontend) ContainerRun(context.Context, ...ContainerRun) error {
	return ErrFrontendNotInitialized
}

func (*stubFrontend) ImageInfo(context.Context, ...string) (map[string]*ImageInfo, error) {
	return nil, ErrFrontendNotInitialized
}

func (*stubFrontend) ImagePull(context.Context, ...string) error {
	return ErrFrontendNotInitialized
}

func (*stubFrontend) ImageRemove(context.Context, bool, ...string) error {
	return ErrFrontendNotInitialized
}

func (*stubFrontend) ImageTag(context.Context, ...ImageTag) error {
	return ErrFrontendNotInitialized
}

func (*stubFrontend) ImageLoadFromFileCommand(string) string {
	return ""
}

func (*stubFrontend) ImageLoad(context.Context, ...io.Reader) error {
	return ErrFrontendNotInitialized
}

func (*stubFrontend) VolumeInfo(context.Context, ...string) (map[string]*VolumeInfo, error) {
	return nil, ErrFrontendNotInitialized
}
