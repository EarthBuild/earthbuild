package exec

import (
	"os"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// maxRecordedOutput bounds what a step's standard output may cost.
//
// The guest's own limit, for the same reason: a step that prints a gigabyte must
// not be able to exhaust this process through a channel nobody asked for. Past
// it the recording stops and says so, which is the part that matters - a
// `$( )` substitution reading a *truncated* value reads a wrong one, and has to
// know to run the command again instead.
const maxRecordedOutput = 64 << 10

// EnvRecordOutput turns off keeping what a step printed.
//
// On by default, because a cache hit that cannot reproduce a step's output is
// how `LET v=$(cmd)` came to give three files cold and nothing ever after. Off
// for a caller who would rather a build log showed only what this run did.
const EnvRecordOutput = "EARTH_STEP_OUTPUT"

// recordOutput reports whether a step's output is kept on its result.
func recordOutput() bool {
	switch os.Getenv(EnvRecordOutput) {
	case "0", "false", "no":
		return false
	default:
		return true
	}
}

// Echo replays a step's recorded output through this executor's sink.
//
// **The same path a running step's lines take**, so every reader of them
// behaves identically whether the step ran or was served: a progress display
// shows the step's output, and a `$( )` substitution collects its value.
//
// Standard error is not replayed, because it was not recorded - `$( )` in every
// shell captures standard output alone, and a build log wanting both is a
// different question from a step's value.
func (e *Executor) Echo(n *ir.Node, out string) {
	if out == "" {
		return
	}

	where := n.Meta.Source
	if where == "" {
		where = n.Op.Kind.String()
	}

	for line := range strings.SplitSeq(strings.TrimSuffix(out, "\n"), "\n") {
		if e.Progress != nil {
			e.Progress(where, line, n.Meta.RawOutput)
		}

		if e.Capture != nil {
			e.Capture(n, line, false)
		}
	}
}
