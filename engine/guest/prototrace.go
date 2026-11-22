package guest

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
)

// EnvProtoTrace names a directory into which every byte read from a guest
// connection is copied.
//
// **A desynchronised stream cannot be diagnosed from the frame that reports
// it.** Once a length has been taken from the middle of a message, each read
// returns a window into that message and the next; the parse fails wherever the
// window happens to land, which is megabytes past the boundary that actually
// moved. Replaying the captured bytes against the framing rules finds the first
// frame whose length does not lead to another well-formed frame, and that one
// is the fault.
//
// Off by default and never on in CI: a capture is the whole conversation, and
// the conversation includes every layer a step faults in.
const EnvProtoTrace = "EARTH_PROTO_TRACE"

// traced is what makes two connections in one process land in two files.
var traced atomic.Uint64

// traceStream copies everything read from r into a file, where asked.
//
// Failure to open the capture is reported and ignored: this exists to diagnose
// a build, and refusing to run the build would remove the thing being
// diagnosed.
func traceStream(r io.Reader) io.Reader {
	dir := os.Getenv(EnvProtoTrace)
	if dir == "" {
		return r
	}

	err := os.MkdirAll(dir, 0o700)
	if err != nil {
		fmt.Fprintf(os.Stderr, "earth: no protocol capture in %s: %v\n", dir, err)

		return r
	}

	at := filepath.Join(dir, fmt.Sprintf("%d-%d.frames", os.Getpid(), traced.Add(1)))

	// 0o600: a capture holds a build's whole conversation, which includes the
	// values of its secrets.
	f, err := os.OpenFile(at, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "earth: no protocol capture at %s: %v\n", at, err)

		return r
	}

	return io.TeeReader(r, f)
}
