package core

import (
	"context"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Materialiser turns a layer stack into something a step can run against.
//
// It is the port that stage S3 makes real. On Linux the implementation is
// overlayfs over containerd snapshots; on macOS it is `earth-guestd` inside the
// VM, because `container exec` accepts no mount options and a running VM cannot
// have filesystems attached from outside (experiment E1b). Both must satisfy
// the same contract, which is why the contract is written down as a suite
// rather than left implicit in whichever landed first.
type Materialiser interface {
	// Materialise prepares the stack and returns a handle to it. The stack is
	// ordered oldest-first and has already been flattened (4.8), so an
	// implementation may assume it is within the mount limit.
	Materialise(ctx context.Context, stack []ir.NodeID) (Handle, error)
}

// Handle is a materialised stack, ready for a step to run against.
type Handle interface {
	// Root is where the filesystem appears. Core never opens it - the executor
	// does - but core passes it along, so it is part of the contract.
	Root() string

	// Observations reports what the step looked at: reads, negative lookups and
	// directory listings (green paper 3.4).
	//
	// Empty until stage S5. It is on the handle rather than the executor
	// because the materialiser is what sees the faults, and having the executor
	// reach upward to record them would invert the dependency and make both
	// untestable (plan §2.0.3).
	Observations() Observation

	// Delta is where the step's own writes land, distinct from Root, which is
	// the whole filesystem it sees.
	//
	// The distinction is the layer model itself: a step produces its *writes*,
	// not the tree it saw. Digesting Root would identify a layer by the entire
	// base plus the change, so a one-line edit over a 200 MB image would produce
	// a 200 MB layer that shares nothing with the one before it.
	Delta() string

	// Release drops the handle. Idempotent: releasing twice is not an error,
	// because cleanup paths run more than once and must not care.
	Release() error
}

// Observation is green paper's 𝑟 ≡ (𝑅, 𝑁, 𝐷).
//
// Declared now, populated at S5. Recording it early keeps the shape of the
// interfaces honest: a Handle that could not report observations would have to
// grow the ability later, and every implementation would need revisiting.
type Observation struct {
	// Reads are paths read, with the digest of what was read.
	Reads map[string]ir.NodeID
	// Negative are lookups that found nothing: failed opens, stats of absent
	// paths. A specification recording only Reads admits false cache hits,
	// because a step that reads nothing when a file is absent would key
	// identically against a base where it exists (green paper 3.4, I3).
	Negative []string
	// Listings are directories enumerated, with the digest of each listing. A
	// listing digest subsumes every negative lookup inside that directory,
	// which is what keeps Negative small when a compiler probes twenty include
	// paths for five hundred headers.
	Listings map[string]ir.NodeID
	// Incomplete says the source knows it missed something: a ring buffer that
	// overflowed, a tracer attached after the step began, an access path it
	// cannot see.
	//
	// It exists because the alternative to admitting loss is a Κ₂ entry claiming
	// a step reads exactly the paths recorded, made about a step that read more.
	// The first base differing in an unrecorded path is then a false hit - the
	// one failure this design exists to prevent (I3).
	//
	// This field is what makes a lossy observation source *usable*: loss that is
	// detected costs an L2 hit, loss that is silent costs correctness. A source
	// that cannot report its own loss cannot be used for cache keys at all,
	// however fast it is.
	Incomplete bool
	// Why names each distinct reason the source knows it missed something,
	// sorted. Diagnostic only, and **deliberately not keyed**: it says something
	// about the observation's own quality rather than about what the step read,
	// and two machines whose tracers failed with different errnos observed the
	// same step.
	//
	// It exists because "this step will never earn an L2 hit" is a performance
	// bug nobody can find without it. Three defects in this work were found by
	// the reason and not by a test, and each time the reason had to be added
	// first (E209, E215, E217).
	Why []string
}
