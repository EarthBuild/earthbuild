//go:build linux

package exec_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

func backends(t *testing.T) []backend {
	t.Helper()

	n := exec.NewNative()

	return []backend{{name: testNative, sb: n, avail: n.Available}}
}
