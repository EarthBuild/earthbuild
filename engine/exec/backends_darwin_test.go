//go:build darwin

package exec_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

func backends(t *testing.T) []backend {
	t.Helper()

	a := exec.NewApple()

	return []backend{{name: "apple", sb: a, avail: a.Available}}
}
