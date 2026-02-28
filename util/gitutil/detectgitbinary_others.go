//go:build !windows

package gitutil

import (
	"context"
	"errors"
	"os/exec"
)

func detectGitBinary(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", "which git")

	_, err := cmd.Output()

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return ErrNoGitBinary
	}

	return err
}
