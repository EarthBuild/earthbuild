//go:build windows

package terminal

import (
	"context"
	"errors"
	"io"

	"github.com/EarthBuild/earthbuild/conslogging"
)

func ConnectTerm(ctx context.Context, addr io.ReadWriteCloser, console conslogging.ConsoleLogger) error {
	return errors.New("debugger not supported on Windows yet")
}
