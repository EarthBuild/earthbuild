package guest

import (
	"errors"
	"os"
	"os/exec"
)

// ErrNoTerminal is what a platform without a controlling terminal says.
var ErrNoTerminal = errors.New("this platform cannot give a step a controlling terminal")

// AttachTerminal gives a step the caller's terminal.
//
// **It does not claim it, and cannot.** Claiming - `setsid` then `TIOCSCTTY` -
// makes the terminal the step's *controlling* terminal, which is what job
// control, the signal from Ctrl-C and an interruptible `read` come from. A
// terminal can only be claimed by one session, and the caller's terminal is
// already the caller's: that is what `/dev/tty` means. Measured rather than
// assumed (E197):
//
//	second claim while another session holds it    operation not permitted
//	streams only, no claim                         <nil>
//
// So the step reads and writes the terminal - `isatty` is true, a prompt works,
// `read` works - and the *session* stays with the engine. What that costs is job
// control inside the step: `fg` and `bg` in a shell the step runs have nothing
// to control.
//
// What it buys is that Ctrl-C reaches the engine, which cancels the build and
// unwinds it tidily (E179) - which is what somebody pressing it during a build
// means, rather than a signal to one step of it.
//
// A step with a controlling terminal of its own needs a *second* pty, allocated
// where the step runs and relayed to the caller's. That is how every other tool
// does it, it is a larger change, and this is not it.
func AttachTerminal(cmd *exec.Cmd, tty *os.File) error {
	if tty == nil {
		return ErrNoTerminal
	}

	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty

	return nil
}
