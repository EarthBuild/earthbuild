//go:build windows

package gitutil

import (
	"context"
	"errors"
	"os/exec"
)

func detectGitBinary(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "cmd", "/C", "where git")
	_, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return ErrNoGitBinary
		}
		return err
	}
	return nil
}
