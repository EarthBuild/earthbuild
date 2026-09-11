package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// ErrNotDerivable says a job key cannot be computed for this build.
//
// Never a failure: every caller falls back to the plan fingerprint, which is
// conservative and always available. See docs-internals/job-skipping.md.
var ErrNotDerivable = errors.New("this build's inputs cannot be derived without running it")

// hostInput is one thing a build read from the host.
//
// The digest is the *entry's*, not the file's contents: a script that stopped
// being executable runs differently, and a key over contents alone would skip a
// build that now fails. `layer.Take` over one path gives exactly that - mode,
// kind, size, link target and content, without the mtime - which is the digest
// the rest of the engine already means by "what is at this path".
type hostInput struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Digest string `json:"digest,omitempty"`
}

const (
	inputFile    = "file"
	inputListing = "listing"
	inputAbsent  = "absent"
)

// gone is the digest of a host path that is not there.
//
// A value rather than an error, because absence is a difference and not a
// failure: a file the build read and the checkout no longer has must move the
// key, not stop the derivation. Distinct from the zero digest, which an empty
// file could plausibly reach.
var gone = ir.NodeID{'n', 'o', 't', '-', 'h', 'e', 'r', 'e'}

// hostInputsFrom is 𝑅: what the build read, expressed as paths in the checkout.
//
// **Only what a context placed becomes a host input.** A read of the base image
// is covered by the pinned digest in the shape; a read of an earlier step's
// output is a function of that step's own inputs, which are here by the same
// argument; a read of the step's own writes is a function of the step. What is
// left is the checkout, and that is what this returns.
//
// Every uncertainty refuses. The key is served with nothing to verify it
// afterwards, so where L2 can afford a hint this cannot - see the hard gates in
// docs-internals/job-skipping.md.
func hostInputsFrom(
	contexts map[string]bool, places []core.Placement, obs core.Observation, root string,
) ([]hostInput, error) {
	if obs.Incomplete {
		return nil, fmt.Errorf("%w: the tracer reported that it missed something", ErrNotDerivable)
	}

	// Longest destination first, so a copy nested inside another wins the path
	// it actually placed.
	from := make([]core.Placement, 0, len(places))

	for _, p := range places {
		if contexts[p.Layer] {
			from = append(from, p)
		}
	}

	sort.Slice(from, func(i, j int) bool { return len(from[i].To) > len(from[j].To) })

	var out []hostInput

	// **The digest the step saw is deliberately not used.** It is of the file
	// as the copy placed it, and a copy may have changed its mode or ownership;
	// the host's own digest is read here so that re-deriving needs nothing but
	// the checkout. What the copy did is in the shape.
	for at := range obs.Reads {
		host, ok := hostPathOf(from, at)
		if !ok {
			continue
		}

		out = append(out, hostInput{Path: host, Kind: inputFile, Digest: sealOf(root, host).String()})
	}

	for at := range obs.Listings {
		host, ok := hostPathOf(from, at)
		if !ok {
			continue
		}

		out = append(out, hostInput{Path: host, Kind: inputListing, Digest: listingOf(root, host).String()})
	}

	for _, at := range obs.Negative {
		host, ok := hostPathOf(from, at)
		if !ok {
			continue
		}

		out = append(out, hostInput{Path: host, Kind: inputAbsent, Digest: sealOf(root, host).String()})
	}

	// Sorted, because a map walk is not an order and this reaches a digest.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}

		return out[i].Kind < out[j].Kind
	})

	return out, nil
}

// hostPathOf rewrites a path inside a step's filesystem to the checkout path the
// copy took it from, or says it did not come from one.
func hostPathOf(places []core.Placement, at string) (string, bool) {
	clean := slashed(at)

	for _, p := range places {
		to := slashed(p.To)

		switch {
		case clean == to:
			return p.From, true

		case strings.HasPrefix(clean, to+"/"):
			return p.From + clean[len(to):], true
		}
	}

	return "", false
}

// slashed is a slash-separated absolute path with no trailing separator.
func slashed(p string) string {
	return strings.TrimSuffix(filepath.ToSlash(filepath.Clean("/"+p)), "/")
}

// sealOf is what the checkout holds at a path, or `gone`.
//
// `layer.Take` over a single path, so this agrees with every other answer the
// engine gives about what is at a path rather than being a second opinion.
func sealOf(root, rel string) ir.NodeID {
	at := filepath.Join(root, filepath.FromSlash(rel))

	_, err := os.Lstat(at)
	if err != nil {
		return gone
	}

	c, err := layer.Take(at)
	if err != nil {
		return gone
	}

	return c.Content
}

// listingOf is the names a directory holds, or `gone`.
func listingOf(root, rel string) ir.NodeID {
	id, err := layer.ListingDigestAt(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return gone
	}

	return id
}

// jobKey is Κ_job: the shape of the build and what it read, together.
func jobKey(shape ir.NodeID, inputs []hostInput) string {
	h := ir.NewHasher()

	h.Fixed(shape[:])
	h.Count(len(inputs))

	for _, in := range inputs {
		h.Str(in.Path)
		h.Str(in.Kind)
		h.Str(in.Digest)
	}

	return h.Sum().String()
}
