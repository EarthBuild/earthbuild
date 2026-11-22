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

// SetProgress makes this sandbox able to say how far a blob it is fetching has
// been written.
//
// **The same channel, a second question.** A guest unpacking a blob as it
// arrives needs to know where the writer has got to, and the only thing it had
// to ask was a file on the shared mount whose answer is about 460ms old - which
// is why streaming bought nothing (E688). A fault-in already travels
// guest-to-host over a socket; this goes the same way.
//
// Set per build rather than per sandbox: the answers come from the fetch that
// is running now, and a machine outlives any one of them.
func (a *Apple) SetProgress(f func(blob string, have int64) (int64, error)) {
	a.fillMu.Lock()
	defer a.fillMu.Unlock()

	a.progress = f
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
	started := a.fill != nil
	a.fillMu.Unlock()

	// **A relay is wanted for either question.** Faulting a path in is the
	// worker's; asking how far a blob has been written is a streaming build's,
	// and only that channel makes it worth doing - a file on the shared mount
	// answers about 460ms late (E688).
	//
	// The switch rather than `a.progress`, because that is set per build and
	// this runs when the sandbox starts, long before any fetch exists.
	if !started && !streamToGuest() {
		return
	}

	guestBin, err := a.guestBinary()
	if err != nil {
		a.noFills(err)

		return
	}

	// **No context and no timeout**, unlike the `container` calls in
	// apple_darwin.go. Those are probes and cleanups; this is the fault-in
	// relay, which serves the sandbox for as long as it is up. A bound would
	// stop it part way through a build, and the caller's context is not it
	// either: the relay outlives any one step.
	//
	//nolint:noctx // the sandbox's lifetime, not a request's
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

		_ = guest.ServeFillsAnd(stream{Reader: out, WriteCloser: in},
			a.fillAnswer, a.progressAnswer)
	}()
}

// noFills says a step will take its base whole, and why.
func (a *Apple) noFills(err error) {
	fmt.Fprintf(os.Stderr, "earth: this sandbox cannot fault paths in: %v"+
		"\n  steps will take whole layers, which is slower and correct\n", err)
}

// fillAnswer reads the filler at the moment a question arrives.

// **Not the one that was there when the relay started**, for the same reason
// `progressAnswer` does not: a sandbox outlives any one build and the filler is
// set per build, so a relay that captured it at boot holds nil for the life of
// the sandbox. That was safe only while the relay refused to start without a
// filler; streaming a blob is now a second reason to start it, and on macOS the
// default one (E811).
func (a *Apple) fillAnswer(handle, path string) error {
	a.fillMu.Lock()
	f := a.fill
	a.fillMu.Unlock()

	if f == nil {
		return fmt.Errorf("no worker on this host can fault in %s", path)
	}

	return f(handle, path)
}

// progressAnswer reads the answerer at the moment a question arrives.
//
// **Not the one that was there when the relay started.** A sandbox is found and
// reused by name and outlives any one build, so the relay is running long before
// the fetch that a question is about - capturing the answerer at start would
// answer this build's questions with the last build's fetch, or with nothing.
func (a *Apple) progressAnswer(blob string, have int64) (int64, error) {
	a.fillMu.Lock()
	f := a.progress
	a.fillMu.Unlock()

	if f == nil {
		return 0, fmt.Errorf("no fetch on this host is writing %s", blob)
	}

	return f(blob, have)
}
