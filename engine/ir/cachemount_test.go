package ir

import "testing"

// TestAnOrdinaryCacheMountDoesNotPinAStep.
//
// **A cache mount cannot change what a step produces, and this engine already
// says so.** The step's key hashes a mount's *declaration* - target, id, flags -
// and never its contents, so a cache hit is already an assertion that whatever
// is in there does not reach the layer. `Persist` is the one that does reach it
// and is in the key for exactly that reason.
//
// So a worker running the same step against its own, differently-populated
// cache must produce the same layer. If it does not, the local cache was
// already unsound and had been for every hit it ever served.
//
// Refusing to delegate it was therefore stricter than the cache tier, and
// inconsistently so. It also costs everything: this repository's own Earthfile
// has 34 cache mounts, and `+all-binaries` delegated 4 of 47 steps because the
// `go build` at the heart of every binary carries two (E-F2).
func TestAnOrdinaryCacheMountDoesNotPinAStep(t *testing.T) {
	t.Parallel()

	shared := Op{
		Kind:   OpExec,
		Mounts: []Mount{{Target: "/go/pkg/mod", ID: "go-mod"}},
	}

	if only, why := shared.OnInvokerOnly(); only {
		t.Errorf("a shared cache mount pins the step (%q), so the expensive"+
			" half of a real build can never be delegated", why)
	}

	// `--sharing=locked` is mutual exclusion, and a worker has its own
	// directory to be exclusive about.
	locked := Op{
		Kind:   OpExec,
		Mounts: []Mount{{Target: "/go/pkg/mod", ID: "go-mod", Exclusive: true}},
	}

	if only, why := locked.OnInvokerOnly(); only {
		t.Errorf("a locked cache mount pins the step (%q); locking is per"+
			" machine and every machine has its own", why)
	}
}

// TestTheMountsThatDoPinStillPin.
//
// Each for a different reason, and none of them "it is a mount": a secret is
// not on the wire, a persisted cache is captured into the layer and so *is* the
// result, and a sandbox path names a file on one machine's disk.
func TestTheMountsThatDoPinStillPin(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		what string
		m    Mount
	}{
		{"a secret", Mount{Target: "/run/secrets/tok", ID: "tok", Secret: true}},
		{"a persisted cache", Mount{Target: "/out", ID: "o", Persist: true}},
		{"a sandbox path", Mount{Target: "/in", Sandbox: "/var/lib/earthbuild/x"}},
	} {
		op := Op{Kind: OpExec, Mounts: []Mount{c.m}}

		if only, _ := op.OnInvokerOnly(); !only {
			t.Errorf("%s no longer pins the step, and it must", c.what)
		}
	}
}
