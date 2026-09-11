package cli

import (
	"errors"
	"sort"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/interp"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// wouldSkip asks whether this build has been run before, and hands back the
// shape it computed so a build that does run can record itself under it.
//
// **Before planning, which is the point.** σ needs no plan, and the inputs it
// names are read from the checkout - so a job that need not run costs a parse,
// a hash and a few file reads rather than a machine, a registry round trip and
// a digest of the whole build context.
//
// **Every uncertainty is a build.** A shape that cannot be had, a record that
// is not there, a store that cannot be read: none of them is an error and all
// of them mean run. The only thing that means skip is a record that says so.
func wouldSkip(in shapeInput, root string, s skipRecordStore) (bool, ir.NodeID, string, error) {
	shape, err := shapeOf(in)
	if err != nil {
		// **Why, not silence.** A flag that quietly does nothing is one nobody
		// can act on: the operator sees builds that never skip and has no way
		// to learn that an unpinned reference is the reason. Every one of these
		// has a remedy and the message names it.
		if errors.Is(err, ErrNotDerivable) {
			return false, ir.NodeID{}, strings.TrimPrefix(err.Error(),
				ErrNotDerivable.Error()+": "), nil
		}

		return false, ir.NodeID{}, "", err
	}

	rec, ok := s.get(in.Target, in.Platform)
	if !ok {
		return false, shape, "", nil
	}

	return rec.stillHolds(shape, root), shape, "", nil
}

// noteBuild writes down what this build read, so the next one can skip.
//
// **Best effort and silent about it.** Everything here is an optimisation for a
// later build; a record that cannot be written costs that build a build, which
// is what would have happened anyway. What it must not do is write a record
// that claims more than the build saw.
func noteBuild(o Options, plan *interp.Plan, sched *core.Scheduler, shape ir.NodeID) {
	if sched == nil || shape == (ir.NodeID{}) {
		return
	}

	// **A build where anything came from cache saw a subset of the inputs**, so
	// it leaves the record it found alone. See refreshable.
	if !refreshable(sched.Record) {
		return
	}

	inputs, err := hostInputsOfBuild(sched.Record, contextLayersOf(plan), o.Dir)
	if err != nil {
		return
	}

	// **Every Earthfile the build read, as an input like any other.** This is
	// what lets a build spanning several files be keyed without following a
	// reference or refusing one: the interpreter read them and says which.
	inputs = append(inputs, earthfileInputs(plan)...)

	sort.Slice(inputs, func(i, j int) bool {
		if inputs[i].Path != inputs[j].Path {
			return inputs[i].Path < inputs[j].Path
		}

		return inputs[i].Kind < inputs[j].Kind
	})

	store, err := skipRecordStoreFor(o.AutoSkipDB)
	if err != nil {
		return
	}

	store.put(skipRecord{
		Version: skipRecordVersion,
		Target:  o.Target, Platform: o.platformOrDefault(),
		Shape: shape.String(), Inputs: inputs, Key: jobKey(shape, inputs),
	})
}

// earthfileInputs is every Earthfile the plan read, as host inputs.
//
// Absolute paths, because a build may read a file outside its own context root
// and the record has to name it the way the next build will look for it.
func earthfileInputs(plan *interp.Plan) []hostInput {
	if plan == nil {
		return nil
	}

	files := plan.Earthfiles()
	out := make([]hostInput, 0, len(files))

	for _, at := range files {
		out = append(out, hostInput{
			Path: at, Kind: inputEarthfile, Digest: earthfileDigest(at).String(),
		})
	}

	return out
}

// contextLayersOf is which of a build's layers came from the checkout.
//
// Only these become host inputs: a read of the base image is covered by the
// pinned digest in the shape, and a read of an earlier step's output is a
// function of that step's own inputs. See hostInputsFrom.
func contextLayersOf(plan *interp.Plan) map[string]bool {
	out := map[string]bool{}

	if plan == nil {
		return out
	}

	for _, n := range plan.Graph.Nodes() {
		if n.Op.Kind == ir.OpLocal {
			out[n.ID().String()] = true
		}
	}

	return out
}

// shapeFor is the invocation in the terms shapeOf takes.
//
// **One place, so a flag that changes what a build does cannot reach the engine
// without reaching the shape.** A flag added to Options and used by the build
// but not folded in here is a false skip - see the open question in
// docs-internals/job-skipping.md.
func shapeFor(
	o Options, src []byte, args, secretDigest, secrets map[string]string,
) shapeInput {
	names := make([]string, 0, len(secrets))
	for name := range secrets {
		names = append(names, name)
	}

	return shapeInput{
		Source: src, Target: o.Target, Platform: o.platformOrDefault(),
		Args: args, SecretDigests: secretDigest, SecretNames: names,
		Push: o.Push, Strict: o.Strict, NoOutput: o.NoOutput,
		AllowPrivileged: o.AllowPrivileged, VersionFlags: o.VersionFlags,
	}
}
