package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// **`--mount type=tmpfs` is memory a step writes into and nobody keeps.**
//
// It was unimplemented rather than declined - parseMount admits cache, secret
// and bind and refuses the rest - and it is the construct the Native suite
// reaches once `RUN --privileged` stops being refused. `tests/Earthfile`'s
// `+star-test` is the instance.
//
// The engine already has the two halves: an ephemeral mount is a directory made
// for one step and removed with it, and the guest mounts tmpfs in three other
// places. This is the pair of them.
func TestATmpfsMountIsPlanned(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(`VERSION 0.8

test:
    FROM alpine:3.20
    RUN --mount=type=tmpfs,target=/scratch true
`, "test")
	if err != nil {
		t.Fatalf("a tmpfs mount was refused: %v", err)
	}

	var seen bool

	for _, n := range p.Graph.Nodes() {
		for _, m := range n.Op.Mounts {
			if m.Target != "/scratch" {
				continue
			}

			seen = true

			if !m.Tmpfs {
				t.Error("the mount reached the graph without being a tmpfs")
			}

			// Nothing of it survives the step, which is the whole of what the
			// construct promises.
			if !m.Ephemeral {
				t.Error("a tmpfs that outlives its step is not a tmpfs")
			}
		}
	}

	if !seen {
		t.Error("no mount at /scratch reached the graph")
	}
}

// A tmpfs is not a cache, and must not be given one: two steps asking for
// scratch memory are not sharing anything, and a cache would hand the second
// what the first wrote.
func TestATmpfsIsNotSharedBetweenSteps(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(`VERSION 0.8

test:
    FROM alpine:3.20
    RUN --mount=type=tmpfs,target=/scratch true
    RUN --mount=type=tmpfs,target=/scratch true
`, "test")
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		for _, m := range n.Op.Mounts {
			if m.Target == "/scratch" && m.ID != "" {
				t.Errorf("a tmpfs was given a cache identity %q", m.ID)
			}
		}
	}

	if got := describe(p.Graph.Nodes()); strings.Count(got, "/scratch") == 0 {
		t.Error("the mounts did not reach the plan")
	}
}
