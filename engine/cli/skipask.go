package cli

import (
	"errors"
	"fmt"
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
	return wouldSkipPlan(in, root, s, "")
}

// wouldSkipPlan is wouldSkip told this build's plan fingerprint, which is what
// a record left by a build that watched nothing is compared against.
func wouldSkipPlan(
	in shapeInput, root string, s skipRecordStore, plan string,
) (bool, ir.NodeID, string, error) {
	shape, err := shapeOf(in)
	if err != nil {
		// **Why, not silence.** A flag that quietly does nothing is one nobody
		// can act on: the operator sees builds that never skip and has no way
		// to learn that an unpinned reference is the reason. Every one of these
		// has a remedy and the message names it.
		if errors.Is(err, ErrNotDerivable) {
			return false, ir.NodeID{}, reasonOf(err), nil
		}

		return false, ir.NodeID{}, "", err
	}

	rec, ok := s.get(in.Target, in.Platform)
	if !ok {
		return false, shape, "", nil
	}

	// **The reads first, the fingerprint second.** The reads are the finer
	// answer - a file nobody opened does not move them - so a record carrying
	// them decides, and the fingerprint is what a build that watched nothing
	// left behind. Consulting only the first meant the coarse record was
	// written by every cached build and read by none.
	if rec.stillHolds(shape, root) {
		return true, shape, "", nil
	}

	return rec.planHolds(plan), shape, "", nil
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

	store, err := skipRecordStoreFor(o.AutoSkipDB)
	if err != nil {
		return
	}

	// **Before anything is derived.** A build containing a step that has to
	// happen has no skippable answer to record, however well it was watched:
	// what a later build would skip is the step itself.
	if why := mustRun(plan); why != "" {
		fmt.Fprintf(o.Out, "auto-skip: %s will not be skipped\n  %s\n", o.Target, why)
		keepUnskippable(store, o.Target, o.platformOrDefault(), why)

		return
	}

	// **What a build that ran nothing still established.** Every chain key hit,
	// which covers the declared inputs - so the fingerprint over those is true
	// even though no step watched anything. Without it `--auto-skip` could never
	// start on a machine that already had a store, which is every machine after
	// the first build, and the flag would appear to do nothing for ever.
	fingerprint := inputsOf(plan, o.Target, o.platformOrDefault()).Fingerprint

	var inputs []hostInput

	// **Only a build where every watched step ran saw the whole of 𝑅.** One that
	// hit cache anywhere gathered a subset, which is the shape that skips on a
	// change nobody accounted for. See refreshable.
	// **A cached step no longer refuses the key.** Its reads come from the
	// profile the engine already keeps for its class - see readsOf - so the
	// only thing that stops a record now is a step nobody has any account of.
	inputs, err = hostInputsOfBuild(sched.Record, sched.Profiles, contextLayersOf(plan), o.Dir)
	if err != nil {
		// Said, because silence here is permanent: a build that cannot record
		// what it read leaves the coarse key in place, and the coarse key
		// cannot ignore a file nobody opened.
		fmt.Fprintf(o.Out, "auto-skip: %s\n  %v\n",
			"this build cannot record what it read, so the coarser key stands",
			reasonOf(err))

		inputs = nil
	} else {
		inputs = append(inputs, earthfileInputs(plan)...)

		sort.Slice(inputs, func(i, j int) bool {
			if inputs[i].Path != inputs[j].Path {
				return inputs[i].Path < inputs[j].Path
			}

			return inputs[i].Kind < inputs[j].Kind
		})
	}

	keep(store, o.Target, o.platformOrDefault(), shape, fingerprint, inputs)
}

// whyNotRefreshable names the first step that stopped this build recording what
// it read.
//
// A reader told only "the coarser key stands" has to guess at which of a
// hundred steps did it, which is the count-without-a-cause this engine keeps
// refusing to ship.
func whyNotRefreshable(rec *core.Record) string {
	if rec == nil {
		return "this build kept no record"
	}

	for _, step := range rec.Steps {
		if watched(step.Kind) && !executed(step.Outcome) {
			return stepName(step) + " came from cache, so nobody watched what it reads"
		}
	}

	if len(rec.Steps) == 0 {
		return "it ran no steps"
	}

	return "no step of it was watched"
}

// keep writes a build's record without losing what an earlier one learned.
//
// **A build that hit cache must not downgrade a record made by one that ran.**
// The fingerprint is brought up to date either way - it is true of this build -
// and the reads are replaced only when this build actually saw them. Otherwise
// running a build that happened to hit cache would undo the mechanism.
func keep(
	store skipRecordStore, target, platform string, shape ir.NodeID,
	fingerprint string, inputs []hostInput,
) {
	out := recordFor(target, platform, shape, fingerprint, inputs)

	if len(out.Inputs) == 0 {
		if was, ok := store.get(out.Target, out.Platform); ok {
			out.Shape, out.Inputs, out.Key = was.Shape, was.Inputs, was.Key
		}
	}

	store.put(out)
}

// keepUnskippable replaces whatever this target had with a record that answers
// nothing, and says why.
//
// **Replaces rather than leaves alone.** The previous record was written when
// the build did not contain this, and leaving it is how a target acquires a
// `LOCALLY` and goes on being skipped - the shape and the fingerprint both move
// when the Earthfile does, but only for as long as nothing else restores them.
func keepUnskippable(store skipRecordStore, target, platform, why string) {
	store.put(skipRecord{
		Version: skipRecordVersion, Target: target, Platform: platform,
		MustRun: why,
	})
}

// mustRun names a construct in the plan that no record can stand in for, or is
// empty.
//
// A subset of caveatsOf, deliberately. An unpinned base and a secret with no
// fleet key are reasons the key *under-claims*, and running unpinned builds
// anyway is a trade this flag is allowed to make. These two are different:
// each is a step that has to happen, so skipping the build does not produce a
// coarser answer, it produces no answer at all.
func mustRun(plan *interp.Plan) string {
	if plan == nil || plan.Graph == nil {
		return ""
	}

	for _, n := range plan.Graph.Nodes() {
		switch {
		case n.Op.Kind == ir.OpHost:
			return loc(n) + " runs LOCALLY, on this machine and outside the" +
				" build: skipping it would skip whatever it writes there"

		case n.Op.NoCache:
			return loc(n) + " is --no-cache, so it runs whatever the inputs say"
		}
	}

	return ""
}

// loc is where to tell the reader to look.
func loc(n *ir.Node) string {
	if n.Meta.Source == "" {
		return "a step"
	}

	return n.Meta.Source
}

// recordFor is what a build has to say about itself.
func recordFor(
	target, platform string, shape ir.NodeID, fingerprint string, inputs []hostInput,
) skipRecord {
	out := skipRecord{
		Version: skipRecordVersion, Target: target, Platform: platform,
		Plan: fingerprint,
	}

	if len(inputs) > 0 {
		out.Shape, out.Inputs, out.Key = shape.String(), inputs, jobKey(shape, inputs)
	}

	return out
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

// reasonOf is the specific half of an ErrNotDerivable, or the whole of any
// other error.
//
// The sentinel says only that a reason exists; the text after it says which
// step and what about it. `errors.Unwrap` returns the sentinel and throws the
// half away, which is how a cold substrate build came to report that its
// inputs could not be derived without ever saying why.
func reasonOf(err error) string {
	if !errors.Is(err, ErrNotDerivable) {
		return err.Error()
	}

	return strings.TrimPrefix(err.Error(), ErrNotDerivable.Error()+": ")
}
