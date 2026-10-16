//go:build !darwin && !linux

package cli

import (
	"errors"
	"runtime"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// sandbox refuses on a platform with no backend, rather than falling back to
// running steps unconfined - which would produce results that look cacheable
// and are not (green paper A3).
func sandbox(image string) (exec.Sandbox, error) {
	return nil, errors.New("the native engine has no sandbox for " + runtime.GOOS +
		"\n  supported: macOS (Apple container) and Linux (namespaces)" +
		"\n  to build here, use --engine=buildkit")
}
