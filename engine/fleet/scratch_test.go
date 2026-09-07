package fleet

import (
	"errors"
	"slices"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A step whose only mounts are private caches can be delegated.
//
// `--sharing=private` names no directory on any machine: the guest makes one for
// the step and removes it after (§3.3c). So a worker can produce exactly what
// the invoker would - an empty directory at that path - which is the condition
// for delegating anything.
//
// Worth doing because of what the old rule cost. "Any mount pins the step" is,
// for a cargo or npm build, "nothing is delegated at all": those put a CACHE in
// nearly every RUN, and a fleet that refuses every step is a fleet that works
// perfectly and does nothing (E433).
func TestAPrivateCacheDoesNotRefuseDelegation(t *testing.T) {
	t.Parallel()

	op := ir.Op{Kind: ir.OpExec, Args: []string{"make"}}
	op.Mounts = []ir.Mount{{Target: "/scratch", Ephemeral: true}}

	a, err := Delegate(&ir.Node{Op: op}, nil, nil)
	if err != nil {
		t.Fatalf("refused a step whose only mount is a private cache: %v", err)
	}

	if !slices.Contains(a.Op.Scratch, "/scratch") {
		t.Errorf("the assignment does not mention /scratch: %v", a.Op.Scratch)
	}
}

// The assignment carries the directory, or the worker builds a different step.
//
// This is the half that makes the widening safe rather than merely permissive.
// At home the step's writes under the mount are discarded with it; on a worker
// that never heard of the mount they land in the output layer. Same key, two
// results, which is I3 - so the wire carries the targets and `expressible`
// opens only because it does.
func TestASharedCacheStillRefusesDelegation(t *testing.T) {
	t.Parallel()

	for _, m := range []ir.Mount{
		{Target: "/root/.cargo", ID: "cargo"},
		{Target: "/c", ID: "npm", Exclusive: true},
		{Target: "/run/secrets/tok", Secret: true, Ephemeral: true},
	} {
		op := ir.Op{Kind: ir.OpExec, Args: []string{"make"}}
		op.Mounts = []ir.Mount{m}

		_, err := Delegate(&ir.Node{Op: op}, nil, nil)
		if !errors.Is(err, ErrNotDelegable) {
			t.Errorf("delegated a step mounting %+v: %v"+
				"\n  the worker would run it against an empty directory it"+
				" believes is warm", m, err)
		}
	}
}

// One private cache among named ones does not smuggle the step out.
//
// The pin is a property of the set, not of a mount: a step is delegable when
// *every* mount is reproducible elsewhere. Written because the obvious loop -
// "collect the ephemeral ones, send those" - passes the previous two tests and
// is the send-what-fits answer Delegate's own documentation refuses.
func TestOnePrivateCacheAmongNamedOnesStillPins(t *testing.T) {
	t.Parallel()

	op := ir.Op{Kind: ir.OpExec, Args: []string{"make"}}
	op.Mounts = []ir.Mount{
		{Target: "/scratch", Ephemeral: true},
		{Target: "/root/.cargo", ID: "cargo"},
	}

	_, err := Delegate(&ir.Node{Op: op}, nil, nil)
	if !errors.Is(err, ErrNotDelegable) {
		t.Errorf("delegated a step that also mounts a named cache: %v", err)
	}
}

// The worker rebuilds the mount the invoker had, not merely the command.
//
// The wire carries targets; the step is `ir.Op` at both ends and must be the
// *same* one, because both ends key on it. A worker that ran the command without
// the mount would write into its result what the invoker discards - and would
// report it under the invoker's key, which is a wrong build presented as a
// distributed one (I3).
func TestAWorkerRebuildsThePrivateCache(t *testing.T) {
	t.Parallel()

	sent := ir.Op{Kind: ir.OpExec, Args: []string{"make"}}
	sent.Mounts = []ir.Mount{
		{Target: "/scratch", Ephemeral: true},
		{Target: "/tmp/build", Ephemeral: true},
	}

	a, err := Delegate(&ir.Node{Op: sent}, nil, nil)
	if err != nil {
		t.Fatalf("delegating: %v", err)
	}

	got, err := operationOf(a.Op)
	if err != nil {
		t.Fatalf("rebuilding on the worker: %v", err)
	}

	if !slices.Equal(got.Mounts, sent.Mounts) {
		t.Fatalf("the worker rebuilt %+v, the invoker sent %+v", got.Mounts, sent.Mounts)
	}

	// Keyed identically, which is the property the mounts were for. Compared
	// through the key rather than by eye: the two Ops travel through the wire's
	// vocabulary and back, and equality of what they *do* is what matters.
	if (&ir.Node{Op: got}).ID() != (&ir.Node{Op: sent}).ID() {
		t.Error("the rebuilt step has a different identity from the one sent" +
			"\n  the worker's result would be filed under a key describing" +
			" another step")
	}
}
