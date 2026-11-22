package guest

import (
	"encoding/json"
	"strings"
	"testing"
)

// A guest that knows it missed something can say so across the wire.
//
// `Observation.Incomplete` is what makes a lossy source *usable*: loss that is
// declared costs an L2 hit, loss that is hidden costs correctness (green paper
// §3.4). The guest's own copy observation declares itself lossy when the
// destination path went through a symlink - and the protocol had no field for
// it, so the host decoded a lossy observation as a complete one.
//
// **The most dangerous shape a protocol can have**: a sender that is careful,
// a receiver that is careful, and a wire that quietly drops the care. The
// guest's `Incomplete` was set correctly, `Result.Observed` would have been set
// correctly, and Κ₂ would have claimed a step read exactly the paths recorded
// about a step that read more.
//
// `omitempty`, so a guest that predates the field sends nothing and a host
// decodes false - which is the wrong default in principle. It is the right one
// here because the only thing that *sets* it is newer than the field: an older
// guest has no lossy source to be lossy about.
func TestTheWireCarriesAnAdmissionOfLoss(t *testing.T) {
	t.Parallel()

	b, err := json.Marshal(Response{Incomplete: true})
	if err != nil {
		t.Fatal(err)
	}

	var back Response

	err = json.Unmarshal(b, &back)
	if err != nil {
		t.Fatal(err)
	}

	if !back.Incomplete {
		t.Errorf("an observation the guest admitted was lossy crossed the wire"+
			" as complete: %s", b)
	}

	// And the absent case stays absent, so a guest that never sets it costs
	// nothing in bytes and nothing in meaning.
	b, err = json.Marshal(Response{})
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(b), "incomplete") {
		t.Errorf("a response that admits nothing still spends bytes saying so: %s", b)
	}
}
