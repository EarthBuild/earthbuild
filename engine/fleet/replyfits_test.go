package fleet

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A reply fits the wire, whatever the step read.
//
// **An observation is unbounded and a control message is not.** `Reads` carries
// one entry per path the step touched, and a Go build touches thousands; the
// reply for `+earthly`'s link step encoded to 1,293,465 bytes against a
// `maxMessage` of 1,048,576. The worker could not send it:
//
//	earth-worker: answer an assignment: a message of 1293465 bytes,
//	  and 1048576 is the most this engine sends
//
// and the stream then died, which the driver reported as `the worker stopped
// answering after 1 attempt(s)` - a machine blamed for a message this engine
// built. Measured on a two-machine fleet, 46 delegated steps, one of them
// killing the worker for the rest of the build.
//
// The step *ran*. Its layer, its exit status and its duration are all still
// true, and an observation is advice that reaches Κ₂ only through the driver's
// own rules (I5). So an observation that will not fit is dropped and said to be
// dropped - `Incomplete` is the field a worker already has for knowing it
// missed something - rather than costing the result it was attached to.
func TestAReplyFitsTheWireWhateverTheStepRead(t *testing.T) {
	t.Parallel()

	reads := make(map[string]ir.NodeID, 40000)
	for i := range 40000 {
		reads["/a/rather/long/path/to/a/file/number/"+strconv.Itoa(i)] = ir.NodeID{byte(i)}
	}

	got := replyOf(core.Result{
		Layer: ir.NodeID{1}, Content: ir.NodeID{2}, Exit: 0, Bytes: 4096,
		Declares:    ir.NodeID{3},
		Observation: core.Observation{Reads: reads},
	})

	body, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}

	if len(body) > maxMessage {
		t.Errorf("the reply encodes to %d bytes against a limit of %d"+
			"\n  the worker cannot send it, and the stream dies with it",
			len(body), maxMessage)
	}

	// What the step actually did survives; only the advice is given up.
	if got.Layer != (ir.NodeID{1}) || got.Bytes != 4096 || got.Declares != (ir.NodeID{3}) {
		t.Error("dropping the observation lost the result it was attached to")
	}

	if !got.Observation.Incomplete {
		t.Error("an observation was dropped and the reply did not say so;" +
			"\n  a driver reading it would take silence for a step that read nothing")
	}
}

// A reply that fits is left exactly as it was.
func TestAnOrdinaryReplyKeepsItsObservation(t *testing.T) {
	t.Parallel()

	got := replyOf(core.Result{
		Layer: ir.NodeID{1},
		Observation: core.Observation{
			Reads: map[string]ir.NodeID{"/bin/sh": {7}},
		},
	})

	if len(got.Observation.Reads) != 1 || got.Observation.Incomplete {
		t.Errorf("an observation that fits was altered: %+v", got.Observation)
	}
}
