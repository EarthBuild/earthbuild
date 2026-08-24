package core

import (
	"fmt"
	"slices"
	"sort"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// DeriveObservedKey computes Κ₂, green paper (4.6):
//
//	Κ₂(s, 𝑟) ≡ ℋ(0x02 ‖ sort(𝑅) ‖ sort(𝑁) ‖ sort(𝐷) ‖ 𝒮(ω) ‖ 𝒮(ε) ‖ 𝒮(π))
//
// Where Κ₁ keys on the whole base, Κ₂ keys on what the step actually looked at.
// Two steps over *different* bases share this key when they touched nothing
// that differs - which is the point, and why a base-image bump need not
// invalidate steps that could not observe it.
//
// 𝑁 and 𝐷 are not refinements of 𝑅. A step evaluating `if [ -f /x ]` reads
// nothing, so a key over 𝑅 alone would let it hit against a base where /x
// exists. That is invariant I3 violated: a false cache hit, the one failure a
// build system must never have.
func DeriveObservedKey(n *ir.Node, refs []ir.NodeID, obs Observation) Key {
	h := ir.NewHasher()

	h.Byte(domainObserved)

	// sort(𝑅): paths read, with what was read. Sorted because map order must
	// not reach a key.
	reads := make([]string, 0, len(obs.Reads))
	for p := range obs.Reads {
		reads = append(reads, p)
	}

	sort.Strings(reads)
	h.Count(len(reads))

	for _, p := range reads {
		h.Str(p)

		d := obs.Reads[p]
		h.Fixed(d[:])
	}

	// sort(𝑁): lookups that found nothing.
	//
	// A *set*, which the field's slice type does not enforce. A real source
	// repeats: `cc -I/a -I/b -I/c` stats the same absent header once per
	// directory, and `command -v` walks PATH. Hashing the repeat would make the
	// key depend on the source's buffering rather than on what the step
	// observed - so two runs of one build could key differently, and at S6 two
	// engines observing identically would never share a hit.
	neg := uniqueSorted(obs.Negative)
	h.Count(len(neg))

	for _, p := range neg {
		h.Str(p)
	}

	// sort(𝐷): directories listed, with the digest of each listing. A listing
	// digest subsumes every negative lookup inside that directory, which is
	// what keeps 𝑁 small when a compiler probes twenty include paths.
	dirs := make([]string, 0, len(obs.Listings))
	for p := range obs.Listings {
		dirs = append(dirs, p)
	}

	sort.Strings(dirs)
	h.Count(len(dirs))

	for _, p := range dirs {
		h.Str(p)

		d := obs.Listings[p]
		h.Fixed(d[:])
	}

	// 𝒮(ω) ‖ 𝒮(ε) ‖ 𝒮(π) - the same serialisation Κ₁ uses, because the
	// operation and its ambient state are as much part of identity as what it
	// read. The comment here said "identical to Κ₁" and the code hashed the
	// kind, the arguments and the environment: nine fields short, including
	// `User` and `Dir` (E113).
	hashOperation(h, n, refs)
	hashEnvAndPlatform(h, n)

	return h.Sum()
}

// StepClass identifies a step independently of what it runs over: the operation,
// its ambient state and its platform, with no inputs.
//
// This is the key profiles are stored under, and the choice is load-bearing. A
// key including the inputs - node identity - changes the moment the base image
// changes, so the profile would be missing in exactly the situation L2 exists
// to handle. The class is what stays put while the base moves.
//
// Predicting from a class is safe because a prediction is never trusted: it is
// checked against the current base (4.7) before any entry derived from it is
// used. A wrong prediction costs a failed check, never a wrong result.
func StepClass(n *ir.Node) Key {
	h := ir.NewHasher()

	h.Byte(domainClass)

	// The same 𝒮(ω) the two keys use, with nil refs: a class is a *prediction*
	// key and one that changed whenever a source file changed would predict for
	// nothing. This hashed the kind, the arguments and the environment before
	// (E113), so `RUN --user root` and `RUN --user build` shared a profile and
	// each predicted the other's reads.
	hashOperation(h, n, nil)
	hashEnvAndPlatform(h, n)

	return h.Sum()
}

// componentDigests derives the four parts of a chain key separately, so that a
// divergence can be attributed to one of them rather than merely detected.
//
// They are computed with the same domain-separated, injective encoding as the
// keys themselves; the point is that Base ‖ Op ‖ Env ‖ Plat determines the
// chain key, so two records agreeing on all four must agree on it.
func componentDigests(n *ir.Node, base []ir.NodeID) (bd, od, ed, pd ir.NodeID) {
	h := ir.NewHasher()
	h.Byte(domainComponent)
	h.Count(len(base))

	for _, id := range base {
		h.Fixed(id[:])
	}

	bd = h.Sum()

	h = ir.NewHasher()
	h.Byte(domainComponent)
	h.Byte(byte(n.Op.Kind))
	h.Count(len(n.Op.Args))

	for _, a := range n.Op.Args {
		h.Str(a)
	}

	od = h.Sum()

	h = ir.NewHasher()
	h.Byte(domainComponent)

	keys := make([]string, 0, len(n.Op.Env))
	for k := range n.Op.Env {
		keys = append(keys, k)
	}

	sort.Strings(keys)
	h.Count(len(keys))

	for _, k := range keys {
		h.Str(k)
		h.Str(n.Op.Env[k])
	}

	ed = h.Sum()

	h = ir.NewHasher()
	h.Byte(domainComponent)
	h.Str(n.Platform.OS)
	h.Str(n.Platform.Arch)
	h.Str(n.Platform.Variant)

	pd = h.Sum()

	return bd, od, ed, pd
}

// BaseView answers questions about a materialised base without reading it.
//
// It is what makes a prediction checkable: verifying that a recorded
// observation still holds touches only the paths the observation names, so the
// cost is proportional to the prediction rather than to the tree.
type BaseView interface {
	// Digest returns the content digest of a path, and whether it exists.
	Digest(path string) (ir.NodeID, bool)
	// ListingDigest returns the digest of a directory's listing, and whether
	// the directory exists.
	ListingDigest(dir string) (ir.NodeID, bool)
}

// WhyStale names the first way a prediction no longer describes a base, or
// empty when it still does.
//
// `Consistent` answers yes or no, and a build whose L2 never hits then reports
// `1 of 3 predictions stale` - a count without a cause. It says the tier is
// being invalidated and not by what, and the only way forward is to guess and
// measure. That is what it cost when the tier went live: every copy's
// prediction was stale because the walk recorded `/`, whose digest carries mode
// and extended attributes that differ between two base images (E125), and the
// engine knew the path at the moment it refused.
//
// The same shape as first-divergence reporting for chain keys (B.4). A
// prediction is a claim about inputs and deserves the same answer.
//
// **Deterministic**: the paths are sorted, because a reason that names a
// different path on each run is one nobody can quote in a bug report - and map
// iteration order is the classic way to produce one.
func WhyStale(obs Observation, base BaseView) string {
	for _, path := range sortedKeys(obs.Reads) {
		got, ok := base.Digest(path)
		if !ok {
			return path + " is gone from the base"
		}

		if want := obs.Reads[path]; got != want {
			// Both digests, not only the path.
			//
			// The path alone answers "what invalidated the tier" and leaves
			// "which side is wrong" - what the step observed, or what the base
			// holds now - and that is the question every investigation of this
			// asks next. Five hypotheses were eliminated by hand before these
			// two numbers were printed, and every one of them would have been
			// answered in a second by having them (E493).
			//
			// Short forms: this goes in a one-line summary beside a build's
			// steps, and two full digests is 128 characters of a line nobody
			// then reads. Twelve is enough to tell two apart and to grep the
			// store for either.
			return fmt.Sprintf("%s changed in the base (observed %s, base has %s)",
				path, short(want), short(got))
		}
	}

	for _, path := range uniqueSorted(obs.Negative) {
		if _, exists := base.Digest(path); exists {
			return path + " exists in the base, and the step ran when it did not"
		}
	}

	for _, dir := range sortedKeys(obs.Listings) {
		got, ok := base.ListingDigest(dir)
		if !ok {
			return dir + " is gone from the base"
		}

		if got != obs.Listings[dir] {
			return dir + " holds different names in the base"
		}
	}

	return ""
}

// sortedKeys is a map's keys in order, so a message about them is the same
// every run.
func sortedKeys(m map[string]ir.NodeID) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
}

// Consistent reports whether a predicted observation set still describes the
// base, green paper (4.7):
//
//	consistent(𝑟̂, 𝑏) ⟹ Κ₂(s, 𝑟̂) is the key this step would produce
//
// Every path in 𝑅̂ must resolve to the digest recorded, every path in 𝑁̂ must
// still be absent, and every listing in 𝐷̂ must still hash as recorded.
//
// The middle condition is the one that is easy to omit and fatal to omit. A
// prediction that a file was *absent* is a claim about the base exactly as much
// as a claim about what was read, and a check that skips it will happily reuse
// a result computed when the file did not exist.
func Consistent(obs Observation, base BaseView) bool {
	return WhyStale(obs, base) == ""
}

// uniqueSorted is a slice as the set it represents: sorted, without repeats.
//
// Deduplicating must not be able to degenerate into discarding - a derivation
// that dropped 𝑁 entirely would satisfy "repeats do not change the key" and
// destroy I3, so `TestADistinctNegativeLookupStillChangesTheKey` sits beside the
// test this exists for.
func uniqueSorted(in []string) []string {
	if len(in) < 2 {
		return in
	}

	out := append([]string(nil), in...)
	sort.Strings(out)

	return slices.Compact(out)
}

// short is a digest at the length a summary line can carry.
//
// Long enough to distinguish two digests and to find either in a store; short
// enough that a reason naming two of them is still a line rather than a
// paragraph.
func short(id ir.NodeID) string {
	const enough = 12

	s := id.String()
	if len(s) <= enough {
		return s
	}

	return s[:enough]
}
