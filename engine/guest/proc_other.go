//go:build !unix

package guest

import (
	"errors"
	"os/exec"
	"syscall"
)

// killGroup has no process group to end here.
//
// This package holds both halves of the protocol - the server that runs steps
// and the client that talks to it - so it compiles wherever the CLI does, and
// the CLI runs on platforms that never start a step. Refusing is right: a caller
// that reaches this on such a platform has already gone wrong somewhere the
// error can name (E581).
func killGroup(int, syscall.Signal) error {
	return errors.New("this platform has no process groups to signal")
}

// ownGroup does nothing where there are no process groups.
func ownGroup(*exec.Cmd) {}
