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
		if _, ok := errors.AsType[*exec.ExitError](err); ok {
			return ErrNoGitBinary
		}
		return err
	}
	return nil
}
