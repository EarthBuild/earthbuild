package cli

import (
	"context"
	"sync"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// puller fetches an image so that something later does not have to wait for it.
type pullFunc func(ctx context.Context, ref string) error

// prefetch fetches what confidently-predicted branches needed last time, and
// returns a function that waits for it.
//
// The freely-speculable tier put to work. An image pull is network-bound and
// nothing else in a build proceeds during it, so moving those bytes before the
// condition that selects the branch has even been run takes the transfer off
// the critical path. Being wrong costs bandwidth; being right costs nothing at
// all.
//
// It returns rather than waits, which is the entire point and was not true at
// first: waiting for the pulls before interpreting anything put them at the
// *head* of the critical path, serialised, which is strictly worse than not
// prefetching - a wrong prediction was then paid for in full before the build
// had started. Concurrency with the build is safe because the image cache
// stages a pull to one side and renames it into place, and losing that race is
// already handled.
//
// The waiter is not optional bookkeeping: a build that has returned must not
// leave pulls running against a cache directory it has stopped using.
//
// Nothing here can fail a build. A prefetch is a hint (green paper I5) and the
// image will be pulled properly when something actually needs it - so an error
// is dropped rather than reported, and a prefetch that could fail a build would
// make a hint load-bearing.
func prefetch(ctx context.Context, learned *core.Predictions, pull pullFunc) func() {
	if learned == nil || pull == nil {
		return func() {}
	}

	// Deduplicated: several sites commonly predict the same base image, and
	// pulling it four times concurrently is worse than not prefetching at all.
	wanted := map[string]bool{}

	for _, site := range learned.Sites() {
		branch, confident := learned.Predict(site)
		if !confident {
			continue
		}

		for _, ref := range learned.Needs(site, branch) {
			wanted[ref] = true
		}
	}

	var wg sync.WaitGroup

	for ref := range wanted {
		wg.Go(func() {
			_ = pull(ctx, ref)
		})
	}

	return wg.Wait
}

// recordNeeds attributes a build's images to the conditions it evaluated.
//
// Every site the build decided gets the whole build's image list against the
// branch it took. Over-inclusive on purpose: attributing images to a branch
// exactly would need the interpreter to track which nodes came from which
// subtree, which it has no other reason to do - and being wrong in this
// direction costs bandwidth, which is the whole reason this tier is free.
func recordNeeds(learned *core.Predictions, decided map[string]bool, refs []string) {
	if learned == nil || len(decided) == 0 || len(refs) == 0 {
		return
	}

	for site, branch := range decided {
		learned.Needed(site, branch, refs)
	}
}

// imageRefs is every image a plan names.
func imageRefs(plan *interp.Plan) []string {
	var out []string

	seen := map[string]bool{}

	for _, n := range plan.Graph.Nodes() {
		if n.Op.Kind != ir.OpImage || len(n.Op.Args) == 0 {
			continue
		}

		if ref := n.Op.Args[0]; !seen[ref] {
			out = append(out, ref)
			seen[ref] = true
		}
	}

	return out
}

// intoImageCache pulls a reference into the shared image cache.
//
// The prefetch has somewhere to put bytes only because the cache is keyed by
// reference and platform: fetched before the graph exists, an image has no node
// identity to be filed under, and whichever step turns out to need it looks in
// the same place.
func intoImageCache(root, platform string) pullFunc {
	return func(ctx context.Context, ref string) error {
		return exec.Prefetch(ctx, root, ref, platform,
			func(ctx context.Context, ref, dir string) (ocispec.ImageConfig, error) {
				return image.Pull(ctx, ref, dir, image.Options{Platform: platform})
			})
	}
}
