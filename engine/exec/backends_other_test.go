//go:build !darwin && !linux

package exec_test

import "testing"

func backends(t *testing.T) []backend {
	t.Helper()

	return nil
}
