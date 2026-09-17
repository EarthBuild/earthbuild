package interp

import (
	"time"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// ResolveImage answers what a mutable reference names right now.
//
// It is given the reference as written and the platform being built for, and
// returns a reference that names content - `alpine@sha256:...`. The platform
// matters because a multi-platform tag names a different manifest per platform,
// and pinning the index rather than the image would leave the choice open.
type ResolveImage func(ref, platform string) (string, error)

// WithImageResolver supplies Θ (green paper §3.4d).
//
// **Absent does not refuse, unlike the other seams here.** `GIT CLONE` without a
// cloner is a construct the caller withheld and the refusal is the right answer;
// `FROM` is in every Earthfile, and a plan-only caller - `ls`, `doc`, corpus
// analysis - must produce a graph without reaching the network. So without a
// resolver a reference is left exactly as written.
//
// What that costs is I17, and the plan says so rather than hiding it: nothing
// appears in [Plan.Pinned], so an unpinned build cannot be mistaken for a pinned
// one by anything downstream. A reference left as written also keys as written,
// which is the I3 hole this exists to close - a tag that moves is then the same
// key over different content.
func WithImageResolver(fn ResolveImage) Option {
	return func(o *options) { o.resolveImage = fn }
}

// pin resolves a reference, once per build.
//
// **Once per reference, not once per use** (I17). Three targets on the same base
// ask three times and the registry is asked once; without the memo a tag that
// moved between two of those calls would put two different bases in one build,
// which is a divergence the Earthfile cannot express and nobody would look for.
//
// Memoised on the *pair*: the same tag on two platforms is two references to two
// manifests, and collapsing them would pin one platform's image for both.
//
// A resolver that fails leaves the reference as written. The alternative is to
// fail the plan, and that would make an unreachable registry the difference
// between a build that runs from cache and one that does not - the pinning is
// worth having and is not worth refusing a build over. The failure is not
// silent: an unpinned reference is absent from Plan.Pinned.
func (p *Plan) pin(ref, platform string) string {
	if p.opt.resolveImage == nil || ref == "" {
		return ref
	}

	key := ref + "\x00" + platform

	if to, ok := p.pinned[key]; ok {
		return to
	}

	// Timed because the answer is worth reporting: on a build with nothing else
	// to do this *is* the build, and a reader told "resolving these cost 0.41s
	// of 0.43s" acts on it where a reader told "consider --pin" does not
	// (E550). Measured here rather than in the resolver, because this is the
	// point that knows a round trip actually happened - the memo above means
	// three uses of one tag cost one lookup, and reporting three would be
	// reporting the Earthfile rather than the network.
	started := time.Now()
	to, err := p.opt.resolveImage(ref, platform)
	p.PinCost += time.Since(started)

	if err != nil || to == "" {
		return ref
	}

	if p.pinned == nil {
		p.pinned = map[string]string{}
	}

	p.pinned[key] = to

	if p.Pinned == nil {
		p.Pinned = map[string]string{}
	}

	p.Pinned[ref] = to

	return to
}

// ResolveHelper answers what a cache helper's module actually is.
//
// It is given the reference as the author wrote it - `./go.wasm` - and the
// directory of the Earthfile that wrote it, and returns a digest naming the
// module's bytes.
//
// **Both, because a path in an Earthfile means that Earthfile's directory.**
// `unit.dir` says so of every other relative reference, and resolving a helper
// against the *invocation's* directory instead made `--helper ./h.wasm` in
// `examples/npm/Earthfile` name a file at the repository root - which is how
// every example here is built (`BUILD ./examples/x+y`), so the construct was
// unusable in the place it is meant to be shown off.
//
// **Resolving is expected to file the module somewhere both ends can read**,
// which is the one way this differs from [ResolveImage]. A pinned image
// reference is a name a registry will answer for; a pinned helper is a name only
// this machine can answer for until somebody puts the bytes in 𝔅. The seam
// returns a digest and says nothing about where it went, because the caller that
// resolved it is the caller that owns the store.
type ResolveHelper func(ref, dir string) (string, error)

// WithHelperResolver pins the program that reads a portable cache.
//
// **A path is a name and not an identity.** A helper decides what a unit is,
// what it is called and what bytes are inside each frame, so two machines
// running different helpers over one cache produce units that are not the same
// units. Κ₁ hashed the path, which two machines can hold identically over
// different bytes, so the agreement it was enforcing was an agreement about
// spelling.
//
// Absent leaves the reference as written and claims no pin, exactly as
// [WithImageResolver] does and for the same reason: `ls`, `doc` and corpus
// analysis must produce a graph without reading anything, and a coarser key is a
// better failure than a refused build.
func WithHelperResolver(fn ResolveHelper) Option {
	return func(o *options) { o.resolveHelper = fn }
}

// pinHelper resolves a helper reference, once per build.
//
// Memoised on the reference *and* the directory it was written in. A helper is
// one module and runs the same everywhere - which is why there is no platform in
// this key, where [Plan.pin] needs one - but two Earthfiles may each say
// `./h.wasm` and mean different files.
//
// A resolver that fails leaves the mount unpinned rather than failing the build.
// A cache that cannot be shared is a slower build on some other machine; a
// refused step is no build at all, and the pinning is not worth that (I11).
func (p *Plan) pinHelper(ref, dir string) string {
	if p.opt.resolveHelper == nil || ref == "" {
		return ""
	}

	// Memoised on the pair, because the same spelling in two Earthfiles is two
	// different files - which is the whole point of resolving against the
	// Earthfile's own directory, and would be undone by a memo that ignored it.
	key := dir + "\x00" + ref
	if to, ok := p.pinnedHelpers[key]; ok {
		return to
	}

	started := time.Now()
	to, err := p.opt.resolveHelper(ref, dir)
	p.PinCost += time.Since(started)

	if err != nil || to == "" {
		return ""
	}

	if p.pinnedHelpers == nil {
		p.pinnedHelpers = map[string]string{}
	}

	p.pinnedHelpers[key] = to

	// **Not recorded in [Plan.Pinned]**, which is Θ's record and carries advice
	// with it: `recordPinning` tells the reader that `--pin` writes these into
	// the Earthfile, which is true of an image reference and nonsense for a
	// path on disk. The mount carries the digest, so the provenance is already
	// where anything asking the question would look.
	return to
}

// pinHelpers resolves every cache helper these mounts name, in place.
//
// Called where a step's mounts are assembled rather than where a flag is
// parsed: `CACHE --helper` and `RUN --mount=...,helper=` are two parsers over
// one idea, and pinning in each would let two paths of one build resolve the
// same reference twice - which is the divergence [Plan.pin]'s memo exists to
// make impossible for images.
func (p *Plan) pinHelpers(ms []ir.Mount, dir string) {
	if p.opt.resolveHelper == nil {
		return
	}

	for i := range ms {
		ms[i].HelperID = p.pinHelper(ms[i].Helper, dir)
	}
}
