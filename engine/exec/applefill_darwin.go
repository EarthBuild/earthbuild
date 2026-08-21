//go:build darwin

package exec

import (
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/guest"
)

// guestFillSocket is where the guest listens for its fault-in channel.
//
// Short, and inside the sandbox. A unix socket address is a fixed-size field -
// 104 bytes on darwin - so a path assembled from a store directory would not
// fit, and one on the host would be reachable by things that are not this guest.
const guestFillSocket = "/run/earth-fills.sock"

// SetFill makes this sandbox able to fault paths in.
//
// **A fault travels the wrong way.** Every other message is the host asking the
// guest, and `container exec` gives one stdio pair per invocation, which the
// main protocol holds. A sandbox that spawns its guest as a child passes a
// second descriptor; this one reaches its guest through a VM and cannot - which
// is why a darwin worker announced that it could not fault paths in and took
// whole layers instead (E305).
//
// The guest therefore listens on a socket inside the sandbox, and a second exec
// carries bytes between that socket and here. No frame changes: `Fills` speaks
// the same protocol over whatever stream it is handed.
//
// What it buys is what a worker fetches. A step reads a fraction of its base -
// two files of 5,410 for `go version`, 1,752 for a cold `go build` (E514) - so a
// worker that can fault takes what a step is predicted to read and the rest only
// if it turns out to be needed.
func (a *Apple) SetFill(f func(handle, path string) error) {
	a.fillMu.Lock()
	defer a.fillMu.Unlock()

	a.fill = f
}

// stream is the relay's two halves seen as one connection.
type stream struct {
	io.Reader
	io.WriteCloser
}

// serveFills starts the relay and answers faults for as long as it runs.
//
// **Best-effort, and loudly so.** A sandbox that cannot carry faults is slower
// and correct: the step gets its base whole, which is what happened before this
// existed. What it must not do is fail quietly, because the symptom of that is a
// build that is mysteriously slow rather than one that says why.
func (a *Apple) serveFills() {
	a.fillMu.Lock()
	fill := a.fill
	a.fillMu.Unlock()

	if fill == nil {
		return
	}

	guestBin, err := a.guestBinary()
	if err != nil {
		a.noFills(err)

		return
	}

	relay := osexec.Command("container", "exec", "-i", //nolint:gosec // fixed argv
		"-e", guest.EnvFillSocket+"="+guestFillSocket,
		a.name, "/earth/"+filepath.Base(guestBin), "--fills")

	in, err := relay.StdinPipe()
	if err != nil {
		a.noFills(err)

		return
	}

	out, err := relay.StdoutPipe()
	if err != nil {
		a.noFills(err)

		return
	}

	relay.Stderr = os.Stderr

	err = relay.Start()
	if err != nil {
		a.noFills(err)

		return
	}

	go func() {
		defer func() {
			_ = in.Close()

			if relay.Process != nil {
				_ = relay.Process.Kill()
			}

			_ = relay.Wait()
		}()

		_ = guest.ServeFills(stream{Reader: out, WriteCloser: in}, fill)
	}()
}

// noFills says a step will take its base whole, and why.
func (a *Apple) noFills(err error) {
	fmt.Fprintf(os.Stderr, "earth: this sandbox cannot fault paths in: %v"+
		"\n  steps will take whole layers, which is slower and correct\n", err)
}
