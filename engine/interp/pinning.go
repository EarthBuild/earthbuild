package interp

import "time"

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
